package schemadrift

import (
	"fmt"
	"sort"
	"strings"
)

const (
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
)

// Compare parses each environment independently. If any file fails to parse,
// it returns before any comparison is performed.
func Compare(opts Options) (*Report, error) {
	if len(opts.Environments) < 2 {
		return nil, fmt.Errorf("schema drift requires at least two environments")
	}
	if opts.SummaryThreshold <= 0 {
		opts.SummaryThreshold = 200
	}

	envs := append([]Environment(nil), opts.Environments...)
	sort.SliceStable(envs, func(i, j int) bool {
		ri, rj := environmentRank(envs[i].Name), environmentRank(envs[j].Name)
		if ri != rj {
			return ri < rj
		}
		return envs[i].Name < envs[j].Name
	})

	models := make(map[string]*schemaModel, len(envs))
	for _, env := range envs {
		if strings.TrimSpace(env.Name) == "" {
			return nil, fmt.Errorf("schema drift environment name is empty")
		}
		if _, ok := models[env.Name]; ok {
			return nil, fmt.Errorf("duplicate schema drift environment %q", env.Name)
		}
		model, err := buildSchema(opts.Engine, env)
		if err != nil {
			return nil, err
		}
		models[env.Name] = model
	}

	source := envs[0].Name
	for _, env := range envs {
		if env.Name == "development" {
			source = env.Name
			break
		}
	}
	target := envs[len(envs)-1].Name
	for _, env := range envs {
		if env.Name == "production" {
			target = env.Name
			break
		}
	}

	report := &Report{
		Source:           source,
		GateTarget:       target,
		SummaryThreshold: opts.SummaryThreshold,
	}
	for i := 0; i < len(envs); i++ {
		for j := i + 1; j < len(envs); j++ {
			report.Pairs = append(report.Pairs, comparePair(envs[i].Name, envs[j].Name, models[envs[i].Name], models[envs[j].Name]))
		}
	}
	return report, nil
}

func environmentRank(name string) int {
	switch name {
	case "development":
		return 0
	case "staging":
		return 1
	case "production":
		return 2
	default:
		return 3
	}
}

func comparePair(fromName, toName string, from, to *schemaModel) PairReport {
	pair := PairReport{From: fromName, To: toName}
	changedTables := map[string]struct{}{}

	tableKeys := unionKeys(from.Tables, to.Tables)
	for _, key := range tableKeys {
		left, right := from.Tables[key], to.Tables[key]
		display := key
		if left != nil {
			display = left.Name
		} else if right != nil {
			display = right.Name
		}
		switch {
		case left == nil:
			pair.Diffs = append(pair.Diffs, Diff{
				From: fromName, To: toName, Table: display, Kind: "table_added",
				Severity: SeverityMedium, Detail: "table exists only in " + toName,
			})
		case right == nil:
			changedTables[key] = struct{}{}
			pair.Diffs = append(pair.Diffs, Diff{
				From: fromName, To: toName, Table: display, Kind: "table_removed",
				Severity: SeverityCritical, Incompatible: true, Detail: "table exists only in " + fromName,
			})
		default:
			diffs := compareTable(fromName, toName, left, right)
			if len(diffs) > 0 {
				changedTables[key] = struct{}{}
			}
			pair.Diffs = append(pair.Diffs, diffs...)
		}
	}

	typeKeys := unionKeys(from.Types, to.Types)
	for _, key := range typeKeys {
		left, right := from.Types[key], to.Types[key]
		display := key
		if left != nil {
			display = left.Name
		} else if right != nil {
			display = right.Name
		}
		switch {
		case left == nil:
			pair.Diffs = append(pair.Diffs, Diff{
				From: fromName, To: toName, Object: display, Kind: "type_added",
				Severity: SeverityMedium, Detail: "type exists only in " + toName,
			})
		case right == nil:
			pair.Diffs = append(pair.Diffs, Diff{
				From: fromName, To: toName, Object: display, Kind: "type_removed",
				Severity: SeverityCritical, Incompatible: true, Detail: "type exists only in " + fromName,
			})
		default:
			pair.Diffs = append(pair.Diffs, compareType(fromName, toName, left, right)...)
		}
	}

	viewKeys := unionKeys(from.Views, to.Views)
	for _, key := range viewKeys {
		left, right := from.Views[key], to.Views[key]
		display := key
		if left != nil {
			display = left.Name
		} else if right != nil {
			display = right.Name
		}
		switch {
		case left == nil:
			pair.Diffs = append(pair.Diffs, Diff{
				From: fromName, To: toName, Object: display, Kind: "view_added",
				Severity: SeverityMedium, Detail: "view exists only in " + toName,
			})
		case right == nil:
			pair.Diffs = append(pair.Diffs, Diff{
				From: fromName, To: toName, Object: display, Kind: "view_removed",
				Severity: SeverityCritical, Incompatible: true, Detail: "view exists only in " + fromName,
			})
		default:
			if !sameStrings(left.Dependencies, right.Dependencies) {
				pair.Diffs = append(pair.Diffs, Diff{
					From: fromName, To: toName, Object: display, Kind: "view_dependency_changed",
					Severity: SeverityMedium,
					Detail:   fmt.Sprintf("dependencies: [%s] -> [%s]", strings.Join(left.Dependencies, ", "), strings.Join(right.Dependencies, ", ")),
				})
			}
		}
	}

	pair.Views = impactedViews(from, to, changedTables)
	sortDiffs(pair.Diffs)
	return pair
}

