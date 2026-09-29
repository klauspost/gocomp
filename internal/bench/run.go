package bench

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/klauspost/compress-site/internal/codec"
	"github.com/klauspost/compress-site/internal/corpus"
)

// Event reports progress. Sends are non-blocking, so a slow consumer can never
// stall a measurement.
// A pass event carries the same Result shape as a result event, but with the
// best and pass count so far. Round-robin means no measurement is final until
// its whole group is, which without this leaves a long run with nothing to show.
type Event struct {
	Kind string // phase, job, pass, result, warn; and corpus, from the TUI, which walks the corpora
	// Corpus is stamped by emit rather than by each call site, so no event can
	// arrive without saying which corpus it is about. An Engine measures one
	// corpus, but a run walks several and they take hours apiece.
	Corpus string
	Phase  string
	Label  string
	Done   int
	Total  int
	Result *Result
	Msg    string
}

// Engine executes a measurement plan against one loaded corpus.
type Engine struct {
	Cfg      Config
	Data     *corpus.Data
	Variants []Variant
	Events   chan<- Event

	results []Result
	done    int
	total   int
	decoded []bool // by variant index: decode already ran off the encode output
}

func (e *Engine) emit(ev Event) {
	if e.Events == nil {
		return
	}
	ev.Done, ev.Total = e.done, e.total
	ev.Corpus = e.Data.ID
	select {
	case e.Events <- ev:
	default:
	}
}

// Run executes the plan. A cancelled context stops cleanly and returns what
// completed, so a partial run is still worth merging.
func (e *Engine) Run(ctx context.Context) ([]Result, error) {
	enc, dec := Plan(e.Cfg, e.Data.Kind, e.Variants)
	e.total = enc + dec
	e.decoded = make([]bool, len(e.Variants))

	prev := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(prev)

	if e.Cfg.Encode {
		if err := e.runEncode(ctx); err != nil {
			return e.results, err
		}
	}
	if e.Cfg.Decode {
		if err := e.runDecode(ctx); err != nil {
			return e.results, err
		}
	}
	return e.results, ctx.Err()
}

// keep adopts a returned buffer only when it is larger, so one codec that
// allocates its own smaller destination cannot force the next one to grow
// inside a timed region.
func keep(dst *[]byte, out []byte) {
	if cap(out) > cap(*dst) {
		*dst = out[:0]
	}
}

type encJob struct {
	v      Variant
	vi     int // index into Engine.Variants
	enc    codec.Encoder
	out    int64
	last   []byte // the latest pass's output
	source bool   // its output is this variant's decode input
}

// sourceGroup is the encode thread count whose output is exactly what
// compressFor would produce for v: the one at SrcThreads, or -- for a codec that
// declares no concurrency, and so encodes the same bytes at any thread count --
// the last group it runs in, when SrcThreads is not among them.
func (e *Engine) sourceGroup(v Variant) (int, bool) {
	conc := v.Enc.Concurrent(e.Data.Kind)
	ts := e.Cfg.ThreadsFor(conc)
	switch {
	case containsInt(ts, e.Cfg.SrcThreads):
		return e.Cfg.SrcThreads, true
	case !conc && len(ts) > 0:
		return ts[len(ts)-1], true
	}
	return 0, false
}

