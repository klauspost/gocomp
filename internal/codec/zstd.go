package codec

import (
	"strconv"

	"github.com/klauspost/compress-site/internal/corpus"
	"github.com/klauspost/compress/zstd"
	stdzstd "github.com/klauspost/stdgozstd"
)

var zstdLevels = []Level{
	{int(zstd.SpeedFastest), "fastest", false},
	{int(zstd.SpeedDefault), "default", false},
	{int(zstd.SpeedBetterCompression), "better", false},
	{int(zstd.SpeedBestCompression), "best", false},
}

func init() {
	RegisterEnc(Enc{
		ID: "zskp", Name: "Zstandard", Impl: "klauspost/compress", Format: FmtZstd,
		Module: kpCompress, Kinds: bothKinds,
		// EncodeAll, used for the per-file path, is single-goroutine; only the
		// stream path with concurrent blocks parallelises.
		Conc: corpus.KindFile, Levels: zstdLevels,
		New: func(lvl int, o Opts) (Encoder, error) {
			// WithConcurrentBlocks cannot be changed on Reset, so it is fixed here.
			concurrent := o.Kind == corpus.KindFile && o.Threads > 1
			e, err := zstd.NewWriter(nil,
				zstd.WithEncoderLevel(zstd.EncoderLevel(lvl)),
				zstd.WithEncoderConcurrency(o.LibThreads(corpus.KindFile)),
				zstd.WithConcurrentBlocks(concurrent))
			if err != nil {
				return nil, err
			}
			if o.Kind == corpus.KindDir {
				return blockFn{
					fn:  func(dst, src []byte) ([]byte, error) { return e.EncodeAll(src, dst[:0]), nil },
					rel: func() { _ = e.Close() },
				}, nil
			}
			return &streamEnc{rst: e.Reset, w: e, fin: e.Close, rel: func() { _ = e.Close() }}, nil
		},
	})
	RegisterDec(Dec{
		ID: "zskp", Name: "Zstandard", Impl: "klauspost/compress", Format: FmtZstd,
		Module: kpCompress, Kinds: bothKinds, Conc: corpus.KindFile,
		New: func(o Opts) (Decoder, error) {
			d, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(o.LibThreads(corpus.KindFile)))
			if err != nil {
				return nil, err
			}
			if o.Kind == corpus.KindDir {
				return blockFn{
					fn:  func(dst, src []byte) ([]byte, error) { return d.DecodeAll(src, dst[:0]) },
					rel: d.Close,
				}, nil
			}
			return &streamDec{rst: d.Reset, fn: d.WriteTo, rel: d.Close}, nil
		},
	})

	// stdgozstd is the candidate standard-library implementation. Level -1 is
	// omitted: it is an alias for 3 and would duplicate that row.
	var stdLevels []Level
	for i := 0; i <= 9; i++ {
		name := strconv.Itoa(i)
		switch i {
		case 0:
			name = "store"
		case 3:
			name = "default"
		}
		stdLevels = append(stdLevels, Level{i, name, false})
	}
	RegisterEnc(Enc{
		ID: "stdzstd", Name: "Zstandard", Impl: "klauspost/stdgozstd", Format: FmtZstd,
		Module: "github.com/klauspost/stdgozstd", Kinds: bothKinds, Levels: stdLevels,
		New: func(lvl int, o Opts) (Encoder, error) {
			w := stdzstd.NewWriter(nil)
			if err := w.SetLevel(lvl); err != nil {
				return nil, err
			}
			if o.Kind == corpus.KindDir {
				return blockFn{fn: func(dst, src []byte) ([]byte, error) {
					return w.AppendCompress(dst[:0], src), nil
				}}, nil
			}
			return &streamEnc{rst: w.Reset, w: w, fin: w.Close}, nil
		},
	})
	RegisterDec(Dec{
		ID: "stdzstd", Name: "Zstandard", Impl: "klauspost/stdgozstd", Format: FmtZstd,
		Module: "github.com/klauspost/stdgozstd", Kinds: bothKinds,
		New: func(o Opts) (Decoder, error) {
			r := stdzstd.NewReader(nil)
			if o.Kind == corpus.KindDir {
				return blockFn{fn: func(dst, src []byte) ([]byte, error) {
					return r.AppendDecompress(dst[:0], src)
				}}, nil
			}
			return &streamDec{rst: r.Reset, fn: r.WriteTo, rel: func() { _ = r.Close() }}, nil
		},
	})
}
