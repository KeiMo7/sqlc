// Package nullability records why the existing analysis decided that a query
// column may be NULL. It does not decide nullability itself: callers create an
// Origin at the point their existing rule makes that decision.
package nullability

import (
	"fmt"
	"sort"
	"strings"
)

// Cause separates a conclusion that came from the schema from one introduced
// by the shape of a query.
type Cause string

const (
	CauseSchema   Cause = "schema"
	CauseQuery    Cause = "query"
	CauseFallback Cause = "fallback"
)

const (
	RuleSchemaNullable       = "schema.column_nullable"
	RuleSchemaNotNull        = "schema.column_not_null"
	RuleSchemaNotNullDefault = "schema.column_not_null_with_default"
	RuleViewProjected        = "view.projected_column"
	RuleOuterJoin            = "query.outer_join"
	RuleConstant             = "query.constant"
	RulePredicate            = "query.predicate"
	RuleOperator             = "query.operator"
	RuleBoolean              = "query.boolean_expression"
	RuleCase                 = "query.expression.case"
	RuleCoalesce             = "query.expression.coalesce"
	RuleCast                 = "query.expression.cast"
	RuleFunction             = "query.function.return"
	RuleAggregateCount       = "query.aggregate.count"
	RuleAggregateNullable    = "query.aggregate.nullable"
	RuleAggregateNotNull     = "query.aggregate.not_null"
	RuleSubqueryExists       = "query.subquery.exists"
	RuleSubqueryScalar       = "query.subquery.scalar"
	RuleSubqueryArray        = "query.subquery.array"
	RuleFallback             = "fallback.unknown_rule"
	RuleFallbackInconsistent = "fallback.unexplained_decision"
)

// Step is one rule in a derivation chain, in the order the rules ran.
type Step struct {
	Nullable bool   `json:"nullable"`
	Cause    Cause  `json:"cause"`
	Rule     string `json:"rule"`
	Detail   string `json:"detail"`
}

// Origin is the final rule and the chain that led to it.
type Origin struct {
	Nullable bool   `json:"nullable"`
	Cause    Cause  `json:"cause"`
	Rule     string `json:"rule"`
	Detail   string `json:"detail"`
	Steps    []Step `json:"steps,omitempty"`
}

// New starts a derivation chain.
func New(nullable bool, cause Cause, rule, detail string) *Origin {
	step := Step{
		Nullable: nullable,
		Cause:    cause,
		Rule:     rule,
		Detail:   detail,
	}
	return &Origin{
		Nullable: nullable,
		Cause:    cause,
		Rule:     rule,
		Detail:   detail,
		Steps:    []Step{step},
	}
}

// SchemaColumn records a conclusion taken from a column declaration. The
// caller has already read the declaration's NOT NULL flag.
func SchemaColumn(schema, table, column string, notNull, hasDefault bool) *Origin {
	name := qualified(schema, table, column)
	nullable := !notNull
	rule := RuleSchemaNullable
	detail := fmt.Sprintf("column %s is declared nullable", name)
	if notNull {
		rule = RuleSchemaNotNull
		detail = fmt.Sprintf("column %s is declared NOT NULL", name)
		if hasDefault {
			rule = RuleSchemaNotNullDefault
			detail = fmt.Sprintf("column %s is declared NOT NULL with a default; the default does not make the column nullable", name)
		}
	}
	return New(nullable, CauseSchema, rule, detail)
}

// QueryRule records a conclusion made by one of the query-shape rules.
func QueryRule(nullable bool, rule, detail string) *Origin {
	return New(nullable, CauseQuery, rule, detail)
}

// Fallback records a conservative conclusion when the existing analysis had no
// more specific rule to report.
func Fallback(nullable bool) *Origin {
	direction := "NOT NULL"
	if nullable {
		direction = "NULL"
	}
	return New(
		nullable,
		CauseFallback,
		RuleFallback,
		fmt.Sprintf("no specific rule was available; the conservative policy treats this field as %s", direction),
	)
}