func (e *Engine) runEncode(ctx context.Context) error {
	e.emit(Event{Kind: "phase", Phase: "encode"})
	kind := e.Data.Kind

	for _, t := range e.Cfg.Threads {
		var jobs []*encJob
		for vi, v := range e.Variants {
			if !containsInt(e.Cfg.ThreadsFor(v.Enc.Concurrent(kind)), t) {
				continue
			}
			enc, err := v.Enc.New(v.Level.N, e.opts(t, v.Window))
			if err != nil {
				e.fail(OpEncode, v.Enc.ID, "", v.Level.N, v.Window, t, v.Enc.Module, err)
				e.total-- // Plan counted it; it will never run, so 100% must not require it
				continue
			}
			// A directory's output is overwritten file by file, so the whole
			// compressed corpus never exists during an encode pass.
			g, ok := e.sourceGroup(v)
			src := e.Cfg.Decode && kind == corpus.KindFile && ok && g == t &&
				len(e.Cfg.DecodersOf(v, kind, e.Variants)) > 0
			jobs = append(jobs, &encJob{v: v, vi: vi, enc: enc, source: src})
		}
		if len(jobs) == 0 {
			continue
		}

		runtime.GOMAXPROCS(t)
		bound := 0
		for _, j := range jobs {
			n := e.Data.Max
			if kind == corpus.KindFile {
				n = len(e.Data.File)
			}
			bound = max(bound, j.v.Enc.Bound(n))
		}
		dst := make([]byte, 0, bound)

		st, err := e.roundRobin(ctx, len(jobs), func(i int) (time.Duration, error) {
			j := jobs[i]
			e.emit(Event{Kind: "job", Phase: "encode", Label: j.v.Label(), Msg: threadLabel(t)})
			d, out, n, err := e.encodeOnce(j.enc, &dst)
			j.out, j.last = n, out
			return d, err
		}, func(i int, a agg) {
			r := e.encResult(jobs[i], t, a)
			e.emit(Event{Kind: "pass", Result: &r})
		}, func(i int) {
			// Its last pass has just landed, and the next job's pass would
			// overwrite it: decoding it now, straight out of the encode buffer,
			// is what saves encoding it again for the decode phase.
			j := jobs[i]
			if !j.source {
				return
			}
			j.enc.Release()
			j.enc = nil
			e.decoded[j.vi] = true
			comp := &compressed{file: j.last, total: int64(len(j.last)), verified: -1}
			e.emit(Event{Kind: "phase", Phase: "decode"})
			_ = e.decodeSource(ctx, j.v, comp) // cancellation surfaces at the next pass
			comp.release()
			j.last = nil
			runtime.GOMAXPROCS(t) // each decode job set its own
			e.emit(Event{Kind: "phase", Phase: "encode"})
		})
		for i, j := range jobs {
			switch a := st[i]; {
			case a.err != nil:
				e.fail(OpEncode, j.v.Enc.ID, "", j.v.Level.N, j.v.Window, t, j.v.Enc.Module, a.err)
			case a.n > 0:
				e.record(e.encResult(j, t, a))
			}
			if j.enc != nil {
				j.enc.Release()
			}
			j.last = nil
		}
		dst = nil
		runtime.GC()
		if err != nil {
			return err
		}
	}
	return nil
}

// encodeOnce is the timed region. GC runs immediately before the clock starts
// and never inside it. For a single file it also returns the output, which
// stays valid until the next pass writes into the same buffer.
func (e *Engine) encodeOnce(enc codec.Encoder, dst *[]byte) (time.Duration, []byte, int64, error) {
	d := e.Data
	if d.Kind == corpus.KindFile {
		runtime.GC()
		start := time.Now()
		out, err := enc.Encode(*dst, d.File)
		el := time.Since(start)
		if err != nil {
			return 0, nil, 0, err
		}
		keep(dst, out)
		return el, out, int64(len(out)), nil
	}
	var n int64
	runtime.GC()
	start := time.Now()
	for i := range d.Files {
		out, err := enc.Encode(*dst, d.Files[i].Data)
		if err != nil {
			return 0, nil, 0, fmt.Errorf("%s: %w", d.Files[i].Name, err)
		}
		n += int64(len(out))
		keep(dst, out)
	}
	return time.Since(start), nil, n, nil
}

type decJob struct {
	dec  codec.Dec
	inst codec.Decoder
	t    int
	out  int64
}

