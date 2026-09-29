package corpus

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Progress reports long running corpus work. Total is 0 when unknown.
type Progress struct {
	Corpus string
	Stage  string // download, extract, read, generate, hash
	Done   int64
	Total  int64
}

// ProgressFn receives progress updates. It may be nil.
type ProgressFn func(Progress)

func (f ProgressFn) report(p Progress) {
	if f != nil {
		f(p)
	}
}

// Fetch makes cp available in the cache: adopting an existing plain copy,
// downloading and extracting, or doing nothing for synthetic corpora.
func Fetch(ctx context.Context, c *Cache, cp Corpus, prog ProgressFn) error {
	if cp.Synth != nil {
		return nil // generated in memory at load time
	}
	if path, adopted, ok := c.Resolve(cp); ok {
		e := c.Entries[cp.ID]
		e.ID, e.Path, e.Adopted = cp.ID, path, adopted
		c.Entries[cp.ID] = e
		return nil
	}
	if cp.Remote == "" {
		return fmt.Errorf("corpus %s is not downloadable: %s", cp.ID, cp.Note)
	}

	archive := c.dlPath(cp.Remote)
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		return err
	}
	etag, err := download(ctx, cp.URL(), archive, c.Entries[cp.ID].ETag, func(done, total int64) {
		prog.report(Progress{Corpus: cp.ID, Stage: "download", Done: done, Total: total})
	})
	if err != nil {
		return fmt.Errorf("downloading %s: %w", cp.URL(), err)
	}

	dst := c.exPath(cp.ID)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := extract(archive, cp, dst, prog); err != nil {
		return fmt.Errorf("extracting %s: %w", cp.Remote, err)
	}

	st, _ := os.Stat(archive)
	e := c.Entries[cp.ID]
	e.ID, e.Path, e.Adopted = cp.ID, dst, false
	e.URL, e.ETag, e.Fetched = cp.URL(), etag, time.Now().UTC().Format(time.RFC3339)
	if st != nil {
		e.DLSize = st.Size()
	}
	c.Entries[cp.ID] = e
	return nil
}

// download fetches url into path, resuming a previous partial transfer when the
// server still reports the same entity.
func download(ctx context.Context, url, path, etag string, prog func(done, total int64)) (string, error) {
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		return etag, nil // already complete
	}
	part := path + ".part"
	var have int64
	if st, err := os.Stat(part); err == nil {
		have = st.Size()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	if have > 0 && etag != "" {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
		req.Header.Set("If-Range", etag)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK:
		have = 0 // server ignored the range, start over
		flags |= os.O_TRUNC
	default:
		return "", fmt.Errorf("unexpected status %s", resp.Status)
	}

	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return "", err
	}
	total := have + resp.ContentLength
	if resp.ContentLength < 0 {
		total = 0
	}
	_, err = io.Copy(f, &progressReader{r: resp.Body, done: have, total: total, fn: prog})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	return resp.Header.Get("ETag"), os.Rename(part, path)
}

type progressReader struct {
	r           io.Reader
	done, total int64
	fn          func(done, total int64)
	last        time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if p.fn != nil && time.Since(p.last) > 200*time.Millisecond {
		p.last = time.Now()
		p.fn(p.done, p.total)
	}
	return n, err
}
