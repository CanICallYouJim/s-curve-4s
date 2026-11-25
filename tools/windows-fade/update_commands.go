package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
)

func updateCommand(args []string) (bool, error) {
	if len(args) == 0 || (args[0] != "--update-status" && args[0] != "--install-update") {
		return false, nil
	}
	flags := flag.NewFlagSet("update", flag.ContinueOnError)
	result := flags.String("result", "", "JSON result path")
	cleanup := flags.Bool("cleanup-cache", false, "Очистить кеш после показа окна")
	var protected cacheProtectedPaths
	flags.Var(&protected, "protect-app", "Путь запущенной версии")
	if err := flags.Parse(args[1:]); err != nil {
		return true, err
	}
	if *result == "" {
		return true, fmt.Errorf("не указан файл результата")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	var response any
	var err error
	if args[0] == "--update-status" {
		if *cleanup {
			_ = cleanupUpdateCache(protected)
		}
		target, _ := cachedUpdateTarget()
		status := UpdateStatus{Current: appVersion}
		if target == "" {
			status = checkAppUpdate(ctx)
		}
		response = struct {
			UpdateStatus
			Target string `json:"target"`
		}{status, target}
	} else {
		target, e := installAppUpdate(ctx)
		err = e
		message := ""
		if err != nil {
			message = err.Error()
		}
		response = struct {
			Target string `json:"target"`
			Error  string `json:"error"`
		}{target, message}
	}
	data, e := json.Marshal(response)
	if e == nil {
		e = os.WriteFile(*result, data, 0600)
	}
	if e != nil {
		return true, e
	}
	return true, err
}

func startUpdatedApp(target string) error {
	cmd := exec.Command(target)
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("запуск обновления: %w", err)
	}
	return cmd.Process.Release()
}
