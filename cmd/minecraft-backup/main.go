package main

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type world struct {
	Edition string `json:"edition"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Root    string `json:"root,omitempty"`
}

type appStatus struct {
	BedrockPaths []string
	JavaPath     string
	Worlds       []world
	DefaultDest  string
}

type operationResult struct {
	Message string
	File    string
}

type appState struct {
	mu            sync.Mutex
	busy          bool
	isGameRunning func() bool
}

// Test hosts can replace this function; production always uses the native probe.
var minecraftProbe = probeMinecraftRunning

const (
	rootZipWorld           = "__zip_root_world__"
	maxRestoreArchiveSize  = int64(64 << 30)
	maxRestoreExtractSize  = int64(256 << 30)
	maxRestoreManifestSize = int64(1 << 20)
)

func scanStatus() appStatus {
	bedrock := findBedrockWorldDirs()
	java := findJavaWorldDir()
	return appStatus{
		BedrockPaths: bedrock,
		JavaPath:     java,
		Worlds:       scanAllWorlds(bedrock, java),
		DefaultDest:  defaultBackupDir(),
	}
}

func (s *appState) beginOperation() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return errors.New("正在处理，请稍等")
	}
	s.busy = true
	return nil
}

func (s *appState) endOperation() {
	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
}

func (s *appState) gameRunning() bool {
	if s.isGameRunning != nil {
		return s.isGameRunning()
	}
	return minecraftRunning()
}

func (s *appState) createBackup(dest string) (operationResult, error) {
	if err := s.beginOperation(); err != nil {
		return operationResult{}, err
	}
	defer s.endOperation()
	if s.gameRunning() {
		return operationResult{}, errors.New("检测到 Minecraft 正在运行。请先关闭游戏，再备份。")
	}

	dest = strings.TrimSpace(dest)
	if dest == "" {
		dest = defaultBackupDir()
	}
	status := scanStatus()
	if len(status.BedrockPaths) == 0 && status.JavaPath == "" {
		return operationResult{}, errors.New("没有找到 Minecraft 世界目录。请确认 Minecraft 已安装并至少创建过一个世界。")
	}
	if len(status.Worlds) == 0 {
		return operationResult{}, errors.New("没有找到可备份的世界存档。")
	}

	name := "Minecraft-Worlds-" + time.Now().Format("20060102-150405") + ".zip"
	out, err := uniqueBackupFile(filepath.Join(dest, name))
	if err != nil {
		return operationResult{}, fmt.Errorf("创建备份文件失败: %w", err)
	}
	if backupPathInsideWorld(out, status.Worlds) {
		return operationResult{}, errors.New("备份位置不能放在 Minecraft 世界文件夹里面，请换到文档、桌面或移动硬盘。")
	}
	if err := os.MkdirAll(dest, 0755); err != nil {
		return operationResult{}, fmt.Errorf("创建备份目录失败: %w", err)
	}
	count, err := zipWorlds(out, status.Worlds)
	if err != nil {
		return operationResult{}, fmt.Errorf("备份失败: %w", err)
	}
	return operationResult{
		Message: fmt.Sprintf("备份完成：%d 个世界", count),
		File:    out,
	}, nil
}

func (s *appState) restoreBackup(path string) (operationResult, error) {
	if err := s.beginOperation(); err != nil {
		return operationResult{}, err
	}
	defer s.endOperation()
	if s.gameRunning() {
		return operationResult{}, errors.New("检测到 Minecraft 正在运行。请先关闭游戏，再恢复。")
	}
	if strings.TrimSpace(path) == "" {
		return operationResult{}, errors.New("请选择备份 zip 或 mcworld 文件。")
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".zip" && ext != ".mcworld" {
		return operationResult{}, errors.New("只支持恢复 .zip 或 .mcworld 文件。")
	}
	count, err := restoreZip(path)
	if err != nil {
		return operationResult{}, fmt.Errorf("恢复失败: %w", err)
	}
	return operationResult{
		Message: fmt.Sprintf("恢复完成：%d 个世界。请重新打开 Minecraft 查看。", count),
	}, nil
}

func findBedrockWorldDirs() []string {
	local := os.Getenv("LOCALAPPDATA")
	roaming := os.Getenv("APPDATA")

	var patterns []string
	if roaming != "" {
		patterns = append(patterns,
			filepath.Join(roaming, "Minecraft Bedrock", "Users", "*", "games", "com.mojang", "minecraftWorlds"),
			filepath.Join(roaming, "Minecraft Bedrock", "users", "*", "games", "com.mojang", "minecraftWorlds"),
			filepath.Join(roaming, "Minecraft Bedrock", "games", "com.mojang", "minecraftWorlds"),
			filepath.Join(roaming, "Minecraft Bedrock Preview", "Users", "*", "games", "com.mojang", "minecraftWorlds"),
			filepath.Join(roaming, "Minecraft Bedrock Preview", "users", "*", "games", "com.mojang", "minecraftWorlds"),
		)
	}
	if local != "" {
		patterns = append(patterns,
			filepath.Join(local, "Packages", "Microsoft.MinecraftUWP_*", "LocalState", "games", "com.mojang", "minecraftWorlds"),
			filepath.Join(local, "Packages", "*MinecraftUWP*", "LocalState", "games", "com.mojang", "minecraftWorlds"),
			filepath.Join(local, "Packages", "Microsoft.MinecraftWindowsBeta_*", "LocalState", "games", "com.mojang", "minecraftWorlds"),
		)
	}

	var dirs []string
	seen := map[string]bool{}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, p := range matches {
			dirs = appendExistingDir(dirs, seen, p)
		}
	}

	if roaming != "" {
		dirs = appendNamedDirs(dirs, seen, filepath.Join(roaming, "Minecraft Bedrock"), "minecraftWorlds")
		dirs = appendNamedDirs(dirs, seen, filepath.Join(roaming, "Minecraft Bedrock Preview"), "minecraftWorlds")
	}
	if local != "" {
		dirs = appendNamedDirs(dirs, seen, filepath.Join(local, "Packages", "Microsoft.MinecraftUWP_8wekyb3d8bbwe", "LocalState"), "minecraftWorlds")
	}

	sort.Strings(dirs)
	return dirs
}

func appendExistingDir(dirs []string, seen map[string]bool, path string) []string {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		key := strings.ToLower(filepath.Clean(path))
		if !seen[key] {
			seen[key] = true
			dirs = append(dirs, path)
		}
	}
	return dirs
}

func appendNamedDirs(dirs []string, seen map[string]bool, root, name string) []string {
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return dirs
	}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), name) {
			dirs = appendExistingDir(dirs, seen, path)
			return filepath.SkipDir
		}
		return nil
	})
	return dirs
}

func findJavaWorldDir() string {
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		return ""
	}
	p := filepath.Join(appdata, ".minecraft", "saves")
	if info, err := os.Stat(p); err == nil && info.IsDir() {
		return p
	}
	return ""
}

func scanAllWorlds(bedrockDirs []string, javaDir string) []world {
	worlds := scanWorlds("Bedrock", bedrockDirs)
	if javaDir != "" {
		worlds = append(worlds, scanWorlds("Java", []string{javaDir})...)
	}
	sort.Slice(worlds, func(i, j int) bool {
		left := worlds[i].Edition + strings.ToLower(worlds[i].Name)
		right := worlds[j].Edition + strings.ToLower(worlds[j].Name)
		return left < right
	})
	return worlds
}

func scanWorlds(edition string, baseDirs []string) []world {
	var worlds []world
	for _, base := range baseDirs {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			p := filepath.Join(base, entry.Name())
			if !looksLikeWorld(p) {
				continue
			}
			worlds = append(worlds, world{
				Edition: edition,
				ID:      entry.Name(),
				Name:    readWorldName(p, entry.Name()),
				Path:    p,
			})
		}
	}
	return worlds
}

func looksLikeWorld(path string) bool {
	known := []string{"level.dat", "levelname.txt", "db"}
	for _, name := range known {
		if _, err := os.Stat(filepath.Join(path, name)); err == nil {
			return true
		}
	}
	return false
}

func readWorldName(path, fallback string) string {
	data, err := os.ReadFile(filepath.Join(path, "levelname.txt"))
	if err != nil {
		return fallback
	}
	name := strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	if name == "" {
		return fallback
	}
	return name
}

func defaultBackupDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", "MinecraftWorldBackups")
	}
	return filepath.Join(home, "Documents", "MinecraftWorldBackups")
}

func zipWorlds(out string, worlds []world) (int, error) {
	return zipWorldsWithOpen(out, worlds, os.Open)
}

func zipWorldsWithOpen(out string, worlds []world, openWorldFile func(string) (*os.File, error)) (int, error) {
	file, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
	if err != nil {
		return 0, err
	}

	zw := zip.NewWriter(file)
	closed := false
	defer func() {
		if !closed {
			_ = zw.Close()
			_ = file.Close()
			_ = os.Remove(out)
		}
	}()

	manifestWorlds := make([]world, 0, len(worlds))
	usedRoots := map[string]bool{}
	for i := range worlds {
		worlds[i].Root = uniqueBackupRootName(backupRootName(worlds[i]), usedRoots)
		manifestWorlds = append(manifestWorlds, worlds[i])
	}
	if err := writeManifest(zw, manifestWorlds); err != nil {
		return 0, err
	}

	for _, w := range worlds {
		root := w.Path
		prefix := w.Root
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == root {
				return nil
			}

			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			zipName := filepath.ToSlash(filepath.Join(prefix, rel))
			info, err := d.Info()
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil
			}

			if d.IsDir() {
				_, err = zw.CreateHeader(&zip.FileHeader{
					Name:     zipName + "/",
					Method:   zip.Deflate,
					Modified: info.ModTime(),
				})
				return err
			}

			header, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			header.Name = zipName
			header.Method = zip.Deflate

			writer, err := zw.CreateHeader(header)
			if err != nil {
				return err
			}
			src, err := openWorldFile(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(writer, src)
			closeErr := src.Close()
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		})
		if err != nil {
			return 0, err
		}
	}
	if err := zw.Close(); err != nil {
		_ = file.Close()
		return 0, err
	}
	if err := file.Close(); err != nil {
		return 0, err
	}
	closed = true
	return len(worlds), nil
}

func backupRootName(w world) string {
	return safeName(w.Edition) + "_" + safeName(w.Name) + "_" + safeName(w.ID)
}

func uniqueBackupRootName(base string, used map[string]bool) string {
	for i := 1; ; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s_%d", base, i)
		}
		key := strings.ToLower(name)
		if !used[key] {
			used[key] = true
			return name
		}
	}
}

func writeManifest(zw *zip.Writer, worlds []world) error {
	header := &zip.FileHeader{
		Name:     "__minecraft_backup_manifest.json",
		Method:   zip.Deflate,
		Modified: time.Now(),
	}
	writer, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	return json.NewEncoder(writer).Encode(map[string]any{
		"version":   1,
		"createdAt": time.Now().Format(time.RFC3339),
		"worlds":    worlds,
	})
}

func restoreZip(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, errors.New("请选择普通 zip 或 mcworld 文件")
	}
	if info.Size() > maxRestoreArchiveSize {
		return 0, fmt.Errorf("备份文件超过 %s", formatBytes(maxRestoreArchiveSize))
	}
	reader, err := zip.NewReader(file, info.Size())
	if err != nil {
		return 0, err
	}
	for _, f := range reader.File {
		if strings.HasPrefix(f.Name, "/") || strings.HasPrefix(f.Name, "\\") {
			return 0, errors.New("zip 路径不安全")
		}
	}

	manifest := readManifest(reader.File)
	groups := groupZipWorlds(reader.File, manifest)
	if len(groups) == 0 {
		return 0, errors.New("zip 里没有找到世界文件夹")
	}

	count := 0
	for _, g := range groups {
		destBase := restoreBaseDir(g.Edition)
		if destBase == "" {
			return count, fmt.Errorf("找不到 %s 的恢复目录", g.Edition)
		}
		if err := os.MkdirAll(destBase, 0755); err != nil {
			return count, err
		}
		targetName := g.ID
		if targetName == "" {
			targetName = g.Root
		}
		target, err := uniqueRestorePath(filepath.Join(destBase, safeName(targetName)))
		if err != nil {
			return count, err
		}
		if err := extractZipGroup(reader.File, g.Root, target); err != nil {
			_ = os.RemoveAll(target)
			return count, err
		}
		count++
	}
	return count, nil
}

func readManifest(files []*zip.File) map[string]world {
	for _, f := range files {
		if f.Name != "__minecraft_backup_manifest.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, maxRestoreManifestSize+1))
		if err != nil || int64(len(data)) > maxRestoreManifestSize {
			return nil
		}
		var manifest struct {
			Worlds []world `json:"worlds"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			return nil
		}
		result := map[string]world{}
		for _, w := range manifest.Worlds {
			if w.Root != "" {
				result[w.Root] = w
			}
		}
		return result
	}
	return nil
}

