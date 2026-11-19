// Package idempotent verifies that the code generation pipeline is
// deterministic: generating twice from the same inputs must produce
// byte-for-byte identical output.
//
// The comparison result is structured so callers can report both a short
// summary (file counts and changed-line counts) and drill down into the
// concrete per-file hunks behind it.
package idempotent

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// GenerateFunc is one execution of the code generation pipeline. It maps an
// absolute output path to the rendered file contents. The concrete binding
// lives in internal/cmd so this package stays free of upward imports.
type GenerateFunc func(ctx context.Context) (map[string]string, error)

// LineDiff is one differing line pair reported for a file.
type LineDiff struct {
	Line     int    `json:"line"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

// FileDiff holds the per-file drilldown for one output file.
type FileDiff struct {
	Name      string     `json:"name"`
	Added     int        `json:"added"`
	Removed   int        `json:"removed"`
	FirstDiff *LineDiff  `json:"first_diff,omitempty"`
	LineDiffs []LineDiff `json:"line_diffs,omitempty"`
}

// NondeterminismSource points at one place in the pipeline whose output
// order depends on Go map iteration rather than an explicit order.
type NondeterminismSource struct {
	Package string `json:"package"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Code    string `json:"code"`
	Reason  string `json:"reason"`
	Status  string `json:"status"`
}

// Report is the structured result of comparing two generation runs.
type Report struct {
	// FileCount is the number of output files compared (union of both runs).
	FileCount int `json:"file_count"`
	// ChangedFiles is the number of files whose contents or presence differ.
	ChangedFiles int `json:"changed_files"`
	// AddedLines / RemovedLines aggregate the per-file line counts.
	AddedLines   int `json:"added_lines"`
	RemovedLines int `json:"removed_lines"`
	// FirstMismatch names the first file (in stable filename order) and the
	// first line within it where the two runs disagree.
	FirstMismatch *Mismatch `json:"first_mismatch,omitempty"`
	// FileDiffs holds the drilldown, sorted by filename.
	FileDiffs []FileDiff `json:"file_diffs"`
	// Sources lists the map-iteration order dependencies the generation
	// pipeline relies on. Entries marked "sorted" are neutralized by an
	// explicit stable sort; "fallback" entries are ordered by a deterministic
	// fallback sort that emits a hint instead of trusting map order.
	Sources []NondeterminismSource `json:"sources"`
}

// Mismatch pinpoints the first disagreement between two runs.
type Mismatch struct {
	File string    `json:"file"`
	Diff *LineDiff `json:"diff,omitempty"`
	Kind string    `json:"kind"`
}

// Stable reports whether the two runs were byte-for-byte identical.
func (r *Report) Stable() bool {
	return r.FirstMismatch == nil
}

// Summary renders the compact, human-readable summary line.
func (r *Report) Summary() string {
	if r.Stable() {
		return fmt.Sprintf("idempotent: %d files compared, 0 differences", r.FileCount)
	}
	return fmt.Sprintf(
		"not idempotent: %d files compared, %d changed, %d lines added, %d lines removed; first mismatch %s",
		r.FileCount, r.ChangedFiles, r.AddedLines, r.RemovedLines,
		formatMismatch(r.FirstMismatch),
	)
}

func formatMismatch(m *Mismatch) string {
	if m == nil {
		return "<unknown>"
	}
	switch m.Kind {
	case "missing":
		return fmt.Sprintf("%s: file missing from second run", m.File)
	case "unexpected":
		return fmt.Sprintf("%s: file absent from first run", m.File)
	default:
		if m.Diff == nil {
			return m.File
		}
		return fmt.Sprintf("%s:%d", m.File, m.Diff.Line)
	}
}

