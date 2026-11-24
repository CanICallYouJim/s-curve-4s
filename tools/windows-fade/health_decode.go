package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type diagnosticSink struct {
	pending  []byte
	Errors   int
	Warnings int
	Metadata int
	Messages []string
	seen     map[string]bool
}

func (d *diagnosticSink) line(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	low := strings.ToLower(line)
	// An inferred duration is normal for old MP3/ADTS without an index.
	if strings.Contains(low, "estimating duration from bitrate") || strings.Contains(low, "last message repeated") || strings.Contains(low, "guessed channel layout") {
		return
	}
	if strings.Contains(low, "bits_left=") && !strings.Contains(low, "bits_left=-") {
		return
	}
	metadata := textMetadataMessage(line)
	isError := strings.Contains(low, "[error]") || strings.Contains(low, "[fatal]")
	for _, word := range []string{"header missing", "error while decoding", "invalid new backstep", "invalid block type", "big_values too big", "invalid data found", "crc mismatch", "corrupt decoded frame", "bits_left=-"} {
		if strings.Contains(low, word) {
			isError = true
			break
		}
	}
	if metadata {
		d.Metadata++
	} else if isError {
		d.Errors++
	} else {
		d.Warnings++
	}
	if d.seen == nil {
		d.seen = make(map[string]bool)
	}
	// Strip volatile object addresses before de-duplication and persistence.
	if first := strings.Index(line, " @ "); first >= 0 {
		if end := strings.Index(line[first:], "]"); end >= 0 {
			line = line[:first] + line[first+end:]
		}
	}
	if !d.seen[line] && len(d.Messages) < 32 {
		d.seen[line] = true
		if len(line) > 500 {
			line = line[:500] + "…"
		}
		d.Messages = append(d.Messages, line)
	}
}

func (d *diagnosticSink) Write(p []byte) (int, error) {
	size := len(p)
	for len(p) > 0 {
		end := bytes.IndexByte(p, '\n')
		if end < 0 {
			if len(d.pending) < 65536 {
				limit := min(len(p), 65536-len(d.pending))
				d.pending = append(d.pending, p[:limit]...)
			}
			break
		}
		room := 65536 - len(d.pending)
		d.pending = append(d.pending, p[:min(end, room)]...)
		d.line(string(d.pending))
		d.pending = d.pending[:0]
		p = p[end+1:]
	}
	return size, nil
}

func (d *diagnosticSink) finish() {
	if len(d.pending) > 0 {
		d.line(string(d.pending))
		d.pending = nil
	}
}
func (d *diagnosticSink) detail() string { return strings.Join(d.Messages, "\n") }

type auditEvent struct {
	Type          string `json:"type"`
	Stream        int    `json:"stream_index"`
	Position      string `json:"pos"`
	FramePosition string `json:"pkt_pos"`
	PTS           string `json:"pts_time"`
	BestPTS       string `json:"best_effort_timestamp_time"`
	Duration      string `json:"duration_time"`
	Flags         string `json:"flags"`
	Samples       int    `json:"nb_samples"`
	SideData      []struct {
		Type    string `json:"side_data_type"`
		Skip    int    `json:"skip_samples"`
		Padding int    `json:"discard_padding"`
	} `json:"side_data_list"`
}

type auditPacket struct {
	Key        string
	Start, End float64
	Corrupt    bool
}

type packetAudit struct {
	Packets    []auditPacket
	Frames     map[string]bool
	FrameCount int
	Duration   float64
	AudioEnd   int64
	Limited    bool
}

func eventTime(value string) (float64, bool) {
	time, err := strconv.ParseFloat(value, 64)
	return time, err == nil
}

