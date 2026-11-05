//go:build windows

package main

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"syscall"
	"unsafe"
)

// Registry and port lookups through the Windows API: the launcher starts no
// other program besides the game.

var (
	advapi32                       = sysDLL("advapi32.dll")
	procRegCreateKeyExW            = advapi32.NewProc("RegCreateKeyExW")
	procRegSetValueExW             = advapi32.NewProc("RegSetValueExW")
	procRegCloseKey                = advapi32.NewProc("RegCloseKey")
	iphlpapi                       = sysDLL("iphlpapi.dll")
	procGetExtendedTcpTable        = iphlpapi.NewProc("GetExtendedTcpTable")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
)

const (
	hkeyCurrentUser  = 0x80000001
	hkeyLocalMachine = 0x80000002
	keySetValue      = 0x0002
	keyWow64_32      = 0x0200
	regSZ            = 1
)

// regSetString writes a REG_SZ value (HKLM: the 32-bit view, as the game reads it).
func regSetString(hklm bool, path, name, value string) error {
	root := uintptr(hkeyCurrentUser)
	access := uintptr(keySetValue)
	if hklm {
		root, access = hkeyLocalMachine, keySetValue|keyWow64_32
	}
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var key syscall.Handle
	if r, _, _ := procRegCreateKeyExW.Call(root, uintptr(unsafe.Pointer(p)), 0, 0, 0, access, 0, uintptr(unsafe.Pointer(&key)), 0); r != 0 {
		return syscall.Errno(r)
	}
	defer procRegCloseKey.Call(uintptr(key))
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	v, err := syscall.UTF16FromString(value)
	if err != nil {
		return err
	}
	if r, _, _ := procRegSetValueExW.Call(uintptr(key), uintptr(unsafe.Pointer(n)), 0, regSZ, uintptr(unsafe.Pointer(&v[0])), uintptr(len(v)*2)); r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

// portOwner: the image name of the process listening on TCP port (IPv4);
// "" when unknown. Tells another nfsrun-launcher from the EA app or Origin,
// which also serve LSX on 127.0.0.1:3216.
func portOwner(port int) string {
	const afInet, tcpTableOwnerPidListener = 2, 3
	var size uint32
	procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, afInet, tcpTableOwnerPidListener, 0)
	if size == 0 {
		return ""
	}
	buf := make([]byte, size+1024)
	size = uint32(len(buf))
	if r, _, _ := procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, afInet, tcpTableOwnerPidListener, 0); r != 0 {
		return ""
	}
	n := int(binary.LittleEndian.Uint32(buf))
	for i := 0; i < n; i++ {
		row := buf[4+i*24:]
		if len(row) < 24 {
			break
		}
		// MIB_TCPROW_OWNER_PID: state, local addr, local port (network order), remote addr, remote port, pid
		lp := binary.LittleEndian.Uint32(row[8:])
		if int(lp&0xff)<<8|int(lp>>8&0xff) == port {
			return processImage(binary.LittleEndian.Uint32(row[20:]))
		}
	}
	return ""
}

func processImage(pid uint32) string {
	const processQueryLimitedInformation = 0x1000
	h, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if h == 0 {
		return fmt.Sprintf("pid %d", pid)
	}
	defer procCloseHandle.Call(h)
	var name [520]uint16
	n := uint32(len(name))
	if r, _, _ := procQueryFullProcessImageNameW.Call(h, 0, uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&n))); r == 0 {
		return fmt.Sprintf("pid %d", pid)
	}
	return filepath.Base(syscall.UTF16ToString(name[:n]))
}
