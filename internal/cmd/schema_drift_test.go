package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProjectFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateSchemaDriftDefaultOff(t *testing.T) {
	dir := t.TempDir()
	writeProjectFile(t, dir, "schema.sql", "CREATE TABLE users (id integer PRIMARY KEY);\n")
	writeProjectFile(t, dir, "query.sql", "-- name: GetUser :one\nSELECT id FROM users;\n")
	writeProjectFile(t, dir, "sqlc.yaml", `version: "2"
sql:
  - engine: postgresql
    schema: schema.sql
    queries: query.sql
    gen:
      go:
        package: db
        out: db
`)
	var stderr bytes.Buffer
	output, err := Generate(context.Background(), dir, "", &Options{Stderr: &stderr})
	if err != nil {
		t.Fatalf("generate failed: %v\n%s", err, stderr.String())
	}
	if len(output) == 0 {
		t.Fatal("expected generated output")
	}
	if _, err := os.Stat(filepath.Join(dir, "schema-drift-report.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("schema drift report should not be written while disabled: %v", err)
	}
}

func TestGenerateSchemaDriftGate(t *testing.T) {
	dir := t.TempDir()
	dev := "CREATE TABLE users (id integer PRIMARY KEY, email text);\n"
	prod := "CREATE TABLE users (id integer PRIMARY KEY);\n"
	writeProjectFile(t, dir, "schema/dev.sql", dev)
	writeProjectFile(t, dir, "schema/staging.sql", prod)
	writeProjectFile(t, dir, "schema/prod.sql", prod)
	writeProjectFile(t, dir, "query.sql", "-- name: GetUser :one\nSELECT id FROM users;\n")
	writeProjectFile(t, dir, "sqlc.yaml", `version: "2"
sql:
  - engine: postgresql
    schema: schema/dev.sql
    queries: query.sql
    schema_drift:
      enabled: true
      staging: schema/staging.sql
      production: schema/prod.sql
      gate: true
      report: drift/full.md
    gen:
      go:
        package: db
        out: db
`)
	var stderr bytes.Buffer
	output, err := Generate(context.Background(), dir, "", &Options{Stderr: &stderr})
	if !errors.Is(err, ErrSchemaDriftGate) {
		t.Fatalf("expected gate error, got %v\nstderr:\n%s", err, stderr.String())
	}
	if output != nil {
		t.Fatal("generation must not run after a failed gate")
	}
	report, err := os.ReadFile(filepath.Join(dir, "drift", "full.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "column_removed") {
		t.Fatalf("full report missing drift detail:\n%s", report)
	}
	if !strings.Contains(stderr.String(), "column_removed") {
		t.Fatalf("gate output missing incompatible drift:\n%s", stderr.String())
	}
	if current, err := os.ReadFile(filepath.Join(dir, "schema/dev.sql")); err != nil || string(current) != dev {
		t.Fatalf("schema file was modified: %v %q", err, current)
	}
}

func TestGenerateSchemaDriftParseFailure(t *testing.T) {
	dir := t.TempDir()
	writeProjectFile(t, dir, "schema/dev.sql", "CREATE TABLE users (id integer);\n")
	writeProjectFile(t, dir, "schema/staging.sql", "CREATE TABLE users (\n")
	writeProjectFile(t, dir, "schema/prod.sql", "CREATE TABLE users (id integer);\n")
	writeProjectFile(t, dir, "query.sql", "-- name: GetUser :one\nSELECT id FROM users;\n")
	writeProjectFile(t, dir, "sqlc.yaml", `version: "2"
sql:
  - engine: postgresql
    schema: schema/dev.sql
    queries: query.sql
    schema_drift:
      enabled: true
      staging: schema/staging.sql
      production: schema/prod.sql
    gen:
      go:
        package: db
        out: db
`)
	var stderr bytes.Buffer
	output, err := Generate(context.Background(), dir, "", &Options{Stderr: &stderr})
	if err == nil {
		t.Fatal("expected parse failure")
	}
	if output != nil {
		t.Fatal("partial generation must not run")
	}
	if !strings.Contains(err.Error(), filepath.Join("schema", "staging.sql")) {
		t.Fatalf("error did not name the failing file: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "schema-drift-report.md")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a partial report must not be written: %v", statErr)
	}
}

func TestSchemaDriftCommandExitCode(t *testing.T) {
	dir := t.TempDir()
	writeProjectFile(t, dir, "schema/dev.sql", "CREATE TABLE users (id integer, email text);\n")
	writeProjectFile(t, dir, "schema/staging.sql", "CREATE TABLE users (id integer);\n")
	writeProjectFile(t, dir, "schema/prod.sql", "CREATE TABLE users (id integer);\n")
	configPath := filepath.Join(dir, "sqlc.yaml")
	writeProjectFile(t, dir, "sqlc.yaml", `version: "2"
sql:
  - engine: postgresql
    schema: schema/dev.sql
    queries: schema/dev.sql
    schema_drift:
      enabled: true
      staging: schema/staging.sql
      production: schema/prod.sql
      gate: true
`)
	var stdout, stderr bytes.Buffer
	code := Do([]string{"schema-drift", "-f", configPath}, nil, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("gate should exit non-zero\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "column_removed") {
		t.Fatalf("gate stdout missing incompatible difference:\n%s", stdout.String())
	}
}
