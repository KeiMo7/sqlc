package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnosticsSyntaxSemanticAmbiguityAndFalsePositives(t *testing.T) {
	t.Run("syntax suggestion", func(t *testing.T) {
		dir := diagnosticTestDir(t, map[string]string{
			"schema.sql": "CREATE TABLE users (id bigint PRIMARY KEY, user_id bigint NOT NULL);\n",
			"query.sql":  "-- name: Typo :one\nselectt id FROM users;\n",
		}, "schema.sql", "query.sql")

		off, err := runDiagnosticGenerate(dir, false, "text")
		if err == nil {
			t.Fatal("generate succeeded, want a syntax error")
		}
		on, err := runDiagnosticGenerate(dir, true, "text")
		if err == nil {
			t.Fatal("diagnostic generate succeeded, want a syntax error")
		}
		if firstLine(off) != firstLine(on) {
			t.Fatalf("legacy first line changed:\noff: %q\non:  %q", firstLine(off), firstLine(on))
		}
		assertContains(t, on, "location: query.sql:2:1")
		assertContains(t, on, "snippet: selectt id FROM users;")
		assertContains(t, on, `suggestion: did you mean "SELECT"?`)
	})

	t.Run("unclosed paren and truncated statement", func(t *testing.T) {
		cases := []struct {
			name  string
			query string
			want  string
		}{
			{
				name:  "unclosed",
				query: "-- name: Unclosed :many\nSELECT (id FROM users;\n",
				want:  "location: query.sql:2:12",
			},
			{
				name:  "truncated",
				query: "-- name: Truncated :many\nSELECT id FROM users WHERE id =\n",
				want:  "location: query.sql:3:1",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				dir := diagnosticTestDir(t, map[string]string{
					"schema.sql": "CREATE TABLE users (id bigint PRIMARY KEY);\n",
					"query.sql":  tc.query,
				}, "schema.sql", "query.sql")
				stderr, err := runDiagnosticGenerate(dir, true, "text")
				if err == nil {
					t.Fatal("generate succeeded, want a syntax error")
				}
				assertContains(t, stderr, tc.want)
				assertContains(t, stderr, "snippet:")
			})
		}
	})

	t.Run("semantic suggestion", func(t *testing.T) {
		dir := diagnosticTestDir(t, map[string]string{
			"schema.sql": "CREATE TABLE users (id bigint PRIMARY KEY, user_id bigint NOT NULL);\n",
			"query.sql":  "-- name: Find :one\nSELECT usr_id FROM users WHERE id = $1;\n",
		}, "schema.sql", "query.sql")

		stderr, err := runDiagnosticGenerate(dir, true, "text")
		if err == nil {
			t.Fatal("generate succeeded, want a semantic error")
		}
		assertContains(t, stderr, "location: query.sql:2:8")
		assertContains(t, stderr, "snippet: SELECT usr_id FROM users WHERE id = $1;")
		assertContains(t, stderr, `suggestion: did you mean "user_id"? (edit distance 1)`)
		assertContains(t, stderr, `related: schema.sql:1:1: table "users" is defined here`)
	})

	t.Run("relation suggestion", func(t *testing.T) {
		dir := diagnosticTestDir(t, map[string]string{
			"schema.sql": "CREATE TABLE users (id bigint PRIMARY KEY);\n",
			"query.sql":  "-- name: Users :many\nSELECT * FROM usres;\n",
		}, "schema.sql", "query.sql")

		stderr, err := runDiagnosticGenerate(dir, true, "text")
		if err == nil {
			t.Fatal("generate succeeded, want a relation error")
		}
		assertContains(t, stderr, "location: query.sql:2:15")
		assertContains(t, stderr, `suggestion: did you mean "users"?`)
	})

	t.Run("cross file view", func(t *testing.T) {
		dir := diagnosticTestDir(t, map[string]string{
			"schema.sql": "CREATE TABLE users (id bigint PRIMARY KEY, display_name text);\n",
			"views.sql":  "CREATE VIEW active_users AS SELECT id, display_name FROM users;\n",
			"query.sql":  "-- name: Active :many\nSELECT email FROM active_users;\n",
		}, "schema.sql views.sql", "query.sql")

		stderr, err := runDiagnosticGenerate(dir, true, "text")
		if err == nil {
			t.Fatal("generate succeeded, want a semantic error")
		}
		assertContains(t, stderr, "related: views.sql:1:13: view \"active_users\" is defined here")
	})

	t.Run("ambiguous relation candidates", func(t *testing.T) {
		dir := diagnosticTestDir(t, map[string]string{
			"schema.sql": strings.Join([]string{
				"CREATE SCHEMA archive;",
				"CREATE TABLE public.users (id bigint PRIMARY KEY);",
				"CREATE TABLE archive.users (id bigint PRIMARY KEY);",
				"",
			}, "\n"),
			"query.sql": "-- name: Users :many\nSELECT id FROM users;\n",
		}, "schema.sql", "query.sql")

		first, err := runDiagnosticGenerate(dir, true, "text")
		if err == nil {
			t.Fatal("generate succeeded, want an ambiguity error")
		}
		second, err := runDiagnosticGenerate(dir, true, "text")
		if err == nil {
			t.Fatal("second generate succeeded, want an ambiguity error")
		}
		if first != second {
			t.Fatalf("diagnostics changed between runs:\nfirst:\n%s\nsecond:\n%s", first, second)
		}
		assertContains(t, first, "location: query.sql:2:16")
		assertContains(t, first, "candidates: archive.users, public.users")
	})

	t.Run("comments and strings do not false positive", func(t *testing.T) {
		dir := diagnosticTestDir(t, map[string]string{
			"schema.sql": "CREATE TABLE users (id bigint PRIMARY KEY, user_id bigint NOT NULL);\n",
			"query.sql": strings.Join([]string{
				"-- name: Valid :one",
				"-- selectt and usr_id appear here only as prose.",
				"SELECT 'selectt usr_id' AS note, user_id FROM users WHERE id = $1;",
				"",
			}, "\n"),
		}, "schema.sql", "query.sql")

		stderr, err := runDiagnosticGenerate(dir, true, "text")
		if err != nil {
			t.Fatalf("generate failed on valid comment/string: %s", stderr)
		}
		if stderr != "" {
			t.Fatalf("unexpected diagnostics for valid input: %s", stderr)
		}
	})

	t.Run("unqualified unique table resolves", func(t *testing.T) {
		dir := diagnosticTestDir(t, map[string]string{
			"schema.sql": strings.Join([]string{
				"CREATE SCHEMA archive;",
				"CREATE TABLE public.users (id bigint PRIMARY KEY, display_name text);",
				"CREATE TABLE archive.events (id bigint PRIMARY KEY, user_id bigint NOT NULL);",
				"",
			}, "\n"),
			"query.sql": "-- name: Events :many\nSELECT e.id, u.display_name FROM events AS e JOIN public.users AS u ON u.id = e.user_id;\n",
		}, "schema.sql", "query.sql")

		stderr, err := runDiagnosticGenerate(dir, true, "text")
		if err != nil {
			t.Fatalf("mixed qualified/unqualified query failed: %s", stderr)
		}
	})

	t.Run("all errors are listed", func(t *testing.T) {
		dir := diagnosticTestDir(t, map[string]string{
			"schema.sql": "CREATE TABLE users (id bigint PRIMARY KEY, user_id bigint NOT NULL);\n",
			"a.sql":      "-- name: A :one\nSELECT usr_id FROM users;\n",
			"b.sql":      "-- name: B :one\nSELECT missing_column FROM users;\n",
			"c.sql":      "-- name: C :one\nselectt user_id FROM users;\n",
		}, "schema.sql", "a.sql b.sql c.sql")

		stderr, err := runDiagnosticGenerate(dir, true, "text")
		if err == nil {
			t.Fatal("generate succeeded, want multiple errors")
		}
		assertContains(t, stderr, "a.sql:2:8:")
		assertContains(t, stderr, "b.sql:2:8:")
		assertContains(t, stderr, "c.sql:2:2:")
		assertContains(t, stderr, `suggestion: did you mean "SELECT"?`)
		if got := strings.Count(stderr, "  location:"); got != 3 {
			t.Fatalf("got %d locations, want 3:\n%s", got, stderr)
		}
	})

	t.Run("wrong function arity", func(t *testing.T) {
		dir := diagnosticTestDir(t, map[string]string{
			"schema.sql": strings.Join([]string{
				"CREATE TABLE users (id bigint PRIMARY KEY);",
				"CREATE FUNCTION public.between_ids(lo bigint, hi bigint) RETURNS bigint AS $$ SELECT lo $$ LANGUAGE sql;",
				"",
			}, "\n"),
			"query.sql": "-- name: WrongArgs :one\nSELECT public.between_ids(id) FROM users WHERE id = $1;\n",
		}, "schema.sql", "query.sql")

		stderr, err := runDiagnosticGenerate(dir, true, "text")
		if err == nil {
			t.Fatal("generate succeeded, want a function arity error")
		}
		assertContains(t, stderr, `function between_ids(unknown) does not exist`)
		assertContains(t, stderr, "location: query.sql:2:8")
		assertContains(t, stderr, "snippet: SELECT public.between_ids(id) FROM users WHERE id = $1;")
	})

	t.Run("machine readable", func(t *testing.T) {
		dir := diagnosticTestDir(t, map[string]string{
			"schema.sql": "CREATE TABLE users (id bigint PRIMARY KEY, user_id bigint NOT NULL);\n",
			"query.sql":  "-- name: Find :one\nSELECT usr_id FROM users WHERE id = $1;\n",
		}, "schema.sql", "query.sql")

		stderr, err := runDiagnosticGenerate(dir, true, "json")
		if err == nil {
			t.Fatal("generate succeeded, want a semantic error")
		}
		lines := strings.Split(strings.TrimSpace(stderr), "\n")
		if len(lines) < 2 {
			t.Fatalf("expected legacy line plus JSON object, got %q", stderr)
		}
		var report struct {
			File        string `json:"file"`
			Line        int    `json:"line"`
			Column      int    `json:"column"`
			ByteOffset  int    `json:"byte_offset"`
			HasLocation bool   `json:"has_location"`
			Snippet     string `json:"snippet"`
			Suggestions []struct {
				Name string `json:"name"`
			} `json:"suggestions"`
		}
		if err := json.Unmarshal([]byte(lines[len(lines)-1]), &report); err != nil {
			t.Fatalf("invalid JSON diagnostic %q: %v", lines[len(lines)-1], err)
		}
		if !report.HasLocation || report.File != "query.sql" || report.Line != 2 || report.Column != 8 || report.ByteOffset == 0 {
			t.Fatalf("unexpected JSON location: %+v", report)
		}
		if report.Snippet != "SELECT usr_id FROM users WHERE id = $1;" {
			t.Fatalf("unexpected snippet: %q", report.Snippet)
		}
		if len(report.Suggestions) == 0 || report.Suggestions[0].Name != "user_id" {
			t.Fatalf("unexpected suggestions: %+v", report.Suggestions)
		}
	})
}

