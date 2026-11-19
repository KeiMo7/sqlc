package diagnostic

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sqlc-dev/sqlc/internal/multierr"
)

func TestDisabledOutputMatchesLegacyLine(t *testing.T) {
	err := &multierr.FileError{
		Filename:   "/tmp/project/query.sql",
		Line:       4,
		Column:     2,
		SourceText: "SELECT 1;\n",
		Err:        errors.New("boom"),
	}
	var out bytes.Buffer
	FileError(&out, "/tmp/project", err, Config{Enabled: false})
	if got, want := out.String(), "query.sql:4:2: boom\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestNoLocationIsExplicit(t *testing.T) {
	err := &multierr.FileError{
		Filename: "/tmp/project/query.sql",
		Line:     1,
		Column:   1,
		Err:      errors.New("codegen failed"),
	}
	var out bytes.Buffer
	FileError(&out, "/tmp/project", err, Config{Enabled: true, Format: FormatText})
	text := out.String()
	if !strings.Contains(text, "location: no location information") {
		t.Fatalf("missing no-location note:\n%s", text)
	}
	if strings.Contains(text, "location: query.sql:1:1") {
		t.Fatalf("invented a location:\n%s", text)
	}
}

func TestJSONReport(t *testing.T) {
	err := &multierr.FileError{
		Filename:   "/tmp/project/query.sql",
		Line:       2,
		Column:     8,
		Offset:     7,
		HasOffset:  true,
		EndOffset:  13,
		Kind:       "semantic",
		Object:     "column",
		Symbol:     "usr_id",
		SourceText: "SELECT usr_id FROM users;\n",
		Err:        errors.New("column does not exist"),
	}
	var out bytes.Buffer
	FileError(&out, "/tmp/project", err, Config{Enabled: true, Format: FormatJSON})
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want legacy + JSON: %q", len(lines), out.String())
	}
	var report Report
	if err := json.Unmarshal([]byte(lines[1]), &report); err != nil {
		t.Fatal(err)
	}
	if !report.HasLocation || report.Line != 1 || report.Column != 8 || report.ByteOffset != 7 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Snippet != "SELECT usr_id FROM users;" {
		t.Fatalf("snippet = %q", report.Snippet)
	}
}
