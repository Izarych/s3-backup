package main

import (
	"golang.org/x/sys/windows"
	"unsafe"
)

func showError(text string) {
	body, _ := windows.UTF16PtrFromString(text)
	title, _ := windows.UTF16PtrFromString("S3 Backup")
	windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(body)), uintptr(unsafe.Pointer(title)), 0x10)
}