func TestBatchFixIncreasesPassingQueries(t *testing.T) {
	files := map[string]string{
		"schema.sql": "CREATE TABLE users (id bigint PRIMARY KEY, user_id bigint NOT NULL);\n",
		"a.sql":      "-- name: A :one\nSELECT usr_id FROM users;\n",
		"b.sql":      "-- name: B :one\nSELECT user_id FROM users;\n",
		"c.sql":      "-- name: C :one\nSELECT user_id FROM users;\n",
	}
	dir := diagnosticTestDir(t, files, "schema.sql", "a.sql b.sql c.sql")
	if _, err := runDiagnosticGenerate(dir, true, "text"); err == nil {
		t.Fatal("generate succeeded before fixing the typo")
	}
	t.Log("passing queries before fix: 0")

	files["a.sql"] = "-- name: A :one\nSELECT user_id FROM users;\n"
	writeDiagnosticFile(t, dir, "a.sql", files["a.sql"])
	output, err := runGenerateOutput(dir, true, "text")
	if err != nil {
		t.Fatal(err)
	}
	var generated strings.Builder
	for name, contents := range output {
		if strings.HasSuffix(filepath.Base(name), ".sql.go") {
			generated.WriteString(contents)
		}
	}
	if got := strings.Count(generated.String(), "func (q *Queries)"); got != 3 {
		t.Fatalf("generated %d passing queries, want 3", got)
	}
	t.Log("passing queries after fix: 3")
}

