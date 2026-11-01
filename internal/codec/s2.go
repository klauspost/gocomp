package codec

import (
	"io"

	"github.com/klauspost/compress/s2"
	"github.com/klauspost/gocomp/internal/corpus"
)

var (
	s2Levels = []Level{
		{N: 0, Name: "uncompressed", Class: Uncompressed},
		{N: 1, Name: "default"},
		{N: 2, Name: "better"},
		{N: 3, Name: "best"},
	}
	s2BlkLevels = s2Levels[1:]
)

func s2Opts(lvl int) []s2.WriterOption {
	switch lvl {
	case 0:
		return []s2.WriterOption{s2.WriterUncompressed()}
	case 2:
		return []s2.WriterOption{s2.WriterBetterCompression()}
	case 3:
		return []s2.WriterOption{s2.WriterBestCompression()}
	}
	return nil
}

func s2BlockEnc(lvl int) func(dst, src []byte) ([]byte, error) {
	fn := map[int]func(dst, src []byte) []byte{
		1: s2.Encode, 2: s2.EncodeBetter, 3: s2.EncodeBest,
	}[lvl]
	return func(dst, src []byte) ([]byte, error) { return fn(dst, src), nil }
}

func s2SnappyBlockEnc(lvl int) func(dst, src []byte) ([]byte, error) {
	fn := map[int]func(dst, src []byte) []byte{
		1: s2.EncodeSnappy, 2: s2.EncodeSnappyBetter, 3: s2.EncodeSnappyBest,
	}[lvl]
	return func(dst, src []byte) ([]byte, error) { return fn(dst, src), nil }
}

// s2Decode picks the sequential reader unless concurrency actually applies.
// DecodeConcurrent still builds its pipeline at concurrency 1, which on a
// directory of small files costs more than the decode itself.
func s2Decode(r *s2.Reader, o Opts) func(io.Writer) (int64, error) {
	n := o.LibThreads(corpus.KindFile)
	if n <= 1 {
		return copyTo(r)
	}
	return func(w io.Writer) (int64, error) { return r.DecodeConcurrent(w, n) }
}

// newS2Writer builds the stream encoder shared by the s2 and snappy-compat frames.
// The block size goes before extra so that WriterSnappyCompat still clamps the
// library's 1MB default down to the 64KB snappy frames allow.
func newS2Writer(lvl int, o Opts, extra ...s2.WriterOption) Encoder {
	opts := append(s2Opts(lvl), s2.WriterConcurrency(o.LibThreads(corpus.KindFile)))
	if o.Window > 0 {
		opts = append(opts, s2.WriterBlockSize(o.Window))
	}
	w := s2.NewWriter(nil, append(opts, extra...)...)
	// EncodeBuffer is the concurrent path; a plain Write is sequential.
	return &streamEnc{rst: w.Reset, w: w, fast: w.EncodeBuffer, fin: w.Close}
}

func init() {
	RegisterEnc(Enc{
		ID: "s2", Name: "S2", Impl: "klauspost/compress", Format: FmtS2,
		Module: kpCompress, Kinds: bothKinds, Conc: corpus.KindFile, Levels: s2Levels,
		Windows: pow2s(4<<10, 4<<20), DefWindow: fixedWindow(1 << 20),
		New: func(lvl int, o Opts) (Encoder, error) { return newS2Writer(lvl, o), nil },
	})
	RegisterDec(Dec{
		ID: "s2", Name: "S2", Impl: "klauspost/compress", Format: FmtS2,
		Module: kpCompress, Kinds: bothKinds, Conc: corpus.KindFile,
		New: func(o Opts) (Decoder, error) {
			r := s2.NewReader(nil)
			return &streamDec{rst: nopReset(r.Reset), fn: s2Decode(r, o)}, nil
		},
	})

	RegisterEnc(Enc{
		ID: "s2s", Name: "S2 (snappy frame)", Impl: "klauspost/compress", Format: FmtSnappy,
		Module: kpCompress, Kinds: bothKinds, Conc: corpus.KindFile, Levels: s2Levels,
		Windows: pow2s(4<<10, 64<<10), // snappy frames cap blocks at 64KB
		// WriterSnappyCompat's clamp of the 1MB default, which no sweep size equals.
		DefWindow: fixedWindow(64<<10 - 8),
		New: func(lvl int, o Opts) (Encoder, error) {
			return newS2Writer(lvl, o, s2.WriterSnappyCompat()), nil
		},
	})
	RegisterDec(Dec{
		ID: "s2sn", Name: "S2 reading snappy", Impl: "klauspost/compress", Format: FmtSnappy,
		Module: kpCompress, Kinds: bothKinds, Conc: corpus.KindFile, With: "s2s",
		New: func(o Opts) (Decoder, error) {
			r := s2.NewReader(nil)
			return &streamDec{rst: nopReset(r.Reset), fn: s2Decode(r, o)}, nil
		},
	})

	// Block variants are directory-only: a single 10GB block is neither a
	// realistic use nor decodable without buffering the whole thing twice.
	RegisterEnc(Enc{
		ID: "s2blk", Name: "S2 block", Impl: "klauspost/compress", Format: FmtS2Block,
		Module: kpCompress, Kinds: corpus.KindDir, Levels: s2BlkLevels, MaxSize: s2.MaxEncodedLen,
		New: func(lvl int, o Opts) (Encoder, error) { return blockFn{fn: s2BlockEnc(lvl)}, nil },
	})
	RegisterDec(Dec{
		ID: "s2blk", Name: "S2 block", Impl: "klauspost/compress", Format: FmtS2Block,
		Module: kpCompress, Kinds: corpus.KindDir,
		New: func(o Opts) (Decoder, error) { return blockFn{fn: s2.Decode}, nil },
	})

	RegisterEnc(Enc{
		ID: "s2sblk", Name: "S2 snappy block", Impl: "klauspost/compress", Format: FmtSnappyBlk,
		Module: kpCompress, Kinds: corpus.KindDir, Levels: s2BlkLevels, MaxSize: s2.MaxEncodedLen,
		New: func(lvl int, o Opts) (Encoder, error) { return blockFn{fn: s2SnappyBlockEnc(lvl)}, nil },
	})
	RegisterDec(Dec{
		ID: "s2sblk", Name: "S2 reading snappy block", Impl: "klauspost/compress", Format: FmtSnappyBlk,
		Module: kpCompress, Kinds: corpus.KindDir,
		New: func(o Opts) (Decoder, error) { return blockFn{fn: s2.Decode}, nil },
	})
}
