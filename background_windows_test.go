//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplaceBackupFileSuccessAndFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "current.zip")
	source := filepath.Join(dir, "next.tmp")
	if err := os.WriteFile(target, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := replaceBackupFile(source, target); err == nil {
		t.Fatal("missing source unexpectedly succeeded")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "previous" {
		t.Fatalf("previous backup changed: %q %v", data, err)
	}
	if err := os.WriteFile(source, []byte("next"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := replaceBackupFile(source, target); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "next" {
		t.Fatalf("new backup missing: %q %v", data, err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source retained: %v", err)
	}
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := replaceBackupFile(source, target); err == nil {
		t.Fatal("directory replaced regular backup")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "next" {
		t.Fatalf("failure damaged backup: %q %v", data, err)
	}
}

func TestStartupHelpersDoNotChangeRegistry(t *testing.T) {
	exe := func() (string, error) { return `C:\测试 空格\Minecraft地图备份.exe`, nil }
	expected := `"C:\测试 空格\Minecraft地图备份.exe" --background`
	var stored string
	removed := false
	write := func(command string) error { stored = command; return nil }
	remove := func() error { removed = true; return nil }
	if err := setStartupEnabledWith(true, exe, write, remove); err != nil {
		t.Fatal(err)
	}
	if stored != expected || removed {
		t.Fatalf("incorrect own startup command: %q removed=%t", stored, removed)
	}
	read := func() (string, bool, error) { return strings.ToUpper(stored), true, nil }
	if enabled, err := startupEnabledWith(read, exe); err != nil || !enabled {
		t.Fatalf("enabled=%t err=%v", enabled, err)
	}
	other := func() (string, bool, error) { return `"C:\old.exe" --background`, true, nil }
	if enabled, err := startupEnabledWith(other, exe); err != nil || enabled {
		t.Fatalf("stale command enabled=%t err=%v", enabled, err)
	}
	missing := func() (string, bool, error) { return "", false, nil }
	if enabled, err := startupEnabledWith(missing, exe); err != nil || enabled {
		t.Fatalf("missing command enabled=%t err=%v", enabled, err)
	}
	if err := setStartupEnabledWith(false, func() (string, error) { t.Fatal("disable resolved executable"); return "", nil }, write, remove); err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("disable did not delete own value")
	}
	want := errors.New("registry denied")
	if err := setStartupEnabledWith(true, exe, func(string) error { return want }, remove); !errors.Is(err, want) {
		t.Fatalf("write error hidden: %v", err)
	}
	if enabled, err := startupEnabledWith(func() (string, bool, error) { return "", false, want }, exe); enabled || !errors.Is(err, want) {
		t.Fatalf("read error hidden: %t %v", enabled, err)
	}
}

func TestJavaProbePreservesUnknownState(t *testing.T) {
	for _, tc := range []struct {
		name, data       string
		running, unknown bool
	}{
		{"empty", `[]`, false, false},
		{"other java", `[{"ProcessId":1,"CommandLine":"java.exe -jar server-tools.jar"}]`, false, false},
		{"minecraft", `[{"ProcessId":1,"CommandLine":"javaw.exe net.minecraft.client.main.Main"}]`, true, false},
		{"unreadable", `[{"ProcessId":1,"CommandLine":null}]`, false, true},
		{"blank", `[{"ProcessId":1,"CommandLine":""}]`, false, true},
		{"invalid", `not json`, false, true},
		{"null", `null`, false, true},
		{"known running overrides unreadable", `[{"ProcessId":1,"CommandLine":null},{"ProcessId":2,"CommandLine":"java.exe net.minecraft.client.main.Main"}]`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			running, err := parseJavaMinecraftProcesses([]byte(tc.data))
			if running != tc.running || (err != nil) != tc.unknown {
				t.Fatalf("running=%t err=%v", running, err)
			}
		})
	}
}

func TestNativeMinecraftSeparatesStoreLauncher(t *testing.T) {
	for _, tc := range []struct {
		name, exe, path  string
		pathErr          error
		running, unknown bool
	}{
		{"bedrock", "Minecraft.Windows.exe", "", nil, true, false},
		{"store launcher", "Minecraft.exe", `C:\Program Files\WindowsApps\Microsoft.4297127D64EC6_2.6.2.0_x64__8wekyb3d8bbwe\Minecraft.exe`, nil, false, false},
		{"store launcher case insensitive", "MINECRAFT.EXE", `C:/Program Files/WindowsApps/MICROSOFT.4297127D64EC6_2.7.0.0_x64__8wekyb3d8bbwe/Minecraft.exe`, nil, false, false},
		{"other native game", "Minecraft.exe", `C:\Games\Minecraft\Minecraft.exe`, nil, true, false},
		{"unverified package", "Minecraft.exe", `C:\Program Files\WindowsApps\Another.Package_1\Minecraft.exe`, nil, true, false},
		{"unreadable", "Minecraft.exe", "", errors.New("access denied"), false, true},
		{"empty path", "Minecraft.exe", "", nil, false, true},
		{"named launcher", "MinecraftLauncher.exe", "", nil, false, false},
		{"unrelated", "java.exe", "", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			running, err := nativeMinecraftProcess(tc.exe, 123, func(uint32) (string, error) { return tc.path, tc.pathErr })
			if running != tc.running || (err != nil) != tc.unknown {
				t.Fatalf("running=%t err=%v", running, err)
			}
		})
	}
}

func TestProcessImagePathCurrentProcess(t *testing.T) {
	got, err := processImagePath(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(got, want) {
		t.Fatalf("image path %q != executable %q", got, want)
	}
}

func TestJavaMinecraftClientEntryPoints(t *testing.T) {
	for _, tc := range []struct {
		name, command    string
		running, unknown bool
	}{
		{"vanilla", `"C:\Minecraft\runtime\javaw.exe" -Xmx4G -cp "C:\Minecraft\libraries\*" net.minecraft.client.main.Main --gameDir "C:\Minecraft"`, true, false},
		{"hmcl", `javaw.exe -jar "D:\Minecraft\HMCL.jar"`, false, false},
		{"pcl jar", `javaw.exe -jar "D:\Minecraft\PCL.jar"`, false, false},
		{"unrelated minecraft directory", `java.exe -jar D:\Minecraft\tools.jar`, false, false},
		{"java launcher main", `javaw.exe -cp D:\Minecraft\launcher.jar org.jackhuang.hmcl.Launcher`, false, false},
		{"old minecraft launcher", `javaw.exe -cp launcher.jar net.minecraft.launcher.Main`, false, false},
		{"classpath not main", `javaw.exe -cp net.minecraft.client.main.Main org.example.Tools`, false, false},
		{"minecraft named argument", `javaw.exe org.example.Tools net.minecraft.client.main.Main`, false, false},
		{"fabric", `javaw.exe -Dfabric.development=false net.fabricmc.loader.impl.launch.knot.KnotClient --gameDir .minecraft`, true, false},
		{"old fabric", `javaw.exe net.fabricmc.loader.launch.knot.KnotClient`, true, false},
		{"quilt", `javaw.exe org.quiltmc.loader.impl.launch.knot.KnotClient`, true, false},
		{"prism game wrapper", `javaw.exe org.prismlauncher.EntryPoint`, true, false},
		{"forge", `javaw.exe -cp libraries/* cpw.mods.modlauncher.Launcher --launchTarget forgeclient`, true, false},
		{"neoforge", `javaw.exe cpw.mods.bootstraplauncher.BootstrapLauncher --launchTarget=neoforgeclient`, true, false},
		{"forge server", `javaw.exe cpw.mods.modlauncher.Launcher --launchTarget forgeserver`, false, false},
		{"old forge", `javaw.exe net.minecraft.launchwrapper.Launch --tweakClass net.minecraftforge.fml.common.launcher.FMLTweaker`, true, false},
		{"old forge server", `javaw.exe net.minecraft.launchwrapper.Launch --tweakClass net.minecraftforge.fml.common.launcher.FMLServerTweaker`, false, false},
		{"vanilla server", `java.exe net.minecraft.server.Main`, false, false},
		{"argfile unknown", `javaw.exe @C:\Minecraft\args.txt`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			running, err := javaMinecraftClientCommand(tc.command)
			if running != tc.running || (err != nil) != tc.unknown {
				t.Fatalf("running=%t err=%v", running, err)
			}
		})
	}
}
