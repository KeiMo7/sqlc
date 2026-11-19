package nullability

import "testing"

func TestBuildKeepsEveryConclusionAndFilters(t *testing.T) {
	snapshots := []QuerySnapshot{
		{
			QueryID:   "query.sql:GetBio",
			QueryName: "GetBio",
			Filename:  "query.sql",
			Engine:    "postgresql",
			Package:   "querytest",
			Columns: []ColumnSnapshot{{
				Position:     1,
				Field:        "bio",
				Table:        "profiles",
				SourceColumn: "bio",
				Nullable:     false,
				Origin:       SchemaColumn("public", "profiles", "bio", true, false),
			}},
		},
		{
			QueryID:   "query.sql:ListProfiles",
			QueryName: "ListProfiles",
			Filename:  "query.sql",
			Engine:    "postgresql",
			Package:   "querytest",
			Columns: []ColumnSnapshot{{
				Position:     1,
				Field:        "bio",
				Table:        "profiles",
				SourceColumn: "bio",
				Nullable:     true,
				Origin: QueryRule(true, RuleOuterJoin, "profiles.bio is on the nullable side of an outer join").
					Then(true, CauseQuery, RuleOuterJoin, "profiles.bio is on the nullable side of an outer join"),
			}},
		},
		{
			QueryID:   "query.sql:GetEmail",
			QueryName: "GetEmail",
			Filename:  "query.sql",
			Engine:    "postgresql",
			Package:   "querytest",
			Columns: []ColumnSnapshot{{
				Position:     1,
				Field:        "email",
				Table:        "users",
				SourceColumn: "email",
				Nullable:     true,
				Origin:       SchemaColumn("public", "users", "email", false, false),
			}},
		},
	}

	artifact := Build(snapshots, Filters{Fields: []string{"profiles.bio"}})
	if len(artifact.Queries) != 2 {
		t.Fatalf("queries = %d, want 2", len(artifact.Queries))
	}
	for _, query := range artifact.Queries {
		if len(query.Columns) != 1 || query.Columns[0].SourceColumn != "bio" {
			t.Fatalf("filtered query %s has unexpected columns: %#v", query.QueryID, query.Columns)
		}
	}
	if len(artifact.Fields) != 1 || artifact.Fields[0].Column != "bio" {
		t.Fatalf("fields = %#v, want only profiles.bio", artifact.Fields)
	}
	if len(artifact.Fields[0].Conclusions) != 2 {
		t.Fatalf("conclusions = %d, want both occurrences", len(artifact.Fields[0].Conclusions))
	}
	if len(artifact.CrossQueryDiffs) != 1 {
		t.Fatalf("cross-query differences = %d, want 1", len(artifact.CrossQueryDiffs))
	}
	if artifact.CrossQueryDiffs[0].Conclusions[1].Cause != CauseQuery {
		t.Fatalf("difference did not retain the query-shape cause: %#v", artifact.CrossQueryDiffs[0])
	}

	usersOnly := Build(snapshots, Filters{Tables: []string{"users"}})
	if len(usersOnly.Queries) != 1 || usersOnly.Queries[0].QueryID != "query.sql:GetEmail" {
		t.Fatalf("table filter returned unexpected queries: %#v", usersOnly.Queries)
	}
}

func TestBuildShowsDialectDifferencesSideBySide(t *testing.T) {
	snapshots := []QuerySnapshot{
		{
			QueryID:   "query.sql:GetUser",
			QueryName: "GetUser",
			Filename:  "query.sql",
			Engine:    "postgresql",
			Package:   "pg",
			Columns: []ColumnSnapshot{{
				Position:     1,
				Field:        "score",
				Table:        "users",
				SourceColumn: "score",
				Nullable:     true,
				Origin:       QueryRule(true, RuleAggregateNullable, "sum is nullable in this dialect"),
			}},
		},
		{
			QueryID:   "query.sql:GetUser",
			QueryName: "GetUser",
			Filename:  "query.sql",
			Engine:    "mysql",
			Package:   "my",
			Columns: []ColumnSnapshot{{
				Position:     1,
				Field:        "score",
				Table:        "users",
				SourceColumn: "score",
				Nullable:     false,
				Origin:       SchemaColumn("", "users", "score", true, false),
			}},
		},
	}

	artifact := Build(snapshots, Filters{})
	if len(artifact.DialectDifferences) != 1 {
		t.Fatalf("dialect differences = %d, want 1", len(artifact.DialectDifferences))
	}
	diff := artifact.DialectDifferences[0]
	if diff.QueryName != "GetUser" || diff.Field != "score" {
		t.Fatalf("unexpected dialect difference: %#v", diff)
	}
	if len(diff.Conclusions) != 2 {
		t.Fatalf("dialect conclusions = %d, want 2", len(diff.Conclusions))
	}
	if diff.Conclusions[0].Engine == diff.Conclusions[1].Engine {
		t.Fatalf("dialect difference did not keep both engines: %#v", diff.Conclusions)
	}
}

func TestFallbackStatesConservativeDirection(t *testing.T) {
	artifact := Build([]QuerySnapshot{{
		QueryID:   "query.sql:Unknown",
		QueryName: "Unknown",
		Engine:    "postgresql",
		Columns: []ColumnSnapshot{{
			Position: 1,
			Field:    "value",
			Nullable: true,
		}},
	}}, Filters{})
	if got := artifact.Queries[0].Columns[0].ConservativePolicy; got != "nullable" {
		t.Fatalf("conservative policy = %q, want nullable", got)
	}
	if artifact.Queries[0].Columns[0].Rule != RuleFallback {
		t.Fatalf("rule = %q, want %q", artifact.Queries[0].Columns[0].Rule, RuleFallback)
	}
}