func groupZipWorlds(files []*zip.File, manifest map[string]world) []world {
	if w, ok := rootLevelWorld(files); ok {
		return []world{w}
	}

	type worldMarkers struct {
		hasLevel, hasBedrockDB, hasJavaRegion bool
	}
	markers := map[string]worldMarkers{}
	for _, f := range files {
		name := strings.TrimRight(f.Name, "/")
		if name == "" || strings.HasPrefix(name, "__minecraft_backup_manifest.json") {
			continue
		}
		parts := strings.SplitN(name, "/", 2)
		root := parts[0]
		if root == "" || unsafeZipRoot(root) {
			continue
		}
		m := markers[root]
		if len(parts) == 2 {
			rel := strings.ToLower(parts[1])
			m.hasLevel = m.hasLevel || rel == "level.dat" || rel == "levelname.txt"
			m.hasBedrockDB = m.hasBedrockDB || rel == "db" || strings.HasPrefix(rel, "db/")
			m.hasJavaRegion = m.hasJavaRegion || rel == "region" || strings.HasPrefix(rel, "region/")
		}
		markers[root] = m
	}
	var worlds []world
	for root, m := range markers {
		if w, ok := manifest[root]; ok {
			worlds = append(worlds, w)
			continue
		}
		if !m.hasLevel && !m.hasBedrockDB && !m.hasJavaRegion {
			continue
		}
		edition := "Bedrock"
		if (m.hasJavaRegion && !m.hasBedrockDB) || strings.HasPrefix(strings.ToLower(root), "java_") {
			edition = "Java"
		}
		worlds = append(worlds, world{Edition: edition, ID: root, Name: root, Root: root})
	}
	sort.Slice(worlds, func(i, j int) bool { return worlds[i].Root < worlds[j].Root })
	return worlds
}

