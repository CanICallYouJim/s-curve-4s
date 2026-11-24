package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func previewFixture(t *testing.T) (tools, string, healthTrack) {
	t.Helper()
	engines := testTools(t)
	root := t.TempDir()
	path := filepath.Join(root, "трек.wav")
	// A continuously varying frequency distinguishes the requested range from
	// an accidental excerpt taken at the beginning of the recording.
	cmd := exec.Command(engines.ffmpeg, "-v", "error", "-f", "lavfi", "-i", "aevalsrc=0.25*sin(2*PI*(200*t+20*t*t)):s=44100:d=20", "-metadata", "title=private-source-title", "-c:a", "pcm_s16le", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, output)
	}
	fp, err := fileFingerprint(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return engines, root, healthTrack{Path: filepath.Base(path), SHA256: fp.Digest, Result: healthResult{Duration: 20}}
}

func TestHealthPreviewExcerptAndCache(t *testing.T) {
	engines, root, track := previewFixture(t)
	before, _ := os.ReadFile(filepath.Join(root, track.Path))
	preview, err := engines.healthPreview(context.Background(), root, track, 12)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Start != 7 || preview.End != 16 {
		t.Fatalf("wrong range: %+v", preview)
	}
	media, err := base64.StdEncoding.DecodeString(preview.Data)
	if err != nil || len(media) == 0 {
		t.Fatalf("invalid embedded MP3: %v", err)
	}
	clip := filepath.Join(t.TempDir(), "preview.mp3")
	if err = os.WriteFile(clip, media, 0600); err != nil {
		t.Fatal(err)
	}
	probe, err := exec.Command(engines.ffprobe, "-v", "error", "-show_entries", "format_tags:format=duration", "-of", "json", clip).Output()
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Format struct {
			Duration string            `json:"duration"`
			Tags     map[string]string `json:"tags"`
		} `json:"format"`
	}
	if err = json.Unmarshal(probe, &metadata); err != nil {
		t.Fatal(err)
	}
	duration, _ := strconv.ParseFloat(metadata.Format.Duration, 64)
	if math.Abs(duration-9) > .1 || metadata.Format.Tags["title"] != "" {
		t.Fatalf("duration/tags: %+v", metadata.Format)
	}
	x := samples(t, engines.ffmpeg, filepath.Join(root, track.Path))
	y := samples(t, engines.ffmpeg, clip)
	var xx, yy, xy float64
	for i := 4410; i < 2*44100; i++ {
		a, b := float64(x[7*44100+i]), float64(y[i])
		xx += a * a
		yy += b * b
		xy += a * b
	}
	if xy/math.Sqrt(xx*yy) < .99 {
		t.Fatalf("excerpt does not align with source at 7 s: correlation %.5f", xy/math.Sqrt(xx*yy))
	}
	after, _ := os.ReadFile(filepath.Join(root, track.Path))
	if !bytes.Equal(before, after) {
		t.Fatal("source audio changed")
	}
	cached, err := (tools{ffmpeg: "/missing/ffmpeg", ffprobe: "/missing/ffprobe"}).healthPreview(context.Background(), root, track, 12)
	if err != nil || cached != preview {
		t.Fatalf("cache invoked missing tools or changed media: %v", err)
	}
}

func TestHealthPreviewUpdatedSourceAndBoundaries(t *testing.T) {
	engines, root, track := previewFixture(t)
	atEnd, err := engines.healthPreview(context.Background(), root, track, 20.03)
	if err != nil || atEnd.Start != 15 || atEnd.End != 20 {
		t.Fatalf("EOF clamp: %+v %v", atEnd, err)
	}
	initial, err := engines.healthPreview(context.Background(), root, track, 0)
	if err != nil || initial.Start != 0 || initial.End != 8 {
		t.Fatalf("default excerpt: %+v %v", initial, err)
	}
	path := filepath.Join(root, track.Path)
	data, _ := os.ReadFile(path)
	data[len(data)-8] ^= 1
	os.WriteFile(path, data, 0600)
	if _, err = engines.healthPreview(context.Background(), root, track, 10); err == nil {
		t.Fatal("generated preview using a stale source fingerprint")
	}
	fp, err := fileFingerprint(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	track.SHA256 = fp.Digest
	if _, err = engines.healthPreview(context.Background(), root, track, 0); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(root, checkFolder, "previews", "*.json"))
	if len(files) != 3 {
		t.Fatalf("changed source reused old media cache: %d", len(files))
	}
}

