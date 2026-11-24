package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const checkFolder = "Проверка музыки"
const checkVersion = "music-health-2"
const checkCacheFolder = "cache"
const audioOnlyPolicyVersion = 2

type healthIssue struct {
	Kind     string   `json:"kind"`
	Severity string   `json:"severity"`
	Start    *float64 `json:"start,omitempty"`
	End      *float64 `json:"end,omitempty"`
	Detail   string   `json:"detail"`
}

type healthResult struct {
	AudioPolicy int           `json:"audio_policy,omitempty"`
	Complete    bool          `json:"complete"`
	CheckedAt   string        `json:"checked_at"`
	Duration    float64       `json:"duration"`
	Issues      []healthIssue `json:"issues,omitempty"`
}

type cacheEntry struct {
	Profile string       `json:"profile"`
	SHA256  string       `json:"sha256"`
	Result  healthResult `json:"result"`
}

type healthTrack struct {
	Path   string
	SHA256 string
	Cached bool
	Result healthResult
}

type healthReport struct {
	Root        string
	Recursive   bool
	Started     string
	Finished    string
	Interrupted bool
	Tracks      []healthTrack
	Notices     []string
	Fresh       int
	Cached      int
}

type fingerprint struct {
	Digest string
	Info   fs.FileInfo
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}

func fileFingerprint(ctx context.Context, path string) (fingerprint, error) {
	file, err := os.Open(path)
	if err != nil {
		return fingerprint{}, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return fingerprint{}, err
	}
	if !before.Mode().IsRegular() {
		return fingerprint{}, errors.New("файл не является обычным файлом")
	}
	digest := sha256.New()
	if _, err = io.CopyBuffer(digest, contextReader{ctx, file}, make([]byte, 128*1024)); err != nil {
		return fingerprint{}, err
	}
	after, err := file.Stat()
	if err != nil {
		return fingerprint{}, err
	}
	current, err := os.Stat(path)
	if err != nil {
		return fingerprint{}, err
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || !os.SameFile(before, current) || current.Size() != after.Size() || !current.ModTime().Equal(after.ModTime()) {
		return fingerprint{}, errors.New("файл изменился во время чтения")
	}
	return fingerprint{hex.EncodeToString(digest.Sum(nil)), after}, nil
}

func plainCheckDir(path string) error {
	err := os.Mkdir(path, 0700)
	if err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("папка отчётов или истории является ссылкой либо обычным файлом: " + path)
	}
	return nil
}

func loadCheckCache(path, profile string) (map[string]healthResult, []string, error) {
	result := make(map[string]healthResult)
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return result, nil, nil
	}
	if err != nil {
		return result, nil, err
	}
	invalid := 0
	for _, file := range entries {
		if !strings.HasSuffix(file.Name(), ".json") || !file.Type().IsRegular() {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(path, file.Name()))
		if readErr != nil {
			invalid++
			continue
		}
		var entry cacheEntry
		if json.Unmarshal(data, &entry) != nil || len(entry.SHA256) != 64 || !entry.Result.Complete || entry.Result.CheckedAt == "" {
			invalid++
			continue
		}
		if _, err = hex.DecodeString(entry.SHA256); err != nil {
			invalid++
			continue
		}
		if entry.Profile == profile {
			result[entry.SHA256] = entry.Result
		}
	}
	var notices []string
	if invalid > 0 {
		notices = append(notices, fmt.Sprintf("Неполных или повреждённых записей истории: %d. Отсутствующие результаты проверяются заново.", invalid))
	}
	return result, notices, nil
}

func appendCheckCache(path string, entry cacheEntry) error {
	if err := plainCheckDir(path); err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(path, ".cache-*.tmp")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	version := sha256.Sum256([]byte(entry.Profile))
	name := entry.SHA256 + "-" + hex.EncodeToString(version[:6]) + ".json"
	// Rename replaces a directory entry, never writes into an existing inode.
	// Even an existing hard link to music cannot make this write alter music.
	return os.Rename(temp, filepath.Join(path, name))
}

