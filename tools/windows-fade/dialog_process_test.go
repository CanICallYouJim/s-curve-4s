package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDialogChildHelper(t *testing.T) {
	mode := os.Getenv("TEST_DIALOG_CHILD")
	if mode == "" {
		return
	}
	if mode == "ready" {
		if err := os.WriteFile(os.Getenv("TEST_DIALOG_READY"), []byte("ready"), 0600); err != nil {
			os.Exit(8)
		}
		time.Sleep(1200 * time.Millisecond)
		os.Exit(0)
	}
	if mode == "blocked" {
		time.Sleep(20 * time.Second)
		os.Exit(0)
	}
	fmt.Fprintln(os.Stderr, "dialog startup failed")
	os.Exit(7)
}

func TestDialogStartupLifecycle(t *testing.T) {
	for _, mode := range []string{"ready", "blocked", "failure"} {
		t.Run(mode, func(t *testing.T) {
			ready := filepath.Join(t.TempDir(), "ready")
			cmd := exec.Command(os.Args[0], "-test.run=^TestDialogChildHelper$")
			cmd.Env = append(os.Environ(), "TEST_DIALOG_CHILD="+mode, "TEST_DIALOG_READY="+ready)
			notified := false
			timeout := time.Second
			if mode == "blocked" {
				timeout = 100 * time.Millisecond
			}
			started := time.Now()
			output, err := waitForDialog(cmd, ready, timeout, func() { notified = true })
			switch mode {
			case "ready":
				if err != nil || !notified || time.Since(started) < time.Second {
					t.Fatalf("shown dialog should outlive startup deadline: %v %v %s", err, notified, output)
				}
			case "blocked":
				if err == nil || notified || !strings.Contains(err.Error(), "не открылось") || cmd.ProcessState == nil {
					t.Fatalf("timeout must reap child: %v %v", err, notified)
				}
			case "failure":
				if err == nil || notified || !strings.Contains(string(output), "dialog startup failed") {
					t.Fatalf("lost startup error: %v %s", err, output)
				}
			}
		})
	}
}
