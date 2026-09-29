package corpus

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/zeebo/xxh3"
)

// A .zst download is decoded straight into memory, whatever the size guess:
// every wrong guess must still yield the stream's exact bytes.
func TestLoadDecodesZstDownload(t *testing.T) {
	src := make([]byte, 3<<20)
	Rand(src[:1<<20])
	for i := 1 << 20; i < len(src); i++ {
		src[i] = src[i%4096]
	}
	n := int64(len(src))

	// The stream writer does not know the size up front, so it writes no
	// content size; EncodeAll does.
	var stream bytes.Buffer
	w, err := zstd.NewWriter(&stream)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(src); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	enc, _ := zstd.NewWriter(nil)
	withSize := enc.EncodeAll(src, nil)

	for _, tc := range []struct {
		name  string
		file  []byte
		known int64
	}{
		{"exact", stream.Bytes(), n},
		{"short guess", stream.Bytes(), n / 3},
		{"long guess", stream.Bytes(), n * 2},
		{"no size anywhere", stream.Bytes(), 0},
		{"size from the frame header", withSize, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			dl := filepath.Join(dir, "dl", "t.zst")
			if err := os.MkdirAll(filepath.Dir(dl), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dl, tc.file, 0o644); err != nil {
				t.Fatal(err)
			}
			cp := Corpus{ID: "t", Kind: KindFile, Remote: "t.zst", Size: tc.known}
			c := &Cache{Dir: dir, Entries: map[string]Entry{}}

			// The download is present, so fetching must neither reach the
			// network nor write an extracted copy.
			if err := Fetch(context.Background(), c, cp, nil); err != nil {
				t.Fatal(err)
			}
			d, err := Load(context.Background(), c, cp, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(d.File, src) || d.Hash != xxh3.Hash(src) || d.Max != len(src) {
				t.Fatalf("decoded %d bytes, want %d identical ones", len(d.File), len(src))
			}
			if _, err := os.Stat(filepath.Join(dir, "ex")); !os.IsNotExist(err) {
				t.Errorf("an extracted copy was written: %v", err)
			}
			if got := c.Entries["t"].Path; got != dl {
				t.Errorf("entry path = %s, want the download %s", got, dl)
			}
		})
	}
}
