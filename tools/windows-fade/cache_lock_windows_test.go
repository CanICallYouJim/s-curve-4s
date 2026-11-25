package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestUpdateCacheBusyWindowsExeRetainsWholeVersion(t *testing.T) {
	root, now := cacheFixture(t)
	target := filepath.Join(root, "1.0.0", "app.exe")
	name, _ := syscall.UTF16PtrFromString(target)
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(handle)
	if err = pruneUpdateCache(root, nil, now); err != nil {
		t.Fatal(err)
	}
	cacheExists(t, target, true)
	cacheExists(t, filepath.Join(root, "1.0.0", ".asset-sha256"), true)
}

func TestUpdateCacheWindowsPartialRemovalRetries(t *testing.T) {
	root, now := cacheFixture(t)
	extra := filepath.Join(root, "1.0.0", "busy.dat")
	if err := os.WriteFile(extra, []byte("busy"), 0600); err != nil {
		t.Fatal(err)
	}
	name, _ := syscall.UTF16PtrFromString(extra)
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if handle != syscall.InvalidHandle {
			syscall.CloseHandle(handle)
		}
	}()
	if err = pruneUpdateCache(root, nil, now); err != nil {
		t.Fatal(err)
	}
	cacheExists(t, filepath.Join(root, "1.0.0", "app.exe"), false)
	cacheExists(t, filepath.Join(root, "1.0.0", ".asset-sha256"), true)
	syscall.CloseHandle(handle)
	handle = syscall.InvalidHandle
	if err = pruneUpdateCache(root, nil, now); err != nil {
		t.Fatal(err)
	}
	cacheExists(t, filepath.Join(root, "1.0.0"), false)
}
