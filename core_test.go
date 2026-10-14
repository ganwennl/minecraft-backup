package main

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func isolatedWorldDirs(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("APPDATA", root)
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "Local"))
	bedrock := filepath.Join(root, "Minecraft Bedrock", "Users", "123", "games", "com.mojang", "minecraftWorlds")
	java := filepath.Join(root, ".minecraft", "saves")
	for _, dir := range []string{bedrock, java} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	return bedrock, java
}

func writeTestWorld(t *testing.T, base, name, title string) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "levelname.txt"), []byte(title), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeTestArchive(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, contents := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestScanStatusFindsBothEditions(t *testing.T) {
	bedrock, java := isolatedWorldDirs(t)
	writeTestWorld(t, bedrock, "bedrock1", "基岩世界")
	writeTestWorld(t, java, "java1", "Java World")

	status := scanStatus()
	if len(status.BedrockPaths) != 1 || status.BedrockPaths[0] != bedrock || status.JavaPath != java {
		t.Fatalf("unexpected world paths: %#v", status)
	}
	if len(status.Worlds) != 2 || status.Worlds[0].Edition != "Bedrock" || status.Worlds[1].Edition != "Java" {
		t.Fatalf("unexpected worlds: %#v", status.Worlds)
	}
	if status.DefaultDest != defaultBackupDir() {
		t.Fatalf("default destination = %q", status.DefaultDest)
	}
}

func TestNativeBackupAndLocalRestore(t *testing.T) {
	bedrock, _ := isolatedWorldDirs(t)
	writeTestWorld(t, bedrock, "world1", "My World")
	dest := filepath.Join(t.TempDir(), "backups")
	state := &appState{isGameRunning: func() bool { return false }}
	backup, err := state.createBackup(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(backup.Message, "1 个世界") || filepath.Dir(backup.File) != dest {
		t.Fatalf("unexpected backup result: %#v", backup)
	}
	if _, err := os.Stat(backup.File); err != nil {
		t.Fatal(err)
	}

	// Native restore reads the chosen file directly, even when temporary storage is unavailable.
	t.Setenv("TEMP", filepath.Join(t.TempDir(), "unavailable"))
	t.Setenv("TMP", filepath.Join(t.TempDir(), "unavailable"))
	result, err := state.restoreBackup(backup.File)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Message, "1 个世界") {
		t.Fatalf("unexpected restore result: %#v", result)
	}
	worlds := scanWorlds("Bedrock", []string{bedrock})
	if len(worlds) != 2 {
		t.Fatalf("worlds after restore = %#v", worlds)
	}
	for _, w := range worlds {
		if w.Name != "My World" {
			t.Fatalf("world contents changed: %#v", w)
		}
	}
	if state.busy {
		t.Fatal("state remained busy after successful operations")
	}
}

func TestNativeRestoreAcceptsMCWorldAndRejectsOtherFiles(t *testing.T) {
	bedrock, _ := isolatedWorldDirs(t)
	state := &appState{isGameRunning: func() bool { return false }}
	archive := filepath.Join(t.TempDir(), "world.MCWORLD")
	writeTestArchive(t, archive, map[string]string{"levelname.txt": "Imported World", "db/data.ldb": "data"})
	if _, err := state.restoreBackup(archive); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(bedrock, "Imported World", "db", "data.ldb")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", "world.txt", filepath.Join(t.TempDir(), "missing.zip")} {
		if _, err := state.restoreBackup(path); err == nil {
			t.Fatalf("restore succeeded for %q", path)
		}
		if state.busy {
			t.Fatalf("state remained busy after rejection of %q", path)
		}
	}
}

func TestNativeOperationsRejectRunningGame(t *testing.T) {
	state := &appState{isGameRunning: func() bool { return true }}
	for _, run := range []func(string) (operationResult, error){state.createBackup, state.restoreBackup} {
		if _, err := run("unused.zip"); err == nil || !strings.Contains(err.Error(), "正在运行") {
			t.Fatalf("game guard returned %v", err)
		}
		if state.busy {
			t.Fatal("game rejection left state busy")
		}
	}
}

