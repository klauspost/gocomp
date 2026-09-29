package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/klauspost/compress-site/internal/preset"
)

// pane is the preset browser. It is a modal rather than a tab so the 1-2-3
// selection flow is untouched: p opens it over whichever screen you were on,
// esc puts you back there.
type pane struct {
	items []preset.Preset
	cur   int
	mode  paneMode
	buf   string
	note  string
}

type paneMode int

const (
	paneBrowse paneMode = iota
	paneNaming
	paneConfirm
)

func (m *Model) openPane() {
	p := &pane{}
	items, err := preset.List(m.opts.Presets)
	if err != nil {
		p.note = err.Error()
	}
	p.items = items
	if len(items) == 0 {
		p.note = "no presets in " + m.opts.Presets + " yet; s saves the current selection"
	}
	m.pane = p
}

func (m *Model) paneKey(msg tea.KeyMsg) tea.Cmd {
	p := m.pane
	switch p.mode {
	case paneNaming:
		m.paneNameKey(msg)
		return nil
	case paneConfirm:
		m.paneConfirmKey(msg)
		return nil
	}

	switch msg.String() {
	case "esc", "p":
		m.pane = nil
	case "up", "k":
		p.cur = max(0, p.cur-1)
	case "down", "j":
		p.cur = min(len(p.items)-1, p.cur+1)
	case "enter":
		if len(p.items) == 0 {
			return nil
		}
		m.applySaved(p.items[p.cur])
		m.pane = nil
		m.note = "loaded preset " + p.items[p.cur].Name
	case "s":
		p.mode, p.buf, p.note = paneNaming, m.suggestName(), ""
	case "d":
		if len(p.items) > 0 {
			p.mode = paneConfirm
		}
	case "R":
		if len(p.items) > 0 {
			m.applySaved(p.items[p.cur])
			m.pane = nil
			return m.startRun()
		}
	}
	return nil
}

func (m *Model) paneNameKey(msg tea.KeyMsg) {
	p := m.pane
	switch msg.String() {
	case "esc":
		p.mode, p.note = paneBrowse, ""
	case "enter":
		s := m.currentPreset()
		s.Name = strings.TrimSpace(p.buf)
		if err := s.Save(m.opts.Presets); err != nil {
			p.note = err.Error()
			return
		}
		p.mode, p.note = paneBrowse, "saved "+s.Name
		m.reloadPane(s.Name)
	case "backspace":
		if len(p.buf) > 0 {
			p.buf = p.buf[:len(p.buf)-1]
		}
	default:
		if s := msg.String(); len(s) == 1 {
			p.buf += s
		}
	}
}

func (m *Model) paneConfirmKey(msg tea.KeyMsg) {
	p := m.pane
	name := p.items[p.cur].Name
	switch msg.String() {
	case "y":
		if err := preset.Delete(m.opts.Presets, name); err != nil {
			p.note = err.Error()
		} else {
			p.note = "deleted " + name
		}
		p.mode = paneBrowse
		m.reloadPane("")
	default:
		p.mode, p.note = paneBrowse, ""
	}
}

// reloadPane re-reads the directory so the list reflects what is on disk, and
// keeps the cursor on a named entry when there is one.
func (m *Model) reloadPane(keep string) {
	p := m.pane
	items, err := preset.List(m.opts.Presets)
	if err != nil {
		p.note = err.Error()
		return
	}
	p.items = items
	p.cur = min(p.cur, max(0, len(items)-1))
	for i, it := range items {
		if it.Name == keep {
			p.cur = i
		}
	}
}

// suggestName offers the highlighted preset's name, so saving over one you just
// loaded is two keystrokes and saving a new one is still a free-text field.
func (m *Model) suggestName() string {
	if len(m.pane.items) > 0 {
		return m.pane.items[m.pane.cur].Name
	}
	return ""
}