// Check generates twice from the same inputs and compares the outputs. The
// two runs execute in the same process on purpose: Go randomizes map
// iteration order per range, so any output that depends on a map range is
// exercised against a fresh random order on every run, exactly the failure
// mode the pipeline must not have.
func Check(ctx context.Context, generate GenerateFunc) (*Report, error) {
	first, err := generate(ctx)
	if err != nil {
		return nil, fmt.Errorf("first generation failed: %w", err)
	}
	second, err := generate(ctx)
	if err != nil {
		return nil, fmt.Errorf("second generation failed: %w", err)
	}
	return Compare(first, second), nil
}

// Compare builds a structured diff between two generation outputs.
func Compare(first, second map[string]string) *Report {
	names := make(map[string]struct{}, len(first)+len(second))
	for name := range first {
		names[name] = struct{}{}
	}
	for name := range second {
		names[name] = struct{}{}
	}

	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)

	report := &Report{
		FileCount: len(ordered),
		Sources:   knownSources(),
	}

	for _, name := range ordered {
		left, inFirst := first[name]
		right, inSecond := second[name]

		var fd FileDiff
		switch {
		case inFirst && !inSecond:
			fd = FileDiff{Name: name}
			report.bump(&fd)
			report.setFirst(&Mismatch{File: name, Kind: "missing"})
		case !inFirst && inSecond:
			fd = FileDiff{Name: name}
			report.bump(&fd)
			report.setFirst(&Mismatch{File: name, Kind: "unexpected"})
		case left != right:
			fd = diffFile(name, left, right)
			report.bump(&fd)
			report.setFirst(&Mismatch{File: name, Kind: "content", Diff: fd.FirstDiff})
		default:
			continue
		}
		report.FileDiffs = append(report.FileDiffs, fd)
	}
	return report
}

func (r *Report) bump(fd *FileDiff) {
	r.ChangedFiles++
	r.AddedLines += fd.Added
	r.RemovedLines += fd.Removed
}

func (r *Report) setFirst(m *Mismatch) {
	if r.FirstMismatch == nil {
		r.FirstMismatch = m
	}
}

// diffFile compares two file contents line by line. Files are walked in
// filename order by Compare, so the first FileDiff reported is the first
// mismatching file; within a file the smallest differing line number is the
// first mismatch position.
func diffFile(name, left, right string) FileDiff {
	leftLines := splitLines(left)
	rightLines := splitLines(right)

	fd := FileDiff{Name: name}
	n := len(leftLines)
	if len(rightLines) > n {
		n = len(rightLines)
	}
	for i := 0; i < n; i++ {
		var l, r string
		hasL, hasR := i < len(leftLines), i < len(rightLines)
		if hasL {
			l = leftLines[i]
		}
		if hasR {
			r = rightLines[i]
		}
		if l == r && hasL && hasR {
			continue
		}
		d := LineDiff{Line: i + 1, Expected: l, Actual: r}
		if fd.FirstDiff == nil {
			cp := d
			fd.FirstDiff = &cp
		}
		fd.LineDiffs = append(fd.LineDiffs, d)
		if hasL {
			fd.Removed++
		}
		if hasR {
			fd.Added++
		}
	}
	return fd
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// Detail renders the per-file drilldown: every changed file with its added
// and removed line counts, followed by the concrete differing lines. The
// summary and this detail are separate outputs on purpose; one cannot
// substitute for the other.
func (r *Report) Detail() string {
	if len(r.FileDiffs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, fd := range r.FileDiffs {
		fmt.Fprintf(&b, "--- %s (+%d -%d)\n", fd.Name, fd.Added, fd.Removed)
		for _, d := range fd.LineDiffs {
			fmt.Fprintf(&b, "  line %d:\n    - %q\n    + %q\n", d.Line, d.Expected, d.Actual)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// SourceHints renders the nondeterminism-source register: where map
// iteration feeds the generated output and what stabilizes it.
func (r *Report) SourceHints() string {
	var b strings.Builder
	for _, s := range r.Sources {
		fmt.Fprintf(&b, "[%s] %s:%d: %s (%s)\n    %s\n",
			s.Status, s.File, s.Line, s.Code, s.Package, s.Reason)
	}
	return strings.TrimRight(b.String(), "\n")
}
