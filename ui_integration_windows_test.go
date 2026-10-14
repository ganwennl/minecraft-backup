//go:build windows

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// This host is compiled only into the Go test executable. Production has no
// environment switch capable of replacing its real Minecraft process probe.
func TestNativeAutomationHost(t *testing.T) {
	fixture := os.Getenv("MINECRAFT_UI_FIXTURE")
	if fixture == "" {
		t.Skip("native integration host requires an isolated fixture")
	}
	repo, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "go.mod")); err != nil {
		t.Fatalf("integration host must run from the repository: %v", err)
	}
	build, err := filepath.EvalSymlinks(filepath.Join(repo, "build"))
	if err != nil {
		t.Fatal(err)
	}
	fixture, err = filepath.EvalSymlinks(fixture)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err = filepath.Abs(fixture)
	if err != nil {
		t.Fatal(err)
	}
	inside := func(base, filename string) bool {
		rel, err := filepath.Rel(base, filename)
		return err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
	}
	if !inside(build, fixture) {
		t.Fatal("integration fixture must be a private directory inside repository/build")
	}
	info, err := os.Stat(fixture)
	if err != nil || !info.IsDir() {
		t.Fatal("integration fixture is not a directory")
	}
	for _, name := range []string{"APPDATA", "LOCALAPPDATA", "USERPROFILE", "HOME"} {
		value, err := filepath.EvalSymlinks(os.Getenv(name))
		if err != nil || !inside(fixture, value) {
			t.Fatalf("%s must point inside the isolated integration fixture", name)
		}
	}
	settings, err := loadSettings()
	if err != nil || !settings.Automatic {
		t.Fatalf("integration fixture must pre-enable automatic backup: %v", err)
	}
	backupDir, err := filepath.EvalSymlinks(settings.BackupDir)
	if err != nil || !inside(fixture, backupDir) {
		t.Fatal("integration backup destination must be inside the isolated fixture")
	}
	previousProbe := minecraftProbe
	minecraftProbe = func() (bool, error) {
		_, err := os.Stat(filepath.Join(fixture, "game-running.marker"))
		if os.IsNotExist(err) {
			return false, nil
		}
		return err == nil, err
	}
	defer func() { minecraftProbe = previousProbe }()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	deactivate := activateNativeTestManifest(t, filepath.Join(repo, "app.manifest"))
	defer deactivate()
	background := os.Getenv("MINECRAFT_UI_FOREGROUND") != "1"
	if err := runDesktop(background, false); err != nil {
		t.Fatal(err)
	}
}

func activateNativeTestManifest(t *testing.T, manifest string) func() {
	t.Helper()
	type activationContext struct {
		Size, Flags                              uint32
		Source                                   *uint16
		ProcessorArchitecture, LanguageID        uint16
		AssemblyDirectory, Resource, Application *uint16
		Module                                   uintptr
	}
	source, err := syscall.UTF16PtrFromString(manifest)
	if err != nil {
		t.Fatal(err)
	}
	ctx := activationContext{Source: source}
	ctx.Size = uint32(unsafe.Sizeof(ctx))
	kernel := syscall.NewLazyDLL("kernel32.dll")
	handle, _, callErr := kernel.NewProc("CreateActCtxW").Call(uintptr(unsafe.Pointer(&ctx)))
	if handle == ^uintptr(0) {
		t.Fatalf("activate test common-controls manifest: %v", callErr)
	}
	var cookie uintptr
	if ok, _, callErr := kernel.NewProc("ActivateActCtx").Call(handle, uintptr(unsafe.Pointer(&cookie))); ok == 0 {
		kernel.NewProc("ReleaseActCtx").Call(handle)
		t.Fatalf("activate test common-controls context: %v", callErr)
	}
	return func() {
		kernel.NewProc("DeactivateActCtx").Call(0, cookie)
		kernel.NewProc("ReleaseActCtx").Call(handle)
	}
}
