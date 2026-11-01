package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/klauspost/gocomp/internal/bench"
	"github.com/klauspost/gocomp/internal/corpus"
	"github.com/pbnjay/memory"
)

var (
	tabStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	tabActive  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("62"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))
	cursorLine = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	pauseStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("214"))
	pauseOff   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
)

func totalMem() uint64 { return memory.TotalMemory() }

func human(n int64) string {
	const unit = 1000
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}

func check(b bool) string {
	if b {
		return "[x]"
	}
	return "[ ]"
}

// rows renders a scrolling window around the cursor.
func window(lines []string, cur, height int) []string {
	if height <= 0 || len(lines) <= height {
		return lines
	}
	off := cur - height/2
	off = max(0, min(off, len(lines)-height))
	return lines[off : off+height]
}

func (m *Model) bodyHeight() int {
	h := m.h - 6
	if h < 5 {
		h = 5
	}
	return h
}

// ---------------------------------------------------------------- corpus

func (m *Model) corpusKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		m.cCur = max(0, m.cCur-1)
	case "down", "j":
		m.cCur = min(len(m.corpora)-1, m.cCur+1)
	case "home", "g":
		m.cCur = 0
	case "end", "G":
		m.cCur = len(m.corpora) - 1
	case " ":
		m.corpora[m.cCur].sel = !m.corpora[m.cCur].sel
	case "t":
		tier := m.corpora[m.cCur].c.Tier()
		on := !m.corpora[m.cCur].sel
		for i := range m.corpora {
			if m.corpora[i].c.Tier() == tier {
				m.corpora[i].sel = on
			}
		}
	case "a":
		on := false
		for _, it := range m.corpora {
			if !it.sel {
				on = true
				break
			}
		}
		for i := range m.corpora {
			m.corpora[i].sel = on
		}
	case "n":
		for i := range m.corpora {
			m.corpora[i].sel = false
		}
	case "d":
		return m.fetchSelected()
	}
	return nil
}

func (m *Model) corpusView() string {
	var lines []string
	tier := ""
	for i, it := range m.corpora {
		if t := it.c.Tier().String(); t != tier {
			tier = t
			lines = append(lines, dimStyle.Render("  "+strings.ToUpper(tier)))
		}
		state := it.state
		if state == "missing" {
			state = errStyle.Render(state)
		} else {
			state = okStyle.Render(state)
		}
		files := ""
		if it.c.Kind == corpus.KindDir {
			files = fmt.Sprintf(" %d files", it.files)
		}
		row := fmt.Sprintf("%s %-20s %-5s %9s%s  %s  %s",
			check(it.sel), it.c.ID, it.c.Kind, human(it.size), files, state, dimStyle.Render(it.c.Desc))
		if i == m.cCur {
			row = cursorLine.Render("> " + row)
		} else {
			row = "  " + row
		}
		lines = append(lines, row)
	}
	return strings.Join(window(lines, m.cCur, m.bodyHeight()), "\n")
}

// ---------------------------------------------------------------- codecs

// codecRow identifies a rendered line: a codec, or one of its levels.
type codecRow struct {
	enc   int
	level int // -1 for the codec row itself
}

func (m *Model) codecRows() []codecRow {
	var rows []codecRow
	for i, it := range m.encs {
		if it.e.Slow && !m.slow {
			continue
		}
		rows = append(rows, codecRow{enc: i, level: -1})
		if it.expanded {
			for j := range it.e.Levels {
				rows = append(rows, codecRow{enc: i, level: j})
			}
		}
	}
	return rows
}

type pick int

const (
	pickFast pick = iota
	pickKlauspost
	pickDefault
	pickAll
)

// applyPick seeds a selection. The default is the two interesting ends of each
// codec's range, which is the sane starting point for an exploratory run.
func (m *Model) applyPick(p pick) {
	for i := range m.encs {
		it := &m.encs[i]
		for j := range it.sel {
			it.sel[j] = false
		}
		if it.e.Slow && !m.slow {
			continue
		}
		switch p {
		case pickAll:
			for j, l := range it.e.Levels {
				it.sel[j] = !l.Slow || m.slow
			}
		case pickKlauspost:
			if !strings.Contains(it.e.Impl, "klauspost") && !strings.Contains(it.e.Impl, "minlz") {
				continue
			}
			for j, l := range it.e.Levels {
				it.sel[j] = !l.Slow || m.slow
			}
		case pickFast:
			if it.e.Concurrent(corpus.KindFile) {
				m.selectDefault(it)
			}
		default:
			m.selectDefault(it)
		}
	}
}

