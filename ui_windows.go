//go:build windows

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	windowClass    = "MinecraftWorldBackupNativeWindow"
	windowTitle    = "Minecraft 地图备份"
	wmDone         = 0x8001
	wmTray         = 0x8002
	wmReload       = 0x8003
	idDest         = 101
	idBrowseDest   = 102
	idBackup       = 103
	idSource       = 104
	idBrowseSource = 105
	idRestore      = 106
	idRefresh      = 107
	idOpen         = 108
	idExit         = 109
	idAutomatic    = 110
	idStartup      = 111
	idShow         = 112
	idSchedule     = 113
	idHours        = 114
)

var (
	user32         = syscall.NewLazyDLL("user32.dll")
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	gdi32          = syscall.NewLazyDLL("gdi32.dll")
	shell32        = syscall.NewLazyDLL("shell32.dll")
	ole32          = syscall.NewLazyDLL("ole32.dll")
	comdlg32       = syscall.NewLazyDLL("comdlg32.dll")
	pCreateWindow  = user32.NewProc("CreateWindowExW")
	pSendMessage   = user32.NewProc("SendMessageW")
	pSetText       = user32.NewProc("SetWindowTextW")
	pGetText       = user32.NewProc("GetWindowTextW")
	pGetTextLength = user32.NewProc("GetWindowTextLengthW")
	pMoveWindow    = user32.NewProc("MoveWindow")
	pEnableWindow  = user32.NewProc("EnableWindow")
	pPostMessage   = user32.NewProc("PostMessageW")
	pMessageBox    = user32.NewProc("MessageBoxW")
	pDefWindowProc = user32.NewProc("DefWindowProcW")
	nativeUI       *desktopApp
)

type winPoint struct{ X, Y int32 }
type winRect struct{ Left, Top, Right, Bottom int32 }
type winMessage struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          winPoint
	Private        uint32
}
type windowClassEx struct {
	Size, Style                        uint32
	Procedure                          uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}
type minMaxInfo struct{ Reserved, MaxSize, MaxPosition, MinTrackSize, MaxTrackSize winPoint }

// OPENFILENAMEW follows the Windows SDK layout (including pointer alignment).
type openFileName struct {
	Size                         uint32
	Owner, Instance              uintptr
	Filter, CustomFilter         *uint16
	MaxCustomFilter, FilterIndex uint32
	File                         *uint16
	MaxFile                      uint32
	FileTitle                    *uint16
	MaxFileTitle                 uint32
	InitialDir, Title            *uint16
	Flags                        uint32
	FileOffset, FileExtension    uint16
	DefaultExtension             *uint16
	CustomData, Hook             uintptr
	TemplateName                 *uint16
	Reserved                     uintptr
	Reserved2, FlagsEx           uint32
}
type browseInfo struct {
	Owner, Root        uintptr
	DisplayName, Title *uint16
	Flags              uint32
	Callback, Param    uintptr
	Image              int32
}

type desktopResult struct {
	Status    appStatus
	Operation operationResult
	Err       error
	Scan      bool
	Poll      bool
	Automatic bool
	Running   bool
	Auto      autoBackupResult
}
type desktopApp struct {
	window, font, titleFont                                                            uintptr
	controls                                                                           map[int]uintptr
	title, subtitle, destLabel, restoreLabel, tip, worldsLabel, worldsText, statusText uintptr
	state                                                                              appState
	dpi                                                                                int
	busy, protectClose                                                                 bool // Owned by the UI thread.
	mailboxMu                                                                          sync.Mutex
	results                                                                            []desktopResult
	settings                                                                           appSettings
	autoStatus                                                                         uintptr
	monitor                                                                            autoMonitor
	polling, trayPresent, startup, pendingReload                                       bool
	icon                                                                               uintptr
	taskbarCreated                                                                     uint32
	modalDepth                                                                         int
	hoursLabel                                                                         uintptr
}

func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

func utf16(s string) *uint16 {
	// World names and error messages can contain NUL; keep them displayable.
	p, _ := syscall.UTF16PtrFromString(strings.ReplaceAll(s, "\x00", "�"))
	return p
}

