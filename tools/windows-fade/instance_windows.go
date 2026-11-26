package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

var releaseGUIInstance = func() {}

func appWindowTitle() string {
	if applicationMode == "check" {
		return "Проверка музыки"
	}
	return "S-кривая · 4 сек"
}

func acquireGUIInstance() (bool, error) {
	name, _ := syscall.UTF16PtrFromString("Local\\IG.LenaDJ." + applicationMode)
	handle, _, err := syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW").Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return false, fmt.Errorf("запуск приложения: %w", err)
	}
	if err == syscall.ERROR_ALREADY_EXISTS {
		syscall.CloseHandle(syscall.Handle(handle))
		user := syscall.NewLazyDLL("user32.dll")
		for _, caption := range []string{appWindowTitle(), appWindowTitle() + " · обработка"} {
			title, _ := syscall.UTF16PtrFromString(caption)
			window, _, _ := user.NewProc("FindWindowW").Call(0, uintptr(unsafe.Pointer(title)))
			if window != 0 {
				user.NewProc("ShowWindow").Call(window, 9) // SW_RESTORE
				user.NewProc("SetForegroundWindow").Call(window)
				break
			}
		}
		return false, nil
	}
	var once sync.Once
	releaseGUIInstance = func() { once.Do(func() { syscall.CloseHandle(syscall.Handle(handle)) }) }
	return true, nil
}

func reportDialogError(err error) {
	message := "Не удалось открыть окно программы.\n\n" + err.Error()
	if folder, e := os.UserCacheDir(); e == nil {
		folder = filepath.Join(folder, "LenaDJ", "SCurve")
		if os.MkdirAll(folder, 0700) == nil {
			log := filepath.Join(folder, "startup.log")
			if os.WriteFile(log, []byte(message), 0600) == nil {
				message += "\n\nПодробности: " + log
			}
		}
	}
	if os.Getenv("CI") != "" {
		return
	}
	text, _ := syscall.UTF16PtrFromString(message)
	title, _ := syscall.UTF16PtrFromString(appWindowTitle())
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}

func startUpdatedGUI(target string) error {
	releaseGUIInstance()
	err := startUpdatedApp(target)
	if err != nil && len(os.Args) == 1 {
		// A failed handoff must reacquire the lock before the folder session can retry.
		acquired, lockErr := acquireGUIInstance()
		if !acquired || lockErr != nil {
			return errors.Join(err, lockErr, errCancelled)
		}
	}
	return err
}
