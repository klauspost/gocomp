package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/klauspost/compress-site/internal/bench"
	"github.com/klauspost/compress-site/internal/codec"
	"github.com/klauspost/compress-site/internal/corpus"
	"github.com/klauspost/compress-site/internal/db"
	"github.com/klauspost/compress-site/internal/preset"
	"github.com/klauspost/compress-site/internal/sysinfo"
)

type runOpts struct {
	corpora  string
	codecs   string
	levels   string
	cpu      string
	srcCPU   int
	ops      string
	passes   int
	minWall  time.Duration
	maxPass  int
	maxJob   time.Duration
	block    int
	slow     bool
	collapse bool
	validate bool
	newRun   bool
	force    bool
	dryRun   bool
	jsonOut  bool
	quiet    bool
	out      string
	cache    string
	preset   string
	presets  string
}

func (o *runOpts) bind(fs *flag.FlagSet) {
	fs.StringVar(&o.corpora, "corpus", "small,medium", "corpus ids, a tier (small|medium|large|huge), a kind (file|dir), or all")
	fs.StringVar(&o.codecs, "codec", "all", "codec ids, optionally with levels: \"zskp:1-4,s2,gzkp:1,5,9\"")
	fs.StringVar(&o.levels, "level", "all", "levels for codecs without an explicit list: all | default | \"1-9,-2\"")
	fs.StringVar(&o.cpu, "cpu", "", "thread sweep, Go benchmark style: \"1,2,4,8\" or \"sweep\" (default NumCPU)")
	fs.IntVar(&o.srcCPU, "src-threads", 0, "threads used to produce decode inputs (default NumCPU)")
	fs.StringVar(&o.ops, "op", "encode,decode", "encode, decode or both")
	fs.IntVar(&o.passes, "passes", 3, "minimum passes per measurement")
	fs.DurationVar(&o.minWall, "min", 2*time.Second, "adaptive target wall time per measurement")
	fs.IntVar(&o.maxPass, "max-passes", 25, "cap on adaptive passes")
	fs.DurationVar(&o.maxJob, "max-job", 5*time.Minute, "a first pass longer than this gets no repeats")
	fs.IntVar(&o.block, "block", 4<<20, "stream block size for concurrent codecs")
	fs.BoolVar(&o.slow, "slow", false, "include codecs and levels marked slow (xz, brotli 10-11)")
	fs.BoolVar(&o.collapse, "collapse", false, "run codecs that declare no concurrency at one thread value only")
	fs.BoolVar(&o.validate, "validate", false, "xxh3 round-trip check for every encoder variant")
	fs.BoolVar(&o.newRun, "new-run", false, "store as a separate run instead of merging")
	fs.BoolVar(&o.force, "force", false, "ignore the memory guard")
	fs.BoolVar(&o.dryRun, "dry-run", false, "print the plan and estimated memory, then exit")
	fs.BoolVar(&o.jsonOut, "json", false, "emit newline-delimited JSON results instead of TSV")
	fs.BoolVar(&o.quiet, "quiet", false, "suppress progress output")
	fs.StringVar(&o.out, "out", "web/data", "database directory")
	fs.StringVar(&o.cache, "cache", "", "corpus cache directory (default testdata, or $"+corpus.EnvCache+")")
	fs.StringVar(&o.preset, "preset", "", "saved selection to run: a name from -presets, or a path to a .json")
	fs.StringVar(&o.presets, "presets", "", "preset directory (default "+preset.DefaultDir+")")
}

