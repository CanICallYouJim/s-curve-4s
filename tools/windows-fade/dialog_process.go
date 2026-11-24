package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// Limit only startup. Once the window is shown, the user may take any amount
// of time to choose a folder. Always reap the child, including on timeout.
func waitForDialog(cmd *exec.Cmd, readyPath string, timeout time.Duration, onReady func()) ([]byte, error) {
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			return output.Bytes(), err
		case <-tick.C:
			if _, err := os.Stat(readyPath); err == nil {
				onReady()
				err := <-done
				return output.Bytes(), err
			}
		case <-timer.C:
			// A window may have become ready at the deadline between two polls.
			if _, err := os.Stat(readyPath); err == nil {
				onReady()
				err := <-done
				return output.Bytes(), err
			}
			_ = cmd.Process.Kill()
			<-done
			return output.Bytes(), fmt.Errorf("окно выбора папки не открылось за %s", timeout)
		}
	}
}
