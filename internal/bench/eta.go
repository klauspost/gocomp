package bench

import (
	"slices"
	"time"

	"github.com/klauspost/compress-site/internal/codec"
	"github.com/klauspost/compress-site/internal/corpus"
	"github.com/klauspost/compress-site/internal/db"
)

// Key names a measurement the same way on every corpus. src is the encoder
// variant on both sides, so a Result and a stored row produce the same key.
type Key struct {
	op      Op
	codec   string
	src     string
	window  int
	threads int
}

// anyThreads pools a measurement over every thread count it was stored at. A
// codec that declares no concurrency runs on one thread whatever GOMAXPROCS
// says, so another thread count prices it nearly as well as its own.
const anyThreads = -1

func (r Result) Key() Key {
	src := r.Src
	if r.Op == OpEncode {
		src = codec.Variant(r.Codec, r.Level)
	}
	return Key{r.Op, r.Codec, src, r.Window, r.Threads}
}

func (v Variant) encKey(t int) Key             { return Key{OpEncode, v.Enc.ID, v.ID(), v.Window, t} }
func (v Variant) decKey(dec string, t int) Key { return Key{OpDecode, dec, v.ID(), v.Window, t} }

// History is what one pass cost in past runs, in nanoseconds per uncompressed
// byte, by measurement and corpus. Every machine and library version counts:
// the estimate only has to be roughly right, and a measurement replaces it with
// its own timing from its first pass on.
type History map[Key]map[string][]float64

// LoadHistory reads the database at dir. It is best effort: a run that cannot
// be read only leaves the estimate with less to go on.
func LoadHistory(dir string) History {
	h := History{}
	ix, err := db.LoadIndex(dir)
	if err != nil {
		return h
	}
	add := func(k Key, cp string, ns, n int64) {
		if ns <= 0 || n <= 0 {
			return
		}
		for _, t := range []int{k.threads, anyThreads} {
			k.threads = t
			if h[k] == nil {
				h[k] = map[string][]float64{}
			}
			h[k][cp] = append(h[k][cp], float64(ns)/float64(n))
		}
	}
	for _, s := range ix.Runs {
		r, err := db.LoadRun(dir, s.Corpus, s.ID)
		if err != nil || r == nil {
			continue
		}
		for _, x := range r.Encode {
			add(Key{OpEncode, x.Codec, codec.Variant(x.Codec, x.Level), x.Window, x.Threads}, r.Corpus, x.NS, x.In)
		}
		for _, x := range r.Decode {
			add(Key{OpDecode, x.Codec, x.Src, x.Window, x.Threads}, r.Corpus, x.NS, x.Out)
		}
	}
	return h
}

// pass prices one pass of k over n bytes of corpus cp, at any thread count
// unless conc. Without numbers from cp itself it takes the median over the
// corpora that have some; own says which.
func (h History) pass(k Key, conc bool, cp string, n int64) (time.Duration, bool, bool) {
	if !conc {
		k.threads = anyThreads
	}
	v, own := h[k][cp]
	if !own {
		for _, x := range h[k] {
			v = append(v, median(x))
		}
	}
	if len(v) == 0 {
		return 0, false, false
	}
	return max(time.Duration(median(v)*float64(n)), 1), own, true
}

func median(v []float64) float64 {
	s := slices.Sorted(slices.Values(v))
	return s[len(s)/2]
}

// ETA is the expected time left in a run.
type ETA struct {
	cfg     Config
	corpora []string
	items   []map[Key]*etaItem // by corpus, in run order
	at      int                // the corpus being measured; -1 before the first
	// Measured over expected first passes, for the current corpus's prices
	// borrowed from other corpora. Those say what a codec costs, not what this
	// corpus costs; a few measurements say how far off that is.
	got, want time.Duration
}

type etaItem struct {
	pass   time.Duration // expected pass; 0 when nothing is known
	prep   time.Duration // its share of encoding the decode input
	own    bool          // pass is from this corpus's own history
	n      int
	target int
	best   time.Duration
	done   bool
}

