package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/klauspost/gocomp/internal/bench"
	"github.com/klauspost/gocomp/internal/corpus"
	"github.com/klauspost/gocomp/internal/db"
	"github.com/klauspost/gocomp/internal/preset"
	"github.com/klauspost/gocomp/internal/sysinfo"
)

type tickMsg time.Time

// tickCmd redraws at 4fps while running. Anything faster steals CPU from the
// measurement it is reporting on.
func tickCmd() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// runDoneMsg names the runState it belongs to, so a message from a cancelled
// run that finished late cannot be mistaken for the current one. It also hands
// the cache back: the worker owns it for the duration of a run.
type runDoneMsg struct {
	st      *runState
	cache   *corpus.Cache
	summary string
	err     error
}

type runState struct {
	cancel   context.CancelFunc
	pause    *bench.Pause // nil for a download, which has no pass to stop between
	events   chan bench.Event
	doneCh   chan runDoneMsg
	started  time.Time
	elapsed  time.Duration
	corpus   string
	corpusAt int // position of corpus in the run, from 1
	corpusOf int
	label    string
	phase    string
	done     int
	total    int
	live     []liveRow
	log      []string
	results  int
	warnings int
	finished bool
	summary  string
	err      error
	eta      *bench.ETA    // nil for a download
	etaAt    time.Duration // run time when the estimate last changed; -1 while a corpus loads
	pass     bench.Key     // the measurement whose pass is running; zero while nothing timed is
	passAt   time.Duration // run time that pass started
}

// since is how long the run has taken: counting while it runs, frozen once it
// is over. The Done screen called time.Since directly, so the summary of a
// finished run went on climbing -- every repaint, whatever caused it, reported
// a longer benchmark than the one that actually ran. Time spent paused is not
// benchmark time either.
func (r *runState) since() time.Duration {
	if r.finished {
		return r.elapsed
	}
	_, held := r.pause.Held()
	return time.Since(r.started) - held
}

// job is everything the worker needs, snapshotted by value. The worker reads
// no Model field and writes none, so the Model stays owned by the bubbletea
// goroutine alone -- the only thing crossing the boundary is a channel.
type job struct {
	st       *runState
	out      string
	cfg      bench.Config
	env      sysinfo.Env
	libs     map[string]string
	cache    *corpus.Cache
	corpora  []corpus.Corpus
	variants map[corpus.Kind][]bench.Variant
}

// newJob transfers ownership of the cache to the worker; m.cache is nil until
// the run reports back.
func (m *Model) newJob(st *runState, cps []corpus.Corpus) job {
	j := job{
		st: st, out: m.opts.Out, cfg: m.cfg, env: m.env, libs: m.libs,
		cache: m.cache, corpora: cps, variants: m.variantsByKind(),
	}
	m.cache = nil
	return j
}

func (m *Model) startRun() tea.Cmd {
	cps, enc, dec, _ := m.selection()
	if len(cps) == 0 {
		m.note = "select at least one corpus first"
		return nil
	}
	if enc+dec == 0 {
		m.note = "select at least one codec level"
		return nil
	}
	if m.cache == nil {
		// A run still owns it. Only reachable by hammering r, but newJob would
		// hand the worker a nil cache and the fetch would panic.
		m.note = "a run is still finishing"
		return nil
	}
	m.save()
	m.scr = scrRun
	st := &runState{
		pause:   &bench.Pause{},
		events:  make(chan bench.Event, 1024),
		doneCh:  make(chan runDoneMsg, 1),
		started: time.Now(),
		total:   enc + dec,
		phase:   "starting",
		etaAt:   -1,
	}
	m.run = st
	ctx := m.newCtx()
	j := m.newJob(st, cps)
	// Read afresh, and before the worker starts merging into the same database.
	m.hist = bench.LoadHistory(j.out)
	st.eta = bench.NewETA(j.cfg, m.hist, j.corpora, j.variants)
	go execute(ctx, j)
	return tea.Batch(waitFor(st.events), waitDone(st.doneCh), tickCmd())
}

// waitFor re-issues itself on every receive, the standard bubbletea pattern for
// consuming a channel.
func waitFor(ch <-chan bench.Event) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

