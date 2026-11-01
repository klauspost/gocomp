// Package tui is the interactive front end for selecting and running benchmarks.
//
// It is for exploration. Numbers you intend to publish should come from
// "compress-site run": a terminal redrawing during a measurement is noise the
// batch path does not have.
package tui

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/klauspost/compress-site/internal/bench"
	"github.com/klauspost/compress-site/internal/codec"
	"github.com/klauspost/compress-site/internal/corpus"
	"github.com/klauspost/compress-site/internal/preset"
	"github.com/klauspost/compress-site/internal/sysinfo"
)

// Options configures where the TUI reads and writes.
type Options struct {
	Out     string
	Cache   string
	Presets string
}

type screen int

const (
	scrCorpus screen = iota
	scrCodecs
	scrOptions
	scrRun
	scrDone
)

var screenNames = [...]string{"Corpus", "Codecs", "Options", "Run", "Done"}

type corpusItem struct {
	c     corpus.Corpus
	sel   bool
	state string
	size  int64
	files int
}

type encItem struct {
	e        codec.Enc
	expanded bool
	sel      []bool // one per level
}

func (it encItem) any() bool {
	for _, s := range it.sel {
		if s {
			return true
		}
	}
	return false
}

// reach counts the levels the current view can actually select, and how many
// of those are on. Everything that decides what the family toggle does has to
// go through it: judging "is this codec fully selected?" over levels the
// toggle cannot touch is what made space unable to clear a codec with slow
// levels while slow was hidden -- all() stayed false, so every press selected
// again and none ever cleared.
func (it encItem) reach(slow bool) (on, total int) {
	for j, l := range it.e.Levels {
		if l.Slow && !slow {
			continue
		}
		total++
		if it.sel[j] {
			on++
		}
	}
	return on, total
}

// Model is the whole application state; screens are a field, not nested programs.
type Model struct {
	opts Options
	// cache is handed to the worker for the duration of a run and is nil until
	// it comes back, so only one goroutine ever touches it. cacheDir is the
	// immutable part the UI still needs meanwhile.
	cache    *corpus.Cache
	cacheDir string
	env      sysinfo.Env
	libs     map[string]string
	scr      screen
	w, h     int
	err      error
	note     string

	corpora []corpusItem
	cCur    int

	encs []encItem
	eCur int
	slow bool

	cfg     bench.Config
	fields  []*field
	fCur    int
	editing bool

	pane *pane   // the preset browser, nil unless open
	dbp  *dbPane // the database cleanup, nil unless open
	run  *runState
	// hist prices the estimates. nil until read, and again whenever the
	// database changes under it.
	hist bench.History
}

