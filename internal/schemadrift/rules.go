package schemadrift

import (
	"strings"

	"github.com/sqlc-dev/sqlc/internal/config"
	"github.com/sqlc-dev/sqlc/internal/sql/ast"
	"github.com/sqlc-dev/sqlc/internal/sql/format"
)

// All equivalence decisions live in this file. The comparison code and the
// renderers only consume these normalized values.

var typeAliases = map[string]string{
	"int":           "integer",
	"int4":          "integer",
	"integer":       "integer",
	"int2":          "smallint",
	"smallint":      "smallint",
	"int8":          "bigint",
	"bigint":        "bigint",
	"bool":          "boolean",
	"boolean":       "boolean",
	"float4":        "real",
	"real":          "real",
	"float8":        "double precision",
	"double":        "double precision",
	"decimal":       "numeric",
	"numeric":       "numeric",
	"varchar":       "character varying",
	"char":          "character",
	"bpchar":        "character",
	"timestamp":     "timestamp without time zone",
	"timestamptz":   "timestamp with time zone",
	"time":          "time without time zone",
	"timetz":        "time with time zone",
	"serial4":       "serial",
	"serial8":       "bigserial",
	"serial2":       "smallserial",
	"int unsigned":  "integer unsigned",
	"int4 unsigned": "integer unsigned",
	"int8 unsigned": "bigint unsigned",
	"int2 unsigned": "smallint unsigned",
}

func normalizeIdent(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func normalizeList(in []string) []string {
	out := make([]string, len(in))
	for i := range in {
		out[i] = normalizeIdent(in[i])
	}
	return out
}

func tableKey(name *ast.TableName) string {
	if name == nil {
		return ""
	}
	schema := normalizeIdent(name.Schema)
	if schema == "public" {
		schema = ""
	}
	if schema == "" {
		return normalizeIdent(name.Name)
	}
	return schema + "." + normalizeIdent(name.Name)
}

func typeKey(name *ast.TypeName) string {
	if name == nil {
		return ""
	}
	schema := normalizeIdent(name.Schema)
	if schema == "public" || schema == "pg_catalog" {
		schema = ""
	}
	if schema == "" {
		return normalizeIdent(name.Name)
	}
	return schema + "." + normalizeIdent(name.Name)
}

func typeKeyFromParts(parts []string) string {
	parts = normalizeList(parts)
	if len(parts) > 0 && parts[len(parts)-1] == "public" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) >= 2 && parts[len(parts)-2] == "public" {
		parts = append(parts[:len(parts)-2], parts[len(parts)-1])
	}
	return strings.Join(parts, ".")
}

func rangeVarKey(rv *ast.RangeVar) string {
	if rv == nil {
		return ""
	}
	return tableKey(&ast.TableName{
		Catalog: stringValue(rv.Catalogname),
		Schema:  stringValue(rv.Schemaname),
		Name:    stringValue(rv.Relname),
	})
}

func normalizeType(engine config.Engine, tn *ast.TypeName, d format.Dialect) string {
	if tn == nil {
		return ""
	}
	out := strings.TrimSpace(ast.Format(tn, d))
	out = strings.ToLower(out)
	out = strings.Join(strings.Fields(out), " ")
	out = strings.ReplaceAll(out, " . ", ".")
	out = strings.ReplaceAll(out, " .", ".")
	out = strings.ReplaceAll(out, ". ", ".")
	out = strings.TrimPrefix(out, "pg_catalog.")
	out = strings.TrimPrefix(out, "public.")
	if alias, ok := typeAliases[out]; ok {
		out = alias
	}
	switch engine {
	case config.EnginePostgreSQL, config.EngineDuckDB:
		out = strings.ReplaceAll(out, "public.", "")
	}
	return out
}

func normalizeDefault(engine config.Engine, node ast.Node, columnType string, d format.Dialect) string {
	if node == nil {
		return ""
	}
	out := strings.TrimSpace(ast.Format(node, d))
	out = stripOuterParens(out)
	out = foldUnquoted(out)
	out = strings.Join(strings.Fields(out), " ")
	out = strings.ReplaceAll(out, " (", "(")
	out = strings.ReplaceAll(out, "( ", "(")
	out = strings.ReplaceAll(out, " )", ")")
	out = strings.ReplaceAll(out, ",", ", ")

	// PostgreSQL records an explicit cast on many defaults. A cast to the
	// column's own type does not make the default a different value.
	if columnType != "" {
		for _, suffix := range []string{"::" + columnType, "::" + strings.ReplaceAll(columnType, " ", "")} {
			if strings.HasSuffix(out, suffix) {
				out = strings.TrimSpace(strings.TrimSuffix(out, suffix))
				break
			}
		}
	}
	switch out {
	case "now()":
		out = "current_timestamp"
	}
	return out
}

func normalizeExpr(node ast.Node, d format.Dialect) string {
	if node == nil {
		return ""
	}
	out := strings.TrimSpace(ast.Format(node, d))
	out = stripOuterParens(out)
	out = foldUnquoted(out)
	return strings.Join(strings.Fields(out), " ")
}

func stripOuterParens(s string) string {
	s = strings.TrimSpace(s)
	for len(s) >= 2 && s[0] == '(' && s[len(s)-1] == ')' {
		depth := 0
		quoted := byte(0)
		balanced := true
		for i := 0; i < len(s); i++ {
			c := s[i]
			if quoted != 0 {
				if c == quoted {
					if i+1 < len(s) && s[i+1] == quoted {
						i++
						continue
					}
					quoted = 0
				}
				continue
			}
			switch c {
			case '\'', '"':
				quoted = c
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 && i != len(s)-1 {
					balanced = false
				}
			}
		}
		if !balanced || depth != 0 {
			break
		}
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// foldUnquoted lowercases SQL keywords and function names while preserving
// the contents of string literals and quoted identifiers.
func foldUnquoted(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			out.WriteByte(c)
			if c == quote {
				if i+1 < len(s) && s[i+1] == quote {
					out.WriteByte(s[i+1])
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			out.WriteByte(c)
		default:
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			out.WriteByte(c)
		}
	}
	return out.String()
}

func listStrings(l *ast.List) []string {
	if l == nil {
		return nil
	}
	out := make([]string, 0, len(l.Items))
	for _, item := range l.Items {
		switch n := item.(type) {
		case *ast.String:
			out = append(out, n.Str)
		default:
			out = append(out, ast.Format(item, fallbackDialect{}))
		}
	}
	return out
}
