package db

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/klauspost/compress-site/internal/codec"
	"github.com/klauspost/compress-site/internal/corpus"
	"github.com/klauspost/compress-site/internal/sysinfo"
)

type LevelInfo struct {
	N      int    `json:"n"`
	Name   string `json:"name"`
	Slow   bool   `json:"slow,omitempty"`
	Class  string `json:"class,omitempty"`  // "uncompressed" or "special"; absent is regular
	Window int    `json:"window,omitempty"` // the size window 0 means here; absent where it is fixed
}

// RoleInfo describes one direction of a codec. Encoding and decoding are kept
// apart because their concurrency genuinely differs: pgzip parallelises
// compression but inflates a single member serially.
type RoleInfo struct {
	Kinds  []string    `json:"kinds"`
	Conc   []string    `json:"conc"`
	Slow   bool        `json:"slow,omitempty"`
	Levels []LevelInfo `json:"levels,omitempty"` // encode only
}

// CodecInfo carries the static facts the site needs.
type CodecInfo struct {
	ID     string    `json:"id"`
	Name   string    `json:"name"`
	Impl   string    `json:"impl"`
	Format string    `json:"format"`
	Module string    `json:"module"`
	Encode *RoleInfo `json:"encode,omitempty"`
	Decode *RoleInfo `json:"decode,omitempty"`
}

type FormatInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type CorpusInfo struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Desc  string   `json:"desc,omitempty"`
	Kind  string   `json:"kind"`
	Size  int64    `json:"size"`
	Files int      `json:"files"`
	Tier  string   `json:"tier"`
	Tags  []string `json:"tags,omitempty"`
}

type MachineInfo struct {
	ID    string      `json:"id"`
	Label string      `json:"label"`
	CPU   sysinfo.CPU `json:"cpu"`
	OS    sysinfo.OS  `json:"os"`
	Mem   uint64      `json:"mem_total"`
}

// RunStub is the index entry: enough to drive every filter without fetching a
// run file.
type RunStub struct {
	ID      string              `json:"id"`
	File    string              `json:"file"`
	Corpus  string              `json:"corpus"`
	Kind    string              `json:"kind"`
	Machine string              `json:"machine"`
	Go      string              `json:"go"`
	GOOS    string              `json:"goos"`
	GOARCH  string              `json:"goarch"`
	GOAMD64 string              `json:"goamd64,omitempty"`
	Bench   string              `json:"bench"`
	Threads []int               `json:"threads"`
	Windows []int               `json:"windows"`
	Codecs  []string            `json:"codecs"`
	Libs    map[string][]string `json:"libs"`
	Variant map[string]string   `json:"variant,omitempty"`
	NEncode int                 `json:"n_encode"`
	NDecode int                 `json:"n_decode"`
	Merges  int                 `json:"merges"`
	Created string              `json:"created"`
	Updated string              `json:"updated"`
}

// Index is the main database: one entry per run plus the shared dictionaries.
type Index struct {
	Schema    int                    `json:"schema"`
	Generated string                 `json:"generated"`
	Formats   []FormatInfo           `json:"formats"`
	Codecs    []CodecInfo            `json:"codecs"`
	Corpora   []CorpusInfo           `json:"corpora"`
	Machines  map[string]MachineInfo `json:"machines"`
	Runs      []RunStub              `json:"runs"`
}

// LoadIndex reads the manifest, returning an empty one if absent.
func LoadIndex(dir string) (*Index, error) {
	ix := &Index{Schema: Schema, Machines: map[string]MachineInfo{}}
	b, err := os.ReadFile(filepath.Join(dir, IndexFile))
	if os.IsNotExist(err) {
		return ix, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, ix); err != nil {
		return nil, err
	}
	if ix.Machines == nil {
		ix.Machines = map[string]MachineInfo{}
	}
	return ix, nil
}

