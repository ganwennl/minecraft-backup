package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSettingsPersistAutomaticAndDestination(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	initial, err := loadSettings()
	if err != nil || initial.Automatic {
		t.Fatalf("defaults: %#v, %v", initial, err)
	}
	want := appSettings{BackupDir: filepath.Join(t.TempDir(), "中文备份"), Automatic: true}
	if err := saveSettings(want); err != nil {
		t.Fatal(err)
	}
	got, err := loadSettings()
	if err != nil || got != want {
		t.Fatalf("settings: %#v, %v", got, err)
	}
	want.Automatic = false
	if err := saveSettings(want); err != nil {
		t.Fatal(err)
	}
	got, err = loadSettings()
	if err != nil || got != want {
		t.Fatalf("updated settings: %#v, %v", got, err)
	}
}

func TestCorruptSettingsNeverEnableAutomatic(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	path, err := settingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"automatic":true,`), 0600); err != nil {
		t.Fatal(err)
	}
	settings, err := loadSettings()
	if err == nil || settings.Automatic {
		t.Fatalf("corrupt settings: %#v, %v", settings, err)
	}
}

func TestSettingsPersistScheduleAndLastSuccess(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	want := appSettings{BackupDir: filepath.Join(t.TempDir(), "scheduled"), Automatic: true, IntervalHours: 24, LastAutomaticAt: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}
	if err := saveSettings(want); err != nil {
		t.Fatal(err)
	}
	got, err := loadSettings()
	if err != nil || got.IntervalHours != 24 || !got.LastAutomaticAt.Equal(want.LastAutomaticAt) {
		t.Fatalf("schedule: %#v, %v", got, err)
	}
	for _, invalid := range []int{-1, 8761} {
		want.IntervalHours = invalid
		if err := saveSettings(want); err == nil {
			t.Fatalf("invalid interval %d accepted", invalid)
		}
	}
}
