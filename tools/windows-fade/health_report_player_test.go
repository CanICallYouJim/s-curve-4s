package main

import (
	"bytes"
	"context"
	"encoding/base64"
	stdhtml "html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestHealthReportPlayerScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is needed for the JavaScript unit test")
	}
	output, err := exec.Command(node, "health_player_test.js").CombinedOutput()
	if err != nil {
		t.Fatalf("player behavior: %v\n%s", err, output)
	}
}

func TestHealthReportEmbedsPlayablePreviewOnce(t *testing.T) {
	engines := testTools(t)
	root := t.TempDir()
	source := filepath.Join(root, "Кириллица & тихий трек.mp3")
	fixture(t, engines.ffmpeg, source, "-c:a", "libmp3lame", "-b:a", "256k")
	original, err := fileFingerprint(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	at, end := 4.5, 4.6
	track := healthTrack{Path: filepath.Base(source), SHA256: original.Digest, Result: healthyResult()}
	track.Result.Issues = []healthIssue{{Kind: "packet_gap", Severity: "warning", Start: &at, End: &end, Detail: "listen"}}
	report := healthReport{Root: root, Fresh: 1, Tracks: []healthTrack{track}}
	output, err := saveHealthReportWithPreviews(context.Background(), report, engines)
	if err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	text := string(html)
	match := regexp.MustCompile(`<span id="preview-0" hidden>([^<]+)</span>`).FindStringSubmatch(text)
	if len(match) != 2 {
		t.Fatal("standalone audio payload missing")
	}
	media, err := base64.StdEncoding.DecodeString(stdhtml.UnescapeString(match[1]))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(html, []byte(match[1])) != 1 {
		t.Fatal("same payload embedded multiple times")
	}
	if !strings.Contains(text, "<script>"+healthPlayerJS+"</script>") {
		t.Fatal("embedded player script was escaped or changed")
	}
	if strings.Count(text, `data-preview-id="preview-0"`) != 2 {
		t.Fatal("timestamp and track excerpt not wired to same media")
	}
	if strings.Contains(text, `href="../`) || strings.Contains(text, `data-audio="`) {
		t.Fatal("report still navigates to denied file URL")
	}
	if !strings.Contains(text, `id="musicFiles"`) || !strings.Contains(text, "URL.createObjectURL") {
		t.Fatal("chosen full audio source not supported")
	}
	excerpt := filepath.Join(t.TempDir(), "preview.mp3")
	os.WriteFile(excerpt, media, 0600)
	if out, err := exec.Command(engines.ffmpeg, "-v", "error", "-xerror", "-i", excerpt, "-f", "null", "-").CombinedOutput(); err != nil {
		t.Fatalf("invalid embedded MP3: %v %s", err, out)
	}
	after, _ := fileFingerprint(context.Background(), source)
	if original.Digest != after.Digest || !os.SameFile(original.Info, after.Info) {
		t.Fatal("report creation changed source")
	}
	if stats := healthStats(report); stats.Checked != 1 || stats.Warnings != 1 {
		t.Fatal("report changed diagnosis")
	}
}

func TestHealthReportShowsPreviewFailure(t *testing.T) {
	engines := testTools(t)
	root := t.TempDir()
	start, end := 2.0, 2.2
	result := healthyResult()
	result.Issues = []healthIssue{{Kind: "packet_gap", Severity: "warning", Start: &start, End: &end}}
	report := healthReport{Root: root, Fresh: 1, Tracks: []healthTrack{{Path: "missing.mp3", SHA256: strings.Repeat("a", 64), Result: result}}}
	path, err := saveHealthReportWithPreviews(context.Background(), report, engines)
	if err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "Встроенный фрагмент недоступен") {
		t.Fatal("preview failure was silently hidden")
	}
	if healthStats(report).Warnings != 1 {
		t.Fatal("preview failure changed diagnosis")
	}
}

func TestHealthReportExplainsFindingsAndKeepsClosedTechnicalDetails(t *testing.T) {
	root := t.TempDir()
	at := 76.33
	report := healthReport{Root: root, Finished: "2026-11-22T02:45:00+03:00", Fresh: 2, Tracks: []healthTrack{
		{Path: "folder/broken.mp3", SHA256: "raw-digest", Result: healthResult{Complete: true, Duration: 180, Issues: []healthIssue{
			{Kind: "decode_error", Severity: "error", Start: &at, Detail: "header <missing>\ntraceback: raw & unchanged"},
			{Kind: "decoder_log", Severity: "info", Detail: "specialist-only-log"},
			{Kind: "decode_error", Severity: "error", Detail: "error without timestamp"},
		}}},
		{Path: "metadata.mp3", Result: healthResult{Complete: true, Issues: []healthIssue{{Kind: "decoder_warning", Severity: "warning", Detail: "[error] Incorrect BOM value\n[error] Error reading comment frame, skipped"}}}},
		{Path: "pending.mp3", Result: healthResult{Issues: []healthIssue{{Kind: "not_checked", Severity: "warning", Detail: "operation cancelled"}}}},
	}}
	path, err := saveHealthReport(report)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"При чтении звука возникла ошибка", "Точное место определить не удалось. Прослушайте запись целиком.", "22.11.2026 в 02:45", "Программа не закончила проверку этой записи", "<summary>broken.mp3<", "Требует внимания", "Стоит прослушать", "Без найденных проблем"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing plain report text: %s", want)
		}
	}
	if !strings.Contains(text, `data-status="clean" data-path="metadata.mp3"`) {
		t.Fatal("text-field errors were presented as sound problems")
	}
	blocks := regexp.MustCompile(`(?s)<details class="technical"><summary>Подробности для специалиста</summary>.*?</details>`).FindAllString(text, -1)
	if len(blocks) != 3 {
		t.Fatalf("want 3 closed technical disclosures, got %d", len(blocks))
	}
	raw := "header &lt;missing&gt;\ntraceback: raw &amp; unchanged"
	if !strings.Contains(blocks[0], raw) || !strings.Contains(blocks[0], "raw-digest") || !strings.Contains(blocks[0], "specialist-only-log") {
		t.Fatal("raw diagnosis or digest missing from closed technical details")
	}
	mainText := regexp.MustCompile(`(?s)<details class="technical">.*?</details>`).ReplaceAllString(text, "")
	if strings.Contains(mainText, root) {
		t.Fatal("full root path leaked outside technical details")
	}
	for _, hidden := range []string{raw, "raw-digest", "specialist-only-log", "Incorrect BOM value", "operation cancelled"} {
		if strings.Contains(mainText, hidden) {
			t.Fatalf("raw technical data leaked into main report: %s", hidden)
		}
	}
	if strings.Contains(text, `<details class="technical" open`) {
		t.Fatal("technical details must start closed")
	}
	stats := healthStats(report)
	if stats.Errors != 1 || stats.Warnings != 0 || stats.Clean != 1 || stats.Incomplete != 1 || stats.Checked != 2 {
		t.Fatalf("diagnostic counts changed: %+v", stats)
	}
}
