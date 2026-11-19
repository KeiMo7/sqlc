package schemadrift

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/sqlc-dev/sqlc/internal/config"
	"github.com/sqlc-dev/sqlc/internal/engine/clickhouse"
	"github.com/sqlc-dev/sqlc/internal/engine/dolphin"
	"github.com/sqlc-dev/sqlc/internal/engine/duckdb"
	"github.com/sqlc-dev/sqlc/internal/engine/googlesql"
	"github.com/sqlc-dev/sqlc/internal/engine/mssql"
	"github.com/sqlc-dev/sqlc/internal/engine/postgresql"
	"github.com/sqlc-dev/sqlc/internal/engine/sqlite"
	"github.com/sqlc-dev/sqlc/internal/migrations"
	"github.com/sqlc-dev/sqlc/internal/sql/ast"
	"github.com/sqlc-dev/sqlc/internal/sql/astutils"
	"github.com/sqlc-dev/sqlc/internal/sql/format"
	"github.com/sqlc-dev/sqlc/internal/sql/sqlerr"
	"github.com/sqlc-dev/sqlc/internal/sql/sqlpath"
)

type Environment struct {
	Name   string
	Schema []string
}

type Options struct {
	Engine           config.Engine
	Environments     []Environment
	SummaryThreshold int
}

type Report struct {
	Source           string
	GateTarget       string
	Pairs            []PairReport
	SummaryThreshold int
}

type PairReport struct {
	From  string
	To    string
	Diffs []Diff
	Views []ViewImpact
}

type Diff struct {
	From         string
	To           string
	Table        string
	Object       string
	Column       string
	Kind         string
	Severity     string
	Incompatible bool
	Detail       string
	Partition    bool
}

type ViewImpact struct {
	View       string
	Tables     []string
	Transitive bool
}

type schemaModel struct {
	Tables map[string]*tableDef
	Views  map[string]*viewDef
	Types  map[string]*typeDef
}

type tableDef struct {
	Name        string
	Key         string
	Columns     map[string]*columnDef
	Constraints map[string]*constraintDef
	Inherits    []string
	Partition   *partitionDef
}

type columnDef struct {
	Name    string
	Key     string
	Type    string
	NotNull bool
	Default string
}

type constraintDef struct {
	Kind       string
	Name       string
	Columns    []string
	RefTable   string
	RefColumns []string
	Expression string
}

type partitionDef struct {
	Strategy string
	Columns  []string
	Bound    string
	Parent   string
}

type viewDef struct {
	Name         string
	Key          string
	Dependencies []string
}

type typeDef struct {
	Name   string
	Key    string
	Kind   string
	Values []string
	Fields map[string]string
	Base   string
}

type schemaParser interface {
	Parse(io.Reader) ([]ast.Statement, error)
}

type fallbackDialect struct{}

func (fallbackDialect) QuoteIdent(s string) string       { return s }
func (fallbackDialect) TypeName(_, name string) string   { return name }
func (fallbackDialect) Param(n int, _ bool) string       { return fmt.Sprintf("$%d", n) }
func (fallbackDialect) Cast(arg, typeName string) string { return arg + "::" + typeName }

func parserForEngine(engine config.Engine) (schemaParser, format.Dialect, error) {
	switch engine {
	case config.EnginePostgreSQL:
		p := postgresql.NewParser()
		return p, p, nil
	case config.EngineMySQL:
		p := dolphin.NewParser()
		return p, p, nil
	case config.EngineSQLite:
		p := sqlite.NewParser()
		return p, p, nil
	case config.EngineClickHouse:
		p := clickhouse.NewParser()
		return p, p, nil
	case config.EngineDuckDB:
		return duckdb.NewParser(), fallbackDialect{}, nil
	case config.EngineGoogleSQL:
		return googlesql.NewParser(), fallbackDialect{}, nil
	case config.EngineMSSQL:
		return mssql.NewParser(), fallbackDialect{}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported schema drift engine %q", engine)
	}
}