func main() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	background := flag.Bool("background", false, "在托盘后台启动")
	enable := flag.Bool("enable-automation", false, "开启自动备份和当前用户开机启动")
	noAuto := flag.Bool("no-auto", false, "仅本次禁用自动备份（用于隔离测试）")
	flag.Parse()
	var err error
	if *enable {
		err = enableAutomation()
	} else {
		err = runDesktop(*background, *noAuto)
	}
	if err != nil {
		pMessageBox.Call(0, uintptr(unsafe.Pointer(utf16(err.Error()))), uintptr(unsafe.Pointer(utf16(windowTitle))), 0x10)
		os.Exit(1)
	}
}

func runDesktop(background, noAuto bool) error {
	// A named mutex prevents two instances from writing/restoring simultaneously.
	mutex, _, lastErr := kernel32.NewProc("CreateMutexW").Call(0, 0, uintptr(unsafe.Pointer(utf16("Local\\MinecraftWorldBackupNative"))))
	if mutex == 0 {
		return fmt.Errorf("创建程序锁失败：%w", lastErr)
	}
	defer syscall.CloseHandle(syscall.Handle(mutex))
	if lastErr == syscall.ERROR_ALREADY_EXISTS {
		other, _, _ := user32.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(utf16(windowClass))), 0)
		if other != 0 && !background {
			user32.NewProc("ShowWindow").Call(other, 9)
			user32.NewProc("SetForegroundWindow").Call(other)
		}
		return nil
	}
	// System DPI awareness keeps native controls sharp without manual monitor transitions.
	user32.NewProc("SetProcessDPIAware").Call()
	hr, _, _ := ole32.NewProc("OleInitialize").Call(0)
	if int32(hr) < 0 {
		return fmt.Errorf("初始化文件选择器失败：0x%x", hr)
	}
	defer ole32.NewProc("OleUninitialize").Call()
	commonControls := [2]uint32{8, 0x4000} // ICC_STANDARD_CLASSES
	if ok, _, err := syscall.NewLazyDLL("comctl32.dll").NewProc("InitCommonControlsEx").Call(uintptr(unsafe.Pointer(&commonControls))); ok == 0 {
		return fmt.Errorf("初始化原生控件失败：%w", err)
	}
	settings, settingsErr := loadSettings()
	if noAuto {
		settings.Automatic = false
	}
	app := &desktopApp{controls: make(map[int]uintptr), dpi: 96, settings: settings}
	app.resetMonitor()
	app.startup, _ = startupEnabled()
	if proc := user32.NewProc("GetDpiForSystem"); proc.Find() == nil {
		if dpi, _, _ := proc.Call(); dpi != 0 {
			app.dpi = int(dpi)
		}
	}
	nativeUI = app
	defer func() { nativeUI = nil }()
	instance, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	cursor, _, _ := user32.NewProc("LoadCursorW").Call(0, 32512)
	icons, err := loadDesktopIcons(instance)
	if err != nil {
		return err
	}
	defer icons.close()
	app.icon = icons.small
	created, _, _ := user32.NewProc("RegisterWindowMessageW").Call(uintptr(unsafe.Pointer(utf16("TaskbarCreated"))))
	app.taskbarCreated = uint32(created)
	wc := windowClassEx{Instance: instance, Procedure: syscall.NewCallback(windowProcedure), Cursor: cursor, Icon: icons.large, SmallIcon: icons.small, Background: 6, Name: utf16(windowClass)}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if atom, _, err := user32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		return fmt.Errorf("注册窗口失败：%w", err)
	}
	defer func() {
		user32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(wc.Name)), instance)
	}()
	windowWidth, windowHeight := app.scale(980), app.scale(860)
	x, y := uintptr(0x80000000), uintptr(0x80000000)
	var work winRect
	if ok, _, _ := user32.NewProc("SystemParametersInfoW").Call(48, 0, uintptr(unsafe.Pointer(&work)), 0); ok != 0 {
		windowWidth = min(windowWidth, int(work.Right-work.Left))
		windowHeight = min(windowHeight, int(work.Bottom-work.Top))
		x = uintptr(int(work.Left) + (int(work.Right-work.Left)-windowWidth)/2)
		y = uintptr(int(work.Top) + (int(work.Bottom-work.Top)-windowHeight)/2)
	}
	hwnd, _, err := pCreateWindow.Call(0x10000, uintptr(unsafe.Pointer(wc.Name)), uintptr(unsafe.Pointer(utf16(windowTitle))), 0x02cf0000,
		x, y, uintptr(windowWidth), uintptr(windowHeight), 0, 0, instance, 0)
	if hwnd == 0 {
		return fmt.Errorf("创建窗口失败：%w", err)
	}
	app.window = hwnd
	icons.applyWindow(hwnd)
	defer func() {
		if exists, _, _ := user32.NewProc("IsWindow").Call(hwnd); exists != 0 {
			user32.NewProc("DestroyWindow").Call(hwnd)
		}
		gdi32.NewProc("DeleteObject").Call(app.font)
		gdi32.NewProc("DeleteObject").Call(app.titleFont)
	}()
	if err := app.createControls(); err != nil {
		user32.NewProc("DestroyWindow").Call(hwnd)
		return err
	}
	if err := icons.applyButtons(app, instance); err != nil {
		return err
	}
	app.installTray()
	if !background || !app.trayPresent {
		app.showWindow()
	}
	user32.NewProc("SetTimer").Call(hwnd, 1, 15000, 0)
	app.scan()
	if settingsErr != nil {
		setText(app.autoStatus, "设置读取失败，自动备份未启用："+settingsErr.Error())
	}
	var msg winMessage
	for {
		ret, _, err := user32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) == -1 {
			return fmt.Errorf("读取窗口消息失败：%w", err)
		}
		if ret == 0 {
			break
		}
		if handled, _, _ := user32.NewProc("IsDialogMessageW").Call(hwnd, uintptr(unsafe.Pointer(&msg))); handled != 0 {
			continue
		}
		user32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
		user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
	}
	return nil
}

