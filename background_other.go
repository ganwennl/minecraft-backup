//go:build !windows

package main

import (
	"errors"
	"os"
)

func replaceBackupFile(source, target string) error { return os.Rename(source, target) }
func startupEnabled() (bool, error)                 { return false, errors.New("开机启动仅支持 Windows") }
func setStartupEnabled(bool) error                  { return errors.New("开机启动仅支持 Windows") }
func probeMinecraftRunning() (bool, error) {
	return false, errors.New("游戏进程检测仅支持 Windows")
}