func compareTable(fromName, toName string, from, to *tableDef) []Diff {
	var diffs []Diff
	table := from.Name
	if table == "" {
		table = to.Name
	}

	partitionChild := isPartitionChild(from) || isPartitionChild(to)
	if !partitionChild {
		common := 0
		for key := range from.Columns {
			if _, ok := to.Columns[key]; ok {
				common++
			}
		}
		if common == 0 && (len(from.Columns) > 0 || len(to.Columns) > 0) {
			diffs = append(diffs, Diff{
				From: fromName, To: toName, Table: table, Kind: "table_shape_changed",
				Severity: SeverityCritical, Incompatible: true,
				Detail: fmt.Sprintf("no shared columns; %s=[%s], %s=[%s]",
					fromName, strings.Join(columnNames(from), ", "),
					toName, strings.Join(columnNames(to), ", ")),
			})
		} else {
			for _, key := range unionKeys(from.Columns, to.Columns) {
				left, right := from.Columns[key], to.Columns[key]
				display := key
				if left != nil {
					display = left.Name
				} else if right != nil {
					display = right.Name
				}
				switch {
				case left == nil:
					diffs = append(diffs, Diff{
						From: fromName, To: toName, Table: table, Column: display, Kind: "column_added",
						Severity: SeverityMedium,
						Detail:   fmt.Sprintf("column exists only in %s (type %s)", toName, right.Type),
					})
				case right == nil:
					diffs = append(diffs, Diff{
						From: fromName, To: toName, Table: table, Column: display, Kind: "column_removed",
						Severity: SeverityCritical, Incompatible: true,
						Detail: fmt.Sprintf("column exists only in %s (type %s)", fromName, left.Type),
					})
				default:
					if left.Type != right.Type {
						diffs = append(diffs, Diff{
							From: fromName, To: toName, Table: table, Column: display, Kind: "type_changed",
							Severity: SeverityCritical, Incompatible: true,
							Detail: fmt.Sprintf("type: %s -> %s", left.Type, right.Type),
						})
					}
					if left.NotNull != right.NotNull {
						diffs = append(diffs, Diff{
							From: fromName, To: toName, Table: table, Column: display, Kind: "nullability_changed",
							Severity: SeverityHigh, Incompatible: true,
							Detail: fmt.Sprintf("nullability: %s -> %s", nullability(left.NotNull), nullability(right.NotNull)),
						})
					}
					if left.Default != right.Default {
						diffs = append(diffs, Diff{
							From: fromName, To: toName, Table: table, Column: display, Kind: "default_changed",
							Severity: SeverityMedium,
							Detail:   fmt.Sprintf("default: %s -> %s", valueOrNone(left.Default), valueOrNone(right.Default)),
						})
					}
				}
			}
		}
	}

	for _, key := range unionKeys(from.Constraints, to.Constraints) {
		left, right := from.Constraints[key], to.Constraints[key]
		if left != nil && right != nil {
			continue
		}
		con := left
		action := "removed in " + toName
		if con == nil {
			con = right
			action = "added in " + toName
		}
		kind := con.Kind + "_changed"
		incompatible := left != nil && con.Kind == "primary_key"
		diffs = append(diffs, Diff{
			From: fromName, To: toName, Table: table, Kind: kind,
			Severity: SeverityHigh, Incompatible: incompatible,
			Detail: action + ": " + constraintLabel(con),
		})
	}

	if !sameStrings(from.Inherits, to.Inherits) {
		diffs = append(diffs, Diff{
			From: fromName, To: toName, Table: table, Kind: "inheritance_changed",
			Severity: SeverityHigh,
			Detail:   fmt.Sprintf("parents: [%s] -> [%s]", strings.Join(from.Inherits, ", "), strings.Join(to.Inherits, ", ")),
		})
	}
	if from.Partition.identity() != to.Partition.identity() {
		diffs = append(diffs, Diff{
			From: fromName, To: toName, Table: table, Kind: "partition_changed",
			Severity: SeverityHigh, Partition: true,
			Detail: fmt.Sprintf("partition: %s -> %s", partitionLabel(from.Partition), partitionLabel(to.Partition)),
		})
	}
	return diffs
}