func readPacketAudit(reader io.Reader, rates map[int]float64) (packetAudit, error) {
	audit := packetAudit{Frames: make(map[string]bool)}
	decoder := json.NewDecoder(reader)
	token, err := decoder.Token()
	if err != nil {
		return audit, err
	}
	if token != json.Delim('{') {
		return audit, errors.New("неожиданный ответ ffprobe")
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return audit, err
		}
		if key != "packets_and_frames" {
			var skipped json.RawMessage
			if err = decoder.Decode(&skipped); err != nil {
				return audit, err
			}
			continue
		}
		if token, err = decoder.Token(); err != nil || token != json.Delim('[') {
			return audit, errors.New("нет массива пакетов ffprobe")
		}
		for decoder.More() {
			var event auditEvent
			if err = decoder.Decode(&event); err != nil {
				return audit, err
			}
			position := event.Position
			if event.Type == "frame" {
				position = event.FramePosition
			}
			key := fmt.Sprintf("%d/%s", event.Stream, position)
			if event.Type == "frame" {
				audit.FrameCount++
				if len(audit.Frames) < 1000000 && position != "" && position != "N/A" {
					audit.Frames[key] = true
				} else if len(audit.Frames) >= 1000000 {
					audit.Limited = true
				}
				pts, ok := eventTime(event.BestPTS)
				if !ok {
					pts, ok = eventTime(event.PTS)
				}
				rate := rates[event.Stream]
				if ok && rate > 0 {
					audit.Duration = max(audit.Duration, pts+float64(event.Samples)/rate)
				}
			} else if event.Type == "packet" {
				pts, ok := eventTime(event.PTS)
				duration, valid := eventTime(event.Duration)
				if !ok || !valid || pts < 0 || duration <= 0 || strings.Contains(event.Flags, "D") || position == "" || position == "N/A" {
					continue
				}
				// Fully discarded encoder padding legitimately produces no frame.
				discarded := false
				for _, side := range event.SideData {
					if side.Type == "Skip Samples" && rates[event.Stream] > 0 && float64(side.Skip+side.Padding) >= duration*rates[event.Stream]-.05 {
						discarded = true
					}
				}
				if discarded && !strings.Contains(event.Flags, "C") {
					continue
				}
				if len(audit.Packets) < 1000000 {
					audit.Packets = append(audit.Packets, auditPacket{key, pts, pts + duration, strings.Contains(event.Flags, "C")})
				} else {
					audit.Limited = true
				}
			}
		}
		if _, err = decoder.Token(); err != nil {
			return audit, err
		}
	}
	_, err = decoder.Token()
	return audit, err
}

func missingIntervals(audit packetAudit, confirmed bool) []healthIssue {
	packets := append([]auditPacket{}, audit.Packets...)
	sort.Slice(packets, func(i, j int) bool { return packets[i].Start < packets[j].Start })
	var result []healthIssue
	for _, packet := range packets {
		if audit.Frames[packet.Key] && !packet.Corrupt {
			continue
		}
		severity, kind := "warning", "packet_gap"
		if confirmed || packet.Corrupt {
			severity, kind = "error", "decode_error"
		}
		if audit.AudioEnd > 0 && !confirmed && !packet.Corrupt {
			_, pos, ok := strings.Cut(packet.Key, "/")
			offset, err := strconv.ParseInt(pos, 10, 64)
			if ok && err == nil && offset >= audit.AudioEnd {
				severity = "info"
			}
		}
		if len(result) > 0 && result[len(result)-1].Severity == severity && result[len(result)-1].Kind == kind && packet.Start <= *result[len(result)-1].End+.15 {
			*result[len(result)-1].End = max(*result[len(result)-1].End, packet.End)
			continue
		}
		if len(result) >= 256 {
			break
		}
		start, end := packet.Start, packet.End
		detail := "Для пакета не найден декодированный кадр. Для некоторых контейнеров это допустимо; требуется проверка на слух."
		if severity == "error" {
			detail = "Недекодированные или повреждённые пакеты; декодер сообщил ошибки. Время приблизительное, слушайте с запасом 1–2 секунды."
		}
		result = append(result, healthIssue{Kind: kind, Severity: severity, Start: &start, End: &end, Detail: detail})
	}
	return result
}

