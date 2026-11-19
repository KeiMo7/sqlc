package source

import (
	"sort"
	"strings"
)

// Position is a 1-based line and byte column derived from a byte offset.
//
// Offset is always a byte offset into the source. Column counts bytes, not
// runes, so it can be used with editor APIs that expect a byte-oriented
// position. Use LineColumnRune for a rune-oriented column when the consumer
// needs one.
type Position struct {
	Offset int
	Line   int
	Column int
}

// LineColumn converts a byte offset to a 1-based line and byte column.
//
// It never searches for text. The line is found by scanning the source for
// newline bytes, and the column is the number of bytes between the start of
// that line and offset.
func LineColumn(src string, offset int) Position {
	if offset < 0 {
		offset = 0
	}
	if offset > len(src) {
		offset = len(src)
	}
	line := 1
	lineStart := 0
	for i := 0; i < offset; i++ {
		if src[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}
	return Position{
		Offset: offset,
		Line:   line,
		Column: offset - lineStart + 1,
	}
}

// ByteOffset converts a 1-based line and byte column to a byte offset. It
// returns -1 when the position is out of range.
func ByteOffset(src string, line, column int) int {
	if line < 1 || column < 1 {
		return -1
	}
	starts := lineStarts(src)
	if line > len(starts) {
		return -1
	}
	start := starts[line-1]
	end := len(src)
	if line < len(starts) {
		end = starts[line] - 1
	}
	off := start + column - 1
	if off > end || off > len(src) {
		return -1
	}
	return off
}

// LineAt returns the complete line containing offset, without its line
// terminator, together with the byte column of offset within that line.
func LineAt(src string, offset int) (line string, column int) {
	pos := LineColumn(src, offset)
	start := pos.Offset - pos.Column + 1
	end := pos.Offset
	for end < len(src) && src[end] != '\n' && src[end] != '\r' {
		end++
	}
	return src[start:end], pos.Column
}

// TokenEnd returns the end of the identifier-like token beginning at offset.
// It is used to render a snippet without searching the file for the token.
func TokenEnd(src string, offset int) int {
	if offset < 0 || offset >= len(src) {
		return offset
	}
	end := offset
	for end < len(src) {
		switch src[end] {
		case ' ', '\t', '\r', '\n', ',', ';', '(', ')', '[', ']', '{', '}', '+', '-', '*', '/', '=', '<', '>', '!', '|', '&', ':':
			return end
		default:
			end++
		}
	}
	return end
}

// Candidate is a possible spelling for an unresolved symbol. Distance is the
// Levenshtein edit distance from the unresolved symbol.
type Candidate struct {
	Name     string `json:"name"`
	Distance int    `json:"distance"`
}

// RankCandidates sorts candidates by edit distance and then by spelling, and
// keeps at most limit entries. Sorting by spelling makes the result stable
// regardless of map or symbol-table iteration order.
func RankCandidates(symbol string, candidates []string, limit int) []Candidate {
	if symbol == "" || limit <= 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var ranked []Candidate
	for _, name := range candidates {
		if name == "" || name == symbol {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		distance := Levenshtein(symbol, name)
		if distance > maximumDistance(symbol, name) {
			continue
		}
		ranked = append(ranked, Candidate{Name: name, Distance: distance})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Distance != ranked[j].Distance {
			return ranked[i].Distance < ranked[j].Distance
		}
		li, lj := strings.ToLower(ranked[i].Name), strings.ToLower(ranked[j].Name)
		if li != lj {
			return li < lj
		}
		return ranked[i].Name < ranked[j].Name
	})
	if len(ranked) > 1 {
		best := ranked[0].Distance
		n := 1
		for n < len(ranked) && ranked[n].Distance == best {
			n++
		}
		ranked = ranked[:n]
	}
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked
}

// Levenshtein returns the rune edit distance between a and b.
func Levenshtein(a, b string) int {
	ar := []rune(strings.ToLower(a))
	br := []rune(strings.ToLower(b))
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}
	prev := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur := make([]int, len(br)+1)
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(br)]
}

func maximumDistance(a, b string) int {
	maxLen := len([]rune(a))
	if n := len([]rune(b)); n > maxLen {
		maxLen = n
	}
	limit := maxLen / 3
	if limit < 2 {
		limit = 2
	}
	if limit > 3 {
		limit = 3
	}
	return limit
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

func lineStarts(src string) []int {
	starts := []int{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}
