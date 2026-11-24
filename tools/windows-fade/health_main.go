package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
)

// The native Mac window reads this result instead of guessing from console text.
type checkSummary struct {
	Output      string `json:"output"`
	Total       int    `json:"total"`
	Checked     int    `json:"checked"`
	Fresh       int    `json:"fresh"`
	Cached      int    `json:"cached"`
	Errors      int    `json:"errors"`
	Warnings    int    `json:"warnings"`
	Incomplete  int    `json:"incomplete"`
	Interrupted bool   `json:"interrupted"`
	Error       string `json:"error"`
}

func runCheck() error { return runCheckArgs(os.Args[1:]) }

func runCheckArgs(args []string) (err error) {
	flags := flag.NewFlagSet("Music-check", flag.ContinueOnError)
	folder := flags.String("folder", "", "Папка с музыкальной библиотекой")
	recursive := flags.Bool("recursive", true, "Проверять также подпапки")
	toolsFolder := flags.String("tools", "", "Папка со встроенными FFmpeg и ffprobe")
	resultPath := flags.String("result", "", "JSON с итогами для окна приложения")
	if err = flags.Parse(args); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	result := checkSummary{}
	if *resultPath != "" {
		defer func() {
			if err != nil {
				result.Error = err.Error()
			}
			result.Interrupted = result.Interrupted || ctx.Err() != nil || errors.Is(err, context.Canceled)
			data, e := json.Marshal(result)
			if e == nil {
				e = os.WriteFile(*resultPath, data, 0600)
			}
			if e != nil {
				err = fmt.Errorf("сохранение итогов проверки: %w", e)
			}
		}()
	}
	if flags.NArg() != 0 {
		return errors.New("укажите папку через --folder")
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	historyPath := filepath.Join(config, "LenaDJ", "MusicCheck", "folders.json")
	history := loadHistory(historyPath)
	interactive := *folder == ""
	if *folder == "" {
		*folder, *recursive, err = chooseCheckFolder(history)
		if err != nil {
			return err
		}
		if *folder == "" {
			return errCancelled
		}
	}
	*folder, err = filepath.Abs(*folder)
	if err != nil {
		return err
	}
	if interactive {
		if err = saveHistory(historyPath, rememberFolder(history, *folder)); err != nil {
			fmt.Fprintln(os.Stderr, "История папок не сохранена:", err)
		}
	}
	var t tools
	if *toolsFolder != "" {
		base, e := filepath.Abs(*toolsFolder)
		if e != nil {
			return e
		}
		t.ffmpeg, err = exec.LookPath(filepath.Join(base, "ffmpeg"))
		if err != nil {
			return fmt.Errorf("не удалось запустить встроенный FFmpeg: %w", err)
		}
		t.ffprobe, err = exec.LookPath(filepath.Join(base, "ffprobe"))
		if err != nil {
			return fmt.Errorf("не удалось запустить встроенный ffprobe: %w", err)
		}
	} else if runtime.GOOS == "windows" {
		var temp string
		fmt.Println("Подготовка к проверке музыки…")
		t, temp, err = unpack()
		if err != nil {
			return err
		}
		defer os.RemoveAll(temp)
	} else {
		t.ffmpeg, err = exec.LookPath("ffmpeg")
		if err != nil {
			return err
		}
		t.ffprobe, err = exec.LookPath("ffprobe")
		if err != nil {
			return err
		}
	}
	profile, err := t.checkProfile(ctx)
	if err != nil {
		return fmt.Errorf("проверка инструментов: %w", err)
	}
	fmt.Println("Проверка музыкальной библиотеки. Анализ выполняется по одному файлу.")
	fmt.Println("Проверяем, изменились ли файлы. Для неизменившихся записей используем прошлый результат.")
	report, err := scanHealth(ctx, *folder, *recursive, profile, t.analyzeHealth, os.Stdout)
	if err != nil {
		return err
	}
	fmt.Println("Подготовка встроенных фрагментов для прослушивания в отчёте…")
	output, err := saveHealthReportWithPreviews(ctx, report, t)
	if err != nil {
		return err
	}
	stats := healthStats(report)
	result = checkSummary{Output: output, Total: stats.Total, Checked: stats.Checked, Fresh: stats.Fresh, Cached: stats.Cached, Errors: stats.Errors, Warnings: stats.Warnings, Incomplete: stats.Incomplete, Interrupted: report.Interrupted || ctx.Err() != nil}
	fmt.Printf("\nНайдено: %d. Проверено: %d. Заново: %d. Из истории: %d.\nТребует внимания: %d. Стоит прослушать: %d. Проверка не закончена: %d.\nОтчёт: %s\n", stats.Total, stats.Checked, stats.Fresh, stats.Cached, stats.Errors, stats.Warnings, stats.Incomplete, output)
	return nil
}