func (m *Model) paneView() string {
	p := m.pane
	var b strings.Builder
	b.WriteString("  " + cursorLine.Render("presets") + dimStyle.Render("  "+m.opts.Presets) + "\n\n")

	lines := make([]string, 0, len(p.items))
	for _, it := range p.items {
		row := fmt.Sprintf("%-16s %s", it.Name, it.Summary())
		if it.Desc != "" {
			row += dimStyle.Render("  " + it.Desc)
		}
		lines = append(lines, row)
	}
	// Scrolled by hand rather than through window(), which returns lines
	// without the offset needed to know which one the cursor is on.
	h := max(m.bodyHeight()-4, 1)
	off := 0
	if len(lines) > h {
		off = max(0, min(p.cur-h/2, len(lines)-h))
	}
	for i := off; i < min(off+h, len(lines)); i++ {
		if i == p.cur {
			b.WriteString(cursorLine.Render("> "+lines[i]) + "\n")
		} else {
			b.WriteString("  " + lines[i] + "\n")
		}
	}

	switch p.mode {
	case paneNaming:
		b.WriteString("\n  save as: " + p.buf + "_\n")
	case paneConfirm:
		b.WriteString("\n  " + errStyle.Render("delete "+p.items[p.cur].Name+"? y/n") + "\n")
	}
	if p.note != "" {
		b.WriteString("\n  " + dimStyle.Render(p.note) + "\n")
	}
	return b.String()
}

func (m *Model) paneHelp() string {
	switch m.pane.mode {
	case paneNaming:
		return "type a name · enter save · esc cancel"
	case paneConfirm:
		return "y delete · any other key cancel"
	}
	return "enter load · R load and run · s save as… · d delete · esc back"
}

// ------------------------------------------------------------- model <-> file

// currentPreset is the whole selection as it would be saved. It is also what
// the TUI autosaves between sessions, so there is one format, not two.
func (m *Model) currentPreset() preset.Preset {
	p := preset.Preset{
		Codecs: map[string][]int{}, Threads: m.cfg.Threads, SrcThreads: m.cfg.SrcThreads,
		Encode: m.cfg.Encode, Decode: m.cfg.Decode, Passes: m.cfg.Passes,
		MinWall: preset.Dur(m.cfg.MinWall), MaxPasses: m.cfg.MaxPasses,
		MaxJob: preset.Dur(m.cfg.MaxJob), Block: m.cfg.BlockSize,
		Validate: m.cfg.Validate, Collapse: m.cfg.Collapse, Slow: m.slow,
	}
	for _, it := range m.corpora {
		if it.sel {
			p.Corpora = append(p.Corpora, it.c.ID)
		}
	}
	for _, it := range m.encs {
		var lv []int
		for j, on := range it.sel {
			if on {
				lv = append(lv, it.e.Levels[j].N)
			}
		}
		if lv != nil {
			p.Codecs[it.e.ID] = lv
		}
	}
	return p
}

// applySaved is the inverse. Absent fields keep the current value, so a
// hand-written preset naming only corpora is valid.
func (m *Model) applySaved(s preset.Preset) {
	want := map[string]bool{}
	for _, id := range s.Corpora {
		want[id] = true
	}
	for i := range m.corpora {
		m.corpora[i].sel = want[m.corpora[i].c.ID]
	}
	if len(s.Codecs) > 0 {
		for i := range m.encs {
			it := &m.encs[i]
			lv := s.Codecs[it.e.ID]
			for j := range it.sel {
				it.sel[j] = containsInt(lv, it.e.Levels[j].N)
			}
		}
	}
	if len(s.Threads) > 0 {
		m.cfg.Threads = s.Threads
	}
	if s.SrcThreads > 0 {
		m.cfg.SrcThreads = s.SrcThreads
	}
	if s.Passes > 0 {
		m.cfg.Passes = s.Passes
	}
	if s.MinWall > 0 {
		m.cfg.MinWall = time.Duration(s.MinWall)
	}
	if s.MaxPasses > 0 {
		m.cfg.MaxPasses = s.MaxPasses
	}
	if s.MaxJob > 0 {
		m.cfg.MaxJob = time.Duration(s.MaxJob)
	}
	if s.Block > 0 {
		m.cfg.BlockSize = s.Block
	}
	m.cfg.Encode, m.cfg.Decode = s.Encode, s.Decode
	if !m.cfg.Encode && !m.cfg.Decode {
		m.cfg.Encode, m.cfg.Decode = true, true
	}
	m.cfg.Validate, m.cfg.Collapse, m.slow = s.Validate, s.Collapse, s.Slow
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