func waitDone(ch <-chan runDoneMsg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

func (m *Model) onEvent(ev bench.Event) tea.Cmd {
	r := m.run
	if r == nil {
		return nil
	}
	// Every kind carries it, so the heading cannot lag a corpus behind the
	// measurements underneath it.
	if ev.Corpus != "" {
		r.corpus = ev.Corpus
	}
	if r.eta != nil {
		r.eta.Observe(ev)
	}
	switch ev.Kind {
	case "corpus":
		// Done and Total count corpora here, so they must not reach the
		// measurement progress below.
		r.corpusAt, r.corpusOf = ev.Done, ev.Total
		// Loading is not in the estimate, so the countdown waits for the engine.
		r.etaAt = -1
		r.live = nil // nothing of the last corpus is still running
		return waitFor(r.events)
	case "phase":
		r.phase = ev.Phase
	case "job":
		r.label = ev.Label
		if ev.Msg != "" {
			r.label += "  " + ev.Msg
		}
		r.pass, r.passAt = ev.Key, r.since()
	case "pass":
		// A group's results only land when the whole group is done, which on a
		// large corpus is minutes of an empty screen. These are the same lines,
		// provisional.
		if ev.Result != nil {
			l := formatResult(*ev.Result, ev.Target)
			if n := ev.Target - ev.Result.Passes; n > 0 {
				l += fmt.Sprintf(" · %d more ≈ %s", n, approx(time.Duration(n)*ev.Result.Best))
			}
			r.live = setLive(r.live, ev.Result.Key(), l)
		}
	case "result":
		r.results++
		if ev.Result != nil {
			r.live = setLive(r.live, ev.Result.Key(), "")
			r.log = appendLog(r.log, formatResult(*ev.Result, 0))
		}
	case "warn":
		r.warnings++
		if ev.Result != nil {
			r.live = setLive(r.live, ev.Result.Key(), "")
		}
		r.log = appendLog(r.log, errStyle.Render("! "+ev.Msg))
	}
	// Fetch and load report as jobs too, and so do passes about to start, none
	// of which moves the estimate.
	if ev.Kind != "job" {
		r.etaAt = r.since()
	}
	if ev.Total > 0 {
		r.done, r.total = ev.Done, ev.Total
	}
	return waitFor(r.events)
}

// liveRows is how many in-progress measurements stay on screen. Round-robin
// cycles through the whole group, so a handful is enough to keep it moving.
const liveRows = 5

// liveRow is a measurement still running, as of its latest pass.
type liveRow struct {
	key  bench.Key
	line string
}

// setLive keeps one row per measurement, the latest pass last. An empty line
// drops it: a row left behind once its measurement finished read as one still
// running.
func setLive(rows []liveRow, k bench.Key, line string) []liveRow {
	rows = slices.DeleteFunc(rows, func(r liveRow) bool { return r.key == k })
	if line == "" {
		return rows
	}
	return ring(rows, liveRow{k, line}, liveRows)
}

func appendLog(log []string, line string) []string { return ring(log, line, 200) }

func ring[T any](s []T, v T, n int) []T {
	s = append(s, v)
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return s
}

// formatResult shows the passes out of target, for a measurement still running.
func formatResult(r bench.Result, target int) string {
	if r.Err != "" {
		return errStyle.Render(fmt.Sprintf("! %s %s: %s", r.Op, r.Codec, r.Err))
	}
	src := r.Src
	if src == "" {
		src = "-"
	}
	win := ""
	if r.Window > 0 {
		win = " w" + bench.WindowName(r.Window)
	}
	n := strconv.Itoa(r.Passes)
	if target > 0 {
		n += "/" + strconv.Itoa(target)
	}
	return fmt.Sprintf("%s %-9s %3d %-12s t%-3d %8.2f MB/s  %.3f:1  n=%s%s",
		r.Op, r.Codec, r.Level, src, r.Threads, r.MBps(), r.Ratio(), n, win)
}

// left renders the estimate for the current corpus and for the whole run.
// Between events it counts down by the run time since the last one, so a pass
// that takes minutes does not freeze it.
func (r *runState) left() (cur, total string) {
	if r.eta == nil {
		return "", ""
	}
	c, all, unknown := r.eta.Left()
	if r.etaAt >= 0 {
		gone := r.since() - r.etaAt
		c, all = max(c-gone, 0), max(all-gone, 0)
	}
	total = estimate(all, unknown, "left")
	if unknown > 0 && all == 0 {
		return "", total
	}
	return ", ~" + approx(c) + " left", total
}

// passLeft counts down the pass running now. One that outruns its estimate
// shows how long it has taken instead, rather than sitting at zero. Passes
// under a second would only flicker.
func (r *runState) passLeft() string {
	if r.eta == nil {
		return ""
	}
	d, ok := r.eta.Pass(r.pass)
	if !ok || d < time.Second {
		return ""
	}
	gone := r.since() - r.passAt
	if gone > d {
		return fmt.Sprintf(" · %s, expected ~%s", approx(gone), approx(d))
	}
	return " · ~" + approx(d-gone) + " left"
}

// estimate renders a total that leaves out the measurements nothing is known
// about, which makes it a floor while there are any.
func estimate(d time.Duration, unknown int, what string) string {
	switch {
	case unknown > 0 && d == 0:
		return " · no history to estimate from"
	case unknown > 0:
		return fmt.Sprintf(" · ≥%s %s, %d without history", approx(d), what, unknown)
	}
	return " · ~" + approx(d) + " " + what
}

// approx is a duration to the precision an estimate deserves.
func approx(d time.Duration) string {
	switch {
	case d >= time.Hour:
		d = d.Round(time.Minute)
	case d >= 10*time.Second:
		d = d.Round(time.Second)
	default:
		d = d.Round(100 * time.Millisecond)
	}
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	return s
}

// execute runs the benchmark and merges each corpus as it completes, so a
// cancelled run still leaves the finished corpora in the database. It is a
// free function on purpose: everything it touches came in through job.
func execute(ctx context.Context, j job) {
	var b strings.Builder
	var firstErr error
	prog := progressTo(j.st.events)

	// A corpus none of whose codecs take any requested window is skipped before
	// counting, so the heading's 2/5 counts what will actually run -- and so no
	// corpus is loaded to measure nothing, which is minutes on a large one.
	var todo []corpus.Corpus
	for _, cp := range j.corpora {
		if len(j.cfg.Expand(j.variants[cp.Kind], 0)) > 0 {
			todo = append(todo, cp)
		}
	}
	for i, cp := range todo {
		if j.st.pause.Wait(ctx) != nil {
			break
		}
		// Not the usual non-blocking send: this is the heading for the whole
		// corpus, hours on a large one, and a dropped event leaves it wrong.
		select {
		case j.st.events <- bench.Event{Kind: "corpus", Corpus: cp.ID, Done: i + 1, Total: len(todo)}:
		case <-ctx.Done():
		}
		variants := j.variants[cp.Kind]
		if err := corpus.Fetch(ctx, j.cache, cp, prog); err != nil {
			firstErr = err
			break
		}
		data, err := corpus.Load(ctx, j.cache, cp, prog)
		if err != nil {
			firstErr = err
			break
		}
		_ = j.cache.Save()

		// Windows past the largest input are trimmed here, the first point at
		// which that size is known.
		eng := &bench.Engine{Cfg: j.cfg, Data: data, Variants: j.cfg.Expand(variants, data.Max), Events: j.st.events, Pause: j.st.pause}
		results, runErr := eng.Run(ctx)
		// meta is the last use of data, so the corpus is unreachable from here
		// on and this GC hands its memory back before the next one is loaded.
		meta := bench.MetaOf(data)
		runtime.GC()

		if len(results) > 0 && j.out != "" {
			run := bench.ToRun(meta, results, j.cfg, j.env, j.libs)
			res, err := db.Merge(j.out, run, false)
			if err != nil {
				firstErr = err
				break
			}
			fmt.Fprintf(&b, "%s: %d measurements (%d new, %d improved, %d kept)\n",
				cp.ID, len(results), res.Added, res.Improved, res.Kept)
			for _, w := range res.Warnings {
				fmt.Fprintf(&b, "  warning: %s\n", w)
			}
		}
		if runErr != nil && ctx.Err() == nil {
			firstErr = runErr
			break
		}
	}
	j.st.doneCh <- runDoneMsg{st: j.st, cache: j.cache, summary: b.String(), err: firstErr}
}

// fetchSelected downloads every selected corpus that is not already present.
func (m *Model) fetchSelected() tea.Cmd {
	var want []corpus.Corpus
	for _, it := range m.corpora {
		if it.sel && it.state == "missing" {
			want = append(want, it.c)
		}
	}
	if len(want) == 0 {
		m.note = "nothing to download: every selected corpus is already present"
		return nil
	}
	m.save()
	m.scr = scrRun
	st := &runState{
		events: make(chan bench.Event, 256), doneCh: make(chan runDoneMsg, 1),
		started: time.Now(), phase: "download",
	}
	m.run = st
	ctx := m.newCtx()
	j := m.newJob(st, want)
	go download(ctx, j)
	return tea.Batch(waitFor(st.events), waitDone(st.doneCh), tickCmd())
}

func download(ctx context.Context, j job) {
	var b strings.Builder
	var firstErr error
	prog := progressTo(j.st.events)
	for _, c := range j.corpora {
		if err := corpus.Fetch(ctx, j.cache, c, prog); err != nil {
			firstErr = err
			break
		}
		fmt.Fprintf(&b, "%s: ready\n", c.ID)
	}
	_ = j.cache.Save()
	j.st.doneCh <- runDoneMsg{st: j.st, cache: j.cache, summary: b.String(), err: firstErr}
}

// progressTo bypasses Engine.emit -- fetching happens before there is an
// Engine -- so it fills Corpus itself, and drops it from the label the view
// now prefixes with it anyway.
func progressTo(events chan<- bench.Event) corpus.ProgressFn {
	return func(p corpus.Progress) {
		msg := fmt.Sprintf("%s %s", p.Stage, human(p.Done))
		if p.Total > 0 {
			msg += " / " + human(p.Total)
		}
		select {
		case events <- bench.Event{Kind: "job", Corpus: p.Corpus, Label: msg}:
		default:
		}
	}
}

func (m *Model) runKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c", "q":
		if m.run != nil && m.run.cancel != nil {
			m.run.cancel()
		}
		if m.run != nil && m.run.finished {
			return tea.Quit
		}
		if m.run != nil {
			// Cancelling releases the hold; left set, the badge would claim a
			// pause is still pending while the run winds down.
			m.run.pause.Set(false)
		}
		m.note = "cancelling; finished corpora are still written"
	case "p", " ":
		if m.run != nil && !m.run.finished {
			m.run.pause.Set(!m.run.pause.Paused())
		}
	}
	return nil
}

