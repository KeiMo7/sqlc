package ast

import "github.com/sqlc-dev/sqlc/internal/sql/format"

type ColumnDef struct {
	Tag NodeTag[ColumnDef] `json:"tag"`

	Colname string `json:"colname"`
	// TypeName is the column's type for the catalog. When Typeless is set
	// the author wrote no type at all — SQLite allows it — and the column
	// prints without one, whatever TypeName carries.
	TypeName   *TypeName `json:"type_name,omitempty"`
	Typeless   bool      `json:"typeless"`
	IsNotNull  bool      `json:"is_not_null"`
	IsUnsigned bool      `json:"is_unsigned"`
	IsArray    bool      `json:"is_array"`
	ArrayDims  int       `json:"array_dims"`
	Vals       *List     `json:"vals,omitempty"`
	Length     *int      `json:"length,omitempty"`
	PrimaryKey bool      `json:"primary_key"`
	// IsHidden marks a column a relation offers by name without listing it,
	// like the column an sqlite fts5 table names after itself. The legacy
	// catalog drops hidden columns; the core catalog keeps them out of star
	// expansions and models.
	IsHidden bool `json:"is_hidden"`

	// From pg.ColumnDef
	Inhcount      int  `json:"inhcount"`
	IsLocal       bool `json:"is_local"`
	IsFromType    bool `json:"is_from_type"`
	IsFromParent  bool `json:"is_from_parent"`
	Storage       byte `json:"storage"`
	RawDefault    Node `json:"raw_default,omitempty"`
	CookedDefault Node `json:"cooked_default,omitempty"`
	// HasDefaultValue records a default an engine's parser identified without
	// retaining the expression itself.
	HasDefaultValue bool           `json:"-"`
	Identity        byte           `json:"identity"`
	CollClause      *CollateClause `json:"coll_clause,omitempty"`
	CollOid         Oid            `json:"coll_oid"`
	Constraints     *List          `json:"constraints,omitempty"`
	Fdwoptions      *List          `json:"fdwoptions,omitempty"`
	Location        int            `json:"location"`
	Comment         string         `json:"comment"`
}

func (n *ColumnDef) Pos() int {
	return n.Location
}

// HasDefault reports whether the column declares a default. Engines differ in
// where their parser puts it: some fill RawDefault or CookedDefault, while
// others record a constraint carrying RawExpr or CookedExpr.
func (n *ColumnDef) HasDefault() bool {
	if n == nil {
		return false
	}
	if n.HasDefaultValue {
		return true
	}
	if n.RawDefault != nil || n.CookedDefault != nil {
		return true
	}
	if n.Constraints == nil {
		return false
	}
	for _, item := range n.Constraints.Items {
		if c, ok := item.(*Constraint); ok && (c.RawExpr != nil || c.CookedExpr != nil) {
			return true
		}
	}
	return false
}

func (n *ColumnDef) Format(buf *TrackedBuffer, d format.Dialect) {
	if n == nil {
		return
	}
	buf.WriteString(n.Colname)
	if !n.Typeless {
		buf.WriteString(" ")
		buf.astFormat(n.TypeName, d)
		// MySQL integer types: signedness is part of the type.
		if n.IsUnsigned {
			buf.WriteString(" unsigned")
		}
	}
	// Use IsArray from ColumnDef since TypeName.ArrayBounds may not be set
	// (for type resolution compatibility)
	if n.IsArray && !items(n.TypeName.ArrayBounds) {
		buf.WriteString("[]")
	}
	if n.PrimaryKey {
		buf.WriteString(" PRIMARY KEY")
	} else if n.IsNotNull {
		buf.WriteString(" NOT NULL")
	}
	buf.astFormat(n.Constraints, d)
}
