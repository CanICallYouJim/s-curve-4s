package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const outputName = "S-кривая 4 с"

// Audio-only containers. ReadDir deliberately does not visit subdirectories.
const extensions = ".mp3 .mp2 .wav .wave .flac .m4a .m4b .aac .adts .ogg .oga .opus .wma .aif .aiff .aifc .ape .wv .tta .alac .caf .au .snd .ac3 .eac3 .dts .mka .amr .3ga .dsf .dff .tak .mpc .mp+ .ra .spx .w64 .rf64 .a52 .weba"

type stream struct {
	DeclaredDuration string `json:"duration"`
	Index            int    `json:"index"`
	Codec            string `json:"codec_name"`
	Type             string `json:"codec_type"`
	Rate             string `json:"sample_rate"`
	SampleFormat     string `json:"sample_fmt"`
	Channels         int    `json:"channels"`
	Layout           string `json:"channel_layout"`
	Bits             int    `json:"bits_per_sample"`
	RawBits          string `json:"bits_per_raw_sample"`
	Bitrate          string `json:"bit_rate"`
	Disposition      struct {
		Cover int `json:"attached_pic"`
	} `json:"disposition"`
}

type media struct {
	Streams []stream `json:"streams"`
}

type tools struct{ ffmpeg, ffprobe string }

func (m media) audio() (stream, error) {
	for _, s := range m.Streams {
		if s.Type == "audio" && s.Channels > 0 && s.Rate != "0" {
			return s, nil
		}
	}
	return stream{}, errors.New("нет читаемой аудиодорожки")
}

func command(ctx context.Context, binary string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary, args...)
	configureCommand(cmd)
	return cmd
}

func (t tools) probe(ctx context.Context, path string) (media, error) {
	cmd := command(ctx, t.ffprobe, "-v", "error", "-show_streams", "-of", "json", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return media{}, fmt.Errorf("не удалось прочитать аудио: %w\n%s", err, stderr.String())
	}
	var info media
	err = json.Unmarshal(data, &info)
	return info, err
}

// Opus stores no nominal bitrate in its header. Use the measured average
// packet bitrate as the VBR target when the source does not specify one.
func (t tools) packetBitrate(ctx context.Context, path string) (string, error) {
	cmd := command(ctx, t.ffprobe, "-v", "error", "-select_streams", "a:0", "-show_entries", "packet=size,duration_time", "-of", "json", path)
	raw, err := cmd.Output()
	if err != nil {
		return "", err
	}
	var packets struct {
		Packets []struct {
			Size     string `json:"size"`
			Duration string `json:"duration_time"`
		} `json:"packets"`
	}
	if err = json.Unmarshal(raw, &packets); err != nil {
		return "", err
	}
	var size int64
	var duration float64
	for _, packet := range packets.Packets {
		n, e := strconv.ParseInt(packet.Size, 10, 64)
		seconds, f := strconv.ParseFloat(packet.Duration, 64)
		if e == nil && f == nil && seconds > 0 {
			size += n
			duration += seconds
		}
	}
	if size <= 0 || duration <= 0 {
		return "", errors.New("невозможно определить средний аудиобитрейт")
	}
	return strconv.FormatInt(int64(float64(size)*8/duration+.5), 10), nil
}

func muxerOptions(extension string) []string {
	switch strings.ToLower(extension) {
	case ".wave":
		return []string{"-f", "wav"}
	case ".rf64":
		return []string{"-f", "wav", "-rf64", "always"}
	case ".alac":
		return []string{"-f", "ipod"}
	case ".snd":
		return []string{"-f", "au"}
	case ".3ga":
		return []string{"-f", "3gp"}
	case ".weba":
		return []string{"-f", "webm"}
	case ".a52":
		return []string{"-f", "ac3"}
	case ".adts":
		return []string{"-f", "adts"}
	}
	return nil
}

