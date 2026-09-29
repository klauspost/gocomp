package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/klauspost/compress-site/internal/db"
)

// dbPane deletes from the database: whole runs, or the rows of the ticked runs
// that a codec, a source encoder or a library version names. It is modal like
// the preset browser, and like it cannot open over the Run screen, so it never
// rewrites a file a run is merging into.
type dbPane struct {
	stubs   []db.RunStub
	runs    []*db.Run  // parallel to stubs, only for previews: deletes re-read from disk
	tallies []db.Tally // parallel to stubs
	tick    []bool
	multi   bool // more than one machine, so rows name theirs
	labels  map[string]string

	codecs, libs    []string
	isSrc           map[string]bool
	codec, src, lib map[string]bool // what d deletes

	cur  int
	ask  string
	do   func() (string, error) // non-nil while ask waits for y
	note string
}

type dbItem struct {
	sec int // 0 runs, 1 codecs, 2 libraries
	i   int
}

func (m *Model) openDB() {
	p, err := loadDB(m.opts.Out)
	if err != nil {
		p = &dbPane{note: err.Error()}
	}
	m.dbp = p
}

// loadDB reads the whole database. It is a few hundred KB, and the counts
// beside every choice need all of it.
func loadDB(dir string) (*dbPane, error) {
	p := &dbPane{
		isSrc: map[string]bool{}, labels: map[string]string{},
		codec: map[string]bool{}, src: map[string]bool{}, lib: map[string]bool{},
	}
	ix, err := db.LoadIndex(dir)
	if err != nil {
		return nil, err
	}
	p.multi = len(ix.Machines) > 1
	for id, mi := range ix.Machines {
		p.labels[id] = mi.Label
	}
	slices.SortStableFunc(ix.Runs, func(a, b db.RunStub) int { return strings.Compare(b.Updated, a.Updated) })
	codecs, libs := map[string]bool{}, map[string]bool{}
	for _, s := range ix.Runs {
		r, err := db.LoadRun(dir, s.Corpus, s.ID)
		if err != nil {
			return nil, err
		}
		if r == nil {
			r = &db.Run{Corpus: s.Corpus, ID: s.ID}
		}
		t := r.Tally()
		for k := range t.Codecs {
			codecs[k] = true
		}
		for k := range t.Sources {
			codecs[k], p.isSrc[k] = true, true
		}
		for k := range t.Libs {
			libs[k] = true
		}
		p.stubs, p.runs, p.tallies = append(p.stubs, s), append(p.runs, r), append(p.tallies, t)
	}
	p.tick = make([]bool, len(p.stubs))
	p.codecs, p.libs = slices.Sorted(maps.Keys(codecs)), slices.Sorted(maps.Keys(libs))
	if len(p.stubs) == 0 {
		p.note = "no runs in " + dir
	}
	return p, nil
}

func (p *dbPane) items() []dbItem {
	var out []dbItem
	for sec, n := range []int{len(p.stubs), len(p.codecs), len(p.libs)} {
		for i := range n {
			out = append(out, dbItem{sec, i})
		}
	}
	return out
}

func (p *dbPane) ticked() []db.RunStub {
	var out []db.RunStub
	for i, on := range p.tick {
		if on {
			out = append(out, p.stubs[i])
		}
	}
	return out
}

func (p *dbPane) filter() db.Filter {
	keys := func(m map[string]bool) []string {
		var out []string
		for k, on := range m {
			if on {
				out = append(out, k)
			}
		}
		return out
	}
	return db.Filter{Codecs: keys(p.codec), Sources: keys(p.src), Libs: keys(p.lib)}
}

func (m *Model) dbKey(msg tea.KeyMsg) tea.Cmd {
	p := m.dbp
	if p.do != nil {
		do := p.do
		p.ask, p.do = "", nil
		if msg.String() != "y" {
			return nil
		}
		// Ticks do not survive the reload: rows shift underneath them, and a
		// leftover tick would aim the next delete somewhere else.
		note, err := do()
		if err == nil {
			var q *dbPane
			if q, err = loadDB(m.opts.Out); err == nil {
				q.cur = min(p.cur, max(0, len(q.items())-1))
				m.dbp, p = q, q
			}
		}
		if err != nil {
			note = err.Error()
		}
		p.note = note
		return nil
	}
	items := p.items()
	if len(items) == 0 {
		if s := msg.String(); s == "esc" || s == "D" {
			m.dbp = nil
		}
		return nil
	}
	it := items[p.cur]
	p.note = ""
	switch msg.String() {
	case "esc", "D":
		m.dbp = nil
	case "up", "k":
		p.cur = max(0, p.cur-1)
	case "down", "j":
		p.cur = min(len(items)-1, p.cur+1)
	case "home", "g":
		p.cur = 0
	case "end", "G":
		p.cur = len(items) - 1
	case " ":
		switch it.sec {
		case 0:
			p.tick[it.i] = !p.tick[it.i]
		case 1:
			p.codec[p.codecs[it.i]] = !p.codec[p.codecs[it.i]]
		case 2:
			p.lib[p.libs[it.i]] = !p.lib[p.libs[it.i]]
		}
	case "s":
		if it.sec == 1 && p.isSrc[p.codecs[it.i]] {
			p.src[p.codecs[it.i]] = !p.src[p.codecs[it.i]]
		}
	case "a":
		on := slices.Contains(p.tick, false)
		for i := range p.tick {
			p.tick[i] = on
		}
	case "d":
		m.askPrune()
	case "X":
		m.askRemove()
	}
	return nil
}

