package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFolderSessionContinuesAfterRunAndStopsOnExit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		runs  []error
		count int
	}{
		{"finish, interruption, and error", "\r\n\r\n\r\nq\r\n", []error{nil, context.Canceled, errors.New("batch failed"), nil}, 4},
		{"empty input", "", []error{nil}, 1},
		{"picker Cancel", "\r\n\r\n", []error{nil, errCancelled}, 2},
		{"case-insensitive exit after an unknown answer", "other\r\nQ\r\n", []error{nil}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := 0
			var output, diagnostics bytes.Buffer
			runFolderSession(func() error {
				if count >= len(tc.runs) {
					t.Fatal("unexpected extra folder selection")
				}
				result := tc.runs[count]
				count++
				return result
			}, strings.NewReader(tc.input), &output, &diagnostics)
			if count != tc.count {
				t.Fatalf("ran %d selections, want %d", count, tc.count)
			}
			if tc.name == "finish, interruption, and error" && !strings.Contains(diagnostics.String(), "batch failed") {
				t.Fatal("batch failure is missing from the console")
			}
		})
	}
}