// Exact makes o agree with the nullability the existing analysis produced. A
// mismatch means the instrumentation missed the rule, so the chain becomes a
// fallback rather than claiming a rule that did not run.
func Exact(o *Origin, nullable bool) *Origin {
	if o == nil {
		return Fallback(nullable)
	}
	out := o.Clone()
	if out.Nullable == nullable {
		return out
	}
	direction := "NOT NULL"
	if nullable {
		direction = "NULL"
	}
	return New(
		nullable,
		CauseFallback,
		RuleFallbackInconsistent,
		fmt.Sprintf("the recorded rule did not explain the decision; the conservative policy keeps the existing %s result", direction),
	)
}

// Clone returns an independent copy.
func (o *Origin) Clone() *Origin {
	if o == nil {
		return nil
	}
	out := *o
	out.Steps = append([]Step(nil), o.Steps...)
	return &out
}

// Append adds a contributing rule without changing the final rule. This is
// for facts such as a view projection that preserve an earlier conclusion.
func (o *Origin) Append(rule string, cause Cause, detail string) *Origin {
	if o == nil {
		return nil
	}
	out := o.Clone()
	out.Steps = append(out.Steps, Step{
		Nullable: out.Nullable,
		Cause:    cause,
		Rule:     rule,
		Detail:   detail,
	})
	return out
}

// Then adds a rule that changes the final conclusion.
func (o *Origin) Then(nullable bool, cause Cause, rule, detail string) *Origin {
	if o == nil {
		return New(nullable, cause, rule, detail)
	}
	out := o.Clone()
	out.Nullable = nullable
	out.Cause = cause
	out.Rule = rule
	out.Detail = detail
	out.Steps = append(out.Steps, Step{
		Nullable: nullable,
		Cause:    cause,
		Rule:     rule,
		Detail:   detail,
	})
	return out
}

func qualified(schema, table, column string) string {
	var parts []string
	for _, part := range []string{schema, table, column} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ".")
}

// Filters select the source relations and fields included in the artifact.
// Tables and fields are ANDed when both are set.
type Filters struct {
	Tables []string `json:"tables,omitempty"`
	Fields []string `json:"fields,omitempty"`
}

// ColumnSnapshot is what the compiler observed for one output field.
type ColumnSnapshot struct {
	Position     int
	Field        string
	Schema       string
	Table        string
	SourceColumn string
	DataType     string
	Nullable     bool
	Origin       *Origin
}

// QuerySnapshot is one analyzed query under one database dialect.
type QuerySnapshot struct {
	QueryID   string
	QueryName string
	Filename  string
	Engine    string
	Package   string
	SQL       string
	Columns   []ColumnSnapshot
}

type Artifact struct {
	Version            int                 `json:"version"`
	Filters            Filters             `json:"filters"`
	Queries            []QueryExplanation  `json:"queries"`
	Fields             []FieldExplanation  `json:"fields"`
	CrossQueryDiffs    []Difference        `json:"cross_query_differences,omitempty"`
	DialectDifferences []DialectDifference `json:"dialect_differences,omitempty"`
}

type QueryExplanation struct {
	QueryID   string              `json:"query_id"`
	QueryName string              `json:"query_name"`
	Filename  string              `json:"filename,omitempty"`
	Engine    string              `json:"engine"`
	Package   string              `json:"package,omitempty"`
	SQL       string              `json:"sql"`
	Columns   []ColumnExplanation `json:"columns"`
}

type ColumnExplanation struct {
	Position           int    `json:"position"`
	Field              string `json:"field"`
	Schema             string `json:"schema,omitempty"`
	Table              string `json:"table,omitempty"`
	SourceColumn       string `json:"source_column,omitempty"`
	DataType           string `json:"data_type,omitempty"`
	Nullable           bool   `json:"nullable"`
	Cause              Cause  `json:"cause"`
	Rule               string `json:"rule"`
	Reason             string `json:"reason"`
	Steps              []Step `json:"steps"`
	ConservativePolicy string `json:"conservative_policy,omitempty"`
}

type Conclusion struct {
	QueryID  string `json:"query_id"`
	Engine   string `json:"engine"`
	Package  string `json:"package,omitempty"`
	Nullable bool   `json:"nullable"`
	Cause    Cause  `json:"cause"`
	Rule     string `json:"rule"`
}

type FieldExplanation struct {
	Schema      string       `json:"schema,omitempty"`
	Table       string       `json:"table,omitempty"`
	Column      string       `json:"column"`
	Conclusions []Conclusion `json:"conclusions"`
}

