package main

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestDialogDoesNotHideItsWindow(t *testing.T) {
	gui := folderDialogCommand(context.Background(), "powershell.exe")
	if gui.SysProcAttr == nil || gui.SysProcAttr.HideWindow || gui.SysProcAttr.CreationFlags&0x08000000 == 0 {
		t.Fatal("GUI host must suppress its console without SW_HIDE")
	}
	audio := command(context.Background(), "ffmpeg.exe")
	if audio.SysProcAttr == nil || !audio.SysProcAttr.HideWindow {
		t.Fatal("audio tools must remain hidden")
	}
}

// Exercises the actual embedded WinForms host, including brand compilation and
// startup acknowledgement. The release CI runs this on Windows, not a mock.
func TestWindowsDialogSmoke(t *testing.T) {
	if os.Getenv("CI") == "" {
		t.Skip("real desktop smoke runs in Windows CI")
	}
	result := make(chan error, 1)
	go func() { _, err := chooseFolderDialog(nil, false); result <- err }()
	user := syscall.NewLazyDLL("user32.dll")
	title, _ := syscall.UTF16PtrFromString(appWindowTitle())
	deadline := time.Now().Add(55 * time.Second)
	var hwnd uintptr
	for time.Now().Before(deadline) {
		hwnd, _, _ = user.NewProc("FindWindowW").Call(0, uintptr(unsafe.Pointer(title)))
		if hwnd != 0 {
			visible, _, _ := user.NewProc("IsWindowVisible").Call(hwnd)
			if visible != 0 {
				break
			}
		}
		select {
		case err := <-result:
			t.Fatalf("dialog exited without a visible window: %v", err)
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	if hwnd == 0 {
		t.Fatal("no dialog window")
	}
	visible, _, _ := user.NewProc("IsWindowVisible").Call(hwnd)
	user.NewProc("PostMessageW").Call(hwnd, 0x10, 0, 0) // WM_CLOSE
	if visible == 0 {
		t.Fatal("dialog window hidden")
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("closed dialog left a waiting process")
	}
}

func TestSingleGUIInstance(t *testing.T) {
	first, err := acquireGUIInstance()
	if err != nil || !first {
		t.Fatalf("first instance: %v %v", first, err)
	}
	release := releaseGUIInstance
	defer release()
	second, err := acquireGUIInstance()
	if err != nil || second {
		t.Fatalf("duplicate instance accepted: %v %v", second, err)
	}
	release()
	third, err := acquireGUIInstance()
	if err != nil || !third {
		t.Fatalf("lock not released: %v %v", third, err)
	}
	releaseGUIInstance()
}