// selectDefault picks level 1 -- the fastest setting in every codec that has
// one -- and the level the library calls its default. They are usually two
// different levels and together they bracket the interesting range; where they
// coincide (s2) or only one exists (snappy), one is selected.
func (m *Model) selectDefault(it *encItem) {
	any := false
	for j, l := range it.e.Levels {
		if l.N == 1 || l.Name == "default" {
			it.sel[j], any = true, true
		}
	}
	if any {
		return
	}
	// No level 1 and nothing named default: take the first that is not slow.
	for j, l := range it.e.Levels {
		if !l.Slow {
			it.sel[j] = true
			return
		}
	}
	it.sel[0] = true
}

func (m *Model) codecKey(msg tea.KeyMsg) tea.Cmd {
	rows := m.codecRows()
	if len(rows) == 0 {
		return nil
	}
	m.eCur = min(m.eCur, len(rows)-1)
	cur := rows[m.eCur]
	it := &m.encs[cur.enc]

	switch msg.String() {
	case "up", "k":
		m.eCur = max(0, m.eCur-1)
	case "down", "j":
		m.eCur = min(len(rows)-1, m.eCur+1)
	case "enter":
		if cur.level < 0 {
			it.expanded = !it.expanded
		} else {
			it.sel[cur.level] = !it.sel[cur.level]
		}
	case " ":
		if cur.level >= 0 {
			it.sel[cur.level] = !it.sel[cur.level]
			break
		}
		sel, total := it.reach(m.slow)
		on := sel < total
		for j, l := range it.e.Levels {
			// Clearing clears everything, including levels hidden behind the
			// slow filter. A selection made while slow was shown would
			// otherwise be stuck on, marked [~] with nothing on screen to
			// untick.
			it.sel[j] = on && (!l.Slow || m.slow)
		}
	case "S":
		m.slow = !m.slow
		m.eCur = 0
	case "1":
		m.applyPick(pickFast)
	case "2":
		m.applyPick(pickKlauspost)
	case "3":
		m.applyPick(pickDefault)
	case "4":
		m.applyPick(pickAll)
	}
	return nil
}

func (m *Model) codecView() string {
	rows := m.codecRows()
	var lines []string
	for i, r := range rows {
		it := m.encs[r.enc]
		var text string
		if r.level < 0 {
			// [x] means "everything you can see is on", so a codec whose slow
			// levels are hidden still reads as full rather than as a partial
			// selection the reader cannot complete.
			sel, total := it.reach(m.slow)
			mark := "[ ]"
			switch {
			case total > 0 && sel == total:
				mark = "[x]"
			case it.any():
				mark = "[~]"
			}
			arrow := "+"
			if it.expanded {
				arrow = "-"
			}
			conc := ""
			if it.e.Conc != 0 {
				conc = dimStyle.Render(" concurrent:" + it.e.Conc.String())
			}
			text = fmt.Sprintf("%s %s %-8s %-22s %-14s%s",
				mark, arrow, it.e.ID, it.e.Impl, it.e.Format, conc)
		} else {
			l := it.e.Levels[r.level]
			name := l.Name
			if l.Slow {
				name += " (slow)"
			}
			text = fmt.Sprintf("      %s %3d %s", check(it.sel[r.level]), l.N, name)
		}
		if i == m.eCur {
			text = cursorLine.Render("> " + text)
		} else {
			text = "  " + text
		}
		lines = append(lines, text)
	}
	decs := "its own implementation and every other selected codec of its format"
	if m.cfg.AllDecoders {
		decs = "every implementation of its format"
	}
	head := dimStyle.Render(fmt.Sprintf("  decoders follow the selection: each encoder is decoded by %s%s",
		decs, map[bool]string{true: " · slow shown", false: ""}[m.slow]))
	return head + "\n" + strings.Join(window(lines, m.eCur, m.bodyHeight()-1), "\n")
}

// ---------------------------------------------------------------- options

type field struct {
	label  string
	kind   string // text, bool, ops
	get    func() string
	set    func(string)
	toggle func()
	buf    string
}

