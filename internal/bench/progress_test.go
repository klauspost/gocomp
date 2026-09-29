package bench

import (
	"context"
	"testing"
	"time"

	"github.com/klauspost/compress-site/internal/codec"
	"github.com/klauspost/compress-site/internal/corpus"
)

func testData(t *testing.T) *corpus.Data {
	t.Helper()
	body := make([]byte, 0, 512<<10)
	for len(body) < 512<<10 {
		body = append(body, "the quick brown fox jumps over the lazy dog 0123456789\n"...)
	}
	return &corpus.Data{
		Corpus: corpus.Corpus{ID: "test", Kind: corpus.KindFile},
		File:   body, Total: int64(len(body)), Max: len(body),
	}
}

func testVariants(t *testing.T) []Variant {
	t.Helper()
	var out []Variant
	for _, id := range []string{"s2", "zskp"} {
		e, ok := codec.LookupEnc(id)
		if !ok {
			t.Fatalf("codec %s is not registered", id)
		}
		for _, l := range e.Levels[:min(2, len(e.Levels))] {
			out = append(out, Variant{Enc: e, Level: l})
		}
	}
	return out
}

// The progress counter used to advance on each measurement's *first* pass, so
// the bar read 100% while the last group was still grinding through its
// repeats -- on a large corpus, most of the wall time. done must instead track
// measurements that are actually finished.
func TestProgressReachesFullOnlyAtTheEnd(t *testing.T) {
	events := make(chan Event, 4096)
	eng := &Engine{
		Cfg: Config{
			Threads: []int{1}, SrcThreads: 1, Passes: 3, MaxPasses: 8,
			MinWall: 50 * time.Millisecond, MaxJob: time.Minute,
			BlockSize: 64 << 10, Encode: true, Decode: true,
		},
		Data: testData(t), Variants: testVariants(t), Events: events,
	}

	results, err := eng.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	close(events)

	var prev int
	var total int
	fullAt, lastPass := -1, -1
	seen := map[int]bool{}
	i := 0
	for ev := range events {
		if ev.Total > 0 {
			total = ev.Total
		}
		if ev.Done < prev {
			t.Fatalf("event %d: progress went backwards, %d -> %d", i, prev, ev.Done)
		}
		if ev.Done > ev.Total {
			t.Fatalf("event %d: done %d exceeds total %d", i, ev.Done, ev.Total)
		}
		prev = ev.Done
		seen[ev.Done] = true
		if ev.Kind == "pass" {
			lastPass = i
		}
		if fullAt < 0 && ev.Total > 0 && ev.Done == ev.Total {
			fullAt = i
		}
		i++
	}

	if fullAt < 0 {
		t.Fatalf("progress never reached %d/%d", prev, total)
	}
	if fullAt < lastPass {
		t.Errorf("progress hit 100%% at event %d but measurements were still running at %d",
			fullAt, lastPass)
	}
	if prev != total {
		t.Errorf("finished at %d/%d", prev, total)
	}
	// Round-robin finishes a whole group at once, so crediting only finished
	// measurements pinned the bar at 0 for the entire encode phase. The bar has
	// to move while a group is in flight, not just at its boundaries.
	if len(seen) < 5 {
		t.Errorf("progress only took %d distinct values (%v); the bar barely moves", len(seen), seen)
	}
	if len(results) == 0 {
		t.Fatal("no results")
	}
}

// Plan counts measurements that a failing New() or a failing decode source
// never produces. Those have to leave the total, or the bar can never fill.
func TestTotalDropsMeasurementsThatCannotRun(t *testing.T) {
	broken := codec.Enc{
		ID: "broken", Name: "broken", Impl: "test", Format: codec.Format("broken-test"),
		Module: "std", Kinds: corpus.KindFile | corpus.KindDir,
		Levels: []codec.Level{{N: 1, Name: "1"}},
		New: func(int, codec.Opts) (codec.Encoder, error) {
			return nil, errNotUsable
		},
	}
	eng := &Engine{
		Cfg: Config{
			Threads: []int{1}, SrcThreads: 1, Passes: 1, MaxPasses: 1,
			MinWall: time.Millisecond, MaxJob: time.Minute,
			BlockSize: 64 << 10, Encode: true, Decode: true,
		},
		Data:     testData(t),
		Variants: append(testVariants(t), Variant{Enc: broken, Level: broken.Levels[0]}),
	}
	if _, err := eng.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if eng.done != eng.total {
		t.Errorf("finished at %d/%d; an unusable codec must leave the total", eng.done, eng.total)
	}
}

var errNotUsable = errUnusable{}

type errUnusable struct{}

func (errUnusable) Error() string { return "not usable here" }
