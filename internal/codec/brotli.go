package codec

import (
	"strconv"

	"github.com/andybalholm/brotli"
)

const brotliMod = "github.com/andybalholm/brotli"

func init() {
	var lv []Level
	for i := 0; i <= 11; i++ {
		lv = append(lv, Level{i, strconv.Itoa(i), i >= 10})
	}
	RegisterEnc(Enc{
		ID: "br", Name: "Brotli", Impl: "andybalholm/brotli", Format: FmtBrotli,
		Module: brotliMod, Kinds: bothKinds, Levels: lv,
		New: func(lvl int, o Opts) (Encoder, error) {
			w := brotli.NewWriterLevel(nil, lvl)
			return &streamEnc{rst: w.Reset, w: w, fin: w.Close}, nil
		},
	})

	// The V2 matchfinder encoder is a separate implementation of the same
	// format, so it also gives the decoders a second input shape to chew on.
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
