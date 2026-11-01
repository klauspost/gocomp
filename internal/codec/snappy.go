package codec

import (
	"github.com/golang/snappy"
	"github.com/klauspost/gocomp/internal/corpus"
)

const snappyMod = "github.com/golang/snappy"

// klauspost/compress/snappy is deliberately absent: it is the s2 encoder behind
// a snappy API, so it would duplicate the s2s and s2sblk entries.
func init() {
	RegisterEnc(Enc{
		ID: "snappy", Name: "Snappy", Impl: "golang/snappy", Format: FmtSnappy,
		Module: snappyMod, Kinds: bothKinds,
		New: func(lvl int, o Opts) (Encoder, error) {
			w := snappy.NewBufferedWriter(nil)
			return &streamEnc{rst: w.Reset, w: w, fin: w.Close}, nil
		},
	})
	RegisterDec(Dec{
		ID: "snappy", Name: "Snappy", Impl: "golang/snappy", Format: FmtSnappy,
		Module: snappyMod, Kinds: bothKinds,
		New: func(o Opts) (Decoder, error) {
			r := snappy.NewReader(nil)
			return &streamDec{rst: nopReset(r.Reset), fn: copyTo(r)}, nil
		},
	})

	RegisterEnc(Enc{
		ID: "snapblk", Name: "Snappy block", Impl: "golang/snappy", Format: FmtSnappyBlk,
		Module: snappyMod, Kinds: corpus.KindDir, MaxSize: snappy.MaxEncodedLen,
		New: func(lvl int, o Opts) (Encoder, error) {
			return blockFn{fn: func(dst, src []byte) ([]byte, error) {
				return snappy.Encode(dst, src), nil
			}}, nil
		},
	})
	RegisterDec(Dec{
		ID: "snapblk", Name: "Snappy block", Impl: "golang/snappy", Format: FmtSnappyBlk,
		Module: snappyMod, Kinds: corpus.KindDir,
		New: func(o Opts) (Decoder, error) { return blockFn{fn: snappy.Decode}, nil },
	})
}
