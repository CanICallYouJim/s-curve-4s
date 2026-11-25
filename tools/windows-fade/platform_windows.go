package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

func configureConsole() {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	kernel.NewProc("SetConsoleOutputCP").Call(65001)
	kernel.NewProc("SetConsoleCP").Call(65001)
	title, _ := syscall.UTF16PtrFromString(appWindowTitle() + " · обработка")
	kernel.NewProc("SetConsoleTitleW").Call(uintptr(unsafe.Pointer(title)))
}

func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

//go:embed choose-folder.ps1
var folderDialog string

//go:embed brand.png
var brandSprite []byte

//go:embed BrandControl.cs
var brandControl []byte

func showConsole(show bool) {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	window, _, _ := kernel.NewProc("GetConsoleWindow").Call()
	mode := uintptr(0)
	if show {
		mode = 5
	}
	if window != 0 {
		syscall.NewLazyDLL("user32.dll").NewProc("ShowWindow").Call(window, mode)
	}
}

func chooseFolder(history []string) (string, error) {
	selection, err := chooseFolderDialog(history, false)
	if err != nil {
		return "", err
	}
	var action struct {
		Action string `json:"action"`
	}
	if json.Unmarshal([]byte(selection), &action) == nil && action.Action == "update" {
		fmt.Println("Загружаем обновление…")
		target, err := installAppUpdate(context.Background())
		if err != nil {
			reportDialogError(err)
			return "", err
		}
		releaseGUIInstance()
		if err = startUpdatedApp(target); err != nil {
			reportDialogError(err)
			return "", err
		}
		return "", errCancelled
	}
	return selection, nil
}

func chooseCheckFolder(history []string) (string, bool, error) {
	selection, err := chooseFolderDialog(history, true)
	if err != nil || selection == "" {
		return selection, false, err
	}
	var result struct {
		Folder    string `json:"folder"`
		Recursive bool   `json:"recursive"`
	}
	if err = json.Unmarshal([]byte(selection), &result); err != nil {
		return "", false, err
	}
	return result.Folder, result.Recursive, nil
}

// The GUI host needs no console, but MUST NOT inherit SW_HIDE: that also
// hides its first WinForms window. HideWindow remains for audio tools only.
func folderDialogCommand(ctx context.Context, binary string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd
}

func chooseFolderDialog(history []string, check bool) (selection string, err error) {
	fmt.Println("Открываем окно выбора папки…")
	defer func() {
		showConsole(true)
		if err != nil {
			reportDialogError(err)
		}
	}()
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	temp, err := os.MkdirTemp("", "lena-folder-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temp)
	result := filepath.Join(temp, "selected.txt")
	ready := filepath.Join(temp, "ready")
	updatePath := filepath.Join(temp, "update.json")
	if !check {
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			status := checkAppUpdate(ctx)
			if data, e := json.Marshal(status); e == nil {
				_ = os.WriteFile(updatePath, data, 0600)
			}
		}()
		defer func() { cancel(); wg.Wait() }()
	}
	brandImagePath := filepath.Join(temp, "brand.png")
	brandCodePath := filepath.Join(temp, "BrandControl.cs")
	if err = os.WriteFile(brandImagePath, brandSprite, 0600); err != nil {
		return "", err
	}
	if err = os.WriteFile(brandCodePath, brandControl, 0600); err != nil {
		return "", err
	}
	encoded := utf16.Encode([]rune(folderDialog))
	raw := make([]byte, len(encoded)*2)
	for i, v := range encoded {
		binary.LittleEndian.PutUint16(raw[i*2:], v)
	}
	script := base64.StdEncoding.EncodeToString(raw)
	powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	cmd := folderDialogCommand(context.Background(), powershell, "-NoProfile", "-STA", "-EncodedCommand", script)
	data, _ := json.Marshal(history)
	cmd.Env = append(os.Environ(), "LENA_FADE_EXE="+executable, "LENA_FADE_HISTORY="+string(data), "LENA_FADE_SELECTION="+result, "LENA_FADE_READY="+ready, "LENA_FADE_UPDATE_STATUS="+updatePath, "LENA_FADE_VERSION="+appVersion, "LENA_FADE_BRAND_IMAGE="+brandImagePath, "LENA_FADE_BRAND_CODE="+brandCodePath)
	if check {
		cmd.Env = append(cmd.Env, "LENA_MUSIC_CHECK=1")
	}
	var housekeeping sync.WaitGroup
	defer housekeeping.Wait()
	if output, err := waitForDialog(cmd, ready, 45*time.Second, func() {
		showConsole(false)
		if !check {
			housekeeping.Add(1)
			go func() { defer housekeeping.Done(); _ = cleanupUpdateCache(nil) }()
		}
	}); err != nil {
		return "", fmt.Errorf("выбор папки: %w\n%s", err, output)
	}
	selected, err := os.ReadFile(result)
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(selected), err
}
