package cli

import (
	"slices"
	"testing"

	"github.com/klauspost/gocomp/internal/corpus"
)

func TestParseLevels(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []int
	}{
		{"1", []int{1}},
		{"-2", []int{-2}}, // leading '-' is a sign, not a range
		{"1-9", []int{1, 2, 3, 4, 5, 6, 7, 8, 9}},
		{"1-9,-2", []int{-2, 1, 2, 3, 4, 5, 6, 7, 8, 9}},
		{"-3--1", []int{-3, -2, -1}},              // a range of negative levels
		{"9-1", []int{1, 2, 3, 4, 5, 6, 7, 8, 9}}, // reversed ranges are tolerated
		{"3,3,3", []int{3}},                       // duplicates collapse
	} {
		got, err := parseLevels(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%q = %v, want %v", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"x", "1-x", "--"} {
		if _, err := parseLevels(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

// The codec spec is comma separated and so are its level lists; the rule is
// that a numeric item continues the previous codec, which works because codec
// ids are never numeric.
func TestParseCodecs(t *testing.T) {
	got, err := parseCodecs("zskp:1-4,s2,gzkp:1,5,9")
	if err != nil {
		t.Fatal(err)
	}
	want := []codecSel{
		{id: "zskp", levels: []int{1, 2, 3, 4}},
		{id: "s2"},
		{id: "gzkp", levels: []int{1, 5, 9}},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d selections, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].id != want[i].id || !slices.Equal(got[i].levels, want[i].levels) {
			t.Errorf("%d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestVariantsFor(t *testing.T) {
	sels, err := parseCodecs("zskp:1-2,s2")
	if err != nil {
		t.Fatal(err)
	}
	mode, err := parseLevelMode("default")
	if err != nil {
		t.Fatal(err)
	}
	v, err := variantsFor(sels, mode, corpus.KindFile, false)
	if err != nil {
		t.Fatal(err)
	}
	// zskp takes its explicit levels; s2 falls back to the -level mode.
	var ids []string
	for _, x := range v {
		ids = append(ids, x.ID())
	}
	if !slices.Contains(ids, "zskp-1") || !slices.Contains(ids, "zskp-2") {
		t.Errorf("explicit levels missing: %v", ids)
	}
	if slices.Contains(ids, "zskp-3") {
		t.Errorf("explicit levels should exclude 3: %v", ids)
	}
	if !slices.Contains(ids, "s2-1") {
		t.Errorf("s2 should fall back to its default level: %v", ids)
	}
}

func TestVariantsForDedupesCodecs(t *testing.T) {
	sels, _ := parseCodecs("s2,s2")
	mode, _ := parseLevelMode("default")
	v, err := variantsFor(sels, mode, corpus.KindFile, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 1 {
		t.Errorf("a repeated codec id produced %d variants, want 1", len(v))
	}
}

func TestVariantsForSkipsUnsupportedKind(t *testing.T) {
	sels, _ := parseCodecs("s2blk")
	mode, _ := parseLevelMode("all")
	// s2blk is directory-only, so on a single-file corpus there is nothing to run.
	if _, err := variantsFor(sels, mode, corpus.KindFile, false); err == nil {
		t.Error("expected an error when nothing in the selection supports the kind")
	}
	if _, err := variantsFor(sels, mode, corpus.KindDir, false); err != nil {
		t.Errorf("s2blk should run on directory corpora: %v", err)
	}
}

func TestSweep(t *testing.T) {
	if got := sweep(16); !slices.Equal(got, []int{1, 2, 4, 8, 16}) {
		t.Errorf("sweep(16) = %v", got)
	}
	if got := sweep(12); !slices.Equal(got, []int{1, 2, 4, 8, 12}) {
		t.Errorf("sweep(12) = %v", got)
	}
	if got := sweep(1); !slices.Equal(got, []int{1}) {
		t.Errorf("sweep(1) = %v", got)
	}
}
