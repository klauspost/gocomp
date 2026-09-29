package corpus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// DefaultCache is used when neither -cache nor COMPRESS_SITE_CACHE is set.
const DefaultCache = "testdata"

// EnvCache overrides the cache directory.
const EnvCache = "COMPRESS_SITE_CACHE"

// CacheDir resolves the cache root.
func CacheDir(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv(EnvCache); v != "" {
		return v
	}
	return DefaultCache
}

// Entry records what the cache knows about one corpus.
type Entry struct {
	ID       string `json:"id"`
	Path     string `json:"path"`    // resolved payload, file or directory
	Adopted  bool   `json:"adopted"` // found under a Local name rather than extracted
	Size     int64  `json:"size"`    // measured uncompressed bytes
	Files    int    `json:"files"`   // measured member count for KindDir
	Hash     string `json:"hash"`    // xxh3 of the assembled corpus
	URL      string `json:"url,omitempty"`
	ETag     string `json:"etag,omitempty"`
	DLSize   int64  `json:"dl_size,omitempty"`
	Fetched  string `json:"fetched,omitempty"`
	Verified string `json:"verified,omitempty"`
}

// Cache is the on-disk index beside the downloaded corpora.
type Cache struct {
	Dir     string
	Entries map[string]Entry
}

func cacheFile(dir string) string { return filepath.Join(dir, "cache.json") }

// OpenCache reads the cache index, returning an empty one if absent.
func OpenCache(dir string) (*Cache, error) {
	c := &Cache{Dir: dir, Entries: map[string]Entry{}}
	b, err := os.ReadFile(cacheFile(dir))
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Entry
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("reading %s: %w", cacheFile(dir), err)
	}
	for _, e := range list {
		c.Entries[e.ID] = e
	}
	return c, nil
}

// Save writes the index atomically.
func (c *Cache) Save() error {
	list := make([]Entry, 0, len(c.Entries))
	for _, e := range c.Entries {
		list = append(list, e)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return err
	}
	return writeFileAtomic(cacheFile(c.Dir), append(b, '\n'))
}

func (c *Cache) dlPath(name string) string { return filepath.Join(c.Dir, "dl", name) }
func (c *Cache) exPath(id string) string   { return filepath.Join(c.Dir, "ex", id) }

// Resolve reports the on-disk payload for cp, preferring an existing plain copy
// under one of its Local names so an existing collection is adopted untouched.
func (c *Cache) Resolve(cp Corpus) (path string, adopted bool, ok bool) {
	want := func(p string) bool {
		st, err := os.Stat(p)
		if err != nil {
			return false
		}
		return st.IsDir() == (cp.Kind == KindDir)
	}
	for _, name := range cp.Local {
		if p := filepath.Join(c.Dir, name); want(p) {
			return p, true, true
		}
	}
	if p := c.exPath(cp.ID); want(p) {
		return p, false, true
	}
	return "", false, false
}

func writeFileAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
