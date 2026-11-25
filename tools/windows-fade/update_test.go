package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type updateRoundTrip func(*http.Request) (*http.Response, error)

func (f updateRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func updateTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	previousClient, previousPlatform, previousVersion := updateHTTPClient, updatePlatform, appVersion
	t.Cleanup(func() {
		updateHTTPClient = previousClient
		updatePlatform = previousPlatform
		appVersion = previousVersion
	})
	appVersion = "1.2.0"
	root, err := updateCacheRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func updateFixture(t *testing.T, platform string, payload []byte, checksum string) {
	t.Helper()
	updatePlatform = platform
	sum := sha256.Sum256(payload)
	if checksum == "" {
		checksum = hex.EncodeToString(sum[:])
	}
	asset := updateAsset{URL: "https://github.com/" + releaseRepository + "/releases/download/v1.3.0/app.bin", SHA256: checksum}
	manifest := updateManifest{Version: "1.3.0", Notes: "Новая версия", Windows: asset, MacOS: asset}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "update.json") {
			_ = json.NewEncoder(w).Encode(manifest)
			return
		}
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)
	updateHTTPClient = &http.Client{Transport: updateRoundTrip(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		copy.URL, _ = url.Parse(server.URL + r.URL.Path)
		return http.DefaultTransport.RoundTrip(copy)
	})}
}

