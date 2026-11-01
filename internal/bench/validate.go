package bench

import (
	"fmt"

	"github.com/klauspost/gocomp/internal/codec"
	"github.com/klauspost/gocomp/internal/corpus"
	"github.com/zeebo/xxh3"
)

// compressed holds the input for one source variant's decode measurements. It
// is released before the next variant is produced, which is what keeps peak
// memory at one compressed copy rather than one per variant.
type compressed struct {
	file     []byte
	files    [][]byte
	backing  []byte
	total    int64
	verified int // 1 verified, 0 mismatch, -1 not checked
}

func (c *compressed) release() {
	c.file, c.files, c.backing = nil, nil, nil
}

// compressFor produces the decode input for v, untimed, at Cfg.SrcThreads.
// Fixing the producer's thread count is deliberate: it keeps the decode sweep
// measuring decoder scaling rather than changes in block layout.
func (e *Engine) compressFor(v Variant) (*compressed, error) {
	enc, err := v.Enc.New(v.Level.N, e.opts(e.Cfg.SrcThreads, v.Window))
	if err != nil {
		return nil, err
	}
	defer enc.Release()

	d := e.Data
	c := &compressed{verified: -1}
	if d.Kind == corpus.KindFile {
		out, err := enc.Encode(make([]byte, 0, v.Enc.Bound(len(d.File))), d.File)
		if err != nil {
			return nil, err
		}
		// The encoder may hand back its own buffer; copy so nothing downstream
		// keeps the whole scratch allocation alive.
		c.file = append([]byte(nil), out...)
		c.total = int64(len(c.file))
		return c, nil
	}

	scratch := make([]byte, 0, v.Enc.Bound(d.Max))
	backing := make([]byte, 0, d.Total/2+1<<20)
	offs := make([]int, 0, len(d.Files)+1)
	offs = append(offs, 0)
	for i := range d.Files {
		out, err := enc.Encode(scratch, d.Files[i].Data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.Files[i].Name, err)
		}
		backing = append(backing, out...)
		offs = append(offs, len(backing))
		scratch = out[:0]
	}
	c.backing = backing
	c.files = make([][]byte, len(d.Files))
	for i := range d.Files {
		c.files[i] = backing[offs[i]:offs[i+1]:offs[i+1]]
	}
	c.total = int64(len(backing))
	return c, nil
}

// validate decodes the prepared input with every matching decoder and checks
// the xxh3 of the result against the source. It runs outside every timed region.
func (e *Engine) validate(v Variant, c *compressed, decs []codec.Dec) error {
	c.verified = 1
	for _, dc := range decs {
		inst, err := dc.New(e.opts(e.Cfg.SrcThreads, 0))
		if err != nil {
			c.verified = 0
			return fmt.Errorf("validate %s <- %s: %w", dc.ID, v.Label(), err)
		}
		err = e.checkOne(inst, c)
		inst.Release()
		if err != nil {
			c.verified = 0
			return fmt.Errorf("validate %s <- %s: %w", dc.ID, v.Label(), err)
		}
	}
	return nil
}

func (e *Engine) checkOne(dec codec.Decoder, c *compressed) error {
	d := e.Data
	if d.Kind == corpus.KindFile {
		out, err := dec.Decode(make([]byte, 0, len(d.File)+64<<10), c.file)
		if err != nil {
			return err
		}
		return compare(d.File, out, d.Hash, "corpus")
	}
	dst := make([]byte, 0, d.Max+64<<10)
	for i := range d.Files {
		out, err := dec.Decode(dst, c.files[i])
		if err != nil {
			return fmt.Errorf("%s: %w", d.Files[i].Name, err)
		}
		if err := compare(d.Files[i].Data, out, d.Files[i].Hash, d.Files[i].Name); err != nil {
			return err
		}
		dst = out[:0]
	}
	return nil
}

func compare(want, got []byte, wantHash uint64, name string) error {
	if len(got) != len(want) {
		return fmt.Errorf("%s: decoded %d bytes, want %d", name, len(got), len(want))
	}
	if h := xxh3.Hash(got); h != wantHash {
		return fmt.Errorf("%s: xxh3 %016x, want %016x (first difference at offset %d)",
			name, h, wantHash, firstDiff(want, got))
	}
	return nil
}

func firstDiff(a, b []byte) int {
	for i := range a {
		if a[i] != b[i] {
			return i
		}
	}
	return len(a)
}