func TestHealthPreviewRejectsLinkedCacheDirectory(t *testing.T) {
	engines, root, track := previewFixture(t)
	if err := plainCheckDir(filepath.Join(root, checkFolder)); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, track.Path)
	before, _ := os.ReadFile(source)
	if err := os.Symlink(root, filepath.Join(root, checkFolder, "previews")); err != nil {
		t.Skip(err)
	}
	if _, err := engines.healthPreview(context.Background(), root, track, 12); err == nil {
		t.Fatal("symlink cache directory accepted")
	}
	after, _ := os.ReadFile(source)
	if !bytes.Equal(before, after) {
		t.Fatal("audio changed through linked cache")
	}
}

func TestHealthPreviewReplacesCacheLinkWithoutWritingAudio(t *testing.T) {
	engines, root, track := previewFixture(t)
	if _, err := engines.healthPreview(context.Background(), root, track, 12); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(root, checkFolder, "previews", "*.json"))
	if len(files) != 1 {
		t.Fatal("missing initial cache")
	}
	source := filepath.Join(root, track.Path)
	before, _ := os.ReadFile(source)
	for _, kind := range []string{"symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			if err := os.Remove(files[0]); err != nil {
				t.Fatal(err)
			}
			var err error
			if kind == "symlink" {
				err = os.Symlink(source, files[0])
			} else {
				err = os.Link(source, files[0])
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = engines.healthPreview(context.Background(), root, track, 12); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(source)
			if !bytes.Equal(before, after) {
				t.Fatalf("wrote through %s cache entry", kind)
			}
		})
	}
}