func (t tools) auditPackets(ctx context.Context, path string, info media) (packetAudit, diagnosticSink, error) {
	rates := make(map[int]float64)
	for _, s := range info.Streams {
		if s.Type == "audio" {
			rates[s.Index], _ = strconv.ParseFloat(s.Rate, 64)
		}
	}
	cmd := command(ctx, t.ffprobe, "-v", "repeat+level+warning", "-threads", "1", "-err_detect", "crccheck+bitstream+buffer", "-select_streams", "a", "-show_packets", "-show_frames", "-show_entries", "packet=stream_index,pts_time,duration_time,pos,flags:packet_side_data=side_data_type,skip_samples,discard_padding:frame=stream_index,pkt_pos,pts_time,best_effort_timestamp_time,nb_samples", "-of", "json", path)
	var diagnostics diagnosticSink
	cmd.Stderr = &diagnostics
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return packetAudit{}, diagnostics, err
	}
	if err = cmd.Start(); err != nil {
		return packetAudit{}, diagnostics, err
	}
	audit, parseErr := readPacketAudit(pipe, rates)
	if parseErr != nil && ctx.Err() == nil {
		_ = cmd.Process.Kill()
	}
	runErr := cmd.Wait()
	diagnostics.finish()
	if ctx.Err() != nil {
		return audit, diagnostics, ctx.Err()
	}
	if parseErr != nil {
		return audit, diagnostics, fmt.Errorf("неполный ответ декодера: %w", parseErr)
	}
	return audit, diagnostics, runErr
}

func (t tools) auditSignal(ctx context.Context, path string) (signalAnalysis, diagnosticSink, error) {
	cmd := command(ctx, t.ffmpeg, "-nostdin", "-v", "repeat+level+warning", "-threads", "1", "-i", path, "-map", "0:a:0", "-filter_threads", "1", "-ar", "22050", "-ac", "2", "-f", "f32le", "pipe:1")
	var diagnostics diagnosticSink
	cmd.Stderr = &diagnostics
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return signalAnalysis{}, diagnostics, err
	}
	if err = cmd.Start(); err != nil {
		return signalAnalysis{}, diagnostics, err
	}
	analysis, readErr := analyzePCM(pipe, 22050, 2)
	if readErr != nil && ctx.Err() == nil {
		_ = cmd.Process.Kill()
	}
	runErr := cmd.Wait()
	diagnostics.finish()
	if ctx.Err() != nil {
		return analysis, diagnostics, ctx.Err()
	}
	if readErr != nil {
		return analysis, diagnostics, readErr
	}
	return analysis, diagnostics, runErr
}

func (t tools) checkProfile(ctx context.Context) (string, error) {
	var versions []string
	for _, tool := range []string{t.ffmpeg, t.ffprobe} {
		raw, err := command(ctx, tool, "-version").Output()
		if err != nil {
			return "", err
		}
		line, _, _ := strings.Cut(string(raw), "\n")
		versions = append(versions, line)
	}
	return checkVersion + "|" + strings.Join(versions, "|"), nil
}

func (t tools) analyzeHealth(parent context.Context, path string) (healthResult, error) {
	return t.analyzeAudioHealth(parent, path, true)
}

