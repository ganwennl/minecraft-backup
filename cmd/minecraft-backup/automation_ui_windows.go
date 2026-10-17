//go:build windows

package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// The original notification-icon callback format is used (no version-4 packing).
type notifyIconData struct {
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Timeout             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                [16]byte
	BalloonIcon         uintptr
}

func enableAutomation() error {
	previous, err := loadSettings()
	if err != nil {
		return err
	}
	settings := previous
	settings.Automatic = true
	if err := saveSettings(settings); err != nil {
		return err
	}
	if err := setStartupEnabled(true); err != nil {
		if rollback := saveSettings(previous); rollback != nil {
			return fmt.Errorf("开机启动失败：%v；恢复设置失败：%w", err, rollback)
		}
		return err
	}
	if other, _, _ := user32.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(utf16(windowClass))), 0); other != 0 {
		pPostMessage.Call(other, wmReload, 0, 0)
	}
	return nil
}

func (a *desktopApp) updateSettingsControls() {
	check := func(id int, enabled bool) {
		value := uintptr(0)
		if enabled {
			value = 1
		}
		pSendMessage.Call(a.controls[id], 0xf1, value, 0)
	}
	check(idAutomatic, a.settings.Automatic)
	check(idStartup, a.startup)
	mode := uintptr(0)
	if a.settings.IntervalHours > 0 {
		mode = 1
	}
	pSendMessage.Call(a.controls[idSchedule], 0x14e, mode, 0)
	hours := a.settings.IntervalHours
	if hours == 0 {
		hours = 12
	}
	setText(a.controls[idHours], strconv.Itoa(hours))
	a.enableHours()
	if a.settings.Automatic {
		setText(a.autoStatus, "自动备份已启用：等待游戏结束，新世界将自动纳入")
	} else {
		setText(a.autoStatus, "自动备份未启用")
	}
}

func (a *desktopApp) reloadSettings() {
	settings, err := loadSettings()
	if err != nil {
		setText(a.autoStatus, "读取设置失败："+err.Error())
		return
	}
	a.settings = settings
	a.startup, _ = startupEnabled()
	setText(a.controls[idDest], settings.BackupDir)
	a.resetMonitor()
	a.updateSettingsControls()
	a.pollAutomation()
}

func (a *desktopApp) commitDestination() error {
	path := strings.TrimSpace(getText(a.controls[idDest]))
	if path == "" {
		path = defaultBackupDir()
	}
	path, err := filepath.Abs(path)
	if err != nil {
		a.message("备份路径无效："+err.Error(), 0x10)
		return err
	}
	if path == a.settings.BackupDir {
		return nil
	}
	settings := a.settings
	settings.BackupDir = path
	settings.LastAutomaticAt = time.Time{}
	if err := saveSettings(settings); err != nil {
		setText(a.controls[idDest], a.settings.BackupDir)
		a.message("保存备份位置失败："+err.Error(), 0x10)
		return err
	}
	a.settings = settings
	setText(a.controls[idDest], path)
	a.resetMonitor()
	return nil
}

func (a *desktopApp) resetMonitor() {
	a.monitor.reset()
	a.monitor.configure(time.Duration(a.settings.IntervalHours)*time.Hour, a.settings.LastAutomaticAt)
}
func (a *desktopApp) enableHours() {
	enabled := uintptr(0)
	if !a.busy && a.settings.IntervalHours > 0 {
		enabled = 1
	}
	pEnableWindow.Call(a.controls[idHours], enabled)
}
func (a *desktopApp) saveSchedule() {
	if a.busy {
		return
	}
	mode, _, _ := pSendMessage.Call(a.controls[idSchedule], 0x147, 0, 0)
	hours := 0
	if mode == 1 {
		var err error
		hours, err = strconv.Atoi(strings.TrimSpace(getText(a.controls[idHours])))
		if err != nil || hours < 1 || hours > 8760 {
			a.message("请填写 1 至 8760 小时，例如 12 或 24。", 0x40)
			a.updateSettingsControls()
			return
		}
	}
	if hours == a.settings.IntervalHours {
		a.enableHours()
		return
	}
	settings := a.settings
	settings.IntervalHours = hours
	if err := saveSettings(settings); err != nil {
		a.message("保存备份间隔失败："+err.Error(), 0x10)
	} else {
		a.settings = settings
		a.resetMonitor()
	}
	a.updateSettingsControls()
	a.pollAutomation()
}

func (a *desktopApp) showWindow() {
	// Two calls also make an explicit activation visible after a hidden launcher.
	user32.NewProc("ShowWindow").Call(a.window, 9)
	user32.NewProc("ShowWindow").Call(a.window, 5)
	user32.NewProc("SetForegroundWindow").Call(a.window)
	user32.NewProc("UpdateWindow").Call(a.window)
}