func buildSchema(engine config.Engine, env Environment) (*schemaModel, error) {
	paths, err := sqlpath.Glob(env.Schema)
	if err != nil {
		return nil, fmt.Errorf("schema %s: %w", env.Name, err)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, fmt.Errorf("schema %s: no SQL files matched", env.Name)
	}

	parser, dialect, err := parserForEngine(engine)
	if err != nil {
		return nil, err
	}
	b := &builder{
		engine:  engine,
		dialect: dialect,
		schema: &schemaModel{
			Tables: map[string]*tableDef{},
			Views:  map[string]*viewDef{},
			Types:  map[string]*typeDef{},
		},
	}
	for _, path := range paths {
		blob, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		contents := migrations.RemoveRollbackStatements(string(blob))
		contents = migrations.RemovePsqlMetaCommands(contents)
		stmts, err := parser.Parse(strings.NewReader(contents))
		if err != nil {
			return nil, atFile(path, err)
		}
		for i := range stmts {
			if stmts[i].Raw == nil {
				continue
			}
			if err := b.apply(stmts[i].Raw.Stmt); err != nil {
				return nil, atFile(path, err)
			}
		}
	}
	return b.schema, nil
}

func atFile(path string, err error) error {
	var serr *sqlerr.Error
	if errors.As(err, &serr) && serr.Line > 0 {
		if serr.Column > 0 {
			return fmt.Errorf("%s:%d:%d: %w", path, serr.Line, serr.Column, err)
		}
		return fmt.Errorf("%s:%d: %w", path, serr.Line, err)
	}
	return fmt.Errorf("%s: %w", path, err)
}

type builder struct {
	engine  config.Engine
	dialect format.Dialect
	schema  *schemaModel
}

func (b *builder) apply(node ast.Node) error {
	switch n := node.(type) {
	case nil:
		return nil
	case *ast.List:
		for _, item := range n.Items {
			if err := b.apply(item); err != nil {
				return err
			}
		}
	case *ast.CreateTableStmt:
		return b.createTable(n)
	case *ast.CreateStmt:
		return b.createStmt(n)
	case *ast.AlterTableStmt:
		return b.alterTable(n)
	case *ast.AlterTableSetSchemaStmt:
		return b.alterTableSetSchema(n)
	case *ast.CreateEnumStmt:
		return b.createEnum(n)
	case *ast.AlterTypeAddValueStmt:
		return b.alterTypeAddValue(n)
	case *ast.AlterTypeRenameValueStmt:
		return b.alterTypeRenameValue(n)
	case *ast.CompositeTypeStmt:
		return b.createCompositeType(n)
	case *ast.CreateDomainStmt:
		return b.createDomain(n)
	case *ast.CreateRangeStmt:
		return b.createRange(n)
	case *ast.ViewStmt:
		return b.createView(n)
	case *ast.IndexStmt:
		return b.createIndex(n)
	case *ast.DropTableStmt:
		for _, name := range n.Tables {
			key := tableKey(name)
			delete(b.schema.Tables, key)
			delete(b.schema.Views, key)
		}
	case *ast.RenameTableStmt:
		return b.renameTable(n)
	case *ast.RenameColumnStmt:
		return b.renameColumn(n)
	}
	return nil
}

func (b *builder) createTable(stmt *ast.CreateTableStmt) error {
	if stmt.Name == nil {
		return fmt.Errorf("create table with nil name")
	}
	key := tableKey(stmt.Name)
	display := tableDisplay(stmt.Name)
	if _, ok := b.schema.Tables[key]; ok {
		if stmt.IfNotExists {
			return nil
		}
		return fmt.Errorf("table %q already exists", display)
	}
	t := &tableDef{
		Name:        display,
		Key:         key,
		Columns:     map[string]*columnDef{},
		Constraints: map[string]*constraintDef{},
	}
	for _, inherited := range stmt.Inherits {
		t.Inherits = append(t.Inherits, tableKey(inherited))
	}
	sort.Strings(t.Inherits)
	if stmt.Partspec != nil || stmt.Partbound != nil {
		t.Partition = &partitionDef{}
		if stmt.Partspec != nil {
			if stmt.Partspec.Strategy != nil {
				t.Partition.Strategy = strings.ToLower(*stmt.Partspec.Strategy)
			}
			t.Partition.Columns = b.partitionColumns(stmt.Partspec.PartParams)
		}
		if stmt.Partbound != nil {
			t.Partition.Bound = b.partitionBound(stmt.Partbound)
		}
		if len(t.Inherits) > 0 {
			t.Partition.Parent = t.Inherits[0]
		}
	}
	for _, col := range stmt.Cols {
		b.addColumn(t, col)
	}
	for _, item := range listItems(stmt.Constraints) {
		b.addConstraintItem(t, item)
	}
	b.schema.Tables[key] = t
	return nil
}

