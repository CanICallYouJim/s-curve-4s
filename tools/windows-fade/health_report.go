package main

import (
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var healthKindNames = map[string]string{
	"unreadable":        "Не удалось прочитать запись",
	"decode_error":      "Ошибка при чтении звука",
	"packet_gap":        "Участок требует прослушивания",
	"decoder_warning":   "Замечания к файлу",
	"duration_mismatch": "Длительность не совпадает",
	"silence_gap":       "Возможный провал звука",
	"repeated_audio":    "Возможный повтор звука",
	"clipping":          "Возможный хрип на громких местах",
	"signal_limit":      "Замечаний много — показаны не все",
	"timeline_limit":    "Не все места удалось отметить",
	"read_error":        "Не удалось полностью прочитать файл",
	"file_changed":      "Файл изменился во время проверки",
	"analysis_failed":   "Проверка не завершена",
	"not_checked":       "Не проверено",
	"decoder_log":       "Диагностика декодера",
}

func healthKind(kind string) string {
	if name, ok := healthKindNames[kind]; ok {
		return name
	}
	return kind
}

// These explanations describe the finding without changing its diagnosis.
func explainHealthIssue(issue healthIssue, duration float64) (string, string) {
	switch issue.Kind {
	case "unreadable":
		return "Программа не смогла открыть запись или прочитать звук.", "Попробуйте открыть её в своём плеере. Если она тоже не играет, найдите другую копию."
	case "decode_error":
		if issue.Start == nil {
			return "При чтении звука возникла ошибка. Точное место определить не удалось.", "Прослушайте запись целиком. Если слышно заедание или обрыв, замените запись другой копией."
		}
		return "При чтении звука возникла ошибка. В этом месте может быть сбой, но по одному сообщению нельзя понять, слышен ли он.", "Прослушайте отмеченное место. Если слышно заедание или обрыв, замените запись другой копией."
	case "packet_gap":
		if issue.Start != nil && duration > 0 && *issue.Start >= duration-1 {
			return "Программа заметила особенность в конце файла. Она не обязательно влияет на звук.", "Проверьте последние секунды: не обрывается ли музыка раньше времени."
		}
		return "Часть файла не удалось прочитать как звук. Это ещё не означает, что музыка повреждена.", "Прослушайте отмеченное место: нет ли пропуска, щелчка или заедания."
	case "decoder_warning":
		return "При чтении записи возникло предупреждение о возможной проблеме со звуком.", "Прослушайте запись: нет ли пропуска, треска или обрыва."
	case "duration_mismatch":
		return "Длительность, указанная в файле, отличается от прочитанного звука. Причиной могут быть служебные данные или обрезанный конец.", "Проверьте начало и конец записи: вся ли музыка на месте."
	case "silence_gap":
		return "Найдена короткая тишина внутри записи. Она может быть музыкальной паузой.", "Прослушайте это место: пауза звучит естественно или звук внезапно пропадает?"
	case "repeated_audio":
		return "Найдено почти одинаковое повторение звука. Оно может быть частью музыки.", "Прослушайте это место: нет ли неестественного заедания."
	case "clipping":
		return "На громких местах возможен хрип или треск. Это может быть особенностью исходной записи.", "Прослушайте отмеченное место на обычной громкости. Если искажение мешает, поищите другую копию."
	case "signal_limit", "timeline_limit":
		return "Запись проверялась до конца, но список отмеченных мест ограничен.", "В подробностях сохранено, какие сведения не вошли в список. Учитывайте это при прослушивании."
	case "file_changed":
		return "Во время проверки файл изменился, поэтому результат нельзя считать окончательным.", "Дождитесь окончания копирования или редактирования и запустите проверку ещё раз."
	case "read_error":
		return "Не удалось прочитать файл целиком. Возможно, он недоступен или возникла ошибка накопителя.", "Убедитесь, что файл доступен, и запустите проверку ещё раз. Если ошибка повторяется, попробуйте скопировать его на другой накопитель."
	case "analysis_failed", "not_checked":
		return "Программа не закончила проверку этой записи. Пока неизвестно, всё ли с ней в порядке.", "Запустите проверку ещё раз — готовые результаты будут использованы повторно."
	default:
		return "Есть замечание, которое нужно проверить отдельно.", "Прослушайте запись. Исходное сообщение сохранено в подробностях для специалиста."
	}
}

func healthStatus(result healthResult) string {
	result = audioOnlyResult(result)
	if !result.Complete {
		return "incomplete"
	}
	status := "clean"
	for _, issue := range result.Issues {
		if issue.Severity == "error" {
			return "error"
		}
		if issue.Severity == "warning" {
			status = "warning"
		}
	}
	return status
}

func statusTitle(status string) string {
	return map[string]string{"clean": "Без найденных проблем", "warning": "Стоит прослушать", "error": "Требует внимания", "incomplete": "Проверка не закончена"}[status]
}

func timeCode(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	minutes := int(seconds) / 60
	return fmt.Sprintf("%d:%05.2f", minutes, seconds-float64(minutes*60))
}

type kindCount struct {
	Kind, Name string
	Count      int
}
type reportStats struct {
	Total, Checked, Fresh, Cached, Errors, Warnings, Clean, Incomplete int
	Kinds                                                              []kindCount
}

func healthStats(report healthReport) reportStats {
	stats := reportStats{Total: len(report.Tracks), Fresh: report.Fresh, Cached: report.Cached, Checked: report.Fresh + report.Cached}
	counts := make(map[string]int)
	for _, track := range report.Tracks {
		switch healthStatus(track.Result) {
		case "error":
			stats.Errors++
		case "warning":
			stats.Warnings++
		case "clean":
			stats.Clean++
		default:
			stats.Incomplete++
		}
		seen := make(map[string]bool)
		for _, issue := range audioOnlyResult(track.Result).Issues {
			if issue.Severity != "info" && !seen[issue.Kind] {
				counts[issue.Kind]++
				seen[issue.Kind] = true
			}
		}
	}
	for kind, count := range counts {
		stats.Kinds = append(stats.Kinds, kindCount{kind, healthKind(kind), count})
	}
	sort.Slice(stats.Kinds, func(i, j int) bool { return stats.Kinds[i].Name < stats.Kinds[j].Name })
	return stats
}

type reportIssue struct {
	healthIssue
	Title, Time         string
	Explanation, Action string
	Visible             bool
	ListenStart         float64
	Preview             healthPreview
	PreviewRange        string
	PreviewID           string
	PreviewError        string
}
type reportTrack struct {
	PreviewError string
	healthTrack
	Status, StatusTitle string
	Name, FullPath      string
	Preview             healthPreview
	PreviewRange        string
	PreviewID           string
	Details             []reportIssue
}

type embeddedHealthPreview struct{ ID, Data string }

type reportView struct {
	Clips []embeddedHealthPreview
	healthReport
	RootName, FinishedText string
	Stats                  reportStats
	Rows                   []reportTrack
	PlayerScript           template.JS
	PlaybackControls       template.HTML
}

func saveHealthReport(report healthReport) (string, error) {
	return writeHealthReport(context.Background(), report, nil)
}

func saveHealthReportWithPreviews(ctx context.Context, report healthReport, engines tools) (string, error) {
	return writeHealthReport(ctx, report, &engines)
}

func writeHealthReport(ctx context.Context, report healthReport, engines *tools) (string, error) {
	view := reportView{healthReport: report, Stats: healthStats(report), PlayerScript: template.JS(healthPlayerJS), PlaybackControls: template.HTML(healthPlaybackControls)}
	view.RootName = filepath.Base(report.Root)
	view.FinishedText = report.Finished
	if finished, err := time.Parse(time.RFC3339, report.Finished); err == nil {
		view.FinishedText = finished.Format("02.01.2006 в 15:04")
	}
	ids := make(map[string]string)
	registerPreview := func(track healthTrack, preview healthPreview) string {
		key := fmt.Sprintf("%s/%.6f/%.6f", track.SHA256, preview.Start, preview.End)
		if id, ok := ids[key]; ok {
			return id
		}
		id := fmt.Sprintf("preview-%d", len(view.Clips))
		ids[key] = id
		view.Clips = append(view.Clips, embeddedHealthPreview{ID: id, Data: preview.Data})
		return id
	}
	for _, track := range report.Tracks {
		track.Result = audioOnlyResult(track.Result)
		status := healthStatus(track.Result)
		row := reportTrack{healthTrack: track, Status: status, StatusTitle: statusTitle(status), Name: filepath.Base(strings.ReplaceAll(track.Path, "\\", "/")), FullPath: filepath.Join(report.Root, track.Path)}
		for _, issue := range track.Result.Issues {
			detail := reportIssue{healthIssue: issue, Title: healthKind(issue.Kind), Visible: issue.Severity != "info" && issue.Kind != "decoder_log"}
			detail.Explanation, detail.Action = explainHealthIssue(issue, track.Result.Duration)
			if issue.Start != nil {
				detail.Time = timeCode(*issue.Start)
				detail.ListenStart = max(0, *issue.Start-1.5)
				if issue.End != nil {
					detail.Time += "–" + timeCode(*issue.End)
				}
			}
			if engines != nil && status != "clean" && status != "incomplete" && issue.Start != nil {
				preview, err := engines.healthPreview(ctx, report.Root, track, *issue.Start)
				if err == nil {
					detail.Preview = preview
					detail.PreviewRange = timeCode(preview.Start) + "–" + timeCode(preview.End)
					detail.PreviewID = registerPreview(track, preview)
				} else {
					detail.PreviewError = err.Error()
				}
			}
			row.Details = append(row.Details, detail)
		}
		for _, detail := range row.Details {
			if detail.Preview.Data != "" {
				row.Preview = detail.Preview
				row.PreviewRange = detail.PreviewRange
				row.PreviewID = detail.PreviewID
				break
			}
		}
		if engines != nil && status != "clean" && status != "incomplete" && row.Preview.Data == "" {
			preview, err := engines.healthPreview(ctx, report.Root, track, 0)
			if err == nil {
				row.Preview = preview
				row.PreviewRange = timeCode(preview.Start) + "–" + timeCode(preview.End)
				row.PreviewID = registerPreview(track, preview)
			} else {
				row.PreviewError = err.Error()
			}
		}
		view.Rows = append(view.Rows, row)
	}
	// Stable sort keeps alphabetical order within each severity group.
	ranks := map[string]int{"error": 0, "warning": 1, "incomplete": 2, "clean": 3}
	sort.SliceStable(view.Rows, func(i, j int) bool { return ranks[view.Rows[i].Status] < ranks[view.Rows[j].Status] })
	output := filepath.Join(report.Root, checkFolder)
	if err := plainCheckDir(output); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(output, ".report-*.html")
	if err != nil {
		return "", err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err = healthTemplate.Execute(file, view); err != nil {
		file.Close()
		return "", err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	name := "Отчёт — " + time.Now().Format("2006-01-02 15-04-05.000000000") + ".html"
	path := filepath.Join(output, name)
	if err = os.Rename(temp, path); err != nil {
		return "", err
	}
	return path, nil
}

//go:embed health_player.js
var healthPlayerJS string

const healthPlaybackControls = `<section class="access" aria-label="Доступ к музыке">
<button type="button" id="chooseMusic">Выбрать папку с музыкой</button>
<input type="file" id="musicFiles" webkitdirectory directory multiple hidden>
<p id="accessStatus" role="status">Короткие фрагменты можно слушать сразу. Чтобы слушать записи целиком, выберите папку с музыкой, указанную в начале отчёта.</p>
</section>`

var healthTemplate = template.Must(template.New("report").Funcs(template.FuncMap{"join": strings.Join}).Parse(`<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Проверка музыки</title>
<style>
:root{font:16px system-ui,sans-serif;color:#edf5f5;background:#15232d;color-scheme:dark}*{box-sizing:border-box}body{max-width:1040px;margin:auto;padding:28px 20px 110px}h1{font-size:2rem;margin:0 0 12px}h2{font-size:1.2rem}p{line-height:1.55;color:#c1d1d6}.path{overflow-wrap:anywhere}.summary{display:flex;gap:12px;flex-wrap:wrap}.summary p{background:#203541;padding:14px;margin:0;flex:1;min-width:145px}.summary b{display:block;color:#fff;font-size:1.7rem}table{width:100%;border-collapse:collapse}th,td{text-align:left;padding:9px 0;border-bottom:1px solid #38505d}td:last-child{text-align:right}details.track{border:1px solid #38505d;border-radius:8px;padding:14px;margin:12px 0;background:#1b2d38}summary{cursor:pointer;overflow-wrap:anywhere}.status{display:block;font-size:.85rem;margin-top:5px;color:#8eddce}.error .status{color:#ffb6a3}.warning .status,.incomplete .status{color:#efd69c}.meta{font-size:.85rem}code,pre{font-family:ui-monospace,monospace}pre{white-space:pre-wrap;overflow-wrap:anywhere;font-size:.8rem}li{margin:12px 0;line-height:1.5}button,input,select{font:inherit;padding:8px;border:1px solid #547581;border-radius:5px}button{cursor:pointer;background:#234b55;color:#b6ffed}input{min-width:200px;flex:1}label{display:flex;align-items:center;gap:8px}.filters{display:flex;gap:10px;margin:24px 0;flex-wrap:wrap}footer{position:fixed;bottom:0;left:0;right:0;background:#0d1a22;padding:10px 20px;border-top:1px solid #38505d}footer>div{max-width:1000px;margin:auto;display:flex;align-items:center;gap:14px}audio{width:360px;max-width:50vw;height:36px}#playing{font-size:.85rem;overflow-wrap:anywhere}a{color:#8ef2dc}.access{margin:24px 0;padding:16px;border:1px solid #547581;border-radius:8px}.access p{margin-bottom:0}.notice{border-left:3px solid #efd69c;padding-left:14px}[hidden]{display:none!important}
</style></head><body>
<h1>Проверка музыки</h1><p>{{.RootName}}</p>
<details class="technical"><summary>Подробности для специалиста — папка</summary><p class="path">{{.Root}}</p></details>
<p>Завершено {{.FinishedText}} · {{if .Recursive}}включая подпапки{{else}}только выбранная папка{{end}}</p>
{{if .Interrupted}}<p class="notice">Проверка остановлена. Завершённые результаты сохранены. Остальные записи перечислены как не проверенные полностью.</p>{{end}}
<div class="summary"><p><b>{{.Stats.Checked}}</b>Проверено</p><p><b>{{.Stats.Errors}}</b>Требует внимания</p><p><b>{{.Stats.Warnings}}</b>Стоит прослушать</p><p><b>{{.Stats.Clean}}</b>Без найденных проблем</p></div>
<p>Всего найдено {{.Stats.Total}}. Проверено заново {{.Stats.Fresh}}, использован прошлый результат для {{.Stats.Cached}}. Проверка не закончена для {{.Stats.Incomplete}}.</p>
<p>Начните с записей, которые требуют внимания: откройте их и прослушайте отмеченные места. Предупреждение не всегда означает поломку, а проверка не может найти все слышимые дефекты.</p>
{{if .Notices}}<p class="notice">Во время проверки возникли дополнительные замечания. Подробности сохранены ниже.</p><details><summary>Подробности для специалиста — ход проверки</summary>{{range .Notices}}<pre>{{.}}</pre>{{end}}</details>{{end}}
{{if .Stats.Kinds}}<h2>Что заметила программа</h2><table><thead><tr><th>Тип</th><th>Записей</th></tr></thead><tbody>{{range .Stats.Kinds}}<tr><td>{{.Name}}</td><td>{{.Count}}</td></tr>{{end}}</tbody></table><p class="meta">Одна запись может иметь несколько типов проблем. Каждый тип считается по числу файлов.</p>{{end}}
{{.PlaybackControls}}
<div class="filters"><input id="search" type="search" placeholder="Найти по имени файла" aria-label="Поиск по имени"><label>Показать <select id="filter"><option value="problems">Требующие проверки</option><option value="all">Все записи</option><option value="error">Требует внимания</option><option value="warning">Стоит прослушать</option><option value="incomplete">Не проверенные полностью</option><option value="clean">Без обнаруженных проблем</option></select></label></div>
{{range .Rows}}<details class="track {{.Status}}" data-status="{{.Status}}" data-path="{{.Path}}"><summary>{{.Name}}<span class="status">{{.StatusTitle}}</span></summary>
{{if .Cached}}<p class="meta">Файл не изменился — использован прошлый результат.</p>{{end}}
{{if eq .Status "incomplete"}}<p>Проверка этой записи не закончена. Запустите её ещё раз, чтобы получить результат.</p>{{end}}
{{if .Details}}<ul>{{$path:=.Path}}{{range .Details}}{{if .Visible}}<li><strong>{{.Title}}</strong> {{if .Time}}<button type="button" data-audio-path="{{$path}}" data-start="{{.ListenStart}}" {{if .Preview.Data}}data-preview-id="{{.PreviewID}}" data-preview-start="{{.Preview.Start}}" data-preview-range="{{.PreviewRange}}"{{end}}>{{.Time}}</button>{{end}}<p>{{.Explanation}} {{.Action}}</p>{{if .PreviewError}}<p class="notice">Встроенный фрагмент недоступен. Чтобы прослушать запись, выберите папку с музыкой.</p>{{end}}</li>{{end}}{{end}}</ul>{{end}}
{{if .PreviewError}}<p class="notice">Встроенный фрагмент недоступен. Чтобы прослушать запись, выберите папку с музыкой.</p>{{end}}
<p>{{if .Preview.Data}}<button type="button" data-audio-path="{{.Path}}" data-start="{{.Preview.Start}}" data-preview-id="{{.PreviewID}}" data-preview-start="{{.Preview.Start}}" data-preview-range="{{.PreviewRange}}">Прослушать фрагмент {{.PreviewRange}}</button> {{end}}<button type="button" data-audio-path="{{.Path}}" data-start="0">Слушать целиком</button></p>
<details class="technical"><summary>Подробности для специалиста</summary>
<p class="meta path">Файл: {{.FullPath}}</p>
{{if .Result.Complete}}<p class="meta">Проверка звука {{.Result.CheckedAt}}. Длительность {{printf "%.2f" .Result.Duration}} с.</p>{{end}}
{{if .SHA256}}<p class="meta path">SHA‑256 <code>{{.SHA256}}</code></p>{{end}}
{{range .Details}}<p>{{.Kind}} · {{.Severity}}{{if .Time}} · {{.Time}}{{end}}</p><pre>{{.Detail}}</pre>{{if .PreviewError}}<pre>{{.PreviewError}}</pre>{{end}}{{end}}
{{if .PreviewError}}<pre>{{.PreviewError}}</pre>{{end}}
</details></details>{{end}}
<p id="empty" hidden>Нет записей, соответствующих фильтру.</p>
<footer><div><audio id="player" controls preload="none"></audio><span id="playing">Нажмите таймкод или «Прослушать фрагмент». Для полной записи используйте «Слушать целиком».</span></div></footer>
{{range .Clips}}<span id="{{.ID}}" hidden>{{.Data}}</span>{{end}}
<script>{{.PlayerScript}}</script></body></html>`))
