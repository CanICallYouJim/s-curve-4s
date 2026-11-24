package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testTools(t *testing.T) tools {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is needed for integration tests")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is needed for integration tests")
	}
	return tools{ffmpeg, ffprobe}
}

func fixture(t *testing.T, bin, path string, options ...string) {
	t.Helper()
	args := []string{"-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100:duration=6", "-af", "volume=6", "-metadata", "title=Тест & музыка"}
	args = append(args, options...)
	args = append(args, path)
	if output, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, output)
	}
}

func samples(t *testing.T, bin, path string) []float32 {
	t.Helper()
	raw, err := exec.Command(bin, "-v", "error", "-i", path, "-map", "0:a:0", "-ac", "1", "-ar", "44100", "-f", "f32le", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	result := make([]float32, len(raw)/4)
	for i := range result {
		result[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4 : i*4+4]))
	}
	return result
}

func TestFolderBoundaryAndParameters(t *testing.T) {
	binaries := testTools(t)
	folder := t.TempDir()
	fixture(t, binaries.ffmpeg, filepath.Join(folder, "Кириллица & [1].MP3"), "-c:a", "libmp3lame", "-b:a", "128k")
	fixture(t, binaries.ffmpeg, filepath.Join(folder, "same.wav"), "-c:a", "pcm_s24le")
	fixture(t, binaries.ffmpeg, filepath.Join(folder, "same.flac"), "-c:a", "flac", "-sample_fmt", "s16")
	fixture(t, binaries.ffmpeg, filepath.Join(folder, "aac.m4a"), "-c:a", "aac", "-b:a", "192k")
	os.Mkdir(filepath.Join(folder, "вложенная"), 0700)
	fixture(t, binaries.ffmpeg, filepath.Join(folder, "вложенная", "ignored.mp3"), "-c:a", "libmp3lame")
	os.WriteFile(filepath.Join(folder, "broken.mp3"), make([]byte, 2048), 0600)
	os.WriteFile(filepath.Join(folder, "file.txt"), []byte("keep"), 0600)
	before := map[string][32]byte{}
	entries, _ := os.ReadDir(folder)
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			data, _ := os.ReadFile(filepath.Join(folder, entry.Name()))
			before[entry.Name()] = sha256.Sum256(data)
		}
	}
	var log bytes.Buffer
	output, done, failed, err := processFolder(context.Background(), folder, binaries, &log)
	if err != nil || done != 4 || failed != 1 {
		t.Fatalf("%d %d %v\n%s", done, failed, err, log.String())
	}
	for name, digest := range before {
		data, _ := os.ReadFile(filepath.Join(folder, name))
		if sha256.Sum256(data) != digest {
			t.Fatalf("source changed %s", name)
		}
	}
	outputs, _ := os.ReadDir(output)
	if len(outputs) != 5 {
		t.Fatalf("unexpected outputs: %v", outputs)
	}
	if _, err = os.Stat(filepath.Join(output, "ignored.mp3")); !os.IsNotExist(err) {
		t.Fatal("recursive scan")
	}
	a, err := binaries.probe(context.Background(), filepath.Join(output, "Кириллица & [1].MP3"))
	if err != nil {
		t.Fatal(err)
	}
	mp3, err := a.audio()
	if err != nil || mp3.Bitrate != "128000" {
		t.Fatalf("MP3 bitrate changed: %+v %v", mp3, err)
	}
	second, err := newOutput(folder)
	if err != nil || second == output {
		t.Fatalf("retry output: %v", err)
	}
	for name := range before {
		if filepath.Ext(strings.ToLower(name)) == ".txt" || name == "broken.mp3" {
			continue
		}
		x := samples(t, binaries.ffmpeg, filepath.Join(folder, name))
		y := samples(t, binaries.ffmpeg, filepath.Join(output, name))
		if math.Abs(float64(len(x)-len(y))) > 4410 {
			t.Fatalf("duration %s: %d %d", name, len(x), len(y))
		}
		for _, second := range []float64{1, 2, 3, 4.5, 5.5} {
			var xy, xx float64
			for i := int((second - .03) * 44100); i < int((second+.03)*44100); i++ {
				xy += float64(x[i]) * float64(y[i])
				xx += float64(x[i]) * float64(x[i])
			}
			expected := .5 - .5*math.Cos(math.Pi*math.Min(second/4, 1))
			if math.Abs(xy/xx-expected) > .025 {
				t.Fatalf("gain %s at %.1f: %.4f expected %.4f", name, second, xy/xx, expected)
			}
		}
	}
}