func (t tools) analyzeAudioHealth(parent context.Context, path string, allowTailProof bool) (healthResult, error) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Minute)
	defer cancel()
	result := healthResult{CheckedAt: time.Now().Format(time.RFC3339)}
	info, err := t.probe(ctx, path)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		// A missing engine is an infrastructure failure, never a cached verdict.
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return result, err
		}
		result.Complete = true
		result.Issues = []healthIssue{{Kind: "unreadable", Severity: "error", Detail: err.Error()}}
		return result, nil
	}
	if _, err = info.audio(); err != nil {
		result.Complete = true
		result.Issues = []healthIssue{{Kind: "unreadable", Severity: "error", Detail: "Нет читаемой аудиодорожки. Возможны повреждение файла или неподдерживаемый формат."}}
		return result, nil
	}
	audit, diagnostics, err := t.auditPackets(ctx, path, info)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return result, err
		}
		if exit.ExitCode() < 0 {
			return result, fmt.Errorf("декодер аварийно завершился: %w", err)
		}
		diagnostics.Errors++
	}
	result.Duration = audit.Duration
	if strings.EqualFold(filepath.Ext(path), ".mp3") {
		if data, readErr := os.ReadFile(path); readErr == nil {
			if _, suffix, tagErr := mp3Tags(data); tagErr == nil && len(suffix) > 0 {
				audit.AudioEnd = int64(len(data) - len(suffix))
			}
		}
	}
	audioCount := 0
	var audioStream stream
	for _, s := range info.Streams {
		if s.Type == "audio" {
			audioCount++
			audioStream = s
		}
	}
	if audioCount == 1 {
		declared, ok := eventTime(audioStream.DeclaredDuration)
		if ok && declared > 0 && audit.Duration > 0 && declared-audit.Duration > max(.2, declared*.01) {
			result.Issues = append(result.Issues, healthIssue{Kind: "duration_mismatch", Severity: "warning", Detail: fmt.Sprintf("Заголовок обещает %.2f с, но декодированы кадры только до %.2f с. Возможно обрезан конец или неверно заполнен заголовок.", declared, audit.Duration)})
		}
	}
	if audit.FrameCount == 0 {
		result.Complete = true
		result.Issues = []healthIssue{{Kind: "unreadable", Severity: "error", Detail: "Ни одного аудиокадра не удалось декодировать. " + diagnostics.detail()}}
		return result, nil
	}
	result.Issues = append(result.Issues, missingIntervals(audit, diagnostics.Errors > 0)...)
	hasDecodeError := false
	for _, issue := range result.Issues {
		if issue.Kind == "decode_error" {
			hasDecodeError = true
		}
	}
	if diagnostics.Errors > 0 && !hasDecodeError {
		result.Issues = append(result.Issues, healthIssue{Kind: "decode_error", Severity: "error", Detail: "Декодер сообщил ошибки, точный таймкод не определён."})
	}
	if diagnostics.Errors > 0 && allowTailProof {
		if cleaned, ok := t.proveMetadataTail(ctx, path, result); ok {
			for i := range result.Issues {
				if result.Issues[i].Kind == "decode_error" {
					result.Issues[i].Severity = "info"
				}
			}
			result.Issues = append(result.Issues, cleaned.Issues...)
			result.Issues = append(result.Issues, healthIssue{Kind: "decoder_log", Severity: "info", Detail: diagnostics.detail()})
			result.Complete = true
			return result, nil
		}
	}
	if diagnostics.Errors > 0 {
		result.Issues = append(result.Issues, healthIssue{Kind: "decoder_log", Severity: "info", Detail: diagnostics.detail()})
	} else {
		if diagnostics.Warnings > 0 || diagnostics.Metadata > 0 {
			result.Issues = append(result.Issues, healthIssue{Kind: "decoder_warning", Severity: map[bool]string{true: "warning", false: "info"}[diagnostics.Warnings > 0], Detail: diagnostics.detail()})
		}
		signal, signalLog, signalErr := t.auditSignal(ctx, path)
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if signalErr != nil {
			return result, fmt.Errorf("анализ звука не завершён: %w", signalErr)
		}
		if signalLog.Errors > 0 {
			result.Issues = append(result.Issues, healthIssue{Kind: "decode_error", Severity: "error", Detail: signalLog.detail()})
		}
		if signalLog.Metadata > 0 || signalLog.Warnings > 0 {
			result.Issues = append(result.Issues, healthIssue{Kind: "decoder_warning", Severity: map[bool]string{true: "warning", false: "info"}[signalLog.Warnings > 0], Detail: signalLog.detail()})
		}
		for _, finding := range signal.Findings {
			start, end := finding.Start, finding.End
			severity := "warning"
			if finding.Kind == "signal_limit" {
				severity = "info"
			}
			result.Issues = append(result.Issues, healthIssue{Kind: finding.Kind, Severity: severity, Start: &start, End: &end, Detail: finding.Detail})
		}
		if signal.Duration <= 0 {
			return result, errors.New("декодер не выдал звук для анализа")
		}
		if audit.Duration > 0 && absFloat(audit.Duration-signal.Duration) > max(1, audit.Duration*.01) {
			result.Issues = append(result.Issues, healthIssue{Kind: "duration_mismatch", Severity: "warning", Detail: fmt.Sprintf("Различается длительность по временным меткам и по декодированному звуку: %.2f и %.2f с. Возможны неверные метки или потерянные участки.", audit.Duration, signal.Duration)})
		}
	}
	if audit.Limited {
		result.Issues = append(result.Issues, healthIssue{Kind: "timeline_limit", Severity: "info", Detail: "Очень длинная запись: детализация пакетов ограничена первым миллионом; декодер проверил всю запись."})
	}
	result.Complete = true
	return result, nil
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

