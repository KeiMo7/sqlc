// Package diagnostic renders optional, location-aware SQL diagnostics.
//
// Rendering is append-only: callers first print the legacy one-line error,
// then this package adds exact location, snippet, suggestions and related
// locations. With Config.Enabled false the renderer prints exactly the
// legacy line.
package diagnostic

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sqlc-dev/sqlc/internal/multierr"
	"github.com/sqlc-dev/sqlc/internal/source"
)

const (
	FormatText = "text"
	FormatJSON = "json"
)

// Config controls optional diagnostic output.
type Config struct {
	Enabled bool
	Format  string
}

// Report is the machine-readable form of one diagnostic. Byte offsets are
// 0-based, lines and columns are 1-based byte coordinates.
type Report struct {
	Version       int                `json:"version"`
	File          string             `json:"file,omitempty"`
	Line          int                `json:"line,omitempty"`
	Column        int                `json:"column,omitempty"`
	ByteOffset    int                `json:"byte_offset"`
	EndByteOffset int                `json:"end_byte_offset,omitempty"`
	HasLocation   bool               `json:"has_location"`
	Kind          string             `json:"kind,omitempty"`
	Object        string             `json:"object,omitempty"`
	Symbol        string             `json:"symbol,omitempty"`
	Message       string             `json:"message"`
	Snippet       string             `json:"snippet,omitempty"`
	Candidates    []string           `json:"candidates,omitempty"`
	Suggestions   []source.Candidate `json:"suggestions,omitempty"`
	Related       []RelatedLocation  `json:"related,omitempty"`
	Note          string             `json:"note,omitempty"`
}

// RelatedLocation is a secondary source location.
type RelatedLocation struct {
	File       string `json:"file"`
	Line       int    `json:"line,omitempty"`
	Column     int    `json:"column,omitempty"`
	ByteOffset int    `json:"byte_offset"`
	Message    string `json:"message,omitempty"`
}

// FileError renders a collected file error. The first line keeps the existing
// path:line:column: message format. Additional detail is appended only when
// diagnostics are enabled.
func FileError(w io.Writer, dir string, fe *multierr.FileError, cfg Config) {
	path := displayPath(dir, fe.Filename)
	fmt.Fprintf(w, "%s:%d:%d: %s\n", path, fe.Line, fe.Column, fe.Err)
	if !cfg.Enabled {
		return
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			fmt.Fprintf(w, "  diagnostic error: %v; original error preserved: %s\n", recovered, fe.Err)
		}
	}()

	report := reportFromFileError(dir, fe)
	if normalizeFormat(cfg.Format) == FormatJSON {
		blob, err := json.Marshal(report)
		if err != nil {
			fmt.Fprintf(w, "  diagnostic error: %v; original error preserved: %s\n", err, fe.Err)
			return
		}
		fmt.Fprintf(w, "%s\n", blob)
		return
	}
	writeText(w, report)
}

// NoLocation renders an error that did not originate from a parse or analysis
// location, for example a code generation failure. It never invents a line or
// column.
func NoLocation(w io.Writer, dir, filename, message string, cfg Config) {
	if !cfg.Enabled {
		return
	}
	path := displayPath(dir, filename)
	report := Report{
		Version:     1,
		File:        path,
		Message:     message,
		HasLocation: false,
	}
	if normalizeFormat(cfg.Format) == FormatJSON {
		blob, err := json.Marshal(report)
		if err != nil {
			fmt.Fprintf(w, "  diagnostic error: %v; original error preserved: %s\n", err, message)
			return
		}
		fmt.Fprintf(w, "%s\n", blob)
		return
	}
	fmt.Fprintln(w, "  location: no location information")
}

func reportFromFileError(dir string, fe *multierr.FileError) Report {
	report := Report{
		Version:     1,
		File:        displayPath(dir, fe.Filename),
		Message:     fe.Err.Error(),
		Kind:        fe.Kind,
		Object:      fe.Object,
		Symbol:      fe.Symbol,
		Candidates:  append([]string(nil), fe.Candidates...),
		HasLocation: fe.HasOffset,
	}
	sort.Strings(report.Candidates)
	if fe.HasOffset {
		pos := source.LineColumn(fe.SourceText, fe.Offset)
		report.Line = pos.Line
		report.Column = pos.Column
		report.ByteOffset = fe.Offset
		if fe.EndOffset > fe.Offset {
			report.EndByteOffset = fe.EndOffset
		}
		if fe.SourceText != "" {
			line, _ := source.LineAt(fe.SourceText, fe.Offset)
			report.Snippet = line
			if line == "" && fe.Offset >= len(fe.SourceText) {
				report.Snippet = "<end of file>"
			}
		}
		if report.Kind != "ambiguity" {
			report.Suggestions = source.RankCandidates(fe.Symbol, fe.Candidates, 3)
		}
	} else {
		report.Note = "no location information"
	}
	for _, rel := range fe.Related {
		report.Related = append(report.Related, RelatedLocation{
			File:       displayPath(dir, rel.Filename),
			Line:       rel.Line,
			Column:     rel.Column,
			ByteOffset: rel.Offset,
			Message:    rel.Message,
		})
	}
	if report.Kind == "ambiguity" || (report.Kind == "" && len(fe.Candidates) > 0 && fe.Object == "relation") {
		sort.Strings(report.Candidates)
	}
	return report
}

func writeText(w io.Writer, report Report) {
	if !report.HasLocation {
		fmt.Fprintln(w, "  location: no location information")
		return
	}
	fmt.Fprintf(w, "  location: %s:%d:%d (byte %d", report.File, report.Line, report.Column, report.ByteOffset)
	if report.EndByteOffset > report.ByteOffset {
		fmt.Fprintf(w, "-%d", report.EndByteOffset)
	}
	fmt.Fprintln(w, ")")
	if report.Snippet != "" {
		fmt.Fprintf(w, "  snippet: %s\n", report.Snippet)
		pad := report.Column - 1
		if report.EndByteOffset > report.ByteOffset {
			width := report.EndByteOffset - report.ByteOffset
			if width < 1 {
				width = 1
			}
			fmt.Fprintf(w, "           %s%s\n", strings.Repeat(" ", pad), strings.Repeat("^", width))
		} else {
			fmt.Fprintf(w, "           %s^\n", strings.Repeat(" ", pad))
		}
	}
	if report.Kind == "ambiguity" && len(report.Candidates) > 0 {
		fmt.Fprintf(w, "  candidates: %s\n", strings.Join(report.Candidates, ", "))
	}
	for _, s := range report.Suggestions {
		fmt.Fprintf(w, "  suggestion: did you mean %q? (edit distance %d)\n", s.Name, s.Distance)
	}
	for _, rel := range report.Related {
		if rel.Line > 0 && rel.Column > 0 {
			fmt.Fprintf(w, "  related: %s:%d:%d: %s\n", rel.File, rel.Line, rel.Column, rel.Message)
		} else {
			fmt.Fprintf(w, "  related: %s: %s\n", rel.File, rel.Message)
		}
	}
}

func displayPath(dir, filename string) string {
	if dir == "" {
		return filename
	}
	rel, err := filepath.Rel(dir, filename)
	if err != nil {
		return filename
	}
	return rel
}

func normalizeFormat(format string) string {
	if strings.EqualFold(format, FormatJSON) {
		return FormatJSON
	}
	return FormatText
}