// runDecode walks the source variants encoding did not already decode, one at
// a time: the compressed bytes are produced once, every decoder is timed
// against them, then they are released before the next variant. Holding every
// variant's output at once would be tens of gigabytes on a large corpus.
func (e *Engine) runDecode(ctx context.Context) error {
	kind := e.Data.Kind
	phased := false
	for vi, v := range e.Variants {
		decs := e.Cfg.DecodersOf(v, kind, e.Variants)
		if len(decs) == 0 || e.decoded[vi] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !phased {
			e.emit(Event{Kind: "phase", Phase: "decode"})
			phased = true
		}

		e.emit(Event{Kind: "job", Phase: "decode", Label: v.Label(), Msg: "preparing input"})
		runtime.GOMAXPROCS(e.Cfg.SrcThreads)
		comp, err := e.compressFor(v)
		if err != nil {
			e.fail(OpDecode, "", v.ID(), v.Level.N, v.Window, e.Cfg.SrcThreads, v.Enc.Module, err)
			// Every decoder planned against this source is now unreachable.
			for _, d := range decs {
				e.total -= len(e.Cfg.ThreadsFor(d.Concurrent(kind)))
			}
			continue
		}
		err = e.decodeSource(ctx, v, comp)
		comp.release()
		runtime.GC()
		if err != nil {
			return err
		}
	}
	return nil
}

// decodeSource times v's decoders against comp, which the caller owns and
// releases.
func (e *Engine) decodeSource(ctx context.Context, v Variant, comp *compressed) error {
	kind := e.Data.Kind
	decs := e.Cfg.DecodersOf(v, kind, e.Variants)
	if e.Cfg.Validate {
		// Recorded as a result, not just an event: the CLI has no event
		// channel, and a silent integrity failure defeats the whole mode.
		if err := e.validate(v, comp, decs); err != nil {
			e.fail(OpDecode, "", v.ID(), v.Level.N, v.Window, e.Cfg.SrcThreads, v.Enc.Module, err)
		}
	}

	var jobs []*decJob
	for _, d := range decs {
		for _, t := range e.Cfg.ThreadsFor(d.Concurrent(kind)) {
			inst, err := d.New(e.opts(t, 0))
			if err != nil {
				e.fail(OpDecode, d.ID, v.ID(), v.Level.N, v.Window, t, d.Module, err)
				e.total--
				continue
			}
			jobs = append(jobs, &decJob{dec: d, inst: inst, t: t})
		}
	}

	// The headroom matters: appender.ReadFrom grows when fewer than 64KB
	// remain, which on an exactly-sized buffer means a realloc and a full
	// memcpy inside the timed region.
	dst := make([]byte, 0, e.Data.Max+64<<10)
	st, err := e.roundRobin(ctx, len(jobs), func(i int) (time.Duration, error) {
		j := jobs[i]
		runtime.GOMAXPROCS(j.t)
		e.emit(Event{Kind: "job", Phase: "decode", Label: j.dec.ID + " <- " + v.Label(), Msg: threadLabel(j.t)})
		d, n, err := e.decodeOnce(j.inst, comp, &dst)
		j.out = n
		return d, err
	}, func(i int, a agg) {
		r := decResult(jobs[i], v, comp, a)
		e.emit(Event{Kind: "pass", Result: &r})
	}, nil)
	for i, j := range jobs {
		switch a := st[i]; {
		case a.err != nil:
			e.fail(OpDecode, j.dec.ID, v.ID(), v.Level.N, v.Window, j.t, j.dec.Module, a.err)
		case a.n > 0:
			e.record(decResult(j, v, comp, a))
		}
		j.inst.Release()
	}
	return err
}

func (e *Engine) decodeOnce(dec codec.Decoder, c *compressed, dst *[]byte) (time.Duration, int64, error) {
	if e.Data.Kind == corpus.KindFile {
		runtime.GC()
		start := time.Now()
		out, err := dec.Decode(*dst, c.file)
		el := time.Since(start)
		if err != nil {
			return 0, 0, err
		}
		keep(dst, out)
		return el, int64(len(out)), nil
	}
	var n int64
	runtime.GC()
	start := time.Now()
	for i := range c.files {
		out, err := dec.Decode(*dst, c.files[i])
		if err != nil {
			return 0, 0, fmt.Errorf("%s: %w", e.Data.Files[i].Name, err)
		}
		n += int64(len(out))
		keep(dst, out)
	}
	return time.Since(start), n, nil
}

