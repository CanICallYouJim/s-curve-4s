package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"math/rand"
	"testing"
)

func signalPCM(samples []float32) []byte {
	data := make([]byte, len(samples)*4)
	for i, sample := range samples {
		binary.LittleEndian.PutUint32(data[i*4:i*4+4], math.Float32bits(sample))
	}
	return data
}

func signalTone(rate int, seconds, frequency float64) []float32 {
	samples := make([]float32, int(float64(rate)*seconds))
	for i := range samples {
		samples[i] = float32(.4 * math.Sin(2*math.Pi*frequency*float64(i)/float64(rate)))
	}
	return samples
}

func TestSignalHealthyTonesAndSilence(t *testing.T) {
	for _, frequency := range []float64{0, 220, 440, 7000} {
		samples := signalTone(16000, 2, frequency)
		result, err := analyzePCM(bytes.NewReader(signalPCM(samples)), 16000, 1)
		if err != nil || len(result.Findings) != 0 || result.Duration != 2 {
			t.Fatalf("frequency %g: %+v %v", frequency, result, err)
		}
	}
}

func TestSignalInternalDropout(t *testing.T) {
	samples := signalTone(16000, 3, 440)
	for i := 16000; i < 20800; i++ {
		samples[i] = 0
	}
	result, err := analyzePCM(bytes.NewReader(signalPCM(samples)), 16000, 1)
	if err != nil || len(result.Findings) != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	finding := result.Findings[0]
	if finding.Kind != "silence_gap" || math.Abs(finding.Start-1) > .025 || math.Abs(finding.End-1.3) > .025 {
		t.Fatal(finding)
	}
}

func TestSignalEdgeSilenceAndLongPause(t *testing.T) {
	samples := make([]float32, 16000)
	samples = append(samples, signalTone(16000, 1, 440)...)
	samples = append(samples, make([]float32, 16000*3)...)
	samples = append(samples, signalTone(16000, 1, 440)...)
	samples = append(samples, make([]float32, 16000)...)
	result, err := analyzePCM(bytes.NewReader(signalPCM(samples)), 16000, 1)
	if err != nil || len(result.Findings) != 0 {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestSignalRepeatedComplexBlock(t *testing.T) {
	random := rand.New(rand.NewSource(13))
	block := make([]float32, 1600)
	for i := range block {
		block[i] = float32((random.Float64() - .5) * .5)
	}
	samples := signalTone(16000, 1, 440)
	for i := 0; i < 9; i++ {
		samples = append(samples, block...)
	}
	samples = append(samples, signalTone(16000, 1, 440)...)
	result, err := analyzePCM(bytes.NewReader(signalPCM(samples)), 16000, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range result.Findings {
		if finding.Kind == "repeated_audio" && finding.Start >= .98 && finding.Start <= 1.02 && finding.End >= 1.85 {
			return
		}
	}
	t.Fatalf("loop not detected: %+v", result)
}

func TestSignalNonRepeatingNoise(t *testing.T) {
	random := rand.New(rand.NewSource(42))
	samples := make([]float32, 16000*2)
	for i := range samples {
		samples[i] = float32((random.Float64() - .5) * .5)
	}
	result, err := analyzePCM(bytes.NewReader(signalPCM(samples)), 16000, 1)
	if err != nil || len(result.Findings) != 0 {
		t.Fatalf("%+v %v", result, err)
	}
}

type signalShortReader struct{ io.Reader }

func (r signalShortReader) Read(p []byte) (int, error) {
	if len(p) > 7 {
		p = p[:7]
	}
	return r.Reader.Read(p)
}

func TestSignalStreamingAndTruncatedFrames(t *testing.T) {
	mono := signalTone(44100, .731, 440)
	stereo := make([]float32, len(mono)*2)
	for i, sample := range mono {
		stereo[2*i], stereo[2*i+1] = sample, sample/2
	}
	data := signalPCM(stereo)
	result, err := analyzePCM(signalShortReader{bytes.NewReader(data)}, 44100, 2)
	if err != nil || len(result.Findings) != 0 || math.Abs(result.Duration-float64(len(mono))/44100) > 1e-9 {
		t.Fatalf("%+v %v", result, err)
	}
	for _, tail := range []int{1, 2, 3, 4} {
		if _, err := analyzePCM(bytes.NewReader(data[:len(data)-tail]), 44100, 2); err == nil {
			t.Fatalf("accepted truncated stereo frame, tail %d", tail)
		}
	}
}

func TestSignalClippedPlateau(t *testing.T) {
	samples := signalTone(16000, 2, 440)
	for i := 16000; i < 16080; i++ {
		samples[i] = 1
	}
	result, err := analyzePCM(bytes.NewReader(signalPCM(samples)), 16000, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range result.Findings {
		if finding.Kind == "clipping" && finding.Start >= .98 && finding.End <= 1.04 {
			return
		}
	}
	t.Fatal(result)
}
