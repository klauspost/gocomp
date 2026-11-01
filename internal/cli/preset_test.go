package cli

import (
	"flag"
	"testing"
	"time"

	"github.com/klauspost/gocomp/internal/preset"
)

func savedPreset(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := preset.Preset{
		Name:    "nightly",
		Corpora: []string{"enwik8", "gofiles"},
		Codecs:  map[string][]int{"zskp": {1, 2}, "s2": {1}},
		Threads: []int{1, 8}, SrcThreads: 16,
		Encode: true, Passes: 7, MinWall: preset.Dur(4 * time.Second),
		Slow: true,
	}
	if err := p.Save(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func parseRun(t *testing.T, args ...string) runOpts {
	t.Helper()
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	var o runOpts
	o.bind(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	if err := o.loadPreset(fs); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestPresetFillsTheRunOptions(t *testing.T) {
	dir := savedPreset(t)
	o := parseRun(t, "-presets", dir, "-preset", "nightly")

	if o.corpora != "enwik8,gofiles" {
		t.Errorf("corpora = %q", o.corpora)
	}
	if o.codecs != "s2:1,zskp:1,2" {
		t.Errorf("codecs = %q", o.codecs)
	}
	if o.cpu != "1,8" || o.srcCPU != 16 {
		t.Errorf("threads = %q/%d", o.cpu, o.srcCPU)
	}
	if o.ops != "encode" {
		t.Errorf("ops = %q, want encode only", o.ops)
	}
	if o.passes != 7 || o.minWall != 4*time.Second || !o.slow {
		t.Errorf("config did not come through: %+v", o)
	}
	// Untouched by the preset, so still the flag default.
	if o.windows != "default" || o.out != "web/data" {
		t.Errorf("a preset must not disturb defaults it does not set: %+v", o)
	}
}

// The command line wins. This is the one that breaks if the explicit values are
// snapshotted after the preset overwrites the very variables flag.Value points
// at, which is exactly the shape of the bug.
func TestExplicitFlagsBeatThePreset(t *testing.T) {
	dir := savedPreset(t)
	o := parseRun(t, "-presets", dir, "-preset", "nightly",
		"-corpus", "enwik9", "-cpu", "32", "-passes", "2", "-slow=false")

	if o.corpora != "enwik9" {
		t.Errorf("corpora = %q, want the command line to win", o.corpora)
	}
	if o.cpu != "32" {
		t.Errorf("cpu = %q, want the command line to win", o.cpu)
	}
	if o.passes != 2 {
		t.Errorf("passes = %d, want the command line to win", o.passes)
	}
	if o.slow {
		t.Error("-slow=false must override a preset that sets it")
	}
	// Not named on the command line, so still from the preset.
	if o.codecs != "s2:1,zskp:1,2" || o.srcCPU != 16 {
		t.Errorf("unnamed flags should keep the preset value: %+v", o)
	}
}

func TestPresetSelectionParsesBackIntoTheSameThing(t *testing.T) {
	o := parseRun(t, "-presets", savedPreset(t), "-preset", "nightly")
	sels, err := parseCodecs(o.codecs)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]int{"s2": {1}, "zskp": {1, 2}}
	if len(sels) != len(want) {
		t.Fatalf("got %d codecs, want %d: %+v", len(sels), len(want), sels)
	}
	for _, s := range sels {
		w, ok := want[s.id]
		if !ok {
			t.Fatalf("unexpected codec %q", s.id)
		}
		if len(s.levels) != len(w) {
			t.Fatalf("%s: got levels %v, want %v", s.id, s.levels, w)
		}
		for i := range w {
			if s.levels[i] != w[i] {
				t.Fatalf("%s: got levels %v, want %v", s.id, s.levels, w)
			}
		}
	}
}

func TestMissingPresetIsAnError(t *testing.T) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	var o runOpts
	o.bind(fs)
	if err := fs.Parse([]string{"-presets", t.TempDir(), "-preset", "absent"}); err != nil {
		t.Fatal(err)
	}
	if err := o.loadPreset(fs); err == nil {
		t.Fatal("expected an error for a preset that does not exist")
	}
}
