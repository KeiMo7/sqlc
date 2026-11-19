package schemadrift

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sqlc-dev/sqlc/internal/config"
)

func writeSchema(t *testing.T, root, name string, files map[string]string) Environment {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for filename, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, filename), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return Environment{Name: name, Schema: []string{dir}}
}

func compareSchemas(t *testing.T, dev, staging, prod map[string]string) *Report {
	t.Helper()
	root := t.TempDir()
	report, err := Compare(Options{
		Engine: config.EnginePostgreSQL,
		Environments: []Environment{
			writeSchema(t, root, "development", dev),
			writeSchema(t, root, "staging", staging),
			writeSchema(t, root, "production", prod),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func pairDiffs(t *testing.T, report *Report, from, to string) []Diff {
	t.Helper()
	for _, pair := range report.Pairs {
		if pair.From == from && pair.To == to {
			return pair.Diffs
		}
	}
	t.Fatalf("pair %s -> %s not found", from, to)
	return nil
}

func findDiff(diffs []Diff, kind, column string) *Diff {
	for i := range diffs {
		if diffs[i].Kind == kind && (column == "" || diffs[i].Column == column) {
			return &diffs[i]
		}
	}
	return nil
}

func TestColumnDifferences(t *testing.T) {
	dev := map[string]string{"schema.sql": `
CREATE TABLE users (
  id integer PRIMARY KEY,
  email text NOT NULL DEFAULT 'dev@example.com',
  removed text,
  changed integer
);`}
	prod := map[string]string{"schema.sql": `
CREATE TABLE users (
  id integer PRIMARY KEY,
  email text,
  added text,
  changed bigint
);`}
	diffs := pairDiffs(t, compareSchemas(t, dev, prod, prod), "development", "production")

	for _, tc := range []struct {
		kind   string
		column string
	}{
		{"column_added", "added"},
		{"column_removed", "removed"},
		{"type_changed", "changed"},
		{"nullability_changed", "email"},
		{"default_changed", "email"},
	} {
		diff := findDiff(diffs, tc.kind, tc.column)
		if diff == nil {
			t.Fatalf("missing %s for column %s in %+v", tc.kind, tc.column, diffs)
		}
		if diff.Severity == "" {
			t.Fatalf("%s has no severity", tc.kind)
		}
	}
	if !findDiff(diffs, "column_removed", "removed").Incompatible {
		t.Fatal("removed column should be incompatible")
	}
	if findDiff(diffs, "column_added", "added").Incompatible {
		t.Fatal("column present only in the target is not incompatible")
	}
}

func TestConstraintDifferences(t *testing.T) {
	dev := map[string]string{"schema.sql": `
CREATE TABLE orgs (id integer PRIMARY KEY);
CREATE TABLE accounts (
  id integer PRIMARY KEY,
  email text,
  org_id integer REFERENCES orgs(id)
);
CREATE UNIQUE INDEX accounts_email_idx ON accounts (email);`}
	prod := map[string]string{"schema.sql": `
CREATE TABLE orgs (id integer PRIMARY KEY);
CREATE TABLE accounts (
  id integer,
  email text,
  org_id integer
);`}
	diffs := pairDiffs(t, compareSchemas(t, dev, prod, prod), "development", "production")
	for _, kind := range []string{"primary_key_changed", "unique_changed", "foreign_key_changed"} {
		if findDiff(diffs, kind, "") == nil {
			t.Fatalf("missing %s in %+v", kind, diffs)
		}
	}
}

func TestTableShapeDifference(t *testing.T) {
	dev := map[string]string{"schema.sql": `CREATE TABLE shape (a integer, b text);`}
	prod := map[string]string{"schema.sql": `CREATE TABLE shape (c bigint, d boolean);`}
	diffs := pairDiffs(t, compareSchemas(t, dev, prod, prod), "development", "production")
	diff := findDiff(diffs, "table_shape_changed", "")
	if diff == nil {
		t.Fatalf("missing table_shape_changed in %+v", diffs)
	}
	if !diff.Incompatible {
		t.Fatal("table shape change should be incompatible")
	}
}

func TestEquivalentSchemasDoNotDrift(t *testing.T) {
	dev := map[string]string{"schema.sql": `
-- column order and comments differ, the schema does not
CREATE TABLE users (
  id int PRIMARY KEY,
  name varchar(20),
  active boolean DEFAULT true
);
COMMENT ON TABLE users IS 'development comment';`}
	prod := map[string]string{"schema.sql": `
CREATE TABLE users (
  active bool DEFAULT TRUE,
  name character varying(20),
  id integer PRIMARY KEY
);
COMMENT ON TABLE users IS 'production comment';`}
	report := compareSchemas(t, dev, prod, prod)
	if got := report.DiffCount(); got != 0 {
		t.Fatalf("expected no differences, got %d:\n%s", got, report.RenderFull())
	}
}

func TestPartitionsAndInheritance(t *testing.T) {
	dev := map[string]string{"schema.sql": `
CREATE TABLE events (
  id bigint,
  created_at date
) PARTITION BY RANGE (created_at);
CREATE TABLE events_2024 PARTITION OF events
  FOR VALUES FROM ('2024-01-01') TO ('2025-01-01');
CREATE TABLE audit (id bigint);
CREATE TABLE audit_archive (archived_at date) INHERITS (audit);`}
	prod := map[string]string{"schema.sql": `
CREATE TABLE events (
  id bigint,
  created_at date
) PARTITION BY RANGE (created_at);
CREATE TABLE events_2024 PARTITION OF events
  FOR VALUES FROM ('2024-07-01') TO ('2025-01-01');
CREATE TABLE audit (id bigint);
CREATE TABLE audit_archive (archived_at date) INHERITS (audit);`}
	diffs := pairDiffs(t, compareSchemas(t, dev, prod, prod), "development", "production")
	partition := findDiff(diffs, "partition_changed", "")
	if partition == nil {
		t.Fatalf("missing partition_changed in %+v", diffs)
	}
	if !partition.Partition {
		t.Fatal("partition change was not marked for the partition section")
	}
	// Parent and child columns intentionally differ. Only comparing the same
	// relation across environments keeps that from being reported as drift.
	for _, diff := range diffs {
		if diff.Table == "events" || diff.Table == "events_2024" || diff.Table == "audit" || diff.Table == "audit_archive" {
			if strings.HasPrefix(diff.Kind, "column_") || diff.Kind == "table_shape_changed" {
				t.Fatalf("inheritance/partition column layout reported as drift: %+v", diff)
			}
		}
	}
}

func TestViewDependencyImpact(t *testing.T) {
	dev := map[string]string{"schema.sql": `
CREATE TABLE users (id integer, email text);
CREATE VIEW active_users AS SELECT id, email FROM users;
CREATE VIEW active_user_ids AS SELECT id FROM active_users;`}
	prod := map[string]string{"schema.sql": `
CREATE TABLE users (id integer);
CREATE VIEW active_users AS SELECT id FROM users;
CREATE VIEW active_user_ids AS SELECT id FROM active_users;`}
	report := compareSchemas(t, dev, prod, prod)
	var pair PairReport
	for _, candidate := range report.Pairs {
		if candidate.From == "development" && candidate.To == "production" {
			pair = candidate
		}
	}
	if len(pair.Views) != 2 {
		t.Fatalf("expected two impacted views, got %+v", pair.Views)
	}
	if pair.Views[0].View != "active_user_ids" || !pair.Views[0].Transitive {
		t.Fatalf("expected transitive active_user_ids impact, got %+v", pair.Views[0])
	}
	if pair.Views[1].View != "active_users" || pair.Views[1].Transitive {
		t.Fatalf("expected direct active_users impact, got %+v", pair.Views[1])
	}
}

func TestEnumsAndCompositeTypes(t *testing.T) {
	dev := map[string]string{"schema.sql": `
CREATE TYPE mood AS ENUM ('happy', 'sad');
CREATE TYPE profile AS (display_name text, age integer);`}
	prod := map[string]string{"schema.sql": `
CREATE TYPE mood AS ENUM ('happy', 'angry');
CREATE TYPE profile AS (display_name varchar(20), age bigint);`}
	diffs := pairDiffs(t, compareSchemas(t, dev, prod, prod), "development", "production")
	for _, kind := range []string{
		"enum_value_added",
		"enum_value_removed",
		"composite_field_type_changed",
	} {
		if findDiff(diffs, kind, "") == nil {
			t.Fatalf("missing %s in %+v", kind, diffs)
		}
	}
}

func TestParseFailureNamesFileAndAborts(t *testing.T) {
	root := t.TempDir()
	dev := writeSchema(t, root, "development", map[string]string{"good.sql": `CREATE TABLE ok (id integer);`})
	staging := writeSchema(t, root, "staging", map[string]string{"broken.sql": `CREATE TABLE broken (`})
	prod := writeSchema(t, root, "production", map[string]string{"good.sql": `CREATE TABLE ok (id integer);`})
	report, err := Compare(Options{
		Engine:       config.EnginePostgreSQL,
		Environments: []Environment{dev, staging, prod},
	})
	if err == nil {
		t.Fatal("expected parse error")
	}
	if report != nil {
		t.Fatal("partial report must not be returned")
	}
	if !strings.Contains(err.Error(), filepath.Join(staging.Schema[0], "broken.sql")) {
		t.Fatalf("error should name the failing file: %s", err)
	}
}

func TestGateFiltersAndOutputIsStable(t *testing.T) {
	dev := map[string]string{"schema.sql": `
CREATE TABLE users (
  id integer PRIMARY KEY,
  email text DEFAULT 'dev',
  removed integer
);`}
	prod := map[string]string{"schema.sql": `
CREATE TABLE users (
  id integer PRIMARY KEY,
  email text DEFAULT 'prod'
);`}
	report := compareSchemas(t, dev, prod, prod)
	full := report.RenderFull()
	gate := report.RenderGate()
	if !strings.Contains(full, "default_changed") {
		t.Fatalf("full report should include non-gate differences:\n%s", full)
	}
	if strings.Contains(gate, "default_changed") {
		t.Fatalf("gate report included a non-incompatible difference:\n%s", gate)
	}
	if !strings.Contains(gate, "column_removed") {
		t.Fatalf("gate report omitted an incompatible difference:\n%s", gate)
	}
	if second := report.RenderFull(); second != full {
		t.Fatal("full report is not deterministic")
	}
	if second := report.RenderGate(); second != gate {
		t.Fatal("gate report is not deterministic")
	}
}

func TestSummaryThresholdGroupsByTable(t *testing.T) {
	dev := map[string]string{"schema.sql": `
CREATE TABLE users (id integer, a text, b text, c text);`}
	prod := map[string]string{"schema.sql": `CREATE TABLE users (id integer);`}
	root := t.TempDir()
	report, err := Compare(Options{
		Engine: config.EnginePostgreSQL,
		Environments: []Environment{
			writeSchema(t, root, "development", dev),
			writeSchema(t, root, "staging", prod),
			writeSchema(t, root, "production", prod),
		},
		SummaryThreshold: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered := report.RenderFull()
	if !strings.Contains(rendered, "Summary by table") {
		t.Fatalf("expected summary mode:\n%s", rendered)
	}
	if strings.Contains(rendered, "column `a`") {
		t.Fatalf("summary mode should not emit individual differences:\n%s", rendered)
	}
	complete := report.RenderComplete()
	if !strings.Contains(complete, "column `a`") {
		t.Fatalf("complete report should retain every difference:\n%s", complete)
	}
}

func TestThreeEnvironmentAcceptanceReport(t *testing.T) {
	root := filepath.Join("testdata", "acceptance")
	report, err := Compare(Options{
		Engine: config.EnginePostgreSQL,
		Environments: []Environment{
			{Name: "development", Schema: []string{filepath.Join(root, "development.sql")}},
			{Name: "staging", Schema: []string{filepath.Join(root, "staging.sql")}},
			{Name: "production", Schema: []string{filepath.Join(root, "production.sql")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.IncompatibleCount() != 3 {
		t.Fatalf("expected 3 incompatible development/production differences, got %d:\n%s",
			report.IncompatibleCount(), report.RenderGate())
	}
	gate := report.RenderGate()
	for _, expected := range []string{"type_changed", "nullability_changed", "enum_value_removed"} {
		if !strings.Contains(gate, expected) {
			t.Fatalf("gate report missing %s:\n%s", expected, gate)
		}
	}
	for _, excluded := range []string{"default_changed", "unique_changed", "partition_changed"} {
		if strings.Contains(gate, excluded) {
			t.Fatalf("gate report included non-incompatible %s:\n%s", excluded, gate)
		}
	}

	full := report.RenderFull()
	for _, section := range []string{"Partition changes", "View dependency impact", "foreign_key_changed"} {
		if !strings.Contains(full, section) {
			t.Fatalf("full report missing %s:\n%s", section, full)
		}
	}
	expected, err := os.ReadFile(filepath.Join(root, "schema-drift-report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if full != string(expected) {
		t.Fatalf("acceptance report changed; regenerate it with sqlc schema-drift\nexpected:\n%s\nactual:\n%s", expected, full)
	}
}