func (a *desktopApp) scale(n int) int { return n * a.dpi / 96 }
func (a *desktopApp) makeFont(size, weight int) uintptr {
	f, _, _ := gdi32.NewProc("CreateFontW").Call(uintptr(int32(-a.scale(size))), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0,
		uintptr(unsafe.Pointer(utf16("Microsoft YaHei UI"))))
	return f
}
func (a *desktopApp) control(class, text string, style uintptr, id int) (uintptr, error) {
	extended := uintptr(0)
	if class == "EDIT" {
		extended = 0x200
	}
	h, _, err := pCreateWindow.Call(extended, uintptr(unsafe.Pointer(utf16(class))), uintptr(unsafe.Pointer(utf16(text))), style|0x50000000,
		0, 0, 0, 0, a.window, uintptr(id), 0, 0)
	if h == 0 {
		return 0, fmt.Errorf("创建控件失败：%w", err)
	}
	pSendMessage.Call(h, 0x30, a.font, 1)
	if id != 0 {
		a.controls[id] = h
	}
	return h, nil
}
func (a *desktopApp) createControls() error {
	a.font, a.titleFont = a.makeFont(15, 400), a.makeFont(25, 600)
	var err error
	create := func(dst *uintptr, class, text string, style uintptr, id int) {
		if err != nil {
			return
		}
		*dst, err = a.control(class, text, style, id)
	}
	create(&a.title, "STATIC", "Minecraft 地图备份", 0, 0)
	pSendMessage.Call(a.title, 0x30, a.titleFont, 1)
	create(&a.subtitle, "STATIC", "自动查找基岩版与 Java 版世界，一键备份和恢复。", 0, 0)
	var h uintptr
	create(&h, "BUTTON", "自动备份（每世界一份，仅更新变化）", 0x10003, idAutomatic)
	create(&h, "BUTTON", "开机启动", 0x10003, idStartup)
	create(&h, "COMBOBOX", "", 0x210003, idSchedule)
	for _, text := range []string{"每次游戏退出后", "定时备份（游戏关闭时执行）"} {
		pSendMessage.Call(h, 0x143, 0, uintptr(unsafe.Pointer(utf16(text))))
	}
	create(&h, "EDIT", "12", 0x12000, idHours)
	pSendMessage.Call(h, 0xc5, 4, 0)
	create(&a.hoursLabel, "STATIC", "小时（可填 12、24 等）", 0, 0)
	create(&a.autoStatus, "STATIC", "自动备份未启用", 0, 0)
	create(&a.destLabel, "STATIC", "备份保存位置", 0, 0)
	create(&h, "EDIT", a.settings.BackupDir, 0x10080, idDest)
	create(&h, "BUTTON", "选择文件夹…", 0x10000, idBrowseDest)
	create(&h, "BUTTON", "开始备份", 0x10001, idBackup)
	create(&a.restoreLabel, "STATIC", "恢复文件（.zip / .mcworld）", 0, 0)
	create(&h, "EDIT", "", 0x10080, idSource)
	create(&h, "BUTTON", "选择文件…", 0x10000, idBrowseSource)
	create(&h, "BUTTON", "开始恢复", 0x10000, idRestore)
	create(&a.tip, "STATIC", "恢复时同名世界会另存；启用自动备份后，关闭窗口将留在托盘。", 0, 0)
	create(&a.worldsLabel, "STATIC", "正在查找世界…", 0, 0)
	create(&h, "BUTTON", "重新扫描", 0x10000, idRefresh)
	create(&a.worldsText, "EDIT", "", 0x300944, 0)
	create(&a.statusText, "EDIT", "准备就绪", 0x200844, 0)
	create(&h, "BUTTON", "打开备份文件夹", 0x10000, idOpen)
	create(&h, "BUTTON", "退出", 0x10000, idExit)
	// Permit large world lists and long paths in read-only multiline controls.
	pSendMessage.Call(a.worldsText, 0xc5, 0x7ffffffe, 0)
	pSendMessage.Call(a.statusText, 0xc5, 0x7ffffffe, 0)
	a.updateSettingsControls()
	a.layout()
	return err
}

