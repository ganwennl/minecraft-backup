//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	startupRegistryKey   = `Software\Microsoft\Windows\CurrentVersion\Run`
	startupRegistryValue = "MinecraftWorldBackup"
)

var (
	backgroundKernel32    = syscall.NewLazyDLL("kernel32.dll")
	backgroundAdvapi32    = syscall.NewLazyDLL("advapi32.dll")
	moveBackupFile        = backgroundKernel32.NewProc("MoveFileExW")
	regOpenStartupKey     = backgroundAdvapi32.NewProc("RegOpenKeyExW")
	regCreateStartupKey   = backgroundAdvapi32.NewProc("RegCreateKeyExW")
	regQueryStartupValue  = backgroundAdvapi32.NewProc("RegQueryValueExW")
	regSetStartupValue    = backgroundAdvapi32.NewProc("RegSetValueExW")
	regDeleteStartupValue = backgroundAdvapi32.NewProc("RegDeleteValueW")
	regCloseStartupKey    = backgroundAdvapi32.NewProc("RegCloseKey")
)

// MoveFileExW leaves the previous backup in place when replacing it fails.
// The caller writes a separate temporary file first, on the same volume.
func replaceBackupFile(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("临时备份不是普通文件")
	}
	if existing, err := os.Lstat(target); err == nil {
		if !existing.Mode().IsRegular() {
			return errors.New("备份目标不是普通文件")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	from, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	ok, _, callErr := moveBackupFile.Call(uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(to)), 0x1|0x8)
	if ok == 0 {
		return fmt.Errorf("替换备份失败：%w", callErr)
	}
	return nil
}

func startupCommand(executable func() (string, error)) (string, error) {
	path, err := executable()
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(path, "\x00\"") {
		return "", errors.New("程序路径无效")
	}
	return `"` + path + `" --background`, nil
}

func startupEnabledWith(read func() (string, bool, error), executable func() (string, error)) (bool, error) {
	value, exists, err := read()
	if err != nil || !exists {
		return false, err
	}
	expected, err := startupCommand(executable)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(value), expected), nil
}

func setStartupEnabledWith(enabled bool, executable func() (string, error), write func(string) error, remove func() error) error {
	if !enabled {
		return remove()
	}
	command, err := startupCommand(executable)
	if err != nil {
		return err
	}
	return write(command)
}

func startupEnabled() (bool, error) {
	return startupEnabledWith(readStartupRegistryValue, os.Executable)
}

func setStartupEnabled(enabled bool) error {
	return setStartupEnabledWith(enabled, os.Executable, writeStartupRegistryValue, deleteStartupRegistryValue)
}

func openStartupRegistryKey(access uintptr, create bool) (uintptr, error) {
	keyName, _ := syscall.UTF16PtrFromString(startupRegistryKey)
	var key uintptr
	var status uintptr
	if create {
		status, _, _ = regCreateStartupKey.Call(0x80000001, uintptr(unsafe.Pointer(keyName)), 0, 0, 0, access, 0, uintptr(unsafe.Pointer(&key)), 0)
	} else {
		status, _, _ = regOpenStartupKey.Call(0x80000001, uintptr(unsafe.Pointer(keyName)), 0, access, uintptr(unsafe.Pointer(&key)))
	}
	if status != 0 {
		return 0, syscall.Errno(status)
	}
	return key, nil
}

func readStartupRegistryValue() (string, bool, error) {
	key, err := openStartupRegistryKey(0x1, false)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer regCloseStartupKey.Call(key)
	name, _ := syscall.UTF16PtrFromString(startupRegistryValue)
	var kind, size uint32
	status, _, _ := regQueryStartupValue.Call(key, uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&kind)), 0, uintptr(unsafe.Pointer(&size)))
	if status == uintptr(syscall.ERROR_FILE_NOT_FOUND) {
		return "", false, nil
	}
	if status != 0 {
		return "", false, syscall.Errno(status)
	}
	if kind != 1 || size < 2 || size%2 != 0 || size > 65536 {
		return "", false, errors.New("开机启动项格式无效")
	}
	data := make([]uint16, size/2)
	status, _, _ = regQueryStartupValue.Call(key, uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&kind)), uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&size)))
	if status != 0 {
		return "", false, syscall.Errno(status)
	}
	if kind != 1 || size < 2 || size%2 != 0 || int(size/2) > len(data) || data[size/2-1] != 0 {
		return "", false, errors.New("开机启动项内容无效")
	}
	data = data[:size/2-1]
	for _, ch := range data {
		if ch == 0 {
			return "", false, errors.New("开机启动项包含无效字符")
		}
	}
	return syscall.UTF16ToString(data), true, nil
}

