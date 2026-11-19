package ast

import "github.com/sqlc-dev/sqlc/internal/sql/format"

const (
	AT_AddColumn AlterTableType = iota
	AT_AlterColumnType
	AT_DropColumn
	AT_DropNotNull
	AT_SetNotNull
	AT_SetDefault
	AT_DropDefault
	AT_AddConstraint
	AT_DropConstraint
)

type AlterTableType int

func (t AlterTableType) String() string {
	switch t {
	case AT_AddColumn:
		return "AddColumn"
	case AT_AlterColumnType:
		return "AlterColumnType"
	case AT_DropColumn:
		return "DropColumn"
	case AT_DropNotNull:
		return "DropNotNull"
	case AT_SetNotNull:
		return "SetNotNull"
	case AT_SetDefault:
		return "SetDefault"
	case AT_DropDefault:
		return "DropDefault"
	case AT_AddConstraint:
		return "AddConstraint"
	case AT_DropConstraint:
		return "DropConstraint"
	default:
		return "Unknown"
	}
}

type AlterTableCmd struct {
	Tag NodeTag[AlterTableCmd] `json:"tag"`

	Subtype    AlterTableType `json:"subtype"`
	Name       *string        `json:"name,omitempty"`
	Def        *ColumnDef     `json:"def,omitempty"`
	Constraint *Constraint    `json:"constraint,omitempty"`
	Newowner   *RoleSpec      `json:"newowner,omitempty"`
	Behavior   DropBehavior   `json:"behavior"`
	MissingOk  bool           `json:"missing_ok"`
}

func (n *AlterTableCmd) Pos() int {
	return 0
}

func (n *AlterTableCmd) Format(buf *TrackedBuffer, d format.Dialect) {
	if n == nil {
		return
	}
	switch n.Subtype {
	case AT_AddColumn:
		buf.WriteString(" ADD COLUMN ")
	case AT_DropColumn:
		buf.WriteString(" DROP COLUMN ")
	}

	buf.astFormat(n.Def, d)
}
