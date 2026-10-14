package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func autoTestWorld(t *testing.T, base, title string) world {
	t.Helper()
	dir := writeTestWorld(t, base, "same-id", title)
	if err := os.WriteFile(filepath.Join(dir, "data.bin"), []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "empty-directory"), 0755); err != nil {
		t.Fatal(err)
	}
	return world{Edition: "Bedrock", ID: "same-id", Name: title, Path: dir}
}

func readAutoTestFile(t *testing.T, filename string) []byte {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func readAutoTestZipEntry(t *testing.T, filename, suffix string) string {
	t.Helper()
	zr, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, suffix) {
			continue
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	t.Fatalf("ZIP entry ending in %q not found", suffix)
	return ""
}

func TestAutoBackupSkipsUnchangedContentWithoutRewritingArchive(t *testing.T) {
	w := autoTestWorld(t, t.TempDir(), "Test World")
	dest := t.TempDir()
	first, err := runAutoBackup([]world{w}, dest, func() bool { return false })
	if err != nil || first.Updated != 1 || first.Unchanged != 0 || len(first.Files) != 1 {
		t.Fatalf("first run = %#v, %v", first, err)
	}
	filename := first.Files[0]
	if filepath.Dir(filename) != filepath.Join(dest, "自动备份") || !strings.HasPrefix(filepath.Base(filename), "bedrock-") {
		t.Fatalf("unexpected automatic backup path %q", filename)
	}
	before := readAutoTestFile(t, filename)
	stamp := time.Unix(1234567890, 0)
	if err := os.Chtimes(filename, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	second, err := runAutoBackup([]world{w}, dest, func() bool { return false })
	if err != nil || second.Updated != 0 || second.Unchanged != 1 || len(second.Files) != 0 {
		t.Fatalf("unchanged run = %#v, %v", second, err)
	}
	after := readAutoTestFile(t, filename)
	info, err := os.Stat(filename)
	if err != nil || !bytes.Equal(before, after) || !info.ModTime().Equal(stamp) {
		t.Fatalf("unchanged archive was rewritten: %v", err)
	}
}

func TestAutoBackupUpdatesSameFilenameForContentRenameAndDeletion(t *testing.T) {
	w := autoTestWorld(t, t.TempDir(), "First Name")
	dest := t.TempDir()
	initial, err := runAutoBackup([]world{w}, dest, nil)
	if err != nil {
		t.Fatal(err)
	}
	filename := initial.Files[0]
	dataPath := filepath.Join(w.Path, "data.bin")
	info, err := os.Stat(dataPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataPath, []byte("two"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dataPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	changed, err := runAutoBackup([]world{w}, dest, nil)
	if err != nil || changed.Updated != 1 || len(changed.Files) != 1 || changed.Files[0] != filename {
		t.Fatalf("same-mtime change = %#v, %v", changed, err)
	}
	if got := readAutoTestZipEntry(t, filename, "/data.bin"); got != "two" {
		t.Fatalf("updated bytes = %q", got)
	}
	w.Name = "Renamed World"
	if err := os.WriteFile(filepath.Join(w.Path, "levelname.txt"), []byte(w.Name), 0644); err != nil {
		t.Fatal(err)
	}
	renamed, err := runAutoBackup([]world{w}, dest, nil)
	if err != nil || renamed.Updated != 1 || renamed.Files[0] != filename {
		t.Fatalf("renamed world = %#v, %v", renamed, err)
	}
	if err := os.Remove(dataPath); err != nil {
		t.Fatal(err)
	}
	deleted, err := runAutoBackup([]world{w}, dest, nil)
	if err != nil || deleted.Updated != 1 || deleted.Files[0] != filename {
		t.Fatalf("deleted file = %#v, %v", deleted, err)
	}
	zr, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/data.bin") {
			t.Fatal("deleted source file remains in updated backup")
		}
	}
	entries, err := os.ReadDir(filepath.Dir(filename))
	if err != nil || len(entries) != 1 {
		t.Fatalf("automatic backup accumulated files: %#v, %v", entries, err)
	}
}

func TestAutoBackupDistinguishesSameNamesAndIDsFromDifferentSources(t *testing.T) {
	first := autoTestWorld(t, t.TempDir(), "Same Name")
	second := autoTestWorld(t, t.TempDir(), "Same Name")
	dest := t.TempDir()
	result, err := runAutoBackup([]world{first, second}, dest, nil)
	if err != nil || result.Updated != 2 || len(result.Files) != 2 || result.Files[0] == result.Files[1] {
		t.Fatalf("same-name worlds = %#v, %v", result, err)
	}
	// Removed worlds and unrelated files are retained.
	other := filepath.Join(dest, "自动备份", "unrelated.zip")
	if err := os.WriteFile(other, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := runAutoBackup([]world{first}, dest, nil); err != nil {
		t.Fatal(err)
	}
	for _, filename := range append(result.Files, other) {
		if _, err := os.Stat(filename); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAutoBackupRestoresMissingOrCorruptedTarget(t *testing.T) {
	for _, scenario := range []string{"missing", "corrupted header", "corrupted content"} {
		t.Run(scenario, func(t *testing.T) {
			w := autoTestWorld(t, t.TempDir(), "Test")
			dest := t.TempDir()
			first, err := runAutoBackup([]world{w}, dest, nil)
			if err != nil {
				t.Fatal(err)
			}
			target := first.Files[0]
			switch scenario {
			case "corrupted header":
				err = os.WriteFile(target, []byte("broken ZIP"), 0644)
			case "corrupted content":
				zr, openErr := zip.OpenReader(target)
				if openErr != nil {
					t.Fatal(openErr)
				}
				var offset int64
				for _, f := range zr.File {
					if strings.HasSuffix(f.Name, "/data.bin") {
						offset, err = f.DataOffset()
					}
				}
				_ = zr.Close()
				if err != nil || offset == 0 {
					t.Fatalf("could not locate content for corruption: %v", err)
				}
				data := readAutoTestFile(t, target)
				data[offset] ^= 0xff
				err = os.WriteFile(target, data, 0644)
			case "missing":
				err = os.Remove(target)
			}
			if err != nil {
				t.Fatal(err)
			}
			result, err := runAutoBackup([]world{w}, dest, nil)
			if err != nil || result.Updated != 1 || result.Files[0] != target {
				t.Fatalf("target repair = %#v, %v", result, err)
			}
			id, _ := autoBackupWorldID(w)
			if _, err := fingerprintAutoBackup(target, id); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAutoBackupDoesNotPublishUnstableOrFailedArchives(t *testing.T) {
	for _, scenario := range []string{"source changed before compression", "source changed after compression", "source deleted", "game restarted", "compression failed", "replace failed"} {
		t.Run(scenario, func(t *testing.T) {
			w := autoTestWorld(t, t.TempDir(), "Test")
			dest := t.TempDir()
			first, err := runAutoBackup([]world{w}, dest, nil)
			if err != nil {
				t.Fatal(err)
			}
			target := first.Files[0]
			before := readAutoTestFile(t, target)
			dataPath := filepath.Join(w.Path, "data.bin")
			if err := os.WriteFile(dataPath, []byte("pending"), 0644); err != nil {
				t.Fatal(err)
			}
			running := false
			ops := autoBackupOps{createArchive: func(filename string, worlds []world) (int, error) {
				if scenario == "source changed before compression" {
					if err := os.WriteFile(dataPath, []byte("during compression"), 0644); err != nil {
						return 0, err
					}
				}
				if scenario == "compression failed" {
					return 0, os.ErrPermission
				}
				count, err := zipWorlds(filename, worlds)
				if err != nil {
					return count, err
				}
				switch scenario {
				case "source changed after compression":
					err = os.WriteFile(dataPath, []byte("changed after compression"), 0644)
				case "source deleted":
					err = os.Remove(dataPath)
				case "game restarted":
					running = true
				}
				return count, err
			}}
			if scenario == "replace failed" {
				ops.replaceFile = func(string, string) error { return os.ErrPermission }
			}
			result, err := runAutoBackupWithOps([]world{w}, dest, func() bool { return running }, ops)
			if err == nil || result.Updated != 0 || !bytes.Equal(before, readAutoTestFile(t, target)) {
				t.Fatalf("failed operation modified old backup: %#v, %v", result, err)
			}
			entries, readErr := os.ReadDir(filepath.Dir(target))
			if readErr != nil || len(entries) != 1 {
				t.Fatalf("temporary files remain: %#v, %v", entries, readErr)
			}
		})
	}
}

func TestAutoBackupArchiveRemainsCompatibleWithRestore(t *testing.T) {
	bedrock, _ := isolatedWorldDirs(t)
	w := autoTestWorld(t, bedrock, "Restorable")
	result, err := runAutoBackup([]world{w}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := restoreZip(result.Files[0]); err != nil || count != 1 {
		t.Fatalf("restore automatic archive = %d, %v", count, err)
	}
	if worlds := scanWorlds("Bedrock", []string{bedrock}); len(worlds) != 2 {
		t.Fatalf("restored worlds = %#v", worlds)
	}
}

func TestAutoBackupUsesSharedOperationLockAndGameGuard(t *testing.T) {
	state := &appState{busy: true, isGameRunning: func() bool { t.Fatal("busy operation checked game"); return false }}
	if _, err := state.autoBackup(t.TempDir()); err == nil {
		t.Fatal("automatic backup bypassed shared busy state")
	}
	state.busy = false
	state.isGameRunning = func() bool { return true }
	if _, err := state.autoBackup(t.TempDir()); !errors.Is(err, errAutoBackupGameRunning) {
		t.Fatalf("running game guard = %v", err)
	}
	if state.busy {
		t.Fatal("automatic backup did not release shared operation state")
	}
}

func TestAutoBackupRejectsDestinationInsideWorldAndUnreadableWorld(t *testing.T) {
	w := autoTestWorld(t, t.TempDir(), "Test")
	if _, err := runAutoBackup([]world{w}, w.Path, nil); err == nil {
		t.Fatal("automatic backup was allowed inside source world")
	}
	if err := os.Remove(filepath.Join(w.Path, "data.bin")); err != nil {
		t.Fatal(err)
	}
	missing := w
	missing.Path = filepath.Join(t.TempDir(), "missing")
	if result, err := runAutoBackup([]world{missing}, t.TempDir(), nil); err == nil || result.Updated != 0 {
		t.Fatalf("unreadable world = %#v, %v", result, err)
	}
}

func TestAutoBackupStableIDUsesNormalizedPathAndEdition(t *testing.T) {
	w := autoTestWorld(t, t.TempDir(), "Original")
	id, err := autoBackupWorldID(w)
	if err != nil {
		t.Fatal(err)
	}
	other := w
	other.Name = "Renamed"
	other.Path = filepath.Join(w.Path, ".")
	same, err := autoBackupWorldID(other)
	if err != nil || same != id {
		t.Fatalf("normalized path or display rename changed identity: %q, %q, %v", id, same, err)
	}
	other.Edition = "Java"
	different, err := autoBackupWorldID(other)
	if err != nil || different == id {
		t.Fatalf("different edition reused identity: %q, %v", different, err)
	}
}
