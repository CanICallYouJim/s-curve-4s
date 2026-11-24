package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func healthyResult() healthResult {
	return healthResult{Complete: true, CheckedAt: time.Now().Format(time.RFC3339), Duration: 6}
}

func TestHealthIncrementalUsesAllBytes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "тест.MP3")
	original := []byte("ID3 original metadata and audio")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	calls := 0
	analyze := func(context.Context, string) (healthResult, error) { calls++; return healthyResult(), nil }
	scan := func(profile string) healthReport {
		t.Helper()
		report, err := scanHealth(context.Background(), root, true, profile, analyze, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		return report
	}
	first := scan("v1")
	if calls != 1 || first.Fresh != 1 || first.Cached != 0 {
		t.Fatalf("initial: %d %+v", calls, first)
	}
	second := scan("v1")
	if calls != 1 || second.Fresh != 0 || second.Cached != 1 {
		t.Fatal("unchanged input was reanalyzed")
	}
	// Same pathname, same length, same modification time; only a tag byte changes.
	modified := append([]byte{}, original...)
	modified[5] ^= 1
	os.WriteFile(path, modified, 0600)
	os.Chtimes(path, info.ModTime(), info.ModTime())
	third := scan("v1")
	if calls != 2 || third.Fresh != 1 || third.Cached != 0 {
		t.Fatal("same-size, same-time metadata replacement was missed")
	}
	os.Rename(path, filepath.Join(root, "new name.mp3"))
	if renamed := scan("v1"); calls != 2 || renamed.Cached != 1 {
		t.Fatal("byte-identical rename lost its cached diagnosis")
	}
	if versioned := scan("v2"); calls != 3 || versioned.Fresh != 1 {
		t.Fatal("analysis profile was ignored")
	}
}

func TestHealthChangedDuringAnalysisIsNotCached(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "track.mp3")
	os.WriteFile(path, []byte("same length A"), 0600)
	analyze := func(context.Context, string) (healthResult, error) {
		os.WriteFile(path, []byte("same length B"), 0600)
		return healthyResult(), nil
	}
	report, err := scanHealth(context.Background(), root, false, "v1", analyze, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if report.Tracks[0].Result.Complete || report.Tracks[0].Result.Issues[0].Kind != "file_changed" {
		t.Fatal(report)
	}
	cached, _, _ := loadCheckCache(filepath.Join(root, checkFolder, checkCacheFolder), "v1")
	if len(cached) != 0 {
		t.Fatal("inconsistent diagnosis cached")
	}
}

func TestHealthCacheSurvivesPartialRecordAndKeepsBadVerdict(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "bad.mp3")
	os.WriteFile(source, []byte("invalid mp3"), 0600)
	count := 0
	analyze := func(context.Context, string) (healthResult, error) {
		count++
		result := healthyResult()
		result.Issues = []healthIssue{{Kind: "unreadable", Severity: "error", Detail: "cannot decode"}}
		return result, nil
	}
	first, err := scanHealth(context.Background(), root, false, "v1", analyze, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(root, checkFolder, checkCacheFolder)
	os.WriteFile(filepath.Join(cachePath, "interrupted.json"), []byte(`{"profile":"v1","sha256":"unfinished`), 0600)
	second, err := scanHealth(context.Background(), root, false, "v1", analyze, io.Discard)
	if err != nil || count != 1 || second.Cached != 1 || healthStats(second).Errors != 1 || len(second.Notices) == 0 {
		t.Fatalf("recovery: %d %+v %v", count, second, err)
	}
	entry := cacheEntry{Profile: "v1", SHA256: first.Tracks[0].SHA256, Result: first.Tracks[0].Result}
	if err = appendCheckCache(cachePath, entry); err != nil {
		t.Fatal(err)
	}
	entries, _, err := loadCheckCache(cachePath, "v1")
	if err != nil || len(entries) != 1 {
		t.Fatalf("append after interrupted line: %v %+v", err, entries)
	}
}