func (b *builder) createStmt(stmt *ast.CreateStmt) error {
	if stmt.Relation == nil || stmt.Relation.Relname == nil {
		return fmt.Errorf("create table with nil name")
	}
	name := rangeVarTable(stmt.Relation)
	create := &ast.CreateTableStmt{
		Name:        name,
		IfNotExists: stmt.IfNotExists,
		Partspec:    stmt.Partspec,
		Partbound:   stmt.Partbound,
		Constraints: stmt.Constraints,
	}
	if stmt.InhRelations != nil {
		for _, item := range stmt.InhRelations.Items {
			if rv, ok := item.(*ast.RangeVar); ok {
				create.Inherits = append(create.Inherits, rangeVarTable(rv))
			}
		}
	}
	for _, item := range listItems(stmt.TableElts) {
		switch n := item.(type) {
		case *ast.ColumnDef:
			create.Cols = append(create.Cols, n)
		case *ast.Constraint:
			if create.Constraints == nil {
				create.Constraints = &ast.List{}
			}
			create.Constraints.Items = append(create.Constraints.Items, n)
		}
	}
	return b.createTable(create)
}

func (b *builder) addColumn(t *tableDef, col *ast.ColumnDef) {
	if col == nil {
		return
	}
	key := normalizeIdent(col.Colname)
	typ := normalizeType(b.engine, col.TypeName, b.dialect)
	c := &columnDef{
		Name:    col.Colname,
		Key:     key,
		Type:    typ,
		NotNull: col.IsNotNull || col.PrimaryKey,
		Default: normalizeDefault(b.engine, col.RawDefault, typ, b.dialect),
	}
	t.Columns[key] = c
	if col.PrimaryKey {
		b.addConstraint(t, &constraintDef{Kind: "primary_key", Columns: []string{key}})
	}
	for _, item := range listItems(col.Constraints) {
		if con, ok := item.(*ast.Constraint); ok {
			b.addColumnConstraint(t, key, con)
		}
	}
}

func (b *builder) addColumnConstraint(t *tableDef, columnKey string, con *ast.Constraint) {
	switch con.Contype {
	case ast.ConstrTypeNotNull:
		if col := t.Columns[columnKey]; col != nil {
			col.NotNull = true
		}
	case ast.ConstrTypeDefault:
		if col := t.Columns[columnKey]; col != nil {
			node := con.RawExpr
			if node == nil && con.CookedExpr != nil {
				node = &ast.String{Str: *con.CookedExpr}
			}
			col.Default = normalizeDefault(b.engine, node, col.Type, b.dialect)
		}
	case ast.ConstrTypePrimary:
		b.addConstraint(t, &constraintDef{Kind: "primary_key", Name: stringValue(con.Conname), Columns: []string{columnKey}})
	case ast.ConstrTypeUnique:
		b.addConstraint(t, &constraintDef{Kind: "unique", Name: stringValue(con.Conname), Columns: []string{columnKey}})
	case ast.ConstrTypeCheck:
		b.addConstraint(t, &constraintDef{Kind: "check", Name: stringValue(con.Conname), Expression: b.constraintExpr(con)})
	case ast.ConstrTypeForeign:
		fk := b.constraintFromAST(con)
		if len(fk.Columns) == 0 {
			fk.Columns = []string{columnKey}
		}
		b.addConstraint(t, fk)
	}
}

