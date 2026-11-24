package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFadeCLIResult(t *testing.T) {
	binaries := testTools(t)
	if filepath.Dir(binaries.ffmpeg) != filepath.Dir(binaries.ffprobe) {
		t.Skip("tools must share a directory")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	folder := filepath.Join(t.TempDir(), "Музыка & пробелы")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	fixture(t, binaries.ffmpeg, filepath.Join(folder, "test.wav"), "-c:a", "pcm_s24le")
	resultPath := filepath.Join(t.TempDir(), "result.json")
	args := []string{"--folder", folder, "--tools", filepath.Dir(binaries.ffmpeg), "--result", resultPath}
	if err := runFade(args); err != nil {
		t.Fatal(err)
	}
	read := func() fadeResult {
		t.Helper()
		data, err := os.ReadFile(resultPath)
		if err != nil {
			t.Fatal(err)
		}
		var result fadeResult
		if err = json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	result := read()
	if result.Done != 1 || result.Failed != 0 || result.Error != "" || filepath.Dir(result.Output) != folder {
		t.Fatalf("result: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(result.Output, "test.wav")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "broken.mp3"), []byte("not audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runFade(args); err == nil {
		t.Fatal("partial failure must return an error")
	}
	result = read()
	if result.Done != 1 || result.Failed != 1 || result.Error == "" {
		t.Fatalf("partial: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(result.Output, "broken.mp3")); !os.IsNotExist(err) {
		t.Fatal("failed output published")
	}
	args[1] = filepath.Join(folder, "test.wav")
	if err := runFade(args); err == nil {
		t.Fatal("file accepted as folder")
	}
	if result = read(); result.Error == "" || result.Output != "" {
		t.Fatalf("invalid folder: %+v", result)
	}
}
