package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/klauspost/gocomp/internal/preset"
)

func keys(t *testing.T, m *Model, ss ...string) {
	t.Helper()
	for _, s := range ss {
		var msg tea.KeyMsg
		switch s {
		case "enter", "esc", "backspace", "up", "down":
			msg = tea.KeyMsg{Type: map[string]tea.KeyType{
				"enter": tea.KeyEnter, "esc": tea.KeyEsc, "backspace": tea.KeyBackspace,
				"up": tea.KeyUp, "down": tea.KeyDown,
			}[s]}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
		}
		m.Update(msg)
	}
}

// The whole point of presets is that a selection survives a restart, so the
// test goes through the keyboard and then through a fresh model.
func TestPaneSavesAndLoadsThroughTheKeyboard(t *testing.T) {
	m := newTestModel(t)
	m.corpora[0].sel = true
	m.cfg.Threads = []int{2, 4}
	m.cfg.Validate = true

	keys(t, m, "p")
	if m.pane == nil {
		t.Fatal("p did not open the preset pane")
	}
	keys(t, m, "s", "n", "i", "g", "h", "t", "enter")
	if m.pane == nil || m.pane.mode != paneBrowse {
		t.Fatalf("save did not return to the browser: %+v", m.pane)
	}
	if len(m.pane.items) != 1 || m.pane.items[0].Name != "night" {
		t.Fatalf("the saved preset is not in the list: %+v", m.pane.items)
	}
	keys(t, m, "esc")
	if m.pane != nil {
		t.Fatal("esc did not close the pane")
	}

	// A fresh model starts from the defaults, then loads the preset.
	m2 := newTestModel(t)
	m2.opts.Presets = m.opts.Presets
	m2.cfg.Threads = []int{1}
	m2.cfg.Validate = false
	keys(t, m2, "p", "enter")
	if m2.pane != nil {
		t.Fatal("enter should load and close")
	}
	if got := m2.cfg.Threads; len(got) != 2 || got[0] != 2 || got[1] != 4 {
		t.Errorf("threads = %v, want the preset's 2,4", got)
	}
	if !m2.cfg.Validate {
		t.Error("validate did not come back from the preset")
	}
	if !m2.corpora[0].sel {
		t.Error("the corpus selection did not come back from the preset")
	}
}

func TestPaneDeleteAsksFirst(t *testing.T) {
	m := newTestModel(t)
	if err := (preset.Preset{Name: "doomed", Encode: true}).Save(m.opts.Presets); err != nil {
		t.Fatal(err)
	}
	keys(t, m, "p", "d")
	if m.pane.mode != paneConfirm {
		t.Fatal("d should ask before deleting")
	}
	keys(t, m, "n")
	if m.pane.mode != paneBrowse || len(m.pane.items) != 1 {
		t.Fatal("any key other than y must cancel the delete")
	}
	keys(t, m, "d", "y")
	if len(m.pane.items) != 0 {
		t.Fatalf("y did not delete: %+v", m.pane.items)
	}
}

// While the pane is open it owns every key, or typing a name would also drive
// the screen behind it.
func TestPaneIsModal(t *testing.T) {
	m := newTestModel(t)
	m.scr = scrCodecs
	before := m.eCur
	keys(t, m, "p", "s", "j", "k", "esc", "esc")
	if m.eCur != before {
		t.Errorf("keys leaked to the codec screen: cursor moved %d -> %d", before, m.eCur)
	}
	if !strings.Contains(m.View(), "Codecs") {
		t.Error("the tab bar should still be visible behind the pane")
	}
}

// Level 1 is the fastest setting in every codec that has one; the named default
// is the other end worth looking at. Both are selected when nothing is stored.
func TestDefaultSelectionIsFastestAndDefault(t *testing.T) {
	m, err := newModel(Options{Out: t.TempDir(), Cache: t.TempDir(), Presets: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]int{
		"zskp": {1, 2}, // 1:fastest, 2:default
		// klauspost/compress/flate resolves DefaultCompression to 5, and 5 is
		// what carries the name now: -1 was an alias for it, not a level, and
		// selecting both measured the same setting twice.
		"gzkp":   {1, 5},
		"gzstd":  {1, 6}, // compress/flate resolves it to 6 instead
		"s2":     {1},    // 1 is both
		"snappy": {0},    // no level 1, only 0:default
		"minlz":  {1},    // 1:fastest, nothing named default
	}
	for _, it := range m.encs {
		w, ok := want[it.e.ID]
		if !ok {
			continue
		}
		var got []int
		for j, on := range it.sel {
			if on {
				got = append(got, it.e.Levels[j].N)
			}
		}
		if len(got) != len(w) {
			t.Errorf("%s: selected %v, want %v", it.e.ID, got, w)
			continue
		}
		for i := range w {
			if got[i] != w[i] {
				t.Errorf("%s: selected %v, want %v", it.e.ID, got, w)
				break
			}
		}
	}
	for _, it := range m.encs {
		if it.e.Slow {
			continue
		}
		if !it.any() {
			t.Errorf("%s has nothing selected by default", it.e.ID)
		}
	}
}