func isPartitionChild(t *tableDef) bool {
	return t != nil && t.Partition != nil && t.Partition.Parent != "" && t.Partition.Bound != ""
}

func compareType(fromName, toName string, from, to *typeDef) []Diff {
	var diffs []Diff
	if from.Kind != to.Kind {
		return []Diff{{
			From: fromName, To: toName, Object: from.Name, Kind: "type_kind_changed",
			Severity: SeverityCritical, Incompatible: true,
			Detail: fmt.Sprintf("type kind: %s -> %s", from.Kind, to.Kind),
		}}
	}
	switch from.Kind {
	case "enum":
		fromValues := map[string]struct{}{}
		toValues := map[string]struct{}{}
		for _, value := range from.Values {
			fromValues[value] = struct{}{}
		}
		for _, value := range to.Values {
			toValues[value] = struct{}{}
		}
		for _, value := range from.Values {
			if _, ok := toValues[value]; ok {
				continue
			}
			diffs = append(diffs, Diff{
				From: fromName, To: toName, Object: from.Name, Kind: "enum_value_removed",
				Severity: SeverityCritical, Incompatible: true,
				Detail: fmt.Sprintf("enum value %q exists only in %s", value, fromName),
			})
		}
		for _, value := range to.Values {
			if _, ok := fromValues[value]; ok {
				continue
			}
			diffs = append(diffs, Diff{
				From: fromName, To: toName, Object: from.Name, Kind: "enum_value_added",
				Severity: SeverityMedium,
				Detail:   fmt.Sprintf("enum value %q exists only in %s", value, toName),
			})
		}
		if len(fromValues) == len(toValues) && len(fromValues) > 0 && !sameStrings(from.Values, to.Values) && sameStringSets(from.Values, to.Values) {
			diffs = append(diffs, Diff{
				From: fromName, To: toName, Object: from.Name, Kind: "enum_value_order_changed",
				Severity: SeverityLow, Detail: "enum values have the same labels in a different order",
			})
		}
	case "composite":
		for _, key := range unionKeys(from.Fields, to.Fields) {
			left, leftOK := from.Fields[key]
			right, rightOK := to.Fields[key]
			switch {
			case !rightOK:
				diffs = append(diffs, Diff{
					From: fromName, To: toName, Object: from.Name, Column: key, Kind: "composite_field_removed",
					Severity: SeverityCritical, Incompatible: true,
					Detail: fmt.Sprintf("field %q exists only in %s", key, fromName),
				})
			case !leftOK:
				diffs = append(diffs, Diff{
					From: fromName, To: toName, Object: from.Name, Column: key, Kind: "composite_field_added",
					Severity: SeverityMedium,
					Detail:   fmt.Sprintf("field %q exists only in %s", key, toName),
				})
			case left != right:
				diffs = append(diffs, Diff{
					From: fromName, To: toName, Object: from.Name, Column: key, Kind: "composite_field_type_changed",
					Severity: SeverityCritical, Incompatible: true,
					Detail: fmt.Sprintf("field %q type: %s -> %s", key, left, right),
				})
			}
		}
	case "domain", "range":
		if from.Base != to.Base {
			diffs = append(diffs, Diff{
				From: fromName, To: toName, Object: from.Name, Kind: "type_base_changed",
				Severity: SeverityCritical, Incompatible: true,
				Detail: fmt.Sprintf("base type: %s -> %s", from.Base, to.Base),
			})
		}
	}
	return diffs
}

