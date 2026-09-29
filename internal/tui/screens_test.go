package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// space on a codec family used to be a one-way switch for anything with a
// slow level: "fully selected" was judged over every level, but the toggle
// only ever sets the non-slow ones, so all() stayed false and every press
// selected again. br has levels 0..11 with 10 and 11 slow.
func TestSpaceClearsACodecWithSlowLevels(t *testing.T) {
	m := newTestModel(t)
	m.slow = false
	for i := range m.encs {
		for j := range m.encs[i].sel {
			m.encs[i].sel[j] = false
		}
	}

	enc := -1
	for i, it := range m.encs {
		if it.e.ID == "br" {
			enc = i
		}
	}
	if enc < 0 {
		t.Skip("br is not registered")
	}
	slow := 0
	for _, l := range m.encs[enc].e.Levels {
		if l.Slow {
			slow++
		}
	}
	if slow == 0 {
		t.Fatalf("br has no slow levels; this test no longer covers the bug")
	}

	for i, r := range m.codecRows() {
		if r.enc == enc && r.level < 0 {
			m.eCur = i
		}
	}
	space := tea.KeyMsg{Type: tea.KeySpace}

	m.codecKey(space)
	on, total := m.encs[enc].reach(false)
	if on != total || total == 0 {
		t.Fatalf("first space selected %d of %d reachable levels", on, total)
	}
	for j, l := range m.encs[enc].e.Levels {
		if l.Slow && m.encs[enc].sel[j] {
			t.Errorf("level %d is slow and slow is hidden, but it was selected", l.N)
		}
	}

	m.codecKey(space)
	if m.encs[enc].any() {
		t.Error("second space did not clear the codec")
	}
}

// A slow level selected while slow was shown must not become unclearable once
// it is hidden again: nothing on screen would untick it.
func TestSpaceClearsSlowLevelsSelectedEarlier(t *testing.T) {
	m := newTestModel(t)
	enc := -1
	for i, it := range m.encs {
		if it.e.ID == "br" {
			enc = i
		}
	}
	if enc < 0 {
		t.Skip("br is not registered")
	}
	m.slow = true
	for j := range m.encs[enc].sel {
		m.encs[enc].sel[j] = true
	}
	m.slow = false

	for i, r := range m.codecRows() {
		if r.enc == enc && r.level < 0 {
			m.eCur = i
		}
	}
	m.codecKey(tea.KeyMsg{Type: tea.KeySpace})
	if m.encs[enc].any() {
		t.Error("space left a hidden slow level selected, with nothing on screen to untick")
	}
}
