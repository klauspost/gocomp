package db

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zeebo/xxh3"
)

// encKey and decKey include the library version, which is why a rebuild against
// a new klauspost/compress lands beside the old numbers instead of overwriting
// them. That coexistence is the "compare library versions" feature.
type encKey struct {
	codec   string
	level   int
	window  int
	threads int
	lib     string
}

type decKey struct {
	codec   string
	src     string
	window  int
	threads int
	lib     string
	srcLib  string
}

// lib is bounds-checked because the index comes from a file on disk.
func (r *Run) lib(i int) string {
	if i < 0 || i >= len(r.Libs) {
		return ""
	}
	return r.Libs[i]
}

func (r *Run) encKey(x EncRow) encKey {
	return encKey{x.Codec, x.Level, x.Window, x.Threads, r.lib(x.Lib)}
}

func (r *Run) decKey(x DecRow) decKey {
	return decKey{x.Codec, x.Src, x.Window, x.Threads, r.lib(x.Lib), r.lib(x.SrcLib)}
}

// gated is the environment the numbers depend on but the run ID deliberately
// excludes. Two runs that disagree on any of it are not comparable, so they are
// kept in separate files instead of averaging into each other. Corpus, CPU, Go
// version and bench version are absent because they are already in the ID.
func gated(r *Run) [][2]string {
	return [][2]string{
		{"goos", r.Env.GOOS},
		{"goarch", r.Env.GOARCH},
		{"goamd64", r.Env.GOAMD64},
		{"goarm64", r.Env.GOARM64},
		{"tags", r.Env.Tags},
		{"goexperiment", r.Env.Experiment},
		{"cgo", strconv.FormatBool(r.Env.CGO)},
		{"corpus_hash", r.CorpusHash},
		{"src_threads", strconv.Itoa(r.SrcThreads)},
		{"numcpu", strconv.Itoa(r.Env.NumCPU)},
		{"gogc", strconv.Itoa(r.Env.GOGC)},
		{"gomemlimit", strconv.FormatInt(r.Env.GOMemLimit, 10)},
	}
}

// envMismatch reports the first gated field that makes two runs incomparable.
// This is what stops a GOAMD64=v1 run merging into a GOAMD64=v4 one -- or a
// taskset-limited run merging into unrestricted numbers, which NumCPU catches.
func envMismatch(old, cur *Run) string {
	a, b := gated(old), gated(cur)
	for i := range a {
		if a[i][1] != b[i][1] {
			return fmt.Sprintf("%s differs (stored %q, this run %q)", a[i][0], a[i][1], b[i][1])
		}
	}
	return ""
}

// envTag names the file an incomparable run goes to. It hashes the whole gated
// set rather than the field that happened to differ, so one configuration
// always lands in one file: re-running it merges with itself instead of
// littering the database with a new run every time.
func envTag(r *Run) string {
	var b strings.Builder
	for _, f := range gated(r) {
		b.WriteString(f[0])
		b.WriteByte('=')
		b.WriteString(f[1])
		b.WriteByte(0)
	}
	return strconv.FormatUint(xxh3.HashString(b.String()), 36)
}

// Variant is the gated environment as a map, for the index. The site needs it
// to tell sibling runs apart; without it they would union back together in a
// chart, which is exactly what the separate files prevent. Empty values are
// dropped so the common case adds almost nothing to index.json.
func (r *Run) Variant() map[string]string {
	m := map[string]string{}
	for _, f := range gated(r) {
		if f[1] != "" {
			m[f[0]] = f[1]
		}
	}
	return m
}

// MergeResult summarises what Merge did, for the CLI to report.
type MergeResult struct {
	Added    int
	Improved int
	Kept     int
	Warnings []string
}

