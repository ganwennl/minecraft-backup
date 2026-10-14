package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type autoBackupResult struct {
	Updated, Unchanged int
	Files              []string
}

type autoBackupMetadata struct {
	Format      string `json:"format"`
	Version     int    `json:"version"`
	WorldID     string `json:"worldId"`
	Fingerprint string `json:"fingerprint"`
}

type autoBackupEntry struct {
	name      string
	directory bool
	size      int64
	digest    [sha256.Size]byte
}

type autoBackupOps struct {
	createArchive func(string, []world) (int, error)
	replaceFile   func(string, string) error
}

var errAutoBackupGameRunning = errors.New("检测到 Minecraft 正在运行，自动备份将在游戏关闭后重试")

func (s *appState) autoBackup(dest string) (autoBackupResult, error) {
	if err := s.beginOperation(); err != nil {
		return autoBackupResult{}, err
	}
	defer s.endOperation()
	checkGame := s.gameRunning
	if s.isGameRunning == nil {
		checkGame = func() bool {
			running, err := minecraftProbe()
			return err != nil || running
		}
	}
	if checkGame() {
		return autoBackupResult{}, errAutoBackupGameRunning
	}
	return runAutoBackup(scanStatus().Worlds, dest, checkGame)
}

func runAutoBackup(worlds []world, dest string, gameRunning func() bool) (autoBackupResult, error) {
	return runAutoBackupWithOps(worlds, dest, gameRunning, autoBackupOps{})
}

func runAutoBackupWithOps(worlds []world, dest string, gameRunning func() bool, ops autoBackupOps) (autoBackupResult, error) {
	var result autoBackupResult
	if gameRunning != nil && gameRunning() {
		return result, errAutoBackupGameRunning
	}
	if len(worlds) == 0 {
		return result, nil
	}
	if strings.TrimSpace(dest) == "" {
		dest = defaultBackupDir()
	}
	dir, err := filepath.Abs(filepath.Join(dest, "自动备份"))
	if err != nil {
		return result, err
	}
	if backupPathInsideWorld(dir, worlds) {
		return result, errors.New("自动备份位置不能放在 Minecraft 世界文件夹里面")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return result, fmt.Errorf("创建自动备份目录失败: %w", err)
	}
	if ops.createArchive == nil {
		ops.createArchive = zipWorlds
	}
	if ops.replaceFile == nil {
		ops.replaceFile = replaceBackupFile
	}
	seen := map[string]bool{}
	var failures []error
	for _, w := range worlds {
		id, err := autoBackupWorldID(w)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", w.Name, err))
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		target := filepath.Join(dir, strings.ToLower(safeName(w.Edition))+"-"+id+".zip")
		updated, err := updateAutoBackup(w, id, target, gameRunning, ops)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", w.Name, err))
			if errors.Is(err, errAutoBackupGameRunning) {
				break
			}
			continue
		}
		if updated {
			result.Updated++
			result.Files = append(result.Files, target)
		} else {
			result.Unchanged++
		}
	}
	return result, errors.Join(failures...)
}

