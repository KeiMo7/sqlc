package sqlerr

import (
	"errors"
	"fmt"
)

var Exists = errors.New("already exists")
var NotFound = errors.New("does not exist")
var NotUnique = errors.New("is not unique")

type Error struct {
	Err      error
	Code     string
	Message  string
	Location int
	Line     int
	Column   int
	// Offset is the exact byte offset of the error. Location is kept for
	// compatibility with existing callers, which use it both as a byte
	// offset and as a parser-specific cursor position.
	Offset     int
	HasOffset  bool
	EndOffset  int
	Symbol     string
	Object     string
	Relation   string
	Kind       string
	Candidates []string
	Related    []Related
	NoLocation bool
	// Hint     string
}

// Diagnostic is the structured information attached to an error for the
// optional diagnostic renderer. It deliberately does not affect Error().
type Diagnostic struct {
	Offset        int
	HasOffset     bool
	EndOffset     int
	Line          int
	Column        int
	HasLineColumn bool
	Symbol        string
	Object        string
	Relation      string
	Kind          string
	Candidates    []string
	Related       []Related
	NoLocation    bool
}

// Related is another source location relevant to an error, such as the
// definition of a view a failing query depends on.
type Related struct {
	Filename  string
	Line      int
	Column    int
	Offset    int
	EndOffset int
	Snippet   string
	Message   string
}

type diagnosticCarrier interface {
	SQLCDiagnostic() *Diagnostic
}

// DetailedError attaches structured diagnostics without changing the original
// error text or its errors.Is/errors.As behavior.
type DetailedError struct {
	Err    error
	Detail Diagnostic
}

func (e *DetailedError) Error() string {
	return e.Err.Error()
}

func (e *DetailedError) Unwrap() error {
	return e.Err
}

func (e *DetailedError) SQLCDiagnostic() *Diagnostic {
	if e == nil {
		return nil
	}
	d := e.Detail
	return &d
}

func (e *Error) SQLCDiagnostic() *Diagnostic {
	if e == nil {
		return nil
	}
	return &Diagnostic{
		Offset:        e.Offset,
		HasOffset:     e.HasOffset,
		EndOffset:     e.EndOffset,
		Line:          e.Line,
		Column:        e.Column,
		HasLineColumn: e.Line != 0 && e.Column != 0,
		Symbol:        e.Symbol,
		Object:        e.Object,
		Relation:      e.Relation,
		Kind:          e.Kind,
		Candidates:    e.Candidates,
		Related:       e.Related,
		NoLocation:    e.NoLocation,
	}
}

// Details returns structured diagnostic data from an error, if any.
func Details(err error) (Diagnostic, bool) {
	var carrier diagnosticCarrier
	if !errors.As(err, &carrier) {
		return Diagnostic{}, false
	}
	d := carrier.SQLCDiagnostic()
	if d == nil {
		return Diagnostic{}, false
	}
	return *d, true
}

// WithDetail attaches structured diagnostics while preserving the original
// error. If err already has details, unset fields are inherited.
func WithDetail(err error, detail Diagnostic) error {
	if err == nil {
		return nil
	}
	if existing, ok := Details(err); ok {
		if detail.Offset == 0 && !detail.HasOffset {
			detail.Offset = existing.Offset
			detail.HasOffset = existing.HasOffset
		}
		if detail.EndOffset == 0 {
			detail.EndOffset = existing.EndOffset
		}
		if !detail.HasLineColumn {
			detail.Line = existing.Line
			detail.Column = existing.Column
			detail.HasLineColumn = existing.HasLineColumn
		}
		if detail.Symbol == "" {
			detail.Symbol = existing.Symbol
		}
		if detail.Object == "" {
			detail.Object = existing.Object
		}
		if detail.Relation == "" {
			detail.Relation = existing.Relation
		}
		if detail.Kind == "" {
			detail.Kind = existing.Kind
		}
		if len(detail.Candidates) == 0 {
			detail.Candidates = existing.Candidates
		}
		if len(detail.Related) == 0 {
			detail.Related = existing.Related
		}
		detail.NoLocation = detail.NoLocation || existing.NoLocation
	}
	return &DetailedError{Err: err, Detail: detail}
}

