package codec

import (
	"strconv"

	"github.com/klauspost/compress-site/internal/corpus"
	"github.com/klauspost/compress/zstd"
	stdzstd "github.com/klauspost/stdgozstd"
)

var zstdLevels = []Level{
	{N: int(zstd.SpeedFastest), Name: "fastest"},
	{N: int(zstd.SpeedDefault), Name: "default"},
	{N: int(zstd.SpeedBetterCompression), Name: "better"},
	{N: int(zstd.SpeedBestCompression), Name: "best"},
}

func init() {
	RegisterEnc(Enc{
		ID: "zskp", Name: "Zstandard", Impl: "klauspost/compress", Format: FmtZstd,
		Module: kpCompress, Kinds: bothKinds,
		// EncodeAll, used for the per-file path, is single-goroutine; only the
		// stream path with concurrent blocks parallelises.
		Conc: corpus.KindFile, Levels: zstdLevels,
		Windows: pow2s(zstd.MinWindowSize, zstd.MaxWindowSize),
		DefWindow: func(lvl int) int {
			if lvl == int(zstd.SpeedFastest) {
				return 4 << 20
			}
			return 8 << 20
		},
		New: func(lvl int, o Opts) (Encoder, error) {
			// WithConcurrentBlocks cannot be changed on Reset, so it is fixed here.
			concurrent := o.Kind == corpus.KindFile && o.Threads > 1
			opts := []zstd.EOption{
				zstd.WithEncoderLevel(zstd.EncoderLevel(lvl)),
				zstd.WithEncoderConcurrency(o.LibThreads(corpus.KindFile)),
				zstd.WithConcurrentBlocks(concurrent),
			}
			if o.Window > 0 {
				opts = append(opts, zstd.WithWindowSize(o.Window))
			}
			e, err := zstd.NewWriter(nil, opts...)
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
	stdLevels := []Level{{N: 0, Name: "store", Class: Uncompressed}}
	for i := 1; i <= 9; i++ {
		name := strconv.Itoa(i)
		if i == 3 {
			name = "default"
		}
		stdLevels = append(stdLevels, Level{N: i, Name: name})
	}
	RegisterEnc(Enc{
		ID: "stdzstd", Name: "Zstandard", Impl: "klauspost/stdgozstd", Format: FmtZstd,
		Module: "github.com/klauspost/stdgozstd", Kinds: bothKinds, Levels: stdLevels,
		// Not from MinWindowSize: SetWindowSize leaves the block size at 128KB,
		// and a block larger than the window is invalid, so below 128KB both
		// decoders reject the frame ("window size exceeded").
		Windows: pow2s(128<<10, stdzstd.MaxWindowSize),
		// SetLevel's own table; level 0 stores and has no window.
		DefWindow: func(lvl int) int { return []int{0, 2, 3, 4, 4, 4, 5, 6, 7, 8}[lvl] << 20 },
		New: func(lvl int, o Opts) (Encoder, error) {
			w := stdzstd.NewWriter(nil)
			if err := w.SetLevel(lvl); err != nil {
				return nil, err
			}
			// After SetLevel, which resets the window to the level's own.
			if o.Window > 0 {
				if err := w.SetWindowSize(o.Window); err != nil {
					return nil, err
				}
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
			// The default cap is 128MB, below the largest window either zstd
			// encoder can be asked for. It is a limit, not a tuning knob.
			if err := r.SetMaxWindowSize(stdzstd.MaxWindowSize); err != nil {
				return nil, err
			}
			if o.Kind == corpus.KindDir {
				return blockFn{fn: func(dst, src []byte) ([]byte, error) {
					return r.AppendDecompress(dst[:0], src)
				}}, nil
			}
			return &streamDec{rst: r.Reset, fn: r.WriteTo, rel: func() { _ = r.Close() }}, nil
		},
	})
}
