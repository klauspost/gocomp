package codec

import (
	"fmt"
	"sort"
	"strings"

	"github.com/klauspost/compress-site/internal/corpus"
)

var (
	encs = map[string]Enc{}
	decs = map[string]Dec{}
)

// RegisterEnc adds an encoder. Called from init in the per-format files.
func RegisterEnc(e Enc) {
	mustID(e.ID)
	if _, dup := encs[e.ID]; dup {
		panic("codec: duplicate encoder id " + e.ID)
	}
	if e.Conc&^e.Kinds != 0 {
		panic("codec: " + e.ID + " declares Conc for a kind not in Kinds")
	}
	if len(e.Levels) == 0 {
		e.Levels = []Level{{N: 0, Name: "default"}}
	}
	encs[e.ID] = e
}

// RegisterDec adds a decoder.
func RegisterDec(d Dec) {
	mustID(d.ID)
	if _, dup := decs[d.ID]; dup {
		panic("codec: duplicate decoder id " + d.ID)
	}
	if d.Conc&^d.Kinds != 0 {
		panic("codec: " + d.ID + " declares Conc for a kind not in Kinds")
	}
	decs[d.ID] = d
}

// mustID enforces the invariant SplitVariant relies on.
func mustID(id string) {
	if id == "" || strings.ContainsAny(id, "-,: ") {
		panic("codec: invalid id " + id)
	}
}

// Encoders returns every registered encoder, ordered by format then id.
func Encoders() []Enc {
	out := make([]Enc, 0, len(encs))
	for _, e := range encs {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Format != out[j].Format {
			return out[i].Format < out[j].Format
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Decoders returns every registered decoder, ordered by format then id.
func Decoders() []Dec {
	out := make([]Dec, 0, len(decs))
	for _, d := range decs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Format != out[j].Format {
			return out[i].Format < out[j].Format
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// DecodersFor returns the decoders able to read f on kind k.
func DecodersFor(f Format, k corpus.Kind) []Dec {
	var out []Dec
	for _, d := range Decoders() {
		if d.Format == f && d.Kinds&k != 0 {
			out = append(out, d)
		}
	}
	return out
}

func LookupEnc(id string) (Enc, bool) { e, ok := encs[id]; return e, ok }
func LookupDec(id string) (Dec, bool) { d, ok := decs[id]; return d, ok }

// Modules lists the distinct module paths behind registered codecs, so build
// info lookup records eight versions rather than the whole dependency graph.
func Modules() []string {
	seen := map[string]bool{}
	for _, e := range encs {
		seen[e.Module] = true
	}
	for _, d := range decs {
		seen[d.Module] = true
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// NewEnc constructs an encoder, checking the level exists.
func NewEnc(id string, level int, o Opts) (Enc, Encoder, error) {
	e, ok := LookupEnc(id)
	if !ok {
		return Enc{}, nil, fmt.Errorf("unknown encoder %q", id)
	}
	if e.Kinds&o.Kind == 0 {
		return e, nil, fmt.Errorf("encoder %s does not support %s corpora", id, o.Kind)
	}
	if _, ok := e.Level(level); !ok {
		return e, nil, fmt.Errorf("encoder %s has no level %d", id, level)
	}
	enc, err := e.New(level, o)
	return e, enc, err
}

// NewDec constructs a decoder.
func NewDec(id string, o Opts) (Dec, Decoder, error) {
	d, ok := LookupDec(id)
	if !ok {
		return Dec{}, nil, fmt.Errorf("unknown decoder %q", id)
	}
	if d.Kinds&o.Kind == 0 {
		return d, nil, fmt.Errorf("decoder %s does not support %s corpora", id, o.Kind)
	}
	dec, err := d.New(o)
	return d, dec, err
}
