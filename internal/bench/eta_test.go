package bench

import (
	"context"
	"maps"
	"testing"
	"time"

	"github.com/klauspost/gocomp/internal/codec"
	"github.com/klauspost/gocomp/internal/corpus"
	"github.com/klauspost/gocomp/internal/db"
	"github.com/zeebo/xxh3"
)

// Passes is pinned so a measurement always costs three of its passes, which
// keeps the expected figures readable.
func etaFixture(t *testing.T) (Config, []Variant, []corpus.Corpus, History) {
	t.Helper()
	cfg := Config{
		Threads: []int{1}, SrcThreads: 1, Passes: 3, MaxPasses: 3,
		MinWall: time.Second, MaxJob: time.Hour, Encode: true,
	}
	vs := testVariants(t)[:3]
	key := func(v Variant) Key { return Key{OpEncode, v.Enc.ID, v.ID(), 0, 1} }
	// vs[0] has a's own numbers, and borrows the median of a, c and d on b.
	// vs[1] is only known on c, so it borrows everywhere. vs[2] is unknown.
	h := History{
		key(vs[0]): {"a": {2}, "c": {4}, "d": {10}},
		key(vs[1]): {"c": {3}},
	}
	cps := []corpus.Corpus{
		{ID: "a", Kind: corpus.KindFile, Size: 1e6},
		{ID: "b", Kind: corpus.KindFile, Size: 1e6},
	}
	return cfg, vs, cps, h
}

func TestETAPricesFromHistory(t *testing.T) {
	cfg, vs, cps, h := etaFixture(t)
	eta := NewETA(cfg, h, cps, map[corpus.Kind][]Variant{corpus.KindFile: vs})
	// a: 3*2ms + 3*3ms, b: 3*4ms + 3*3ms.
	cur, total, unknown := eta.Left()
	if cur != 0 || total != 36*time.Millisecond || unknown != 2 {
		t.Errorf("before the run: cur %s, total %s, %d unknown; want 0, 36ms, 2", cur, total, unknown)
	}
}

// A codec that declares no concurrency runs on one thread at any GOMAXPROCS,
// so it is priced from whatever thread count is stored. One that does is not.
func TestETAPoolsThreadsOnlyForSingleThreadedCodecs(t *testing.T) {
	var vs []Variant
	for _, id := range []string{"br", "s2"} {
		e, ok := codec.LookupEnc(id)
		if !ok {
			t.Fatalf("codec %s is not registered", id)
		}
		vs = append(vs, Variant{Enc: e, Level: e.Levels[0]})
	}
	if vs[0].Enc.Concurrent(corpus.KindFile) || !vs[1].Enc.Concurrent(corpus.KindFile) {
		t.Fatal("the fixture needs br single-threaded and s2 concurrent on files")
	}
	dir := t.TempDir()
	r := &db.Run{ID: "x", Corpus: "a", Kind: "file", Bench: "1"}
	lib := r.LibIndex("example.com/mod", "v1.0.0")
	for _, v := range vs {
		r.Encode = append(r.Encode, db.EncRow{Codec: v.Enc.ID, Level: v.Level.N, Threads: 1, Lib: lib, In: 1e6, Out: 1, NS: 2e6, Passes: 3})
	}
	if _, err := db.Merge(dir, r, false); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Threads: []int{16}, SrcThreads: 16, Passes: 3, MaxPasses: 3, MinWall: time.Second, MaxJob: time.Hour, Encode: true}
	eta := NewETA(cfg, LoadHistory(dir), []corpus.Corpus{{ID: "a", Kind: corpus.KindFile, Size: 1e6}},
		map[corpus.Kind][]Variant{corpus.KindFile: vs})
	if _, total, unknown := eta.Left(); total != 6*time.Millisecond || unknown != 1 {
		t.Errorf("at 16 threads from 1-thread rows: total %s, %d unknown; want br's 6ms and s2 unknown", total, unknown)
	}
}