func TestUpdateVersionsAndOffline(t *testing.T) {
	updateTestHome(t)
	for _, test := range []struct {
		candidate, current string
		want               bool
	}{
		{"1.3.0", "1.2.0", true}, {"1.10.0", "1.9.9", true}, {"2.0.0", "1.99.0", true}, {"1.2.0", "1.2.0", false}, {"1.1.9", "1.2.0", false},
	} {
		got, err := newerVersion(test.candidate, test.current)
		if err != nil || got != test.want {
			t.Fatalf("%+v: %v %v", test, got, err)
		}
	}
	for _, bad := range []string{"../1.3.0", "v1.3.0", "1.3", "1.3.0-beta", "01.3.0", "1.-1.0", "1.2.3.4"} {
		if _, err := versionParts(bad); err == nil {
			t.Fatal("accepted version", bad)
		}
	}
	updateHTTPClient = &http.Client{Transport: updateRoundTrip(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	status := checkAppUpdate(ctx)
	if status.Available || status.Current != "1.2.0" || status.Error == "" || time.Since(start) > time.Second {
		t.Fatalf("offline result: %+v", status)
	}
}

func TestUpdateWindowsInstallAndHashFailure(t *testing.T) {
	root := updateTestHome(t)
	payload := []byte("MZ\x00\x00test release executable")
	updateFixture(t, "windows", payload, "")
	status := checkAppUpdate(context.Background())
	if !status.Available || status.Latest != "1.3.0" || status.Error != "" {
		t.Fatalf("status: %+v", status)
	}
	target, err := installAppUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join(root, "1.3.0", "app.exe") {
		t.Fatal(target)
	}
	data, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("download: %q %v", data, err)
	}
	if cached, err := cachedUpdateTarget(); err != nil || cached != target {
		t.Fatalf("cache: %q %v", cached, err)
	}
	// Recover a verified payload when pointer publication was interrupted.
	if err = os.Remove(filepath.Join(root, "current.json")); err != nil {
		t.Fatal(err)
	}
	if recovered, err := installAppUpdate(context.Background()); err != nil || recovered != target {
		t.Fatalf("recover: %q %v", recovered, err)
	}
	before, err := os.ReadFile(filepath.Join(root, "current.json"))
	if err != nil {
		t.Fatal(err)
	}
	// A conflicting release cannot replace an already published version or pointer.
	updateFixture(t, "windows", []byte("MZchanged"), strings.Repeat("0", 64))
	if _, err = installAppUpdate(context.Background()); err == nil {
		t.Fatal("conflicting checksum accepted")
	}
	after, _ := os.ReadFile(filepath.Join(root, "current.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("current pointer changed after failed install")
	}
	appVersion = "1.3.0"
	if cached, err := cachedUpdateTarget(); err != nil || cached != "" {
		t.Fatalf("would recurse into same version: %q %v", cached, err)
	}
}

func TestUpdateHashMismatchLeavesExistingState(t *testing.T) {
	root := updateTestHome(t)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	previous := []byte(`{"version":"1.2.0","path":"existing"}`)
	if err := os.WriteFile(filepath.Join(root, "current.json"), previous, 0600); err != nil {
		t.Fatal(err)
	}
	updateFixture(t, "windows", []byte("MZinvalid hash"), strings.Repeat("0", 64))
	if _, err := installAppUpdate(context.Background()); err == nil || !strings.Contains(err.Error(), "контрольная сумма") {
		t.Fatalf("hash mismatch: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "current.json"))
	if !bytes.Equal(previous, after) {
		t.Fatal("old pointer lost")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || (entry.Name() != "current.json" && entry.Name() != ".maintenance.lock") {
			t.Fatal("unverified payload left published", entries)
		}
	}
}

func testUpdateZip(t *testing.T, names []string, symlink bool) []byte {
	t.Helper()
	var raw bytes.Buffer
	archive := zip.NewWriter(&raw)
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		mode := os.FileMode(0644)
		if strings.HasSuffix(name, "SCurve") {
			mode = 0755
		}
		if symlink {
			mode = os.ModeSymlink | 0777
		}
		header.SetMode(mode)
		entry, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write([]byte("test content")); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func TestUpdateMacInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS installation requires Unix executable permissions; exercised by macOS CI")
	}
	root := updateTestHome(t)
	payload := testUpdateZip(t, []string{updateAppName + "/Contents/Info.plist", updateAppName + "/Contents/MacOS/SCurve"}, false)
	updateFixture(t, "darwin", payload, "")
	target, err := installAppUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join(root, "1.3.0", updateAppName) {
		t.Fatal(target)
	}
	info, err := os.Stat(filepath.Join(target, "Contents/MacOS/SCurve"))
	if err != nil || info.Mode().Perm()&0111 == 0 {
		t.Fatalf("executable permissions: %v %v", info, err)
	}
	if cached, err := cachedUpdateTarget(); err != nil || cached != target {
		t.Fatalf("cache: %q %v", cached, err)
	}
}

func TestUpdateRejectsUnsafeZip(t *testing.T) {
	var err error
	for _, name := range []string{"../outside", "/absolute", updateAppName + "/../outside", updateAppName + "\\Contents\\file", "other.app/Contents/file", updateAppName + "//file"} {
		archive := filepath.Join(t.TempDir(), "update.zip")
		if err = os.WriteFile(archive, testUpdateZip(t, []string{name}, false), 0600); err != nil {
			t.Fatal(err)
		}
		if err = extractUpdate(archive, t.TempDir()); err == nil {
			t.Fatal("unsafe ZIP accepted", name)
		}
	}
	archive := filepath.Join(t.TempDir(), "link.zip")
	if err = os.WriteFile(archive, testUpdateZip(t, []string{updateAppName + "/link"}, true), 0600); err != nil {
		t.Fatal(err)
	}
	if err = extractUpdate(archive, t.TempDir()); err == nil {
		t.Fatal("ZIP symlink accepted")
	}
}

func TestUpdateRejectsForeignAssetsAndCachePaths(t *testing.T) {
	root := updateTestHome(t)
	updatePlatform = "windows"
	for _, asset := range []updateAsset{
		{"http://github.com/" + releaseRepository + "/releases/download/v1/app.exe", strings.Repeat("0", 64)},
		{"https://example.com/app.exe", strings.Repeat("0", 64)},
		{"https://github.com/other/repo/releases/download/v1/app.exe", strings.Repeat("0", 64)},
		{"https://github.com/" + releaseRepository + "/releases/download/v1/app.exe", "invalid"},
	} {
		if err := validateUpdateAsset(asset); err == nil {
			t.Fatal("foreign asset accepted", asset)
		}
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(installedUpdate{"1.3.0", filepath.Join(t.TempDir(), "outside.exe")})
	if err := os.WriteFile(filepath.Join(root, "current.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := cachedUpdateTarget(); err == nil {
		t.Fatal("cache path outside store accepted")
	}
	updateHTTPClient = &http.Client{Transport: updateRoundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}
	if status := checkAppUpdate(context.Background()); status.Available || status.Error == "" {
		t.Fatalf("offline advertised update: %+v", status)
	}
}

func TestUpdateNeverForwardsToItself(t *testing.T) {
	root := t.TempDir()
	win := filepath.Join(root, "app.exe")
	if !runningFromUpdate(win, win, "windows") {
		t.Fatal("Windows self-forward")
	}
	app := filepath.Join(root, "S-кривая 4 с.app")
	if !runningFromUpdate(app, filepath.Join(app, "Contents", "MacOS", "fade-engine"), "darwin") {
		t.Fatal("Mac self-forward")
	}
	if runningFromUpdate(app, filepath.Join(root, "other.app", "Contents", "MacOS", "fade-engine"), "darwin") {
		t.Fatal("different app suppressed")
	}
}
