package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var appVersion = "1.2.2"
var releaseRepository = "CanICallYouJim/s-curve-4s"

// Request deadlines are scoped separately: a small manifest must arrive
// promptly, while the bundled offline audio tools may take longer to download.
var updateHTTPClient = &http.Client{}
var updatePlatform = runtime.GOOS

const maxUpdateDownload int64 = 600 << 20
const maxUpdateExpanded uint64 = 1 << 30
const updateAppName = "S-кривая 4 с.app"

type UpdateStatus struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	Notes     string `json:"notes"`
	Error     string `json:"error"`
}

type updateAsset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type updateManifest struct {
	Version string      `json:"version"`
	Notes   string      `json:"notes"`
	Windows updateAsset `json:"windows"`
	MacOS   updateAsset `json:"macos"`
}

type installedUpdate struct {
	Version string `json:"version"`
	Path    string `json:"path"`
}

func versionParts(version string) ([3]uint64, error) {
	var result [3]uint64
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return result, fmt.Errorf("неверная версия %q", version)
	}
	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return result, fmt.Errorf("неверная версия %q", version)
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return result, fmt.Errorf("неверная версия %q", version)
			}
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return result, err
		}
		result[i] = n
	}
	return result, nil
}

func newerVersion(candidate, current string) (bool, error) {
	a, err := versionParts(candidate)
	if err != nil {
		return false, err
	}
	b, err := versionParts(current)
	if err != nil {
		return false, err
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i], nil
		}
	}
	return false, nil
}

func fetchUpdateManifest(ctx context.Context) (updateManifest, error) {
	var manifest updateManifest
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	endpoint := "https://github.com/" + releaseRepository + "/releases/latest/download/update.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return manifest, err
	}
	req.Header.Set("User-Agent", "IG-SCurve/"+appVersion)
	response, err := updateHTTPClient.Do(req)
	if err != nil {
		return manifest, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return manifest, fmt.Errorf("проверка обновлений: HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return manifest, err
	}
	if len(raw) > 1<<20 {
		return manifest, errors.New("слишком большой файл обновления")
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return manifest, fmt.Errorf("файл обновления: %w", err)
	}
	if _, err = versionParts(manifest.Version); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func checkAppUpdate(ctx context.Context) UpdateStatus {
	result := UpdateStatus{Current: appVersion}
	manifest, err := fetchUpdateManifest(ctx)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Latest = manifest.Version
	result.Notes = manifest.Notes
	result.Available, err = newerVersion(manifest.Version, appVersion)
	if err != nil {
		result.Error = err.Error()
	}
	return result
}

func updateCacheRoot() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "IG", "SCurve", "updates"), nil
}

func updateTarget(directory string) (string, error) {
	switch updatePlatform {
	case "windows":
		return filepath.Join(directory, "app.exe"), nil
	case "darwin":
		return filepath.Join(directory, updateAppName), nil
	default:
		return "", errors.New("обновление доступно только в Windows и macOS")
	}
}

func validateUpdateTarget(target string) error {
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("путь обновления не может быть ссылкой")
	}
	if updatePlatform == "windows" {
		if !info.Mode().IsRegular() {
			return errors.New("обновление EXE не является файлом")
		}
		file, err := os.Open(target)
		if err != nil {
			return err
		}
		defer file.Close()
		var magic [2]byte
		if _, err = io.ReadFull(file, magic[:]); err != nil {
			return err
		}
		if string(magic[:]) != "MZ" {
			return errors.New("обновление не является Windows EXE")
		}
	} else {
		if !info.IsDir() {
			return errors.New("обновление macOS не является приложением")
		}
		for _, name := range []string{"Contents/Info.plist", "Contents/MacOS/SCurve"} {
			entry, e := os.Lstat(filepath.Join(target, filepath.FromSlash(name)))
			if e != nil {
				return e
			}
			if !entry.Mode().IsRegular() {
				return errors.New("неполное приложение macOS")
			}
			if strings.HasSuffix(name, "SCurve") && entry.Mode().Perm()&0111 == 0 {
				return errors.New("приложение macOS не имеет права запуска")
			}
		}
	}
	return nil
}

func cachedUpdateTarget() (string, error) {
	root, err := updateCacheRoot()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(root, "current.json"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var current installedUpdate
	if err = json.Unmarshal(raw, &current); err != nil {
		return "", err
	}
	newer, err := newerVersion(current.Version, appVersion)
	if err != nil {
		return "", err
	}
	if !newer {
		return "", nil
	}
	directory := filepath.Join(root, current.Version)
	expected, err := updateTarget(directory)
	if err != nil {
		return "", err
	}
	if current.Path != expected {
		return "", errors.New("путь обновления вне хранилища приложения")
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("папка обновления повреждена")
	}
	if err = validateUpdateTarget(expected); err != nil {
		return "", err
	}
	executable, _ := os.Executable()
	if runningFromUpdate(expected, executable, updatePlatform) {
		return "", nil
	}
	return expected, nil
}

func validateUpdateAsset(asset updateAsset) error {
	parsed, err := url.Parse(asset.URL)
	if err != nil {
		return err
	}
	prefix := "/" + releaseRepository + "/releases/download/"
	if parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !strings.HasPrefix(parsed.Path, prefix) || strings.TrimPrefix(parsed.Path, prefix) == "" {
		return errors.New("файл обновления должен находиться в GitHub Releases этой программы")
	}
	hash, err := hex.DecodeString(asset.SHA256)
	if err != nil || len(hash) != sha256.Size {
		return errors.New("неверная контрольная сумма обновления")
	}
	return nil
}

func downloadUpdate(ctx context.Context, asset updateAsset, destination string) error {
	if err := validateUpdateAsset(asset); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "IG-SCurve/"+appVersion)
	response, err := updateHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("загрузка обновления: HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxUpdateDownload {
		return errors.New("слишком большой файл обновления")
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxUpdateDownload+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if size > maxUpdateDownload {
		return errors.New("слишком большой файл обновления")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), asset.SHA256) {
		return errors.New("контрольная сумма обновления не совпадает; установка отменена")
	}
	return nil
}