func (b *builder) addConstraintItem(t *tableDef, item ast.Node) {
	con, ok := item.(*ast.Constraint)
	if !ok {
		return
	}
	switch con.Contype {
	case ast.ConstrTypePrimary, ast.ConstrTypeUnique, ast.ConstrTypeCheck, ast.ConstrTypeForeign:
		b.addConstraint(t, b.constraintFromAST(con))
	case ast.ConstrTypeNotNull:
		for _, col := range listStrings(con.Keys) {
			if c := t.Columns[normalizeIdent(col)]; c != nil {
				c.NotNull = true
			}
		}
	}
}

func (b *builder) constraintFromAST(con *ast.Constraint) *constraintDef {
	out := &constraintDef{Name: stringValue(con.Conname)}
	switch con.Contype {
	case ast.ConstrTypePrimary:
		out.Kind = "primary_key"
		out.Columns = normalizeList(listStrings(con.Keys))
	case ast.ConstrTypeUnique:
		out.Kind = "unique"
		out.Columns = normalizeList(listStrings(con.Keys))
	case ast.ConstrTypeForeign:
		out.Kind = "foreign_key"
		out.Columns = normalizeList(listStrings(con.FkAttrs))
		out.RefTable = rangeVarKey(con.Pktable)
		out.RefColumns = normalizeList(listStrings(con.PkAttrs))
	case ast.ConstrTypeCheck:
		out.Kind = "check"
		out.Expression = b.constraintExpr(con)
	}
	return out
}

func (b *builder) constraintExpr(con *ast.Constraint) string {
	if con.RawExpr != nil {
		return normalizeExpr(con.RawExpr, b.dialect)
	}
	if con.CookedExpr != nil {
		return normalizeExpr(&ast.String{Str: *con.CookedExpr}, b.dialect)
	}
	return ""
}

func (b *builder) addConstraint(t *tableDef, con *constraintDef) {
	if con == nil || con.Kind == "" {
		return
	}
	if con.Kind == "primary_key" {
		for _, column := range con.Columns {
			if col := t.Columns[column]; col != nil {
				col.NotNull = true
			}
		}
	}
	key := con.identity()
	if _, ok := t.Constraints[key]; !ok {
		t.Constraints[key] = con
	}
}

func (b *builder) alterTable(stmt *ast.AlterTableStmt) error {
	name := stmt.Table
	if name == nil && stmt.Relation != nil {
		name = rangeVarTable(stmt.Relation)
	}
	if name == nil {
		return fmt.Errorf("alter table with nil name")
	}
	key := tableKey(name)
	t := b.schema.Tables[key]
	if t == nil {
		return fmt.Errorf("table %q does not exist", tableDisplay(name))
	}
	for _, item := range listItems(stmt.Cmds) {
		cmd, ok := item.(*ast.AlterTableCmd)
		if !ok {
			continue
		}
		switch cmd.Subtype {
		case ast.AT_AddColumn:
			b.addColumn(t, cmd.Def)
		case ast.AT_AlterColumnType:
			if cmd.Def == nil {
				continue
			}
			col := t.Columns[normalizeIdent(cmd.Def.Colname)]
			if col == nil {
				col = t.Columns[normalizeIdent(stringValue(cmd.Name))]
			}
			if col == nil {
				continue
			}
			col.Type = normalizeType(b.engine, cmd.Def.TypeName, b.dialect)
			col.NotNull = col.NotNull || cmd.Def.IsNotNull
			col.Default = normalizeDefault(b.engine, cmd.Def.RawDefault, col.Type, b.dialect)
		case ast.AT_DropColumn:
			colKey := normalizeIdent(stringValue(cmd.Name))
			delete(t.Columns, colKey)
			for conKey, con := range t.Constraints {
				if contains(con.Columns, colKey) {
					delete(t.Constraints, conKey)
				}
			}
		case ast.AT_DropNotNull:
			if col := t.Columns[normalizeIdent(stringValue(cmd.Name))]; col != nil {
				col.NotNull = false
			}
		case ast.AT_SetNotNull:
			if col := t.Columns[normalizeIdent(stringValue(cmd.Name))]; col != nil {
				col.NotNull = true
			}
		case ast.AT_SetDefault:
			if col := t.Columns[normalizeIdent(stringValue(cmd.Name))]; col != nil && cmd.Def != nil {
				col.Default = normalizeDefault(b.engine, cmd.Def.RawDefault, col.Type, b.dialect)
			}
		case ast.AT_DropDefault:
			if col := t.Columns[normalizeIdent(stringValue(cmd.Name))]; col != nil {
				col.Default = ""
			}
		case ast.AT_AddConstraint:
			if cmd.Constraint != nil {
				b.addConstraint(t, b.constraintFromAST(cmd.Constraint))
			}
		case ast.AT_DropConstraint:
			name := normalizeIdent(stringValue(cmd.Name))
			for conKey, con := range t.Constraints {
				if normalizeIdent(con.Name) == name {
					delete(t.Constraints, conKey)
				}
			}
		}
	}
	return nil
}

