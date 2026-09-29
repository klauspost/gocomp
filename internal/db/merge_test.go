package db

import (
	"os"
	"testing"

	"github.com/klauspost/compress-site/internal/sysinfo"
)

func runFixture(lib string, ns int64, verified int) *Run {
	r := &Run{
		ID: "test", Corpus: "demo", Kind: "file", Bench: "1",
		CorpusHash: "abc", SrcThreads: 8, MinPasses: 3,
		Env: sysinfo.Env{
			Go: "go1.26.1", GOOS: "linux", GOARCH: "amd64", GOAMD64: "v3",
			NumCPU: 32, GOGC: 100,
			CPU: sysinfo.CPU{Brand: "Test CPU"},
		},
	}
	i := r.LibIndex("example.com/mod", lib)
	r.Encode = []EncRow{{
		Codec: "zskp", Level: 2, Threads: 8, Lib: i,
		In: 1000, Out: 400, NS: ns, Passes: 3, Verified: verified,
	}}
	r.Decode = []DecRow{{
		Codec: "zskp", Src: "zskp-2", Lib: i, SrcLib: i, Threads: 8,
		In: 400, Out: 1000, NS: ns / 2, Passes: 3, Verified: verified,
	}}
	return r
}

func TestMergeKeepsBestAndAccumulatesPasses(t *testing.T) {
	dir := t.TempDir()
	if _, err := Merge(dir, runFixture("v1.0.0", 1000, 1), false); err != nil {
		t.Fatal(err)
	}
	// A slower re-run must not replace the time, but must still count.
	res, err := Merge(dir, runFixture("v1.0.0", 2000, -1), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 0 || res.Kept != 2 {
		t.Fatalf("expected 2 kept and nothing added, got %+v", res)
	}
	got, err := LoadRun(dir, "demo", "test")
	if err != nil {
		t.Fatal(err)
	}
	if got.Encode[0].NS != 1000 {
		t.Errorf("NS = %d, want the faster 1000", got.Encode[0].NS)
	}
	if got.Encode[0].Passes != 6 {
		t.Errorf("Passes = %d, want 6 accumulated", got.Encode[0].Passes)
	}
	// Verification is sticky: a faster unverified pass must not erase it.
	if got.Encode[0].Verified != 1 {
		t.Errorf("Verified = %d, want it to survive an unverified merge", got.Encode[0].Verified)
	}

	// And a faster re-run does replace the time.
	if _, err := Merge(dir, runFixture("v1.0.0", 500, -1), false); err != nil {
		t.Fatal(err)
	}
	got, _ = LoadRun(dir, "demo", "test")
	if got.Encode[0].NS != 500 || got.Encode[0].Passes != 9 {
		t.Errorf("after improvement: NS=%d passes=%d, want 500 and 9", got.Encode[0].NS, got.Encode[0].Passes)
	}
}

// A new library version is a different measurement, so it coexists rather than
// overwriting. This is what makes "compare library versions" work at all.
func TestMergeLibraryVersionsCoexist(t *testing.T) {
	dir := t.TempDir()
	if _, err := Merge(dir, runFixture("v1.0.0", 1000, 1), false); err != nil {
		t.Fatal(err)
	}
	res, err := Merge(dir, runFixture("v1.1.0", 900, 1), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 2 {
		t.Fatalf("a new library version should add rows, got %+v", res)
	}
	got, _ := LoadRun(dir, "demo", "test")
	if len(got.Encode) != 2 {
		t.Fatalf("got %d encode rows, want one per library version", len(got.Encode))
	}
	seen := map[string]int64{}
	for _, r := range got.Encode {
		seen[got.Libs[r.Lib]] = r.NS
	}
	if seen["example.com/mod@v1.0.0"] != 1000 || seen["example.com/mod@v1.1.0"] != 900 {
		t.Errorf("versions did not keep their own timings: %v", seen)
	}
}

// An incomparable environment keeps its own file. The measurements are never
// refused -- an hour of benchmarking must not be lost to a flag typo -- but
// they never average into numbers they cannot be compared with either.
func TestIncompatibleEnvGetsItsOwnRun(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Run)
	}{
		{"goamd64", func(r *Run) { r.Env.GOAMD64 = "v1" }},
		{"corpus hash", func(r *Run) { r.CorpusHash = "different" }},
		{"src threads", func(r *Run) { r.SrcThreads = 1 }},
		{"build tags", func(r *Run) { r.Env.Tags = "withasm" }},
		// A taskset-limited run has the same CPU brand, so only NumCPU catches it.
		{"numcpu", func(r *Run) { r.Env.NumCPU = 4 }},
		{"gogc", func(r *Run) { r.Env.GOGC = 10 }},
		{"gomemlimit", func(r *Run) { r.Env.GOMemLimit = 1 << 30 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := Merge(dir, runFixture("v1.0.0", 1000, 1), false); err != nil {
				t.Fatal(err)
			}
			bad := runFixture("v1.0.0", 900, 1)
			tc.mutate(bad)
			res, err := Merge(dir, bad, false)
			if err != nil {
				t.Fatalf("the run must be stored, not refused: %v", err)
			}
			if len(res.Warnings) == 0 {
				t.Error("storing a run apart must say so")
			}
			if bad.ID == "test" {
				t.Fatal("the run kept the id it could not merge into")
			}

			base, err := LoadRun(dir, "demo", "test")
			if err != nil {
				t.Fatal(err)
			}
			if base.Encode[0].NS != 1000 {
				t.Errorf("the stored run was contaminated: NS = %d, want 1000", base.Encode[0].NS)
			}
			side, err := LoadRun(dir, "demo", bad.ID)
			if err != nil {
				t.Fatal(err)
			}
			if side == nil || side.Encode[0].NS != 900 {
				t.Fatalf("the incomparable run did not land in its own file: %+v", side)
			}

			// The sibling is named after the environment, not the clock, so the
			// same settings merge into it again instead of piling up files.
			again := runFixture("v1.0.0", 800, 1)
			tc.mutate(again)
			if _, err := Merge(dir, again, false); err != nil {
				t.Fatal(err)
			}
			if again.ID != bad.ID {
				t.Errorf("re-running the same environment landed in %s, want %s", again.ID, bad.ID)
			}
			ix, err := LoadIndex(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(ix.Runs) != 2 {
				t.Errorf("got %d runs, want the base and one sibling", len(ix.Runs))
			}
		})
	}
}

// The index has to carry what makes a sibling different, or the site unions it
// straight back into the run it was separated from.
func TestVariantReachesTheIndex(t *testing.T) {
	dir := t.TempDir()
	if _, err := Merge(dir, runFixture("v1.0.0", 1000, 1), false); err != nil {
		t.Fatal(err)
	}
	bad := runFixture("v1.0.0", 900, 1)
	bad.SrcThreads = 1
	if _, err := Merge(dir, bad, false); err != nil {
		t.Fatal(err)
	}
	ix, err := LoadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, s := range ix.Runs {
		seen[s.ID] = s.Variant["src_threads"]
		if s.Variant["goarm64"] != "" {
			t.Errorf("%s: empty gated fields must not be indexed: %v", s.ID, s.Variant)
		}
	}
	if seen["test"] != "8" || seen[bad.ID] != "1" {
		t.Errorf("src_threads did not reach the index: %v", seen)
	}
}

func TestNewRunStoresSeparately(t *testing.T) {
	dir := t.TempDir()
	if _, err := Merge(dir, runFixture("v1.0.0", 1000, 1), false); err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(dir, runFixture("v1.0.0", 900, 1), true); err != nil {
		t.Fatal(err)
	}
	ix, err := LoadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Runs) != 2 {
		t.Fatalf("got %d runs, want 2", len(ix.Runs))
	}
}

