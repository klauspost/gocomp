package corpus

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/zeebo/xxh3"
)

// Load brings cp fully into memory. Synthetic corpora are generated rather than
// read; everything else must already be in the cache (see Fetch).
func Load(ctx context.Context, c *Cache, cp Corpus, prog ProgressFn) (*Data, error) {
	if cp.Synth != nil {
		return loadSynth(cp, prog), nil
	}
	path, adopted, ok := c.Resolve(cp)
	if !ok {
		if cp.Remote == "" {
			return nil, fmt.Errorf("corpus %s is not present: %s", cp.ID, cp.Note)
		}
		return nil, fmt.Errorf("corpus %s is not cached; run: gocomp corpus fetch %s", cp.ID, cp.ID)
	}

	var (
		d   *Data
		err error
	)
	e := c.Entries[cp.ID]
	if cp.Kind == KindDir {
		d, err = loadDir(ctx, cp, path, prog)
	} else {
		d, err = loadFile(cp, path, max(e.Size, cp.Size), prog)
	}
	if err != nil {
		return nil, err
	}

	e.ID, e.Path, e.Adopted = cp.ID, path, adopted
	e.Size, e.Files, e.Hash = d.Total, len(d.Files), strconv.FormatUint(d.Hash, 16)
	if cp.Kind == KindFile {
		e.Files = 1
	}
	e.Verified = time.Now().UTC().Format(time.RFC3339)
	c.Entries[cp.ID] = e
	return d, nil
}

func loadSynth(cp Corpus, prog ProgressFn) *Data {
	prog.report(Progress{Corpus: cp.ID, Stage: "generate", Total: cp.Size})
	buf := make([]byte, cp.Size)
	cp.Synth(buf)
	return &Data{
		Corpus: cp, File: buf, Total: int64(len(buf)),
		Hash: xxh3.Hash(buf), Max: len(buf),
	}
}

// loadFile reads a plain payload, or decodes a zstd download straight into
// memory. known is the uncompressed size when the catalog or an earlier load
// has measured it, else 0.
func loadFile(cp Corpus, path string, known int64, prog ProgressFn) (*Data, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var buf []byte
	if strings.EqualFold(filepath.Ext(path), ".zst") {
		buf, err = decodeZst(cp, f, known, prog)
	} else {
		var st os.FileInfo
		if st, err = f.Stat(); err == nil {
			prog.report(Progress{Corpus: cp.ID, Stage: "read", Total: st.Size()})
			buf = make([]byte, st.Size())
			_, err = io.ReadFull(f, buf)
		}
	}
	if err != nil {
		return nil, err
	}
	cp.Size, cp.Files = int64(len(buf)), 1
	return &Data{
		Corpus: cp, File: buf, Total: int64(len(buf)),
		Hash: xxh3.Hash(buf), Max: len(buf),
	}, nil
}

// decodeZst sizes the buffer from known, else from the frame header. Either can
// be wrong -- a stale catalog entry, a first frame of several -- and that costs
// a copy at the end, never a wrong corpus: the data is whatever the stream
// holds, and the hash is taken from it.
func decodeZst(cp Corpus, f *os.File, known int64, prog ProgressFn) ([]byte, error) {
	n := known
	if n <= 0 {
		var h zstd.Header
		hdr := make([]byte, zstd.HeaderMaxSize)
		k, _ := f.ReadAt(hdr, 0)
		if h.Decode(hdr[:k]) == nil && h.HasFCS {
			n = int64(h.FrameContentSize)
		}
	}
	zr, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(0))
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	r := &progressReader{r: zr, total: n, fn: func(done, total int64) {
		prog.report(Progress{Corpus: cp.ID, Stage: "decompress", Done: done, Total: total})
	}}
	buf := make([]byte, n)
	k, err := io.ReadFull(r, buf)
	switch err {
	case nil:
		rest, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		return append(buf, rest...), nil
	case io.EOF, io.ErrUnexpectedEOF:
		return buf[:k], nil
	}
	return nil, err
}

func loadDir(ctx context.Context, cp Corpus, root string, prog ProgressFn) (*Data, error) {
	type ent struct {
		rel  string
		size int64
	}
	var ents []ent
	var total int64
	err := filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.IsDir() {
			return ctx.Err()
		}
		fi, err := de.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		ents = append(ents, ent{filepath.ToSlash(rel), fi.Size()})
		total += fi.Size()
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(ents) == 0 {
		return nil, fmt.Errorf("corpus %s: no files under %s", cp.ID, root)
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].rel < ents[j].rel })
	prog.report(Progress{Corpus: cp.ID, Stage: "read", Total: total})

	// One backing allocation for every member: 50k small files would otherwise
	// be 50k allocations, and locality matters once they are the hot data.
	backing := make([]byte, total)
	files := make([]File, 0, len(ents))
	h := xxh3.New()
	var off int64
	var largest int
	for i, e := range ents {
		if i%512 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			prog.report(Progress{Corpus: cp.ID, Stage: "read", Done: off, Total: total})
		}
		buf := backing[off : off+e.size : off+e.size]
		f, err := os.Open(filepath.Join(root, filepath.FromSlash(e.rel)))
		if err != nil {
			return nil, err
		}
		_, err = io.ReadFull(f, buf)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", e.rel, err)
		}
		off += e.size
		largest = max(largest, int(e.size))
		_, _ = h.WriteString(e.rel)
		_, _ = h.Write(buf)
		files = append(files, File{Name: e.rel, Data: buf, Hash: xxh3.Hash(buf)})
	}

	cp.Size, cp.Files = total, len(files)
	return &Data{Corpus: cp, Files: files, Total: total, Hash: h.Sum64(), Max: largest}, nil
}
