//go:build windows

package console

import (
	"syscall"
	"unsafe"
)

const (
	enableProcessedInput = 0x0001
	enableLineInput      = 0x0002
	enableEchoInput      = 0x0004
	enableQuickEditMode  = 0x0040
	enableExtendedFlags  = 0x0080
)

// rawInput switches the console to single-key input: no echo, no line
// editing, and no QuickEdit (a mouse click would otherwise pause the whole
// server until a key is pressed). Ctrl+C keeps working.
func rawInput() (restore func(), ok bool) {
	h, err := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	if err != nil {
		return func() {}, false
	}
	var mode uint32
	if r, _, _ := procGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(&mode))); r == 0 {
		return func() {}, false
	}
	raw := (mode &^ (enableLineInput | enableEchoInput | enableQuickEditMode)) | enableExtendedFlags | enableProcessedInput
	if r, _, _ := procSetConsoleMode.Call(uintptr(h), uintptr(raw)); r == 0 {
		return func() {}, false
	}
	return func() { procSetConsoleMode.Call(uintptr(h), uintptr(mode)) }, true
}
