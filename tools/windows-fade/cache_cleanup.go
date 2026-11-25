package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const abandonedUpdateAge = 24 * time.Hour
const stagingOwner = "ig-scurve-download-v1\n"

type updateCacheState struct {
	installedUpdate
	Previous *installedUpdate `json:"previous,omitempty"`
}

type cacheProtectedPaths []string

func (p *cacheProtectedPaths) String() string         { return strings.Join(*p, ",") }
func (p *cacheProtectedPaths) Set(value string) error { *p = append(*p, value); return nil }

func cacheRegularOrMissing(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("необычный файл служебного кеша")
	}
	return nil
}

func validCachePointer(root string, value installedUpdate) bool {
	if _, err := versionParts(value.Version); err != nil {
		return false
	}
	expected, err := updateTarget(filepath.Join(root, value.Version))
	return err == nil && value.Path == expected
}

func markedCacheVersion(root, version string) (installedUpdate, bool) {
	directory := filepath.Join(root, version)
	if _, err := versionParts(version); err != nil {
		return installedUpdate{}, false
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return installedUpdate{}, false
	}
	marker := filepath.Join(directory, ".asset-sha256")
	if err = cacheRegularOrMissing(marker); err != nil {
		return installedUpdate{}, false
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		return installedUpdate{}, false
	}
	hash, err := hex.DecodeString(string(data))
	if err != nil || len(hash) != 32 {
		return installedUpdate{}, false
	}
	target, err := updateTarget(directory)
	if err != nil {
		return installedUpdate{}, false
	}
	return installedUpdate{version, target}, true
}

func ownedCacheVersion(root, version string) (installedUpdate, bool) {
	value, ok := markedCacheVersion(root, version)
	return value, ok && validateUpdateTarget(value.Path) == nil
}

func cacheStateForInstall(root string, current installedUpdate, executable string) updateCacheState {
	state := updateCacheState{installedUpdate: current}
	if working, ok := ownedCacheVersion(root, appVersion); ok && runningFromUpdate(working.Path, executable, updatePlatform) {
		if older, e := newerVersion(current.Version, working.Version); e == nil && older {
			state.Previous = &working
		}
	}
	if state.Previous == nil {
		if old, e := readCacheState(root); e == nil && old.Previous != nil {
			if older, e := newerVersion(current.Version, old.Previous.Version); e == nil && older {
				state.Previous = old.Previous
			}
		}
	}
	return state
}

func readCacheState(root string) (updateCacheState, error) {
	var state updateCacheState
	path := filepath.Join(root, "current.json")
	if err := cacheRegularOrMissing(path); err != nil {
		return state, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return state, err
	}
	if err = json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	if !validCachePointer(root, state.installedUpdate) {
		return state, errors.New("неверный указатель кеша")
	}
	if state.Previous != nil {
		older, e := newerVersion(state.Version, state.Previous.Version)
		if e != nil || !older || !validCachePointer(root, *state.Previous) {
			return state, errors.New("неверный резервный указатель кеша")
		}
	}
	return state, nil
}

func cleanupUpdateCache(protected []string) error {
	root, err := updateCacheRoot()
	if err != nil {
		return err
	}
	if executable, e := os.Executable(); e == nil {
		protected = append(protected, executable)
	}
	running, err := cacheRunningExecutables(root)
	if err != nil {
		return err
	}
	protected = append(protected, running...)
	return pruneUpdateCache(root, protected, time.Now())
}

// Called only after the UI is shown. The installer holds the same OS lock;
// a live download is never pruned, and a crashed process releases it itself.
func pruneUpdateCache(root string, protected []string, now time.Time) error {
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("необычная папка кеша")
	}
	release, locked, err := tryCacheLock(filepath.Join(root, ".maintenance.lock"))
	if err != nil || !locked {
		return err
	}
	defer release()
	state, err := readCacheState(root)
	hasCurrent := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	versions := map[string]installedUpdate{}
	for _, entry := range entries {
		if value, ok := markedCacheVersion(root, entry.Name()); ok {
			versions[entry.Name()] = value
		}
	}
	keep := map[string]bool{}
	pruneVersions := hasCurrent && appVersion == state.Version
	if pruneVersions {
		if _, ok := ownedCacheVersion(root, state.Version); !ok {
			return errors.New("установленная версия повреждена; очистка пропущена")
		}
		keep[state.Version] = true
		if state.Previous != nil {
			if _, ok := ownedCacheVersion(root, state.Previous.Version); !ok {
				return errors.New("резервная версия повреждена; очистка пропущена")
			}
			keep[state.Previous.Version] = true
		} else {
			// Legacy pointers have no previous field: retain the nearest verified older payload.
			previous := ""
			for version := range versions {
				if _, ok := ownedCacheVersion(root, version); !ok {
					continue
				}
				older, _ := newerVersion(state.Version, version)
				newer := previous == ""
				if !newer {
					newer, _ = newerVersion(version, previous)
				}
				if older && newer {
					previous = version
				}
			}
			if previous != "" {
				keep[previous] = true
			}
		}
		for version, value := range versions {
			for _, path := range protected {
				if runningFromUpdate(canonicalCachePath(value.Path), canonicalCachePath(path), updatePlatform) {
					keep[version] = true
				}
			}
		}
	}
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			continue
		}
		if _, owned := versions[entry.Name()]; owned && pruneVersions && !keep[entry.Name()] {
			newer, _ := newerVersion(entry.Name(), state.Version)
			metadata, e := entry.Info()
			if e != nil {
				continue
			}
			if newer && now.Sub(metadata.ModTime()) < abandonedUpdateAge {
				continue
			}
			if !cacheVersionCanDelete(versions[entry.Name()].Path) {
				continue
			}
			// Preserve ownership proof until the last file is removed, allowing retries
			// after interruption without treating a partial app as unrelated data.
			if e = removeOwnedCacheDirectory(path); e != nil {
				continue
			}
		} else if strings.HasPrefix(entry.Name(), "install-") {
			metadata, e := entry.Info()
			if e != nil || now.Sub(metadata.ModTime()) < abandonedUpdateAge {
				continue
			}
			marker := filepath.Join(path, ".install-owner")
			if cacheRegularOrMissing(marker) != nil {
				continue
			}
			data, e := os.ReadFile(marker)
			if e != nil || string(data) != stagingOwner {
				continue
			}
			_ = os.RemoveAll(path)
		}
	}
	return nil
}

func removeOwnedCacheDirectory(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".asset-sha256" {
			continue
		}
		if err = os.RemoveAll(filepath.Join(path, entry.Name())); err != nil {
			return fmt.Errorf("очистка версии: %w", err)
		}
	}
	if err = os.Remove(filepath.Join(path, ".asset-sha256")); err != nil {
		return err
	}
	return os.Remove(path)
}

func canonicalCachePath(path string) string {
	if actual, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(actual)
	}
	return filepath.Clean(path)
}