func (b *builder) alterTableSetSchema(stmt *ast.AlterTableSetSchemaStmt) error {
	if stmt.Table == nil || stmt.NewSchema == nil {
		return nil
	}
	oldKey := tableKey(stmt.Table)
	t := b.schema.Tables[oldKey]
	if t == nil {
		return nil
	}
	newName := *stmt.Table
	newName.Schema = *stmt.NewSchema
	delete(b.schema.Tables, oldKey)
	t.Name = tableDisplay(&newName)
	t.Key = tableKey(&newName)
	b.schema.Tables[t.Key] = t
	return nil
}

func (b *builder) renameTable(stmt *ast.RenameTableStmt) error {
	if stmt.Table == nil || stmt.NewName == nil {
		return nil
	}
	oldKey := tableKey(stmt.Table)
	t := b.schema.Tables[oldKey]
	if t == nil {
		return fmt.Errorf("table %q does not exist", tableDisplay(stmt.Table))
	}
	newName := *stmt.Table
	newName.Name = *stmt.NewName
	delete(b.schema.Tables, oldKey)
	t.Name = tableDisplay(&newName)
	t.Key = tableKey(&newName)
	b.schema.Tables[t.Key] = t
	return nil
}

func (b *builder) renameColumn(stmt *ast.RenameColumnStmt) error {
	if stmt.Table == nil || stmt.Col == nil || stmt.NewName == nil {
		return nil
	}
	t := b.schema.Tables[tableKey(stmt.Table)]
	if t == nil {
		return fmt.Errorf("table %q does not exist", tableDisplay(stmt.Table))
	}
	oldKey := normalizeIdent(stmt.Col.Name)
	col := t.Columns[oldKey]
	if col == nil {
		return fmt.Errorf("column %q of table %q does not exist", stmt.Col.Name, t.Name)
	}
	delete(t.Columns, oldKey)
	col.Name = *stmt.NewName
	col.Key = normalizeIdent(*stmt.NewName)
	t.Columns[col.Key] = col
	return nil
}

func (b *builder) createEnum(stmt *ast.CreateEnumStmt) error {
	if stmt.TypeName == nil {
		return fmt.Errorf("create enum with nil name")
	}
	key := typeKey(stmt.TypeName)
	typ := b.schema.Types[key]
	if typ == nil {
		typ = &typeDef{Name: typeDisplay(stmt.TypeName), Key: key, Kind: "enum"}
		b.schema.Types[key] = typ
	}
	typ.Values = append(typ.Values, listStrings(stmt.Vals)...)
	return nil
}

func (b *builder) alterTypeAddValue(stmt *ast.AlterTypeAddValueStmt) error {
	if stmt.Type == nil || stmt.NewValue == nil {
		return nil
	}
	typ := b.schema.Types[typeKey(stmt.Type)]
	if typ == nil {
		return fmt.Errorf("type %q does not exist", typeDisplay(stmt.Type))
	}
	value := *stmt.NewValue
	for _, existing := range typ.Values {
		if existing == value {
			if stmt.SkipIfNewValExists {
				return nil
			}
			return fmt.Errorf("enum %q already has value %q", typ.Name, value)
		}
	}
	index := len(typ.Values)
	if stmt.NewValHasNeighbor && stmt.NewValNeighbor != nil {
		for i, existing := range typ.Values {
			if existing == *stmt.NewValNeighbor {
				index = i
				if stmt.NewValIsAfter {
					index++
				}
				break
			}
		}
	}
	typ.Values = append(typ.Values, "")
	copy(typ.Values[index+1:], typ.Values[index:])
	typ.Values[index] = value
	return nil
}

