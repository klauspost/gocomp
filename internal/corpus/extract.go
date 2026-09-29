package corpus

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bodgit/sevenzip"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// extract unpacks archive into dst: a plain file for KindFile, a directory for
// KindDir. Exactly one compression container is unwrapped, so a ".tar.zst"
// yields the ".tar" -- which is itself the corpus -- rather than a file tree.
func extract(archive string, cp Corpus, dst string, prog ProgressFn) error {
	report := func(done, total int64) {
		prog.report(Progress{Corpus: cp.ID, Stage: "extract", Done: done, Total: total})
	}
	switch ext := strings.ToLower(filepath.Ext(cp.Remote)); ext {
	case ".zst":
		return streamOut(archive, dst, report, func(r io.Reader) (io.Reader, func(), error) {
			zr, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(0))
			if err != nil {
				return nil, nil, err
			}
			return zr.IOReadCloser(), zr.Close, nil
		})
	case ".xz":
		return streamOut(archive, dst, report, func(r io.Reader) (io.Reader, func(), error) {
			xr, err := xz.NewReader(r)
			return xr, func() {}, err
		})
	case ".zip":
		return extractZip(archive, cp, dst, report)
	case ".7z":
		return extract7z(archive, cp, dst, report)
	default:
		// Already a plain payload, e.g. stacktrace.txt.
		return copyFile(archive, dst)
	}
}

// streamOut decompresses a single-member container into dst.
func streamOut(archive, dst string, report func(done, total int64), open func(io.Reader) (io.Reader, func(), error)) error {
	in, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer in.Close()

	r, closer, err := open(in)
	if err != nil {
		return err
	}
	defer closer()

	out, err := os.Create(dst + ".tmp")
	if err != nil {
		return err
	}
	pw := &progressWriter{w: out, fn: report}
	_, err = io.Copy(pw, r)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dst + ".tmp")
		return err
	}
	return os.Rename(dst+".tmp", dst)
}

func extractZip(archive string, cp Corpus, dst string, report func(done, total int64)) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()

	members := make([]member, 0, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		members = append(members, member{name: f.Name, size: int64(f.UncompressedSize64), open: f.Open})
	}
	return writeMembers(members, cp, dst, report)
}

func extract7z(archive string, cp Corpus, dst string, report func(done, total int64)) error {
	sr, err := sevenzip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer sr.Close()

	members := make([]member, 0, len(sr.File))
	for _, f := range sr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		members = append(members, member{name: f.Name, size: int64(f.UncompressedSize), open: f.Open})
	}
	return writeMembers(members, cp, dst, report)
}

type member struct {
	name string
	size int64
	open func() (io.ReadCloser, error)
}

func writeMembers(members []member, cp Corpus, dst string, report func(done, total int64)) error {
	if len(members) == 0 {
		return fmt.Errorf("archive contains no files")
	}
	if cp.Kind == KindFile {
		m := members[0]
		if cp.Sub != "" || len(members) > 1 {
			idx := -1
			for i, x := range members {
				if cp.Sub != "" && path.Clean(x.name) == path.Clean(cp.Sub) {
					idx = i
					break
				}
			}
			if idx < 0 {
				return fmt.Errorf("archive has %d entries; set Sub to pick one", len(members))
			}
			m = members[idx]
		}
		rc, err := m.open()
		if err != nil {
			return err
		}
		defer rc.Close()
		return writeTo(dst, rc, report)
	}

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	var done int64
	for _, m := range members {
		p, err := safeJoin(dst, m.name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		rc, err := m.open()
		if err != nil {
			return err
		}
		err = writeTo(p, rc, nil)
		rc.Close()
		if err != nil {
			return err
		}
		done += m.size
		report(done, 0)
	}
	return nil
}

// safeJoin rejects entries that would escape root (zip slip).
func safeJoin(root, name string) (string, error) {
	clean := path.Clean("/" + strings.ReplaceAll(name, `\`, "/"))
	if clean == "/" {
		return "", fmt.Errorf("archive entry %q has no name", name)
	}
	p := filepath.Join(root, filepath.FromSlash(clean[1:]))
	if !strings.HasPrefix(p, filepath.Clean(root)+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q escapes the extraction root", name)
	}
	return p, nil
}

func writeTo(dst string, r io.Reader, report func(done, total int64)) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	var w io.Writer = f
	if report != nil {
		w = &progressWriter{w: f, fn: report}
	}
	_, err = io.Copy(w, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeTo(dst, in, nil)
}

type progressWriter struct {
	w    io.Writer
	n    int64
	fn   func(done, total int64)
	next int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.n += int64(n)
	if p.fn != nil && p.n >= p.next {
		p.next = p.n + 32<<20
		p.fn(p.n, 0)
	}
	return n, err
}
