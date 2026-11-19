package schemadrift

import (
	"fmt"
	"sort"
	"strings"
)

func (r *Report) RenderFull() string {
	return r.renderReport(r.SummaryThreshold)
}

// RenderComplete always emits every difference. The report file uses this so
// a terminal summary threshold never hides data from manual investigation.
func (r *Report) RenderComplete() string {
	return r.renderReport(0)
}

func (r *Report) renderReport(threshold int) string {
	var out strings.Builder
	out.WriteString("# Schema drift report\n\n")
	fmt.Fprintf(&out, "- generated code source: `%s`\n", r.Source)
	fmt.Fprintf(&out, "- gate target: `%s`\n", r.GateTarget)
	out.WriteString("- comparison mode: all environments\n\n")

	if len(r.Pairs) == 0 {
		out.WriteString("No schema differences found.\n")
		return out.String()
	}
	for i, pair := range r.Pairs {
		if i > 0 {
			out.WriteString("\n")
		}
		renderPair(&out, pair, false, threshold)
	}
	return out.String()
}

func (r *Report) RenderGate() string {
	pair, ok := r.GatePair()
	if !ok {
		return "No compatible gate comparison was configured.\n"
	}
	filtered := PairReport{From: pair.From, To: pair.To}
	for _, diff := range pair.Diffs {
		if diff.Incompatible {
			filtered.Diffs = append(filtered.Diffs, diff)
		}
	}

	var out strings.Builder
	out.WriteString("# Schema drift gate\n\n")
	fmt.Fprintf(&out, "- generated code source: `%s`\n", r.Source)
	fmt.Fprintf(&out, "- target database: `%s`\n", r.GateTarget)
	if len(filtered.Diffs) == 0 {
		out.WriteString("- result: no incompatible differences\n")
		return out.String()
	}
	fmt.Fprintf(&out, "- result: %d incompatible difference(s)\n\n", len(filtered.Diffs))
	renderPair(&out, filtered, true, r.SummaryThreshold)
	return out.String()
}

func (r *Report) GatePair() (PairReport, bool) {
	for _, pair := range r.Pairs {
		if pair.From == r.Source && pair.To == r.GateTarget {
			return pair, true
		}
	}
	return PairReport{}, false
}

func (r *Report) HasIncompatible() bool {
	pair, ok := r.GatePair()
	if !ok {
		return false
	}
	for _, diff := range pair.Diffs {
		if diff.Incompatible {
			return true
		}
	}
	return false
}

func (r *Report) IncompatibleCount() int {
	pair, ok := r.GatePair()
	if !ok {
		return 0
	}
	var count int
	for _, diff := range pair.Diffs {
		if diff.Incompatible {
			count++
		}
	}
	return count
}

func (r *Report) DiffCount() int {
	var count int
	for _, pair := range r.Pairs {
		count += len(pair.Diffs)
	}
	return count
}

func (r *Report) Summary() string {
	if r.DiffCount() == 0 {
		return "schema drift: no differences"
	}
	return fmt.Sprintf(
		"schema drift: %d difference(s), %d incompatible with %s (full report available)",
		r.DiffCount(), r.IncompatibleCount(), r.GateTarget,
	)
}

func renderPair(out *strings.Builder, pair PairReport, gate bool, threshold int) {
	fmt.Fprintf(out, "## %s -> %s\n\n", pair.From, pair.To)
	counts := severityCounts(pair.Diffs)
	fmt.Fprintf(out, "- differences: %d (%s)\n", len(pair.Diffs), formatSeverityCounts(counts))
	incompatible := incompatibleCount(pair.Diffs)
	fmt.Fprintf(out, "- incompatible (code generated from %s against %s): %d\n\n", pair.From, pair.To, incompatible)

	if len(pair.Diffs) == 0 {
		out.WriteString("No differences.\n")
		return
	}
	if threshold > 0 && len(pair.Diffs) > threshold {
		renderSummaryByTable(out, pair.Diffs)
		renderViewImpacts(out, pair.Views)
		return
	}

	main, partitions := splitDiffs(pair.Diffs)
	main = filterDiffs(main, func(d Diff) bool {
		return !strings.HasPrefix(d.Kind, "type_") &&
			!strings.HasPrefix(d.Kind, "enum_") &&
			!strings.HasPrefix(d.Kind, "composite_") &&
			!strings.HasPrefix(d.Kind, "view_")
	})
	renderGroupedDiffs(out, "Table and column differences", main)
	renderGroupedDiffs(out, "Type differences", filterDiffs(pair.Diffs, func(d Diff) bool {
		return strings.HasPrefix(d.Kind, "type_") || strings.HasPrefix(d.Kind, "enum_") ||
			strings.HasPrefix(d.Kind, "composite_")
	}))
	renderGroupedDiffs(out, "View differences", filterDiffs(pair.Diffs, func(d Diff) bool {
		return strings.HasPrefix(d.Kind, "view_")
	}))
	renderGroupedDiffs(out, "Partition changes", partitions)
	renderViewImpacts(out, pair.Views)
	if gate {
		return
	}
}

