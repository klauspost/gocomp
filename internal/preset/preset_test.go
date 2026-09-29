package preset

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixture() Preset {
	return Preset{
		Name:    "nightly",
		Desc:    "the usual suspects",
		Corpora: []string{"enwik8", "gofiles"},
		Codecs:  map[string][]int{"zskp": {1, 2}, "gzkp": {-1, 1}, "snappy": nil},
		Threads: []int{1, 8, 32}, SrcThreads: 32,
		Encode: true, Decode: true,
		Passes: 5, MinWall: Dur(3 * time.Second), MaxJob: Dur(2 * time.Minute),
		Slow: true,
	}
}

func TestSaveLoadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	in := fixture()
	if err := in.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, "nightly")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "nightly" || got.Desc != in.Desc || got.SrcThreads != 32 || !got.Slow {
		t.Fatalf("scalars did not survive: %+v", got)
	}
	if time.Duration(got.MinWall) != 3*time.Second || time.Duration(got.MaxJob) != 2*time.Minute {
		t.Errorf("durations did not survive: %v %v", got.MinWall, got.MaxJob)
	}
	if got.CodecSpec() != in.CodecSpec() || got.CorpusSpec() != in.CorpusSpec() {
		t.Errorf("selection did not survive: %q %q", got.CodecSpec(), got.CorpusSpec())
	}
}

// Durations are written as "3s" rather than a nanosecond count because these
// files are edited by hand.
func TestDurationsAreHumanReadable(t *testing.T) {
	dir := t.TempDir()
	if err := fixture().Save(dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "nightly.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(b), `"min_wall": "3s"`) {
		t.Errorf("expected a readable duration, got:\n%s", b)
	}
}

// CodecSpec has to round-trip through the same parser the -codec flag uses,
// including negative levels, or a preset and a command line would not mean the
// same thing.
func TestCodecSpecIsSortedAndExplicit(t *testing.T) {
	p := Preset{Codecs: map[string][]int{"zskp": {1, 2}, "gzkp": {-3, -1, 1}, "br": nil}}
	if got, want := p.CodecSpec(), "br,gzkp:-3,-1,1,zskp:1,2"; got != want {
		t.Errorf("CodecSpec() = %q, want %q", got, want)
	}
}

func TestOpSpecNeverMeasuresNothing(t *testing.T) {
	for _, tc := range []struct {
		enc, dec bool
		want     string
	}{
		{true, true, "encode,decode"},
		{true, false, "encode"},
		{false, true, "decode"},
		{false, false, "encode,decode"},
	} {
		p := Preset{Encode: tc.enc, Decode: tc.dec}
		if got := p.OpSpec(); got != tc.want {
			t.Errorf("Encode=%v Decode=%v: got %q, want %q", tc.enc, tc.dec, got, tc.want)
		}
	}
}

// Names reach this from a TUI text field, so escaping the directory has to be
// impossible rather than merely unlikely.
func TestCheckNameRejectsTraversal(t *testing.T) {
	for _, bad := range []string{
		"", "..", "../evil", `..\evil`, "sub/name", `sub\name`, ".hidden", "with space",
		"semi;colon", "quote\"", "nul\x00",
	} {
		if err := CheckName(bad); err == nil {
			t.Errorf("CheckName(%q) accepted a bad name", bad)
		}
	}
	for _, ok := range []string{"nightly", "quick-2", "publish_full", "v1.2"} {
		if err := CheckName(ok); err != nil {
			t.Errorf("CheckName(%q) = %v, want accepted", ok, err)
		}
	}
	if err := (Preset{Name: "../escape"}).Save(t.TempDir()); err == nil {
		t.Error("Save accepted a traversing name")
	}
}

func TestListSortsAndMissingDirIsEmpty(t *testing.T) {
	ps, err := List(filepath.Join(t.TempDir(), "absent"))
	if err != nil || ps != nil {
		t.Fatalf("a missing preset directory should be empty, got %v %v", ps, err)
	}
	dir := t.TempDir()
	for _, n := range []string{"zeta", "alpha", "mid"} {
		if err := (Preset{Name: n, Encode: true}).Save(dir); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, err = List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 3 || ps[0].Name != "alpha" || ps[2].Name != "zeta" {
		t.Fatalf("List is not sorted or picked up a non-preset: %+v", ps)
	}
}

func TestLoadByPathAndUnknownName(t *testing.T) {
	dir := t.TempDir()
	if err := fixture().Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load("", filepath.Join(dir, "nightly.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "nightly" {
		t.Errorf("name should come from the file name, got %q", got.Name)
	}
	if _, err := Load(dir, "absent"); err == nil {
		t.Error("expected an error naming the missing preset")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
