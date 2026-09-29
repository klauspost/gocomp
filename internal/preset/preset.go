// Package preset stores a benchmark selection as JSON: what to measure, never
// where to put it.
//
// Output and cache directories are deliberately absent. A preset is meant to be
// committed next to the code, and it has to mean the same thing on a machine
// whose corpora live somewhere else.
package preset

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DefaultDir is repo-relative on purpose: a preset is versioned with the
// benchmark version that gives its numbers meaning.
const DefaultDir = "presets"

// Dir resolves the -presets flag.
func Dir(flag string) string {
	if flag != "" {
		return flag
	}
	return DefaultDir
}

// Dur is a duration written as "2s" rather than a nanosecond count, because
// these files are read and edited by hand.
type Dur time.Duration

func (d Dur) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Dur) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	*d = Dur(v)
	return err
}

// Preset is one saved selection. Every field is optional: an absent one leaves
// the corresponding default or command-line flag alone, so a three-line preset
// that names two corpora is valid and useful.
type Preset struct {
	Name       string           `json:"-"` // the file name, without .json
	Desc       string           `json:"desc,omitempty"`
	Corpora    []string         `json:"corpora,omitempty"`
	Codecs     map[string][]int `json:"codecs,omitempty"`
	Threads    []int            `json:"threads,omitempty"`
	SrcThreads int              `json:"src_threads,omitempty"`
	Encode     bool             `json:"encode"`
	Decode     bool             `json:"decode"`
	Passes     int              `json:"passes,omitempty"`
	MinWall    Dur              `json:"min_wall,omitempty"`
	MaxPasses  int              `json:"max_passes,omitempty"`
	MaxJob     Dur              `json:"max_job,omitempty"`
	Windows    []string         `json:"windows,omitempty"` // -window items: "default", "64K"
	Validate   bool             `json:"validate,omitempty"`
	Collapse   bool             `json:"collapse,omitempty"`
	Slow       bool             `json:"slow,omitempty"`
}

// CheckName rejects anything that would write outside the preset directory.
// Names reach this from a text field in the TUI and from a flag, so it is a
// boundary, not a formality.
func CheckName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("preset name is empty")
	case len(name) > 64:
		return fmt.Errorf("preset name is too long")
	case name != filepath.Base(name) || strings.ContainsAny(name, `/\`):
		return fmt.Errorf("preset name %q may not contain a path", name)
	case strings.HasPrefix(name, "."):
		return fmt.Errorf("preset name %q may not start with a dot", name)
	}
	for _, r := range name {
		ok := r == '-' || r == '_' || r == '.' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !ok {
			return fmt.Errorf("preset name %q may not contain %q", name, string(r))
		}
	}
	return nil
}

func path(dir, name string) string { return filepath.Join(dir, name+".json") }

// List reads every preset in dir, sorted by name. A missing directory is empty,
// not an error: presets are optional.
func List(dir string) ([]Preset, error) {
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Preset
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p, err := readFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Load takes either a bare name looked up in dir, or a path to a file. Anything
// holding a separator or ending in .json is a path.
func Load(dir, ref string) (Preset, error) {
	if strings.ContainsAny(ref, `/\`) || strings.HasSuffix(ref, ".json") {
		return readFile(ref)
	}
	if err := CheckName(ref); err != nil {
		return Preset{}, err
	}
	p, err := readFile(path(dir, ref))
	if os.IsNotExist(err) {
		return Preset{}, fmt.Errorf("no preset %q in %s (try: compress-site list presets)", ref, dir)
	}
	return p, err
}

func readFile(name string) (Preset, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return Preset{}, err
	}
	var p Preset
	if err := json.Unmarshal(b, &p); err != nil {
		return Preset{}, fmt.Errorf("%s: %w", name, err)
	}
	p.Name = strings.TrimSuffix(filepath.Base(name), ".json")
	return p, nil
}

// Save writes p to dir/<name>.json through a temp file, so an interrupted write
// cannot leave a half-parsed preset behind.
func (p Preset) Save(dir string) error {
	if err := CheckName(p.Name); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dst := path(dir, p.Name)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func Delete(dir, name string) error {
	if err := CheckName(name); err != nil {
		return err
	}
	return os.Remove(path(dir, name))
}

// CorpusSpec, CodecSpec, ThreadSpec, WindowSpec and OpSpec render the selection back into
// the syntax the run flags already accept, so a preset reaches the engine
// through exactly the same parsing as a command line and cannot diverge from it.
func (p Preset) CorpusSpec() string { return strings.Join(p.Corpora, ",") }

func (p Preset) CodecSpec() string {
	ids := make([]string, 0, len(p.Codecs))
	for id := range p.Codecs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []string
	for _, id := range ids {
		lv := p.Codecs[id]
		if len(lv) == 0 {
			out = append(out, id) // levels left to -level
			continue
		}
		parts := make([]string, len(lv))
		for i, n := range lv {
			parts[i] = strconv.Itoa(n)
		}
		out = append(out, id+":"+strings.Join(parts, ","))
	}
	return strings.Join(out, ",")
}

func (p Preset) ThreadSpec() string {
	parts := make([]string, len(p.Threads))
	for i, n := range p.Threads {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

func (p Preset) WindowSpec() string { return strings.Join(p.Windows, ",") }

// OpSpec falls back to both: a preset that names neither operation would
// otherwise measure nothing, which is never what was meant.
func (p Preset) OpSpec() string {
	switch {
	case p.Encode && !p.Decode:
		return "encode"
	case p.Decode && !p.Encode:
		return "decode"
	}
	return "encode,decode"
}

// Summary is the one-line description the TUI list and "list presets" show.
func (p Preset) Summary() string {
	var parts []string
	parts = append(parts, plural(len(p.Corpora), "corpus", "corpora"))
	parts = append(parts, plural(len(p.Codecs), "codec", "codecs"))
	if len(p.Threads) > 0 {
		parts = append(parts, "t"+p.ThreadSpec())
	}
	if s := p.WindowSpec(); s != "" && s != "default" {
		parts = append(parts, "w"+s)
	}
	parts = append(parts, p.OpSpec())
	if p.Validate {
		parts = append(parts, "validate")
	}
	if p.Slow {
		parts = append(parts, "slow")
	}
	return strings.Join(parts, " · ")
}

func plural(n int, one, many string) string {
	if n == 0 {
		return "all " + many
	}
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