// loadPreset folds -preset into o and then puts back every flag that was given
// explicitly, so the command line always wins over the file. The snapshot has
// to happen first: flag.Value points at the same fields the preset overwrites.
func (o *runOpts) loadPreset(fs *flag.FlagSet) error {
	if o.preset == "" {
		return nil
	}
	p, err := preset.Load(preset.Dir(o.presets), o.preset)
	if err != nil {
		return err
	}
	explicit := map[string]string{}
	fs.Visit(func(f *flag.Flag) {
		if f.Name != "preset" && f.Name != "presets" {
			explicit[f.Name] = f.Value.String()
		}
	})

	if s := p.CorpusSpec(); s != "" {
		o.corpora = s
	}
	if s := p.CodecSpec(); s != "" {
		o.codecs = s
	}
	if s := p.ThreadSpec(); s != "" {
		o.cpu = s
	}
	if p.SrcThreads > 0 {
		o.srcCPU = p.SrcThreads
	}
	o.ops = p.OpSpec()
	if p.Passes > 0 {
		o.passes = p.Passes
	}
	if p.MinWall > 0 {
		o.minWall = time.Duration(p.MinWall)
	}
	if p.MaxPasses > 0 {
		o.maxPass = p.MaxPasses
	}
	if p.MaxJob > 0 {
		o.maxJob = time.Duration(p.MaxJob)
	}
	if p.Block > 0 {
		o.block = p.Block
	}
	o.validate, o.collapse, o.slow = p.Validate, p.Collapse, p.Slow

	for name, v := range explicit {
		if err := fs.Set(name, v); err != nil {
			return fmt.Errorf("-%s: %w", name, err)
		}
	}
	return nil
}

func cmdRun(args []string) error {
	fs := newFlags("run")
	var o runOpts
	o.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := o.loadPreset(fs); err != nil {
		return err
	}
	return runBench(o)
}

func cmdValidate(args []string) error {
	fs := newFlags("validate")
	var o runOpts
	o.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := o.loadPreset(fs); err != nil {
		return err
	}
	o.validate, o.passes, o.maxPass, o.out = true, 1, 1, ""
	o.ops = "encode,decode"
	return runBench(o)
}

