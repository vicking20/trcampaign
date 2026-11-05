//go:build windows

package main

import (
	"fmt"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

// sysDLL loads a DLL from the System32 folder only, never from the game folder
// (where a planted copy would otherwise be found first).
func sysDLL(name string) *syscall.LazyDLL {
	var buf [260]uint16
	n, _, _ := procGetSystemDirectoryW.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 || int(n) >= len(buf) {
		return syscall.NewLazyDLL(name)
	}
	return syscall.NewLazyDLL(filepath.Join(syscall.UTF16ToString(buf[:n]), name))
}

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll") // KnownDLL
	procGetSystemDirectoryW = kernel32.NewProc("GetSystemDirectoryW")
	procCreateJobObjectW    = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJob   = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJob  = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject  = kernel32.NewProc("TerminateJobObject")
	procOpenProcess         = kernel32.NewProc("OpenProcess")
	procCloseHandle         = kernel32.NewProc("CloseHandle")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procAttachConsole       = kernel32.NewProc("AttachConsole")
	procAllocConsole        = kernel32.NewProc("AllocConsole")
)

// The game runs inside a job object with KILL_ON_JOB_CLOSE: when the
// launcher ends -- window closed, Ctrl+C, killed in Task Manager, crash -- the
// system closes the job handle and ends the game with it.
var (
	jobOnce sync.Once
	job     uintptr
	jobErr  error
)

func gameJob() (uintptr, error) {
	jobOnce.Do(func() {
		h, _, err := procCreateJobObjectW.Call(0, 0)
		if h == 0 {
			jobErr = fmt.Errorf("CreateJobObject: %v", err)
			return
		}
		// JOBOBJECT_EXTENDED_LIMIT_INFORMATION: 112 bytes (32-bit) / 144
		// (64-bit); BasicLimitInformation.LimitFlags at offset 16 in both.
		size := 144
		if unsafe.Sizeof(uintptr(0)) == 4 {
			size = 112
		}
		info := make([]byte, size)
		const jobObjectLimitKillOnJobClose = 0x2000
		*(*uint32)(unsafe.Pointer(&info[16])) = jobObjectLimitKillOnJobClose
		const jobObjectExtendedLimitInformation = 9
		if r, _, err := procSetInformationJob.Call(h, jobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info[0])), uintptr(size)); r == 0 {
			procCloseHandle.Call(h)
			jobErr = fmt.Errorf("SetInformationJobObject: %v", err)
			return
		}
		job = h
	})
	return job, jobErr
}

func trackGame(pid int) error {
	j, err := gameJob()
	if err != nil {
		return err
	}
	const processSetQuota, processTerminate = 0x0100, 0x0001
	h, _, err := procOpenProcess.Call(processSetQuota|processTerminate, 0, uintptr(pid))
	if h == 0 {
		return fmt.Errorf("OpenProcess: %v", err)
	}
	defer procCloseHandle.Call(h)
	if r, _, err := procAssignProcessToJob.Call(j, h); r == 0 {
		return fmt.Errorf("AssignProcessToJobObject: %v", err)
	}
	return nil
}

// killGames ends every game this launcher started.
func killGames() {
	if job != 0 {
		procTerminateJobObject.Call(job, 1)
	}
}
