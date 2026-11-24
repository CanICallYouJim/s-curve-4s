package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

// A preview is independent MP3 media. Start and End refer to the source track.
type healthPreview struct {
	Data       string
	Start, End float64
}

const maxHealthPreviewBytes = 2 * 1024 * 1024

func healthPreviewRange(duration, target float64) (float64, float64, error) {
	if math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 || math.IsNaN(target) || math.IsInf(target, 0) {
		return 0, 0, errors.New("неверная длительность или таймкод фрагмента")
	}
	target = min(duration, max(0, target))
	start, end := max(0, target-5), min(duration, target+4)
	if target == 0 {
		end = min(duration, 8)
	}
	if end-start <= 0 {
		return 0, 0, errors.New("пустой фрагмент")
	}
	return start, end, nil
}

func readHealthPreview(path string, start, end float64) (healthPreview, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxHealthPreviewBytes {
		return healthPreview{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > maxHealthPreviewBytes {
		return healthPreview{}, false
	}
	var preview healthPreview
	if json.Unmarshal(data, &preview) != nil || preview.Start != start || preview.End != end {
		return healthPreview{}, false
	}
	media, err := base64.StdEncoding.DecodeString(preview.Data)
	if err != nil || len(media) < 4 || len(media) > maxHealthPreviewBytes || media[0] != 0xff || media[1]&0xe0 != 0xe0 {
		return healthPreview{}, false
	}
	return preview, true
}

func (t tools) healthPreview(ctx context.Context, root string, track healthTrack, target float64) (healthPreview, error) {
	if !filepath.IsLocal(track.Path) {
		return healthPreview{}, errors.New("путь аудиофайла выходит за пределы библиотеки")
	}
	if len(track.SHA256) != 64 {
		return healthPreview{}, errors.New("отсутствует SHA-256 исходной записи")
	}
	if _, err := hex.DecodeString(track.SHA256); err != nil {
		return healthPreview{}, errors.New("неверный SHA-256 исходной записи")
	}
	start, end, err := healthPreviewRange(track.Result.Duration, target)
	if err != nil {
		return healthPreview{}, err
	}
	output := filepath.Join(root, checkFolder)
	if err = plainCheckDir(output); err != nil {
		return healthPreview{}, err
	}
	cache := filepath.Join(output, "previews")
	if err = plainCheckDir(cache); err != nil {
		return healthPreview{}, err
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("mp3-256-preview-2|%s|%.9f|%.9f", track.SHA256, start, end)))
	cacheFile := filepath.Join(cache, hex.EncodeToString(key[:])+".json")
	if preview, ok := readHealthPreview(cacheFile, start, end); ok {
		return preview, nil
	}
	if err = ctx.Err(); err != nil {
		return healthPreview{}, err
	}
	source := filepath.Join(root, track.Path)
	before, err := fileFingerprint(ctx, source)
	if err != nil {
		return healthPreview{}, err
	}
	if before.Digest != track.SHA256 {
		return healthPreview{}, errors.New("исходник изменился после проверки; фрагмент не создан")
	}
	temp, err := os.MkdirTemp(cache, ".preview-")
	if err != nil {
		return healthPreview{}, err
	}
	defer os.RemoveAll(temp)
	draft := filepath.Join(temp, "audio.mp3")
	decoded := filepath.Join(temp, "decoded.nut")
	length := end - start
	// Broken codec frames may change sample properties and reset filters.
	// First create stable PCM with the original timestamps; NUT retains the
	// sample-rate time base. Recoverable source errors are allowed in this pass.
	_, err = runAudio(ctx, t.ffmpeg, []string{
		"-nostdin", "-v", "error", "-threads", "1", "-i", source,
		"-ss", fmt.Sprintf("%.9f", start), "-t", fmt.Sprintf("%.9f", length),
		"-map", "0:a:0", "-vn", "-map_metadata", "-1", "-map_chapters", "-1",
		"-filter_threads", "1", "-c:a", "pcm_f32le", "-ac", "2", "-ar", "44100",
		"-f", "nut", "-progress", "pipe:1", "-stats_period", "3600", decoded,
	})
	if err != nil {
		return healthPreview{}, fmt.Errorf("декодирование фрагмента: %w", err)
	}
	pcmInfo, err := os.Stat(decoded)
	if err != nil || pcmInfo.Size() <= 0 || pcmInfo.Size() > 16*1024*1024 {
		return healthPreview{}, errors.New("недопустимый размер промежуточного фрагмента")
	}
	// Fill missing timestamp intervals with silence instead of collapsing the
	// timeline. This is a listening preview, not a repair of the source audio.
	filter := fmt.Sprintf("aresample=async=1:first_pts=0,apad=whole_dur=%.9f,atrim=duration=%.9f", length, length)
	_, err = runAudio(ctx, t.ffmpeg, []string{
		"-nostdin", "-v", "error", "-xerror", "-threads", "1", "-i", decoded,
		"-map", "0:a:0", "-vn", "-map_metadata", "-1", "-map_chapters", "-1",
		"-filter_threads", "1", "-af", filter, "-c:a", "libmp3lame", "-b:a", "256k", "-ac", "2", "-ar", "44100",
		"-id3v2_version", "0", "-write_id3v1", "0", "-progress", "pipe:1", "-stats_period", "3600", draft,
	})
	if err != nil {
		return healthPreview{}, fmt.Errorf("создание фрагмента: %w", err)
	}
	micros, err := runAudio(ctx, t.ffmpeg, []string{
		"-nostdin", "-v", "error", "-xerror", "-threads", "1", "-i", draft,
		"-map", "0:a:0", "-af", "asetpts=N/SR/TB", "-progress", "pipe:1", "-stats_period", "3600", "-f", "null", "-",
	})
	if err != nil || micros <= 0 || micros > 12_000_000 || math.Abs(float64(micros)/1e6-(end-start)) > .15 {
		return healthPreview{}, fmt.Errorf("не удалось проверить длительность фрагмента: %.6f с, ошибка %v", float64(micros)/1e6, err)
	}
	after, err := fileFingerprint(ctx, source)
	if err != nil || after.Digest != before.Digest || !os.SameFile(before.Info, after.Info) {
		return healthPreview{}, errors.New("исходник изменился во время создания фрагмента")
	}
	info, err := os.Stat(draft)
	if err != nil || info.Size() <= 0 || info.Size() > maxHealthPreviewBytes {
		return healthPreview{}, errors.New("недопустимый размер фрагмента")
	}
	media, err := os.ReadFile(draft)
	if err != nil {
		return healthPreview{}, err
	}
	preview := healthPreview{Data: base64.StdEncoding.EncodeToString(media), Start: start, End: end}
	data, err := json.Marshal(preview)
	if err != nil {
		return healthPreview{}, err
	}
	file, err := os.CreateTemp(cache, ".preview-cache-*.tmp")
	if err != nil {
		return healthPreview{}, err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err != nil {
		file.Close()
		return healthPreview{}, err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return healthPreview{}, err
	}
	if err = file.Close(); err != nil {
		return healthPreview{}, err
	}
	if err = os.Rename(name, cacheFile); err != nil {
		return healthPreview{}, err
	}
	return preview, nil
}