// NewETA plans the measurements a run of cps will make, as the Engine and Plan
// count them, and prices each from h.
func NewETA(cfg Config, h History, cps []corpus.Corpus, variants map[corpus.Kind][]Variant) *ETA {
	e := &ETA{cfg: cfg, at: -1}
	for _, cp := range cps {
		n := cp.Bytes()
		largest := 0
		if cp.Kind == corpus.KindFile {
			largest = int(n) // a directory's largest member is not known until it is loaded
		}
		vs := cfg.Expand(variants[cp.Kind], largest)
		items := map[Key]*etaItem{}
		price := func(k Key, conc bool) *etaItem {
			d, own, _ := h.pass(k, conc, cp.ID, n)
			it := &etaItem{pass: d, own: own}
			items[k] = it
			return it
		}
		for _, v := range vs {
			conc := v.Enc.Concurrent(cp.Kind)
			if cfg.Encode {
				for _, t := range cfg.ThreadsFor(conc) {
					price(v.encKey(t), conc)
				}
			}
			if !cfg.Decode {
				continue
			}
			var decs []*etaItem
			for _, d := range cfg.DecodersOf(v, cp.Kind, vs) {
				dc := d.Concurrent(cp.Kind)
				for _, t := range cfg.ThreadsFor(dc) {
					decs = append(decs, price(v.decKey(d.ID, t), dc))
				}
			}
			// compressFor encodes the input once more, untimed, unless the
			// encode phase hands its own output over.
			if _, ok := cfg.sourceGroup(v, cp.Kind); len(decs) == 0 || cfg.Encode && cp.Kind == corpus.KindFile && ok {
				continue
			}
			d, _, ok := h.pass(v.encKey(cfg.SrcThreads), conc, cp.ID, n)
			for _, it := range decs {
				it.prep = d / time.Duration(len(decs))
				if !ok {
					it.pass = 0
				}
			}
		}
		e.corpora = append(e.corpora, cp.ID)
		e.items = append(e.items, items)
	}
	return e
}

// Observe moves the estimate on with one event from the run.
func (e *ETA) Observe(ev Event) {
	if ev.Kind == "corpus" {
		e.at = slices.Index(e.corpora, ev.Corpus)
		e.got, e.want = 0, 0
		return
	}
	if e.at < 0 || ev.Result == nil {
		return
	}
	it := e.items[e.at][ev.Result.Key()]
	if it == nil {
		return
	}
	switch ev.Kind {
	case "pass":
		if it.n == 0 && it.pass > 0 && !it.own {
			e.got += ev.Result.Best
			e.want += it.pass
		}
		it.n, it.target, it.best = ev.Result.Passes, ev.Target, ev.Result.Best
	case "result", "warn":
		it.done = true
	}
}

// Left is the expected time to the end of the current corpus and of the run,
// and how many measurements still to come nothing is known about. Those count
// as nothing, so while there are any both figures are a floor.
func (e *ETA) Left() (cur, total time.Duration, unknown int) {
	for i := max(e.at, 0); i < len(e.items); i++ {
		scale := 1.0
		if i == e.at {
			scale = e.scale()
		}
		var sum time.Duration
		for _, it := range e.items[i] {
			d, ok := it.left(e.cfg, scale)
			if !ok {
				unknown++
			}
			sum += d
		}
		if i == e.at {
			cur = sum
		}
		total += sum
	}
	return cur, total, unknown
}

// Pass is how long the next pass of k should take, in the current corpus.
func (e *ETA) Pass(k Key) (time.Duration, bool) {
	if e.at < 0 {
		return 0, false
	}
	if it := e.items[e.at][k]; it != nil && !it.done {
		d := it.next(e.scale())
		return d, d > 0
	}
	return 0, false
}

func (e *ETA) scale() float64 {
	if e.want == 0 {
		return 1
	}
	return float64(e.got) / float64(e.want)
}

// next is the expected duration of its next pass: its best so far once it has
// one, its price until then. 0 when nothing is known.
func (it *etaItem) next(scale float64) time.Duration {
	switch {
	case it.n > 0:
		return it.best
	case it.own:
		return it.pass
	}
	return time.Duration(float64(it.pass) * scale)
}

func (it *etaItem) left(cfg Config, scale float64) (time.Duration, bool) {
	d := it.next(scale)
	switch {
	case it.done:
		return 0, true
	case it.n > 0:
		return time.Duration(max(it.target-it.n, 0)) * d, true
	case d == 0:
		return 0, false
	}
	return d*time.Duration(passesFor(d, cfg)) + it.prep, true
}
