package main

import (
	"regexp"
	"strconv"
	"strings"
)

var textFrameError = regexp.MustCompile(`^Error reading frame [A-Z0-9]{3,4}, skipped$`)
var textEncodingError = regexp.MustCompile(`^Unknown encoding [0-9]+$`)

func diagnosticMessage(line string) string {
	line = strings.TrimSpace(line)
	for strings.HasPrefix(line, "[") {
		end := strings.IndexByte(line, ']')
		if end < 0 {
			break
		}
		line = strings.TrimSpace(line[end+1:])
	}
	return line
}

func textMetadataMessage(line string) bool {
	message := diagnosticMessage(line)
	if textFrameError.MatchString(message) {
		return true
	}
	if textEncodingError.MatchString(message) {
		trimmed := strings.TrimSpace(line)
		value, err := strconv.Atoi(strings.TrimPrefix(message, "Unknown encoding "))
		knownSource := trimmed == message || strings.HasPrefix(trimmed, "[mp3") || strings.HasPrefix(trimmed, "[warning]") || strings.HasPrefix(trimmed, "[error]")
		if knownSource && err == nil && value >= 0 && value <= 255 {
			return true
		}
	}
	switch message {
	case "Incorrect BOM value", "Cannot read BOM value, input too short", "Error reading comment frame, skipped", "Error reading lyrics, skipped":
		return true
	}
	return false
}

func onlyTextMetadata(detail string) bool {
	if strings.Contains(detail, "…") {
		return false
	}
	count := 0
	for _, line := range strings.Split(detail, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		count++
		if !textMetadataMessage(line) {
			return false
		}
	}
	return count > 0 && count < 32
}

// Copy cached verdicts: harmless text tags remain available in technical details.
func audioOnlyResult(result healthResult) healthResult {
	result.Issues = append([]healthIssue(nil), result.Issues...)
	for i := range result.Issues {
		issue := &result.Issues[i]
		if issue.Kind == "timeline_limit" || issue.Kind == "signal_limit" || (issue.Kind == "decoder_warning" && onlyTextMetadata(issue.Detail)) {
			issue.Severity = "info"
		}
	}
	return result
}

// Old EOF findings need fresh byte-position/padding evidence, never a time-only downgrade.
func needsAudioPolicyReview(result healthResult) bool {
	if result.AudioPolicy >= audioOnlyPolicyVersion || result.Duration <= 0 {
		return false
	}
	hasTextLog, hasError := false, false
	for _, issue := range result.Issues {
		if issue.Kind == "decoder_log" && onlyTextMetadata(issue.Detail) {
			hasTextLog = true
		}
		if issue.Kind == "decode_error" && issue.Severity == "error" {
			hasError = true
		}
	}
	if hasTextLog && hasError {
		return true
	}
	for _, issue := range result.Issues {
		if issue.Severity != "info" && (issue.Kind == "packet_gap" || issue.Kind == "decode_error") && issue.Start != nil && *issue.Start >= result.Duration-.001 {
			return true
		}
	}
	return false
}