func rootLevelWorld(files []*zip.File) (world, bool) {
	hasLevel := false
	hasBedrockDB := false
	hasJavaRegion := false
	name := ""
	for _, f := range files {
		clean := strings.TrimRight(f.Name, "/")
		if clean == "" || clean == "__minecraft_backup_manifest.json" || strings.Contains(clean, "/") {
			if clean == "db" || strings.HasPrefix(clean, "db/") {
				hasBedrockDB = true
			}
			if clean == "region" || strings.HasPrefix(clean, "region/") {
				hasJavaRegion = true
			}
			continue
		}
		switch strings.ToLower(clean) {
		case "level.dat":
			hasLevel = true
		case "levelname.txt":
			hasLevel = true
			name = readZipTextFile(f)
		}
	}
	if !hasLevel {
		return world{}, false
	}
	edition := "Bedrock"
	if hasJavaRegion && !hasBedrockDB {
		edition = "Java"
	}
	if strings.TrimSpace(name) == "" {
		name = "Restored World"
	}
	return world{
		Edition: edition,
		ID:      safeName(name),
		Name:    name,
		Root:    rootZipWorld,
	}, true
}

func readZipTextFile(f *zip.File) string {
	rc, err := f.Open()
	if err != nil {
		return ""
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 4096))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
}

