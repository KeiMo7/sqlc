package cmd

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestWriteFilesAtomicAndOrdered(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	output := map[string]string{
		filepath.Join(dir, "z.go"):           "package z\n",
		filepath.Join(dir, "a.go"):           "package a\n",
		filepath.Join(dir, "nested", "b.go"): "package b\n",
	}

	if err := writeFiles(output); err != nil {
		t.Fatalf("writeFiles failed: %v", err)
	}

	for name, want := range output {
		got, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("expected %s on disk: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	// No staging files may survive a successful run.
	var leftovers []string
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(d.Name(), ".tmp-") {
			leftovers = append(leftovers, path)
		}
		return nil
	})
	if len(leftovers) != 0 {
		t.Errorf("temporary files left behind: %v", leftovers)
	}
}

func TestWriteFilesFailureLeavesNoHalfWrittenFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Pre-create a valid file so we can assert it stays byte-identical after
	// the failed run rather than being truncated.
	keep := filepath.Join(dir, "keep.go")
	original := []byte("package keep\n")
	if err := os.WriteFile(keep, original, 0644); err != nil {
		t.Fatal(err)
	}

	// A path whose parent is an existing regular file cannot be created, so
	// staging this entry must fail and trigger the full cleanup.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}
	output := map[string]string{
		keep:                           "package keep\n// rewritten\n",
		filepath.Join(blocker, "x.go"): "package x\n",
		filepath.Join(dir, "other.go"): "package other\n",
	}

	if err := writeFiles(output); err == nil {
		t.Fatal("expected writeFiles to fail for an invalid output path")
	}

	got, err := os.ReadFile(keep)
	if err != nil {
		t.Fatalf("keep.go disappeared after the failed run: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("keep.go was modified by a failed run: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "other.go")); !os.IsNotExist(err) {
		t.Errorf("other.go should not exist after a failed run, stat err = %v", err)
	}

	var leftovers []string
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(d.Name(), ".tmp-") {
			leftovers = append(leftovers, path)
		}
		return nil
	})
	sort.Strings(leftovers)
	if len(leftovers) != 0 {
		t.Errorf("temporary files left behind after failure: %v", leftovers)
	}
}
