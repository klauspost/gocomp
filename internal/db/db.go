// Package db stores benchmark runs as JSON under web/data, in the shape the
// static site consumes.
package db

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/klauspost/gocomp/internal/sysinfo"
	"github.com/zeebo/xxh3"
)

// Schema is bumped when the on-disk shape changes incompatibly.
const Schema = 3

// IndexFile is the manifest the site always fetches.
const IndexFile = "index.json"

// EncRow is one compression measurement. ns is the best of Passes runs.
type EncRow struct {
	Codec    string
	Level    int
	Window   int // 0 is the codec's default
	Threads  int
	Lib      int // index into Run.Libs
	In, Out  int64
	NS       int64
	Passes   int
	SD       float64 // relative stddev; recorded, not displayed
	Verified int     // 1 verified, 0 failed, -1 not checked
	Pin      int     // 1 pinned to one logical CPU per core first, 0 placed by the OS, -1 not recorded; not displayed
}

// DecRow is one decompression measurement. Codec is the decoder; Src names the
// encoder variant that produced the input, because the encoder's settings move
// decode speed as much as the decoder does.
type DecRow struct {
	Codec  string
	Src    string // "<encoder>-<level>"
	Window int    // the window Src was encoded with
	SrcLib int
	// SrcThreads is the thread count Src was encoded at, 0 where its bytes do
	// not depend on it. It lives here rather than on the run because it moves
	// only these rows: gating the run on it split every encode row as well.
	SrcThreads int
	Threads    int
	Lib        int
	In, Out    int64
	NS         int64
	Passes     int
	SD         float64
	Verified   int
	Pin        int
}

// RunError records a measurement that could not complete.
type RunError struct {
	Op      string `json:"op"`
	Codec   string `json:"codec"`
	Level   int    `json:"level"`
	Window  int    `json:"window,omitempty"`
	Threads int    `json:"threads"`
	Msg     string `json:"msg"`
}

// Run is one corpus benchmarked on one machine, Go version and bench version.
type Run struct {
	Schema      int          `json:"schema"`
	ID          string       `json:"id"`
	Corpus      string       `json:"corpus"`
	Kind        string       `json:"kind"`
	CorpusSize  int64        `json:"corpus_size"`
	CorpusFiles int          `json:"corpus_files"`
	CorpusHash  string       `json:"corpus_hash"`
	Bench       string       `json:"bench"`
	MinPasses   int          `json:"min_passes"`
	Merges      int          `json:"merges"`
	Created     string       `json:"created"`
	Updated     string       `json:"updated"`
	Libs        []string     `json:"libs"`
	Tool        sysinfo.Tool `json:"tool"`
	Env         sysinfo.Env  `json:"env"`
	Encode      []EncRow     `json:"-"`
	Decode      []DecRow     `json:"-"`
	Errors      []RunError   `json:"errors,omitempty"`
}

// RunID is deterministic in the four dimensions that define a run.
func RunID(corpus, cpuBrand, goVersion, bench string) string {
	h := xxh3.HashString(strings.Join([]string{corpus, cpuBrand, goVersion, bench}, "\x00"))
	return strconv.FormatUint(h, 36)
}

// LibKey is the "module@version" form stored in Run.Libs.
func LibKey(module, version string) string { return module + "@" + version }

// libIndex returns the index of key, appending it when new.
func (r *Run) libIndex(key string) int {
	for i, l := range r.Libs {
		if l == key {
			return i
		}
	}
	r.Libs = append(r.Libs, key)
	return len(r.Libs) - 1
}

// LibIndex is libIndex for callers building a run.
func (r *Run) LibIndex(module, version string) int { return r.libIndex(LibKey(module, version)) }

func (r *Run) fileName() string { return filepath.Join("runs", r.Corpus+"-"+r.ID+".json") }

// Load reads a run file, returning nil when it does not exist.
func LoadRun(dir, corpus, id string) (*Run, error) {
	p := filepath.Join(dir, "runs", corpus+"-"+id+".json")
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var w runWire
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", p, err)
	}
	if w.Schema != Schema {
		return nil, fmt.Errorf("%s: schema %d, this build writes %d", p, w.Schema, Schema)
	}
	r := w.Run
	r.Encode, err = decodeEncRows(w.EncodeTable)
	if err != nil {
		return nil, fmt.Errorf("%s: encode rows: %w", p, err)
	}
	r.Decode, err = decodeDecRows(w.DecodeTable)
	if err != nil {
		return nil, fmt.Errorf("%s: decode rows: %w", p, err)
	}
	return &r, nil
}

// Save writes the run file atomically.
func (r *Run) Save(dir string) error {
	p := filepath.Join(dir, r.fileName())
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := encodeRun(r)
	if err != nil {
		return err
	}
	return writeAtomic(p, b)
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