func TestCancelAndNoPartialOutput(t *testing.T) {
	binaries := testTools(t)
	folder := t.TempDir()
	file := filepath.Join(folder, "test.wav")
	fixture(t, binaries.ffmpeg, file, "-c:a", "pcm_s16le")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := processFolder(ctx, folder, binaries, io.Discard)
	if err == nil {
		t.Fatal("cancel ignored")
	}
}

func TestMP3Tags(t *testing.T) {
	prefix := append([]byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0, 4}, []byte("test")...)
	body := []byte("audio")
	suffix := make([]byte, 128)
	copy(suffix, "TAG")
	a, b, err := mp3Tags(append(append(prefix, body...), suffix...))
	if err != nil || !bytes.Equal(a, prefix) || !bytes.Equal(b, suffix) {
		t.Fatalf("tags: %v", err)
	}
	if _, _, err = mp3Tags([]byte("ID3")); err == nil {
		t.Fatal("short ID3 accepted")
	}
}

func TestOpusAndAlternateExtensions(t *testing.T) {
	binaries := testTools(t)
	for _, extension := range []string{".opus", ".wave", ".rf64", ".alac"} {
		t.Run(extension, func(t *testing.T) {
			folder := t.TempDir()
			source := filepath.Join(folder, "input"+extension)
			options := []string{"-c:a", "pcm_s24le", "-f", "wav"}
			switch extension {
			case ".opus":
				options = []string{"-c:a", "libopus", "-b:a", "128k"}
			case ".rf64":
				options = append(options, "-rf64", "always")
			case ".alac":
				options = []string{"-c:a", "alac", "-f", "ipod"}
			}
			fixture(t, binaries.ffmpeg, source, options...)
			if err := binaries.convert(context.Background(), source, filepath.Join(folder, "output"+extension)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLosslessDepthAndLowBitrates(t *testing.T) {
	binaries := testTools(t)
	for _, codec := range []string{"wavpack", "tta"} {
		extension := map[string]string{"wavpack": ".wv", "tta": ".tta"}[codec]
		t.Run(codec, func(t *testing.T) {
			folder := t.TempDir()
			input := filepath.Join(folder, "input"+extension)
			fixture(t, binaries.ffmpeg, input, "-c:a", codec)
			if err := binaries.convert(context.Background(), input, filepath.Join(folder, "output"+extension)); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, bitrate := range []string{"160k", "192k", "224k"} {
		t.Run(bitrate, func(t *testing.T) {
			folder := t.TempDir()
			input := filepath.Join(folder, "input.mp3")
			output := filepath.Join(folder, "output.mp3")
			fixture(t, binaries.ffmpeg, input, "-c:a", "libmp3lame", "-b:a", bitrate)
			if err := binaries.convert(context.Background(), input, output); err != nil {
				t.Fatal(err)
			}
			x, y := samples(t, binaries.ffmpeg, input), samples(t, binaries.ffmpeg, output)
			var xx, xy float64
			for i := 4 * 44100; i < 5*44100; i++ {
				xx += float64(x[i]) * float64(x[i])
				xy += float64(x[i]) * float64(y[i])
			}
			if math.Abs(xy/xx-1) > .01 {
				t.Fatalf("changed tail level %s %.5f", bitrate, xy/xx)
			}
			before, _ := binaries.probe(context.Background(), input)
			after, _ := binaries.probe(context.Background(), output)
			a, _ := before.audio()
			b, _ := after.audio()
			if a.Bitrate != b.Bitrate {
				t.Fatalf("changed bitrate %s -> %s", a.Bitrate, b.Bitrate)
			}
		})
	}
}

func TestAMRDoesNotSilentlyChangeBitrate(t *testing.T) {
	for _, codec := range []string{"amr_nb", "amr_wb"} {
		_, err := codecOptions(stream{Codec: codec, Rate: "8000", Channels: 1, Bitrate: "8000"})
		if err == nil {
			t.Fatalf("%s must report unsupported bitrate mode", codec)
		}
	}
}
