package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCheckNativeInterface(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Mac bundle uses Unix executable names")
	}
	engines := testTools(t)
	bundle := t.TempDir()
	for name, path := range map[string]string{"ffmpeg": engines.ffmpeg, "ffprobe": engines.ffprobe} {
		if err := os.Symlink(path, filepath.Join(bundle, name)); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	track := filepath.Join(root, "Музыка & [1].wav")
	fixture(t, engines.ffmpeg, track, "-c:a", "pcm_s16le")
	original, _ := os.ReadFile(track)
	if err := os.Mkdir(filepath.Join(root, "Подпапка"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Подпапка", "copy.wav"), original, 0600); err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(t.TempDir(), "result.json")
	args := []string{"--folder", root, "--tools", bundle, "--recursive=false", "--result", resultPath}
	check := func() checkSummary {
		t.Helper()
		if err := runCheckArgs(args); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(resultPath)
		if err != nil {
			t.Fatal(err)
		}
		var result checkSummary
		if err = json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		if result.Error != "" || result.Total != 1 || result.Checked != 1 || result.Output == "" || result.Interrupted {
			t.Fatalf("bad native result: %+v", result)
		}
		if _, err = os.Stat(result.Output); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first, second := check(), check()
	if first.Fresh != 1 || first.Cached != 0 || second.Fresh != 0 || second.Cached != 1 {
		t.Fatalf("history lost: %+v %+v", first, second)
	}
	after, _ := os.ReadFile(track)
	if !bytes.Equal(original, after) {
		t.Fatal("original changed")
	}
	// Even a change outside the sound data must invalidate the whole-file cache.
	changed := append(append([]byte(nil), original...), 0)
	if err := os.WriteFile(track, changed, 0600); err != nil {
		t.Fatal(err)
	}
	third := check()
	if third.Fresh != 1 || third.Cached != 0 {
		t.Fatalf("changed bytes reused: %+v", third)
	}
	missing := t.TempDir()
	err := runCheckArgs([]string{"--folder", root, "--tools", missing, "--result", resultPath})
	if err == nil {
		t.Fatal("missing bundle silently fell back to PATH")
	}
	data, _ := os.ReadFile(resultPath)
	var failed checkSummary
	if err = json.Unmarshal(data, &failed); err != nil || failed.Error == "" || failed.Output != "" {
		t.Fatalf("failed native launch lost reason: %s %v", data, err)
	}
}

func TestCheckStartupCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Mac SIGINT interface")
	}
	if root := os.Getenv("MUSIC_CHECK_CANCEL_TEST"); root != "" {
		if err := runCheckArgs([]string{"--folder", root, "--tools", filepath.Join(root, "tools"), "--result", filepath.Join(root, "result.json")}); err == nil {
			t.Fatal("startup was not interrupted")
		}
		return
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "tools"), 0700); err != nil {
		t.Fatal(err)
	}
	script := []byte("#!/bin/sh\ntouch \"$MUSIC_CHECK_CANCEL_TEST/started\"\nexec /bin/sleep 30\n")
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(root, "tools", name), script, 0700); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestCheckStartupCancellation$")
	cmd.Env = append(os.Environ(), "MUSIC_CHECK_CANCEL_TEST="+root)
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tool did not start: %s", &log)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child failed: %v %s", err, &log)
	}
	data, err := os.ReadFile(filepath.Join(root, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result checkSummary
	if err = json.Unmarshal(data, &result); err != nil || !result.Interrupted || result.Output != "" {
		t.Fatalf("stop shown as error: %s %v", data, err)
	}
}
