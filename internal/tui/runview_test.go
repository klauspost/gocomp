package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/gocomp/internal/bench"
	"github.com/klauspost/gocomp/internal/codec"
	"github.com/klauspost/gocomp/internal/corpus"
	"github.com/klauspost/gocomp/internal/db"
	"github.com/klauspost/gocomp/internal/sysinfo"
)

// The run screen is the only place that says which corpus the numbers on it
// belong to, and a run walks several corpora over hours.
func TestRunViewNamesTheCorpus(t *testing.T) {
	m := newTestModel(t)
	m.scr = scrRun
	m.run = &runState{events: make(chan bench.Event, 8), phase: "starting"}

	m.onEvent(bench.Event{Kind: "phase", Corpus: "enwik9", Phase: "decode"})
	m.onEvent(bench.Event{
		Kind: "job", Corpus: "enwik9",
		Label: "gzkp <- pgzip-5", Msg: "16 thread(s)",
	})

	got := m.runView()
	if !strings.Contains(got, "enwik9 · decode: gzkp <- pgzip-5  16 thread(s)") {
		t.Errorf("run view does not name the corpus:\n%s", got)
	}

	// Moving to the next corpus must move the heading with it.
	m.onEvent(bench.Event{Kind: "job", Corpus: "silesia", Label: "zskp-2", Msg: "1 thread(s)"})
	if got := m.runView(); !strings.Contains(got, "silesia") || strings.Contains(got, "enwik9") {
		t.Errorf("heading did not follow the corpus:\n%s", got)
	}
}

// A run over many corpora says how far through them it is, without the corpus
// count leaking into the measurement progress bar.
func TestRunViewCountsCorpora(t *testing.T) {
	m := newTestModel(t)
	m.scr = scrRun
	m.run = &runState{events: make(chan bench.Event, 8), phase: "encode", done: 7, total: 40}

	m.onEvent(bench.Event{Kind: "corpus", Corpus: "enwik9", Done: 2, Total: 12})
	got := m.runView()
	if !strings.Contains(got, "enwik9 (2/12) · encode") {
		t.Errorf("run view does not count corpora:\n%s", got)
	}
	if m.run.done != 7 || m.run.total != 40 {
		t.Errorf("corpus event moved the measurement progress to %d/%d", m.run.done, m.run.total)
	}

	// One corpus has nothing to count.
	m.onEvent(bench.Event{Kind: "corpus", Corpus: "enwik9", Done: 1, Total: 1})
	if got := m.runView(); strings.Contains(got, "(1/1)") {
		t.Errorf("a single-corpus run shows a count:\n%s", got)
	}
}

