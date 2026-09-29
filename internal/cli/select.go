package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/klauspost/compress-site/internal/bench"
	"github.com/klauspost/compress-site/internal/codec"
	"github.com/klauspost/compress-site/internal/corpus"
)

// codecSel is one requested encoder, optionally pinned to specific levels.
type codecSel struct {
	id     string
	levels []int // nil means "whatever -level says"
}

// parseCodecs reads "zskp:1-4,s2,gzkp:1,5,9". Items are comma separated; an
// item with a colon starts a new codec, a purely numeric item continues the
// previous codec's level list, and anything else is a bare codec id. Codec ids
// are never numeric, so this is unambiguous.
func parseCodecs(spec string) ([]codecSel, error) {
	if spec == "" || spec == "all" {
		return nil, nil
	}
	var out []codecSel
	for _, item := range splitList(spec) {
		if id, lv, ok := strings.Cut(item, ":"); ok {
			levels, err := parseLevels(lv)
			if err != nil {
				return nil, fmt.Errorf("codec %s: %w", id, err)
			}
			out = append(out, codecSel{id: id, levels: levels})
			continue
		}
		if isLevelItem(item) && len(out) > 0 && out[len(out)-1].levels != nil {
			levels, err := parseLevels(item)
			if err != nil {
				return nil, err
			}
			last := &out[len(out)-1]
			last.levels = append(last.levels, levels...)
			continue
		}
		out = append(out, codecSel{id: item})
	}
	return out, nil
}

func isLevelItem(s string) bool {
	if s == "" {
		return false
	}
	if _, err := strconv.Atoi(s); err == nil {
		return true
	}
	if i := strings.IndexByte(s[1:], '-'); i >= 0 {
		_, e1 := strconv.Atoi(s[:i+1])
		_, e2 := strconv.Atoi(s[i+2:])
		return e1 == nil && e2 == nil
	}
	return false
}

// levelMode is the fallback for codecs selected without explicit levels.
type levelMode struct {
	all      bool
	def      bool
	explicit []int
}

func parseLevelMode(spec string) (levelMode, error) {
	switch spec {
	case "", "all":
		return levelMode{all: true}, nil
	case "default":
		return levelMode{def: true}, nil
	}
	lv, err := parseLevels(spec)
	if err != nil {
		return levelMode{}, err
	}
	return levelMode{explicit: lv}, nil
}

// Variants expands the selection against the registry for one corpus kind.
// Unsupported combinations are skipped rather than failing the whole run: a
// block-only codec simply has nothing to do on a single-file corpus.
func variantsFor(sels []codecSel, mode levelMode, kind corpus.Kind, slow bool) ([]bench.Variant, error) {
	encs := codec.Encoders()
	if len(sels) > 0 {
		encs = encs[:0:0]
		seen := map[string]bool{}
		for _, s := range sels {
			e, ok := codec.LookupEnc(s.id)
			if !ok {
				return nil, fmt.Errorf("unknown codec %q (try: compress-site list codecs)", s.id)
			}
			if !seen[e.ID] {
				seen[e.ID] = true
				encs = append(encs, e)
			}
		}
	}

	explicit := map[string][]int{}
	for _, s := range sels {
		if s.levels != nil {
			explicit[s.id] = s.levels
		}
	}

	var out []bench.Variant
	for _, e := range encs {
		if e.Kinds&kind == 0 || (e.Slow && !slow) {
			continue
		}
		want := explicit[e.ID]
		for _, l := range e.Levels {
			switch {
			case want != nil:
				if !containsInt(want, l.N) {
					continue
				}
			case mode.def:
				if l.N != defaultLevel(e).N {
					continue
				}
			case len(mode.explicit) > 0:
				if !containsInt(mode.explicit, l.N) {
					continue
				}
			}
			if l.Slow && !slow && want == nil {
				continue
			}
			out = append(out, bench.Variant{Enc: e, Level: l})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no codec/level combination matches the selection for %s corpora", kind)
	}
	return out, nil
}

// defaultLevel is the level named "default", else the first non-slow one.
func defaultLevel(e codec.Enc) codec.Level {
	for _, l := range e.Levels {
		if l.Name == "default" {
			return l
		}
	}
	for _, l := range e.Levels {
		if !l.Slow {
			return l
		}
	}
	return e.Levels[0]
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
