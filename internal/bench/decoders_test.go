package bench

import (
	"slices"
	"testing"

	"github.com/klauspost/compress-site/internal/codec"
	"github.com/klauspost/compress-site/internal/corpus"
)

func TestDecodersFollowTheSelection(t *testing.T) {
	variant := func(id string) Variant {
		e, ok := codec.LookupEnc(id)
		if !ok {
			t.Fatalf("encoder %s is not registered", id)
		}
		return Variant{Enc: e, Level: e.Levels[0]}
	}
	for _, c := range []struct {
		name string
		of   string   // the encoder whose output is decoded
		sel  []string // every selected encoder
		all  bool
		want []string
	}{
		{"own implementation alone", "zskp", []string{"zskp"}, false, []string{"zskp"}},
		{"another selected codec decodes too", "zskp", []string{"zskp", "stdzstd"}, false, []string{"stdzstd", "zskp"}},
		{"-decoders=all", "zskp", []string{"zskp"}, true, []string{"stdzstd", "zskp"}},
		{"unselected gzip decoders stay out", "gzkp", []string{"gzkp", "zskp"}, false, []string{"gzkp"}},
		{"own implementation under another id", "br2", []string{"br2"}, false, []string{"br"}},
		{"a reader with no encoder follows With", "bz2", []string{"bz2"}, false, []string{"bz2", "bz2std"}},
		{"S2's snappy reader needs s2s selected", "snappy", []string{"snappy"}, false, []string{"snappy"}},
		{"and joins once it is", "snappy", []string{"snappy", "s2s"}, false, []string{"s2sn", "snappy"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var vs []Variant
			for _, id := range c.sel {
				vs = append(vs, variant(id))
			}
			var got []string
			for _, d := range (Config{AllDecoders: c.all}).DecodersOf(variant(c.of), corpus.KindFile, vs) {
				got = append(got, d.ID)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("decoders of %s with %v selected: got %v, want %v", c.of, c.sel, got, c.want)
			}
		})
	}
}
