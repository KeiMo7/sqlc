package idempotent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

var errBoom = errors.New("boom")

func TestCheckInvokesGeneratorTwice(t *testing.T) {
	t.Parallel()

	calls := 0
	report, err := Check(context.Background(), func(_ context.Context) (map[string]string, error) {
		calls++
		return map[string]string{"/out/a.go": "package a\n"}, nil
	})
	if err != nil {
		t.Fatalf("Check returned an error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("generator invoked %d times, want 2", calls)
	}
	if !report.Stable() {
		t.Errorf("identical generated output reported as unstable: %s", report.Summary())
	}
}

func TestCheckPropagatesGeneratorError(t *testing.T) {
	t.Parallel()

	_, err := Check(context.Background(), func(_ context.Context) (map[string]string, error) {
		return nil, errBoom
	})
	if err == nil || !strings.Contains(err.Error(), "first generation failed") {
		t.Fatalf("error = %v, want a wrapped first-generation failure", err)
	}
}
func TestCompareIdentical(t *testing.T) {
	t.Parallel()

	output := map[string]string{
		"/out/a.go": "package out\n",
		"/out/b.go": "package out\n",
	}
	report := Compare(output, output)
	if !report.Stable() {
		t.Fatalf("identical inputs reported as unstable: %s", report.Summary())
	}
	if report.FileCount != 2 || report.ChangedFiles != 0 {
		t.Errorf("counts = %d/%d, want 2/0", report.FileCount, report.ChangedFiles)
	}
	if report.Detail() != "" {
		t.Errorf("stable run must have an empty detail, got %q", report.Detail())
	}
	if !strings.Contains(report.Summary(), "0 differences") {
		t.Errorf("summary = %q", report.Summary())
	}
}

func TestCompareEmpty(t *testing.T) {
	t.Parallel()

	report := Compare(map[string]string{}, map[string]string{})
	if !report.Stable() {
		t.Fatalf("empty outputs should be stable: %s", report.Summary())
	}
	if report.FileCount != 0 {
		t.Errorf("FileCount = %d, want 0", report.FileCount)
	}
}

func TestCompareMissingAndUnexpected(t *testing.T) {
	t.Parallel()

	first := map[string]string{
		"/out/a.go": "package a\n",
		"/out/b.go": "package b\n",
	}
	second := map[string]string{
		"/out/b.go": "package b\n",
		"/out/c.go": "package c\n",
	}

	report := Compare(first, second)
	if report.Stable() {
		t.Fatal("expected instability")
	}
	if report.ChangedFiles != 2 {
		t.Errorf("ChangedFiles = %d, want 2", report.ChangedFiles)
	}
	if report.FirstMismatch == nil || report.FirstMismatch.File != "/out/a.go" || report.FirstMismatch.Kind != "missing" {
		t.Fatalf("first mismatch = %+v, want missing /out/a.go", report.FirstMismatch)
	}

	kinds := map[string]string{}
	for _, fd := range report.FileDiffs {
		switch fd.Name {
		case "/out/a.go":
			kinds["a"] = "missing"
		case "/out/c.go":
			kinds["c"] = "unexpected"
		}
	}
	if kinds["a"] != "missing" || kinds["c"] != "unexpected" {
		t.Errorf("file diff kinds = %+v", kinds)
	}
}

func TestCompareFirstLineMismatch(t *testing.T) {
	t.Parallel()

	left := "line1\nline2\nline3\n"
	right := "line1\nchanged\nline3\n"
	report := Compare(
		map[string]string{"/out/x.go": left},
		map[string]string{"/out/x.go": right},
	)
	if report.Stable() {
		t.Fatal("expected a content mismatch")
	}
	if report.AddedLines != 1 || report.RemovedLines != 1 {
		t.Errorf("line counts = +%d -%d, want +1 -1", report.AddedLines, report.RemovedLines)
	}
	if report.FirstMismatch == nil || report.FirstMismatch.Diff == nil || report.FirstMismatch.Diff.Line != 2 {
		t.Fatalf("first mismatch = %+v, want line 2", report.FirstMismatch)
	}
	detail := report.Detail()
	if !strings.Contains(detail, "/out/x.go") || !strings.Contains(detail, "line 2") {
		t.Errorf("detail missing the pinpoint: %q", detail)
	}
}

func TestSourceHints(t *testing.T) {
	t.Parallel()

	report := Compare(map[string]string{}, map[string]string{})
	hints := report.SourceHints()
	if len(report.Sources) == 0 {
		t.Fatal("expected registered sources")
	}
	if !strings.Contains(hints, "sorted") {
		t.Errorf("hints do not explain the sorted fallback: %q", hints)
	}
	for _, source := range report.Sources {
		if source.Status != "sorted" && source.Status != "fallback" {
			t.Errorf("unexpected source status %q", source.Status)
		}
	}
}
