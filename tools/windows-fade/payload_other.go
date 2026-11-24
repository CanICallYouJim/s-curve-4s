//go:build !windows

package main

import "os/exec"

// macOS .app supplies its bundled tools with --tools; CLI may use PATH.
func unpack() (tools, string, error) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return tools{}, "", err
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return tools{}, "", err
	}
	return tools{ffmpeg, ffprobe}, "", nil
}
