package config

import (
	"strings"
	"testing"
)

func TestSchemaDriftDefaultsOff(t *testing.T) {
	conf, err := ParseConfig(strings.NewReader(`{
  "version": "2",
  "sql": [{
    "engine": "postgresql",
    "schema": "schema.sql",
    "queries": "query.sql",
    "gen": {"go": {"package": "db", "out": "db"}}
  }]
}`))
	if err != nil {
		t.Fatal(err)
	}
	if conf.SQL[0].SchemaDrift != nil {
		t.Fatal("schema drift must be disabled unless explicitly configured")
	}
}

func TestSchemaDriftConfig(t *testing.T) {
	conf, err := ParseConfig(strings.NewReader(`{
  "version": "2",
  "sql": [{
    "engine": "postgresql",
    "schema": "schema.sql",
    "queries": "query.sql",
    "schema_drift": {
      "enabled": true,
      "staging": "schema/staging",
      "production": "schema/production",
      "gate": true,
      "report": "schema-drift.md",
      "summary_threshold": 25
    },
    "gen": {"go": {"package": "db", "out": "db"}}
  }]
}`))
	if err != nil {
		t.Fatal(err)
	}
	drift := conf.SQL[0].SchemaDrift
	if drift == nil || !drift.Enabled || !drift.Gate {
		t.Fatalf("unexpected schema drift config: %+v", drift)
	}
	if len(drift.Development) != 0 {
		t.Fatal("development should fall back to sql.schema when omitted")
	}
	if len(drift.Staging) != 1 || len(drift.Production) != 1 {
		t.Fatalf("unexpected environment paths: %+v", drift)
	}
	if drift.SummaryThreshold != 25 || drift.Report != "schema-drift.md" {
		t.Fatalf("unexpected reporting config: %+v", drift)
	}
}

func TestSchemaDriftValidation(t *testing.T) {
	conf := &Config{
		SQL: []SQL{{
			Schema: Paths{"dev"},
			SchemaDrift: &SchemaDrift{
				Enabled: true,
			},
		}},
	}
	if err := Validate(conf); err == nil || !strings.Contains(err.Error(), "staging") {
		t.Fatalf("expected staging validation error, got %v", err)
	}

	conf.SQL[0].SchemaDrift.Staging = Paths{"staging"}
	if err := Validate(conf); err == nil || !strings.Contains(err.Error(), "production") {
		t.Fatalf("expected production validation error, got %v", err)
	}

	conf.SQL[0].SchemaDrift.Production = Paths{"production"}
	if err := Validate(conf); err != nil {
		t.Fatalf("expected valid schema drift config, got %v", err)
	}
}
