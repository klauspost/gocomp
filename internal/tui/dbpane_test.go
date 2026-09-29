package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/klauspost/compress-site/internal/db"
	"github.com/klauspost/compress-site/internal/sysinfo"
)

// seedDB stores runs alpha and beta, each with a zskp encode row and a
// stdzstd decode of it. beta is newer, so items are: 0 beta, 1 alpha,
// 2 stdzstd, 3 zskp, 4 the library.
func seedDB(t *testing.T, dir string) {
	t.Helper()
	for _, c := range []string{"alpha", "beta"} {
		r := &db.Run{
			ID: "x", Corpus: c, Kind: "file", Bench: "1",
			Env: sysinfo.Env{Go: "go1.27.1", CPU: sysinfo.CPU{Brand: "Test CPU"}},
		}
		i := r.LibIndex("example.com/mod", "v1.0.0")
		r.Encode = []db.EncRow{{Codec: "zskp", Level: 1, Threads: 1, Lib: i, In: 10, Out: 5, NS: 1, Passes: 1}}
		r.Decode = []db.DecRow{{Codec: "stdzstd", Src: "zskp-1", Lib: i, SrcLib: i, Threads: 1, In: 5, Out: 10, NS: 1, Passes: 1}}
		if _, err := db.Merge(dir, r, false); err != nil {
			t.Fatal(err)
		}
	}
	// Both merges usually land in the same second; pin the order.
	ix, err := db.LoadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range ix.Runs {
		ix.Runs[i].Updated = map[string]string{"alpha": "2026-01-01T00:00:00Z", "beta": "2026-02-01T00:00:00Z"}[s.Corpus]
	}
	if err := ix.Save(dir); err != nil {
		t.Fatal(err)
	}
}

func rows(t *testing.T, dir, corpus string) (enc, dec int) {
	t.Helper()
	r, err := db.LoadRun(dir, corpus, "x")
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		return -1, -1
	}
	return len(r.Encode), len(r.Decode)
}

func TestDBPaneDeletesTickedRowsOnlyAfterYes(t *testing.T) {
	m := newTestModel(t)
	seedDB(t, m.opts.Out)
	keys(t, m, "D")
	if m.dbp == nil || len(m.dbp.stubs) != 2 {
		t.Fatalf("D did not open the database pane over both runs: %+v", m.dbp)
	}
	if m.dbp.stubs[0].Corpus != "beta" {
		t.Errorf("runs are not newest first: %s is on top", m.dbp.stubs[0].Corpus)
	}
	keys(t, m, "d")
	if m.dbp.do != nil {
		t.Fatal("d with nothing ticked must not offer a delete")
	}
	// Tick beta, then the stdzstd codec.
	keys(t, m, " ", "down", "down", " ", "d")
	if m.dbp.do == nil || !strings.Contains(m.dbp.ask, "0 encode + 1 decode rows from 1 run") {
		t.Fatalf("expected a preview of one decode row, got %q", m.dbp.ask)
	}
	keys(t, m, "n")
	if e, d := rows(t, m.opts.Out, "beta"); e != 1 || d != 1 {
		t.Fatalf("any key but y must cancel; beta has %d/%d rows", e, d)
	}
	keys(t, m, "d", "y")
	if e, d := rows(t, m.opts.Out, "beta"); e != 1 || d != 0 {
		t.Errorf("beta has %d/%d rows, want the stdzstd decode gone", e, d)
	}
	if e, d := rows(t, m.opts.Out, "alpha"); e != 1 || d != 1 {
		t.Errorf("alpha was not ticked but has %d/%d rows", e, d)
	}
	if m.dbp.tick[0] || m.dbp.codec["stdzstd"] {
		t.Error("ticks survived the delete")
	}
}

// Ticking zskp as a source takes the decode rows of its output, not its own.
func TestDBPaneSourceTick(t *testing.T) {
	m := newTestModel(t)
	seedDB(t, m.opts.Out)
	keys(t, m, "D", "a", "down", "down", "down", "s", "d", "y")
	for _, c := range []string{"alpha", "beta"} {
		if e, d := rows(t, m.opts.Out, c); e != 1 || d != 0 {
			t.Errorf("%s has %d/%d rows, want only zskp's own encode row left", c, e, d)
		}
	}
}

func TestDBPaneRemovesRuns(t *testing.T) {
	m := newTestModel(t)
	seedDB(t, m.opts.Out)
	keys(t, m, "D", "down", " ", "X")
	if !strings.Contains(m.dbp.ask, "run alpha x and its 2 rows") {
		t.Fatalf("expected the run to be named, got %q", m.dbp.ask)
	}
	keys(t, m, "y")
	if e, _ := rows(t, m.opts.Out, "alpha"); e != -1 {
		t.Error("alpha's file is still there")
	}
	if len(m.dbp.stubs) != 1 || m.dbp.stubs[0].Corpus != "beta" {
		t.Errorf("the pane did not reload: %+v", m.dbp.stubs)
	}
	keys(t, m, "esc")
	if m.dbp != nil {
		t.Error("esc did not close the pane")
	}
}

func TestDBPaneIsModal(t *testing.T) {
	m := newTestModel(t)
	seedDB(t, m.opts.Out)
	sel := m.corpora[0].sel
	keys(t, m, "D", " ", "a", "t")
	if v := m.View(); !strings.Contains(v, "Corpus") || !strings.Contains(v, "alpha") {
		t.Error("the pane should show over the tab bar")
	}
	keys(t, m, "esc")
	if m.corpora[0].sel != sel {
		t.Error("keys leaked to the corpus screen")
	}
}

// The counts are runs holding each codec and library: of every run while none
// is ticked, then of the ticked ones.
func TestDBPaneCounts(t *testing.T) {
	m := newTestModel(t)
	seedDB(t, m.opts.Out)
	keys(t, m, "D")
	count := func(name string) string {
		for _, l := range strings.Split(m.dbView(), "\n") {
			if f := strings.Fields(l); len(f) > 3 && f[2] == name {
				return f[3]
			}
		}
		return ""
	}
	if c, l := count("stdzstd"), count("example.com/mod@v1.0.0"); c != "2" || l != "2" {
		t.Errorf("nothing ticked: stdzstd %q, library %q; want both runs", c, l)
	}
	keys(t, m, " ")
	if c, l := count("stdzstd"), count("example.com/mod@v1.0.0"); c != "1" || l != "1" {
		t.Errorf("beta ticked: stdzstd %q, library %q; want 1 run", c, l)
	}
}

func TestDBPaneQuits(t *testing.T) {
	m := newTestModel(t)
	keys(t, m, "D")
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd == nil || cmd() != tea.Quit() {
		t.Error("q in the database pane did not quit")
	}
}
