package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/klauspost/compress-site/internal/bench"
)

// newTestModel builds a model over a temp cache holding a small stacktrace.txt,
// which the corpus resolver adopts in place, so the test needs no network and
// no files from outside the repo.
func newTestModel(t *testing.T) *Model {
	t.Helper()
	dir := t.TempDir()
	body := make([]byte, 0, 256<<10)
	for len(body) < 256<<10 {
		body = append(body, "goroutine 1 [running]:\nmain.main()\n\t/src/main.go:13 +0x54\n"...)
	}
	if err := os.WriteFile(filepath.Join(dir, "stacktrace.txt"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := newModel(Options{
		Out: filepath.Join(dir, "data"), Cache: dir, Presets: filepath.Join(dir, "presets"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range m.corpora {
		m.corpora[i].sel = m.corpora[i].c.ID == "stacktrace"
	}
	for i := range m.encs {
		for j := range m.encs[i].sel {
			m.encs[i].sel[j] = false
		}
		switch m.encs[i].e.ID {
		case "s2", "snappy", "zskp", "gzkp", "minlz", "lz4":
			for j := range m.encs[i].sel {
				m.encs[i].sel[j] = true
			}
		}
	}
	m.cfg.Threads = []int{1}
	m.cfg.SrcThreads = 1
	m.cfg.Passes = 2
	m.cfg.MaxPasses = 4
	m.cfg.MinWall = 10 * time.Millisecond
	m.w, m.h = 120, 40
	return m
}

// TestRunIsRaceFree drives a real benchmark through the model exactly as
// bubbletea would, with -race catching any Model field the worker still
// touches. The worker takes a snapshot by value and owns the cache for the
// duration, so the UI goroutine should be the only one reading the Model.
func TestRunIsRaceFree(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a real benchmark")
	}
	m := newTestModel(t)
	cmd := m.startRun()
	if cmd == nil {
		t.Fatal("startRun returned no command; the selection was rejected")
	}
	if m.cache != nil {
		t.Error("the model still holds the cache while a worker is using it")
	}

	// Pump commands the way bubbletea does: every Cmd runs on its own
	// goroutine and its message comes back to Update on this one.
	msgs := make(chan tea.Msg, 256)
	var pump func(tea.Cmd)
	pump = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			// tea.Batch yields a BatchMsg holding the real commands; the
			// runtime expands it, so the harness has to as well.
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, sub := range batch {
					pump(sub)
				}
				return
			}
			if msg != nil {
				msgs <- msg
			}
		}()
	}
	pump(cmd)

	deadline := time.After(90 * time.Second)
	sawLive := false
	for {
		select {
		case msg := <-msgs:
			_, next := m.Update(msg)
			view := m.View() // the UI reads the model while the worker runs
			if m.run != nil && len(m.run.live) > 0 && strings.Contains(view, "in progress") {
				sawLive = true
			}
			pump(next)
			if m.run != nil && m.run.finished {
				if !sawLive {
					t.Error("no live measurements were shown while the run was in flight")
				}
				if m.cache == nil {
					t.Error("the cache was not handed back when the run finished")
				}
				if m.run.err != nil {
					t.Fatalf("run failed: %v", m.run.err)
				}
				if m.run.results == 0 {
					t.Error("no results were reported")
				}
				return
			}
		case <-deadline:
			t.Fatal("run did not finish in time")
		}
	}
}

// TestLateDoneFromCancelledRunIsIgnored covers the ordering that used to panic:
// a cancelled run reports back after the UI has already started another.
func TestLateDoneFromCancelledRunIsIgnored(t *testing.T) {
	m := newTestModel(t)
	old := &runState{events: make(chan bench.Event, 1), doneCh: make(chan runDoneMsg, 1)}
	current := &runState{events: make(chan bench.Event, 1), doneCh: make(chan runDoneMsg, 1)}
	m.run = current
	m.scr = scrRun

	m.finishRun(runDoneMsg{st: old, summary: "stale", err: os.ErrClosed})

	if current.finished {
		t.Error("a superseded run marked the current one finished")
	}
	if m.scr != scrRun {
		t.Error("a superseded run changed the screen")
	}
	if m.run.summary == "stale" {
		t.Error("a superseded run overwrote the current summary")
	}

	// And the model must survive the run being cleared entirely.
	m.run = nil
	m.finishRun(runDoneMsg{st: old, summary: "stale"})
}

func TestSaveRestoreRoundTrips(t *testing.T) {
	m := newTestModel(t)
	m.cfg.Threads = []int{1, 4}
	m.cfg.Validate = true
	m.save()

	again, err := newModel(Options{Out: m.opts.Out, Cache: m.cacheDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.cfg.Threads) != 2 || again.cfg.Threads[1] != 4 {
		t.Errorf("threads did not survive: %v", again.cfg.Threads)
	}
	if !again.cfg.Validate {
		t.Error("validate did not survive")
	}
	var selected int
	for _, it := range again.corpora {
		if it.sel {
			selected++
		}
	}
	if selected != 1 {
		t.Errorf("corpus selection did not survive: %d selected", selected)
	}
}
