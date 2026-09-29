package codec

import (
	flstd "compress/flate"
	"errors"
	"io"

	flkp "github.com/klauspost/compress/flate"
)

const (
	kpCompress = "github.com/klauspost/compress"
	// statelessLevel is the klauspost-only "no history" mode.
	statelessLevel = -3
)

func init() {
	RegisterEnc(Enc{
		ID: "flstd", Name: "deflate", Impl: "compress/flate", Format: FmtDeflate, Module: "std",
		Kinds: bothKinds, Levels: deflateLevels(false, 6), // compress/flate
		New: func(lvl int, o Opts) (Encoder, error) {
			w, err := flstd.NewWriter(nil, lvl)
			if err != nil {
				return nil, err
			}
			return &streamEnc{rst: w.Reset, w: w, fin: w.Close}, nil
		},
	})
	RegisterDec(Dec{
		ID: "flstd", Name: "deflate", Impl: "compress/flate", Format: FmtDeflate, Module: "std",
		Kinds: bothKinds,
		New: func(o Opts) (Decoder, error) {
			r := flstd.NewReader(nil)
			rs, ok := r.(flstd.Resetter)
			if !ok {
				return nil, errors.New("compress/flate reader is not a Resetter")
			}
			return &streamDec{
				rst: func(in io.Reader) error { return rs.Reset(in, nil) },
				fn:  copyTo(r),
				rel: func() { _ = r.Close() },
			}, nil
		},
	})

	RegisterEnc(Enc{
		ID: "flkp", Name: "deflate", Impl: "klauspost/compress", Format: FmtDeflate,
		Module: kpCompress, Kinds: bothKinds, Levels: deflateLevels(true, 5),
		New: func(lvl int, o Opts) (Encoder, error) {
			if lvl == statelessLevel {
				// StatelessDeflate is the one-shot form; NewStatelessWriter has no
				// Reset and would allocate inside the timed region.
				return blockFn{fn: func(dst, src []byte) ([]byte, error) {
					a := appender{b: dst[:0]}
					err := flkp.StatelessDeflate(&a, src, true, nil)
					return a.b, err
				}}, nil
			}
			w, err := flkp.NewWriter(nil, lvl)
			if err != nil {
				return nil, err
			}
			return &streamEnc{rst: w.Reset, w: w, fin: w.Close}, nil
		},
	})
	RegisterDec(Dec{
		ID: "flkp", Name: "deflate", Impl: "klauspost/compress", Format: FmtDeflate,
		Module: kpCompress, Kinds: bothKinds,
		New: func(o Opts) (Decoder, error) {
			r := flkp.NewReader(nil)
			rs, ok := r.(flkp.Resetter)
			if !ok {
				return nil, errors.New("klauspost/compress flate reader is not a Resetter")
			}
			return &streamDec{
				rst: func(in io.Reader) error { return rs.Reset(in, nil) },
				fn:  copyTo(r),
				rel: func() { _ = r.Close() },
			}, nil
		},
	})
}
