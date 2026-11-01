package codec

import (
	"io"

	"github.com/klauspost/gocomp/internal/corpus"
	"github.com/minio/minlz"
)

var minlzLevels = []Level{
	{N: minlz.LevelSuperFast, Name: "superfast"},
	{N: minlz.LevelUncompressed, Name: "uncompressed", Class: Uncompressed},
	{N: minlz.LevelFastest, Name: "fastest"},
	{N: minlz.LevelBalanced, Name: "balanced"},
	{N: minlz.LevelSmallest, Name: "smallest"},
}

// mzBlkLevels drops only LevelUncompressed; the block API supports superfast.
var mzBlkLevels = append([]Level{minlzLevels[0]}, minlzLevels[2:]...)

// Smallest has no delta level: it always tries both literal modes.
var mzSIMDLevels = []Level{
	{N: minlz.LevelSuperFastSIMD, Name: "superfast"},
	{N: minlz.LevelFastestSIMD, Name: "fastest"},
	{N: minlz.LevelBalancedSIMD, Name: "balanced"},
	{N: minlz.LevelSmallestSIMD, Name: "smallest"},
	{N: minlz.LevelSuperFastSIMD | minlz.LevelSIMDDelta, Name: "superfast-delta", Class: Special},
	{N: minlz.LevelFastestSIMD | minlz.LevelSIMDDelta, Name: "fastest-delta", Class: Special},
	{N: minlz.LevelBalancedSIMD | minlz.LevelSIMDDelta, Name: "balanced-delta", Class: Special},
}

func init() {
	RegisterEnc(Enc{
		ID: "minlz", Name: "MinLZ", Impl: "minio/minlz", Format: FmtMinLZ,
		Module: "github.com/minio/minlz", Kinds: bothKinds, Conc: corpus.KindFile,
		Levels: minlzLevels, Windows: pow2s(4<<10, minlz.MaxBlockSize), DefWindow: fixedWindow(2 << 20),
		New: newMinLZEnc,
	})
	RegisterDec(Dec{
		ID: "minlz", Name: "MinLZ", Impl: "minio/minlz", Format: FmtMinLZ,
		Module: "github.com/minio/minlz", Kinds: bothKinds, Conc: corpus.KindFile,
		New: newMinLZDec,
	})

	// Block mode caps at MaxBlockSize, so it is directory-only.
	RegisterEnc(Enc{
		ID: "mzblk", Name: "MinLZ block", Impl: "minio/minlz", Format: FmtMinLZBlk,
		Module: "github.com/minio/minlz", Kinds: corpus.KindDir,
		Levels: mzBlkLevels, MaxSize: minlz.MaxEncodedLen,
		New: newMzBlkEnc,
	})
	RegisterDec(Dec{
		ID: "mzblk", Name: "MinLZ block", Impl: "minio/minlz", Format: FmtMinLZBlk,
		Module: "github.com/minio/minlz", Kinds: corpus.KindDir,
		New: newMzBlkDec,
	})

	// SIMD blocks get their own formats so released MinLZ decoders, which
	// cannot read them, are never paired with them.
	RegisterEnc(Enc{
		ID: "mzsimd", Name: "MinLZ SIMD", Impl: "minio/minlz", Format: FmtMinLZSIMD,
		Module: "github.com/minio/minlz", Kinds: bothKinds, Conc: corpus.KindFile,
		Levels: mzSIMDLevels, Windows: pow2s(4<<10, minlz.MaxBlockSize), DefWindow: fixedWindow(2 << 20),
		New: newMinLZEnc,
	})
	RegisterDec(Dec{
		ID: "mzsimd", Name: "MinLZ SIMD", Impl: "minio/minlz", Format: FmtMinLZSIMD,
		Module: "github.com/minio/minlz", Kinds: bothKinds, Conc: corpus.KindFile,
		New: newMinLZDec,
	})
	RegisterEnc(Enc{
		ID: "mzsimdblk", Name: "MinLZ SIMD block", Impl: "minio/minlz", Format: FmtMinLZSIMDBlk,
		Module: "github.com/minio/minlz", Kinds: corpus.KindDir,
		Levels: mzSIMDLevels, MaxSize: minlz.MaxEncodedLen,
		New: newMzBlkEnc,
	})
	RegisterDec(Dec{
		ID: "mzsimdblk", Name: "MinLZ SIMD block", Impl: "minio/minlz", Format: FmtMinLZSIMDBlk,
		Module: "github.com/minio/minlz", Kinds: corpus.KindDir,
		New: newMzBlkDec,
	})
}

func newMinLZEnc(lvl int, o Opts) (Encoder, error) {
	opts := []minlz.WriterOption{minlz.WriterConcurrency(o.LibThreads(corpus.KindFile))}
	if lvl == minlz.LevelUncompressed {
		opts = append(opts, minlz.WriterUncompressed())
	} else {
		opts = append(opts, minlz.WriterLevel(lvl))
	}
	if o.Window > 0 {
		opts = append(opts, minlz.WriterBlockSize(o.Window))
	}
	w := minlz.NewWriter(nil, opts...)
	return &streamEnc{rst: w.Reset, w: w, fast: w.EncodeBuffer, fin: w.Close}, nil
}

func newMinLZDec(o Opts) (Decoder, error) {
	r := minlz.NewReader(nil, minlz.ReaderFallback(false))
	// DecodeConcurrent is the right entry point at every concurrency
	// here; WriteTo is measurably slower on small inputs.
	n := o.LibThreads(corpus.KindFile)
	return &streamDec{
		rst: nopReset(r.Reset),
		fn:  func(w io.Writer) (int64, error) { return r.DecodeConcurrent(w, n) },
	}, nil
}

func newMzBlkEnc(lvl int, o Opts) (Encoder, error) {
	return blockFn{fn: func(dst, src []byte) ([]byte, error) {
		return minlz.Encode(dst, src, lvl)
	}}, nil
}

func newMzBlkDec(o Opts) (Decoder, error) { return blockFn{fn: minlz.Decode}, nil }
