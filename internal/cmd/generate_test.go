package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sqlc-dev/sqlc/internal/nullability"
)

func TestNullabilityExplanationDoesNotChangeGeneratedCode(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join("..", "endtoend", "testdata", "nullability_explain", "postgresql", "pgx")
	dir := t.TempDir()
	for _, name := range []string{"schema.sql", "query.sql"} {
		blob, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), blob, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig := func(name, out string, explain bool) {
		t.Helper()
		nullabilityConfig := ""
		if explain {
			nullabilityConfig = `
nullability:
  explain: true
  output: "nullability.json"
  fields: ["profiles.bio", "score"]
`
		}
		config := `version: "2"
` + nullabilityConfig + `sql:
  - engine: "postgresql"
    schema: "schema.sql"
    queries: "query.sql"
    gen:
      go:
        package: "querytest"
        out: "` + out + `"
        sql_package: "pgx/v5"
`
		if err := os.WriteFile(filepath.Join(dir, name), []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig("without.yaml", "without", false)
	writeConfig("with.yaml", "with", true)

	var stderr bytes.Buffer
	without, err := Generate(ctx, dir, "without.yaml", &Options{Stderr: &stderr})
	if err != nil {
		t.Fatalf("generate without explanation: %s", stderr.String())
	}
	stderr.Reset()
	with, err := Generate(ctx, dir, "with.yaml", &Options{Stderr: &stderr})
	if err != nil {
		t.Fatalf("generate with explanation: %s", stderr.String())
	}

	withoutCode := generatedCode(without, filepath.Join(dir, "without"))
	withCode := generatedCode(with, filepath.Join(dir, "with"))
	if diff := compareStringMaps(withoutCode, withCode); diff != "" {
		t.Fatalf("generated code changed when explanation was enabled:\n%s", diff)
	}

	artifact, ok := with[filepath.Join(dir, "nullability.json")]
	if !ok {
		t.Fatal("nullability sidecar was not generated")
	}
	if _, ok := without[filepath.Join(dir, "nullability.json")]; ok {
		t.Fatal("nullability sidecar was generated while disabled")
	}
	var doc nullability.Artifact
	if err := json.Unmarshal([]byte(artifact), &doc); err != nil {
		t.Fatalf("parse sidecar: %s", err)
	}
	if len(doc.Fields) != 2 {
		t.Fatalf("filter returned %d fields, want profiles.bio and score: %#v", len(doc.Fields), doc.Fields)
	}
	bio := doc.Fields[0]
	if bio.Column != "bio" || len(bio.Conclusions) != 3 {
		t.Fatalf("profiles.bio conclusions = %d, want 3 query occurrences", len(bio.Conclusions))
	}
	if len(doc.CrossQueryDiffs) != 1 {
		t.Fatalf("cross-query differences = %d, want 1", len(doc.CrossQueryDiffs))
	}

	// These are the three acceptance checks: declaration, outer join, and the
	// NOT NULL DEFAULT column (a default must not make the field nullable).
	assertColumn(t, doc, "GetBio", "bio", false, "schema.column_not_null")
	assertColumn(t, doc, "ListUserProfiles", "bio", true, "query.outer_join")
	assertColumn(t, doc, "GetUser", "score", false, "schema.column_not_null_with_default")
}

func TestNullabilityExplanationDoesNotHideMissingSchemaColumn(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "schema.sql"), []byte(`CREATE TABLE users (id INTEGER NOT NULL);`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "query.sql"), []byte(`-- name: GetMissing :one
SELECT missing FROM users;`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sqlc.yaml"), []byte(`version: "2"
nullability:
  explain: true
  output: "nullability.json"
sql:
  - engine: "postgresql"
    schema: "schema.sql"
    queries: "query.sql"
    gen:
      go:
        package: "querytest"
        out: "go"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	_, err := Generate(context.Background(), dir, "sqlc.yaml", &Options{Stderr: &stderr})
	if err == nil {
		t.Fatal("generation succeeded for a column missing from the schema")
	}
	if !strings.Contains(stderr.String(), `column "missing" does not exist`) {
		t.Fatalf("missing-column error did not identify the field: %s", stderr.String())
	}
}

func generatedCode(output map[string]string, root string) map[string]string {
	out := map[string]string{}
	for path, contents := range output {
		if strings.HasSuffix(path, ".go") {
			rel := strings.TrimPrefix(path, root+string(filepath.Separator))
			out[rel] = contents
		}
	}
	return out
}

func compareStringMaps(a, b map[string]string) string {
	keys := make([]string, 0, len(a)+len(b))
	seen := map[string]struct{}{}
	for key := range a {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range b {
		if _, ok := seen[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var diff strings.Builder
	for _, key := range keys {
		if a[key] != b[key] {
			diff.WriteString(key)
			diff.WriteString("\n")
		}
	}
	return diff.String()
}

func assertColumn(t *testing.T, doc nullability.Artifact, queryName, field string, nullable bool, rule string) {
	t.Helper()
	for _, query := range doc.Queries {
		if query.QueryName != queryName {
			continue
		}
		for _, column := range query.Columns {
			if column.Field != field {
				continue
			}
			if column.Nullable != nullable || column.Rule != rule {
				t.Fatalf("%s.%s = nullable %t via %s, want nullable %t via %s",
					queryName, field, column.Nullable, column.Rule, nullable, rule)
			}
			return
		}
	}
	t.Fatalf("%s.%s was not explained", queryName, field)
}