// roundRobin runs every job once, then repeats the ones that want more passes.
// Interleaving is what keeps thermal drift from favouring whichever codec
// happened to run first.
// onPass is called after every completed pass with the running aggregate, so a
// consumer can show progress within a group instead of waiting for the group.
//
// Progress is counted in passes, not measurements. Counting a measurement on
// its first pass put the bar at 100% the moment the last group started
// repeating; counting it only when finished left the bar at 0% for the same
// stretch, because round-robin finishes a whole group at once. Part-finished
// measurements therefore earn partial credit, scaled back to whole
// measurements so done and total stay comparable across groups.
//
// onDone, when set, is called once for each job the moment it reaches its
// target, before any other job runs again. It may run a whole group of its own;
// the progress that group makes is carried in shift, so this group neither
// counts it as its own nor takes it back at its boundary.
func (e *Engine) roundRobin(ctx context.Context, n int, run func(int) (time.Duration, error),
	onPass func(int, agg), onDone func(int),
) ([]agg, error) {
	st := make([]agg, n)
	base := e.done
	shift := 0

	finished := func(i int) {
		if onDone == nil || st[i].n < st[i].target {
			return
		}
		before := e.done
		onDone(i)
		shift += e.done - before
	}

	report := func() {
		var have, want float64
		for i := range st {
			t := st[i].target
			if t <= 0 {
				t = e.Cfg.Passes // not measured yet, so assume the minimum
			}
			got := st[i].n
			if st[i].err != nil {
				got = t // resolved, just not successfully
			}
			have += float64(min(got, t))
			want += float64(t)
		}
		if want > 0 {
			// Never backwards: a first pass can raise a target well above the
			// assumed minimum, which would otherwise shrink the fraction.
			e.done = max(e.done, base+shift+int(float64(n)*have/want))
		}
	}

	for i := range n {
		if err := ctx.Err(); err != nil {
			return st, err
		}
		d, err := run(i)
		if err != nil {
			st[i].err = err
			report()
			continue
		}
		st[i].add(d)
		st[i].target = passesFor(d, e.Cfg)
		report()
		onPass(i, st[i])
		finished(i)
	}
	for {
		more := false
		for i := range n {
			if st[i].err != nil || st[i].n >= st[i].target {
				continue
			}
			if err := ctx.Err(); err != nil {
				return st, err
			}
			d, err := run(i)
			if err != nil {
				st[i].err = err
				report()
				continue
			}
			st[i].add(d)
			report()
			onPass(i, st[i])
			finished(i)
			more = true
		}
		if !more {
			e.done = base + shift + n // exact at the boundary, whatever the estimate did
			return st, nil
		}
	}
}

func threadLabel(t int) string { return fmt.Sprintf("%d thread(s)", t) }

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func (e *Engine) opts(t, window int) codec.Opts {
	return codec.Opts{Kind: e.Data.Kind, Threads: t, Window: window}
}

// encResult and decResult build the same Result from a partial and a finished
// aggregate, so the line the TUI shows mid-run is the line it ends up with.
func (e *Engine) encResult(j *encJob, t int, a agg) Result {
	return Result{
		Op: OpEncode, Codec: j.v.Enc.ID, Level: j.v.Level.N, Window: j.v.Window, Threads: t,
		Module: j.v.Enc.Module, In: e.Data.Total, Out: j.out,
		Best: a.best, Passes: a.n, SD: a.relSD(), Verified: -1,
	}
}

func decResult(j *decJob, v Variant, c *compressed, a agg) Result {
	return Result{
		Op: OpDecode, Codec: j.dec.ID, Src: v.ID(), Level: v.Level.N, Window: v.Window, Threads: j.t,
		Module: j.dec.Module, SrcModule: v.Enc.Module,
		In: c.total, Out: j.out,
		Best: a.best, Passes: a.n, SD: a.relSD(), Verified: c.verified,
	}
}

func (e *Engine) record(r Result) {
	e.results = append(e.results, r)
	e.emit(Event{Kind: "result", Result: &r})
}

func (e *Engine) fail(op Op, id, src string, level, window, t int, module string, err error) {
	r := Result{
		Op: op, Codec: id, Src: src, Level: level, Window: window, Threads: t,
		Module: module, Err: err.Error(), Verified: -1,
	}
	e.results = append(e.results, r)
	e.emit(Event{Kind: "warn", Msg: fmt.Sprintf("%s %s: %v", op, id, err)})
}
