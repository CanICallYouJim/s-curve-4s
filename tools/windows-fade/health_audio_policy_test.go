package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAudioOnlyCachedMetadata(t *testing.T) {
	detail := "[mp3 @ 0x123] [error] Incorrect BOM value\n[error] Error reading comment frame, skipped"
	original := healthResult{Issues: []healthIssue{{Kind: "decoder_warning", Severity: "warning", Detail: detail}}}
	normalized := audioOnlyResult(original)
	if normalized.Issues[0].Severity != "info" || original.Issues[0].Severity != "warning" || normalized.Issues[0].Detail != detail {
		t.Fatalf("not a pure metadata normalization: %+v %+v", original, normalized)
	}
	for _, unsafe := range []string{detail + "\n[error] Header missing", "[error] Invalid metadata audio frame", detail + "…", strings.Repeat("Incorrect BOM value\n", 32)} {
		if onlyTextMetadata(unsafe) {
			t.Fatalf("unsafe whitelist match: %q", unsafe)
		}
	}
	var sink diagnosticSink
	sink.line("[error] Incorrect BOM value")
	sink.line("[error] Header missing")
	if sink.Metadata != 1 || sink.Errors != 1 || sink.Warnings != 0 {
		t.Fatalf("mixed faults lost: %+v", sink)
	}
}

func TestAudioPolicyEOFNeedsEvidence(t *testing.T) {
	start, end := 6.0, 6.02
	result := healthResult{Duration: 6, Issues: []healthIssue{{Kind: "packet_gap", Severity: "warning", Start: &start, End: &end}}}
	if !needsAudioPolicyReview(result) || audioOnlyResult(result).Issues[0].Severity != "warning" {
		t.Fatal("EOF time changed verdict without evidence")
	}
	audit := packetAudit{Duration: 6, Packets: []auditPacket{{Key: "0/100", Start: 6, End: 6.02}}, Frames: map[string]bool{}}
	if issues := missingIntervals(audit, false); len(issues) != 1 || issues[0].Severity != "warning" {
		t.Fatalf("untagged EOF hidden: %+v", issues)
	}
	audit.AudioEnd = 100
	if issues := missingIntervals(audit, false); issues[0].Severity != "info" {
		t.Fatalf("known tag packet not excluded: %+v", issues)
	}
	if issues := missingIntervals(audit, true); issues[0].Severity != "error" {
		t.Fatalf("unproven decoder error hidden: %+v", issues)
	}
	audit.Packets[0].Corrupt = true
	if issues := missingIntervals(audit, false); issues[0].Severity != "error" {
		t.Fatalf("corrupt packet hidden: %+v", issues)
	}
	start = 5
	if needsAudioPolicyReview(result) {
		t.Fatal("inner fault selected as EOF")
	}
}

func TestAudioPolicyFullyDiscardedPadding(t *testing.T) {
	raw := `{"packets_and_frames":[{"type":"packet","stream_index":0,"pos":"100","pts_time":"6","duration_time":"0.026122","flags":"K__","side_data_list":[{"side_data_type":"Skip Samples","discard_padding":1152}]}]}`
	audit, err := readPacketAudit(strings.NewReader(raw), map[int]float64{0: 44100})
	if err != nil || len(audit.Packets) != 0 {
		t.Fatalf("fully discarded padding treated as lost audio: %+v %v", audit, err)
	}
	raw = strings.Replace(raw, "1152", "100", 1)
	audit, err = readPacketAudit(strings.NewReader(raw), map[int]float64{0: 44100})
	if err != nil || len(audit.Packets) != 1 {
		t.Fatalf("partially discarded audio lost: %+v %v", audit, err)
	}
}

