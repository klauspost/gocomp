package codec

import (
	stdbz2 "compress/bzip2"
	"io"
	"strconv"

	"github.com/dsnet/compress/bzip2"
)

const dsnetMod = "github.com/dsnet/compress"

// bzip2 is the one format here the standard library can only read: golang/go#4828
// has been open since 2013, so dsnet/compress is the only way to produce it in
// Go. That asymmetry is also why both decoders are registered -- the stdlib
// reader is what almost everyone actually decodes bzip2 with.
//
// The level is the block size in units of 100 KB, not an effort dial, so it
// costs roughly the same time at every setting and only changes how much data
// the Burrows-Wheeler transform sorts at once.
func init() {
	var lv []Level
	for i := bzip2.BestSpeed; i <= bzip2.BestCompression; i++ {
		name := strconv.Itoa(i)
		if i == bzip2.DefaultCompression {
			name = "default"
		}
		lv = append(lv, Level{N: i, Name: name})
	}

	RegisterEnc(Enc{
		ID: "bz2", Name: "bzip2", Impl: "dsnet/compress", Format: FmtBzip2,
		// Not Slow. It measures ~31 MB/s on a file corpus, which is faster than
		// br level 9 at 12.7 MB/s, and that runs by default; Slow is reserved
		// for xz and lzma2, which are another order of magnitude down.
		Module: dsnetMod, Kinds: bothKinds, Levels: lv,
		New: func(lvl int, o Opts) (Encoder, error) {
			w, err := bzip2.NewWriter(nil, &bzip2.WriterConfig{Level: lvl})
			if err != nil {
				return nil, err
			}
			// Reset only re-inits the bit writer and resizes the block buffer;
			// it returns nil unconditionally, so nothing is being swallowed.
			return &streamEnc{rst: func(dst io.Writer) { _ = w.Reset(dst) }, w: w, fin: w.Close}, nil
		},
	})
	RegisterDec(Dec{
		ID: "bz2", Name: "bzip2", Impl: "dsnet/compress", Format: FmtBzip2,
		Module: dsnetMod, Kinds: bothKinds,
		New: func(o Opts) (Decoder, error) {
			r, err := bzip2.NewReader(nil, nil)
			if err != nil {
				return nil, err
			}
			return &streamDec{rst: r.Reset, fn: copyTo(r)}, nil
		},
	})

	// compress/bzip2 exposes no concrete type, so there is nothing to Reset and
	// a reader is built per call. The allocation lands inside the timed region,
	// which is the honest cost of the API rather than a flaw in the adapter.
	RegisterDec(Dec{
		ID: "bz2std", Name: "bzip2", Impl: "compress/bzip2", Format: FmtBzip2,
		Module: "std", Kinds: bothKinds, With: "bz2",
		New: func(o Opts) (Decoder, error) {
			return blockFn{fn: func(dst, src []byte) ([]byte, error) {
				a := appender{b: dst[:0]}
				_, err := io.Copy(&a, stdbz2.NewReader(bytesReader(src)))
				return a.b, err
			}}, nil
		},
	})
}