func extractUpdate(archive, destination string) error {
	source, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer source.Close()
	var expanded uint64
	for _, entry := range source.File {
		clean := path.Clean(entry.Name)
		if strings.Contains(entry.Name, "\\") || clean != strings.TrimSuffix(entry.Name, "/") || (clean != updateAppName && !strings.HasPrefix(clean, updateAppName+"/")) {
			return fmt.Errorf("недопустимый путь в обновлении: %q", entry.Name)
		}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || (!mode.IsRegular() && !mode.IsDir()) {
			return errors.New("ссылки и специальные файлы в обновлении запрещены")
		}
		if entry.UncompressedSize64 > maxUpdateExpanded-expanded {
			return errors.New("слишком большой распакованный файл обновления")
		}
		expanded += entry.UncompressedSize64
		target := filepath.Join(destination, filepath.FromSlash(clean))
		if mode.IsDir() {
			if err = os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		input, err := entry.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, io.LimitReader(input, int64(entry.UncompressedSize64)+1))
		inputErr := input.Close()
		outputErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputErr != nil {
			return inputErr
		}
		if outputErr != nil {
			return outputErr
		}
	}
	return nil
}

func saveInstalledUpdate(root string, current installedUpdate) error {
	// A failed newer pointer cannot replace the version actually executing.
	executable, _ := os.Executable()
	state := cacheStateForInstall(root, current, executable)
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(root, "current-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), filepath.Join(root, "current.json"))
}

func installAppUpdate(ctx context.Context) (string, error) {
	manifest, err := fetchUpdateManifest(ctx)
	if err != nil {
		return "", err
	}
	newer, err := newerVersion(manifest.Version, appVersion)
	if err != nil {
		return "", err
	}
	if !newer {
		return "", errors.New("уже установлена актуальная версия")
	}
	asset := manifest.Windows
	if updatePlatform == "darwin" {
		asset = manifest.MacOS
	}
	if err = validateUpdateAsset(asset); err != nil {
		return "", err
	}
	root, err := updateCacheRoot()
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	release, locked, err := tryCacheLock(filepath.Join(root, ".maintenance.lock"))
	if err != nil {
		return "", err
	}
	if !locked {
		return "", errors.New("обновление уже устанавливается; повторите позже")
	}
	defer release()
	finalDirectory := filepath.Join(root, manifest.Version)
	target, err := updateTarget(finalDirectory)
	if err != nil {
		return "", err
	}
	// A previous attempt may have published the verified payload but failed to
	// save the pointer. Reuse it only if it belongs to this exact release asset.
	if _, err = os.Lstat(finalDirectory); err == nil {
		info, e := os.Lstat(finalDirectory)
		if e != nil {
			return "", e
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", errors.New("папка обновления повреждена")
		}
		stored, e := os.ReadFile(filepath.Join(finalDirectory, ".asset-sha256"))
		if e != nil {
			return "", e
		}
		if !strings.EqualFold(string(stored), asset.SHA256) {
			return "", errors.New("версия уже существует с другим файлом обновления")
		}
		if e = validateUpdateTarget(target); e != nil {
			return "", e
		}
		return target, saveInstalledUpdate(root, installedUpdate{manifest.Version, target})
	} else if !os.IsNotExist(err) {
		return "", err
	}
	staging, err := os.MkdirTemp(root, "install-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	if err = os.WriteFile(filepath.Join(staging, ".install-owner"), []byte(stagingOwner), 0600); err != nil {
		return "", err
	}
	archive := filepath.Join(staging, "payload")
	if err = downloadUpdate(ctx, asset, archive); err != nil {
		return "", err
	}
	stagedTarget, _ := updateTarget(staging)
	if updatePlatform == "windows" {
		if err = os.Rename(archive, stagedTarget); err != nil {
			return "", err
		}
		if err = os.Chmod(stagedTarget, 0700); err != nil {
			return "", err
		}
	} else {
		if err = extractUpdate(archive, staging); err != nil {
			return "", err
		}
		if err = os.Remove(archive); err != nil {
			return "", err
		}
	}
	if err = validateUpdateTarget(stagedTarget); err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(staging, ".asset-sha256"), []byte(asset.SHA256), 0600); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	// Keep ownership proof until the atomic move: a crash before rename must
	// leave a recognizable staging directory for the next cleanup.
	if err = os.Rename(staging, finalDirectory); err != nil {
		return "", err
	}
	_ = os.Remove(filepath.Join(finalDirectory, ".install-owner"))
	if err = saveInstalledUpdate(root, installedUpdate{manifest.Version, target}); err != nil {
		return "", err
	}
	return target, nil
}

// The cache must never forward an executable back to itself, even if a release
// was accidentally built with an older version string.
func runningFromUpdate(target, executable, platform string) bool {
	if target == "" || executable == "" {
		return false
	}
	if platform == "windows" {
		return strings.EqualFold(filepath.Clean(target), filepath.Clean(executable))
	}
	relative, err := filepath.Rel(target, executable)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
