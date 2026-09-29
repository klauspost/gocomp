package codec

import (
	gzstd "compress/gzip"
	"io"
	"strconv"

	"github.com/klauspost/compress-site/internal/corpus"
	gzkp "github.com/klauspost/compress/gzip"
	pgz "github.com/klauspost/pgzip"
)

const bothKinds = corpus.KindFile | corpus.KindDir

// deflateLevels are the levels shared by the gzip/flate family. Stateless (-3)
// only exists in the klauspost implementations.
//
// DefaultCompression (-1) is deliberately absent. It is not a level, it is an
// alias the writer resolves before compressing anything, so selecting "all"
// measured one numbered level twice under two names. The numbered level wears
// the name instead -- the same choice stdzstd already makes for its own -1.
// def is a parameter because the alias does not resolve to the same place:
// compress/flate maps it to 6, klauspost/compress/flate to 5.
func deflateLevels(stateless bool, def int) []Level {
	var lv []Level
	if stateless {
		lv = append(lv, Level{-3, "stateless", false})
	}
	lv = append(lv, Level{-2, "huffman", false}, Level{0, "store", false})
	for i := 1; i <= 9; i++ {
		name := strconv.Itoa(i)
		if i == def {
			name = "default"
		}
		lv = append(lv, Level{i, name, false})
	}
	return lv
}

func init() {
	RegisterEnc(Enc{
		ID: "gzstd", Name: "gzip", Impl: "compress/gzip", Format: FmtGzip, Module: "std",
		Kinds: bothKinds, Levels: deflateLevels(false, 6), // compress/flate
		New: func(lvl int, o Opts) (Encoder, error) {
			w, err := gzstd.NewWriterLevel(nil, lvl)
			if err != nil {
				return nil, err
			}
			return &streamEnc{rst: w.Reset, w: w, fin: w.Close}, nil
		},
	})
	RegisterDec(Dec{
		ID: "gzstd", Name: "gzip", Impl: "compress/gzip", Format: FmtGzip, Module: "std",
		Kinds: bothKinds,
		New: func(o Opts) (Decoder, error) {
			r := new(gzstd.Reader)
			return &streamDec{rst: r.Reset, fn: copyTo(r)}, nil
		},
	})

	RegisterEnc(Enc{
		ID: "gzkp", Name: "gzip", Impl: "klauspost/compress", Format: FmtGzip,
		Module: kpCompress, Kinds: bothKinds, Levels: deflateLevels(true, 5),
		New: func(lvl int, o Opts) (Encoder, error) {
			w, err := gzkp.NewWriterLevel(nil, lvl)
			if err != nil {
				return nil, err
			}
			return &streamEnc{rst: w.Reset, w: w, fin: w.Close}, nil
		},
	})
	RegisterDec(Dec{
		ID: "gzkp", Name: "gzip", Impl: "klauspost/compress", Format: FmtGzip,
		Module: kpCompress, Kinds: bothKinds,
		New: func(o Opts) (Decoder, error) {
			r := new(gzkp.Reader)
			return &streamDec{rst: r.Reset, fn: r.WriteTo}, nil
		},
	})

	RegisterEnc(Enc{
		ID: "pgzip", Name: "gzip (parallel)", Impl: "klauspost/pgzip", Format: FmtGzip,
		Module: "github.com/klauspost/pgzip", Kinds: bothKinds,
		// Blocks are only split within a single stream, so many small files
		// give it nothing to parallelise.
		Conc: corpus.KindFile, Levels: deflateLevels(false, 5), // klauspost/compress/flate
		New: func(lvl int, o Opts) (Encoder, error) {
			w, err := pgz.NewWriterLevel(nil, lvl)
			if err != nil {
				return nil, err
			}
			// SetConcurrency rejects a block size at or below pgzip's 16KB tail,
			// and swallowing that error leaves concurrency at Reset's default.
			bs := max(o.BlockSize, 64<<10)
			// Reset resets concurrency to its own defaults, so re-apply every time.
			rst := func(out io.Writer) {
				w.Reset(out)
				_ = w.SetConcurrency(bs, o.LibThreads(corpus.KindFile))
			}
			return &streamEnc{rst: rst, w: w, fin: w.Close}, nil
		},
	})
	RegisterDec(Dec{
		ID: "pgzip", Name: "gzip (parallel)", Impl: "klauspost/pgzip", Format: FmtGzip,
		Module: "github.com/klauspost/pgzip", Kinds: bothKinds,
		// Read-ahead only overlaps I/O; inflate of a single member stays serial.
		New: func(o Opts) (Decoder, error) {
			r := new(pgz.Reader)
			return &streamDec{rst: r.Reset, fn: r.WriteTo, rel: func() { _ = r.Close() }}, nil
		},
	})
}