func (m *Model) runHelp() string {
	switch {
	case m.run == nil || m.run.pause == nil:
		return "ctrl+c cancel"
	case m.run.pause.Paused():
		return "p resume · ctrl+c cancel"
	}
	return "p pause · ctrl+c cancel"
}

// pauseBadge only blinks once the worker has actually stopped. Until then a
// pass is still running -- minutes, on a large corpus -- and "PAUSED" over a
// pinned CPU would be a lie.
func (r *runState) pauseBadge() string {
	if !r.pause.Paused() {
		return ""
	}
	if held, _ := r.pause.Held(); !held {
		return "  " + pauseStyle.Render(" pausing after this pass… ")
	}
	st := pauseStyle
	if time.Now().UnixMilli()/500%2 == 1 {
		st = pauseOff // same width, so the line does not jump
	}
	return "  " + st.Render(" ❚❚ PAUSED ")
}

func (m *Model) finishRun(msg runDoneMsg) {
	// Always take the cache back, even from a superseded run: the worker is
	// done with it either way and the UI would otherwise be left holding nil.
	if msg.cache != nil {
		m.cache = msg.cache
		m.refreshCorpusStates()
	}
	// Stop the clock before the guard. A superseded run is never shown, but
	// leaving it marked as still running means anything that later reads it
	// gets a duration that grows.
	msg.st.elapsed = msg.st.since()
	msg.st.finished = true
	m.hist = nil // the run merged into it, superseded or not
	if m.run == nil || msg.st != m.run {
		return // a cancelled run that finished late
	}
	m.run.summary = msg.summary
	m.run.err = msg.err
	m.scr = scrDone
}

