//go:build windows

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"minecraft-backup-tool/assets"
	"runtime"
	"syscall"
	"unsafe"
)

// Keep the native test host and ordinary go builds usable without an RC resource.
// Production always tries its compiled icon resources first.

var desktopIconComctl32 = syscall.NewLazyDLL("comctl32.dll")

type desktopIcons struct {
	large, small uintptr
	imageLists   []uintptr
}

type buttonImageList struct {
	ImageList uintptr
	Margin    winRect
	Align     uint32
}

func loadDesktopIcons(instance uintptr) (*desktopIcons, error) {
	icons := &desktopIcons{}
	metric := func(id uintptr) int {
		value, _, _ := user32.NewProc("GetSystemMetrics").Call(id)
		return max(1, int(value))
	}
	var err error
	icons.large, err = loadDesktopIcon(instance, 101, "app", metric(11), metric(12))
	if err != nil {
		return nil, err
	}
	icons.small, err = loadDesktopIcon(instance, 101, "app", metric(49), metric(50))
	if err != nil {
		icons.close()
		return nil, err
	}
	return icons, nil
}

func loadDesktopIcon(instance uintptr, resource int, name string, width, height int) (uintptr, error) {
	icon, _, _ := user32.NewProc("LoadImageW").Call(instance, uintptr(resource), 1, uintptr(width), uintptr(height), 0)
	if icon != 0 {
		return icon, nil
	}
	data, err := assets.Icons.ReadFile(name + ".ico")
	if err != nil {
		return 0, fmt.Errorf("读取图标失败: %w", err)
	}
	image, err := selectICOImage(data, width, height)
	if err != nil {
		return 0, fmt.Errorf("读取 %s 图标失败: %w", name, err)
	}
	icon, _, callErr := user32.NewProc("CreateIconFromResourceEx").Call(uintptr(unsafe.Pointer(&image[0])), uintptr(len(image)), 1, 0x00030000, uintptr(width), uintptr(height), 0)
	runtime.KeepAlive(image)
	if icon == 0 {
		return 0, fmt.Errorf("创建 %s 图标失败: %w", name, callErr)
	}
	return icon, nil
}

func selectICOImage(data []byte, width, height int) ([]byte, error) {
	if len(data) < 6 || binary.LittleEndian.Uint16(data[:2]) != 0 || binary.LittleEndian.Uint16(data[2:4]) != 1 {
		return nil, errors.New("ICO 文件头无效")
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count == 0 || count > (len(data)-6)/16 {
		return nil, errors.New("ICO 图像目录无效")
	}
	var best []byte
	bestFits, bestDistance, bestDepth := false, 0, 0
	for i := range count {
		entry := data[6+i*16 : 6+(i+1)*16]
		w, h := int(entry[0]), int(entry[1])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		size := uint64(binary.LittleEndian.Uint32(entry[8:12]))
		offset := uint64(binary.LittleEndian.Uint32(entry[12:16]))
		if size == 0 || offset < uint64(6+count*16) || offset+size > uint64(len(data)) {
			continue
		}
		fits := w >= width && h >= height
		distance := max(w-width, width-w) + max(h-height, height-h)
		depth := int(binary.LittleEndian.Uint16(entry[6:8]))
		if best == nil || (fits && !bestFits) || (fits == bestFits && (distance < bestDistance || (distance == bestDistance && depth > bestDepth))) {
			best, bestFits, bestDistance, bestDepth = data[offset:offset+size], fits, distance, depth
		}
	}
	if best == nil {
		return nil, errors.New("ICO 没有有效图像")
	}
	return best, nil
}

func (icons *desktopIcons) applyWindow(window uintptr) {
	pSendMessage.Call(window, 0x0080, 1, icons.large) // WM_SETICON / ICON_BIG
	pSendMessage.Call(window, 0x0080, 0, icons.small) // WM_SETICON / ICON_SMALL
}

func (icons *desktopIcons) applyButtons(app *desktopApp, instance uintptr) error {
	comctl := desktopIconComctl32
	size := max(16, app.scale(18))
	for _, button := range []struct {
		id, resource int
		name         string
	}{
		{idBrowseDest, 104, "folder"},
		{idBackup, 102, "backup"},
		{idBrowseSource, 105, "file"},
		{idRestore, 103, "restore"},
		{idRefresh, 106, "refresh"},
		{idOpen, 104, "folder"},
		{idExit, 107, "exit"},
	} {
		icon, err := loadDesktopIcon(instance, button.resource, button.name, size, size)
		if err != nil {
			return err
		}
		list, _, callErr := comctl.NewProc("ImageList_Create").Call(uintptr(size), uintptr(size), 0x21, 1, 0) // ILC_COLOR32 | ILC_MASK
		if list == 0 {
			user32.NewProc("DestroyIcon").Call(icon)
			return fmt.Errorf("创建按钮图标列表失败: %w", callErr)
		}
		icons.imageLists = append(icons.imageLists, list)
		index, _, callErr := comctl.NewProc("ImageList_ReplaceIcon").Call(list, ^uintptr(0), icon)
		user32.NewProc("DestroyIcon").Call(icon) // The image list keeps its own copy.
		if int32(index) == -1 {
			return fmt.Errorf("加入按钮图标失败: %w", callErr)
		}
		images := buttonImageList{ImageList: list, Margin: winRect{Left: int32(app.scale(8)), Right: int32(app.scale(4))}}
		if ok, _, _ := pSendMessage.Call(app.controls[button.id], 0x1602, 0, uintptr(unsafe.Pointer(&images))); ok == 0 { // BCM_SETIMAGELIST
			return errors.New("设置按钮图标失败")
		}
	}
	return nil
}

func (icons *desktopIcons) close() {
	for _, list := range icons.imageLists {
		desktopIconComctl32.NewProc("ImageList_Destroy").Call(list)
	}
	icons.imageLists = nil
	if icons.large != 0 {
		user32.NewProc("DestroyIcon").Call(icons.large)
	}
	if icons.small != 0 && icons.small != icons.large {
		user32.NewProc("DestroyIcon").Call(icons.small)
	}
	icons.large, icons.small = 0, 0
}