func TestNativeOperationsRemainExclusiveDuringGameCheck(t *testing.T) {
	isolatedWorldDirs(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	state := &appState{isGameRunning: func() bool {
		close(entered)
		<-release
		return false
	}}
	done := make(chan error, 1)
	dest := t.TempDir()
	go func() {
		_, err := state.createBackup(dest)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("backup did not reach game check")
	}
	_, err := state.restoreBackup("unused.zip")
	close(release)
	if err == nil || !strings.Contains(err.Error(), "正在处理") {
		t.Fatalf("concurrent operation returned %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("backup unexpectedly succeeded without worlds")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("backup did not finish")
	}
	if state.busy {
		t.Fatal("failed backup left state busy")
	}
}

func TestZipWorldsFailsOnUnreadableSourceAndRemovesPartialArchive(t *testing.T) {
	source := writeTestWorld(t, t.TempDir(), "world", "Test")
	archive := filepath.Join(t.TempDir(), "backup.zip")
	count, err := zipWorldsWithOpen(archive, []world{{Edition: "Bedrock", ID: "world", Name: "Test", Path: source}}, func(string) (*os.File, error) {
		return nil, os.ErrPermission
	})
	if !errors.Is(err, os.ErrPermission) || count != 0 {
		t.Fatalf("permission failure returned count=%d, err=%v", count, err)
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatalf("partial archive was retained: %v", err)
	}
}

func TestZipWorldsKeepsExistingArchiveOnCreateCollision(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "backup.zip")
	if err := os.WriteFile(archive, []byte("existing backup"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := zipWorlds(archive, nil); !errors.Is(err, os.ErrExist) {
		t.Fatalf("archive collision returned %v", err)
	}
	data, err := os.ReadFile(archive)
	if err != nil || string(data) != "existing backup" {
		t.Fatalf("existing archive changed: %q, %v", data, err)
	}
}

func TestRestoreOrdinaryJavaArchiveUsesJavaWorldDirectory(t *testing.T) {
	bedrock, java := isolatedWorldDirs(t)
	archive := filepath.Join(t.TempDir(), "java.zip")
	writeTestArchive(t, archive, map[string]string{"MyWorld/level.dat": "level", "MyWorld/region/r.0.0.mca": "region"})
	state := &appState{isGameRunning: func() bool { return false }}
	if _, err := state.restoreBackup(archive); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(java, "MyWorld", "region", "r.0.0.mca")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(bedrock)
	if err != nil || len(entries) != 0 {
		t.Fatalf("Java world appeared in Bedrock directory: %#v, %v", entries, err)
	}
}

func TestRestoreRejectsArchiveWithoutWorldMarkers(t *testing.T) {
	bedrock, java := isolatedWorldDirs(t)
	archive := filepath.Join(t.TempDir(), "documents.zip")
	writeTestArchive(t, archive, map[string]string{"readme.txt": "not a world", "Documents/note.txt": "also not a world"})
	state := &appState{isGameRunning: func() bool { return false }}
	if _, err := state.restoreBackup(archive); err == nil {
		t.Fatal("ordinary documents archive was restored as a world")
	}
	for _, dir := range []string{bedrock, java} {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatalf("rejected archive wrote files: %#v, %v", entries, err)
		}
	}
}

func TestCreateBackupRejectsDestinationInsideWorld(t *testing.T) {
	bedrock, _ := isolatedWorldDirs(t)
	source := writeTestWorld(t, bedrock, "world1", "Test World")
	dest := filepath.Join(source, "backups")
	state := &appState{isGameRunning: func() bool { return false }}
	if _, err := state.createBackup(dest); err == nil || !strings.Contains(err.Error(), "世界文件夹里面") {
		t.Fatalf("backup inside a world returned %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("rejected destination was created: %v", err)
	}
	if state.busy {
		t.Fatal("rejected backup left state busy")
	}
}

func TestRestoreBackupRejectsDirectoryArchive(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "not-a-file.zip")
	if err := os.Mkdir(archive, 0755); err != nil {
		t.Fatal(err)
	}
	state := &appState{isGameRunning: func() bool { return false }}
	if _, err := state.restoreBackup(archive); err == nil {
		t.Fatal("directory archive was accepted")
	}
	if state.busy {
		t.Fatal("rejected restore left state busy")
	}
}

func TestUniqueOutputPathsReturnStatErrors(t *testing.T) {
	for _, unique := range []func(string) (string, error){uniqueBackupFile, uniqueRestorePath} {
		done := make(chan error, 1)
		path := filepath.Join(t.TempDir(), "invalid\x00name")
		go func() {
			_, err := unique(path)
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("invalid path did not return a stat error")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("unique output path kept retrying an invalid name")
		}
	}
}

func TestRestoreBackupRejectsOverlongWorldName(t *testing.T) {
	isolatedWorldDirs(t)
	archive := filepath.Join(t.TempDir(), "long-name.zip")
	writeTestArchive(t, archive, map[string]string{strings.Repeat("a", 300) + "/level.dat": "level"})
	state := &appState{isGameRunning: func() bool { return false }}
	done := make(chan error, 1)
	go func() {
		_, err := state.restoreBackup(archive)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("overlong imported world name was accepted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("overlong imported world name did not fail promptly")
	}
}

func TestRestoreRejectsLeadingSlashArchiveEntriesBeforeWriting(t *testing.T) {
	for _, entry := range []string{"/World/level.dat", "/level.dat", "\\World/level.dat"} {
		t.Run(entry, func(t *testing.T) {
			bedrock, java := isolatedWorldDirs(t)
			archive := filepath.Join(t.TempDir(), "absolute-path.zip")
			writeTestArchive(t, archive, map[string]string{
				entry:                  "bad world",
				"ValidWorld/level.dat": "valid world",
			})
			count, err := restoreZip(archive)
			if err == nil || count != 0 {
				t.Fatalf("absolute archive entry returned count=%d, err=%v", count, err)
			}
			for _, dir := range []string{bedrock, java} {
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 0 {
					t.Fatalf("rejected archive wrote worlds: %#v, %v", entries, err)
				}
			}
		})
	}
}
