//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
)

func configureCommand(cmd *exec.Cmd) {}

func main() {
	fmt.Fprintln(os.Stderr, "Minecraft 地图备份的原生界面需要 Windows。")
	os.Exit(1)
}