func diagnosticTestDir(t *testing.T, files map[string]string, schema, queries string) string {
	t.Helper()
	dir := t.TempDir()
	for name, contents := range files {
		writeDiagnosticFile(t, dir, name, contents)
	}
	config := map[string]any{
		"version": "2",
		"sql": []any{map[string]any{
			"engine":  "postgresql",
			"schema":  strings.Fields(schema),
			"queries": strings.Fields(queries),
			"gen": map[string]any{
				"go": map[string]any{"package": "db", "out": "gen"},
			},
		}},
	}
	blob, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	writeDiagnosticFile(t, dir, "sqlc.json", string(blob))
	return dir
}

func writeDiagnosticFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runDiagnosticGenerate(dir string, diagnostics bool, format string) (string, error) {
	var stderr bytes.Buffer
	_, err := Generate(context.Background(), dir, "", &Options{
		Env: Env{
			Diagnostics:      diagnostics,
			DiagnosticFormat: format,
		},
		Stderr: &stderr,
	})
	return stderr.String(), err
}

func runGenerateOutput(dir string, diagnostics bool, format string) (map[string]string, error) {
	var stderr bytes.Buffer
	return Generate(context.Background(), dir, "", &Options{
		Env: Env{
			Diagnostics:      diagnostics,
			DiagnosticFormat: format,
		},
		Stderr: &stderr,
	})
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("output does not contain %q:\n%s", want, got)
	}
}