func musicPaths(root string, recursive bool) ([]string, []string, error) {
	var paths, notices []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			notices = append(notices, fmt.Sprintf("Не удалось прочитать %s: %v", path, walkErr))
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			if path != root && (!recursive || path == filepath.Join(root, checkFolder)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != "" && strings.Contains(" "+extensions+" ", " "+ext+" ") {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			paths = append(paths, relative)
		}
		return nil
	})
	return paths, notices, err
}

func incompleteIssue(kind, detail string) healthResult {
	return healthResult{Issues: []healthIssue{{Kind: kind, Severity: "warning", Detail: detail}}}
}

type healthAnalyzer func(context.Context, string) (healthResult, error)

func scanHealth(ctx context.Context, root string, recursive bool, profile string, analyze healthAnalyzer, log io.Writer) (healthReport, error) {
	report := healthReport{Root: root, Recursive: recursive, Started: time.Now().Format(time.RFC3339)}
	paths, notices, err := musicPaths(root, recursive)
	report.Notices = append(report.Notices, notices...)
	if err != nil {
		return report, err
	}
	if len(paths) == 0 {
		return report, errors.New("в выбранной папке нет аудиофайлов")
	}
	output := filepath.Join(root, checkFolder)
	if err = plainCheckDir(output); err != nil {
		return report, err
	}
	cachePath := filepath.Join(output, checkCacheFolder)
	cache, notices, err := loadCheckCache(cachePath, profile)
	report.Notices = append(report.Notices, notices...)
	if err != nil {
		report.Notices = append(report.Notices, "Не удалось полностью прочитать историю: "+err.Error())
	}
	for _, path := range paths {
		report.Tracks = append(report.Tracks, healthTrack{Path: path, Result: incompleteIssue("not_checked", "Проверка ещё не выполнена")})
	}
	cacheWritable := true
	for i := range report.Tracks {
		track := &report.Tracks[i]
		if ctx.Err() != nil {
			report.Interrupted = true
			break
		}
		fmt.Fprintf(log, "[%d/%d] %s\n", i+1, len(report.Tracks), track.Path)
		path := filepath.Join(root, track.Path)
		before, hashErr := fileFingerprint(ctx, path)
		if hashErr != nil {
			track.Result = incompleteIssue("read_error", "Не удалось прочитать все байты файла: "+hashErr.Error())
			if ctx.Err() != nil {
				report.Interrupted = true
				break
			}
			continue
		}
		track.SHA256 = before.Digest
		if saved, ok := cache[before.Digest]; ok {
			saved = audioOnlyResult(saved)
			if !needsAudioPolicyReview(saved) {
				track.Cached = true
				track.Result = saved
				report.Cached++
				fmt.Fprintln(log, "  Без изменений — результат из истории")
				continue
			}
		}
		result, analysisErr := analyze(ctx, path)
		if ctx.Err() != nil {
			report.Interrupted = true
			break
		}
		if analysisErr != nil {
			track.Result = incompleteIssue("analysis_failed", "Проверка не завершена: "+analysisErr.Error())
			continue
		}
		after, hashErr := fileFingerprint(ctx, path)
		if hashErr != nil || after.Digest != before.Digest || !os.SameFile(before.Info, after.Info) {
			track.Result = incompleteIssue("file_changed", "Файл заменён или изменён во время проверки. Нужен повторный запуск.")
			continue
		}
		if result.Complete {
			result.AudioPolicy = audioOnlyPolicyVersion
		}
		track.Result = result
		if !result.Complete {
			continue
		}
		report.Fresh++
		cache[before.Digest] = result
		if cacheWritable {
			if err = appendCheckCache(cachePath, cacheEntry{Profile: profile, SHA256: before.Digest, Result: result}); err != nil {
				report.Notices = append(report.Notices, "Не удалось сохранить историю проверок: "+err.Error()+". При следующем запуске часть файлов будет проверена повторно.")
				cacheWritable = false
			}
		}
	}
	report.Finished = time.Now().Format(time.RFC3339)
	return report, nil
}
