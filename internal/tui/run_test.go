package tui

import (
	"strings"
	"testing"
	"time"
)

// The Done screen used to render time.Since(started), so a finished run's
// summary kept counting up for as long as it stayed on screen -- claiming a
// benchmark took longer than it did, and by more the longer you looked at it.
func TestFinishedRunStopsTheClock(t *testing.T) {
	st := &runState{started: time.Now().Add(-90 * time.Second), results: 14}
	m := &Model{run: st}
	m.finishRun(runDoneMsg{st: st})

	if !st.finished {
		t.Fatal("the run was not marked finished")
	}
	if got := st.since().Round(time.Second); got != 90*time.Second {
		t.Fatalf("elapsed = %s, want the 90s the run actually took", got)
	}

	first := m.doneView()
	time.Sleep(30 * time.Millisecond)
	if st.since() != st.elapsed {
		t.Errorf("the clock is still running: %s", st.since())
	}
	if second := m.doneView(); second != first {
		t.Errorf("the summary changed while nothing ran:\n%s\n%s", first, second)
	}
	if !strings.Contains(first, "14 measurements in 1m30s") {
		t.Errorf("summary = %q", first)
	}
}

// A run that was superseded still has to stop its own clock: nothing displays
// it, but leaving it "running" is a trap for whatever reads it next.
func TestSupersededRunStopsItsOwnClock(t *testing.T) {
	old := &runState{started: time.Now().Add(-10 * time.Second)}
	cur := &runState{started: time.Now()}
	m := &Model{run: cur}
	m.finishRun(runDoneMsg{st: old})

	if !old.finished || old.elapsed == 0 {
		t.Errorf("superseded run left running: finished=%v elapsed=%s", old.finished, old.elapsed)
	}
	if cur.finished {
		t.Error("the current run was marked finished by another run's message")
	}
	if m.scr == scrDone {
		t.Error("a superseded run moved the UI to the Done screen")
	}
}
