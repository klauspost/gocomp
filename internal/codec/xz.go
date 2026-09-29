package codec

import (
	"io"

	"github.com/ulikunitz/xz"
	"github.com/ulikunitz/xz/lzma"
)

const xzMod = "github.com/ulikunitz/xz"

// xz and lzma2 have no Reset, so a writer is built per call. They are Slow and
// opt-in, and the construction cost is negligible against their run time.
func init() {
	RegisterEnc(Enc{
		ID: "xz", Name: "xz", Impl: "ulikunitz/xz", Format: FmtXZ,
		Module: xzMod, Kinds: bothKinds, Slow: true,
		New: func(lvl int, o Opts) (Encoder, error) {
			return blockFn{fn: func(dst, src []byte) ([]byte, error) {
				a := appender{b: dst[:0]}
				w, err := xz.NewWriter(&a)
				if err != nil {
					return nil, err
				}
				if _, err := w.Write(src); err != nil {
					return nil, err
				}
				if err := w.Close(); err != nil {
					return nil, err
				}
				return a.b, nil
			}}, nil
		},
	})
	RegisterDec(Dec{
		ID: "xz", Name: "xz", Impl: "ulikunitz/xz", Format: FmtXZ,
		Module: xzMod, Kinds: bothKinds, Slow: true,
		New: func(o Opts) (Decoder, error) {
			return blockFn{fn: func(dst, src []byte) ([]byte, error) {
				r, err := xz.NewReader(bytesReader(src))
				if err != nil {
					return nil, err
				}
				a := appender{b: dst[:0]}
				_, err = io.Copy(&a, r)
				return a.b, err
			}}, nil
		},
	})

	RegisterEnc(Enc{
		ID: "lzma2", Name: "LZMA2", Impl: "ulikunitz/xz", Format: FmtLZMA2,
		Module: xzMod, Kinds: bothKinds, Slow: true,
		New: func(lvl int, o Opts) (Encoder, error) {
			return blockFn{fn: func(dst, src []byte) ([]byte, error) {
				a := appender{b: dst[:0]}
				w, err := lzma.Writer2Config{}.NewWriter2(&a)
				if err != nil {
					return nil, err
				}
				if _, err := w.Write(src); err != nil {
					return nil, err
				}
				if err := w.Close(); err != nil {
					return nil, err
				}
				return a.b, nil
			}}, nil
		},
	})
	RegisterDec(Dec{
		ID: "lzma2", Name: "LZMA2", Impl: "ulikunitz/xz", Format: FmtLZMA2,
		Module: xzMod, Kinds: bothKinds, Slow: true,
		New: func(o Opts) (Decoder, error) {
			return blockFn{fn: func(dst, src []byte) ([]byte, error) {
				r, err := lzma.NewReader2(bytesReader(src))
				if err != nil {
					return nil, err
				}
				a := appender{b: dst[:0]}
				_, err = io.Copy(&a, r)
				return a.b, err
			}}, nil
		},
	})
}