func (a *desktopApp) layout() {
	if a.title == 0 {
		return
	}
	var rc winRect
	user32.NewProc("GetClientRect").Call(a.window, uintptr(unsafe.Pointer(&rc)))
	w, h := int(rc.Right)*96/a.dpi, int(rc.Bottom)*96/a.dpi
	move := func(hwnd uintptr, x, y, width, height int) {
		pMoveWindow.Call(hwnd, uintptr(a.scale(x)), uintptr(a.scale(y)), uintptr(a.scale(width)), uintptr(a.scale(height)), 1)
	}
	move(a.title, 24, 20, w-48, 40)
	move(a.subtitle, 26, 64, w-52, 26)
	move(a.controls[idAutomatic], 24, 103, w-224, 30)
	move(a.controls[idStartup], w-180, 103, 156, 30)
	move(a.controls[idSchedule], 24, 141, 300, 150)
	move(a.controls[idHours], 336, 138, 72, 32)
	move(a.hoursLabel, 420, 142, w-444, 26)
	move(a.autoStatus, 24, 180, w-48, 26)
	move(a.destLabel, 24, 219, w-48, 23)
	move(a.controls[idDest], 24, 246, w-302, 34)
	move(a.controls[idBrowseDest], w-268, 245, 126, 36)
	move(a.controls[idBackup], w-132, 245, 108, 36)
	move(a.restoreLabel, 24, 300, w-48, 23)
	move(a.controls[idSource], 24, 327, w-302, 34)
	move(a.controls[idBrowseSource], w-268, 326, 126, 36)
	move(a.controls[idRestore], w-132, 326, 108, 36)
	move(a.tip, 24, 378, w-48, 26)
	move(a.worldsLabel, 24, 422, w-190, 26)
	move(a.controls[idRefresh], w-150, 414, 126, 34)
	move(a.worldsText, 24, 458, w-48, h-626)
	move(a.statusText, 24, h-151, w-48, 80)
	move(a.controls[idOpen], 24, h-55, 174, 34)
	move(a.controls[idExit], w-132, h-55, 108, 34)
}