func writeStartupRegistryValue(command string) error {
	key, err := openStartupRegistryKey(0x2, true)
	if err != nil {
		return err
	}
	defer regCloseStartupKey.Call(key)
	name, _ := syscall.UTF16PtrFromString(startupRegistryValue)
	data, err := syscall.UTF16FromString(command)
	if err != nil {
		return err
	}
	status, _, _ := regSetStartupValue.Call(key, uintptr(unsafe.Pointer(name)), 0, 1, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2))
	if status != 0 {
		return syscall.Errno(status)
	}
	return nil
}

func deleteStartupRegistryValue() error {
	key, err := openStartupRegistryKey(0x2, false)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	if err != nil {
		return err
	}
	defer regCloseStartupKey.Call(key)
	name, _ := syscall.UTF16PtrFromString(startupRegistryValue)
	status, _, _ := regDeleteStartupValue.Call(key, uintptr(unsafe.Pointer(name)))
	if status != 0 && status != uintptr(syscall.ERROR_FILE_NOT_FOUND) {
		return syscall.Errno(status)
	}
	return nil
}

type backgroundProcessEntry struct {
	Size, Usage, ProcessID             uint32
	DefaultHeapID                      uintptr
	ModuleID, Threads, ParentProcessID uint32
	Priority                           int32
	Flags                              uint32
	ExeFile                            [260]uint16
}

