//go:build windows

package nativedialog

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	comdlg32             = syscall.NewLazyDLL("comdlg32.dll")
	shell32              = syscall.NewLazyDLL("shell32.dll")
	ole32                = syscall.NewLazyDLL("ole32.dll")
	procGetOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")
	procSHBrowseForFolde = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDL = shell32.NewProc("SHGetPathFromIDListW")
	procCoTaskMemFree    = ole32.NewProc("CoTaskMemFree")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCommDlgError     = comdlg32.NewProc("CommDlgExtendedError")
)

const (
	ofnAllowMultiSelect = 0x00000200
	ofnPathMustExist    = 0x00000800
	ofnFileMustExist    = 0x00001000
	ofnExplorer         = 0x00080000
	ofnNoChangeDir      = 0x00000008

	bifReturnOnlyFSDirs = 0x00000001
	bifNewDialogStyle   = 0x00000040

	coinitApartmentThreaded = 0x2
	maxCommonDialogBuffer   = 32 * 1024
)

var (
	ErrCancelled   = errors.New("dialog cancelled")
	ErrUnavailable = errors.New("native dialogs are only available on Windows")
)

type openFileNameW struct {
	Length           uint32
	Owner            uintptr
	Instance         uintptr
	Filter           *uint16
	CustomFilter     *uint16
	MaxCustomFilter  uint32
	FilterIndex      uint32
	File             *uint16
	MaxFile          uint32
	FileTitle        *uint16
	MaxFileTitle     uint32
	InitialDirectory *uint16
	Title            *uint16
	Flags            uint32
	FileOffset       uint16
	FileExtension    uint16
	DefaultExtension *uint16
	CustData         uintptr
	Hook             uintptr
	TemplateName     *uint16
}

type browseInfoW struct {
	Owner       uintptr
	Root        uintptr
	DisplayName *uint16
	Title       *uint16
	Flags       uint32
	Callback    uintptr
	Param       uintptr
	Image       int32
}

func PickFiles(title string) ([]string, error) {
	filter, err := syscall.UTF16PtrFromString(
		"音频文件 (*.flac;*.wav;*.m4a;*.aac;*.ogg;*.wma)\x00*.flac;*.wav;*.m4a;*.aac;*.ogg;*.wma\x00所有文件 (*.*)\x00*.*\x00\x00",
	)
	if err != nil {
		return nil, err
	}
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return nil, err
	}

	buffer := make([]uint16, maxCommonDialogBuffer)
	dialog := openFileNameW{
		Length:      uint32(unsafe.Sizeof(openFileNameW{})),
		Filter:      filter,
		FilterIndex: 1,
		File:        &buffer[0],
		MaxFile:     uint32(len(buffer)),
		Title:       titlePtr,
		Flags: ofnAllowMultiSelect | ofnExplorer | ofnFileMustExist |
			ofnPathMustExist | ofnNoChangeDir,
	}

	ret, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&dialog)))
	if ret == 0 {
		if commDlgError() != 0 {
			return nil, fmt.Errorf("open file dialog failed: %d", commDlgError())
		}
		return nil, ErrCancelled
	}
	return splitFileSelection(buffer), nil
}

func PickFolder(title string) (string, error) {
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return "", err
	}
	display := make([]uint16, syscall.MAX_PATH)
	browse := browseInfoW{
		DisplayName: &display[0],
		Title:       titlePtr,
		Flags:       bifReturnOnlyFSDirs | bifNewDialogStyle,
	}

	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	comInitialized := hr == 0 || hr == 1 // S_OK or S_FALSE
	if comInitialized {
		defer procCoUninitialize.Call()
	} else {
		return "", fmt.Errorf("initialize COM failed: 0x%x", hr)
	}

	pidl, _, _ := procSHBrowseForFolde.Call(uintptr(unsafe.Pointer(&browse)))
	if pidl == 0 {
		return "", ErrCancelled
	}
	defer procCoTaskMemFree.Call(pidl)

	path := make([]uint16, syscall.MAX_PATH)
	ret, _, _ := procSHGetPathFromIDL.Call(pidl, uintptr(unsafe.Pointer(&path[0])))
	if ret == 0 {
		return "", errors.New("resolve selected folder failed")
	}
	selected := strings.TrimSpace(syscall.UTF16ToString(path))
	if selected == "" {
		return "", errors.New("selected folder is unavailable")
	}
	return filepath.Clean(selected), nil
}

func commDlgError() uint32 {
	value, _, _ := procCommDlgError.Call()
	return uint32(value)
}

func splitFileSelection(buffer []uint16) []string {
	raw := syscall.UTF16ToString(buffer)
	parts := strings.Split(raw, "\x00")
	parts = parts[:len(parts)-1] // UTF16ToString stops at the first NUL.

	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			cleaned = append(cleaned, filepath.Clean(value))
		}
	}
	if len(cleaned) <= 1 {
		return cleaned
	}
	dir := cleaned[0]
	files := make([]string, 0, len(cleaned)-1)
	for _, name := range cleaned[1:] {
		files = append(files, filepath.Join(dir, name))
	}
	return files
}