func TestHealthScopeAndCancellation(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "nested"), 0700)
	os.Mkdir(filepath.Join(root, checkFolder), 0700)
	for _, name := range []string{"root.MP3", "nested/sub.flac", checkFolder + "/ignore.mp3"} {
		os.WriteFile(filepath.Join(root, name), []byte(name), 0600)
	}
	flat, _, err := musicPaths(root, false)
	if err != nil || len(flat) != 1 {
		t.Fatalf("flat %v %v", flat, err)
	}
	recursive, _, err := musicPaths(root, true)
	if err != nil || len(recursive) != 2 {
		t.Fatalf("recursive %v %v", recursive, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	analyze := func(context.Context, string) (healthResult, error) { cancel(); return healthyResult(), nil }
	report, err := scanHealth(ctx, root, true, "v1", analyze, io.Discard)
	if err != nil || !report.Interrupted || report.Fresh != 0 || healthStats(report).Incomplete != 2 {
		t.Fatalf("canceled: %+v %v", report, err)
	}
}

func TestHealthReportCountsAndEscapesPaths(t *testing.T) {
	root := t.TempDir()
	at, end := 76.33, 76.75
	bad := healthyResult()
	bad.Issues = []healthIssue{{Kind: "decode_error", Severity: "error", Start: &at, End: &end, Detail: "header <missing>"}, {Kind: "decode_error", Severity: "error", Detail: "another frame"}}
	report := healthReport{Root: root, Started: "now", Finished: "now", Fresh: 1, Tracks: []healthTrack{{Path: `name"<script>&.mp3`, Result: bad}, {Path: "pending.flac", Result: incompleteIssue("not_checked", "pending")}}}
	stats := healthStats(report)
	if stats.Checked != 1 || stats.Errors != 1 || stats.Incomplete != 1 {
		t.Fatal(stats)
	}
	for _, kind := range stats.Kinds {
		if kind.Kind == "decode_error" && kind.Count != 1 {
			t.Fatal("counted events instead of files")
		}
	}
	output, err := saveHealthReport(report)
	if err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), `name"<script>`) || !strings.Contains(string(html), "1:16.33") || !strings.Contains(string(html), "header &lt;missing&gt;") {
		t.Fatal("report escaping or timestamps")
	}
}

func TestHealthHealthyFormats(t *testing.T) {
	engines := testTools(t)
	for _, format := range []struct {
		name    string
		options []string
	}{
		{"healthy.mp3", []string{"-c:a", "libmp3lame", "-b:a", "128k"}},
		{"healthy.m4a", []string{"-c:a", "aac", "-b:a", "128k"}},
		{"healthy.flac", []string{"-c:a", "flac"}},
		{"healthy.wav", []string{"-c:a", "pcm_s16le"}},
		{"healthy.opus", []string{"-c:a", "libopus", "-b:a", "128k"}},
		{"healthy.ogg", []string{"-c:a", "libvorbis", "-b:a", "128k"}},
	} {
		t.Run(format.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), format.name)
			fixture(t, engines.ffmpeg, path, format.options...)
			result, err := engines.analyzeHealth(context.Background(), path)
			if err != nil || !result.Complete || len(result.Issues) != 0 {
				t.Fatalf("healthy audio flagged: %v %+v", err, result)
			}
		})
	}
}

func TestHealthZeroFileAndMissingEngine(t *testing.T) {
	engines := testTools(t)
	path := filepath.Join(t.TempDir(), "zero.mp3")
	os.WriteFile(path, make([]byte, 4096), 0600)
	result, err := engines.analyzeHealth(context.Background(), path)
	if err != nil || !result.Complete || healthStatus(result) != "error" {
		t.Fatalf("zero file: %+v %v", result, err)
	}
	result, err = (tools{ffmpeg: engines.ffmpeg, ffprobe: "/does-not-exist/ffprobe"}).analyzeHealth(context.Background(), path)
	if err == nil || result.Complete {
		t.Fatal("missing engine was recorded as an audio verdict")
	}
}

func TestHealthProvidedBrokenRecording(t *testing.T) {
	engines := testTools(t)
	matches, _ := filepath.Glob(filepath.Join("..", "..", "Broken_*"))
	if len(matches) == 0 {
		t.Skip("user-provided damaged recording not available")
	}
	result, err := engines.analyzeHealth(context.Background(), matches[0])
	if err != nil || !result.Complete {
		t.Fatalf("broken sample: %+v %v", result, err)
	}
	var intervals []healthIssue
	for _, issue := range result.Issues {
		if issue.Kind == "decode_error" && issue.Start != nil {
			intervals = append(intervals, issue)
		}
	}
	for _, second := range []float64{56.87, 61.75, 76.33, 110.34, 144.27, 163.81, 183.07} {
		found := false
		for _, issue := range intervals {
			if absFloat(*issue.Start-second) < 1 && *issue.End >= second-.1 {
				found = true
			}
		}
		if !found {
			raw, _ := json.MarshalIndent(result, "", " ")
			t.Fatalf("missed interval near %.2f: %s", second, raw)
		}
	}
	if len(intervals) != 7 {
		t.Fatalf("expected all 7 episodes, got %d", len(intervals))
	}
}