func (e *Error) Unwrap() error {
	return e.Err
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s %s", e.Message, e.Err.Error())
	} else {
		return e.Message
	}
}

func ColumnExists(rel, col string) *Error {
	return &Error{
		Err:      Exists,
		Code:     "42701",
		Message:  fmt.Sprintf("column %q of relation %q", col, rel),
		Symbol:   col,
		Object:   "column",
		Relation: rel,
		Kind:     "semantic",
	}
}

func ColumnNotFound(rel, col string) *Error {
	return &Error{
		Err:      NotFound,
		Code:     "42703",
		Message:  fmt.Sprintf("column %q of relation %q", col, rel),
		Symbol:   col,
		Object:   "column",
		Relation: rel,
		Kind:     "semantic",
	}
}

func RelationExists(rel string) *Error {
	return &Error{
		Err:     Exists,
		Code:    "42P07",
		Message: fmt.Sprintf("relation %q", rel),
		Symbol:  rel,
		Object:  "relation",
		Kind:    "semantic",
	}
}

func RelationNotFound(rel string) *Error {
	return &Error{
		Err:     NotFound,
		Code:    "42P01",
		Message: fmt.Sprintf("relation %q", rel),
		Symbol:  rel,
		Object:  "relation",
		Kind:    "semantic",
	}
}

func SchemaExists(name string) *Error {
	return &Error{
		Err:     Exists,
		Code:    "42P06",
		Message: fmt.Sprintf("schema %q", name),
	}
}

func SchemaNotFound(sch string) *Error {
	return &Error{
		Err:     NotFound,
		Code:    "3F000",
		Message: fmt.Sprintf("schema %q", sch),
		Symbol:  sch,
		Object:  "schema",
		Kind:    "semantic",
	}
}

func TypeExists(typ string) *Error {
	return &Error{
		Err:     Exists,
		Code:    "42710",
		Message: fmt.Sprintf("type %q", typ),
	}
}

func TypeNotFound(typ string) *Error {
	return &Error{
		Err:     NotFound,
		Code:    "42704",
		Message: fmt.Sprintf("type %q", typ),
		Symbol:  typ,
		Object:  "type",
		Kind:    "semantic",
	}
}

func FunctionNotFound(fun string) *Error {
	return &Error{
		Err:     NotFound,
		Code:    "42704",
		Message: fmt.Sprintf("function %q", fun),
		Symbol:  fun,
		Object:  "function",
		Kind:    "semantic",
	}
}

func FunctionNotUnique(fn string) *Error {
	return &Error{
		Err:     NotUnique,
		Message: fmt.Sprintf("function name %q", fn),
		Symbol:  fn,
		Object:  "function",
		Kind:    "ambiguity",
	}
}

// RelationAmbiguous reports an unqualified relation that exists in more than
// one schema. Candidates are fully qualified names so the caller can choose
// one without guessing.
func RelationAmbiguous(rel string, candidates []string) *Error {
	return &Error{
		Code:       "42P09",
		Message:    fmt.Sprintf("relation %q is ambiguous", rel),
		Symbol:     rel,
		Object:     "relation",
		Kind:       "ambiguity",
		Candidates: candidates,
	}
}

// ColumnAmbiguous reports a column reference that matches more than one table.
func ColumnAmbiguous(rel string, candidates []string) *Error {
	return &Error{
		Code:       "42702",
		Message:    fmt.Sprintf("column reference %q is ambiguous", rel),
		Symbol:     rel,
		Object:     "column",
		Kind:       "ambiguity",
		Candidates: candidates,
	}
}