func TestAudioPolicyTrailingMetadataProof(t *testing.T) {
	engines := testTools(t)
	path := filepath.Join(t.TempDir(), "tail.mp3")
	fixture(t, engines.ffmpeg, path, "-c:a", "libmp3lame", "-b:a", "320k")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	suffix := append([]byte("LYRICSBEGININD0000210LYR00000000029LYRICS200TAG"), make([]byte, 125)...)
	data = append(data, suffix...)
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := engines.analyzeHealth(context.Background(), path)
	if err != nil || !result.Complete {
		t.Fatalf("tail proof failed: %+v %v", result, err)
	}
	for _, issue := range result.Issues {
		if issue.Severity != "info" {
			t.Fatalf("non-audio suffix warned: %+v", issue)
		}
	}
	if len(result.Issues) == 0 {
		t.Fatal("original diagnostics not retained")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(data, after) {
		t.Fatal("modified source")
	}
	// A proven-safe suffix must not cover a genuinely truncated audio stream.
	truncated := append(append([]byte(nil), data[:len(data)/2]...), suffix...)
	if err = os.WriteFile(path, truncated, 0600); err != nil {
		t.Fatal(err)
	}
	result, err = engines.analyzeHealth(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	warned := false
	for _, issue := range result.Issues {
		if issue.Severity != "info" {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("truncated audio hidden by tags: %+v", result)
	}
}

func TestAudioPolicyMetadataStillAnalyzesSound(t *testing.T) {
	engines := testTools(t)
	path := filepath.Join(t.TempDir(), "comment.mp3")
	fixture(t, engines.ffmpeg, path, "-c:a", "libmp3lame", "-b:a", "320k")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A separate valid ID3v2.3 comment frame with UTF-16 encoding but no BOM.
	_, suffix, err := mp3Tags(data)
	if err != nil {
		t.Fatal(err)
	}
	prefix, _, _ := mp3Tags(data)
	frame := append([]byte("COMM\x00\x00\x00\x08\x00\x00"), []byte{1, 'e', 'n', 'g', 0, 0, 'x', 0}...)
	header := []byte{'I', 'D', '3', 3, 0, 0, 0, 0, 0, byte(len(frame))}
	data = append(append(header, frame...), data[len(prefix):len(data)-len(suffix)]...)
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := engines.analyzeHealth(context.Background(), path)
	if err != nil || !result.Complete {
		t.Fatalf("metadata prevented analysis: %+v %v", result, err)
	}
	metadata := false
	for _, issue := range result.Issues {
		if issue.Severity != "info" {
			t.Fatalf("healthy sound warned: %+v", issue)
		}
		metadata = metadata || strings.Contains(issue.Detail, "BOM")
	}
	if !metadata {
		t.Fatal("fixture did not exercise metadata errors")
	}
}

func TestAudioPolicyProvidedTrailingMetadata(t *testing.T) {
	engines := testTools(t)
	root := filepath.Join("..", "..", "out", "Музыка для флешки — S-кривая 4 с")
	names := []string{"CLUB NIGHT F VOCAL SEX SLOW My Emmie - КАЙ.mp3", "ESTRADA KABARE ACAPELLA ROMANTIK SLOW Tony Effe - DAMME 'NA MANO.mp3"}
	for _, name := range names {
		path := filepath.Join(root, "МУЗЫКА НОВАЯ БЕЗ ОТБОРА 2026 06 ИЮНЬ", name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Skip("user recordings unavailable")
		}
		result, err := engines.analyzeHealth(context.Background(), path)
		if err != nil || !result.Complete {
			t.Fatalf("%s: %+v %v", name, result, err)
		}
		for _, issue := range result.Issues {
			if issue.Severity != "info" {
				t.Fatalf("%s: %+v", name, issue)
			}
		}
	}
}

func TestAudioPolicyMetadataDoesNotHideBrokenAudio(t *testing.T) {
	engines := testTools(t)
	matches, _ := filepath.Glob(filepath.Join("..", "..", "Broken_*"))
	if len(matches) == 0 {
		t.Skip("user damaged recording unavailable")
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	prefix, _, err := mp3Tags(data)
	if err != nil {
		t.Fatal(err)
	}
	frame := append([]byte("COMM\x00\x00\x00\x08\x00\x00"), []byte{1, 'e', 'n', 'g', 0, 0, 'x', 0}...)
	header := []byte{'I', 'D', '3', 3, 0, 0, 0, 0, 0, byte(len(frame))}
	data = append(append(header, frame...), data[len(prefix):]...)
	path := filepath.Join(t.TempDir(), "mixed.mp3")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := engines.analyzeHealth(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	errors := 0
	metadata := false
	for _, issue := range result.Issues {
		if issue.Kind == "decode_error" && issue.Severity == "error" {
			errors++
		}
		metadata = metadata || strings.Contains(issue.Detail, "BOM")
	}
	if errors != 7 || !metadata {
		t.Fatalf("mixed file lost faults or metadata: %+v", result)
	}
}

func TestAudioPolicyLimitsAreTechnical(t *testing.T) {
	result := healthResult{Issues: []healthIssue{
		{Kind: "timeline_limit", Severity: "warning"}, {Kind: "signal_limit", Severity: "warning"},
		{Kind: "silence_gap", Severity: "warning"}, {Kind: "duration_mismatch", Severity: "warning"},
		{Kind: "decode_error", Severity: "error"},
	}}
	normalized := audioOnlyResult(result)
	for i, issue := range normalized.Issues {
		expected := result.Issues[i].Severity
		if i < 2 {
			expected = "info"
		}
		if issue.Severity != expected {
			t.Fatalf("unexpected normalization: %+v", normalized)
		}
	}
}

func TestAudioPolicyTextFrameFailuresStayTechnical(t *testing.T) {
	for _, line := range []string{"[warning] Unknown encoding 9", "[mp3] [warning] Unknown encoding 9", "[mp3] [error] Error reading frame TIT2, skipped", "[error] Cannot read BOM value, input too short"} {
		r := healthyResult()
		r.Issues = []healthIssue{{Kind: "decoder_warning", Severity: "warning", Detail: line}}
		if healthStatus(audioOnlyResult(r)) != "clean" {
			t.Fatal(line)
		}
	}
	if textMetadataMessage("[aacfloat] [error] Unknown encoding 9") {
		t.Fatal("unknown audio encoding treated as text")
	}
	r := healthyResult()
	r.Issues = []healthIssue{{Kind: "decode_error", Severity: "error", Detail: "unlocated error"}, {Kind: "decoder_log", Severity: "info", Detail: "[mp3] [error] Error reading frame TIT2, skipped"}}
	if !needsAudioPolicyReview(r) {
		t.Fatal("old metadata error without completed signal analysis was reused")
	}
}