func (b *builder) alterTypeRenameValue(stmt *ast.AlterTypeRenameValueStmt) error {
	if stmt.Type == nil || stmt.OldValue == nil || stmt.NewValue == nil {
		return nil
	}
	typ := b.schema.Types[typeKey(stmt.Type)]
	if typ == nil {
		return fmt.Errorf("type %q does not exist", typeDisplay(stmt.Type))
	}
	for i, value := range typ.Values {
		if value == *stmt.OldValue {
			typ.Values[i] = *stmt.NewValue
			return nil
		}
	}
	return nil
}

func (b *builder) createCompositeType(stmt *ast.CompositeTypeStmt) error {
	if stmt.TypeName == nil {
		return fmt.Errorf("create type with nil name")
	}
	key := typeKey(stmt.TypeName)
	typ := &typeDef{
		Name:   typeDisplay(stmt.TypeName),
		Key:    key,
		Kind:   "composite",
		Fields: map[string]string{},
	}
	for _, item := range listItems(stmt.Coldeflist) {
		col, ok := item.(*ast.ColumnDef)
		if !ok {
			continue
		}
		typ.Fields[normalizeIdent(col.Colname)] = normalizeType(b.engine, col.TypeName, b.dialect)
	}
	b.schema.Types[key] = typ
	return nil
}

func (b *builder) createDomain(stmt *ast.CreateDomainStmt) error {
	names := listStrings(stmt.Domainname)
	if len(names) == 0 {
		return fmt.Errorf("create domain with nil name")
	}
	name := strings.Join(names, ".")
	key := typeKeyFromParts(names)
	b.schema.Types[key] = &typeDef{
		Name: name,
		Key:  key,
		Kind: "domain",
		Base: normalizeType(b.engine, stmt.TypeName, b.dialect),
	}
	return nil
}

func (b *builder) createRange(stmt *ast.CreateRangeStmt) error {
	names := listStrings(stmt.TypeName)
	if len(names) == 0 {
		return fmt.Errorf("create range with nil name")
	}
	name := strings.Join(names, ".")
	typ := &typeDef{Name: name, Key: typeKeyFromParts(names), Kind: "range"}
	for _, item := range listItems(stmt.Params) {
		def, ok := item.(*ast.DefElem)
		if !ok || def.Defname == nil || *def.Defname != "subtype" {
			continue
		}
		typ.Base = normalizeExpr(def.Arg, b.dialect)
	}
	b.schema.Types[typ.Key] = typ
	return nil
}

func (b *builder) createView(stmt *ast.ViewStmt) error {
	if stmt.View == nil || stmt.View.Relname == nil {
		return fmt.Errorf("create view with nil name")
	}
	name := rangeVarTable(stmt.View)
	key := tableKey(name)
	deps := b.viewDependencies(stmt.Query)
	b.schema.Views[key] = &viewDef{
		Name:         tableDisplay(name),
		Key:          key,
		Dependencies: deps,
	}
	return nil
}

func (b *builder) viewDependencies(query ast.Node) []string {
	ctes := map[string]struct{}{}
	astutils.Walk(astutils.VisitorFunc(func(node ast.Node) {
		if cte, ok := node.(*ast.CommonTableExpr); ok && cte.Ctename != nil {
			ctes[normalizeIdent(*cte.Ctename)] = struct{}{}
		}
	}), query)
	seen := map[string]struct{}{}
	astutils.Walk(astutils.VisitorFunc(func(node ast.Node) {
		rv, ok := node.(*ast.RangeVar)
		if !ok || rv.Relname == nil {
			return
		}
		if _, ok := ctes[normalizeIdent(*rv.Relname)]; ok {
			return
		}
		seen[rangeVarKey(rv)] = struct{}{}
	}), query)
	out := make([]string, 0, len(seen))
	for dep := range seen {
		out = append(out, dep)
	}
	sort.Strings(out)
	return out
}

