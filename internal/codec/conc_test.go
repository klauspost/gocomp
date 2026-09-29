package codec

import (
	"runtime"
	"testing"
	"time"

	"github.com/klauspost/compress-site/internal/corpus"
)

// concProbeSize has to clear zstd's job size before WithConcurrentBlocks
// dispatches more than one job: at 64MB zstd shows no gain at all, at 256MB it
// reaches ~0.6x. Every other concurrent codec scales from far smaller inputs.
const concProbeSize = 256 << 20

// TestConcurrencyDeclarations is the test that catches the EncodeAll /
// EncodeBuffer / SetConcurrency traps. Each of them silently produces a flat
// thread sweep, and a "32-thread" number that was actually single-threaded is
// the worst thing this tool could publish.
//
// Only the positive direction is asserted. The reverse is not: GOMAXPROCS
// speeds up even a single-threaded codec through parallel GC, so "scaled a bit
// without declaring Conc" is not evidence of anything.
func TestConcurrencyDeclarations(t *testing.T) {
	if testing.Short() {
		t.Skip("timing sensitive")
	}
	hi := min(8, runtime.NumCPU())
	if hi < 4 {
		t.Skip("needs at least 4 CPUs to tell concurrency from noise")
	}
	src := realisticData(concProbeSize)

	measure := func(e Enc, lvl, threads int) time.Duration {
		o := Opts{Kind: corpus.KindFile, Threads: threads}
		enc, err := e.New(lvl, o)
		if err != nil {
			t.Fatalf("%s: %v", e.ID, err)
		}
		defer enc.Release()
		dst := make([]byte, 0, e.Bound(len(src)))
		var best time.Duration
		for range 3 {
			runtime.GC()
			start := time.Now()
			out, err := enc.Encode(dst, src)
			d := time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			if cap(out) > cap(dst) {
				dst = out[:0]
			}
			if best == 0 || d < best {
				best = d
			}
		}
		return best
	}

	for _, e := range Encoders() {
		if e.Slow || !e.Concurrent(corpus.KindFile) {
			continue
		}
		t.Run(e.ID, func(t *testing.T) {
			lvl := defaultLevelOf(e)
			one := measure(e, lvl, 1)
			many := measure(e, lvl, hi)
			ratio := float64(many) / float64(one)
			t.Logf("threads=1 %v, threads=%d %v (%.2fx)", one, hi, many, ratio)
			if ratio > 0.8 {
				t.Errorf("declares Conc for file corpora but %d threads gave %.2fx of the 1-thread time; "+
					"the concurrent path is probably not wired up", hi, ratio)
			}
		})
	}
}

// realisticData compresses a few dozen times over, like text or JSON. The
// round-trip tests' data is near-pure repetition, which every codec handles at
// RLE speed; job dispatch then dominates and the measurement says nothing
// about whether concurrency works.
func realisticData(n int) []byte {
	words := make([][]byte, 512)
	seed := make([]byte, 512*12)
	corpus.Rand(seed)
	for i := range words {
		words[i] = seed[i*12 : i*12+4+i%8]
	}
	b := make([]byte, 0, n+64)
	var x uint32 = 12345
	for len(b) < n {
		x = x*1664525 + 1013904223
		b = append(b, words[(x>>8)%uint32(len(words))]...)
		b = append(b, ' ')
		if x%64 == 0 {
			b = append(b, '\n')
		}
	}
	return b[:n]
}

func defaultLevelOf(e Enc) int {
	for _, l := range e.Levels {
		if l.Name == "default" {
			return l.N
		}
	}
	for _, l := range e.Levels {
		if !l.Slow && l.Name != "store" && l.Name != "uncompressed" {
			return l.N
		}
	}
	return e.Levels[0].N
}
