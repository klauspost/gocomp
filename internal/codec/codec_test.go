package codec

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/klauspost/compress-site/internal/corpus"
)

// testData is semi-compressible: repeated structure with incompressible noise,
// so neither a store-only nor a match-only path accidentally passes.
func testData(n int) []byte {
	b := make([]byte, 0, n)
	noise := make([]byte, 4096)
	corpus.Rand(noise)
	for i := 0; len(b) < n; i++ {
		b = append(b, fmt.Sprintf("line %d: the quick brown fox jumps over the lazy dog\n", i%97)...)
		if i%16 == 0 {
			b = append(b, noise[:64]...)
		}
	}
	return b[:n]
}

func kinds(k corpus.Kind) []corpus.Kind {
	var out []corpus.Kind
	for _, c := range []corpus.Kind{corpus.KindFile, corpus.KindDir} {
		if k&c != 0 {
			out = append(out, c)
		}
	}
	return out
}

// TestRoundTrip encodes with every registered encoder variant and decodes the
// result with every decoder sharing its format.
func TestRoundTrip(t *testing.T) {
	src := testData(96 << 10)
	for _, e := range Encoders() {
		if e.Slow && testing.Short() {
			continue
		}
		for _, lvl := range e.Levels {
			if lvl.Slow && testing.Short() {
				continue
			}
			for _, k := range kinds(e.Kinds) {
				t.Run(fmt.Sprintf("%s/%d/%s", e.ID, lvl.N, k), func(t *testing.T) {
					t.Parallel()
					o := Opts{Kind: k, Threads: 4}
					enc, err := e.New(lvl.N, o)
					if err != nil {
						t.Fatalf("New: %v", err)
					}
					defer enc.Release()

					comp, err := enc.Encode(make([]byte, 0, e.Bound(len(src))), src)
					if err != nil {
						t.Fatalf("Encode: %v", err)
					}
					if len(comp) == 0 {
						t.Fatal("Encode produced no output")
					}

					decs := DecodersFor(e.Format, k)
					if len(decs) == 0 {
						t.Skipf("no decoder registered for format %s on %s", e.Format, k)
					}
					for _, d := range decs {
						dec, err := d.New(o)
						if err != nil {
							t.Fatalf("decoder %s New: %v", d.ID, err)
						}
						got, err := dec.Decode(make([]byte, 0, len(src)), comp)
						dec.Release()
						if err != nil {
							t.Fatalf("decoder %s: %v", d.ID, err)
						}
						if !bytes.Equal(got, src) {
							t.Fatalf("decoder %s: round trip mismatch, got %d bytes want %d",
								d.ID, len(got), len(src))
						}
					}
				})
			}
		}
	}
}

// TestEncoderReuse catches encoders that only produce correct output on their
// first use, which the benchmark loop would otherwise hide behind a min().
func TestEncoderReuse(t *testing.T) {
	a, b := testData(40<<10), testData(9<<10)
	for _, e := range Encoders() {
		if e.Slow {
			continue
		}
		lvl := e.Levels[len(e.Levels)-1]
		if lvl.Slow {
			lvl = e.Levels[0]
		}
		for _, k := range kinds(e.Kinds) {
			t.Run(fmt.Sprintf("%s/%s", e.ID, k), func(t *testing.T) {
				t.Parallel()
				o := Opts{Kind: k, Threads: 2}
				enc, err := e.New(lvl.N, o)
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				defer enc.Release()

				decs := DecodersFor(e.Format, k)
				if len(decs) == 0 {
					t.Skip("no decoder")
				}
				dec, err := decs[0].New(o)
				if err != nil {
					t.Fatalf("decoder New: %v", err)
				}
				defer dec.Release()

				dst := make([]byte, 0, e.Bound(len(a)))
				out := make([]byte, 0, len(a))
				for i, src := range [][]byte{a, b, a, b, a} {
					comp, err := enc.Encode(dst, src)
					if err != nil {
						t.Fatalf("pass %d encode: %v", i, err)
					}
					got, err := dec.Decode(out, comp)
					if err != nil {
						t.Fatalf("pass %d decode: %v", i, err)
					}
					if !bytes.Equal(got, src) {
						t.Fatalf("pass %d: mismatch, got %d want %d", i, len(got), len(src))
					}
				}
			})
		}
	}
}

