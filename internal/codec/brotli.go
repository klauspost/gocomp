package codec

import (
	"math/bits"
	"strconv"

	"github.com/andybalholm/brotli"
)

const brotliMod = "github.com/andybalholm/brotli"

// log2 turns a window into brotli's LGWin, keeping 0 as "the default".
func log2(n int) int {
	if n == 0 {
		return 0
	}
	return bits.TrailingZeros(uint(n))
}

func init() {
	var lv []Level
	for i := 0; i <= 11; i++ {
		lv = append(lv, Level{N: i, Name: strconv.Itoa(i), Slow: i >= 10})
	}
	RegisterEnc(Enc{
		ID: "br", Name: "Brotli", Impl: "andybalholm/brotli", Format: FmtBrotli,
		// LGWin 10-24. Levels 0 and 1 floor the stream header at 256KB, so
		// below that those two may measure the same setting more than once.
		Module: brotliMod, Kinds: bothKinds, Levels: lv, Windows: pow2s(1<<10, 16<<20),
		New: func(lvl int, o Opts) (Encoder, error) {
			// LGWin 0 is the library's own choice for the quality.
			w := brotli.NewWriterOptions(nil, brotli.WriterOptions{Quality: lvl, LGWin: log2(o.Window)})
			return &streamEnc{rst: w.Reset, w: w, fin: w.Close}, nil
		},
	})

	// The V2 matchfinder encoder is a separate implementation of the same
	// format, so it also gives the decoders a second input shape to chew on.
	// It has no window setting: NewWriterV2 fixes MaxDistance at 1MB.
	//
	// It stops at 9 (NewWriterV2 clamps above that), and 6 is dropped: the
	// level switch maps 5 and 6 to the same Bargain1 matchfinder behind the
	// same encoder, so measuring both measured one setting twice and the rows
	// came back byte-identical. 0 and 1 also share a matchfinder but not an
	// encoder -- level 0 swaps in FastEncoder -- so both of those are real.
	v2 := make([]Level, 0, 9)
	for _, l := range lv[:10] {
		if l.N != 6 {
			v2 = append(v2, l)
		}
	}
	RegisterEnc(Enc{
		ID: "br2", Name: "Brotli (matchfinder)", Impl: "andybalholm/brotli", Format: FmtBrotli,
		Module: brotliMod, Kinds: bothKinds, Levels: v2,
		New: func(lvl int, o Opts) (Encoder, error) {
			w := brotli.NewWriterV2(nil, lvl)
			return &streamEnc{rst: w.Reset, w: w, fin: w.Close}, nil
		},
	})

	RegisterDec(Dec{
		ID: "br", Name: "Brotli", Impl: "andybalholm/brotli", Format: FmtBrotli,
		Module: brotliMod, Kinds: bothKinds,
		New: func(o Opts) (Decoder, error) {
			r := brotli.NewReader(nil)
			return &streamDec{rst: r.Reset, fn: copyTo(r)}, nil
		},
	})
}
