package compiler

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/sqlc-dev/sqlc/internal/source"
	"github.com/sqlc-dev/sqlc/internal/sql/ast"
	"github.com/sqlc-dev/sqlc/internal/sql/astutils"
	"github.com/sqlc-dev/sqlc/internal/sql/sqlerr"
)

type objectDefinition struct {
	Filename  string
	Offset    int
	EndOffset int
	Line      int
	Column    int
	Snippet   string
	Kind      string
	Schema    string
	Name      string
}

type errorInfo struct {
	Kind     string
	Object   string
	Symbol   string
	Relation string
}

var (
	columnOfRelation = regexp.MustCompile(`column "([^"]+)" of relation "([^"]+)"`)
	columnMissing    = regexp.MustCompile(`(?:column(?: reference)?|unknown column) "([^"]+)"`)
	columnAmbiguous  = regexp.MustCompile(`column reference "([^"]+)" is ambiguous`)
	relationMissing  = regexp.MustCompile(`relation "([^"]+)"`)
	functionMissing  = regexp.MustCompile(`function "([^"]+)"`)
	schemaMissing    = regexp.MustCompile(`schema "([^"]+)"`)
	typeMissing      = regexp.MustCompile(`type "([^"]+)"`)
	nearToken        = regexp.MustCompile(`(?:near|at or near) "([^"]+)"`)
	lineColumn       = regexp.MustCompile(`line (\d+), column (\d+)`)
)

var sqlKeywords = []string{
	"ALL", "ALTER", "AND", "AS", "ASC", "BEGIN", "BETWEEN", "BY", "CASE", "CAST",
	"COMMIT", "CREATE", "DELETE", "DESC", "DISTINCT", "DROP", "ELSE", "END", "EXISTS",
	"FALSE", "FOR", "FROM", "GROUP", "HAVING", "IN", "INNER", "INSERT", "INTO", "IS",
	"JOIN", "LEFT", "LIKE", "LIMIT", "NOT", "NULL", "OFFSET", "ON", "OR", "ORDER",
	"OUTER", "RETURNING", "RIGHT", "SELECT", "SET", "TABLE", "THEN", "TRUE", "UNION",
	"UPDATE", "VALUES", "VIEW", "WHEN", "WHERE", "WITH",
}

func (c *Compiler) rememberSchemaStatement(file schemaFile, stmt ast.Statement) {
	if stmt.Raw == nil {
		return
	}
	switch n := stmt.Raw.Stmt.(type) {
	case *ast.CreateTableStmt:
		if n.Name != nil {
			c.rememberDefinition(file, stmt.Raw, "table", n.Name.Schema, n.Name.Name, 0)
		}
	case *ast.CreateTableAsStmt:
		if n.Into != nil && n.Into.Rel != nil {
			rel := n.Into.Rel
			schema, name := "", ""
			if rel.Schemaname != nil {
				schema = *rel.Schemaname
			}
			if rel.Relname != nil {
				name = *rel.Relname
			}
			c.rememberDefinition(file, stmt.Raw, "table", schema, name, rel.Location)
		}
	case *ast.ViewStmt:
		if n.View != nil {
			schema, name := "", ""
			if n.View.Schemaname != nil {
				schema = *n.View.Schemaname
			}
			if n.View.Relname != nil {
				name = *n.View.Relname
			}
			c.rememberDefinition(file, stmt.Raw, "view", schema, name, n.View.Location)
		}
	case *ast.DropTableStmt:
		for _, table := range n.Tables {
			c.rememberDrop(file, stmt.Raw, "drop", table.Schema, table.Name)
		}
	}
}

func (c *Compiler) rememberDefinition(file schemaFile, raw *ast.RawStmt, kind, schema, name string, loc int) {
	if name == "" {
		return
	}
	if schema == "" {
		schema = c.catalogDefaultSchema()
	}
	def := makeDefinition(file, raw, kind, loc, len(name))
	def.Schema, def.Name = schema, name
	if c.definitions == nil {
		c.definitions = map[string]objectDefinition{}
	}
	c.definitions[relationKey(schema, name)] = def
	c.definitions[relationKey("", name)] = def
}

func (c *Compiler) rememberDrop(file schemaFile, raw *ast.RawStmt, kind, schema, name string) {
	if name == "" {
		return
	}
	def := makeDefinition(file, raw, kind, 0, len(name))
	def.Schema, def.Name = schema, name
	if c.drops == nil {
		c.drops = map[string]objectDefinition{}
	}
	c.drops[relationKey(schema, name)] = def
	c.drops[relationKey("", name)] = def
}