func autoBackupWorldID(w world) (string, error) {
	abs, err := filepath.Abs(w.Path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	sum := sha256.Sum256([]byte(strings.ToLower(w.Edition) + "\x00" + abs))
	return hex.EncodeToString(sum[:]), nil
}

func updateAutoBackup(w world, id, target string, gameRunning func() bool, ops autoBackupOps) (bool, error) {
	if gameRunning != nil && gameRunning() {
		return false, errAutoBackupGameRunning
	}
	before, err := fingerprintWorld(w.Path)
	if err != nil {
		return false, fmt.Errorf("读取世界内容失败: %w", err)
	}
	if existing, err := fingerprintAutoBackup(target, id); err == nil && existing == before {
		return false, nil
	}
	// A private directory reserves the archive name without exposing a create/remove race.
	tmpDir, err := os.MkdirTemp(filepath.Dir(target), ".minecraft-auto-")
	if err != nil {
		return false, err
	}
	tmp := filepath.Join(tmpDir, "world.zip")
	defer func() {
		_ = os.Remove(tmp)
		_ = os.Remove(tmpDir)
	}()
	if _, err := ops.createArchive(tmp, []world{w}); err != nil {
		return false, fmt.Errorf("生成自动备份失败: %w", err)
	}
	if err := writeAutoBackupMetadata(tmp, autoBackupMetadata{
		Format: "minecraft-auto-backup", Version: 1, WorldID: id, Fingerprint: before,
	}); err != nil {
		return false, err
	}
	archived, err := fingerprintAutoBackup(tmp, id)
	if err != nil {
		return false, fmt.Errorf("校验自动备份失败: %w", err)
	}
	after, err := fingerprintWorld(w.Path)
	if err != nil {
		return false, fmt.Errorf("复查世界内容失败: %w", err)
	}
	if before != archived || archived != after {
		return false, errors.New("世界内容在备份期间发生变化，已保留上次自动备份，稍后重试")
	}
	if gameRunning != nil && gameRunning() {
		return false, errAutoBackupGameRunning
	}
	if err := ops.replaceFile(tmp, target); err != nil {
		return false, fmt.Errorf("替换自动备份失败: %w", err)
	}
	return true, nil
}

func fingerprintWorld(root string) (string, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("世界路径不是普通目录")
	}
	var entries []autoBackupEntry
	err = filepath.WalkDir(root, func(name string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == root || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		entry := autoBackupEntry{name: filepath.ToSlash(rel), directory: d.IsDir()}
		if !entry.directory {
			f, err := os.Open(name)
			if err != nil {
				return err
			}
			info, err := f.Stat()
			if err != nil || !info.Mode().IsRegular() {
				_ = f.Close()
				if err != nil {
					return err
				}
				return errors.New("世界包含不支持的文件类型")
			}
			h := sha256.New()
			entry.size, err = io.Copy(h, f)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			copy(entry.digest[:], h.Sum(nil))
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return "", err
	}
	return fingerprintEntries(entries), nil
}

func fingerprintEntries(entries []autoBackupEntry) string {
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	h := sha256.New()
	for _, entry := range entries {
		_ = binary.Write(h, binary.LittleEndian, uint64(len(entry.name)))
		_, _ = io.WriteString(h, entry.name)
		kind := byte(0)
		if entry.directory {
			kind = 1
		}
		_, _ = h.Write([]byte{kind})
		_ = binary.Write(h, binary.LittleEndian, entry.size)
		_, _ = h.Write(entry.digest[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func fingerprintAutoBackup(filename, id string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxRestoreArchiveSize {
		return "", errors.New("自动备份文件类型或大小无效")
	}
	zr, err := zip.NewReader(file, info.Size())
	if err != nil {
		return "", err
	}
	var metadata autoBackupMetadata
	if err := json.Unmarshal([]byte(zr.Comment), &metadata); err != nil {
		return "", err
	}
	if metadata.Format != "minecraft-auto-backup" || metadata.Version != 1 || metadata.WorldID != id {
		return "", errors.New("自动备份元数据无效")
	}
	manifest := readManifest(zr.File)
	if len(manifest) != 1 {
		return "", errors.New("自动备份世界清单无效")
	}
	root := ""
	for key := range manifest {
		root = key
	}
	if root == "" || unsafeZipRoot(root) || strings.Contains(root, "/") {
		return "", errors.New("自动备份世界路径无效")
	}
	var entries []autoBackupEntry
	seen := map[string]bool{}
	var extracted int64
	for _, f := range zr.File {
		if f.Name == "__minecraft_backup_manifest.json" {
			continue
		}
		if !strings.HasPrefix(f.Name, root+"/") || f.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("自动备份包含无效路径")
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(f.Name, root+"/"), "/")
		if rel == "" || strings.Contains(rel, "\\") || path.Clean(rel) != rel || path.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") || seen[rel] {
			return "", errors.New("自动备份包含无效路径")
		}
		seen[rel] = true
		entry := autoBackupEntry{name: rel, directory: f.FileInfo().IsDir()}
		if !entry.directory {
			r, err := f.Open()
			if err != nil {
				return "", err
			}
			h := sha256.New()
			entry.size, err = copyWithLimit(h, r, maxRestoreExtractSize-extracted)
			extracted += entry.size
			closeErr := r.Close()
			if err != nil {
				return "", err
			}
			if closeErr != nil {
				return "", closeErr
			}
			copy(entry.digest[:], h.Sum(nil))
		}
		entries = append(entries, entry)
	}
	fingerprint := fingerprintEntries(entries)
	if metadata.Fingerprint != fingerprint {
		return "", errors.New("自动备份内容校验失败")
	}
	return fingerprint, nil
}

func writeAutoBackupMetadata(filename string, metadata autoBackupMetadata) (err error) {
	data, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if len(data) > 65535 {
		return errors.New("自动备份元数据过大")
	}
	f, err := os.OpenFile(filename, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() < 22 {
		return errors.New("自动备份 ZIP 尾部无效")
	}
	// zipWorlds creates an archive with an empty comment. The fixed EOCD tail also
	// follows ZIP64 records, so adding only its comment preserves all file offsets.
	var tail [22]byte
	if _, err := f.ReadAt(tail[:], info.Size()-22); err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(tail[:4]) != 0x06054b50 || binary.LittleEndian.Uint16(tail[20:]) != 0 {
		return errors.New("自动备份 ZIP 尾部无效")
	}
	var length [2]byte
	binary.LittleEndian.PutUint16(length[:], uint16(len(data)))
	if _, err := f.WriteAt(length[:], info.Size()-2); err != nil {
		return err
	}
	if _, err := f.WriteAt(data, info.Size()); err != nil {
		return err
	}
	return f.Sync()
}