func codecOptions(s stream) ([]string, error) {
	codec := s.Codec
	switch codec {
	case "mp3":
		codec = "libmp3lame"
	case "vorbis":
		codec = "libvorbis"
	case "opus":
		codec = "libopus"
	case "speex":
		codec = "libspeex"
	case "amr_nb", "amr_wb":
		return nil, errors.New("AMR: сохранение исходного режима битрейта не поддерживается")
	case "ape", "tak", "musepack7", "musepack8", "dsd_lsbf", "dsd_msbf", "dsd_lsbf_planar", "dsd_msbf_planar":
		return nil, fmt.Errorf("FFmpeg не умеет записывать исходный кодек %s", s.Codec)
	}
	options := []string{"-c:a", codec, "-ar", s.Rate, "-ac", strconv.Itoa(s.Channels)}
	if s.Codec == "mp3" {
		options = append(options, "-sample_fmt", "fltp")
	}
	if s.Layout != "" {
		options = append(options, "-channel_layout", s.Layout)
	}
	// Lossless bitrate is determined by the new samples, not an encoder target.
	switch s.Codec {
	case "mp3", "mp2", "aac", "vorbis", "opus", "wmav1", "wmav2", "ac3", "eac3", "dca", "speex":
		bitrate, err := strconv.Atoi(s.Bitrate)
		if err != nil || bitrate <= 0 {
			return nil, errors.New("в исходнике не указан аудиобитрейт, сохранение настроек невозможно")
		}
		if s.Codec == "mp3" {
			cbr := false
			for _, b := range []int{8000, 16000, 24000, 32000, 40000, 48000, 56000, 64000, 80000, 96000, 112000, 128000, 144000, 160000, 192000, 224000, 256000, 320000} {
				if bitrate == b {
					cbr = true
					break
				}
			}
			if !cbr {
				options = append(options, "-abr", "1")
			}
		}
		options = append(options, "-b:a", strconv.Itoa(bitrate))
	case "wavpack", "tta":
		if s.SampleFormat != "" {
			options = append(options, "-sample_fmt", s.SampleFormat)
		}
		if s.RawBits != "" && s.RawBits != "0" {
			options = append(options, "-bits_per_raw_sample", s.RawBits)
		}
	case "flac":
		bits, _ := strconv.Atoi(s.RawBits)
		if bits == 0 {
			bits = s.Bits
		}
		if bits > 0 {
			sampleFormat := "s16"
			if bits > 16 {
				sampleFormat = "s32"
			}
			options = append(options, "-sample_fmt", sampleFormat, "-bits_per_raw_sample", strconv.Itoa(bits))
		}
	}
	return options, nil
}

func mp3Tags(data []byte) ([]byte, []byte, error) {
	start := 0
	if bytes.HasPrefix(data, []byte("ID3")) {
		if len(data) < 10 {
			return nil, nil, errors.New("повреждён ID3")
		}
		size := 0
		for _, b := range data[6:10] {
			if b > 127 {
				return nil, nil, errors.New("повреждён размер ID3")
			}
			size = size<<7 | int(b)
		}
		start = 10 + size
		if data[3] == 4 && data[5]&0x10 != 0 {
			start += 10
		}
	}
	end := len(data)
	if end >= 128 && string(data[end-128:end-125]) == "TAG" {
		end -= 128
		if end >= 227 && string(data[end-227:end-223]) == "TAG+" {
			end -= 227
		}
	}
	for end >= 32 {
		next := end
		if string(data[end-32:end-24]) == "APETAGEX" {
			next = end - int(binary.LittleEndian.Uint32(data[end-20:end-16]))
			if next >= 32 && string(data[next-32:next-24]) == "APETAGEX" {
				next -= 32
			}
		} else if end >= 15 && string(data[end-9:end]) == "LYRICS200" {
			size, err := strconv.Atoi(string(data[end-15 : end-9]))
			if err != nil {
				return nil, nil, errors.New("повреждён Lyrics3")
			}
			next = end - 15 - size
			if next < 0 || !bytes.HasPrefix(data[next:], []byte("LYRICSBEGIN")) {
				return nil, nil, errors.New("повреждён Lyrics3")
			}
		}
		if next == end {
			break
		}
		if next < start || next < 0 || next >= end {
			return nil, nil, errors.New("повреждены хвостовые теги")
		}
		end = next
	}
	if start >= end {
		return nil, nil, errors.New("нет аудио между тегами")
	}
	return data[:start], data[end:], nil
}