func makeDefinition(file schemaFile, raw *ast.RawStmt, kind string, loc, length int) objectDefinition {
	if loc <= 0 {
		loc = raw.StmtLocation
	}
	if loc < 0 || loc > len(file.contents) {
		loc = raw.StmtLocation
	}
	if loc < 0 {
		loc = 0
	}
	if loc > len(file.contents) {
		loc = len(file.contents)
	}
	end := loc + length
	if length <= 0 || end > len(file.contents) {
		end = source.TokenEnd(file.contents, loc)
	}
	pos := source.LineColumn(file.contents, loc)
	text, _ := source.LineAt(file.contents, loc)
	def := objectDefinition{
		Filename:  file.name,
		Offset:    loc,
		EndOffset: end,
		Line:      pos.Line,
		Column:    pos.Column,
		Snippet:   text,
		Kind:      kind,
	}
	return def
}

func (c *Compiler) catalogDefaultSchema() string {
	if c.catalog != nil {
		return c.catalog.DefaultSchema
	}
	return ""
}

func relationKey(schema, name string) string {
	return strings.ToLower(schema) + "\x00" + strings.ToLower(name)
}

func (c *Compiler) definition(schema, name string) (objectDefinition, bool) {
	if c.definitions == nil {
		return objectDefinition{}, false
	}
	if schema == "" {
		schema = c.catalogDefaultSchema()
	}
	if def, ok := c.definitions[relationKey(schema, name)]; ok {
		return def, true
	}
	def, ok := c.definitions[relationKey("", name)]
	return def, ok
}

func (c *Compiler) dropped(schema, name string) (objectDefinition, bool) {
	if c.drops == nil {
		return objectDefinition{}, false
	}
	if schema == "" {
		schema = c.catalogDefaultSchema()
	}
	if def, ok := c.drops[relationKey(schema, name)]; ok {
		return def, true
	}
	def, ok := c.drops[relationKey("", name)]
	return def, ok
}

// prepareDiagnostics qualifies table references that have exactly one
// possible schema and rejects unqualified references that match more than one.
// It only runs when diagnostics are enabled, so the successful path is
// unchanged when the feature is off.
func (c *Compiler) prepareDiagnostics(raw *ast.RawStmt) error {
	if !c.diagnostics || raw == nil || c.catalog == nil {
		return nil
	}
	ctes := map[string]struct{}{}
	astutils.Walk(astutils.VisitorFunc(func(node ast.Node) {
		if cte, ok := node.(*ast.CommonTableExpr); ok && cte.Ctename != nil {
			ctes[strings.ToLower(*cte.Ctename)] = struct{}{}
		}
	}), raw)

	var ambiguous error
	astutils.Walk(astutils.VisitorFunc(func(node ast.Node) {
		if ambiguous != nil {
			return
		}
		rv, ok := node.(*ast.RangeVar)
		if !ok || rv.Relname == nil || *rv.Relname == "" {
			return
		}
		if rv.Schemaname != nil && *rv.Schemaname != "" {
			return
		}
		if _, ok := ctes[strings.ToLower(*rv.Relname)]; ok {
			return
		}
		matches := c.tablesByName(*rv.Relname)
		switch len(matches) {
		case 0:
			return
		case 1:
			schema := matches[0]
			rv.Schemaname = &schema
		default:
			candidates := make([]string, 0, len(matches))
			for _, schema := range matches {
				candidates = append(candidates, schema+"."+*rv.Relname)
			}
			sort.Strings(candidates)
			err := sqlerr.RelationAmbiguous(*rv.Relname, candidates)
			err.Location = rv.Location
			if rv.Location == 0 {
				err.Location = raw.Pos()
			}
			ambiguous = err
		}
	}), raw)
	return ambiguous
}

func (c *Compiler) tablesByName(name string) []string {
	var schemas []string
	for _, schema := range c.catalog.Schemas {
		for _, table := range schema.Tables {
			if strings.EqualFold(table.Rel.Name, name) {
				schemas = append(schemas, schema.Name)
				break
			}
		}
	}
	sort.Strings(schemas)
	return schemas
}

