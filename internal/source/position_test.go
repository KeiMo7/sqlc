package source

import (
	"reflect"
	"testing"
)

func TestLineColumnUsesByteOffsets(t *testing.T) {
	src := "one\r\n猫x\nthree"
	cases := []struct {
		name   string
		offset int
		want   Position
	}{
		{"first line", 1, Position{Offset: 1, Line: 1, Column: 2}},
		{"second line", 5, Position{Offset: 5, Line: 2, Column: 1}},
		{"after multibyte", 8, Position{Offset: 8, Line: 2, Column: 4}},
		{"third line", 10, Position{Offset: 10, Line: 3, Column: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LineColumn(src, tc.offset); got != tc.want {
				t.Fatalf("LineColumn(%d) = %#v, want %#v", tc.offset, got, tc.want)
			}
		})
	}
}

func TestByteOffsetRoundTrip(t *testing.T) {
	src := "αβ\n猫 = 1\n"
	offsets := []int{len(src)}
	for offset := range src {
		offsets = append(offsets, offset)
	}
	for _, offset := range offsets {
		pos := LineColumn(src, offset)
		if got := ByteOffset(src, pos.Line, pos.Column); got != offset {
			t.Fatalf("ByteOffset(LineColumn(%d)) = %d", offset, got)
		}
	}
}

func TestRankCandidatesIsStableAndLimited(t *testing.T) {
	got := RankCandidates("usr_id", []string{
		"user_id",
		"users",
		"user_uuid",
		"user_id",
		"excluded",
	}, 3)
	want := []Candidate{{Name: "user_id", Distance: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RankCandidates = %#v, want %#v", got, want)
	}

	first := RankCandidates("emial", []string{"email", "emil", "mail", "female"}, 3)
	second := RankCandidates("emial", []string{"female", "mail", "emil", "email"}, 3)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("candidate order changed: %#v vs %#v", first, second)
	}
	if len(first) > 3 {
		t.Fatalf("got %d candidates, want at most 3", len(first))
	}
}
