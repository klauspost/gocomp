package db

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/klauspost/compress-site/internal/codec"
)

// Filter names the rows Prune deletes. Every field adds to what matches, so an
// empty filter deletes nothing.
type Filter struct {
	Codecs  []string // encode rows of these encoders, decode rows of these decoders
	Sources []string // decode rows whose input one of these encoders made
	Libs    []string // "module@version": rows measured with it, or decoding its output
}

func (f Filter) enc(r *Run, x EncRow) bool {
	return slices.Contains(f.Codecs, x.Codec) || slices.Contains(f.Libs, r.lib(x.Lib))
}

func (f Filter) dec(r *Run, x DecRow) bool {
	src, _, _ := codec.SplitVariant(x.Src)
	return slices.Contains(f.Codecs, x.Codec) || slices.Contains(f.Sources, src) ||
		slices.Contains(f.Libs, r.lib(x.Lib)) || slices.Contains(f.Libs, r.lib(x.SrcLib))
}

// Pruned is r without the rows f matches, and how many those were. r itself is
// untouched, so the same call previews a delete and performs it. Errors of a
// deleted codec go too, or the site would report failures for a codec it no
// longer has numbers for.
func (r *Run) Pruned(f Filter) (out *Run, enc, dec int) {
	c := *r
	c.Encode = slices.DeleteFunc(slices.Clone(r.Encode), func(x EncRow) bool { return f.enc(r, x) })
	c.Decode = slices.DeleteFunc(slices.Clone(r.Decode), func(x DecRow) bool { return f.dec(r, x) })
	c.Errors = slices.DeleteFunc(slices.Clone(r.Errors), func(e RunError) bool { return slices.Contains(f.Codecs, e.Codec) })
	return &c, len(r.Encode) - len(c.Encode), len(r.Decode) - len(c.Decode)
}

// Tally counts, for every key a Filter can name, the rows of r that key alone
// matches. A combined filter can delete fewer than the sum, since one row can
// match several keys; Pruned gives the exact figure.
type Tally struct {
	Codecs, Sources, Libs map[string]int
}

func (r *Run) Tally() Tally {
	t := Tally{map[string]int{}, map[string]int{}, map[string]int{}}
	for _, x := range r.Encode {
		t.Codecs[x.Codec]++
		t.Libs[r.lib(x.Lib)]++
	}
	for _, x := range r.Decode {
		t.Codecs[x.Codec]++
		if src, _, ok := codec.SplitVariant(x.Src); ok {
			t.Sources[src]++
		}
		a, b := r.lib(x.Lib), r.lib(x.SrcLib)
		t.Libs[a]++
		if b != a {
			t.Libs[b]++
		}
	}
	delete(t.Libs, "") // an out-of-range index names nothing a filter can hold
	return t
}

// PruneResult summarises what Prune did.
type PruneResult struct {
	Encode, Decode int
	Removed        int // runs left without rows
}

// Prune deletes the rows f matches from runs. Each is re-read from disk rather
// than taken from the caller, so a merge made since the list was shown is not
// written over. A run left with no rows is removed: an entry with nothing to
// plot would only clutter the site's filters.
func Prune(dir string, runs []RunStub, f Filter) (PruneResult, error) {
	var res PruneResult
	ix, err := LoadIndex(dir)
	if err != nil {
		return res, err
	}
	for _, s := range runs {
		r, err := LoadRun(dir, s.Corpus, s.ID)
		if err != nil {
			return res, err
		}
		if r == nil {
			continue // listed, but gone since: nothing left to delete from
		}
		p, e, d := r.Pruned(f)
		if e+d == 0 && len(p.Errors) == len(r.Errors) {
			continue
		}
		res.Encode, res.Decode = res.Encode+e, res.Decode+d
		if len(p.Encode)+len(p.Decode) == 0 {
			res.Removed++
			err = ix.remove(dir, s)
		} else if err = p.Save(dir); err == nil {
			ix.upsert(p)
		}
		if err != nil {
			return res, err
		}
	}
	return res, ix.Save(dir)
}

// Remove deletes whole runs: their files and their index entries.
func Remove(dir string, runs []RunStub) error {
	ix, err := LoadIndex(dir)
	if err != nil {
		return err
	}
	for _, s := range runs {
		if err := ix.remove(dir, s); err != nil {
			return err
		}
	}
	return ix.Save(dir)
}

// remove also drops the run's machine once no run is left on it: the index is
// published, and hardware nothing was measured on is not worth describing.
func (ix *Index) remove(dir string, s RunStub) error {
	p := filepath.Join(dir, (&Run{Corpus: s.Corpus, ID: s.ID}).fileName())
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	ix.Runs = slices.DeleteFunc(ix.Runs, func(x RunStub) bool { return x.ID == s.ID && x.Corpus == s.Corpus })
	if !slices.ContainsFunc(ix.Runs, func(x RunStub) bool { return x.Machine == s.Machine }) {
		delete(ix.Machines, s.Machine)
	}
	return nil
}