func impactedViews(from, to *schemaModel, changed map[string]struct{}) []ViewImpact {
	if len(changed) == 0 {
		return nil
	}
	views := map[string]*viewDef{}
	for key, view := range from.Views {
		views[key] = view
	}
	for key, view := range to.Views {
		if _, ok := views[key]; !ok {
			views[key] = view
		}
	}

	var out []ViewImpact
	for _, key := range sortedKeys(views) {
		view := views[key]
		roots := changedViewRoots(key, views, changed, map[string]struct{}{})
		if len(roots) == 0 {
			continue
		}
		direct := false
		for _, dep := range view.Dependencies {
			if _, ok := changed[dep]; ok {
				direct = true
				break
			}
		}
		out = append(out, ViewImpact{
			View:       view.Name,
			Tables:     roots,
			Transitive: !direct,
		})
	}
	return out
}

func changedViewRoots(key string, views map[string]*viewDef, changed map[string]struct{}, visiting map[string]struct{}) []string {
	if _, ok := visiting[key]; ok {
		return nil
	}
	view := views[key]
	if view == nil {
		return nil
	}
	visiting[key] = struct{}{}
	defer delete(visiting, key)
	seen := map[string]struct{}{}
	for _, dep := range view.Dependencies {
		if _, ok := changed[dep]; ok {
			seen[dep] = struct{}{}
			continue
		}
		if _, ok := views[dep]; ok {
			for _, root := range changedViewRoots(dep, views, changed, visiting) {
				seen[root] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for root := range seen {
		out = append(out, root)
	}
	sort.Strings(out)
	return out
}

func sortDiffs(diffs []Diff) {
	sort.SliceStable(diffs, func(i, j int) bool {
		a, b := diffs[i], diffs[j]
		if a.Table != b.Table {
			return a.Table < b.Table
		}
		if a.Object != b.Object {
			return a.Object < b.Object
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Detail < b.Detail
	})
}

func unionKeys[V any](a, b map[string]V) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for key := range a {
		seen[key] = struct{}{}
	}
	for key := range b {
		seen[key] = struct{}{}
	}
	return sortedKeys(seen)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func columnNames(t *tableDef) []string {
	out := make([]string, 0, len(t.Columns))
	for _, col := range t.Columns {
		out = append(out, col.Name)
	}
	sort.Strings(out)
	return out
}

func nullability(notNull bool) string {
	if notNull {
		return "not null"
	}
	return "nullable"
}

func valueOrNone(value string) string {
	if value == "" {
		return "<none>"
	}
	return value
}

func constraintLabel(con *constraintDef) string {
	if con == nil {
		return ""
	}
	var parts []string
	if con.Name != "" {
		parts = append(parts, "name="+con.Name)
	}
	if len(con.Columns) > 0 {
		parts = append(parts, "columns=["+strings.Join(con.Columns, ", ")+"]")
	}
	if con.RefTable != "" {
		parts = append(parts, "references="+con.RefTable+"("+strings.Join(con.RefColumns, ", ")+")")
	}
	if con.Expression != "" {
		parts = append(parts, "expression="+con.Expression)
	}
	return con.Kind + "(" + strings.Join(parts, ", ") + ")"
}

func partitionLabel(partition *partitionDef) string {
	if partition == nil {
		return "<none>"
	}
	var parts []string
	if partition.Strategy != "" {
		parts = append(parts, "strategy="+partition.Strategy)
	}
	if len(partition.Columns) > 0 {
		parts = append(parts, "columns=["+strings.Join(partition.Columns, ", ")+"]")
	}
	if partition.Bound != "" {
		parts = append(parts, "bound="+partition.Bound)
	}
	if partition.Parent != "" {
		parts = append(parts, "parent="+partition.Parent)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameStringSets(a, b []string) bool {
	set := make(map[string]int, len(a))
	for _, value := range a {
		set[value]++
	}
	for _, value := range b {
		set[value]--
	}
	for _, count := range set {
		if count != 0 {
			return false
		}
	}
	return true
}