func unsafeZipRoot(root string) bool {
	return root == "." || root == ".." || strings.Contains(root, "\\")
}

func restoreBaseDir(edition string) string {
	if strings.EqualFold(edition, "Java") {
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			return ""
		}
		return filepath.Join(appdata, ".minecraft", "saves")
	}
	return preferredBedrockWorldDir()
}

func preferredBedrockWorldDir() string {
	dirs := findBedrockWorldDirs()
	best := ""
	bestCount := -1
	for _, dir := range dirs {
		count := len(scanWorlds("Bedrock", []string{dir}))
		if count > bestCount {
			best = dir
			bestCount = count
		}
	}
	if best != "" {
		return best
	}
	if roaming := os.Getenv("APPDATA"); roaming != "" {
		return filepath.Join(roaming, "Minecraft Bedrock", "Users", "Shared", "games", "com.mojang", "minecraftWorlds")
	}
	return ""
}

func uniqueRestorePath(path string) (string, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path, nil
	} else if err != nil {
		return "", err
	}
	stamp := time.Now().Format("20060102-150405")
	for i := 1; ; i++ {
		candidate := path + "_restored_" + stamp
		if i > 1 {
			candidate = fmt.Sprintf("%s_%d", candidate, i)
		}
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
}

func extractZipGroup(files []*zip.File, root, target string) error {
	if root != rootZipWorld && unsafeZipRoot(root) {
		return errors.New("zip 路径不安全")
	}
	rootPrefix := root + "/"
	cleanTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}

	for _, f := range files {
		if f.FileInfo().Mode()&os.ModeSymlink != 0 {
			return errors.New("zip 包含不支持的符号链接")
		}
		if _, err := zipGroupDestination(f.Name, root, rootPrefix, target, cleanTarget); err != nil {
			return err
		}
	}

	var extracted int64
	for _, f := range files {
		dest, err := zipGroupDestination(f.Name, root, rootPrefix, target, cleanTarget)
		if err != nil {
			return err
		}
		if dest == "" {
			continue
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		src, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0644
		}
		dst, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			src.Close()
			return err
		}
		n, copyErr := copyWithLimit(dst, src, maxRestoreExtractSize-extracted)
		extracted += n
		closeErr := dst.Close()
		src.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func zipGroupDestination(name, root, rootPrefix, target, cleanTarget string) (string, error) {
	if root == rootZipWorld {
		cleanName := strings.Trim(name, "/")
		if cleanName == "" || cleanName == "__minecraft_backup_manifest.json" {
			return "", nil
		}
		return safeZipDestination(target, cleanTarget, filepath.FromSlash(cleanName))
	}

	if name == root || !strings.HasPrefix(name, rootPrefix) {
		return "", nil
	}
	rel := strings.TrimPrefix(name, rootPrefix)
	if rel == "" {
		return "", nil
	}
	return safeZipDestination(target, cleanTarget, filepath.FromSlash(rel))
}

