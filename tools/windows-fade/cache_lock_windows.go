package main

import (
	"os"
	"sync"
	"syscall"
	"unsafe"
)

func tryCacheLock(path string) (func(), bool, error) {
	if err := cacheRegularOrMissing(path); err != nil {
		return nil, false, err
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, false, err
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, false, err
	}
	file := os.NewFile(uintptr(handle), path)
	var overlap syscall.Overlapped
	kernel := syscall.NewLazyDLL("kernel32.dll")
	result, _, e := kernel.NewProc("LockFileEx").Call(uintptr(handle), 3, 0, 1, 0, uintptr(unsafe.Pointer(&overlap))) // exclusive, fail immediately
	if result == 0 {
		file.Close()
		if e == syscall.Errno(33) {
			return nil, false, nil
		} // ERROR_LOCK_VIOLATION
		return nil, false, e
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			kernel.NewProc("UnlockFileEx").Call(uintptr(handle), 0, 1, 0, uintptr(unsafe.Pointer(&overlap)))
			file.Close()
		})
	}, true, nil
}

// NTFS denies a writable exclusive open for an executing or otherwise busy EXE.
// Probe before removing any metadata, so a busy version remains intact.
func cacheVersionCanDelete(target string) bool {
	if _, err := os.Lstat(target); os.IsNotExist(err) {
		return true
	}
	name, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return false
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return false
	}
	syscall.CloseHandle(handle)
	return true
}
