package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFolderHistory(t *testing.T) {
	root := t.TempDir()
	var history []string
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		history = rememberFolder(history, filepath.Join(root, name))
	}
	if len(history) != 6 || filepath.Base(history[0]) != "g" || filepath.Base(history[5]) != "b" {
		t.Fatalf("history: %v", history)
	}
	history = rememberFolder(history, filepath.Join(root, "E"))
	if len(history) != 6 || filepath.Base(history[0]) != "E" {
		t.Fatal(history)
	}
	for _, p := range history[1:] {
		if filepath.Base(p) == "e" {
			t.Fatal("case duplicate")
		}
	}
	path := filepath.Join(root, "settings", "folders.json")
	if err := saveHistory(path, history); err != nil {
		t.Fatal(err)
	}
	if err := saveHistory(path, history); err != nil {
		t.Fatal("overwrite:", err)
	}
	if got := loadHistory(path); !reflect.DeepEqual(got, history) {
		t.Fatalf("round trip %v != %v", got, history)
	}
	os.WriteFile(path, []byte("bad json"), 0600)
	if got := loadHistory(path); len(got) != 0 {
		t.Fatal(got)
	}
}