func (c *Compiler) enrichError(err error, stmt statement, loc int, explicitLocation bool) error {
	if !c.diagnostics || err == nil {
		return err
	}
	info := inferErrorInfo(err)
	detail, hasDetail := sqlerr.Details(err)
	if hasDetail {
		if detail.Kind == "" {
			detail.Kind = info.Kind
		}
		if detail.Object == "" {
			detail.Object = info.Object
		}
		if detail.Symbol == "" {
			detail.Symbol = info.Symbol
		}
		if detail.Relation == "" {
			detail.Relation = info.Relation
		}
	} else {
		detail = sqlerr.Diagnostic{
			Kind:     info.Kind,
			Object:   info.Object,
			Symbol:   info.Symbol,
			Relation: info.Relation,
		}
	}
	if detail.Kind == "" {
		detail.Kind = "semantic"
	}

	offset, end, found := c.locateError(stmt.raw, info.Symbol, info.Object, info.Relation)
	fromAST := found
	if !found && explicitLocation && loc >= 0 && loc <= len(stmt.src) {
		offset, end, found = loc, source.TokenEnd(stmt.src, loc), true
		if info.Symbol != "" && offset+len(info.Symbol) <= len(stmt.src) {
			end = offset + len(info.Symbol)
		}
	}
	if fromAST && stmt.pp != nil {
		offset = stmt.pp.Origin(offset)
		end = stmt.pp.Origin(end)
	}
	if found && offset >= 0 && offset <= len(stmt.src) {
		detail.Offset = offset
		detail.HasOffset = true
		if end <= offset || end > len(stmt.src) {
			end = source.TokenEnd(stmt.src, offset)
		}
		detail.EndOffset = end
	} else {
		detail.NoLocation = true
	}

	if len(detail.Candidates) == 0 {
		if info.Object == "column" && info.Relation == "" {
			info.Relation = c.columnRelation(stmt.raw, info.Symbol)
		}
		detail.Candidates = c.candidatesFor(info)
	}
	detail.Related = append(detail.Related, c.relatedFor(info)...)
	return sqlerr.WithDetail(err, detail)
}

func (c *Compiler) columnRelation(root *ast.RawStmt, symbol string) string {
	if root == nil {
		return ""
	}
	var qualified string
	var tables []string
	astutils.Walk(astutils.VisitorFunc(func(node ast.Node) {
		switch n := node.(type) {
		case *ast.ColumnRef:
			parts := stringSlice(n.Fields)
			if len(parts) == 0 || !strings.EqualFold(parts[len(parts)-1], symbol) {
				return
			}
			if len(parts) > 1 && qualified == "" {
				qualified = parts[len(parts)-2]
			}
		case *ast.RangeVar:
			if n.Relname != nil {
				tables = append(tables, *n.Relname)
			}
		}
	}), root)
	if qualified != "" {
		return qualified
	}
	if len(tables) == 1 {
		return tables[0]
	}
	return ""
}

func (c *Compiler) enrichParseError(err error) error {
	if !c.diagnostics || err == nil {
		return err
	}
	info := inferErrorInfo(err)
	detail, hasDetail := sqlerr.Details(err)
	if hasDetail {
		if detail.Kind == "" {
			detail.Kind = "syntax"
		}
		if detail.Symbol == "" {
			detail.Symbol = info.Symbol
		}
	} else {
		detail = sqlerr.Diagnostic{
			Kind:   "syntax",
			Symbol: info.Symbol,
		}
	}
	if detail.Kind == "" {
		detail.Kind = "syntax"
	}
	if detail.Symbol != "" && !knownKeyword(detail.Symbol) {
		detail.Candidates = sqlKeywords
	}
	if !detail.HasOffset && !detail.HasLineColumn {
		if match := lineColumn.FindStringSubmatch(err.Error()); len(match) == 3 {
			line, lineErr := strconv.Atoi(match[1])
			column, columnErr := strconv.Atoi(match[2])
			if lineErr == nil && columnErr == nil {
				detail.Line = line
				detail.Column = column
				detail.HasLineColumn = true
			}
		}
	}
	return sqlerr.WithDetail(err, detail)
}

func (c *Compiler) locateError(root *ast.RawStmt, symbol, object, relation string) (int, int, bool) {
	if root == nil || symbol == "" {
		return 0, 0, false
	}
	switch object {
	case "relation":
		var offset, end int
		found := false
		astutils.Walk(astutils.VisitorFunc(func(node ast.Node) {
			rv, ok := node.(*ast.RangeVar)
			if !ok || rv.Relname == nil || !strings.EqualFold(*rv.Relname, symbol) {
				return
			}
			offset = rv.Location
			end = offset + len(symbol)
			found = true
		}), root)
		return offset, end, found
	case "column":
		var offset, end int
		found := false
		astutils.Walk(astutils.VisitorFunc(func(node ast.Node) {
			switch n := node.(type) {
			case *ast.ColumnRef:
				parts := stringSlice(n.Fields)
				if len(parts) == 0 || !strings.EqualFold(parts[len(parts)-1], symbol) {
					return
				}
				if relation != "" && len(parts) > 1 && !strings.EqualFold(parts[len(parts)-2], relation) {
					return
				}
				offset = n.Location
				end = offset + len(symbol)
				found = true
			case *ast.ResTarget:
				if n.Name != nil && strings.EqualFold(*n.Name, symbol) {
					offset = n.Location
					end = offset + len(symbol)
					found = true
				}
			}
		}), root)
		return offset, end, found
	case "function":
		var offset, end int
		found := false
		astutils.Walk(astutils.VisitorFunc(func(node ast.Node) {
			call, ok := node.(*ast.FuncCall)
			if !ok || call.Func == nil || !strings.EqualFold(call.Func.Name, symbol) {
				return
			}
			offset = call.Location
			end = offset + len(symbol)
			found = true
		}), root)
		return offset, end, found
	default:
		return 0, 0, false
	}
}