func safeZipDestination(target, cleanTarget, rel string) (string, error) {
	dest := filepath.Join(target, rel)
	cleanDest, err := filepath.Abs(dest)
	if err != nil {
		return "", err
	}
	if cleanDest != cleanTarget && !strings.HasPrefix(cleanDest, cleanTarget+string(os.PathSeparator)) {
		return "", errors.New("zip 路径不安全")
	}
	return dest, nil
}

func safeName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "world"
	}
	replacer := strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	s = strings.Trim(replacer.Replace(s), " .")
	if s == "" || s == "." || s == ".." {
		return "world"
	}
	if windowsReservedName(s) {
		return "_" + s
	}
	return s
}

func windowsReservedName(s string) bool {
	base := strings.ToUpper(s)
	if idx := strings.IndexByte(base, '.'); idx >= 0 {
		base = base[:idx]
	}
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return true
	}
	return false
}

func backupPathInsideWorld(out string, worlds []world) bool {
	outAbs, err := filepath.Abs(out)
	if err != nil {
		return false
	}
	outAbs = strings.ToLower(filepath.Clean(outAbs))
	for _, w := range worlds {
		worldAbs, err := filepath.Abs(w.Path)
		if err != nil {
			continue
		}
		worldAbs = strings.ToLower(filepath.Clean(worldAbs))
		if outAbs == worldAbs || strings.HasPrefix(outAbs, worldAbs+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func uniqueBackupFile(path string) (string, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path, nil
	} else if err != nil {
		return "", err
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s_%d%s", base, i, ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
}

func copyWithLimit(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	if limit < 0 {
		return 0, fmt.Errorf("数据超过限制 %s", formatBytes(maxRestoreExtractSize))
	}
	limited := &io.LimitedReader{R: src, N: limit + 1}
	n, err := io.Copy(dst, limited)
	if n > limit {
		return n, fmt.Errorf("数据超过限制 %s", formatBytes(limit))
	}
	return n, err
}

func formatBytes(n int64) string {
	const gib = int64(1 << 30)
	if n >= gib {
		return fmt.Sprintf("%d GiB", n/gib)
	}
	const mib = int64(1 << 20)
	if n >= mib {
		return fmt.Sprintf("%d MiB", n/mib)
	}
	return fmt.Sprintf("%d bytes", n)
}

func minecraftRunning() bool {
	running, err := minecraftProbe()
	// A failed probe is unknown, never permission to read a potentially live world.
	return err != nil || running
}