// The badge must not claim PAUSED while a pass is still running, and time spent
// stopped is not benchmark time.
func TestRunViewShowsPause(t *testing.T) {
	m := newTestModel(t)
	m.scr = scrRun
	p := &bench.Pause{}
	m.run = &runState{pause: p, started: time.Now().Add(-time.Minute), phase: "encode"}
	if got := m.runView(); strings.Contains(got, "PAUS") {
		t.Fatalf("badge shown before pausing:\n%s", got)
	}

	keys(t, m, "p")
	if !p.Paused() {
		t.Fatal("p did not pause")
	}
	if got := m.runView(); !strings.Contains(got, "pausing after this pass") {
		t.Errorf("a pause the worker has not reached yet is not shown as pending:\n%s", got)
	}

	worker := make(chan struct{})
	go func() { _ = p.Wait(context.Background()); close(worker) }()
	for held, _ := p.Held(); !held; held, _ = p.Held() {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if got := m.runView(); !strings.Contains(got, "PAUSED") {
		t.Errorf("held run not shown as paused:\n%s", got)
	}
	if !strings.Contains(m.footer(), "p resume") {
		t.Errorf("footer does not say how to resume: %q", m.footer())
	}
	if lost := time.Since(m.run.started) - m.run.since(); lost < 20*time.Millisecond {
		t.Errorf("clock kept counting while paused: only %s excluded", lost)
	}

	keys(t, m, "p")
	<-worker
	if got := m.runView(); strings.Contains(got, "PAUS") {
		t.Errorf("badge still shown after resuming:\n%s", got)
	}
}

// The estimate comes from the database, counts down between events, and says
// so when there is nothing to go on.
func TestRunViewEstimatesTimeLeft(t *testing.T) {
	m := newTestModel(t)
	e, ok := codec.LookupEnc("s2")
	if !ok {
		t.Fatal("s2 is not registered")
	}
	v := bench.Variant{Enc: e, Level: e.Levels[0]}
	r := &db.Run{ID: "x", Corpus: "c1", Kind: "file", Bench: "1", Env: sysinfo.Env{Go: "go1.27.1"}}
	lib := r.LibIndex("example.com/mod", "v1.0.0")
	// 10s a pass over c1; c2 has no numbers of its own, so it borrows c1's.
	r.Encode = []db.EncRow{{Codec: "s2", Level: v.Level.N, Threads: 1, Lib: lib, In: 1e6, Out: 1, NS: 10e9, Passes: 3}}
	if _, err := db.Merge(m.opts.Out, r, false); err != nil {
		t.Fatal(err)
	}
	cfg := bench.Config{Threads: []int{1}, SrcThreads: 1, Passes: 3, MaxPasses: 3, MinWall: time.Second, MaxJob: time.Hour, Encode: true}
	cps := []corpus.Corpus{{ID: "c1", Kind: corpus.KindFile, Size: 1e6}, {ID: "c2", Kind: corpus.KindFile, Size: 1e6}}
	vs := map[corpus.Kind][]bench.Variant{corpus.KindFile: {v}}

	m.scr = scrRun
	m.run = &runState{
		events: make(chan bench.Event, 8), started: time.Now().Add(-time.Minute), phase: "starting", etaAt: -1,
		eta: bench.NewETA(cfg, bench.LoadHistory(m.opts.Out), cps, vs),
	}
	m.onEvent(bench.Event{Kind: "corpus", Corpus: "c1", Done: 1, Total: 2})
	got := m.runView()
	if !strings.Contains(got, "· ~1m left") || !strings.Contains(got, "c1 (1/2, ~30s left)") {
		t.Errorf("run view does not estimate 1m in all and 30s for c1:\n%s", got)
	}

	m.onEvent(bench.Event{Kind: "phase", Corpus: "c1", Phase: "encode"})
	m.run.etaAt -= 20 * time.Second
	if got := m.runView(); !strings.Contains(got, "· ~40s left") || !strings.Contains(got, "(1/2, ~10s left)") {
		t.Errorf("estimate did not count down 20s:\n%s", got)
	}

	res := &bench.Result{Op: bench.OpEncode, Codec: "s2", Level: v.Level.N, Threads: 1, In: 1e6, Out: 1, Passes: 1, Best: 2 * time.Second}
	// The pass running now is priced from c1's 10s until it has one of its own.
	m.onEvent(bench.Event{Kind: "job", Corpus: "c1", Label: v.Label(), Msg: "1 thread(s)", Key: res.Key()})
	m.run.passAt -= 4 * time.Second
	if got := m.runView(); !strings.Contains(got, "1 thread(s) · ~6s left") {
		t.Errorf("heading does not count down the running pass:\n%s", got)
	}
	m.run.passAt -= 10 * time.Second
	if got := m.runView(); !strings.Contains(got, "1 thread(s) · 14s, expected ~10s") {
		t.Errorf("a pass past its estimate does not say so:\n%s", got)
	}

	m.onEvent(bench.Event{Kind: "pass", Corpus: "c1", Result: res, Target: 4})
	if got := m.runView(); !strings.Contains(got, "n=1/4 · 3 more ≈ 6s") {
		t.Errorf("live row does not show passes to go and their time:\n%s", got)
	}
	m.onEvent(bench.Event{Kind: "job", Corpus: "c1", Label: v.Label(), Msg: "1 thread(s)", Key: res.Key()})
	if got := m.runView(); !strings.Contains(got, "1 thread(s) · ~2s left") {
		t.Errorf("the next pass is not priced from the measurement's own best:\n%s", got)
	}

	m.run.eta = bench.NewETA(cfg, bench.History{}, cps, vs)
	if got := m.runView(); !strings.Contains(got, "no history to estimate from") {
		t.Errorf("an estimate with nothing to go on is not called out:\n%s", got)
	}
}

// "In progress" has to mean still running: one row per measurement, gone once
// its result lands, and nothing carried over from the corpus before.
func TestRunViewLiveRowsAreStillRunning(t *testing.T) {
	m := newTestModel(t)
	m.scr = scrRun
	m.run = &runState{events: make(chan bench.Event, 8), phase: "encode"}
	res := func(codec string, passes int) *bench.Result {
		return &bench.Result{Op: bench.OpEncode, Codec: codec, Level: 1, Threads: 1, In: 1e6, Out: 1, Passes: passes, Best: time.Second}
	}
	lines := func() (out []string) {
		for _, l := range m.run.live {
			out = append(out, l.line)
		}
		return out
	}

	m.onEvent(bench.Event{Kind: "pass", Result: res("s2", 1), Target: 3})
	m.onEvent(bench.Event{Kind: "pass", Result: res("zskp", 1), Target: 3})
	m.onEvent(bench.Event{Kind: "pass", Result: res("s2", 2), Target: 3})
	if l := lines(); len(l) != 2 || !strings.Contains(l[1], "s2") || !strings.Contains(l[1], "n=2/3 · 1 more ≈ 1s") {
		t.Errorf("a second pass did not replace the first one's row:\n%s", strings.Join(l, "\n"))
	}

	m.onEvent(bench.Event{Kind: "result", Result: res("s2", 3)})
	if l := lines(); len(l) != 1 || !strings.Contains(l[0], "zskp") {
		t.Errorf("a finished measurement is still in progress:\n%s", strings.Join(l, "\n"))
	}

	m.onEvent(bench.Event{Kind: "corpus", Corpus: "next", Done: 2, Total: 2})
	if l := lines(); len(l) != 0 {
		t.Errorf("rows carried over to the next corpus:\n%s", strings.Join(l, "\n"))
	}
}

// The footer prices the selection before it runs. The database is read once,
// not per keypress, and again once a run has merged into it.
func TestFooterEstimatesTheSelection(t *testing.T) {
	m := newTestModel(t)
	if got := m.footer(); !strings.Contains(got, "no history to estimate from") {
		t.Errorf("an empty database is not called out: %q", got)
	}

	e, _ := codec.LookupEnc("s2")
	r := &db.Run{ID: "x", Corpus: "stacktrace", Kind: "file", Bench: "1", Env: sysinfo.Env{Go: "go1.27.1"}}
	lib := r.LibIndex("example.com/mod", "v1.0.0")
	r.Encode = []db.EncRow{{Codec: "s2", Level: e.Levels[0].N, Threads: 1, Lib: lib, In: 1e6, Out: 1, NS: 1e9, Passes: 3}}
	if _, err := db.Merge(m.opts.Out, r, false); err != nil {
		t.Fatal(err)
	}
	if got := m.footer(); !strings.Contains(got, "no history to estimate from") {
		t.Errorf("the footer re-read the database on a repaint: %q", got)
	}

	m.finishRun(runDoneMsg{st: &runState{}})
	m.scr = scrOptions
	if got := m.footer(); !strings.Contains(got, " to run, ") || !strings.Contains(got, "without history") {
		t.Errorf("the footer did not price the selection after a run: %q", got)
	}
	m.scr = scrRun
	if got := m.footer(); strings.Contains(got, "to run") || strings.Contains(got, "history") {
		t.Errorf("the run screen footer prices the selection while it measures: %q", got)
	}
}

// Fetch progress arrives before there is an Engine, so it fills Corpus itself.
func TestFetchProgressNamesTheCorpusOnce(t *testing.T) {
	ch := make(chan bench.Event, 4)
	progressTo(ch)(corpus.Progress{Corpus: "gofiles", Stage: "extract", Done: 1 << 20, Total: 4 << 20})
	ev := <-ch
	if ev.Corpus != "gofiles" {
		t.Errorf("Corpus = %q, want gofiles", ev.Corpus)
	}
	if strings.Contains(ev.Label, "gofiles") {
		t.Errorf("label repeats the corpus the heading already shows: %q", ev.Label)
	}
}