// Native enumeration is cheap and needs no helper process. CIM is used only when
// java/javaw exists, since its command line is needed to distinguish the game.
// Errors remain unknown rather than reporting that a game has exited.
func probeMinecraftRunning() (bool, error) {
	snapshot, _, callErr := backgroundKernel32.NewProc("CreateToolhelp32Snapshot").Call(0x2, 0)
	if snapshot == ^uintptr(0) {
		return false, fmt.Errorf("读取进程列表失败：%w", callErr)
	}
	defer syscall.CloseHandle(syscall.Handle(snapshot))
	entry := backgroundProcessEntry{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	first := backgroundKernel32.NewProc("Process32FirstW")
	next := backgroundKernel32.NewProc("Process32NextW")
	ok, _, enumErr := first.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	var javaIDs []uint32
	var unknown []error
	for ok != 0 {
		name := strings.ToLower(syscall.UTF16ToString(entry.ExeFile[:]))
		running, err := nativeMinecraftProcess(name, entry.ProcessID, processImagePath)
		if running {
			return true, nil
		}
		if err != nil {
			unknown = append(unknown, err)
		}
		if name == "java.exe" || name == "javaw.exe" {
			javaIDs = append(javaIDs, entry.ProcessID)
		}
		ok, _, enumErr = next.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	}
	if enumErr != syscall.ERROR_NO_MORE_FILES {
		return false, fmt.Errorf("读取进程列表失败：%w", enumErr)
	}
	if len(javaIDs) == 0 {
		return false, errors.Join(unknown...)
	}
	running, err := probeJavaMinecraft(javaIDs)
	if running {
		return true, nil
	}
	unknown = append(unknown, err)
	return false, errors.Join(unknown...)
}

func processImagePath(pid uint32) (string, error) {
	process, err := syscall.OpenProcess(0x1000, false, pid) // PROCESS_QUERY_LIMITED_INFORMATION
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(process)
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	ok, _, callErr := backgroundKernel32.NewProc("QueryFullProcessImageNameW").Call(uintptr(process), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		return "", callErr
	}
	if size == 0 || size > uint32(len(buf)) {
		return "", errors.New("进程路径无效")
	}
	return syscall.UTF16ToString(buf[:size]), nil
}

func nativeMinecraftProcess(name string, pid uint32, imagePath func(uint32) (string, error)) (bool, error) {
	switch strings.ToLower(name) {
	case "minecraft.windows.exe":
		return true, nil
	case "minecraft.exe":
		path, err := imagePath(pid)
		if err != nil {
			return false, fmt.Errorf("无法确认 Minecraft 进程类型：%w", err)
		}
		parts := strings.Split(strings.ToLower(strings.ReplaceAll(path, "/", `\`)), `\`)
		// The current Microsoft Store Launcher is also named Minecraft.exe.
		// Exclude only its confirmed package, rather than arbitrary folder names.
		if len(parts) >= 3 && parts[len(parts)-3] == "windowsapps" && strings.HasPrefix(parts[len(parts)-2], "microsoft.4297127d64ec6_") && parts[len(parts)-1] == "minecraft.exe" {
			return false, nil
		}
		if strings.TrimSpace(path) == "" {
			return false, errors.New("Minecraft 进程路径未知")
		}
		return true, nil
	default:
		return false, nil
	}
}

func probeJavaMinecraft(ids []uint32) (bool, error) {
	filters := make([]string, len(ids))
	for i, id := range ids {
		filters[i] = "ProcessId = " + strconv.FormatUint(uint64(id), 10)
	}
	// IDs come only from the native process snapshot; no paths or user input are
	// interpolated into this fixed query. Empty/null command lines are unknown.
	script := `$ErrorActionPreference='Stop'; [Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false); ConvertTo-Json -Compress -InputObject @(Get-CimInstance Win32_Process -Filter '` + strings.Join(filters, " OR ") + `' | Where-Object { $_.Name -in @('java.exe','javaw.exe') } | Select-Object ProcessId,CommandLine)`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	configureCommand(cmd)
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("检测 Java 游戏进程失败：%w", err)
	}
	return parseJavaMinecraftProcesses(out)
}

func parseJavaMinecraftProcesses(out []byte) (bool, error) {
	var processes []struct {
		ProcessID   uint32
		CommandLine *string
	}
	if err := json.Unmarshal(out, &processes); err != nil {
		return false, fmt.Errorf("读取 Java 进程信息失败：%w", err)
	}
	if processes == nil {
		return false, errors.New("Java 进程检测结果无效")
	}
	unknown := false
	for _, process := range processes {
		if process.CommandLine == nil || strings.TrimSpace(*process.CommandLine) == "" {
			unknown = true
			continue
		}
		running, err := javaMinecraftClientCommand(*process.CommandLine)
		if running {
			return true, nil
		}
		if err != nil {
			unknown = true
		}
	}
	if unknown {
		return false, errors.New("无法读取 Java 进程命令行，游戏状态未知")
	}
	return false, nil
}

func javaMinecraftClientCommand(command string) (bool, error) {
	line, err := syscall.UTF16PtrFromString(command)
	if err != nil {
		return false, err
	}
	var count int32
	argv, err := syscall.CommandLineToArgv(line, &count)
	if err != nil {
		return false, err
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(argv)))
	if count < 2 || count > 8192 {
		return false, errors.New("Java 启动参数无效")
	}
	args := make([]string, count)
	for i := range args {
		args[i] = syscall.UTF16ToString(argv[i][:])
	}
	// Find the actual Java entry point, skipping options that consume a value.
	// Minecraft in a launcher/JDK/classpath directory alone is never evidence.
	main := ""
	mainIndex := 0
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "@") {
			return false, errors.New("Java 使用参数文件，游戏状态未知")
		}
		if arg == "-jar" {
			return false, nil
		} // HMCL and other Java launchers use -jar.
		switch arg {
		case "-cp", "-classpath", "--class-path", "-p", "--module-path", "--upgrade-module-path", "--add-modules", "--limit-modules", "--patch-module", "--add-reads", "--add-exports", "--add-opens", "--enable-native-access", "--source":
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		main, mainIndex = arg, i
		break
	}
	switch main {
	case "net.minecraft.client.main.Main", "net.minecraft.client.Minecraft",
		"net.fabricmc.loader.impl.launch.knot.KnotClient", "net.fabricmc.loader.launch.knot.KnotClient",
		"org.quiltmc.loader.impl.launch.knot.KnotClient",
		"org.multimc.EntryPoint", "org.prismlauncher.EntryPoint":
		return true, nil
	case "net.minecraft.launchwrapper.Launch":
		for _, arg := range args[mainIndex+1:] {
			if strings.Contains(arg, "ServerTweaker") {
				return false, nil
			}
		}
		return true, nil
	case "cpw.mods.modlauncher.Launcher", "cpw.mods.bootstraplauncher.BootstrapLauncher":
		for i := mainIndex + 1; i < len(args); i++ {
			target := ""
			if args[i] == "--launchTarget" && i+1 < len(args) {
				target = args[i+1]
			}
			if strings.HasPrefix(args[i], "--launchTarget=") {
				target = strings.TrimPrefix(args[i], "--launchTarget=")
			}
			switch strings.ToLower(target) {
			case "forgeclient", "fmlclient", "neoforgeclient":
				return true, nil
			}
		}
		return false, nil
	}
	return false, nil
}