func TestHealthPreviewToleratesBrokenTrailingMetadata(t *testing.T) {
	engines := testTools(t)
	root := t.TempDir()
	path := filepath.Join(root, "tail.mp3")
	fixture(t, engines.ffmpeg, path, "-c:a", "libmp3lame", "-b:a", "320k")
	data, _ := os.ReadFile(path)
	suffix := append([]byte("LYRICSBEGININD0000210LYR00000000029LYRICS200TAG"), make([]byte, 125)...)
	data = append(data, suffix...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(engines.ffmpeg, "-v", "error", "-xerror", "-i", path, "-f", "null", "-").CombinedOutput(); err == nil || !bytes.Contains(output, []byte("Header missing")) {
		t.Fatalf("fixture did not reproduce the trailing-metadata error: %v %s", err, output)
	}
	fp, err := fileFingerprint(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	track := healthTrack{Path: filepath.Base(path), SHA256: fp.Digest, Result: healthResult{Duration: 6}}
	preview, err := engines.healthPreview(context.Background(), root, track, 6.03)
	if err != nil || preview.Start != 1 || preview.End != 6 {
		t.Fatalf("trailing metadata preview: %+v %v", preview, err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, data) {
		t.Fatal("source with trailing tags was altered")
	}
}

func TestHealthPreviewAllProvidedBrokenIntervals(t *testing.T) {
	engines := testTools(t)
	matches, _ := filepath.Glob(filepath.Join("..", "..", "Broken_*"))
	if len(matches) == 0 {
		t.Skip("user-provided damaged recording not available")
	}
	original, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "Broken.mp3")
	if err = os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	fp, err := fileFingerprint(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := engines.analyzeHealth(context.Background(), path)
	if err != nil || healthStatus(verdict) != "error" {
		t.Fatalf("fixture did not preserve damaged verdict: %+v %v", verdict, err)
	}
	track := healthTrack{Path: filepath.Base(path), SHA256: fp.Digest, Result: verdict}
	for _, target := range []float64{56.87, 61.75, 76.33, 110.34, 144.27, 163.81, 183.07} {
		t.Run(strconv.FormatFloat(target, 'f', 2, 64), func(t *testing.T) {
			preview, err := engines.healthPreview(context.Background(), root, track, target)
			if err != nil {
				t.Fatal(err)
			}
			media, err := base64.StdEncoding.DecodeString(preview.Data)
			if err != nil {
				t.Fatal(err)
			}
			clip := filepath.Join(t.TempDir(), "preview.mp3")
			if err = os.WriteFile(clip, media, 0600); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command(engines.ffmpeg, "-v", "error", "-xerror", "-i", clip, "-f", "null", "-").CombinedOutput(); err != nil {
				t.Fatalf("unusable preview: %v %s", err, output)
			}
			pcm := samples(t, engines.ffmpeg, clip)
			if difference := math.Abs(float64(len(pcm))/44100 - 9); difference > 1.0/44100 {
				t.Fatalf("preview is not 9 seconds: %.9f", float64(len(pcm))/44100)
			}
			// The original target remains inside the preview, with context on both sides.
			if target-preview.Start != 5 || preview.End-target != 4 {
				t.Fatalf("incorrect seek bounds: %+v", preview)
			}
		})
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, original) {
		t.Fatal("damaged source was altered")
	}
	afterVerdict, err := engines.analyzeHealth(context.Background(), path)
	if err != nil || healthStatus(afterVerdict) != "error" {
		t.Fatalf("preview creation masked original damage: %+v %v", afterVerdict, err)
	}
}

func TestHealthPreviewKeepsMissingAudioAtOriginalTimestamp(t *testing.T) {
	engines, root, original := previewFixture(t)
	path := filepath.Join(root, "timestamp-gap.nut")
	cmd := exec.Command(engines.ffmpeg, "-v", "error", "-i", filepath.Join(root, original.Path),
		"-af", "asetpts='PTS+if(gte(T,10),0.5/TB,0)'", "-c:a", "pcm_f32le", "-ac", "2", "-ar", "44100", "-f", "nut", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("timestamp-gap fixture: %v %s", err, output)
	}
	framesRaw, err := exec.Command(engines.ffprobe, "-v", "error", "-select_streams", "a:0", "-show_frames", "-show_entries", "frame=pts_time,nb_samples", "-of", "json", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var timestamps struct {
		Frames []struct {
			PTS     string `json:"pts_time"`
			Samples int    `json:"nb_samples"`
		} `json:"frames"`
	}
	if err = json.Unmarshal(framesRaw, &timestamps); err != nil {
		t.Fatal(err)
	}
	var previousEnd, maxGap float64
	for _, frame := range timestamps.Frames {
		pts, err := strconv.ParseFloat(frame.PTS, 64)
		if err != nil {
			t.Fatal(err)
		}
		maxGap = max(maxGap, pts-previousEnd)
		previousEnd = pts + float64(frame.Samples)/44100
	}
	if maxGap < .49 || maxGap > .51 {
		t.Fatalf("fixture has no half-second timestamp gap: %.6f", maxGap)
	}
	fp, err := fileFingerprint(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	track := healthTrack{Path: filepath.Base(path), SHA256: fp.Digest, Result: healthResult{Duration: 20.5}}
	preview, err := engines.healthPreview(context.Background(), root, track, 10.25)
	if err != nil {
		t.Fatal(err)
	}
	media, err := base64.StdEncoding.DecodeString(preview.Data)
	if err != nil {
		t.Fatal(err)
	}
	clip := filepath.Join(t.TempDir(), "preview.mp3")
	os.WriteFile(clip, media, 0600)
	y := samples(t, engines.ffmpeg, clip)
	// Original 10.15..10.35 s must stay silent at preview 4.9..5.1 s,
	// rather than being filled by later music and padded only at the end.
	var energy float64
	for i := int(4.9 * 44100); i < int(5.1*44100); i++ {
		energy += float64(y[i]) * float64(y[i])
	}
	if rms := math.Sqrt(energy / float64(int(.2*44100))); rms > 1e-5 {
		t.Fatalf("timestamp gap collapsed: RMS %.8f", rms)
	}
	x := samples(t, engines.ffmpeg, filepath.Join(root, original.Path))
	var xx, yy, xy float64
	for i := 0; i < 22050; i++ {
		a, b := float64(x[int(10.25*44100)+i]), float64(y[int(5.5*44100)+i])
		xx += a * a
		yy += b * b
		xy += a * b
	}
	if correlation := xy / math.Sqrt(xx*yy); correlation < .99 {
		t.Fatalf("music after gap shifted: correlation %.5f", correlation)
	}
}