func (c *Compiler) candidatesFor(info errorInfo) []string {
	switch info.Object {
	case "relation":
		return c.relationCandidates()
	case "column":
		return c.columnCandidates(info.Relation)
	case "function":
		return c.functionCandidates()
	}
	return nil
}

func (c *Compiler) relationCandidates() []string {
	var out []string
	seen := map[string]struct{}{}
	for _, schema := range c.catalog.Schemas {
		for _, table := range schema.Tables {
			for _, name := range []string{table.Rel.Name, schema.Name + "." + table.Rel.Name} {
				key := strings.ToLower(name)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (c *Compiler) columnCandidates(relation string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, schema := range c.catalog.Schemas {
		for _, table := range schema.Tables {
			if relation != "" && !strings.EqualFold(table.Rel.Name, relation) {
				continue
			}
			for _, col := range table.Columns {
				key := strings.ToLower(col.Name)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				out = append(out, col.Name)
			}
		}
	}
	if len(out) == 0 && relation != "" {
		return c.columnCandidates("")
	}
	sort.Strings(out)
	return out
}

func (c *Compiler) functionCandidates() []string {
	var out []string
	seen := map[string]struct{}{}
	for _, schema := range c.catalog.Schemas {
		for _, fn := range schema.Funcs {
			key := strings.ToLower(fn.Name)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, fn.Name)
		}
	}
	sort.Strings(out)
	return out
}

func (c *Compiler) relatedFor(info errorInfo) []sqlerr.Related {
	if info.Object != "relation" && info.Object != "column" {
		return nil
	}
	if info.Object == "relation" {
		if def, ok := c.dropped("", info.Symbol); ok {
			return []sqlerr.Related{relatedDefinition(def, "relation was dropped here")}
		}
	}
	if info.Relation == "" {
		return nil
	}
	if def, ok := c.definition("", info.Relation); ok {
		message := fmt.Sprintf("%s %q is defined here", def.Kind, info.Relation)
		return []sqlerr.Related{relatedDefinition(def, message)}
	}
	return nil
}

func relatedDefinition(def objectDefinition, message string) sqlerr.Related {
	return sqlerr.Related{
		Filename:  def.Filename,
		Line:      def.Line,
		Column:    def.Column,
		Offset:    def.Offset,
		EndOffset: def.EndOffset,
		Snippet:   def.Snippet,
		Message:   message,
	}
}

func inferErrorInfo(err error) errorInfo {
	info := errorInfo{Kind: "semantic"}
	if detail, ok := sqlerr.Details(err); ok {
		info.Kind = detail.Kind
		info.Object = detail.Object
		info.Symbol = detail.Symbol
		info.Relation = detail.Relation
	}
	text := err.Error()
	if info.Kind == "" {
		switch {
		case strings.Contains(text, "syntax error"):
			info.Kind = "syntax"
		case strings.Contains(text, "ambiguous"):
			info.Kind = "ambiguity"
		default:
			info.Kind = "semantic"
		}
	}
	if match := columnOfRelation.FindStringSubmatch(text); len(match) == 3 {
		info.Object, info.Symbol, info.Relation = "column", match[1], match[2]
		return info
	}
	if match := columnAmbiguous.FindStringSubmatch(text); len(match) == 2 {
		info.Object, info.Symbol = "column", match[1]
		return info
	}
	if match := columnMissing.FindStringSubmatch(text); len(match) == 2 {
		info.Object, info.Symbol = "column", lastPart(match[1])
		return info
	}
	if match := relationMissing.FindStringSubmatch(text); len(match) == 2 {
		info.Object, info.Symbol = "relation", lastPart(match[1])
		return info
	}
	if match := functionMissing.FindStringSubmatch(text); len(match) == 2 {
		info.Object, info.Symbol = "function", lastPart(match[1])
		return info
	}
	if match := schemaMissing.FindStringSubmatch(text); len(match) == 2 {
		info.Object, info.Symbol = "schema", match[1]
		return info
	}
	if match := typeMissing.FindStringSubmatch(text); len(match) == 2 {
		info.Object, info.Symbol = "type", match[1]
		return info
	}
	if info.Kind == "syntax" {
		if match := nearToken.FindStringSubmatch(text); len(match) == 2 {
			info.Symbol = strings.TrimSpace(match[1])
			info.Symbol = strings.TrimSuffix(info.Symbol, ";")
		}
	}
	return info
}

func lastPart(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 && i+1 < len(name) {
		return name[i+1:]
	}
	return name
}

func knownKeyword(word string) bool {
	for _, keyword := range sqlKeywords {
		if strings.EqualFold(word, keyword) {
			return true
		}
	}
	return false
}