func (m *Model) askPrune() {
	p, f, runs := m.dbp, m.dbp.filter(), m.dbp.ticked()
	switch {
	case len(runs) == 0:
		p.note = "tick the runs to delete from first (a ticks all)"
		return
	case len(f.Codecs)+len(f.Sources)+len(f.Libs) == 0:
		p.note = "tick a codec, source or library to delete"
		return
	}
	var enc, dec, hit, emptied int
	for i, on := range p.tick {
		if !on {
			continue
		}
		q, e, d := p.runs[i].Pruned(f)
		if e+d == 0 {
			continue
		}
		enc, dec, hit = enc+e, dec+d, hit+1
		if len(q.Encode)+len(q.Decode) == 0 {
			emptied++
		}
	}
	if hit == 0 {
		p.note = "nothing in the ticked runs matches"
		return
	}
	p.ask = fmt.Sprintf("delete %d encode + %d decode rows from %s", enc, dec, plural(hit, "run"))
	if emptied > 0 {
		p.ask += fmt.Sprintf(", removing the %s left empty", plural(emptied, "run"))
	}
	p.do = func() (string, error) {
		res, err := db.Prune(m.opts.Out, runs, f)
		return fmt.Sprintf("deleted %d encode + %d decode rows, removed %s",
			res.Encode, res.Decode, plural(res.Removed, "run")), err
	}
}

func (m *Model) askRemove() {
	p, runs := m.dbp, m.dbp.ticked()
	if len(runs) == 0 {
		p.note = "tick the runs to delete first"
		return
	}
	rows := 0
	for _, s := range runs {
		rows += s.NEncode + s.NDecode
	}
	what := plural(len(runs), "run")
	if len(runs) == 1 {
		what = "run " + runs[0].Corpus + " " + runs[0].ID
	}
	p.ask = fmt.Sprintf("delete %s and its %d rows", what, rows)
	p.do = func() (string, error) {
		return "removed " + plural(len(runs), "run"), db.Remove(m.opts.Out, runs)
	}
}

func plural(n int, one string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %ss", n, one)
}

func (m *Model) dbView() string {
	p := m.dbp
	sum := db.Tally{Codecs: map[string]int{}, Sources: map[string]int{}, Libs: map[string]int{}}
	// Runs, not rows: which runs hold it is what picks the runs to tick, and
	// the rows a delete takes are in its confirmation.
	add := func(dst, src map[string]int) {
		for k := range src {
			dst[k]++
		}
	}
	picked := len(p.ticked())
	scope := "all " + plural(len(p.stubs), "run")
	if picked > 0 {
		scope = "the " + plural(picked, "ticked run")
	}
	for i, t := range p.tallies {
		if picked == 0 || p.tick[i] {
			add(sum.Codecs, t.Codecs)
			add(sum.Sources, t.Sources)
			add(sum.Libs, t.Libs)
		}
	}

	var b strings.Builder
	b.WriteString("  " + cursorLine.Render("database") + dimStyle.Render("  "+m.opts.Out+" · runs: how many of "+scope+" hold it") + "\n\n")
	heads := [...]string{"RUNS", fmt.Sprintf("%-17s%4s  as source", "CODECS", "runs"), fmt.Sprintf("%-73s%4s", "LIBRARIES", "runs")}
	var lines []string
	at, sec := 0, -1
	for n, it := range p.items() {
		if it.sec != sec {
			sec = it.sec
			lines = append(lines, dimStyle.Render("  "+heads[sec]))
		}
		var row string
		switch it.sec {
		case 0:
			s := p.stubs[it.i]
			row = fmt.Sprintf("%s %-18s %-28s %-9s %-3s t%-8s %4d enc %4d dec  %.10s",
				check(p.tick[it.i]), s.Corpus, s.ID, s.Go, s.GOAMD64, ints(s.Threads), s.NEncode, s.NDecode, s.Updated)
			if p.multi {
				row += dimStyle.Render("  " + p.labels[s.Machine])
			}
		case 1:
			id := p.codecs[it.i]
			src := "    "
			if p.isSrc[id] {
				src = fmt.Sprintf("%s %5d", check(p.src[id]), sum.Sources[id])
			}
			row = fmt.Sprintf("%s %-10s %6d  %s", check(p.codec[id]), id, sum.Codecs[id], src)
		case 2:
			k := p.libs[it.i]
			row = fmt.Sprintf("%s %-66s %6d", check(p.lib[k]), k, sum.Libs[k])
		}
		if n == p.cur {
			at, row = len(lines), cursorLine.Render("> "+row)
		} else {
			row = "  " + row
		}
		lines = append(lines, row)
	}
	h := max(m.bodyHeight()-5, 1)
	off := 0
	if len(lines) > h {
		off = max(0, min(at-h/2, len(lines)-h))
	}
	for _, l := range lines[off:min(off+h, len(lines))] {
		b.WriteString(l + "\n")
	}
	if p.ask != "" {
		b.WriteString("\n  " + errStyle.Render(p.ask+"? y/n") + "\n")
	}
	if p.note != "" {
		b.WriteString("\n  " + dimStyle.Render(p.note) + "\n")
	}
	return b.String()
}

func (m *Model) dbHelp() string {
	if m.dbp.do != nil {
		return "y delete · any other key cancel"
	}
	return "space tick · s as source · a all runs · d delete rows · X delete runs · esc back"
}
