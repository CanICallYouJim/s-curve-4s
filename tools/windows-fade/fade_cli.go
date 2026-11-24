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
)

type fadeResult struct {
	Output string `json:"output"`
	Done   int    `json:"done"`
	Failed int    `json:"failed"`
	Error  string `json:"error"`
}

func toolsInDirectory(folder string) (tools, error) {
	result := tools{filepath.Join(folder, "ffmpeg"), filepath.Join(folder, "ffprobe")}
	for _, binary := range []*string{&result.ffmpeg, &result.ffprobe} {
		found, err := exec.LookPath(*binary)
		if err != nil {
			return tools{}, fmt.Errorf("инструмент %s: %w", *binary, err)
		}
		*binary = found
	}
	return result, nil
}

func runFade(args []string) (err error) {
	flags := flag.NewFlagSet("S-curve-4s", flag.ContinueOnError)
	folder := flags.String("folder", "", "Папка с треками (без подпапок)")
	toolsFolder := flags.String("tools", "", "Папка с FFmpeg и ffprobe")
	resultPath := flags.String("result", "", "JSON с итогами для интерфейса")
	if err = flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() == 1 && *folder == "" {
		*folder = flags.Arg(0)
	} else if flags.NArg() != 0 {
		return errors.New("укажите только одну папку")
	}
	result := fadeResult{}
	if *resultPath != "" {
		defer func() {
			if err != nil {
				result.Error = err.Error()
			}
			data, e := json.Marshal(result)
			if e == nil {
				e = os.WriteFile(*resultPath, data, 0600)
			}
			if e != nil {
				err = fmt.Errorf("запись результата: %w", e)
			}
		}()
	}
	settings, err := historyFile()
	if err != nil {
		return err
	}
	history := loadHistory(settings)
	if *folder == "" {
		*folder, err = chooseFolder(history)
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
	info, err := os.Stat(*folder)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("выберите существующую папку")
	}
	if err = saveHistory(settings, rememberFolder(history, *folder)); err != nil {
		return fmt.Errorf("сохранение истории папок: %w", err)
	}
	fmt.Println("S-кривая — плавное нарастание громкости за 4 секунды")
	fmt.Println("Частота, каналы, разрядность, кодек и заданный битрейт берутся из исходника.")
	fmt.Println("VBR: средний битрейт после обработки может отличаться.")
	var t tools
	var temp string
	if *toolsFolder != "" {
		t, err = toolsInDirectory(*toolsFolder)
	} else {
		t, temp, err = unpack()
	}
	if err != nil {
		return err
	}
	if temp != "" {
		defer os.RemoveAll(temp)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	result.Output, result.Done, result.Failed, err = processFolder(ctx, *folder, t, os.Stdout)
	if err == nil && result.Failed > 0 {
		err = fmt.Errorf("не обработано файлов: %d; подробности в отчёте", result.Failed)
	}
	return err
}
