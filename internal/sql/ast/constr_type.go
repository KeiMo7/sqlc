package ast

type ConstrType uint

func (n *ConstrType) Pos() int {
	return 0
}

// The constraint kinds the analysis reads, numbered as PostgreSQL's parser
// numbers them, which is what an engine's converter records.
const (
	ConstrTypeNull              ConstrType = 1
	ConstrTypeNotNull           ConstrType = 2
	ConstrTypeDefault           ConstrType = 3
	ConstrTypeIdentity          ConstrType = 4
	ConstrTypeGenerated         ConstrType = 5
	ConstrTypeCheck             ConstrType = 6
	ConstrTypePrimary           ConstrType = 7
	ConstrTypeUnique            ConstrType = 8
	ConstrTypeExclusion         ConstrType = 9
	ConstrTypeForeign           ConstrType = 10
	ConstrTypeAttrDeferrable    ConstrType = 11
	ConstrTypeAttrNotDeferrable ConstrType = 12
	ConstrTypeAttrDeferred      ConstrType = 13
	ConstrTypeAttrImmediate     ConstrType = 14
	ConstrTypeAttrNoInherit     ConstrType = 15
)