// refreshCorpusStates re-reads what the cache now holds, so a corpus that was
// "missing" before a download does not still say so afterwards.
func (m *Model) refreshCorpusStates() {
	for i := range m.corpora {
		it := &m.corpora[i]
		switch {
		case it.c.Synth != nil:
			it.state = "generated"
		default:
			it.state = "missing"
			if _, adopted, ok := m.cache.Resolve(it.c); ok {
				it.state = "cached"
				if adopted {
					it.state = "adopted"
				}
			} else if it.c.Remote == "" {
				it.state = "manual"
			}
		}
		if e, ok := m.cache.Entries[it.c.ID]; ok && e.Size > 0 {
			it.size, it.files = e.Size, e.Files
		}
	}
}

func (m *Model) runView() string {
	r := m.run
	if r == nil {
		return ""
	}
	pct := 0.0
	if r.total > 0 {
		pct = float64(r.done) / float64(r.total)
	}
	width := 40
	filled := int(pct * float64(width))
	bar := "[" + strings.Repeat("=", filled) + strings.Repeat(" ", width-filled) + "]"

	var b strings.Builder
	cur, total := r.left()
	fmt.Fprintf(&b, "  %s %3.0f%%  %d/%d  %s elapsed%s%s\n", bar, pct*100, r.done, r.total,
		r.since().Round(time.Second), total, r.pauseBadge())
	head := r.phase
	if r.corpus != "" {
		c := r.corpus
		if r.corpusOf > 1 {
			c += fmt.Sprintf(" (%d/%d%s)", r.corpusAt, r.corpusOf, cur)
		}
		head = c + " · " + r.phase
	}
	fmt.Fprintf(&b, "  %s: %s%s\n", head, r.label, r.passLeft())

	used := 3
	if len(r.live) > 0 {
		b.WriteString("\n  " + dimStyle.Render("in progress, best so far") + "\n")
		for _, l := range r.live {
			b.WriteString("    " + dimStyle.Render(l.line) + "\n")
		}
		used += len(r.live) + 2
	}
	if len(r.log) > 0 {
		b.WriteString("\n  " + dimStyle.Render("completed") + "\n")
		used += 2
		tail := r.log
		if n := max(m.bodyHeight()-used, 0); len(tail) > n {
			tail = tail[len(tail)-n:]
		}
		for _, l := range tail {
			b.WriteString("    " + l + "\n")
		}
	}
	return b.String()
}

