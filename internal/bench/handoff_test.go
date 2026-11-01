package bench

import (
	"context"
	"testing"
	"time"

	"github.com/klauspost/compress-site/internal/codec"
	"github.com/klauspost/compress-site/internal/corpus"
	"github.com/zeebo/xxh3"
)

// runCollect runs an engine and returns its results and the variants whose
// decode input had to be encoded again.
func runCollect(t *testing.T, cfg Config, data *corpus.Data, vs []Variant) ([]Result, map[string]bool) {
	t.Helper()
	events := make(chan Event, 1<<14)
	eng := &Engine{Cfg: cfg, Data: data, Variants: vs, Events: events}
	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	close(events)
	reencoded := map[string]bool{}
	for ev := range events {
		if ev.Msg == "preparing input" {
			reencoded[ev.Label] = true
		}
	}
	if eng.done != eng.total {
		t.Errorf("progress ended at %d/%d", eng.done, eng.total)
	}
	return res, reencoded
}

// hashedData is testData with the hash that -validate checks against.
func hashedData(t *testing.T) *corpus.Data {
	d := testData(t)
	d.Hash = xxh3.Hash(d.File)
	return d
}

func handoffConfig(threads []int, src int) Config {
	return Config{
		Threads: threads, SrcThreads: src, Passes: 2, MaxPasses: 3,
		MinWall: time.Millisecond, MaxJob: time.Minute, Encode: true, Decode: true, Validate: true,
	}
}

func handoffVariants(t *testing.T) []Variant {
	t.Helper()
	vs := testVariants(t) // s2 and zskp: both concurrent on files
	gz, ok := codec.LookupEnc("gzkp")
	if !ok {
		t.Fatal("gzkp is not registered")
	}
	return append(vs, Variant{Enc: gz, Level: gz.Levels[len(gz.Levels)-1]})
}

// With the source thread count in the sweep, no decode input is encoded twice,
// and what each decoder reads is exactly what the timed encode wrote.
func TestDecodeReadsTheEncodeOutput(t *testing.T) {
	data, vs := hashedData(t), handoffVariants(t)
	cfg := handoffConfig([]int{1}, 1)
	res, reencoded := runCollect(t, cfg, data, vs)
	if len(reencoded) > 0 {
		t.Errorf("decode inputs were encoded again: %v", reencoded)
	}

	// Encode rows are recorded when their group ends, after the decodes that
	// ran inside it, so they are collected first.
	out := map[string]int64{}
	for _, r := range res {
		if r.Op == OpEncode {
			out[codec.Variant(r.Codec, r.Level)] = r.Out
		}
	}
	var dec int
	for _, r := range res {
		switch {
		case r.Err != "":
			t.Errorf("%s %s: %s", r.Op, r.Codec, r.Err)
		case r.Op == OpDecode:
			dec++
			if r.In != out[r.Src] {
				t.Errorf("%s <- %s read %d bytes, the encode wrote %d", r.Codec, r.Src, r.In, out[r.Src])
			}
			if r.Verified != 1 {
				t.Errorf("%s <- %s: not verified", r.Codec, r.Src)
			}
		}
	}
	if _, want := Plan(cfg, data.Kind, vs); dec != want {
		t.Errorf("%d decode results, planned %d", dec, want)
	}
}

// A source thread count outside the sweep still gives a complete decode phase:
// concurrent codecs encode their input again at that count, while a codec
// with no concurrency encodes the same bytes at any count and is still reused.
func TestDecodeFallsBackOutsideTheSourceGroup(t *testing.T) {
	data, vs := hashedData(t), handoffVariants(t)
	cfg := handoffConfig([]int{1}, 2)
	res, reencoded := runCollect(t, cfg, data, vs)
	for _, v := range vs {
		if got, want := reencoded[v.Label()], v.Enc.Concurrent(corpus.KindFile); got != want {
			t.Errorf("%s: encoded again = %v, want %v", v.Label(), got, want)
		}
	}
	srcOf := map[string]codec.Enc{}
	for _, v := range vs {
		srcOf[v.ID()] = v.Enc
	}
	var dec int
	for _, r := range res {
		if r.Err != "" {
			t.Errorf("%s %s: %s", r.Op, r.Codec, r.Err)
		}
		if r.Op == OpDecode {
			dec++
			// Only zskp's bytes move with the thread count, so only its
			// decodes name the count their input was encoded at.
			want := 0
			if srcOf[r.Src].ThreadsVaryOutput {
				want = cfg.SrcThreads
			}
			if r.SrcThreads != want {
				t.Errorf("%s <- %s: SrcThreads = %d, want %d", r.Codec, r.Src, r.SrcThreads, want)
			}
		}
	}
	if _, want := Plan(cfg, data.Kind, vs); dec != want {
		t.Errorf("%d decode results, planned %d", dec, want)
	}
}

// The reused output is the same input compressFor produces for a decode-only
// run, so nothing stored changes meaning.
func TestReusedSourceMatchesADecodeOnlyRun(t *testing.T) {
	data, vs := hashedData(t), handoffVariants(t)
	both, _ := runCollect(t, handoffConfig([]int{1}, 1), data, vs)
	only := handoffConfig([]int{1}, 1)
	only.Encode = false
	alone, reencoded := runCollect(t, only, data, vs)
	if len(reencoded) != len(vs) {
		t.Fatalf("a decode-only run must encode every input itself, did %d of %d", len(reencoded), len(vs))
	}
	in := map[string]int64{}
	for _, r := range alone {
		if r.Op == OpDecode {
			in[r.Codec+"<-"+r.Src] = r.In
		}
	}
	for _, r := range both {
		if r.Op == OpDecode && in[r.Codec+"<-"+r.Src] != r.In {
			t.Errorf("%s <- %s: %d bytes reused, %d from compressFor", r.Codec, r.Src, r.In, in[r.Codec+"<-"+r.Src])
		}
	}
}

// A directory is encoded file by file into one buffer, so its compressed form
// never exists whole and every input is still produced for the decode phase.
func TestDirectoryDecodeStillEncodesItsInput(t *testing.T) {
	file := testData(t)
	a, b := file.File[:200<<10], file.File[200<<10:]
	dir := &corpus.Data{
		Corpus: corpus.Corpus{ID: "testdir", Kind: corpus.KindDir},
		Files: []corpus.File{
			{Name: "a", Data: a, Hash: xxh3.Hash(a)},
			{Name: "b", Data: b, Hash: xxh3.Hash(b)},
		},
		Total: file.Total, Max: len(b),
	}
	vs := testVariants(t)
	res, reencoded := runCollect(t, handoffConfig([]int{1}, 1), dir, vs)
	if len(reencoded) != len(vs) {
		t.Errorf("encoded %d of %d directory inputs for the decode phase", len(reencoded), len(vs))
	}
	for _, r := range res {
		if r.Err != "" {
			t.Errorf("%s %s: %s", r.Op, r.Codec, r.Err)
		}
	}
}
