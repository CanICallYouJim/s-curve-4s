package main

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"io"
	"os"
	"path/filepath"
)

//go:embed payload.zip
var payload []byte

func unpack() (tools, string, error) {
	folder, err := os.MkdirTemp("", "lena-fade-")
	if err != nil {
		return tools{}, "", err
	}
	fail := func(e error) (tools, string, error) { os.RemoveAll(folder); return tools{}, "", e }
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return fail(err)
	}
	for _, file := range archive.File {
		if file.Name != "ffmpeg.exe" && file.Name != "ffprobe.exe" {
			continue
		}
		if err = func() error {
			input, e := file.Open()
			if e != nil {
				return e
			}
			defer input.Close()
			output, e := os.OpenFile(filepath.Join(folder, file.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
			if e != nil {
				return e
			}
			_, e = io.Copy(output, input)
			closeErr := output.Close()
			if e != nil {
				return e
			}
			return closeErr
		}(); err != nil {
			return fail(err)
		}
	}
	result := tools{filepath.Join(folder, "ffmpeg.exe"), filepath.Join(folder, "ffprobe.exe")}
	for _, path := range []string{result.ffmpeg, result.ffprobe} {
		if _, err = os.Stat(path); err != nil {
			return fail(err)
		}
	}
	return result, folder, nil
}
