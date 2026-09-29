package tui

import (
	"strings"
	"testing"

	"github.com/klauspost/compress-site/internal/bench"
	"github.com/klauspost/compress-site/internal/corpus"
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
