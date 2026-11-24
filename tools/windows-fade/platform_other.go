//go:build !windows

package main

import "os/exec"

func configureConsole()              {}
func configureCommand(cmd *exec.Cmd) {}

func chooseFolder(history []string) (string, error) { return "", nil }

func chooseCheckFolder(history []string) (string, bool, error) { return "", false, nil }

var releaseGUIInstance = func() {}

func acquireGUIInstance() (bool, error) { return true, nil }