func ints(v []int) string {
	var s []string
	for _, n := range v {
		s = append(s, strconv.Itoa(n))
	}
	return strings.Join(s, ",")
}

func (m *Model) buildFields() {
	m.fields = []*field{
		{
			label: "threads (-cpu)", kind: "text",
			get: func() string { return ints(m.cfg.Threads) },
			set: func(s string) {
				if v := parseInts(s); len(v) > 0 {
					m.cfg.Threads = v
				}
			},
		},
		{
			label: "window sizes (-window)", kind: "text",
			get: func() string { return bench.WindowSpec(m.cfg.Windows) },
			set: func(s string) {
				if w, err := bench.ParseWindows(s); err == nil {
					m.cfg.Windows = w
				}
			},
		},
		{
			label: "decode source threads", kind: "text",
			get: func() string { return strconv.Itoa(m.cfg.SrcThreads) },
			set: func(s string) {
				if n, err := strconv.Atoi(s); err == nil && n > 0 {
					m.cfg.SrcThreads = n
				}
			},
		},
		{
			label: "minimum passes", kind: "text",
			get: func() string { return strconv.Itoa(m.cfg.Passes) },
			set: func(s string) {
				if n, err := strconv.Atoi(s); err == nil && n > 0 {
					m.cfg.Passes = n
				}
			},
		},
		{
			label: "operations", kind: "ops",
			get: func() string {
				switch {
				case m.cfg.Encode && m.cfg.Decode:
					return "encode + decode"
				case m.cfg.Encode:
					return "encode only"
				default:
					return "decode only"
				}
			},
			toggle: func() {
				switch {
				case m.cfg.Encode && m.cfg.Decode:
					m.cfg.Decode = false
				case m.cfg.Encode:
					m.cfg.Encode, m.cfg.Decode = false, true
				default:
					m.cfg.Encode = true
				}
			},
		},
		{
			label: "validate round trip (xxh3)", kind: "bool",
			get:    func() string { return check(m.cfg.Validate) },
			toggle: func() { m.cfg.Validate = !m.cfg.Validate },
		},
		{
			label: "collapse non-concurrent codecs to one thread count", kind: "bool",
			get:    func() string { return check(m.cfg.Collapse) },
			toggle: func() { m.cfg.Collapse = !m.cfg.Collapse },
		},
		{
			label: "decode with every decoder of the format", kind: "bool",
			get:    func() string { return check(m.cfg.AllDecoders) },
			toggle: func() { m.cfg.AllDecoders = !m.cfg.AllDecoders },
		},
	}
}

func parseInts(s string) []int {
	var out []int
	for _, p := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

func (m *Model) optionsKey(msg tea.KeyMsg) tea.Cmd {
	f := m.fields[m.fCur]
	switch msg.String() {
	case "up", "k":
		m.fCur = max(0, m.fCur-1)
	case "down", "j":
		m.fCur = min(len(m.fields)-1, m.fCur+1)
	case " ":
		if f.toggle != nil {
			f.toggle()
		}
	case "enter":
		if f.kind == "text" {
			m.editing, f.buf = true, f.get()
		} else if f.toggle != nil {
			f.toggle()
		}
	case "R", "r":
		return m.startRun()
	}
	return nil
}

func (m *Model) editKey(msg tea.KeyMsg) {
	f := m.fields[m.fCur]
	switch msg.String() {
	case "enter":
		f.set(f.buf)
		m.editing = false
	case "esc":
		m.editing = false
	case "backspace":
		if len(f.buf) > 0 {
			f.buf = f.buf[:len(f.buf)-1]
		}
	default:
		if s := msg.String(); len(s) == 1 {
			f.buf += s
		}
	}
}

func (m *Model) optionsView() string {
	var b strings.Builder
	for i, f := range m.fields {
		val := f.get()
		if m.editing && i == m.fCur {
			val = f.buf + "_"
		}
		row := fmt.Sprintf("%-50s %s", f.label, val)
		if i == m.fCur {
			b.WriteString(cursorLine.Render("> " + row))
		} else {
			b.WriteString("  " + row)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  database: " + m.opts.Out))
	b.WriteString("\n\n")
	b.WriteString("  press R to run\n")
	return b.String()
}
