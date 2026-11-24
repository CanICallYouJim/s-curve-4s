package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestHealthAudioPolicyMigratesOnlyUnresolvedEndings(t *testing.T) {
	root := t.TempDir()
	if err := plainCheckDir(filepath.Join(root, checkFolder)); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, checkFolder, checkCacheFolder)
	meta := healthyResult()
	meta.Issues = []healthIssue{{Kind: "decoder_warning", Severity: "warning", Detail: "[error] Incorrect BOM value\n[error] Error reading lyrics, skipped"}}
	eof := healthyResult()
	at, end := 6.0, 6.026
	eof.Issues = []healthIssue{{Kind: "packet_gap", Severity: "warning", Start: &at, End: &end}}
	for name, result := range map[string]healthResult{"metadata.mp3": meta, "ending.mp3": eof} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		fingerprint, err := fileFingerprint(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		if err = appendCheckCache(cache, cacheEntry{Profile: "old", SHA256: fingerprint.Digest, Result: result}); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	analyze := func(_ context.Context, path string) (healthResult, error) {
		calls++
		if filepath.Base(path) != "ending.mp3" {
			t.Fatal("unrelated unchanged audio was reanalyzed")
		}
		return healthyResult(), nil
	}
	first, err := scanHealth(context.Background(), root, false, "old", analyze, io.Discard)
	if err != nil || first.Fresh != 1 || first.Cached != 1 || calls != 1 {
		t.Fatalf("migration %+v calls%d err%v", healthStats(first), calls, err)
	}
	if healthStats(first).Warnings != 0 {
		t.Fatal("metadata caused an audio warning")
	}
	second, err := scanHealth(context.Background(), root, false, "old", analyze, io.Discard)
	if err != nil || second.Fresh != 0 || second.Cached != 2 || calls != 1 {
		t.Fatalf("migration repeated %+v calls%d err%v", healthStats(second), calls, err)
	}
	if meta.Issues[0].Severity != "warning" {
		t.Fatal("normalization mutated original evidence")
	}
}

func TestHealthAudioPolicyKeepsRealEOFWarningCached(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "ending.mp3")
	os.WriteFile(path, []byte("audio"), 0600)
	calls := 0
	analyze := func(context.Context, string) (healthResult, error) {
		calls++
		r := healthyResult()
		at := r.Duration
		r.Issues = []healthIssue{{Kind: "packet_gap", Severity: "warning", Start: &at}}
		return r, nil
	}
	for run := 0; run < 2; run++ {
		report, err := scanHealth(context.Background(), root, false, "test", analyze, io.Discard)
		if err != nil || healthStats(report).Warnings != 1 {
			t.Fatalf("%v %+v", err, report)
		}
		if run == 1 && (calls != 1 || report.Cached != 1 || report.Fresh != 0) {
			t.Fatal("unchanged real EOF warning was repeatedly analyzed")
		}
	}
}
