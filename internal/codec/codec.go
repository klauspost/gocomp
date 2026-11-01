// Package codec holds one definition per compressor and decompressor.
//
// Encoders and decoders are matched by Format, so registering three gzip
// implementations automatically yields every decoder/encoder combination.
package codec

import (
	"slices"
	"strconv"
	"strings"

	"github.com/klauspost/gocomp/internal/corpus"
)

// Format identifies the wire format. Decoders can decode any encoder sharing it.
type Format string

const (
	FmtGzip         Format = "gzip"
	FmtDeflate      Format = "deflate"
	FmtZstd         Format = "zstd"
	FmtS2           Format = "s2"
	FmtS2Block      Format = "s2-block"
	FmtSnappy       Format = "snappy"
	FmtSnappyBlk    Format = "snappy-block"
	FmtMinLZ        Format = "minlz"
	FmtMinLZBlk     Format = "minlz-block"
	FmtMinLZSIMD    Format = "minlz-simd"
	FmtMinLZSIMDBlk Format = "minlz-simd-block"
	FmtLZ4          Format = "lz4"
	FmtBrotli       Format = "brotli"
	FmtXZ           Format = "xz"
	FmtLZMA2        Format = "lzma2"
	FmtBzip2        Format = "bzip2"
)

// FormatName is the display label for a format.
var FormatName = map[Format]string{
	FmtGzip: "gzip", FmtDeflate: "deflate", FmtZstd: "Zstandard",
	FmtS2: "S2", FmtS2Block: "S2 block", FmtSnappy: "Snappy", FmtSnappyBlk: "Snappy block",
	FmtMinLZ: "MinLZ", FmtMinLZBlk: "MinLZ block", FmtMinLZSIMD: "MinLZ SIMD", FmtMinLZSIMDBlk: "MinLZ SIMD block",
	FmtLZ4: "LZ4", FmtBrotli: "Brotli", FmtXZ: "xz", FmtLZMA2: "LZMA2", FmtBzip2: "bzip2",
}

// Opts are fixed for the duration of one measurement.
type Opts struct {
	Kind    corpus.Kind
	Threads int // also the process GOMAXPROCS for this measurement
	Window  int // window or block size, one of Enc.Windows; 0 leaves the library's own default
}

// pow2s lists the powers of two from lo to hi inclusive.
func pow2s(lo, hi int) []int {
	var out []int
	for n := lo; n <= hi; n *= 2 {
		out = append(out, n)
	}
	return out
}

// LibThreads is the concurrency to hand the library. It collapses to 1 for
// corpus kinds the implementation declares no concurrency for: spinning up a
// worker pool per 12KB file costs far more than it saves, and measuring that
// overhead would misrepresent the codec rather than the corpus.
func (o Opts) LibThreads(conc corpus.Kind) int {
	if conc&o.Kind != 0 {
		return o.Threads
	}
	return 1
}

// Level is one compression setting of an encoder.
type Level struct {
	N     int
	Name  string
	Slow  bool // skipped unless -slow
	Class LevelClass
}

// LevelClass says whether a level is a point on the codec's speed/size dial.
// The site shows Regular levels only unless asked: the others answer a
// different question, and one of them on a chart sets its scale.
type LevelClass uint8

const (
	Regular LevelClass = iota
	// Uncompressed stores rather than compresses: the memcpy ceiling.
	Uncompressed
	// Special compresses, but by another method than the dial the regular
	// levels sit on -- deflate's huffman-only and stateless modes.
	Special
)

func (c LevelClass) String() string {
	switch c {
	case Uncompressed:
		return "uncompressed"
	case Special:
		return "special"
	}
	return "regular"
}

// Encoder produces one self-contained frame or block per call. Instances are
// reused across files and passes, so Encode must not allocate on the hot path.
type Encoder interface {
	// Encode compresses src into dst[:0] and returns the result. The caller
	// sizes dst via Enc.MaxSize, so a correct encoder never grows it.
	Encode(dst, src []byte) ([]byte, error)
	Release()
}