// TestIDsAndLevels guards the registry invariants the DB keys depend on.
func TestIDsAndLevels(t *testing.T) {
	for _, e := range Encoders() {
		if _, _, ok := SplitVariant(Variant(e.ID, -3)); !ok {
			t.Errorf("%s: variant id does not round trip", e.ID)
		}
		seen := map[int]bool{}
		for _, l := range e.Levels {
			if seen[l.N] {
				t.Errorf("%s: duplicate level %d", e.ID, l.N)
			}
			seen[l.N] = true
			if l.Name == "" {
				t.Errorf("%s: level %d has no name", e.ID, l.N)
			}
			// The site hides levels by class, so a store level registered
			// without one would quietly top every chart again.
			if stores := l.Name == "store" || l.Name == "uncompressed"; stores != (l.Class == Uncompressed) {
				t.Errorf("%s: level %d %q has class %s", e.ID, l.N, l.Name, l.Class)
			}
		}
		if e.Module == "" || e.Format == "" || e.Kinds == 0 {
			t.Errorf("%s: incomplete registration", e.ID)
		}
		if _, ok := FormatName[e.Format]; !ok {
			t.Errorf("%s: format %s has no display name", e.ID, e.Format)
		}
	}
	for _, d := range Decoders() {
		if d.Module == "" || d.Format == "" || d.Kinds == 0 {
			t.Errorf("decoder %s: incomplete registration", d.ID)
		}
		// A run brings a decoder in through the encoders it selects, so one
		// no encoder can name would be unreachable outside -decoders=all.
		_, own := LookupEnc(d.ID)
		switch w, ok := LookupEnc(d.With); {
		case own && d.With != "":
			t.Errorf("decoder %s: With is only for decoders no encoder shares an ID with", d.ID)
		case !own && (!ok || w.Format != d.Format):
			t.Errorf("decoder %s: shares no encoder's ID, so With must name an encoder of format %s", d.ID, d.Format)
		}
	}
}

const maxInt = int64(^uint(0) >> 1)

// TestBoundIsSane guards the destination sizing. A block codec's
// MaxEncodedLen goes negative past its block-size limit, and handing that to
// make() panics rather than failing the measurement.
func TestBoundIsSane(t *testing.T) {
	sizes := []int64{1, 1 << 10, 8 << 20, 100 << 20, 1 << 30, 12 << 30}
	for _, e := range Encoders() {
		for _, n64 := range sizes {
			if n64 > maxInt {
				continue // unrepresentable on this platform
			}
			n := int(n64)
			if e.Kinds&corpus.KindFile == 0 && n > 8<<20 {
				continue // block-only codecs never see a buffer this large
			}
			if b := e.Bound(n); b <= n {
				t.Errorf("%s: Bound(%d) = %d, must exceed the input size", e.ID, n, b)
			}
		}
	}
}

// TestWindows round-trips every encoder at both ends of its window list, and
// checks the setting reaches the library at all. The input is a random 256KB
// chunk repeated, which a large window finds and a small one cannot -- every
// smallest window is below that distance -- and where the window is a block
// size the framing differs regardless, so the two outputs must differ in size.
// An adapter that drops Opts.Window passes a round trip and fails this, the way
// conc_test catches dropped concurrency.
func TestWindows(t *testing.T) {
	chunk := make([]byte, 256<<10)
	corpus.Rand(chunk)
	src := bytes.Repeat(chunk, 3)
	for _, e := range Encoders() {
		if len(e.Windows) == 0 || (e.Slow && testing.Short()) {
			continue
		}
		// Capped because the top zstd and xz sizes would allocate gigabytes to
		// compress half a megabyte.
		lo, hi := e.Windows[0], e.Windows[0]
		for _, w := range e.Windows {
			if w <= 8<<20 {
				hi = w
			}
		}
		lvl := windowTestLevel(e)
		for _, k := range kinds(e.Kinds) {
			t.Run(fmt.Sprintf("%s/%s", e.ID, k), func(t *testing.T) {
				t.Parallel()
				small := roundTrip(t, e, lvl, Opts{Kind: k, Threads: 4, Window: lo}, src)
				large := roundTrip(t, e, lvl, Opts{Kind: k, Threads: 4, Window: hi}, src)
				if small == large {
					t.Errorf("level %d: windows %d and %d both gave %d bytes; Opts.Window is not reaching the library",
						lvl, lo, hi, small)
				}
			})
		}
	}
}