// An apparent EOF decode fault is harmless only when stripping recognized tags
// makes a full strict decode succeed without changing a single decoded sample.
func (t tools) proveMetadataTail(ctx context.Context, path string, original healthResult) (healthResult, bool) {
	if !strings.EqualFold(filepath.Ext(path), ".mp3") {
		return healthResult{}, false
	}
	found := false
	for _, issue := range original.Issues {
		if issue.Kind == "decode_error" {
			if issue.Start == nil || *issue.Start < original.Duration-.001 {
				return healthResult{}, false
			}
			found = true
		}
	}
	if !found {
		return healthResult{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return healthResult{}, false
	}
	_, suffix, err := mp3Tags(data)
	if err != nil || len(suffix) == 0 {
		return healthResult{}, false
	}
	temp, err := os.CreateTemp("", "music-health-tail-*.mp3")
	if err != nil {
		return healthResult{}, false
	}
	name := temp.Name()
	defer os.Remove(name)
	_, writeErr := temp.Write(data[:len(data)-len(suffix)])
	closeErr := temp.Close()
	if writeErr != nil || closeErr != nil {
		return healthResult{}, false
	}
	cleaned, err := t.analyzeAudioHealth(ctx, name, false)
	if err != nil || !cleaned.Complete {
		return healthResult{}, false
	}
	for _, issue := range cleaned.Issues {
		if issue.Severity == "error" {
			return healthResult{}, false
		}
	}
	originalHash, originalSize, err := t.audioPCMHash(ctx, path, false)
	if err != nil {
		return healthResult{}, false
	}
	cleanHash, cleanSize, err := t.audioPCMHash(ctx, name, true)
	if err != nil || originalSize <= 0 || cleanSize != originalSize || originalHash != cleanHash {
		return healthResult{}, false
	}
	return cleaned, true
}

func (t tools) audioPCMHash(ctx context.Context, path string, strict bool) ([32]byte, int64, error) {
	args := []string{"-nostdin", "-v", "error", "-threads", "1"}
	if strict {
		args = append(args, "-xerror")
	}
	args = append(args, "-i", path, "-map", "0:a:0", "-f", "f32le", "pipe:1")
	cmd := command(ctx, t.ffmpeg, args...)
	var diagnostics diagnosticSink
	cmd.Stderr = &diagnostics
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return [32]byte{}, 0, err
	}
	if err = cmd.Start(); err != nil {
		return [32]byte{}, 0, err
	}
	hash := sha256.New()
	size, readErr := io.Copy(hash, pipe)
	runErr := cmd.Wait()
	diagnostics.finish()
	if readErr != nil {
		return [32]byte{}, size, readErr
	}
	if runErr != nil {
		return [32]byte{}, size, runErr
	}
	if strict && diagnostics.Errors > 0 {
		return [32]byte{}, size, errors.New("strict audio decode failed")
	}
	var sum [32]byte
	copy(sum[:], hash.Sum(nil))
	return sum, size, nil
}
