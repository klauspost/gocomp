package bench

import (
	"slices"
	"testing"

	"github.com/klauspost/gocomp/internal/codec"
)

func TestParseWindows(t *testing.T) {
	got, err := ParseWindows("8M, default,64K,8K,1M,64kb,default")
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{0, 8 << 10, 64 << 10, 1 << 20, 8 << 20}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	for _, bad := range []string{"3K", "0", "-1M", "abc", "", "1KK", "K"} {
		if w, err := ParseWindows(bad); err == nil {
			t.Errorf("%q parsed as %v, want an error", bad, w)
		}
	}
}

func TestWindowNameParsesBack(t *testing.T) {
	for _, w := range []int{0, 1 << 10, 64 << 10, 1 << 20, 1 << 30, 1 << 31} {
		got, err := ParseWindows(WindowName(w))
		if err != nil || len(got) != 1 || got[0] != w {
			t.Errorf("%d -> %q -> %v, %v", w, WindowName(w), got, err)
		}
	}
}

func TestExpand(t *testing.T) {
	var vs []Variant
	for _, id := range []string{"lz4", "gzkp", "minlz"} {
		e, ok := codec.LookupEnc(id)
		if !ok {
			t.Fatalf("codec %s is not registered", id)
		}
		vs = append(vs, Variant{Enc: e, Level: e.Levels[0]})
	}
	cfg := Config{Windows: []int{8 << 20, 0, 64 << 10, 128 << 10, 1 << 20}}
	windows := func(out []Variant, id string) []int {
		var ws []int
		for _, v := range out {
			if v.Enc.ID == id {
				ws = append(ws, v.Window)
			}
		}
		return ws
	}

	for _, c := range []struct {
		largest int
		id      string
		want    []int
	}{
		// 128K and 8M are not lz4 block sizes, and gzip's window is fixed.
		{0, "lz4", []int{0, 64 << 10, 1 << 20}},
		{0, "gzkp", []int{0}},
		{0, "minlz", []int{0, 64 << 10, 128 << 10, 1 << 20, 8 << 20}},
		// A 100KB input: the smallest window holding it is kept and every
		// larger one dropped, judged per codec -- for lz4 that is 1M, since it
		// has no 128K.
		{100 << 10, "lz4", []int{0, 64 << 10, 1 << 20}},
		{100 << 10, "minlz", []int{0, 64 << 10, 128 << 10}},
		{100 << 10, "gzkp", []int{0}},
	} {
		if got := windows(cfg.Expand(vs, c.largest), c.id); !slices.Equal(got, c.want) {
			t.Errorf("largest %d, %s: got %v, want %v", c.largest, c.id, got, c.want)
		}
	}

	// minlz's default block is 2M, so 2M is the default row whether or not the
	// sweep also names default.
	for _, ws := range [][]int{{0, 1 << 20, 2 << 20}, {1 << 20, 2 << 20}} {
		got := windows(Config{Windows: ws}.Expand(vs, 0), "minlz")
		if slices.Sort(got); !slices.Equal(got, []int{0, 1 << 20}) {
			t.Errorf("sweep %v: minlz got %v, want the default and 1M", ws, got)
		}
	}

	if got := (Config{}).Expand(vs, 0); len(got) != len(vs) {
		t.Errorf("an unset sweep should mean the default alone, got %d variants", len(got))
	}
}
