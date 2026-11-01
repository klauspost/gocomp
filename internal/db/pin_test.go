package db

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPinColumn checks that the pin state is stored per row, that rows written
// before the column existed read as not recorded, and that a merge keeps the
// pin state of the row whose time it keeps.
func TestPinColumn(t *testing.T) {
	dir := t.TempDir()
	r := runFixture("v1.0.0", 1000, 1)
	r.Encode[0].Pin, r.Decode[0].Pin = 0, 1
	if _, err := Merge(dir, r, false); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRun(dir, "demo", "test")
	if err != nil {
		t.Fatal(err)
	}
	if got.Encode[0].Pin != 0 || got.Decode[0].Pin != 1 {
		t.Fatalf("pin = %d/%d, want 0/1", got.Encode[0].Pin, got.Decode[0].Pin)
	}

	// A faster pinned run takes over the row and its pin state; a slower one does not.
	fast := runFixture("v1.0.0", 500, 1)
	fast.Encode[0].Pin = 1
	slow := runFixture("v1.0.0", 2000, 1)
	for _, x := range []*Run{fast, slow} {
		if _, err := Merge(dir, x, false); err != nil {
			t.Fatal(err)
		}
	}
	got, _ = LoadRun(dir, "demo", "test")
	if got.Encode[0].NS != 500 || got.Encode[0].Pin != 1 {
		t.Errorf("after merges: NS=%d pin=%d, want 500 and 1", got.Encode[0].NS, got.Encode[0].Pin)
	}

	// Drop the column, as in a file written before it existed.
	p := filepath.Join(dir, got.fileName())
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(string(b), `,"v","pin"]`, `,"v"]`)
	s = regexp.MustCompile(`(?m)^(\s+\[.*),-?\d+\]`).ReplaceAllString(s, "$1]")
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = LoadRun(dir, "demo", "test")
	if err != nil {
		t.Fatal(err)
	}
	if got.Encode[0].Pin != -1 || got.Decode[0].Pin != -1 {
		t.Errorf("old rows: pin = %d/%d, want -1/-1", got.Encode[0].Pin, got.Decode[0].Pin)
	}
}
