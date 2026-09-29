package codec

import (
	"io"

	"github.com/klauspost/compress-site/internal/corpus"
	"github.com/minio/minlz"
)

var minlzLevels = []Level{
	{minlz.LevelSuperFast, "superfast", false},
	{minlz.LevelUncompressed, "uncompressed", false},
	{minlz.LevelFastest, "fastest", false},
	{minlz.LevelBalanced, "balanced", false},
	{minlz.LevelSmallest, "smallest", false},
}

// mzBlkLevels drops only LevelUncompressed; the block API supports superfast.
var mzBlkLevels = append([]Level{minlzLevels[0]}, minlzLevels[2:]...)

func init() {
	RegisterEnc(Enc{
		ID: "minlz", Name: "MinLZ", Impl: "minio/minlz", Format: FmtMinLZ,
		Module: "github.com/minio/minlz", Kinds: bothKinds, Conc: corpus.KindFile,
		Levels: minlzLevels,
		New: func(lvl int, o Opts) (Encoder, error) {
			opts := []minlz.WriterOption{minlz.WriterConcurrency(o.LibThreads(corpus.KindFile))}
			if lvl == minlz.LevelUncompressed {
				opts = append(opts, minlz.WriterUncompressed())
			} else {
				opts = append(opts, minlz.WriterLevel(lvl))
			}
			if o.BlockSize > 0 {
				opts = append(opts, minlz.WriterBlockSize(min(o.BlockSize, minlz.MaxBlockSize)))
			}
			w := minlz.NewWriter(nil, opts...)
			return &streamEnc{rst: w.Reset, w: w, fast: w.EncodeBuffer, fin: w.Close}, nil
		},
	})
	RegisterDec(Dec{
		ID: "minlz", Name: "MinLZ", Impl: "minio/minlz", Format: FmtMinLZ,
		Module: "github.com/minio/minlz", Kinds: bothKinds, Conc: corpus.KindFile,
		New: func(o Opts) (Decoder, error) {
			r := minlz.NewReader(nil, minlz.ReaderFallback(false))
			// DecodeConcurrent is the right entry point at every concurrency
			// here; WriteTo is measurably slower on small inputs.
			n := o.LibThreads(corpus.KindFile)
			return &streamDec{
				rst: nopReset(r.Reset),
				fn:  func(w io.Writer) (int64, error) { return r.DecodeConcurrent(w, n) },
			}, nil
		},
	})

	// Block mode caps at MaxBlockSize, so it is directory-only.
	RegisterEnc(Enc{
		ID: "mzblk", Name: "MinLZ block", Impl: "minio/minlz", Format: FmtMinLZBlk,
		Module: "github.com/minio/minlz", Kinds: corpus.KindDir,
		Levels: mzBlkLevels, MaxSize: minlz.MaxEncodedLen,
		New: func(lvl int, o Opts) (Encoder, error) {
			return blockFn{fn: func(dst, src []byte) ([]byte, error) {
				return minlz.Encode(dst, src, lvl)
			}}, nil
		},
	})
	RegisterDec(Dec{
		ID: "mzblk", Name: "MinLZ block", Impl: "minio/minlz", Format: FmtMinLZBlk,
		Module: "github.com/minio/minlz", Kinds: corpus.KindDir,
		New: func(o Opts) (Decoder, error) { return blockFn{fn: minlz.Decode}, nil },
	})
}