type Difference struct {
	Key         string       `json:"key"`
	Reason      string       `json:"reason"`
	Conclusions []Conclusion `json:"conclusions"`
}

type DialectDifference struct {
	QueryName   string       `json:"query_name"`
	Field       string       `json:"field"`
	Table       string       `json:"table,omitempty"`
	Reason      string       `json:"reason"`
	Conclusions []Conclusion `json:"conclusions"`
}

// Build creates the deterministic sidecar document from compiler snapshots.
func Build(snapshots []QuerySnapshot, filters Filters) Artifact {
	artifact := Artifact{
		Version: 1,
		Filters: Filters{
			Tables: append([]string(nil), filters.Tables...),
			Fields: append([]string(nil), filters.Fields...),
		},
		Queries: []QueryExplanation{},
		Fields:  []FieldExplanation{},
	}

	for _, snapshot := range snapshots {
		query := QueryExplanation{
			QueryID:   snapshot.QueryID,
			QueryName: snapshot.QueryName,
			Filename:  snapshot.Filename,
			Engine:    snapshot.Engine,
			Package:   snapshot.Package,
			SQL:       snapshot.SQL,
			Columns:   []ColumnExplanation{},
		}
		for _, column := range snapshot.Columns {
			if !matches(filters, column) {
				continue
			}
			origin := Exact(column.Origin, column.Nullable)
			col := ColumnExplanation{
				Position:     column.Position,
				Field:        column.Field,
				Schema:       column.Schema,
				Table:        column.Table,
				SourceColumn: column.SourceColumn,
				DataType:     column.DataType,
				Nullable:     column.Nullable,
				Cause:        origin.Cause,
				Rule:         origin.Rule,
				Reason:       origin.Detail,
				Steps:        append([]Step(nil), origin.Steps...),
			}
			if origin.Cause == CauseFallback {
				if column.Nullable {
					col.ConservativePolicy = "nullable"
				} else {
					col.ConservativePolicy = "not_null"
				}
			}
			query.Columns = append(query.Columns, col)
		}
		if len(query.Columns) > 0 || (len(filters.Tables) == 0 && len(filters.Fields) == 0) {
			artifact.Queries = append(artifact.Queries, query)
		}
	}

	sort.Slice(artifact.Queries, func(i, j int) bool {
		a, b := artifact.Queries[i], artifact.Queries[j]
		if a.Engine != b.Engine {
			return a.Engine < b.Engine
		}
		if a.QueryID != b.QueryID {
			return a.QueryID < b.QueryID
		}
		return a.Package < b.Package
	})

	artifact.Fields, artifact.CrossQueryDiffs = buildFields(artifact.Queries)
	artifact.DialectDifferences = buildDialectDifferences(artifact.Queries)
	return artifact
}

func matches(filters Filters, column ColumnSnapshot) bool {
	if !matchesAny(filters.Tables, tableCandidates(column)) {
		return false
	}
	return matchesAny(filters.Fields, fieldCandidates(column))
}

func matchesAny(filters, candidates []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		for _, candidate := range candidates {
			if strings.EqualFold(strings.TrimSpace(filter), candidate) {
				return true
			}
		}
	}
	return false
}

func tableCandidates(column ColumnSnapshot) []string {
	if column.Table == "" {
		return nil
	}
	out := []string{column.Table}
	if column.Schema != "" {
		out = append(out, column.Schema+"."+column.Table)
	}
	return out
}

func fieldCandidates(column ColumnSnapshot) []string {
	out := []string{column.Field}
	if column.SourceColumn != "" {
		out = append(out, column.SourceColumn)
	}
	if column.Table != "" && column.SourceColumn != "" {
		out = append(out, column.Table+"."+column.SourceColumn)
		if column.Schema != "" {
			out = append(out, column.Schema+"."+column.Table+"."+column.SourceColumn)
		}
	}
	return out
}