// Merge combines r into any existing run with the same identity and writes both
// the run file and the index. When newRun is set, r is stored separately.
func Merge(dir string, r *Run, newRun bool) (MergeResult, error) {
	var res MergeResult
	r.Schema = Schema
	if r.Created == "" {
		r.Created = nowUTC()
	}
	r.Updated = nowUTC()

	if newRun {
		r.ID += "-" + strconv.FormatInt(time.Now().UTC().Unix(), 36)
		res.Added = len(r.Encode) + len(r.Decode)
		return res, finish(dir, r)
	}

	old, err := LoadRun(dir, r.Corpus, r.ID)
	if err != nil {
		return res, err
	}
	// An incomparable environment must not cost the run: it moves to the file
	// its own environment names instead of being refused. Losing an hour of
	// measurements to a flag typo is a worse failure than an extra run entry.
	if old != nil {
		if bad := envMismatch(old, r); bad != "" {
			r.ID += "-" + envTag(r)
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("%s; kept apart from run %s as %s", bad, old.ID, r.ID))
			if old, err = LoadRun(dir, r.Corpus, r.ID); err != nil {
				return res, err
			}
		}
	}
	if old == nil {
		res.Added = len(r.Encode) + len(r.Decode)
		return res, finish(dir, r)
	}
	// Reaching here with a mismatch means the sibling's own environment moved,
	// which only a hand-edited file or a change to gated() can do.
	if bad := envMismatch(old, r); bad != "" {
		return res, fmt.Errorf("cannot merge into run %s: %s; re-run with -new-run to store it separately", r.ID, bad)
	}

	merged := *old
	merged.Updated = r.Updated
	merged.Merges = old.Merges + 1
	merged.Tool = r.Tool
	merged.Env = r.Env
	merged.CorpusSize, merged.CorpusFiles = r.CorpusSize, r.CorpusFiles
	if r.MinPasses < merged.MinPasses || merged.MinPasses == 0 {
		merged.MinPasses = r.MinPasses
	}

	encIdx := make(map[encKey]int, len(merged.Encode))
	for i, x := range merged.Encode {
		encIdx[merged.encKey(x)] = i
	}
	for _, x := range r.Encode {
		k := r.encKey(x)
		x.Lib = merged.libIndex(k.lib)
		i, ok := encIdx[k]
		if !ok {
			encIdx[k] = len(merged.Encode)
			merged.Encode = append(merged.Encode, x)
			res.Added++
			continue
		}
		cur := &merged.Encode[i]
		if cur.Out != x.Out {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"%s level %d window %d threads %d: output size changed %d -> %d with no library change; taking the new row",
				x.Codec, x.Level, x.Window, x.Threads, cur.Out, x.Out))
			passes := cur.Passes
			*cur = x
			cur.Passes += passes
			res.Improved++
			continue
		}
		passes, verified := cur.Passes+x.Passes, max(cur.Verified, x.Verified)
		if x.NS < cur.NS {
			*cur = x
			res.Improved++
		} else {
			res.Kept++
		}
		cur.Passes, cur.Verified = passes, verified
	}

	decIdx := make(map[decKey]int, len(merged.Decode))
	for i, x := range merged.Decode {
		decIdx[merged.decKey(x)] = i
	}
	for _, x := range r.Decode {
		k := r.decKey(x)
		x.Lib, x.SrcLib = merged.libIndex(k.lib), merged.libIndex(k.srcLib)
		i, ok := decIdx[k]
		if !ok {
			decIdx[k] = len(merged.Decode)
			merged.Decode = append(merged.Decode, x)
			res.Added++
			continue
		}
		cur := &merged.Decode[i]
		passes, verified := cur.Passes+x.Passes, max(cur.Verified, x.Verified)
		if x.NS < cur.NS {
			*cur = x
			res.Improved++
		} else {
			res.Kept++
		}
		cur.Passes, cur.Verified = passes, verified
	}

	merged.Errors = r.Errors
	*r = merged
	return res, finish(dir, r)
}

func finish(dir string, r *Run) error {
	sortRows(r)
	if err := r.Save(dir); err != nil {
		return err
	}
	ix, err := LoadIndex(dir)
	if err != nil {
		return err
	}
	ix.RefreshCatalogs()
	ix.upsert(r)
	return ix.Save(dir)
}

func sortRows(r *Run) {
	sort.SliceStable(r.Encode, func(i, j int) bool {
		a, b := r.Encode[i], r.Encode[j]
		if a.Codec != b.Codec {
			return a.Codec < b.Codec
		}
		if a.Level != b.Level {
			return a.Level < b.Level
		}
		if a.Window != b.Window {
			return a.Window < b.Window
		}
		if a.Threads != b.Threads {
			return a.Threads < b.Threads
		}
		return a.Lib < b.Lib
	})
	sort.SliceStable(r.Decode, func(i, j int) bool {
		a, b := r.Decode[i], r.Decode[j]
		if a.Codec != b.Codec {
			return a.Codec < b.Codec
		}
		if a.Src != b.Src {
			return a.Src < b.Src
		}
		if a.Window != b.Window {
			return a.Window < b.Window
		}
		if a.Threads != b.Threads {
			return a.Threads < b.Threads
		}
		if a.Lib != b.Lib {
			return a.Lib < b.Lib
		}
		return a.SrcLib < b.SrcLib
	})
}

// upsert replaces or appends the run's index entry.
func (ix *Index) upsert(r *Run) {
	mid := r.Env.MachineID()
	ix.Machines[mid] = MachineInfo{
		ID: mid, Label: r.Env.ShortCPU(), CPU: r.Env.CPU, OS: r.Env.OS, Mem: r.Env.MemTotal,
	}

	threads := map[int]bool{}
	windows := map[int]bool{}
	codecs := map[string]bool{}
	libs := map[string]map[string]bool{}
	noteLib := func(i int) {
		if i < 0 || i >= len(r.Libs) {
			return
		}
		mod, ver, _ := strings.Cut(r.Libs[i], "@")
		if libs[mod] == nil {
			libs[mod] = map[string]bool{}
		}
		libs[mod][ver] = true
	}
	for _, x := range r.Encode {
		threads[x.Threads] = true
		windows[x.Window] = true
		codecs[x.Codec] = true
		noteLib(x.Lib)
	}
	for _, x := range r.Decode {
		threads[x.Threads] = true
		windows[x.Window] = true
		codecs[x.Codec] = true
		noteLib(x.Lib)
		noteLib(x.SrcLib)
	}

	stub := RunStub{
		ID: r.ID, File: "runs/" + r.Corpus + "-" + r.ID + ".json",
		Corpus: r.Corpus, Kind: r.Kind, Machine: mid,
		Go: r.Env.Go, GOOS: r.Env.GOOS, GOARCH: r.Env.GOARCH, GOAMD64: r.Env.GOAMD64,
		Bench: r.Bench, Threads: sortedKeys(threads), Windows: sortedKeys(windows), Codecs: sortedStrings(codecs),
		Libs: map[string][]string{}, Variant: r.Variant(),
		NEncode: len(r.Encode), NDecode: len(r.Decode),
		Merges: r.Merges, Created: r.Created, Updated: r.Updated,
	}
	for mod, vers := range libs {
		stub.Libs[mod] = sortedStrings(vers)
	}

	for i := range ix.Runs {
		if ix.Runs[i].ID == r.ID && ix.Runs[i].Corpus == r.Corpus {
			ix.Runs[i] = stub
			return
		}
	}
	ix.Runs = append(ix.Runs, stub)
}

func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func sortedStrings(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