func TestHealthCacheNeverWritesThroughAudioLinks(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.mp3")
	original := []byte("original audio bytes")
	os.WriteFile(source, original, 0600)
	cache := filepath.Join(root, "cache")
	sum := sha256.Sum256(original)
	entry := cacheEntry{Profile: "v1", SHA256: hex.EncodeToString(sum[:]), Result: healthyResult()}
	if err := appendCheckCache(cache, entry); err != nil {
		t.Fatal(err)
	}
	files, _ := os.ReadDir(cache)
	target := filepath.Join(cache, files[0].Name())
	for _, kind := range []string{"symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			os.Remove(target)
			var err error
			if kind == "symlink" {
				err = os.Symlink(source, target)
			} else {
				err = os.Link(source, target)
			}
			if err != nil {
				t.Skip(err)
			}
			if err = appendCheckCache(cache, entry); err != nil {
				t.Fatal(err)
			}
			current, _ := os.ReadFile(source)
			if !bytes.Equal(current, original) {
				t.Fatalf("wrote through %s into audio", kind)
			}
		})
	}
	reportRoot := filepath.Join(root, checkFolder)
	if err := os.Symlink(root, reportRoot); err == nil {
		if _, err = scanHealth(context.Background(), root, false, "v1", func(context.Context, string) (healthResult, error) { return healthyResult(), nil }, io.Discard); err == nil {
			t.Fatal("linked report directory accepted")
		}
	}
}

func TestHealthHeaderDetectsTruncatedFLAC(t *testing.T) {
	engines := testTools(t)
	root := t.TempDir()
	source := filepath.Join(root, "full.flac")
	fixture(t, engines.ffmpeg, source, "-c:a", "flac")
	raw, err := exec.Command(engines.ffprobe, "-v", "error", "-select_streams", "a:0", "-show_packets", "-show_entries", "packet=pos,pts_time", "-of", "json", source).Output()
	if err != nil {
		t.Fatal(err)
	}
	var packets struct {
		Packets []struct {
			Position string `json:"pos"`
			Time     string `json:"pts_time"`
		} `json:"packets"`
	}
	if err = json.Unmarshal(raw, &packets); err != nil {
		t.Fatal(err)
	}
	cut := 0
	for _, packet := range packets.Packets {
		pts, _ := strconv.ParseFloat(packet.Time, 64)
		if pts >= 3 {
			cut, _ = strconv.Atoi(packet.Position)
			break
		}
	}
	if cut == 0 {
		t.Fatal("no frame boundary")
	}
	data, _ := os.ReadFile(source)
	broken := filepath.Join(root, "truncated.flac")
	os.WriteFile(broken, data[:cut], 0600)
	result, err := engines.analyzeHealth(context.Background(), broken)
	if err != nil || !result.Complete {
		t.Fatalf("truncated FLAC %+v %v", result, err)
	}
	found := false
	for _, issue := range result.Issues {
		if issue.Kind == "duration_mismatch" {
			found = true
		}
	}
	if !found || healthStatus(result) == "clean" {
		t.Fatalf("truncated header not detected: %+v", result)
	}
}

func TestHealthFiveThousandFilesOnlyTwoReplacementsAreReanalyzed(t *testing.T) {
	if testing.Short() {
		t.Skip("large-library filesystem integration")
	}
	root := t.TempDir()
	const total = 5000
	for i := 0; i < total; i++ {
		path := filepath.Join(root, fmt.Sprintf("track-%04d.mp3", i))
		if err := os.WriteFile(path, []byte(fmt.Sprintf("audio-and-tags-%04d", i)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	analyze := func(context.Context, string) (healthResult, error) { calls++; return healthyResult(), nil }
	first, err := scanHealth(context.Background(), root, false, "scale-test", analyze, io.Discard)
	if err != nil || calls != total || first.Fresh != total {
		t.Fatalf("initial library: calls=%d report=%+v err=%v", calls, healthStats(first), err)
	}
	for _, i := range []int{47, 4201} {
		path := filepath.Join(root, fmt.Sprintf("track-%04d.mp3", i))
		stat, _ := os.Stat(path)
		original, _ := os.ReadFile(path)
		original[0] ^= 1
		os.WriteFile(path, original, 0600)
		os.Chtimes(path, stat.ModTime(), stat.ModTime())
	}
	second, err := scanHealth(context.Background(), root, false, "scale-test", analyze, io.Discard)
	if err != nil || calls != total+2 || second.Fresh != 2 || second.Cached != total-2 {
		t.Fatalf("two replacements: calls=%d report=%+v err=%v", calls, healthStats(second), err)
	}
	if _, err = saveHealthReport(second); err != nil {
		t.Fatal(err)
	}
}