func buildFields(queries []QueryExplanation) ([]FieldExplanation, []Difference) {
	type fieldKey struct {
		schema string
		table  string
		column string
	}
	groups := map[fieldKey][]Conclusion{}
	for _, query := range queries {
		for _, column := range query.Columns {
			columnName := column.SourceColumn
			if columnName == "" {
				columnName = column.Field
			}
			key := fieldKey{schema: column.Schema, table: column.Table, column: columnName}
			groups[key] = append(groups[key], Conclusion{
				QueryID:  query.QueryID,
				Engine:   query.Engine,
				Package:  query.Package,
				Nullable: column.Nullable,
				Cause:    column.Cause,
				Rule:     column.Rule,
			})
		}
	}

	keys := make([]fieldKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a := qualified(keys[i].schema, keys[i].table, keys[i].column)
		b := qualified(keys[j].schema, keys[j].table, keys[j].column)
		return a < b
	})

	fields := make([]FieldExplanation, 0, len(keys))
	var diffs []Difference
	for _, key := range keys {
		conclusions := append([]Conclusion(nil), groups[key]...)
		sort.Slice(conclusions, func(i, j int) bool {
			if conclusions[i].QueryID != conclusions[j].QueryID {
				return conclusions[i].QueryID < conclusions[j].QueryID
			}
			if conclusions[i].Engine != conclusions[j].Engine {
				return conclusions[i].Engine < conclusions[j].Engine
			}
			return conclusions[i].Package < conclusions[j].Package
		})
		fields = append(fields, FieldExplanation{
			Schema:      key.schema,
			Table:       key.table,
			Column:      key.column,
			Conclusions: conclusions,
		})
		if reason := queryShapeReason(conclusions); reason != "" {
			diffs = append(diffs, Difference{
				Key:         qualified(key.schema, key.table, key.column),
				Reason:      reason,
				Conclusions: conclusions,
			})
		}
	}
	return fields, diffs
}

func queryShapeReason(conclusions []Conclusion) string {
	if len(conclusions) < 2 {
		return ""
	}
	seen := map[string]struct{}{}
	var causes []string
	for _, conclusion := range conclusions {
		signature := fmt.Sprintf("%t/%s/%s", conclusion.Nullable, conclusion.Cause, conclusion.Rule)
		if _, ok := seen[signature]; ok {
			continue
		}
		seen[signature] = struct{}{}
		causes = append(causes, fmt.Sprintf("%s via %s (%s)", conclusion.QueryID, conclusion.Rule, conclusion.Cause))
	}
	if len(seen) < 2 {
		return ""
	}
	return "the same source column has different conclusions because the queries apply different rules: " + strings.Join(causes, "; ")
}

func buildDialectDifferences(queries []QueryExplanation) []DialectDifference {
	type dialectKey struct {
		queryName string
		field     string
		table     string
	}
	groups := map[dialectKey][]Conclusion{}
	for _, query := range queries {
		for _, column := range query.Columns {
			key := dialectKey{
				queryName: query.QueryName,
				field:     column.Field,
				table:     column.Table,
			}
			groups[key] = append(groups[key], Conclusion{
				QueryID:  query.QueryID,
				Engine:   query.Engine,
				Package:  query.Package,
				Nullable: column.Nullable,
				Cause:    column.Cause,
				Rule:     column.Rule,
			})
		}
	}

	var out []DialectDifference
	for key, conclusions := range groups {
		engines := map[string]struct{}{}
		signatures := map[string]struct{}{}
		for _, conclusion := range conclusions {
			engines[conclusion.Engine] = struct{}{}
			signatures[fmt.Sprintf("%t/%s", conclusion.Nullable, conclusion.Cause)] = struct{}{}
		}
		if len(engines) < 2 || len(signatures) < 2 {
			continue
		}
		sort.Slice(conclusions, func(i, j int) bool {
			if conclusions[i].Engine != conclusions[j].Engine {
				return conclusions[i].Engine < conclusions[j].Engine
			}
			return conclusions[i].QueryID < conclusions[j].QueryID
		})
		out = append(out, DialectDifference{
			QueryName:   key.queryName,
			Field:       key.field,
			Table:       key.table,
			Reason:      "the same query and field reach different nullability conclusions in different database dialects",
			Conclusions: conclusions,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].QueryName != out[j].QueryName {
			return out[i].QueryName < out[j].QueryName
		}
		if out[i].Field != out[j].Field {
			return out[i].Field < out[j].Field
		}
		return out[i].Table < out[j].Table
	})
	return out
}