func renderSummaryByTable(out *strings.Builder, diffs []Diff) {
	grouped := groupDiffs(diffs)
	out.WriteString("### Summary by table\n\n")
	for _, key := range sortedGroupKeys(grouped) {
		items := grouped[key]
		fmt.Fprintf(out, "- `%s`: %d difference(s) (%s)\n", key, len(items), formatSeverityCounts(severityCounts(items)))
	}
	out.WriteString("\n")
}

func renderGroupedDiffs(out *strings.Builder, title string, diffs []Diff) {
	if len(diffs) == 0 {
		return
	}
	fmt.Fprintf(out, "### %s\n\n", title)
	grouped := groupDiffs(diffs)
	for _, key := range sortedGroupKeys(grouped) {
		fmt.Fprintf(out, "#### %s\n", key)
		for _, diff := range grouped[key] {
			gate := ""
			if diff.Incompatible {
				gate = " `gate`"
			}
			column := ""
			if diff.Column != "" {
				column = fmt.Sprintf(" column `%s`", diff.Column)
			}
			fmt.Fprintf(out, "- `%s`%s `%s`%s: %s\n", diff.Severity, gate, diff.Kind, column, diff.Detail)
		}
		out.WriteString("\n")
	}
}

func renderViewImpacts(out *strings.Builder, impacts []ViewImpact) {
	if len(impacts) == 0 {
		return
	}
	out.WriteString("### View dependency impact\n\n")
	for _, impact := range impacts {
		kind := "direct"
		if impact.Transitive {
			kind = "transitive"
		}
		fmt.Fprintf(out, "- `%s` (%s): depends on changed table(s) %s\n",
			impact.View, kind, strings.Join(codeList(impact.Tables), ", "))
	}
	out.WriteString("\n")
}

func splitDiffs(diffs []Diff) (main, partitions []Diff) {
	for _, diff := range diffs {
		if diff.Partition {
			partitions = append(partitions, diff)
			continue
		}
		main = append(main, diff)
	}
	return main, partitions
}

func filterDiffs(diffs []Diff, keep func(Diff) bool) []Diff {
	var out []Diff
	for _, diff := range diffs {
		if keep(diff) {
			out = append(out, diff)
		}
	}
	return out
}

func groupDiffs(diffs []Diff) map[string][]Diff {
	grouped := map[string][]Diff{}
	for _, diff := range diffs {
		key := diff.Table
		if key == "" {
			key = diff.Object
		}
		if key == "" {
			key = "<global>"
		}
		grouped[key] = append(grouped[key], diff)
	}
	return grouped
}

func sortedGroupKeys(grouped map[string][]Diff) []string {
	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func severityCounts(diffs []Diff) map[string]int {
	counts := map[string]int{
		SeverityCritical: 0,
		SeverityHigh:     0,
		SeverityMedium:   0,
		SeverityLow:      0,
	}
	for _, diff := range diffs {
		counts[diff.Severity]++
	}
	return counts
}

func formatSeverityCounts(counts map[string]int) string {
	return fmt.Sprintf(
		"critical %d, high %d, medium %d, low %d",
		counts[SeverityCritical], counts[SeverityHigh], counts[SeverityMedium], counts[SeverityLow],
	)
}

func incompatibleCount(diffs []Diff) int {
	var count int
	for _, diff := range diffs {
		if diff.Incompatible {
			count++
		}
	}
	return count
}

func codeList(items []string) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = "`" + item + "`"
	}
	return out
}
