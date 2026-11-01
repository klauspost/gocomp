package codec

import (
	"strconv"

	"github.com/klauspost/gocomp/internal/corpus"
	"github.com/pierrec/lz4/v4"
)

// lz4Level maps our integer levels onto the library's sparse constants:
// 0 is Fast, and LevelN is 1<<(8+N) in v4 (the v2 constants were one shift lower).
func lz4Level(n int) lz4.CompressionLevel {
	if n <= 0 {
		return lz4.Fast
	}
	return lz4.CompressionLevel(1 << (8 + n))
}

// lz4 has no block registration: the raw block format carries neither the
// decoded length nor an incompressible marker, so a round-trippable block
// variant would need framing we invented ourselves. The frame covers both kinds.
func init() {
	// Level 9 is absent. CompressionLevel is not an effort dial the library
	// interprets: it is handed to the HC matcher as a raw hash-chain search
	// depth, and that walk also stops at winSize (64KB). Chain positions
	// strictly decrease, so at most winSize of them fall inside the window --
	// which makes level 8's depth of 65536 already unlimited and level 9's
	// 131072 unreachable. The two produce identical output for every input.
	//
	// Levels 2-7 are kept even though they converge on most corpora: they are
	// genuinely different depths, and where hash chains are long the sweep is
	// the most interesting LZ4 result there is (consensus-db runs 1213 MB/s at
	// level 1 and 138 MB/s at level 7 to buy 0.4% of ratio).
	lv := []Level{{N: 0, Name: "fast"}}
	for i := 1; i <= 8; i++ {
		lv = append(lv, Level{N: i, Name: strconv.Itoa(i), Slow: i == 8})
	}

	RegisterEnc(Enc{
		ID: "lz4", Name: "LZ4", Impl: "pierrec/lz4", Format: FmtLZ4,
		Module: "github.com/pierrec/lz4/v4", Kinds: bothKinds, Conc: corpus.KindFile,
		Levels: lv,
		// The frame format has exactly these four; the match window is 64KB
		// whatever the block size.
		Windows:   []int{int(lz4.Block64Kb), int(lz4.Block256Kb), int(lz4.Block1Mb), int(lz4.Block4Mb)},
		DefWindow: fixedWindow(int(lz4.Block4Mb)),
		New: func(lvl int, o Opts) (Encoder, error) {
			w := lz4.NewWriter(nil)
			opts := []lz4.Option{
				lz4.ConcurrencyOption(o.LibThreads(corpus.KindFile)),
				lz4.CompressionLevelOption(lz4Level(lvl)),
			}
			if o.Window > 0 {
				opts = append(opts, lz4.BlockSizeOption(lz4.BlockSize(o.Window)))
			}
			// Options survive Reset, so they are applied once here.
			if err := w.Apply(opts...); err != nil {
				return nil, err
			}
			return &streamEnc{rst: w.Reset, w: w, fin: w.Close}, nil
		},
	})
	RegisterDec(Dec{
		ID: "lz4", Name: "LZ4", Impl: "pierrec/lz4", Format: FmtLZ4,
		Module: "github.com/pierrec/lz4/v4", Kinds: bothKinds, Conc: corpus.KindFile,
		New: func(o Opts) (Decoder, error) {
			r := lz4.NewReader(nil)
			if err := r.Apply(lz4.ConcurrencyOption(o.LibThreads(corpus.KindFile))); err != nil {
				return nil, err
			}
			return &streamDec{rst: nopReset(r.Reset), fn: copyTo(r)}, nil
		},
	})
}
