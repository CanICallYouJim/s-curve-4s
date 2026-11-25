package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Native UI snapshots do not include a conversion helper left running after
// its UI crashes. Include executable paths; on an incomplete snapshot skip
// cleanup rather than guess that an old app is unused.
func cacheRunningExecutables(root string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/bin/ps", "-axww", "-o", "comm=").Output()
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	self = canonicalCachePath(self)
	root = canonicalCachePath(root)
	foundSelf := false
	var paths []string
	for _, line := range strings.Split(string(output), "\n") {
		candidate := strings.TrimSpace(line)
		if !filepath.IsAbs(candidate) {
			continue
		}
		candidate = canonicalCachePath(candidate)
		if candidate == self {
			foundSelf = true
		}
		relative, e := filepath.Rel(root, candidate)
		if e == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			paths = append(paths, candidate)
		}
	}
	if !foundSelf {
		return nil, errors.New("неполный список работающих процессов; очистка пропущена")
	}
	return paths, nil
}
