package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func cacheFixture(t *testing.T) (string, time.Time) {
	t.Helper()
	root := updateTestHome(t)
	updatePlatform = "windows"
	appVersion = "4.0.0"
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"1.0.0", "2.0.0", "3.0.0", "4.0.0"} {
		directory := filepath.Join(root, version)
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string]string{"app.exe": "MZtest", ".asset-sha256": strings.Repeat("a", 64)} {
			if err := os.WriteFile(filepath.Join(directory, name), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	current, _ := ownedCacheVersion(root, "4.0.0")
	previous, _ := ownedCacheVersion(root, "3.0.0")
	writeCacheState(t, root, updateCacheState{current, &previous})
	return root, time.Now()
}

func writeCacheState(t *testing.T, root string, state updateCacheState) {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "current.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func cacheExists(t *testing.T, path string, want bool) {
	t.Helper()
	_, err := os.Lstat(path)
	if (err == nil) != want {
		t.Fatalf("exists %s = %v, want %v (%v)", path, err == nil, want, err)
	}
}

func cacheStaging(t *testing.T, root, name string, age time.Duration, owned bool, now time.Time) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "payload"), []byte("partial download"), 0600); err != nil {
		t.Fatal(err)
	}
	if owned {
		if err := os.WriteFile(filepath.Join(path, ".install-owner"), []byte(stagingOwner), 0600); err != nil {
			t.Fatal(err)
		}
	}
	old := now.Add(-age)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUpdateCacheRetentionAndStaging(t *testing.T) {
	root, now := cacheFixture(t)
	stale := cacheStaging(t, root, "install-owned", 48*time.Hour, true, now)
	fresh := cacheStaging(t, root, "install-fresh", time.Hour, true, now)
	legacy := cacheStaging(t, root, "install-unmarked", 48*time.Hour, false, now)
	unrelated := filepath.Join(root, "personal-notes")
	if err := os.Mkdir(unrelated, 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("user"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "0.9.0")
	linked := os.Symlink(outside, link) == nil
	if err := pruneUpdateCache(root, nil, now); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"1.0.0", "2.0.0"} {
		cacheExists(t, filepath.Join(root, version), false)
	}
	for _, version := range []string{"3.0.0", "4.0.0"} {
		cacheExists(t, filepath.Join(root, version), true)
	}
	cacheExists(t, stale, false)
	cacheExists(t, fresh, true)
	cacheExists(t, legacy, true)
	cacheExists(t, unrelated, true)
	cacheExists(t, filepath.Join(outside, "keep"), true)
	if linked {
		cacheExists(t, link, true)
	}
}

func TestUpdateCacheProtectsRunningOldVersion(t *testing.T) {
	root, now := cacheFixture(t)
	if err := pruneUpdateCache(root, []string{filepath.Join(root, "1.0.0", "app.exe")}, now); err != nil {
		t.Fatal(err)
	}
	cacheExists(t, filepath.Join(root, "1.0.0"), true)
	cacheExists(t, filepath.Join(root, "2.0.0"), false)
}

func TestUpdateCacheOldLauncherDoesNotPruneVersions(t *testing.T) {
	root, now := cacheFixture(t)
	appVersion = "3.0.0"
	if err := pruneUpdateCache(root, nil, now); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"1.0.0", "2.0.0", "3.0.0", "4.0.0"} {
		cacheExists(t, filepath.Join(root, version), true)
	}
}

func TestUpdateCacheMalformedPointerPreservesEverything(t *testing.T) {
	root, now := cacheFixture(t)
	stale := cacheStaging(t, root, "install-owned", 48*time.Hour, true, now)
	if err := os.WriteFile(filepath.Join(root, "current.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := pruneUpdateCache(root, nil, now); err == nil {
		t.Fatal("malformed pointer ignored")
	}
	for _, version := range []string{"1.0.0", "2.0.0", "3.0.0", "4.0.0"} {
		cacheExists(t, filepath.Join(root, version), true)
	}
	cacheExists(t, stale, true)
}

func TestUpdateCacheActiveInstallerBlocksCleanup(t *testing.T) {
	root, now := cacheFixture(t)
	stale := cacheStaging(t, root, "install-owned", 48*time.Hour, true, now)
	release, locked, err := tryCacheLock(filepath.Join(root, ".maintenance.lock"))
	if err != nil || !locked {
		t.Fatalf("initial lock: %v %v", locked, err)
	}
	defer release()
	if err = pruneUpdateCache(root, nil, now); err != nil {
		t.Fatal(err)
	}
	cacheExists(t, stale, true)
	cacheExists(t, filepath.Join(root, "1.0.0"), true)
	release()
	if err = pruneUpdateCache(root, nil, now); err != nil {
		t.Fatal(err)
	}
	cacheExists(t, stale, false)
}

func TestUpdateCachePreviousComesFromWorkingVersion(t *testing.T) {
	root, _ := cacheFixture(t)
	known, _ := ownedCacheVersion(root, "2.0.0")
	failed, _ := ownedCacheVersion(root, "4.0.0")
	writeCacheState(t, root, updateCacheState{failed, &known})
	appVersion = "0.1.0" // loose launcher; no owned cached payload
	target, _ := updateTarget(filepath.Join(root, "5.0.0"))
	if err := saveInstalledUpdate(root, installedUpdate{"5.0.0", target}); err != nil {
		t.Fatal(err)
	}
	state, err := readCacheState(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.Previous == nil || state.Previous.Version != "2.0.0" {
		t.Fatalf("failed version replaced rollback: %+v", state)
	}
	appVersion = "3.0.0"
	target, _ = updateTarget(filepath.Join(root, "6.0.0"))
	loose := cacheStateForInstall(root, installedUpdate{"6.0.0", target}, filepath.Join(t.TempDir(), "app.exe"))
	if loose.Previous == nil || loose.Previous.Version != "2.0.0" {
		t.Fatal("same-version unbooted cache replaced known rollback")
	}

	state = cacheStateForInstall(root, installedUpdate{"6.0.0", target}, filepath.Join(root, "3.0.0", "app.exe"))
	if state.Previous == nil || state.Previous.Version != "3.0.0" {
		t.Fatalf("running version not retained: %+v", state)
	}
}

func TestUpdateCacheLegacyPointerAndInterruptedRemoval(t *testing.T) {
	root, now := cacheFixture(t)
	current, _ := ownedCacheVersion(root, "4.0.0")
	writeCacheState(t, root, updateCacheState{installedUpdate: current})
	if err := os.Remove(filepath.Join(root, "1.0.0", "app.exe")); err != nil {
		t.Fatal(err)
	}
	if err := pruneUpdateCache(root, nil, now); err != nil {
		t.Fatal(err)
	}
	cacheExists(t, filepath.Join(root, "1.0.0"), false)
	cacheExists(t, filepath.Join(root, "3.0.0"), true)
}

func TestUpdateCacheDamagedPreviousPreservesOlderGoodVersions(t *testing.T) {
	root, now := cacheFixture(t)
	if err := os.Remove(filepath.Join(root, "3.0.0", "app.exe")); err != nil {
		t.Fatal(err)
	}
	if err := pruneUpdateCache(root, nil, now); err == nil {
		t.Fatal("damaged rollback must prevent version deletion")
	}
	cacheExists(t, filepath.Join(root, "2.0.0", "app.exe"), true)
}