// Save writes the manifest atomically.
func (ix *Index) Save(dir string) error {
	ix.Schema = Schema
	ix.Generated = nowUTC()
	sort.Slice(ix.Runs, func(i, j int) bool {
		if ix.Runs[i].Corpus != ix.Runs[j].Corpus {
			return ix.Runs[i].Corpus < ix.Runs[j].Corpus
		}
		return ix.Runs[i].ID < ix.Runs[j].ID
	})
	b, err := json.MarshalIndent(ix, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, IndexFile), append(b, '\n'))
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

func kindNames(k corpus.Kind) []string {
	out := []string{}
	for _, c := range []corpus.Kind{corpus.KindFile, corpus.KindDir} {
		if k&c != 0 {
			out = append(out, c.String())
		}
	}
	return out
}

// RefreshCatalogs rebuilds the codec, format and corpus dictionaries from the
// registry. Existing entries are replaced but never dropped: an old run may
// reference a codec that has since been removed.
func (ix *Index) RefreshCatalogs() {
	codecs := map[string]CodecInfo{}
	for _, c := range ix.Codecs {
		codecs[c.ID] = c
	}
	formats := map[string]FormatInfo{}
	for _, f := range ix.Formats {
		formats[f.ID] = f
	}

	note := func(id, name, impl, module string, f codec.Format, role *RoleInfo, encode bool) {
		ci, ok := codecs[id]
		if !ok {
			ci = CodecInfo{ID: id}
		}
		ci.Name, ci.Impl, ci.Module, ci.Format = name, impl, module, string(f)
		if encode {
			ci.Encode = role
		} else {
			ci.Decode = role
		}
		codecs[id] = ci
		formats[string(f)] = FormatInfo{ID: string(f), Name: codec.FormatName[f]}
	}

	for _, e := range codec.Encoders() {
		lv := make([]LevelInfo, 0, len(e.Levels))
		for _, l := range e.Levels {
			li := LevelInfo{N: l.N, Name: l.Name, Slow: l.Slow, Window: e.DefaultWindow(l.N)}
			if l.Class != codec.Regular {
				li.Class = l.Class.String()
			}
			lv = append(lv, li)
		}
		note(e.ID, e.Name, e.Impl, e.Module, e.Format,
			&RoleInfo{Kinds: kindNames(e.Kinds), Conc: kindNames(e.Conc), Slow: e.Slow, Levels: lv}, true)
	}
	for _, d := range codec.Decoders() {
		note(d.ID, d.Name, d.Impl, d.Module, d.Format,
			&RoleInfo{Kinds: kindNames(d.Kinds), Conc: kindNames(d.Conc), Slow: d.Slow}, false)
	}

	ix.Codecs = ix.Codecs[:0]
	for _, c := range codecs {
		ix.Codecs = append(ix.Codecs, c)
	}
	sort.Slice(ix.Codecs, func(i, j int) bool {
		if ix.Codecs[i].Format != ix.Codecs[j].Format {
			return ix.Codecs[i].Format < ix.Codecs[j].Format
		}
		return ix.Codecs[i].ID < ix.Codecs[j].ID
	})

	ix.Formats = ix.Formats[:0]
	for _, f := range formats {
		ix.Formats = append(ix.Formats, f)
	}
	sort.Slice(ix.Formats, func(i, j int) bool { return ix.Formats[i].ID < ix.Formats[j].ID })

	known := map[string]bool{}
	for _, c := range ix.Corpora {
		known[c.ID] = true
	}
	for _, c := range corpus.All() {
		ci := CorpusInfo{
			ID: c.ID, Name: c.Name, Desc: c.Desc, Kind: c.Kind.String(),
			Size: c.Bytes(), Files: c.Files, Tier: c.Tier().String(), Tags: c.Tags,
		}
		if known[c.ID] {
			for i := range ix.Corpora {
				if ix.Corpora[i].ID == c.ID {
					// Keep a measured size over the catalog estimate.
					if ix.Corpora[i].Size > 0 && c.Size == 0 {
						ci.Size = ix.Corpora[i].Size
					}
					ix.Corpora[i] = ci
					break
				}
			}
			continue
		}
		ix.Corpora = append(ix.Corpora, ci)
	}
	sort.Slice(ix.Corpora, func(i, j int) bool { return ix.Corpora[i].ID < ix.Corpora[j].ID })
}