func windowProcedure(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	a := nativeUI
	if a != nil {
		if a.taskbarCreated != 0 && message == a.taskbarCreated {
			a.installTray()
			return 0
		}
		switch message {
		case 0x0005: // WM_SIZE
			if a.window != 0 {
				if wParam == 1 && a.settings.Automatic && a.trayPresent {
					user32.NewProc("ShowWindow").Call(hwnd, 0)
				} else if wParam != 1 {
					a.layout()
				}
			}
			return 0
		case 0x0024: // WM_GETMINMAXINFO
			var info minMaxInfo
			kernel32.NewProc("RtlMoveMemory").Call(uintptr(unsafe.Pointer(&info)), lParam, unsafe.Sizeof(info))
			info.MinTrackSize = winPoint{int32(a.scale(860)), int32(a.scale(800))}
			kernel32.NewProc("RtlMoveMemory").Call(lParam, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
			return 0
		case 0x0111: // WM_COMMAND: handle button clicks only.
			if wParam>>16 == 0 {
				a.command(int(wParam & 0xffff))
			} else if int(wParam&0xffff) == idDest && wParam>>16 == 0x200 && !a.busy {
				a.commitDestination()
			} else if int(wParam&0xffff) == idSchedule && wParam>>16 == 1 && !a.busy {
				a.saveSchedule()
			} else if int(wParam&0xffff) == idHours && wParam>>16 == 0x200 && !a.busy {
				a.saveSchedule()
			}
			return 0
		case wmDone:
			a.complete()
			return 0
		case wmTray:
			if lParam == 0x0202 || lParam == 0x0203 {
				a.showWindow()
			}
			if lParam == 0x0205 || lParam == 0x007b {
				a.trayMenu()
			}
			return 0
		case wmReload:
			if a.busy || a.modalDepth > 0 {
				a.pendingReload = true
			} else {
				a.reloadSettings()
			}
			return 0
		case 0x0113: // WM_TIMER: probing happens off the UI thread.
			if a.pendingReload && !a.busy && a.modalDepth == 0 {
				a.pendingReload = false
				a.reloadSettings()
			}
			a.pollAutomation()
			return 0
		case 0x0010: // WM_CLOSE
			if a.settings.Automatic && a.trayPresent {
				user32.NewProc("ShowWindow").Call(hwnd, 0)
				return 0
			}
			if a.protectClose {
				a.message("正在处理存档，请等待完成后再关闭。", 0x40)
				return 0
			}
			user32.NewProc("DestroyWindow").Call(hwnd)
			return 0
		case 0x0002: // WM_DESTROY
			user32.NewProc("KillTimer").Call(hwnd, 1)
			a.removeTray()
			user32.NewProc("PostQuitMessage").Call(0)
			return 0
		case 0x0138: // WM_CTLCOLORSTATIC: white labels/read-only controls.
			gdi32.NewProc("SetBkColor").Call(wParam, 0xffffff)
			gdi32.NewProc("SetTextColor").Call(wParam, 0x282828)
			returnValue, _, _ := gdi32.NewProc("GetStockObject").Call(0)
			return returnValue
		}
	}
	ret, _, _ := pDefWindowProc.Call(hwnd, uintptr(message), wParam, lParam)
	return ret
}

func setText(hwnd uintptr, text string) { pSetText.Call(hwnd, uintptr(unsafe.Pointer(utf16(text)))) }
func getText(hwnd uintptr) string {
	n, _, _ := pGetTextLength.Call(hwnd)
	buf := make([]uint16, n+1)
	pGetText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}
func (a *desktopApp) message(text string, flags uintptr) uintptr {
	a.modalDepth++
	defer func() { a.modalDepth-- }()
	ret, _, _ := pMessageBox.Call(a.window, uintptr(unsafe.Pointer(utf16(text))), uintptr(unsafe.Pointer(utf16(windowTitle))), flags)
	return ret
}
func (a *desktopApp) setBusy(busy, protectClose bool) {
	a.busy, a.protectClose = busy, protectClose
	for _, id := range []int{idDest, idBrowseDest, idBackup, idSource, idBrowseSource, idRestore, idRefresh, idOpen, idAutomatic, idStartup, idSchedule, idHours} {
		enabled := uintptr(1)
		if busy {
			enabled = 0
		}
		pEnableWindow.Call(a.controls[id], enabled)
	}
	if !busy {
		a.enableHours()
	}
}
func (a *desktopApp) deliver(result desktopResult) {
	a.mailboxMu.Lock()
	a.results = append(a.results, result)
	a.mailboxMu.Unlock()
	pPostMessage.Call(a.window, wmDone, 0, 0)
}
func (a *desktopApp) scan() {
	a.setBusy(true, false)
	setText(a.statusText, "正在查找 Minecraft 世界…")
	go func() { a.deliver(desktopResult{Status: scanStatus(), Scan: true}) }()
}
func (a *desktopApp) showWorlds(status appStatus) {
	setText(a.worldsLabel, fmt.Sprintf("找到的世界（%d 个）", len(status.Worlds)))
	var b strings.Builder
	for _, w := range status.Worlds {
		edition := "基岩版"
		if strings.EqualFold(w.Edition, "Java") {
			edition = "Java 版"
		}
		fmt.Fprintf(&b, "[%s] %s\r\n%s\r\n\r\n", edition, w.Name, w.Path)
	}
	if len(status.Worlds) == 0 {
		b.WriteString("未找到世界存档。请确认游戏已安装，并至少创建过一个世界。\r\n\r\n")
	}
	b.WriteString("基岩版目录：\r\n")
	if len(status.BedrockPaths) == 0 {
		b.WriteString("未找到\r\n")
	} else {
		b.WriteString(strings.Join(status.BedrockPaths, "\r\n") + "\r\n")
	}
	b.WriteString("\r\nJava 版目录：\r\n")
	if status.JavaPath == "" {
		b.WriteString("未找到")
	} else {
		b.WriteString(status.JavaPath)
	}
	setText(a.worldsText, b.String())
}
func (a *desktopApp) complete() {
	a.mailboxMu.Lock()
	results := a.results
	a.results = nil
	a.mailboxMu.Unlock()
	operationCompleted := false
	for _, result := range results {
		if result.Poll {
			a.completePoll(result)
			continue
		}
		operationCompleted = true
		a.setBusy(false, false)
		a.showWorlds(result.Status)
		if result.Automatic {
			a.monitor.finish(time.Now(), result.Err)
			text := fmt.Sprintf("自动备份完成：更新 %d 个，未变化 %d 个", result.Auto.Updated, result.Auto.Unchanged)
			if result.Err == nil {
				a.settings.LastAutomaticAt = a.monitor.lastSuccess.UTC()
				if err := saveSettings(a.settings); err != nil {
					text += "；保存定时进度失败：" + err.Error()
				}
			}
			if result.Err != nil {
				text = fmt.Sprintf("自动备份稍后重试（已更新 %d 个）：%s", result.Auto.Updated, result.Err)
			}
			setText(a.autoStatus, text)
			setText(a.statusText, text+"\r\n"+filepath.Join(a.settings.BackupDir, "自动备份"))
			a.trayTooltip(text)
		} else if result.Err != nil {
			setText(a.statusText, "操作失败："+result.Err.Error())
		} else if result.Scan {
			setText(a.statusText, "扫描完成。可以开始备份，或选择已有备份恢复。")
		} else {
			text := result.Operation.Message
			if result.Operation.File != "" {
				text += "\r\n" + result.Operation.File
			}
			setText(a.statusText, text)
		}
	}
	if a.pendingReload && !a.busy && a.modalDepth == 0 {
		a.pendingReload = false
		a.reloadSettings()
	}
	if operationCompleted {
		a.pollAutomation()
	}
}
func (a *desktopApp) command(id int) {
	if id == idShow {
		a.showWindow()
		return
	}
	if id == idExit {
		if a.protectClose {
			a.message("正在处理存档，请等待完成后再退出。", 0x40)
			return
		}
		user32.NewProc("DestroyWindow").Call(a.window)
		return
	}
	if a.busy {
		return
	}
	switch id {
	case idAutomatic:
		if err := a.commitDestination(); err != nil {
			a.updateSettingsControls()
			return
		}
		previous := a.settings
		checked, _, _ := pSendMessage.Call(a.controls[idAutomatic], 0xf0, 0, 0)
		a.settings.Automatic = checked == 1
		if err := saveSettings(a.settings); err != nil {
			a.settings = previous
			a.message("保存自动备份设置失败："+err.Error(), 0x10)
		}
		a.resetMonitor()
		a.updateSettingsControls()
		a.pollAutomation()
	case idStartup:
		checked, _, _ := pSendMessage.Call(a.controls[idStartup], 0xf0, 0, 0)
		if err := setStartupEnabled(checked == 1); err != nil {
			a.message("设置开机启动失败："+err.Error(), 0x10)
		} else {
			a.startup = checked == 1
		}
		a.updateSettingsControls()
	case idBrowseDest:
		if path := a.chooseDirectory(); path != "" {
			setText(a.controls[idDest], path)
			a.commitDestination()
		}
	case idBrowseSource:
		if path := a.chooseBackup(); path != "" {
			setText(a.controls[idSource], path)
		}
	case idRefresh:
		a.scan()
	case idBackup, idRestore:
		if err := a.commitDestination(); err != nil {
			return
		}
		path := strings.TrimSpace(getText(a.controls[idDest]))
		if id == idRestore {
			path = strings.TrimSpace(getText(a.controls[idSource]))
			if path == "" {
				a.message("请先选择 .zip 或 .mcworld 备份文件。", 0x40)
				return
			}
			if a.message("请确认已经关闭 Minecraft。\r\n恢复的世界会自动另存，不会覆盖现有世界。\r\n\r\n确定开始恢复？", 0x124) != 6 {
				return
			}
		}
		a.setBusy(true, true)
		text := "正在备份，请等待完成…"
		if id == idRestore {
			text = "正在恢复，请等待完成…"
		}
		setText(a.statusText, text)
		go func() {
			var result operationResult
			var err error
			if id == idBackup {
				result, err = a.state.createBackup(path)
			} else {
				result, err = a.state.restoreBackup(path)
			}
			a.deliver(desktopResult{Status: scanStatus(), Operation: result, Err: err})
		}()
	case idOpen:
		path := strings.TrimSpace(getText(a.controls[idDest]))
		if path == "" {
			path = defaultBackupDir()
		}
		path, err := filepath.Abs(path)
		if err == nil {
			err = os.MkdirAll(path, 0755)
		}
		if err != nil {
			a.message("打开备份目录失败："+err.Error(), 0x10)
			return
		}
		ret, _, _ := shell32.NewProc("ShellExecuteW").Call(a.window, uintptr(unsafe.Pointer(utf16("open"))), uintptr(unsafe.Pointer(utf16(path))), 0, 0, 1)
		if ret <= 32 {
			a.message("打开备份目录失败，请检查文件夹是否可访问。", 0x10)
		}
	}
}

func (a *desktopApp) chooseBackup() string {
	a.modalDepth++
	defer func() { a.modalDepth-- }()
	buf := make([]uint16, 32768)
	// Encode components separately to retain the embedded NUL separators.
	var filter []uint16
	for _, s := range []string{"Minecraft 备份 (*.zip;*.mcworld)", "*.zip;*.mcworld", "所有文件 (*.*)", "*.*", ""} {
		part, _ := syscall.UTF16FromString(s)
		filter = append(filter, part...)
	}
	dialog := openFileName{Owner: a.window, Filter: &filter[0], FilterIndex: 1, File: &buf[0], MaxFile: uint32(len(buf)), Title: utf16("选择 Minecraft 备份"), Flags: 0x00080000 | 0x00001000 | 0x00000800 | 0x00000004 | 0x00000008}
	dialog.Size = uint32(unsafe.Sizeof(dialog))
	ret, _, _ := comdlg32.NewProc("GetOpenFileNameW").Call(uintptr(unsafe.Pointer(&dialog)))
	runtime.KeepAlive(filter)
	if ret == 0 {
		if code, _, _ := comdlg32.NewProc("CommDlgExtendedError").Call(); code != 0 {
			a.message(fmt.Sprintf("打开文件选择器失败：0x%x", code), 0x10)
		}
		return ""
	}
	return syscall.UTF16ToString(buf)
}
func (a *desktopApp) chooseDirectory() string {
	a.modalDepth++
	defer func() { a.modalDepth-- }()
	var display [260]uint16
	info := browseInfo{Owner: a.window, DisplayName: &display[0], Title: utf16("选择备份保存文件夹"), Flags: 0x0001 | 0x0040 | 0x0010}
	pidl, _, _ := shell32.NewProc("SHBrowseForFolderW").Call(uintptr(unsafe.Pointer(&info)))
	if pidl == 0 {
		return ""
	}
	defer ole32.NewProc("CoTaskMemFree").Call(pidl)
	var path [32768]uint16
	ret, _, _ := shell32.NewProc("SHGetPathFromIDListEx").Call(pidl, uintptr(unsafe.Pointer(&path[0])), uintptr(len(path)), 0)
	if ret == 0 {
		a.message("请选择可以写入的普通文件夹。", 0x40)
		return ""
	}
	return syscall.UTF16ToString(path[:])
}