// Decoder is the inverse of Encoder.
type Decoder interface {
	Decode(dst, src []byte) ([]byte, error)
	Release()
}

// Enc defines one encoder implementation and its levels.
type Enc struct {
	ID     string // stable DB key, e.g. "zskp"
	Name   string // display name, e.g. "Zstandard"
	Impl   string // implementation, e.g. "klauspost/compress"
	Format Format
	Module string // module path for version lookup; "std" for the standard library

	// Kinds are the corpus kinds this encoder can run at all.
	Kinds corpus.Kind
	// Conc are the corpus kinds where Opts.Threads actually parallelises work.
	// Zero means single-threaded.
	Conc corpus.Kind
	// ThreadsVaryOutput is set when the bytes, not just the speed, depend on
	// Opts.Threads where Conc applies. Only then is the thread count a decode
	// input was encoded at part of what a decode row measured.
	ThreadsVaryOutput bool

	Levels []Level
	Slow   bool // whole encoder is opt-in

	// Windows are the sizes Opts.Window may take, ascending; nil when the
	// window is fixed. A requested size not listed is skipped, never clamped:
	// clamping would measure the same setting twice under two names.
	Windows []int
	// DefWindow is the size window 0 leaves the library at, by level. A sweep
	// size equal to it is the same setting, and the site names the default by
	// it. Required with Windows; TestDefWindows holds it to the library.
	DefWindow func(level int) int

	// MaxSize bounds the compressed size of n input bytes. Defaults to a
	// generous bound that also covers incompressible input.
	MaxSize func(n int) int
	New     func(level int, o Opts) (Encoder, error)
}

// Dec defines one decoder implementation.
type Dec struct {
	ID     string
	Name   string
	Impl   string
	Format Format
	Module string
	Kinds  corpus.Kind
	Conc   corpus.Kind
	Slow   bool
	// With names the encoder whose selection brings this decoder into a run,
	// for a decoder whose ID no encoder shares. Every other decoder is brought
	// in by its own ID.
	With string
	New  func(o Opts) (Decoder, error)
}

// Concurrent reports whether threads matter for this encoder on kind.
func (e Enc) Concurrent(k corpus.Kind) bool { return e.Conc&k != 0 }

// Concurrent reports whether threads matter for this decoder on kind.
func (d Dec) Concurrent(k corpus.Kind) bool { return d.Conc&k != 0 }

// AcceptsWindow reports whether the encoder can run at window w.
func (e Enc) AcceptsWindow(w int) bool { return w == 0 || slices.Contains(e.Windows, w) }

// DefaultWindow is the size window 0 means at level, 0 for a fixed window.
func (e Enc) DefaultWindow(level int) int {
	if e.DefWindow == nil {
		return 0
	}
	return e.DefWindow(level)
}

func fixedWindow(n int) func(int) int { return func(int) int { return n } }

// Level returns the named level.
func (e Enc) Level(n int) (Level, bool) {
	for _, l := range e.Levels {
		if l.N == n {
			return l, true
		}
	}
	return Level{}, false
}

// Bound is the destination size the engine preallocates. A block codec's
// MaxEncodedLen goes negative past its block-size limit, so a result that is
// not larger than the input falls back to the generic frame bound.
func (e Enc) Bound(n int) int {
	if e.MaxSize != nil {
		if b := e.MaxSize(n); b > n {
			return b
		}
	}
	return n + n/16 + 64<<10
}

// Variant is the DB identity of an encoder at a level, e.g. "zskp-3".
func Variant(encID string, level int) string { return encID + "-" + strconv.Itoa(level) }

// SplitVariant is the inverse of Variant. It splits on the first '-', which is
// unambiguous because Register rejects IDs containing one and levels may be negative.
func SplitVariant(v string) (encID string, level int, ok bool) {
	i := strings.IndexByte(v, '-')
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(v[i+1:])
	if err != nil {
		return "", 0, false
	}
	return v[:i], n, true
}