func runBench(o runOpts) error {
	cfg, err := o.config()
	if err != nil {
		return err
	}
	sels, err := parseCodecs(o.codecs)
	if err != nil {
		return err
	}
	mode, err := parseLevelMode(o.levels)
	if err != nil {
		return err
	}
	corpora, err := corpus.Select(splitList(o.corpora))
	if err != nil {
		return err
	}

	env := sysinfo.Detect()
	if w := env.ArchLevelNote(); w != "" && !o.quiet {
		fmt.Fprintln(os.Stderr, "note:", w)
	}
	libs := sysinfo.Libs(codec.Modules())

	cache, err := corpus.OpenCache(corpus.CacheDir(o.cache))
	if err != nil {
		return err
	}

	if o.dryRun {
		return dryRun(o, cfg, sels, mode, corpora, cache)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	out := newWriter(o)
	defer out.flush()

	for _, cp := range corpora {
		if err := ctx.Err(); err != nil {
			return nil
		}
		variants, err := variantsFor(sels, mode, cp.Kind, o.slow)
		if err != nil {
			if !o.quiet {
				fmt.Fprintf(os.Stderr, "skipping %s: %v\n", cp.ID, err)
			}
			continue
		}
		if !o.force {
			if err := cp.CheckMem(); err != nil {
				return err
			}
		}
		if err := corpus.Fetch(ctx, cache, cp, progressTo(o)); err != nil {
			return err
		}
		data, err := corpus.Load(ctx, cache, cp, progressTo(o))
		if err != nil {
			return err
		}
		if err := cache.Save(); err != nil {
			return err
		}

		eng := &bench.Engine{Cfg: cfg, Data: data, Variants: variants}
		if !o.quiet {
			enc, dec := bench.Plan(cfg, data.Kind, variants)
			fmt.Fprintf(os.Stderr, "%s: %s, %d encode + %d decode measurements\n",
				cp.ID, humanBytes(data.Total), enc, dec)
		}
		results, runErr := eng.Run(ctx)
		clearProgress(o)
		for _, r := range results {
			out.write(cp, r)
		}
		meta := bench.MetaOf(data)
		data = nil
		runtime.GC()

		if o.out != "" && len(results) > 0 {
			if err := store(o, meta, results, env, libs, cfg); err != nil {
				return err
			}
		}
		if runErr != nil && ctx.Err() == nil {
			return runErr
		}
	}
	return nil
}

func (o runOpts) config() (bench.Config, error) {
	cfg := bench.DefaultConfig(runtime.NumCPU())
	switch o.cpu {
	case "":
	case "sweep":
		cfg.Threads = sweep(runtime.NumCPU())
	default:
		t, err := parseInts(o.cpu)
		if err != nil {
			return cfg, err
		}
		cfg.Threads = t
	}
	if o.srcCPU > 0 {
		cfg.SrcThreads = o.srcCPU
	}
	cfg.Encode, cfg.Decode = false, false
	for _, op := range splitList(o.ops) {
		switch op {
		case "encode", "c":
			cfg.Encode = true
		case "decode", "d":
			cfg.Decode = true
		case "both":
			cfg.Encode, cfg.Decode = true, true
		default:
			return cfg, fmt.Errorf("unknown op %q", op)
		}
	}
	if !cfg.Encode && !cfg.Decode {
		return cfg, fmt.Errorf("-op selected nothing")
	}
	cfg.Passes, cfg.MinWall, cfg.MaxPasses = o.passes, o.minWall, o.maxPass
	cfg.MaxJob, cfg.BlockSize = o.maxJob, o.block
	cfg.Validate, cfg.Collapse = o.validate, o.collapse
	return cfg, nil
}

// sweep is 1,2,4,...,n, always including n itself.
func sweep(n int) []int {
	out := []int{}
	for t := 1; t < n; t *= 2 {
		out = append(out, t)
	}
	return append(out, n)
}

func progressTo(o runOpts) corpus.ProgressFn {
	if o.quiet {
		return nil
	}
	var width int
	return func(p corpus.Progress) {
		msg := fmt.Sprintf("%s: %s %s", p.Corpus, p.Stage, humanBytes(p.Done))
		if p.Total > 0 {
			msg = fmt.Sprintf("%s: %s %s / %s", p.Corpus, p.Stage, humanBytes(p.Done), humanBytes(p.Total))
		}
		// Pad rather than using an erase escape: not every Windows console
		// honours ANSI, and a stray "[K" in the output is worse than a space.
		pad := max(0, width-len(msg))
		width = len(msg)
		fmt.Fprintf(os.Stderr, "\r%s%s", msg, strings.Repeat(" ", pad))
	}
}

// clearProgress erases the in-place progress line before printing something
// that should persist.
func clearProgress(o runOpts) {
	if !o.quiet {
		fmt.Fprintf(os.Stderr, "\r%s\r", strings.Repeat(" ", 78))
	}
}

// store converts results into a run and merges it into the database.
func store(o runOpts, meta bench.CorpusMeta, results []bench.Result, env sysinfo.Env, libs map[string]string, cfg bench.Config) error {
	run := bench.ToRun(meta, results, cfg, env, libs)
	res, err := db.Merge(o.out, run, o.newRun)
	if err != nil {
		return err
	}
	if !o.quiet {
		for _, w := range res.Warnings {
			fmt.Fprintln(os.Stderr, "warning:", w)
		}
		fmt.Fprintf(os.Stderr, "%s: run %s -- %d new, %d improved, %d kept\n",
			meta.ID, run.ID, res.Added, res.Improved, res.Kept)
	}
	return nil
}

func dryRun(o runOpts, cfg bench.Config, sels []codecSel, mode levelMode, corpora []corpus.Corpus, cache *corpus.Cache) error {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "corpus\tkind\tsize\tcached\tencode\tdecode\tpeak mem")
	var totalEnc, totalDec int
	for _, cp := range corpora {
		variants, err := variantsFor(sels, mode, cp.Kind, o.slow)
		if err != nil {
			continue
		}
		enc, dec := bench.Plan(cfg, cp.Kind, variants)
		totalEnc, totalDec = totalEnc+enc, totalDec+dec
		_, _, cached := cache.Resolve(cp)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%t\t%d\t%d\t%s\n",
			cp.ID, cp.Kind, humanBytes(cp.Bytes()), cached || cp.Synth != nil, enc, dec, humanBytes(cp.Need()))
	}
	fmt.Fprintf(tw, "total\t\t\t\t%d\t%d\t\n", totalEnc, totalDec)
	tw.Flush()

	fmt.Println()
	fmt.Println("concurrency paths (threads only matter where concurrent is true):")
	ctw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(ctw, "codec\trole\tformat\tfile\tdir\tthreads swept")
	for _, e := range codec.Encoders() {
		fmt.Fprintf(ctw, "%s\t%s\t%s\t%t\t%t\t%v\n", e.ID, "encode", e.Format,
			e.Concurrent(corpus.KindFile), e.Concurrent(corpus.KindDir),
			cfg.ThreadsFor(concIn(corpora, e.Conc)))
	}
	for _, d := range codec.Decoders() {
		fmt.Fprintf(ctw, "%s\t%s\t%s\t%t\t%t\t%v\n", d.ID, "decode", d.Format,
			d.Concurrent(corpus.KindFile), d.Concurrent(corpus.KindDir),
			cfg.ThreadsFor(concIn(corpora, d.Conc)))
	}
	return ctw.Flush()
}