// FFmpeg progress is bounded, unlike stderr from a corrupt file.
func runAudio(ctx context.Context, binary string, args []string) (int64, error) {
	cmd := command(ctx, binary, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err = cmd.Start(); err != nil {
		return 0, err
	}
	var micros int64
	scan := bufio.NewScanner(pipe)
	for scan.Scan() {
		if value, ok := strings.CutPrefix(scan.Text(), "out_time_us="); ok {
			if n, e := strconv.ParseInt(value, 10, 64); e == nil {
				micros = n
			}
		}
	}
	scanErr := scan.Err()
	err = cmd.Wait()
	if err != nil {
		detail := stderr.String()
		if len(detail) > 3000 {
			detail = detail[len(detail)-3000:]
		}
		return micros, fmt.Errorf("%w\n%s", err, detail)
	}
	return micros, scanErr
}

func (t tools) convert(ctx context.Context, source, output string) error {
	info, err := t.probe(ctx, source)
	if err != nil {
		return err
	}
	count := 0
	for _, s := range info.Streams {
		if s.Type == "audio" {
			count++
		}
	}
	if count > 1 {
		return errors.New("несколько аудиодорожек: файл требует отдельной обработки для сохранения всех дорожек")
	}
	original, err := info.audio()
	if err != nil {
		return err
	}
	switch original.Codec {
	case "mp3", "mp2", "aac", "vorbis", "opus", "wmav1", "wmav2", "ac3", "eac3", "dca", "speex":
		if original.Bitrate == "" || original.Bitrate == "N/A" {
			original.Bitrate, err = t.packetBitrate(ctx, source)
			if err != nil {
				return err
			}
		}
	}
	codec, err := codecOptions(original)
	if err != nil {
		return err
	}
	temp, err := os.MkdirTemp(filepath.Dir(output), ".fade-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	draft := filepath.Join(temp, "audio"+filepath.Ext(output))
	args := []string{"-nostdin", "-v", "error", "-xerror", "-threads", "1", "-i", source, "-map", "0:a:0", "-map_metadata", "0", "-map_chapters", "0"}
	var prefix, suffix []byte
	if original.Codec == "mp3" && strings.EqualFold(filepath.Ext(source), ".mp3") {
		data, e := os.ReadFile(source)
		if e != nil {
			return e
		}
		prefix, suffix, err = mp3Tags(data)
		if err != nil {
			return err
		}
		args = append(args, "-map_metadata", "-1", "-id3v2_version", "0")
	} else {
		for _, s := range info.Streams {
			if s.Disposition.Cover == 1 {
				args = append(args, "-map", fmt.Sprintf("0:%d", s.Index))
			}
		}
		args = append(args, "-c:v", "copy")
	}
	args = append(args, "-filter_threads", "1", "-af", fadeFilter(original))
	args = append(args, codec...)
	args = append(args, muxerOptions(filepath.Ext(output))...)
	args = append(args, "-progress", "pipe:1", "-stats_period", "3600", draft)
	encoded, err := runAudio(ctx, t.ffmpeg, args)
	if err != nil {
		return err
	}
	if encoded <= 0 {
		return errors.New("кодировщик не создал аудио")
	}
	if len(prefix)+len(suffix) > 0 {
		audio, e := os.ReadFile(draft)
		if e != nil {
			return e
		}
		// Keep private ID3 frames, cover art, ID3v1, APE and Lyrics tags byte for byte.
		tagged := append(append(append([]byte{}, prefix...), audio...), suffix...)
		if err = os.WriteFile(draft, tagged, 0600); err != nil {
			return err
		}
	}
	final, err := t.probe(ctx, draft)
	if err != nil {
		return err
	}
	audio, err := final.audio()
	if err != nil {
		return err
	}
	if audio.Codec != original.Codec || audio.Rate != original.Rate || audio.Channels != original.Channels || audio.Bits != original.Bits || audio.RawBits != original.RawBits {
		return errors.New("при кодировании изменились кодек, частота, число каналов или разрядность")
	}
	decoded, err := runAudio(ctx, t.ffmpeg, []string{"-nostdin", "-v", "error", "-xerror", "-threads", "1", "-i", draft, "-map", "0:a:0", "-af", "asetpts=N/SR/TB", "-progress", "pipe:1", "-stats_period", "3600", "-f", "null", "-"})
	if err != nil {
		return fmt.Errorf("проверка результата: %w", err)
	}
	if decoded <= 0 || abs64(decoded-encoded) > 150000 {
		return errors.New("неожиданная длительность результата")
	}
	return os.Rename(draft, output)
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func fadeFilter(s stream) string {
	filter := "asetpts=N/SR/TB,afade=t=in:st=0:d=4:c=hsin"
	// LAME's CBR/ABR presets attenuate low bitrates. Offset that preset in
	// float before encoding so the requested unchanged level is restored.
	if s.Codec == "mp3" {
		bitrate, _ := strconv.Atoi(s.Bitrate)
		kbps := float64(bitrate / 1000)
		scale := .95
		switch {
		case kbps >= 256:
			scale = 1
		case kbps >= 224:
			scale = .98 + (kbps-224)*.02/32
		case kbps >= 192:
			scale = .97 + (kbps-192)*.01/32
		case kbps > 160:
			scale = .95 + (kbps-160)*.02/32
		}
		if scale < 1 {
			filter += fmt.Sprintf(",volume=%.16g:precision=float", 1/scale)
		}
	}
	return filter
}

func newOutput(folder string) (string, error) {
	for i := 1; ; i++ {
		name := outputName
		if i > 1 {
			name = fmt.Sprintf("%s (%d)", outputName, i)
		}
		path := filepath.Join(folder, name)
		err := os.Mkdir(path, 0700)
		if err == nil {
			return path, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
	}
}

func processFolder(ctx context.Context, folder string, t tools, log io.Writer) (string, int, int, error) {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return "", 0, 0, err
	}
	var inputs []string
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != "" && strings.Contains(" "+extensions+" ", " "+ext+" ") {
			inputs = append(inputs, entry.Name())
		}
	}
	if len(inputs) == 0 {
		return "", 0, 0, errors.New("в этой папке не найдены аудиофайлы")
	}
	output, err := newOutput(folder)
	if err != nil {
		return "", 0, 0, err
	}
	report, err := os.Create(filepath.Join(output, "Отчёт.txt"))
	if err != nil {
		return output, 0, 0, err
	}
	defer report.Close()
	writer := io.MultiWriter(log, report)
	fmt.Fprintf(writer, "Папка: %s\nS-кривая 4 с. Файлов: %d\n", folder, len(inputs))
	var done, failed int
	for i, name := range inputs {
		if err = ctx.Err(); err != nil {
			return output, done, failed, err
		}
		fmt.Fprintf(writer, "\n[%d/%d] %s\n", i+1, len(inputs), name)
		err = t.convert(ctx, filepath.Join(folder, name), filepath.Join(output, name))
		if err != nil {
			if ctx.Err() != nil {
				return output, done, failed, ctx.Err()
			}
			failed++
			fmt.Fprintf(writer, "ОШИБКА: %s\n", err)
		} else {
			done++
			fmt.Fprintln(writer, "Готово")
		}
	}
	fmt.Fprintf(writer, "\nОбработано: %d. Ошибок: %d.\nРезультаты: %s\n", done, failed, output)
	return output, done, failed, nil
}

var applicationMode = "fade"

func run() error {
	if applicationMode == "check" {
		return runCheck()
	}
	return runFade(os.Args[1:])
}

var errCancelled = errors.New("выбор отменён")

func main() {
	if applicationMode == "fade" {
		if handled, err := updateCommand(os.Args[1:]); handled {
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		if len(os.Args) == 1 {
			if target, err := cachedUpdateTarget(); err == nil && target != "" {
				if err = startUpdatedApp(target); err == nil {
					return
				}
			}
		}
	}
	configureConsole()
	if len(os.Args) == 1 {
		acquired, err := acquireGUIInstance()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		if !acquired {
			return
		}
		defer releaseGUIInstance()
	}
	err := run()
	if applicationMode != "check" && len(os.Args) > 1 {
		if err != nil {
			fmt.Fprintln(os.Stderr, "ОШИБКА:", err)
		}
		if errors.Is(err, errCancelled) || errors.Is(err, context.Canceled) {
			os.Exit(2)
		}
		if err != nil {
			os.Exit(1)
		}
		return
	}
	if errors.Is(err, errCancelled) {
		return
	} else if err != nil {
		fmt.Fprintln(os.Stderr, "\nОШИБКА:", err)
	}
	if applicationMode == "check" && len(os.Args) > 1 {
		return
	}
	fmt.Println("\nНажмите Enter, чтобы закрыть окно.")
	bufio.NewReader(os.Stdin).ReadString('\n')
}