func (b *builder) createIndex(stmt *ast.IndexStmt) error {
	if stmt.Relation == nil || (!stmt.Unique && !stmt.Primary) {
		return nil
	}
	key := rangeVarKey(stmt.Relation)
	t := b.schema.Tables[key]
	if t == nil {
		return fmt.Errorf("table %q does not exist", key)
	}
	con := &constraintDef{Name: stringValue(stmt.Idxname)}
	if stmt.Primary {
		con.Kind = "primary_key"
	} else {
		con.Kind = "unique"
	}
	for _, item := range listItems(stmt.IndexParams) {
		elem, ok := item.(*ast.IndexElem)
		if !ok {
			continue
		}
		if elem.Name != nil {
			con.Columns = append(con.Columns, normalizeIdent(*elem.Name))
		} else if elem.Expr != nil {
			con.Columns = append(con.Columns, normalizeExpr(elem.Expr, b.dialect))
		}
	}
	b.addConstraint(t, con)
	return nil
}

func (b *builder) partitionColumns(l *ast.List) []string {
	var out []string
	for _, item := range listItems(l) {
		elem, ok := item.(*ast.PartitionElem)
		if !ok {
			out = append(out, normalizeExpr(item, b.dialect))
			continue
		}
		if elem.Name != nil {
			out = append(out, normalizeIdent(*elem.Name))
		} else if elem.Expr != nil {
			out = append(out, normalizeExpr(elem.Expr, b.dialect))
		}
	}
	return out
}

func (b *builder) partitionBound(spec *ast.PartitionBoundSpec) string {
	parts := []string{fmt.Sprintf("strategy=%c", spec.Strategy)}
	if values := b.boundValues(spec.Listdatums); len(values) > 0 {
		parts = append(parts, "list=("+strings.Join(values, ",")+")")
	}
	if values := b.boundValues(spec.Lowerdatums); len(values) > 0 {
		parts = append(parts, "from=("+strings.Join(values, ",")+")")
	}
	if values := b.boundValues(spec.Upperdatums); len(values) > 0 {
		parts = append(parts, "to=("+strings.Join(values, ",")+")")
	}
	return strings.Join(parts, " ")
}

func (b *builder) boundValues(l *ast.List) []string {
	var out []string
	for _, item := range listItems(l) {
		if d, ok := item.(*ast.PartitionRangeDatum); ok {
			if d.Value == nil {
				out = append(out, "min")
			} else {
				out = append(out, normalizeExpr(d.Value, b.dialect))
			}
			continue
		}
		out = append(out, normalizeExpr(item, b.dialect))
	}
	return out
}

func (c *constraintDef) identity() string {
	return strings.Join([]string{
		c.Kind,
		strings.Join(c.Columns, ","),
		c.RefTable,
		strings.Join(c.RefColumns, ","),
		c.Expression,
	}, "|")
}

func (p *partitionDef) identity() string {
	if p == nil {
		return ""
	}
	return strings.Join([]string{
		p.Strategy,
		strings.Join(p.Columns, ","),
		p.Bound,
		p.Parent,
	}, "|")
}

func listItems(l *ast.List) []ast.Node {
	if l == nil {
		return nil
	}
	return l.Items
}

func contains(items []string, item string) bool {
	for _, candidate := range items {
		if candidate == item {
			return true
		}
	}
	return false
}

func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func tableDisplay(name *ast.TableName) string {
	if name == nil {
		return ""
	}
	schema := normalizeIdent(name.Schema)
	if schema == "public" {
		schema = ""
	}
	if schema == "" {
		return name.Name
	}
	return schema + "." + name.Name
}

func typeDisplay(name *ast.TypeName) string {
	if name == nil {
		return ""
	}
	schema := normalizeIdent(name.Schema)
	if schema == "public" || schema == "pg_catalog" {
		schema = ""
	}
	if schema == "" {
		return name.Name
	}
	return schema + "." + name.Name
}

func rangeVarTable(rv *ast.RangeVar) *ast.TableName {
	if rv == nil {
		return nil
	}
	return &ast.TableName{
		Catalog: stringValue(rv.Catalogname),
		Schema:  stringValue(rv.Schemaname),
		Name:    stringValue(rv.Relname),
	}
}