// concIn reports whether conc covers any selected corpus's kind, so the dry
// run shows the sweep the listed corpora will actually produce.
func concIn(corpora []corpus.Corpus, conc corpus.Kind) bool {
	for _, c := range corpora {
		if conc&c.Kind != 0 {
			return true
		}
	}
	return false
}

// writer emits one line per measurement: TSV by default, NDJSON with -json.
type writer struct {
	o      runOpts
	tw     *tabwriter.Writer
	header bool
}

func newWriter(o runOpts) *writer {
	w := &writer{o: o}
	if !o.jsonOut {
		w.tw = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	}
	return w
}

func (w *writer) write(cp corpus.Corpus, r bench.Result) {
	if w.o.jsonOut {
		b, _ := json.Marshal(struct {
			Corpus string  `json:"corpus"`
			Op     string  `json:"op"`
			Codec  string  `json:"codec"`
			Src    string  `json:"src,omitempty"`
			Level  int     `json:"level"`
			T      int     `json:"threads"`
			In     int64   `json:"in"`
			Out    int64   `json:"out"`
			NS     int64   `json:"ns"`
			Passes int     `json:"passes"`
			SD     float64 `json:"sd"`
			MBps   float64 `json:"mbps"`
			Ratio  float64 `json:"ratio"`
			Err    string  `json:"err,omitempty"`
		}{
			cp.ID, r.Op.String(), r.Codec, r.Src, r.Level, r.Threads, r.In, r.Out,
			int64(r.Best), r.Passes, r.SD, r.MBps(), r.Ratio(), r.Err,
		})
		fmt.Println(string(b))
		return
	}
	if !w.header {
		fmt.Fprintln(w.tw, "corpus\top\tcodec\tlevel\tsrc\tthr\tin\tout\tratio\tms\tMB/s\tn")
		w.header = true
	}
	// A decode row's level belongs to its source encoder, which src already
	// names; repeating it in the level column only invites misreading.
	level, src := strconv.Itoa(r.Level), r.Src
	if r.Op == bench.OpDecode {
		level = "-"
	} else {
		src = "-"
	}
	if r.Err != "" {
		fmt.Fprintf(w.tw, "%s\t%s\t%s\t%s\t%s\t%d\tERROR: %s\n",
			cp.ID, r.Op, r.Codec, level, src, r.Threads, r.Err)
		return
	}
	fmt.Fprintf(w.tw, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%.3f\t%.3f\t%.02f\t%d\n",
		cp.ID, r.Op, r.Codec, level, src, r.Threads, r.In, r.Out,
		r.Ratio(), float64(r.Best.Nanoseconds())/1e6, r.MBps(), r.Passes)
}

func (w *writer) flush() {
	if w.tw != nil {
		w.tw.Flush()
	}
}

func humanBytes(n int64) string {
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
