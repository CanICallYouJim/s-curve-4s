package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestUpdateCacheLiveChild(t *testing.T) {
	if os.Getenv("IG_CACHE_LIVE_HELPER") == "" {
		return
	}
	if err := os.WriteFile(os.Getenv("IG_CACHE_LIVE_READY"), []byte("ready"), 0600); err != nil {
		os.Exit(1)
	}
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

func TestUpdateCacheFindsHeadlessMacHelper(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "1.0.0", "Старое приложение.app")
	child := filepath.Join(app, "Contents", "MacOS", "fade-engine")
	if err := os.MkdirAll(filepath.Dir(child), 0700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(child, data, 0700); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(child, "-test.run=^TestUpdateCacheLiveChild$")
	cmd.Env = append(os.Environ(), "IG_CACHE_LIVE_HELPER=1", "IG_CACHE_LIVE_READY="+ready)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	paths, err := cacheRunningExecutables(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if runningFromUpdate(canonicalCachePath(app), path, "darwin") {
			return
		}
	}
	t.Fatal("headless cached helper missing from active paths")
}