func TestETAFollowsTheRun(t *testing.T) {
	cfg, vs, cps, h := etaFixture(t)
	eta := NewETA(cfg, h, cps, map[corpus.Kind][]Variant{corpus.KindFile: vs})
	res := func(v Variant, passes int, best time.Duration) *Result {
		return &Result{Op: OpEncode, Codec: v.Enc.ID, Level: v.Level.N, Threads: 1, Passes: passes, Best: best}
	}
	want := func(step string, wc, wt time.Duration, wu int) {
		t.Helper()
		if c, tot, u := eta.Left(); c != wc || tot != wt || u != wu {
			t.Errorf("%s: cur %s, total %s, %d unknown; want %s, %s, %d", step, c, tot, u, wc, wt, wu)
		}
	}
	// 0 wants Pass to know nothing.
	pass := func(step string, v Variant, wd time.Duration) {
		t.Helper()
		if d, ok := eta.Pass(v.encKey(1)); d != wd || ok != (wd > 0) {
			t.Errorf("%s: next pass of %s %s, %v; want %s", step, v.Label(), d, ok, wd)
		}
	}

	eta.Observe(Event{Kind: "corpus", Corpus: "a"})
	want("start of a", 15*time.Millisecond, 36*time.Millisecond, 2)
	pass("start of a", vs[0], 2*time.Millisecond)
	pass("start of a", vs[2], 0)

	// Measured twice as slow as borrowed, which says nothing about vs[0]: a's
	// own numbers already priced it.
	eta.Observe(Event{Kind: "pass", Result: res(vs[1], 1, 6*time.Millisecond), Target: 3})
	want("first pass", 18*time.Millisecond, 39*time.Millisecond, 2)
	pass("first pass", vs[1], 6*time.Millisecond)
	pass("first pass", vs[0], 2*time.Millisecond)

	eta.Observe(Event{Kind: "result", Result: res(vs[0], 3, 2*time.Millisecond)})
	eta.Observe(Event{Kind: "result", Result: res(vs[1], 3, 6*time.Millisecond)})
	eta.Observe(Event{Kind: "warn", Result: res(vs[2], 0, 0)})
	want("end of a", 0, 21*time.Millisecond, 1)
	pass("end of a", vs[0], 0)

	eta.Observe(Event{Kind: "corpus", Corpus: "b"})
	want("start of b", 21*time.Millisecond, 21*time.Millisecond, 1)
	pass("start of b", vs[1], 3*time.Millisecond)

	// Borrowed prices on b run twice as long, so vs[1]'s doubles too.
	eta.Observe(Event{Kind: "pass", Result: res(vs[0], 1, 8*time.Millisecond), Target: 3})
	want("calibrated", 34*time.Millisecond, 34*time.Millisecond, 1)
	pass("calibrated", vs[1], 6*time.Millisecond)
}

// Every measurement the Engine makes has to land on one the estimate planned,
// or it waits for it forever: with no history, all are unknown until then.
func TestETAMatchesEveryMeasurement(t *testing.T) {
	file := testData(t)
	file.Size = file.Total
	a, b := file.File[:200<<10], file.File[200<<10:]
	dir := &corpus.Data{
		Corpus: corpus.Corpus{ID: "testdir", Kind: corpus.KindDir, Size: file.Total},
		Files: []corpus.File{
			{Name: "a", Data: a, Hash: xxh3.Hash(a)},
			{Name: "b", Data: b, Hash: xxh3.Hash(b)},
		},
		Total: file.Total, Max: len(b),
	}
	decodeOnly := handoffConfig([]int{1}, 1)
	decodeOnly.Encode = false
	windows := handoffConfig([]int{1}, 1)
	windows.Windows = []int{0, 16 << 10, 4 << 20} // 4M is past the input, so it goes

	for _, tc := range []struct {
		name string
		cfg  Config
		data *corpus.Data
	}{
		{"inline", handoffConfig([]int{1, 2}, 2), file},
		{"reencode", handoffConfig([]int{1}, 2), file},
		{"decode-only", decodeOnly, file},
		{"windows", windows, file},
		{"directory", handoffConfig([]int{1}, 1), dir},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vs := map[corpus.Kind][]Variant{tc.data.Kind: handoffVariants(t)}
			eta := NewETA(tc.cfg, History{}, []corpus.Corpus{tc.data.Corpus}, vs)
			run := tc.cfg.Expand(vs[tc.data.Kind], tc.data.Max)
			enc, dec := Plan(tc.cfg, tc.data.Kind, run)
			if _, _, n := eta.Left(); n != enc+dec {
				t.Errorf("planned %d measurements, Plan counts %d", n, enc+dec)
			}

			events := make(chan Event, 1<<14)
			eng := &Engine{Cfg: tc.cfg, Data: tc.data, Variants: run, Events: events}
			eta.Observe(Event{Kind: "corpus", Corpus: tc.data.ID})
			if _, err := eng.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			close(events)
			// A job names the pass about to start, so its Key must be the one
			// that pass reports under.
			jobs, passes := map[Key]bool{}, map[Key]bool{}
			for ev := range events {
				eta.Observe(ev)
				switch {
				case ev.Kind == "job" && ev.Key != Key{}:
					jobs[ev.Key] = true
				case ev.Kind == "pass":
					passes[ev.Result.Key()] = true
				}
			}
			if c, tot, n := eta.Left(); c != 0 || tot != 0 || n != 0 {
				t.Errorf("after the run: cur %s, total %s, %d never seen", c, tot, n)
			}
			if !maps.Equal(jobs, passes) {
				t.Errorf("job keys %v do not match pass keys %v", jobs, passes)
			}
		})
	}
}
