package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type appSettings struct {
	BackupDir       string    `json:"backupDir"`
	Automatic       bool      `json:"automatic"`
	IntervalHours   int       `json:"intervalHours"`
	LastAutomaticAt time.Time `json:"lastAutomaticAt,omitempty"`
}

func settingsPath() (string, error) {
	root := os.Getenv("LOCALAPPDATA")
	if root == "" {
		var err error
		root, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(root, "MinecraftWorldBackup", "settings.json"), nil
}

func loadSettings() (appSettings, error) {
	settings := appSettings{BackupDir: defaultBackupDir()}
	path, err := settingsPath()
	if err != nil {
		return settings, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return appSettings{BackupDir: defaultBackupDir()}, fmt.Errorf("读取设置失败：%w", err)
	}
	if strings.TrimSpace(settings.BackupDir) == "" {
		settings.BackupDir = defaultBackupDir()
	}
	if settings.IntervalHours < 0 || settings.IntervalHours > 8760 {
		return appSettings{BackupDir: defaultBackupDir()}, errors.New("定时间隔必须为 1 至 8760 小时")
	}
	return settings, nil
}

func saveSettings(settings appSettings) error {
	if settings.IntervalHours < 0 || settings.IntervalHours > 8760 {
		return errors.New("定时间隔必须为 1 至 8760 小时")
	}
	path, err := settingsPath()
	if err != nil {
		return err
	}
	if strings.TrimSpace(settings.BackupDir) == "" {
		settings.BackupDir = defaultBackupDir()
	}
	settings.BackupDir, err = filepath.Abs(settings.BackupDir)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceBackupFile(tmp.Name(), path)
}