// A hand-edited or truncated file must not take the whole merge down with it.
func TestMergeSurvivesOutOfRangeLibIndex(t *testing.T) {
	dir := t.TempDir()
	r := runFixture("v1.0.0", 1000, 1)
	r.Encode[0].Lib = 99
	r.Decode[0].SrcLib = 99
	if _, err := Merge(dir, r, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(dir, runFixture("v1.0.0", 900, 1), false); err != nil {
		t.Fatalf("merging onto a file with a bad lib index: %v", err)
	}
}

// A window is a different setting, so it coexists with the default the same
// way a library version does, and the index learns it exists.
func TestMergeWindowsCoexist(t *testing.T) {
	dir := t.TempDir()
	if _, err := Merge(dir, runFixture("v1.0.0", 1000, 1), false); err != nil {
		t.Fatal(err)
	}
	win := runFixture("v1.0.0", 900, 1)
	win.Encode[0].Window, win.Decode[0].Window = 64<<10, 64<<10
	res, err := Merge(dir, win, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 2 {
		t.Fatalf("a new window should add rows, got %+v", res)
	}
	got, _ := LoadRun(dir, "demo", "test")
	if len(got.Encode) != 2 || len(got.Decode) != 2 {
		t.Fatalf("got %d/%d rows, want one per window", len(got.Encode), len(got.Decode))
	}
	if got.Encode[0].NS != 1000 || got.Encode[1].Window != 64<<10 || got.Encode[1].NS != 900 {
		t.Errorf("windows did not keep their own timings: %+v", got.Encode)
	}
	ix, err := LoadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	if w := ix.Runs[0].Windows; len(w) != 2 || w[0] != 0 || w[1] != 64<<10 {
		t.Errorf("index windows = %v, want [0 65536]", w)
	}
}

func TestRunFileRoundTrips(t *testing.T) {
	dir := t.TempDir()
	in := runFixture("v1.0.0", 1234567, 1)
	if _, err := Merge(dir, in, false); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(dir + "/runs/demo-test.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadRun(dir, "demo", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Save(dir); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(dir + "/runs/demo-test.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("write -> read -> write is not byte-identical")
	}
	if got.Encode[0].NS != 1234567 || got.Decode[0].Src != "zskp-2" {
		t.Errorf("values did not survive the round trip: %+v %+v", got.Encode[0], got.Decode[0])
	}
}
