package main

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"sort"
)

type signalFinding struct {
	Kind       string
	Start, End float64
	Detail     string
}

type signalAnalysis struct {
	Duration float64
	Findings []signalFinding
}

const maxSignalFindings = 256

// Signal heuristics produce listening prompts. They cannot establish whether a
// pause, repeated sound or loud peak was part of the original performance.
func analyzePCM(r io.Reader, sampleRate, channels int) (signalAnalysis, error) {
	var result signalAnalysis
	if sampleRate < 100 || sampleRate > 768000 || channels < 1 || channels > 64 {
		return result, errors.New("неверные параметры PCM для акустического анализа")
	}
	frameBytes := channels * 4
	windowFrames := sampleRate / 50
	buffer := make([]byte, windowFrames*frameBytes)
	blockFrames := sampleRate / 10
	blockSamples := blockFrames * channels
	block := make([]float32, 0, blockSamples+windowFrames*channels)
	var history [][]float32
	var runs [6]int
	var totalFrames, blockCount int64
	var previousRMS float64
	var silenceStart float64
	var silenceActive, loudBeforeSilence bool
	var clippedRuns []int = make([]int, channels)
	var previous []float32 = make([]float32, channels)
	limitReached := false

	add := func(kind string, start, end float64, detail string) {
		for i := len(result.Findings) - 1; i >= 0; i-- {
			finding := &result.Findings[i]
			if finding.Kind != kind {
				continue
			}
			if start <= finding.End+.12 && end >= finding.Start {
				finding.Start = math.Min(finding.Start, start)
				finding.End = math.Max(finding.End, end)
				return
			}
			break
		}
		if len(result.Findings) >= maxSignalFindings {
			limitReached = true
			return
		}
		result.Findings = append(result.Findings, signalFinding{kind, start, end, detail})
	}

	for {
		n, readErr := io.ReadFull(r, buffer)
		if n%frameBytes != 0 {
			return result, errors.New("оборван PCM: неполный отсчёт или аудиокадр")
		}
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return result, readErr
		}
		if n == 0 {
			break
		}
		start := float64(totalFrames) / float64(sampleRate)
		frames := n / frameBytes
		end := float64(totalFrames+int64(frames)) / float64(sampleRate)
		var energy, peak float64
		clipped := false
		for i := 0; i < n/4; i++ {
			x := math.Float32frombits(binary.LittleEndian.Uint32(buffer[i*4 : i*4+4]))
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				return result, errors.New("PCM содержит NaN или бесконечные значения")
			}
			amplitude := math.Abs(float64(x))
			energy += float64(x) * float64(x)
			peak = math.Max(peak, amplitude)
			channel := i % channels
			if amplitude >= .999 && math.Abs(float64(x-previous[channel])) < .00001 {
				clippedRuns[channel]++
			} else {
				clippedRuns[channel] = 0
			}
			previous[channel] = x
			if clippedRuns[channel] >= sampleRate/1000+1 {
				clipped = true
			}
			block = append(block, x)
		}
		rms := math.Sqrt(energy / float64(n/4))
		if peak <= .00001 {
			if !silenceActive {
				silenceStart = start
				silenceActive = true
				loudBeforeSilence = totalFrames > 0 && previousRMS >= .01
			}
		} else if silenceActive {
			length := start - silenceStart
			if loudBeforeSilence && rms >= .01 && length >= .099 && length <= 2 {
				add("silence_gap", silenceStart, start, "Короткая почти цифровая тишина между звучащими участками. Возможен провал; музыкальная пауза тоже может дать такой признак.")
			}
			silenceActive = false
		}
		if clipped {
			add("clipping", start, end, "Почти плоская вершина около полного уровня. Возможна перегрузка; проверьте этот участок на слух.")
		}
		previousRMS = rms
		totalFrames += int64(frames)
		for len(block) >= blockSamples {
			current := block[:blockSamples]
			complex := complexBlock(current, channels)
			for lag := 1; lag <= 5; lag++ {
				if complex && len(history) >= lag && similarBlocks(current, history[len(history)-lag]) {
					runs[lag]++
				} else {
					runs[lag] = 0
				}
				if runs[lag] < lag || float64(runs[lag]+lag)*float64(blockFrames)/float64(sampleRate) < .399 {
					continue
				}
				first := blockCount - int64(runs[lag]+lag) + 1
				from := float64(first*int64(blockFrames)) / float64(sampleRate)
				to := float64((blockCount+1)*int64(blockFrames)) / float64(sampleRate)
				add("repeated_audio", from, to, "Почти одинаковые короткие блоки сложного звука повторяются подряд. Возможны зацикливание или «заедание»; намеренный музыкальный повтор тоже возможен.")
			}
			history = append(history, append([]float32(nil), current...))
			if len(history) > 5 {
				history = history[1:]
			}
			block = append(block[:0], block[blockSamples:]...)
			blockCount++
		}
		if readErr != nil {
			break
		}
	}
	result.Duration = float64(totalFrames) / float64(sampleRate)
	if limitReached {
		result.Findings = append(result.Findings, signalFinding{"signal_limit", 0, result.Duration, "Показаны первые 256 отдельных акустических предупреждений. Остальные сокращены; полный проход по записи завершён."})
	}
	sort.SliceStable(result.Findings, func(i, j int) bool {
		return result.Findings[i].Start < result.Findings[j].Start
	})
	return result, nil
}

// A sine of any frequency obeys a second-order recurrence. Rejecting blocks
// well described by that recurrence avoids treating a sustained tone as a loop.
func complexBlock(samples []float32, channels int) bool {
	var energy, denominator, numerator float64
	for i := 2 * channels; i < len(samples); i++ {
		x, previous, previous2 := float64(samples[i]), float64(samples[i-channels]), float64(samples[i-2*channels])
		energy += x * x
		denominator += previous * previous
		numerator += (x + previous2) * previous
	}
	if energy < float64(len(samples))*.000025 || denominator == 0 {
		return false
	}
	coefficient := numerator / denominator
	var residual float64
	for i := 2 * channels; i < len(samples); i++ {
		error := float64(samples[i]) + float64(samples[i-2*channels]) - coefficient*float64(samples[i-channels])
		residual += error * error
	}
	return residual/energy >= .02
}

func similarBlocks(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	var difference, energy float64
	for i, value := range a {
		d := float64(value - b[i])
		difference += d * d
		energy += float64(value)*float64(value) + float64(b[i])*float64(b[i])
	}
	return energy > 0 && difference/energy < .000005
}