func (a *desktopApp) trayData(text string) notifyIconData {
	data := notifyIconData{Window: a.window, ID: 1, Flags: 1 | 2 | 4, Callback: wmTray, Icon: a.icon}
	data.Size = uint32(unsafe.Sizeof(data))
	encoded, _ := syscall.UTF16FromString(strings.ReplaceAll(text, "\x00", ""))
	copy(data.Tip[:127], encoded)
	return data
}
func (a *desktopApp) installTray() {
	data := a.trayData("Minecraft 地图备份")
	ok, _, _ := shell32.NewProc("Shell_NotifyIconW").Call(0, uintptr(unsafe.Pointer(&data)))
	a.trayPresent = ok != 0
	if !a.trayPresent && a.window != 0 {
		a.showWindow()
	}
}
func (a *desktopApp) removeTray() {
	if !a.trayPresent {
		return
	}
	data := a.trayData("")
	shell32.NewProc("Shell_NotifyIconW").Call(2, uintptr(unsafe.Pointer(&data)))
	a.trayPresent = false
}
func (a *desktopApp) trayTooltip(text string) {
	if !a.trayPresent {
		return
	}
	data := a.trayData(text)
	data.Flags = 4
	shell32.NewProc("Shell_NotifyIconW").Call(1, uintptr(unsafe.Pointer(&data)))
}
func (a *desktopApp) trayMenu() {
	a.modalDepth++
	defer func() { a.modalDepth-- }()
	menu, _, _ := user32.NewProc("CreatePopupMenu").Call()
	if menu == 0 {
		a.showWindow()
		return
	}
	defer user32.NewProc("DestroyMenu").Call(menu)
	for _, item := range []struct {
		id   int
		text string
	}{{idShow, "打开窗口"}, {idOpen, "打开备份文件夹"}, {idExit, "退出程序"}} {
		user32.NewProc("AppendMenuW").Call(menu, 0, uintptr(item.id), uintptr(unsafe.Pointer(utf16(item.text))))
	}
	var point winPoint
	user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&point)))
	user32.NewProc("SetForegroundWindow").Call(a.window)
	id, _, _ := user32.NewProc("TrackPopupMenu").Call(menu, 0x100|0x2, uintptr(point.X), uintptr(point.Y), 0, a.window, 0)
	pPostMessage.Call(a.window, 0, 0, 0)
	if id != 0 {
		a.command(int(id))
	}
}

func autoWorldKey(worlds []world) string {
	keys := make([]string, 0, len(worlds))
	for _, w := range worlds {
		keys = append(keys, strings.ToLower(w.Edition+"\x00"+filepath.Clean(w.Path)))
	}
	sort.Strings(keys)
	return strings.Join(keys, "\n")
}
func (a *desktopApp) pollAutomation() {
	if !a.settings.Automatic || a.busy || a.polling || a.modalDepth > 0 {
		return
	}
	a.polling = true
	go func() {
		running, err := minecraftProbe()
		status := appStatus{}
		if err == nil {
			status = scanStatus()
		}
		a.deliver(desktopResult{Poll: true, Running: running, Err: err, Status: status})
	}()
}
func (a *desktopApp) completePoll(result desktopResult) {
	a.polling = false
	if !a.settings.Automatic || a.busy || a.modalDepth > 0 {
		return
	}
	due := a.monitor.observe(time.Now(), result.Running, result.Err, autoWorldKey(result.Status.Worlds))
	if result.Err != nil {
		setText(a.autoStatus, "暂时无法确认游戏状态，自动备份等待重试")
		return
	}
	if result.Running {
		setText(a.autoStatus, "Minecraft 正在运行，等待游戏退出")
		return
	}
	if !due {
		if a.settings.IntervalHours > 0 && !a.settings.LastAutomaticAt.IsZero() && time.Now().Before(a.settings.LastAutomaticAt.Add(time.Duration(a.settings.IntervalHours)*time.Hour)) {
			setText(a.autoStatus, fmt.Sprintf("定时备份：每 %d 小时检查一次；下次 %s", a.settings.IntervalHours, a.settings.LastAutomaticAt.Add(time.Duration(a.settings.IntervalHours)*time.Hour).Local().Format("01-02 15:04")))
		} else if a.monitor.pending {
			setText(a.autoStatus, "游戏未运行，等待存档稳定后检查变化")
		}
		return
	}
	a.setBusy(true, true)
	setText(a.autoStatus, "正在检查并自动备份变化的世界…")
	dest := a.settings.BackupDir
	go func() {
		result, err := a.state.autoBackup(dest)
		a.deliver(desktopResult{Automatic: true, Auto: result, Err: err, Status: scanStatus()})
	}()
}
