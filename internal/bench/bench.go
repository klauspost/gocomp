// Package bench runs in-memory compression and decompression measurements.
package bench

import (
	"math"
	"time"

	"github.com/klauspost/gocomp/internal/codec"
	"github.com/klauspost/gocomp/internal/corpus"
)

// Version is the measurement methodology version. Bump it whenever a change
// makes new timings incomparable with old ones: it is part of the run identity.
const Version = "1"

type Op uint8

const (
	OpEncode Op = iota
	OpDecode
)

func (o Op) String() string {
	if o == OpDecode {
		return "d"
	}
	return "c"
}

// Variant is one encoder at one level and window, the unit a decode
// measurement names.
type Variant struct {
	Enc    codec.Enc
	Level  codec.Level
	Window int // 0 is the codec's default
}

// ID is the stored source name. It leaves the window out: that has a column of
// its own, so "zskp-3" keeps meaning what it meant before windows existed.
func (v Variant) ID() string { return codec.Variant(v.Enc.ID, v.Level.N) }

// Label is the ID with the window, for progress and warnings.
func (v Variant) Label() string {
	if v.Window == 0 {
		return v.ID()
	}
	return v.ID() + " w" + WindowName(v.Window)
}

// Config controls scheduling and what gets measured.
type Config struct {
	Threads    []int         // the GOMAXPROCS sweep
	SrcThreads int           // fixed thread count for producing decode inputs
	Passes     int           // minimum passes per measurement
	MinWall    time.Duration // adaptive target wall time per measurement
	MaxPasses  int
	MaxJob     time.Duration // a first pass longer than this gets no repeats
	Windows    []int         // the window sweep; 0 is each codec's default
	Validate   bool
	Collapse   bool // run codecs that declare no concurrency at one thread value
	// AllDecoders times every decoder of a format against every selected
	// encoder's output, instead of only the decoders the selection names.
	AllDecoders bool
	Encode      bool
	Decode      bool
	// Pin runs n threads on n logical CPUs, one per core across the cache
	// groups first, when the CPU topology is known.
	Pin bool
}

// DefaultConfig is the starting point for both the CLI and the TUI.
func DefaultConfig(numCPU int) Config {
	return Config{
		Threads: []int{numCPU}, SrcThreads: numCPU,
		Passes: 3, MinWall: 2 * time.Second, MaxPasses: 25, MaxJob: 5 * time.Minute,
		Windows: []int{0}, Encode: true, Decode: true, Pin: true,
	}
}

// Result is one completed measurement.
type Result struct {
	Op      Op
	Codec   string // encoder id, or decoder id for OpDecode
	Src     string // OpDecode: the encoder variant that produced the input
	Level   int
	Window  int // OpDecode: the window the source was encoded with
	Threads int
	// SrcThreads is the thread count Src was encoded at, for OpDecode, or
	// 0 where Src's bytes do not depend on it.
	SrcThreads int
	Module     string
	SrcModule  string
	In, Out    int64
	Best       time.Duration
	Passes     int
	SD         float64 // relative standard deviation
	Verified   int     // 1 verified, 0 mismatch, -1 not checked
	Pin        int     // 1 pinned by Config.Pin, 0 placed by the OS
	Err        string
}

// MBps is throughput in MB/s (SI, 10^6 bytes) over the meaningful side of the
// transform: uncompressed input for encode, decompressed output for decode.
func (r Result) MBps() float64 {
	n := r.In
	if r.Op == OpDecode {
		n = r.Out
	}
	if r.Best <= 0 {
		return 0
	}
	return float64(n) / 1e6 / r.Best.Seconds()
}

// Ratio is uncompressed over compressed.
func (r Result) Ratio() float64 {
	u, c := r.In, r.Out
	if r.Op == OpDecode {
		u, c = r.Out, r.In
	}
	if c == 0 {
		return 0
	}
	return float64(u) / float64(c)
}

// agg accumulates pass timings: minimum for the reported value, Welford for
// the variance we record but do not display.
type agg struct {
	n        int
	best     time.Duration
	mean, m2 float64
	target   int
	err      error
}

func (a *agg) add(d time.Duration) {
	a.n++
	if a.best == 0 || d < a.best {
		a.best = d
	}
	x := float64(d)
	del := x - a.mean
	a.mean += del / float64(a.n)
	a.m2 += del * (x - a.mean)
}

// relSD is stddev over mean, so it is comparable across measurements of wildly
// different duration.
func (a *agg) relSD() float64 {
	if a.n < 2 || a.mean == 0 {
		return 0
	}
	return math.Sqrt(a.m2/float64(a.n-1)) / a.mean
}

// passesFor gives fast measurements more repetitions, as asked: a 10ms job gets
// the cap, a 30s job gets the minimum.
func passesFor(d time.Duration, c Config) int {
	if d <= 0 {
		return c.MaxPasses // too fast for the clock, so repeat as much as allowed
	}
	if d >= c.MaxJob {
		return 1
	}
	n := int(c.MinWall / d)
	return min(max(n, c.Passes), c.MaxPasses)
}

// ThreadsFor is the sweep a variant actually needs. Without Collapse every
// codec runs at every value, because GOMAXPROCS moves even a single-threaded
// codec's numbers through GC parallelism.
func (c Config) ThreadsFor(concurrent bool) []int {
	if !c.Collapse || concurrent || len(c.Threads) == 0 {
		return c.Threads
	}
	return c.Threads[len(c.Threads)-1:]
}

// Plan counts the measurements a config will produce, for the dry run and the
// TUI preview.
func Plan(cfg Config, kind corpus.Kind, variants []Variant) (encode, decode int) {
	for _, v := range variants {
		if cfg.Encode {
			encode += len(cfg.ThreadsFor(v.Enc.Concurrent(kind)))
		}
		if cfg.Decode {
			for _, d := range cfg.DecodersOf(v, kind, variants) {
				decode += len(cfg.ThreadsFor(d.Concurrent(kind)))
			}
		}
	}
	return encode, decode
}

// DecodersOf lists the decoders timed against v's output, vs being the whole
// selection. Every decoder of the format with AllDecoders; otherwise the ones
// the selection names: v's own implementation always reads it -- br2's output
// is read by br -- and so does every decoder whose codec is selected too, or
// the codec it declares With when it has no encoder of its own. Selecting a
// codec is saying it is being compared, so it is compared as a decoder too.
func (c Config) DecodersOf(v Variant, kind corpus.Kind, vs []Variant) []codec.Dec {
	var out []codec.Dec
	for _, d := range codec.DecodersFor(v.Enc.Format, kind) {
		if c.AllDecoders || d.Impl == v.Enc.Impl || selected(vs, d.ID) || (d.With != "" && selected(vs, d.With)) {
			out = append(out, d)
		}
	}
	return out
}

func selected(vs []Variant, id string) bool {
	for _, v := range vs {
		if v.Enc.ID == id {
			return true
		}
	}
	return false
}
