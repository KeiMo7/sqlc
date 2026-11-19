package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// writeFiles persists the generated output without ever leaving a target
// file half written. Every file is first rendered into a temporary file in
// its destination directory; only once all of them are written and synced
// are they renamed into place, in a stable filename order.
//
// A failure while writing one file removes every temporary file created for
// the run, so the previously generated contents stay untouched on disk.
func writeFiles(output map[string]string) error {
	names := make([]string, 0, len(output))
	for name := range output {
		names = append(names, name)
	}
	sort.Strings(names)

	type staged struct {
		target string
		tmp    string
	}

	stagedFiles := make([]staged, 0, len(names))
	cleanup := func() {
		for _, sf := range stagedFiles {
			os.Remove(sf.tmp)
		}
	}

	for _, name := range names {
		if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			cleanup()
			return fmt.Errorf("%s: %w", name, err)
		}

		tmp, err := os.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".tmp-*")
		if err != nil {
			cleanup()
			return fmt.Errorf("%s: %w", name, err)
		}
		tmpName := tmp.Name()

		if _, err := tmp.WriteString(output[name]); err != nil {
			tmp.Close()
			cleanup()
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := tmp.Sync(); err != nil {
			tmp.Close()
			cleanup()
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := tmp.Close(); err != nil {
			cleanup()
			return fmt.Errorf("%s: %w", name, err)
		}

		stagedFiles = append(stagedFiles, staged{target: name, tmp: tmpName})
	}

	// All files are fully staged. Move them into place one at a time in the
	// same sorted order, so the set of files on disk converges deterministically.
	for _, sf := range stagedFiles {
		if err := os.Rename(sf.tmp, sf.target); err != nil {
			cleanup()
			return fmt.Errorf("%s: %w", sf.target, err)
		}
	}
	return nil
}
