package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindBedrockWorldDirsFindsGDKUserPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APPDATA", root)
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "Local"))

	worldsDir := filepath.Join(root, "Minecraft Bedrock", "Users", "123", "games", "com.mojang", "minecraftWorlds")
	if err := os.MkdirAll(filepath.Join(worldsDir, "world1"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worldsDir, "world1", "levelname.txt"), []byte("Test World"), 0644); err != nil {
		t.Fatal(err)
	}

	dirs := findBedrockWorldDirs()
	if len(dirs) != 1 || dirs[0] != worldsDir {
		t.Fatalf("dirs = %#v, want %#v", dirs, []string{worldsDir})
	}
}

func TestZipWorldsWritesManifestAndFiles(t *testing.T) {
	root := t.TempDir()
	worldDir := filepath.Join(root, "minecraftWorlds", "abc=")
	if err := os.MkdirAll(filepath.Join(worldDir, "db"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worldDir, "levelname.txt"), []byte("My World"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worldDir, "db", "data.ldb"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(root, "backup.zip")
	count, err := zipWorlds(out, []world{{Edition: "Bedrock", ID: "abc=", Name: "My World", Path: worldDir}})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["__minecraft_backup_manifest.json"] {
		t.Fatal("missing manifest")
	}
	if !names["Bedrock_My World_abc=/levelname.txt"] {
		t.Fatal("missing levelname.txt")
	}
	if !names["Bedrock_My World_abc=/db/data.ldb"] {
		t.Fatal("missing db file")
	}
}

func TestZipWorldsUsesUniqueRoots(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "a")
	second := filepath.Join(root, "b")
	for _, dir := range []string{first, second} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "levelname.txt"), []byte("Same"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	out := filepath.Join(root, "backup.zip")
	_, err := zipWorlds(out, []world{
		{Edition: "Bedrock", ID: "abc", Name: "Same", Path: first},
		{Edition: "Bedrock", ID: "abc", Name: "Same", Path: second},
	})
	if err != nil {
		t.Fatal(err)
	}

	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["Bedrock_Same_abc/levelname.txt"] || !names["Bedrock_Same_abc_2/levelname.txt"] {
		t.Fatalf("duplicate roots were not made unique: %#v", names)
	}
}

func TestRestoreZipRestoresWithoutOverwrite(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APPDATA", root)
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "Local"))

	worldsDir := filepath.Join(root, "Minecraft Bedrock", "Users", "123", "games", "com.mojang", "minecraftWorlds")
	existing := filepath.Join(worldsDir, "abc=")
	if err := os.MkdirAll(existing, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existing, "levelname.txt"), []byte("Existing"), 0644); err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(root, "source", "abc=")
	if err := os.MkdirAll(filepath.Join(source, "db"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "levelname.txt"), []byte("Restored"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "db", "data.ldb"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	backup := filepath.Join(root, "backup.zip")
	if _, err := zipWorlds(backup, []world{{Edition: "Bedrock", ID: "abc=", Name: "Restored", Path: source}}); err != nil {
		t.Fatal(err)
	}
	count, err := restoreZip(backup)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	got, err := os.ReadFile(filepath.Join(existing, "levelname.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "Existing" {
		t.Fatalf("existing world was overwritten: %q", got)
	}

	matches, err := filepath.Glob(filepath.Join(worldsDir, "abc=_restored_*", "levelname.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("restored matches = %#v, want 1", matches)
	}
	restored, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != "Restored" {
		t.Fatalf("restored world = %q, want Restored", restored)
	}
}

func TestRestoreZipIgnoresTraversalRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APPDATA", root)
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "Local"))

	backup := filepath.Join(root, "evil.zip")
	file, err := os.Create(backup)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	writer, err := zw.Create("../evil.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("bad")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := restoreZip(backup); err == nil {
		t.Fatal("restoreZip succeeded for traversal zip")
	}
	if _, err := os.Stat(filepath.Join(root, "evil.txt")); !os.IsNotExist(err) {
		t.Fatalf("traversal file was written, err=%v", err)
	}
}

func TestRestoreZipDoesNotPartiallyWriteOnTraversal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APPDATA", root)
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "Local"))

	worldsDir := filepath.Join(root, "Minecraft Bedrock", "Users", "123", "games", "com.mojang", "minecraftWorlds")
	if err := os.MkdirAll(worldsDir, 0755); err != nil {
		t.Fatal(err)
	}

	backup := filepath.Join(root, "partial.zip")
	file, err := os.Create(backup)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	good, err := zw.Create("Bedrock_Test_abc=/levelname.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := good.Write([]byte("should-not-write")); err != nil {
		t.Fatal(err)
	}
	bad, err := zw.Create("Bedrock_Test_abc=/../evil.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Write([]byte("bad")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := restoreZip(backup); err == nil {
		t.Fatal("restoreZip succeeded for traversal zip")
	}
	matches, err := filepath.Glob(filepath.Join(worldsDir, "Bedrock_Test_abc=*", "levelname.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("partial restore wrote files: %#v", matches)
	}
}

func TestRestoreRootLevelMCWorld(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APPDATA", root)
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "Local"))

	worldsDir := filepath.Join(root, "Minecraft Bedrock", "Users", "123", "games", "com.mojang", "minecraftWorlds")
	if err := os.MkdirAll(worldsDir, 0755); err != nil {
		t.Fatal(err)
	}

	backup := filepath.Join(root, "world.mcworld")
	file, err := os.Create(backup)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	files := map[string]string{
		"levelname.txt": "Root World",
		"level.dat":     "level",
		"db/data.ldb":   "data",
	}
	for name, body := range files {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	count, err := restoreZip(backup)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	got, err := os.ReadFile(filepath.Join(worldsDir, "Root World", "levelname.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "Root World" {
		t.Fatalf("levelname = %q, want Root World", got)
	}
	if _, err := os.Stat(filepath.Join(worldsDir, "Root World", "db", "data.ldb")); err != nil {
		t.Fatal(err)
	}
}

func TestBackupPathInsideWorld(t *testing.T) {
	root := t.TempDir()
	worldDir := filepath.Join(root, "minecraftWorlds", "abc=")
	worlds := []world{{Path: worldDir}}

	if !backupPathInsideWorld(filepath.Join(worldDir, "backup.zip"), worlds) {
		t.Fatal("backup path inside world was not detected")
	}
	if backupPathInsideWorld(filepath.Join(root, "backups", "backup.zip"), worlds) {
		t.Fatal("safe backup path was detected as inside world")
	}
}

func TestUniqueRestorePathSkipsExistingTimestampedPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "world")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	first, err := uniqueRestorePath(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(first, 0755); err != nil {
		t.Fatal(err)
	}
	second, err := uniqueRestorePath(path)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("uniqueRestorePath returned an existing path")
	}
}

func TestSafeNameAvoidsWindowsReservedNames(t *testing.T) {
	for _, name := range []string{"CON", "nul.txt", "COM1", "LPT9"} {
		if got := safeName(name); got == name {
			t.Fatalf("safeName(%q) = %q, want changed", name, got)
		}
	}
}

func TestCopyWithLimitRejectsOversizeData(t *testing.T) {
	var out strings.Builder
	n, err := copyWithLimit(&out, strings.NewReader("abcd"), 3)
	if err == nil {
		t.Fatal("copyWithLimit succeeded for oversized input")
	}
	if n != 4 {
		t.Fatalf("copied = %d, want 4", n)
	}
}
