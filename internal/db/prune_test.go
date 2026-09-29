package db

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// pruneFixture has one zskp run in which stdzstd both encodes and decodes, so
// "stdzstd's rows" and "rows decoding stdzstd's output" are different sets.
func pruneFixture(t *testing.T) (string, RunStub) {
	t.Helper()
	dir := t.TempDir()
	r := runFixture("v1.0.0", 1000, 1)
	std := r.LibIndex("std", "go1.26.1")
	r.Encode = append(r.Encode, EncRow{Codec: "stdzstd", Level: 3, Threads: 8, Lib: std, In: 1000, Out: 390, NS: 900, Passes: 3})
	r.Decode = append(r.Decode,
		DecRow{Codec: "stdzstd", Src: "zskp-2", Lib: std, SrcLib: 0, Threads: 8, In: 400, Out: 1000, NS: 600, Passes: 3},
		DecRow{Codec: "zskp", Src: "stdzstd-3", Lib: 0, SrcLib: std, Threads: 8, In: 390, Out: 1000, NS: 500, Passes: 3},
	)
	r.Errors = []RunError{{Op: "d", Codec: "stdzstd", Threads: 8, Msg: "boom"}}
	if _, err := Merge(dir, r, false); err != nil {
		t.Fatal(err)
	}
	ix, err := LoadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, ix.Runs[0]
}

func TestPruneFilters(t *testing.T) {
	for _, c := range []struct {
		name     string
		f        Filter
		enc, dec []string // codec or decoder/source left over
	}{
		{"nothing", Filter{}, []string{"stdzstd", "zskp"}, []string{"stdzstd/zskp-2", "zskp/stdzstd-3", "zskp/zskp-2"}},
		{"codec", Filter{Codecs: []string{"stdzstd"}}, []string{"zskp"}, []string{"zskp/stdzstd-3", "zskp/zskp-2"}},
		{"source", Filter{Sources: []string{"stdzstd"}}, []string{"stdzstd", "zskp"}, []string{"stdzstd/zskp-2", "zskp/zskp-2"}},
		{"both", Filter{Codecs: []string{"stdzstd"}, Sources: []string{"stdzstd"}}, []string{"zskp"}, []string{"zskp/zskp-2"}},
		// A decode row goes when either side was measured with the version.
		{"library", Filter{Libs: []string{"std@go1.26.1"}}, []string{"zskp"}, []string{"zskp/zskp-2"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir, stub := pruneFixture(t)
			if _, err := Prune(dir, []RunStub{stub}, c.f); err != nil {
				t.Fatal(err)
			}
			r, err := LoadRun(dir, stub.Corpus, stub.ID)
			if err != nil {
				t.Fatal(err)
			}
			var enc, dec []string
			for _, x := range r.Encode {
				enc = append(enc, x.Codec)
			}
			for _, x := range r.Decode {
				dec = append(dec, x.Codec+"/"+x.Src)
			}
			if !slices.Equal(enc, c.enc) || !slices.Equal(dec, c.dec) {
				t.Errorf("left encode %v decode %v, want %v %v", enc, dec, c.enc, c.dec)
			}
			if gone := len(r.Errors) == 0; gone != (len(c.f.Codecs) > 0) {
				t.Errorf("errors %v: a codec's errors go with its rows, and only then", r.Errors)
			}
			ix, _ := LoadIndex(dir)
			if ix.Runs[0].NEncode != len(enc) || ix.Runs[0].NDecode != len(dec) {
				t.Errorf("index counts %d/%d, want %d/%d", ix.Runs[0].NEncode, ix.Runs[0].NDecode, len(enc), len(dec))
			}
		})
	}
}

// Pruned has to leave its receiver alone: the TUI previews a delete with it on
// the copies it is still displaying.
func TestPrunedIsAPreview(t *testing.T) {
	dir, stub := pruneFixture(t)
	r, _ := LoadRun(dir, stub.Corpus, stub.ID)
	p, enc, dec := r.Pruned(Filter{Codecs: []string{"stdzstd"}})
	if enc != 1 || dec != 1 || len(p.Encode) != 1 || len(p.Decode) != 2 {
		t.Errorf("pruned %d/%d leaving %d/%d, want 1/1 leaving 1/2", enc, dec, len(p.Encode), len(p.Decode))
	}
	if len(r.Encode) != 2 || len(r.Decode) != 3 || len(r.Errors) != 1 {
		t.Errorf("the receiver changed: %d/%d rows, %d errors", len(r.Encode), len(r.Decode), len(r.Errors))
	}
}

// The counts shown beside each choice must be what that choice alone deletes.
func TestTallyMatchesPruned(t *testing.T) {
	dir, stub := pruneFixture(t)
	r, _ := LoadRun(dir, stub.Corpus, stub.ID)
	tl := r.Tally()
	check := func(what string, counts map[string]int, f func(string) Filter) {
		for k, n := range counts {
			if _, e, d := r.Pruned(f(k)); e+d != n {
				t.Errorf("%s %s: tally %d, Pruned deletes %d", what, k, n, e+d)
			}
		}
	}
	check("codec", tl.Codecs, func(k string) Filter { return Filter{Codecs: []string{k}} })
	check("source", tl.Sources, func(k string) Filter { return Filter{Sources: []string{k}} })
	check("library", tl.Libs, func(k string) Filter { return Filter{Libs: []string{k}} })
	if len(tl.Codecs) != 2 || len(tl.Sources) != 2 || len(tl.Libs) != 2 {
		t.Errorf("tally %+v, want two keys of each", tl)
	}
}

// A run with nothing left is removed with its machine, not kept as an empty
// entry; a run that remains keeps the machine it shares.
func TestPruneRemovesEmptiedRuns(t *testing.T) {
	dir, stub := pruneFixture(t)
	res, err := Prune(dir, []RunStub{stub}, Filter{Codecs: []string{"zskp", "stdzstd"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Encode != 2 || res.Decode != 3 || res.Removed != 1 {
		t.Errorf("got %+v, want 2 encode, 3 decode, 1 removed", res)
	}
	if _, err := os.Stat(filepath.Join(dir, stub.File)); !os.IsNotExist(err) {
		t.Errorf("the emptied run file is still there: %v", err)
	}
	ix, _ := LoadIndex(dir)
	if len(ix.Runs) != 0 || len(ix.Machines) != 0 {
		t.Errorf("index still holds %d runs, %d machines", len(ix.Runs), len(ix.Machines))
	}
}

func TestRemoveKeepsSharedMachines(t *testing.T) {
	dir := t.TempDir()
	if _, err := Merge(dir, runFixture("v1.0.0", 1000, 1), false); err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(dir, runFixture("v1.0.0", 1000, 1), true); err != nil {
		t.Fatal(err)
	}
	ix, _ := LoadIndex(dir)
	if err := Remove(dir, ix.Runs[:1]); err != nil {
		t.Fatal(err)
	}
	ix, _ = LoadIndex(dir)
	if len(ix.Runs) != 1 || len(ix.Machines) != 1 {
		t.Fatalf("got %d runs, %d machines, want the sibling and its machine", len(ix.Runs), len(ix.Machines))
	}
	if r, _ := LoadRun(dir, ix.Runs[0].Corpus, ix.Runs[0].ID); r == nil {
		t.Error("removing one run took its sibling's file")
	}
}