func (m *Model) doneView() string {
	r := m.run
	var b strings.Builder
	if r == nil {
		return "  nothing ran\n"
	}
	if r.err != nil {
		b.WriteString(errStyle.Render("  "+r.err.Error()) + "\n\n")
	}
	fmt.Fprintf(&b, "  %d measurements in %s", r.results, r.since().Round(time.Second))
	if r.warnings > 0 {
		fmt.Fprintf(&b, ", %d warnings", r.warnings)
	}
	b.WriteString("\n\n")
	for _, l := range strings.Split(strings.TrimRight(r.summary, "\n"), "\n") {
		if l != "" {
			b.WriteString("  " + l + "\n")
		}
	}
	fmt.Fprintf(&b, "\n  database: %s\n", m.opts.Out)
	fmt.Fprintf(&b, "  view it with: gocomp serve\n")
	return b.String()
}

// ---------------------------------------------------------------- persistence

// The between-sessions autosave is a preset in every respect except that it
// lives in the cache rather than the repo, so there is one format to
// understand and "gocomp run -preset <cache>/tui-state.json" replays
// the last interactive selection exactly.
func (m *Model) statePath() string { return filepath.Join(m.cacheDir, "tui-state.json") }

func (m *Model) save() {
	b, err := json.MarshalIndent(m.currentPreset(), "", "  ")
	if err != nil {
		return
	}
	// Best effort: losing the remembered selection is not worth an error screen.
	_ = os.MkdirAll(m.cacheDir, 0o755)
	_ = os.WriteFile(m.statePath(), append(b, '\n'), 0o644)
}

func (m *Model) restore() {
	b, err := os.ReadFile(m.statePath())
	if err != nil {
		return
	}
	var s preset.Preset
	if json.Unmarshal(b, &s) != nil {
		return
	}
	m.applySaved(s)
}