// Run starts the interactive program.
func Run(o Options) error {
	m, err := newModel(o)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func newModel(o Options) (*Model, error) {
	cache, err := corpus.OpenCache(corpus.CacheDir(o.Cache))
	if err != nil {
		return nil, err
	}
	o.Presets = preset.Dir(o.Presets)
	m := &Model{
		opts: o, cache: cache, cacheDir: cache.Dir, cfg: bench.DefaultConfig(runtime.NumCPU()),
		env: sysinfo.Detect(), libs: sysinfo.Libs(codec.Modules()),
	}
	// No GOAMD64 note here: m.note replaces the key-hint line, so a startup
	// advisory would hide the shortcuts for the whole session. The level is
	// recorded on the run and shown by "list env".
	for _, c := range corpus.All() {
		it := corpusItem{c: c, state: "missing", size: c.Bytes(), files: c.Files}
		if _, adopted, ok := cache.Resolve(c); ok {
			it.state = "cached"
			if adopted {
				it.state = "adopted"
			}
		} else if c.Synth != nil {
			it.state = "generated"
		} else if c.Remote == "" {
			it.state = "manual"
		}
		if e, ok := cache.Entries[c.ID]; ok && e.Size > 0 {
			it.size, it.files = e.Size, e.Files
		}
		m.corpora = append(m.corpora, it)
	}
	for _, e := range codec.Encoders() {
		m.encs = append(m.encs, encItem{e: e, sel: make([]bool, len(e.Levels))})
	}
	m.applyPick(pickDefault)
	m.buildFields()
	m.restore()
	return m, nil
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		return m.key(msg)
	case bench.Event:
		return m, m.onEvent(msg)
	case runDoneMsg:
		m.finishRun(msg)
		return m, nil
	case tickMsg:
		if m.scr == scrRun {
			return m, tickCmd()
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.scr == scrRun {
		return m, m.runKey(msg)
	}
	if m.editing {
		m.editKey(msg)
		return m, nil
	}
	// The pane is modal: while it is open it owns every key, so typing a
	// preset name cannot also toggle a codec behind it.
	if m.pane != nil {
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, m.paneKey(msg)
	}
	if m.dbp != nil {
		if s := msg.String(); s == "ctrl+c" || s == "q" {
			m.save()
			return m, tea.Quit
		}
		return m, m.dbKey(msg)
	}
	switch msg.String() {
	case "ctrl+c", "q":
		m.save()
		return m, tea.Quit
	case "p":
		if m.scr != scrRun {
			m.openPane()
			return m, nil
		}
	case "D":
		m.openDB()
		return m, nil
	case "tab", "right", "l":
		if m.scr < scrOptions {
			m.scr++
		}
		return m, nil
	case "shift+tab", "left", "h":
		if m.scr > scrCorpus && m.scr < scrRun {
			m.scr--
		}
		return m, nil
	}
	switch m.scr {
	case scrCorpus:
		return m, m.corpusKey(msg)
	case scrCodecs:
		return m, m.codecKey(msg)
	case scrOptions:
		return m, m.optionsKey(msg)
	case scrDone:
		switch msg.String() {
		case "r", "R":
			// Straight back into the same selection. It used to drop you on
			// the Corpus screen, which is "edit", not "run again".
			return m, m.startRun()
		case "e", "tab":
			m.scr = scrCorpus
			m.run = nil
		}
	}
	return m, nil
}

func (m *Model) View() string {
	if m.err != nil {
		return errStyle.Render("error: "+m.err.Error()) + "\n\npress q to quit\n"
	}
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n")
	if m.pane != nil || m.dbp != nil {
		if m.pane != nil {
			b.WriteString(m.paneView())
		} else {
			b.WriteString(m.dbView())
		}
		b.WriteString("\n")
		b.WriteString(m.footer())
		return b.String()
	}
	switch m.scr {
	case scrCorpus:
		b.WriteString(m.corpusView())
	case scrCodecs:
		b.WriteString(m.codecView())
	case scrOptions:
		b.WriteString(m.optionsView())
	case scrRun:
		b.WriteString(m.runView())
	case scrDone:
		b.WriteString(m.doneView())
	}
	b.WriteString("\n")
	b.WriteString(m.footer())
	return b.String()
}

func (m *Model) header() string {
	var tabs []string
	for i, n := range screenNames {
		s := tabStyle
		if screen(i) == m.scr {
			s = tabActive
		}
		tabs = append(tabs, s.Render(fmt.Sprintf(" %d %s ", i+1, n)))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
}

// selected corpora and variants, shared by the footer and the run screen.
func (m *Model) selection() ([]corpus.Corpus, int, int, int64) {
	var out []corpus.Corpus
	var peak int64
	for _, it := range m.corpora {
		if it.sel {
			out = append(out, it.c)
			peak = max(peak, it.c.Need())
		}
	}
	var enc, dec int
	for _, cp := range out {
		// An upper bound until the corpus is loaded and oversize windows go.
		e, d := bench.Plan(m.cfg, cp.Kind, m.cfg.Expand(m.variants(cp.Kind), 0))
		enc, dec = enc+e, dec+d
	}
	return out, enc, dec, peak
}

func (m *Model) variants(kind corpus.Kind) []bench.Variant {
	var out []bench.Variant
	for _, it := range m.encs {
		if it.e.Kinds&kind == 0 {
			continue
		}
		for i, l := range it.e.Levels {
			if it.sel[i] {
				out = append(out, bench.Variant{Enc: it.e, Level: l})
			}
		}
	}
	return out
}

func (m *Model) variantsByKind() map[corpus.Kind][]bench.Variant {
	v := map[corpus.Kind][]bench.Variant{}
	for _, k := range []corpus.Kind{corpus.KindFile, corpus.KindDir} {
		v[k] = m.variants(k)
	}
	return v
}

// history reads the database once and keeps it until something changes it:
// the footer prices the selection on every keypress.
func (m *Model) history() bench.History {
	if m.hist == nil {
		m.hist = bench.LoadHistory(m.opts.Out)
	}
	return m.hist
}

func (m *Model) footer() string {
	cps, enc, dec, peak := m.selection()
	line := fmt.Sprintf("%d corpora · %d encode + %d decode measurements", len(cps), enc, dec)
	// The run screen redraws while it measures, and has an estimate of its own.
	if m.scr != scrRun && enc+dec > 0 {
		_, total, unknown := bench.NewETA(m.cfg, m.history(), cps, m.variantsByKind()).Left()
		line += estimate(total, unknown, "to run")
	}
	line += fmt.Sprintf(" · peak ~%s of %s", human(peak), human(int64(totalMem())))
	st := dimStyle
	if peak > int64(totalMem())/5*3 {
		st = errStyle
		line += " · over budget"
	}
	help := map[screen]string{
		scrCorpus:  "space select · t tier · a all · d download · p presets · D database · tab next",
		scrCodecs:  "space codec · enter levels · 1-4 quick picks · S slow · p presets · D database · tab next",
		scrOptions: "up/down field · enter edit · space toggle · p presets · D database · R run",
		scrRun:     m.runHelp(),
		scrDone:    "r run the same selection again · e edit it · p presets · D database · q quit",
	}[m.scr]
	switch {
	case m.pane != nil:
		help = m.paneHelp()
	case m.dbp != nil:
		help = m.dbHelp()
	case m.note != "":
		help = m.note
	}
	return st.Render(line) + "\n" + dimStyle.Render(help)
}

// ctxWithCancel is stored on the model so ctrl+c can stop a running benchmark.
func (m *Model) newCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	m.run.cancel = cancel
	return ctx
}
