package multierr

import (
	"errors"
	"fmt"

	"github.com/sqlc-dev/sqlc/internal/source"
	"github.com/sqlc-dev/sqlc/internal/sql/sqlerr"
)

type FileError struct {
	Filename string
	Line     int
	Column   int
	// Offset is the exact byte offset used for diagnostics. HasOffset
	// distinguishes the start of a file from an unknown position.
	Offset     int
	HasOffset  bool
	EndOffset  int
	Kind       string
	Object     string
	Symbol     string
	Relation   string
	Candidates []string
	Related    []sqlerr.Related
	NoLocation bool
	SourceText string
	Err        error
}

func (e *FileError) Unwrap() error {
	return e.Err
}

type Error struct {
	errs []*FileError
}

func (e *Error) Add(filename, in string, loc int, err error) {
	line := 1
	column := 1
	offset := loc
	hasOffset := loc != 0 && in != ""
	endOffset := 0
	var detail sqlerr.Diagnostic
	var hasDetail bool
	var lerr *sqlerr.Error
	if errors.As(err, &lerr) {
		if lerr.Location != 0 {
			loc = lerr.Location
			offset = lerr.Location
			hasOffset = in != ""
		} else if lerr.Line != 0 && lerr.Column != 0 {
			line = lerr.Line
			column = lerr.Column
			if in != "" {
				if off := source.ByteOffset(in, line, column); off >= 0 {
					offset = off
					hasOffset = true
				}
			}
		}
	}
	if d, ok := sqlerr.Details(err); ok {
		detail = d
		hasDetail = true
		if d.HasOffset {
			offset = d.Offset
			hasOffset = in != ""
		} else if d.HasLineColumn && in != "" {
			if off := source.ByteOffset(in, d.Line, d.Column); off >= 0 {
				offset = off
				hasOffset = true
			}
		}
		if d.EndOffset != 0 {
			endOffset = d.EndOffset
		}
		if d.NoLocation {
			hasOffset = false
		}
	}
	if hasOffset && in != "" && offset >= 0 && offset <= len(in) {
		if endOffset == 0 {
			endOffset = offset + len(detail.Symbol)
			if detail.Symbol == "" || endOffset > len(in) {
				endOffset = source.TokenEnd(in, offset)
			}
		}
	}
	if in != "" && loc != 0 {
		line, column = source.LineNumber(in, loc)
	}
	fe := &FileError{
		Filename:   filename,
		Line:       line,
		Column:     column,
		Offset:     offset,
		HasOffset:  hasOffset,
		EndOffset:  endOffset,
		Err:        err,
		NoLocation: !hasOffset,
		SourceText: in,
	}
	if hasDetail {
		fe.Kind = detail.Kind
		fe.Object = detail.Object
		fe.Symbol = detail.Symbol
		fe.Relation = detail.Relation
		fe.Candidates = detail.Candidates
		fe.Related = detail.Related
	}
	e.errs = append(e.errs, fe)
}

func (e *Error) Errs() []*FileError {
	return e.errs
}

func (e *Error) Error() string {
	return fmt.Sprintf("multiple errors: %d errors", len(e.errs))
}

func New() *Error {
	return &Error{}
}