// TestDefWindows holds each declared default to the library: window 0 and the
// declared size must write the same bytes. Every format records its window or
// block size in the stream, and the input is longer than any default, so a
// declaration a library upgrade has made wrong changes the output.
func TestDefWindows(t *testing.T) {
	chunk := make([]byte, 256<<10)
	corpus.Rand(chunk)
	src := bytes.Repeat(chunk, 36)
	for _, e := range Encoders() {
		if e.DefWindow == nil || (e.Slow && testing.Short()) {
			continue
		}
		seen := map[int]bool{}
		for _, l := range e.Levels {
			def := e.DefaultWindow(l.N)
			if def == 0 || seen[def] || (l.Slow && testing.Short()) {
				continue
			}
			seen[def] = true
			t.Run(fmt.Sprintf("%s/%d", e.ID, l.N), func(t *testing.T) {
				t.Parallel()
				a, b := encode(t, e, l.N, Opts{Kind: corpus.KindFile, Threads: 1}, src),
					encode(t, e, l.N, Opts{Kind: corpus.KindFile, Threads: 1, Window: def}, src)
				if !bytes.Equal(a, b) {
					t.Errorf("window 0 and the declared default %d differ (%d vs %d bytes)", def, len(a), len(b))
				}
				// Otherwise this level could not tell a wrong declaration apart.
				lo := e.Windows[0]
				if bytes.Equal(a, encode(t, e, l.N, Opts{Kind: corpus.KindFile, Threads: 1, Window: lo}, src)) {
					t.Errorf("window %d writes the same bytes as the default", lo)
				}
			})
		}
	}
}

func encode(t *testing.T, e Enc, level int, o Opts, src []byte) []byte {
	t.Helper()
	enc, err := e.New(level, o)
	if err != nil {
		t.Fatalf("window %d: New: %v", o.Window, err)
	}
	defer enc.Release()
	comp, err := enc.Encode(make([]byte, 0, e.Bound(len(src))), src)
	if err != nil {
		t.Fatalf("window %d: Encode: %v", o.Window, err)
	}
	return comp
}

// windowTestLevel avoids store levels, where a window has nothing to find:
// the level named default, else the middle of the range.
func windowTestLevel(e Enc) int {
	for _, l := range e.Levels {
		if l.Name == "default" {
			return l.N
		}
	}
	return e.Levels[len(e.Levels)/2].N
}

// roundTrip encodes src, decodes it with every decoder of the format and
// returns the compressed size.
func roundTrip(t *testing.T, e Enc, level int, o Opts, src []byte) int {
	t.Helper()
	comp := encode(t, e, level, o, src)
	for _, d := range DecodersFor(e.Format, o.Kind) {
		dec, err := d.New(Opts{Kind: o.Kind, Threads: o.Threads})
		if err != nil {
			t.Fatalf("decoder %s New: %v", d.ID, err)
		}
		got, err := dec.Decode(make([]byte, 0, len(src)), comp)
		dec.Release()
		if err != nil {
			t.Fatalf("window %d: decoder %s: %v", o.Window, d.ID, err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("window %d: decoder %s: round trip mismatch", o.Window, d.ID)
		}
	}
	return len(comp)
}

// TestRegisterRejectsBadWindows guards the invariant ParseWindows and Expand
// rely on: a codec's windows are ascending powers of two.
func TestRegisterRejectsBadWindows(t *testing.T) {
	for _, ws := range [][]int{{3 << 10}, {64 << 10, 32 << 10}, {1 << 10, 1 << 10}, {0}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("windows %v were accepted", ws)
				}
			}()
			RegisterEnc(Enc{ID: "badwin", Windows: ws})
		}()
	}
}

// TestEveryFormatDecodable ensures no encoder is registered without a way to
// verify its output.
func TestEveryFormatDecodable(t *testing.T) {
	for _, e := range Encoders() {
		for _, k := range kinds(e.Kinds) {
			if len(DecodersFor(e.Format, k)) == 0 {
				t.Errorf("%s: no decoder for format %s on %s corpora", e.ID, e.Format, k)
			}
		}
	}
}
