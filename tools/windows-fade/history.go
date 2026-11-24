package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func rememberFolder(history []string, folder string) []string {
	result := []string{filepath.Clean(folder)}
	for _, old := range history {
		old = filepath.Clean(old)
		duplicate := false
		for _, present := range result {
			if strings.EqualFold(old, present) {
				duplicate = true
				break
			}
		}
		if !duplicate && filepath.IsAbs(old) {
			result = append(result, old)
		}
		if len(result) == 6 {
			break
		}
	}
	return result
}

func historyFile() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "LenaDJ", "SCurve", "folders.json"), nil
}

func loadHistory(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var history []string
	if json.Unmarshal(data, &history) != nil {
		return nil
	}
	var result []string
	for i := len(history) - 1; i >= 0; i-- {
		if filepath.IsAbs(history[i]) {
			result = rememberFolder(result, history[i])
		}
	}
	return result
}

func saveHistory(path string, history []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(history)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "folders-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}
