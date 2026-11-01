package db

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

// Measurements are stored column-oriented, one row per line: about four times
// smaller than repeated object keys, and a clean line diff in git. "cols" is
// written so a reader can assert the order rather than hard-code it.
var (
	encCols = []string{"codec", "level", "window", "threads", "lib", "in", "out", "ns", "passes", "sd", "v", "pin"}
	decCols = []string{"codec", "src", "window", "srclib", "srcthreads", "threads", "lib", "in", "out", "ns", "passes", "sd", "v", "pin"}
)

type table struct {
	Cols []string          `json:"cols"`
	Rows []json.RawMessage `json:"rows"`
}

type runWire struct {
	Run
	EncodeTable table `json:"encode"`
	DecodeTable table `json:"decode"`
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

func (r EncRow) values() []any {
	return []any{r.Codec, r.Level, r.Window, r.Threads, r.Lib, r.In, r.Out, r.NS, r.Passes, round3(r.SD), r.Verified, r.Pin}
}

func (r DecRow) values() []any {
	return []any{r.Codec, r.Src, r.Window, r.SrcLib, r.SrcThreads, r.Threads, r.Lib, r.In, r.Out, r.NS, r.Passes, round3(r.SD), r.Verified, r.Pin}
}

func encodeRun(r *Run) ([]byte, error) {
	head, err := json.MarshalIndent(r, "", " ")
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.Write(bytes.TrimRight(head[:len(head)-1], " \n"))
	b.WriteString(",\n")
	if err := writeTable(&b, "encode", encCols, len(r.Encode), func(i int) []any { return r.Encode[i].values() }); err != nil {
		return nil, err
	}
	b.WriteString(",\n")
	if err := writeTable(&b, "decode", decCols, len(r.Decode), func(i int) []any { return r.Decode[i].values() }); err != nil {
		return nil, err
	}
	b.WriteString("\n}\n")
	return b.Bytes(), nil
}

func writeTable(b *bytes.Buffer, name string, cols []string, n int, row func(int) []any) error {
	c, err := json.Marshal(cols)
	if err != nil {
		return err
	}
	fmt.Fprintf(b, " %q: {\n  \"cols\": %s,\n  \"rows\": [", name, c)
	for i := range n {
		v, err := json.Marshal(row(i))
		if err != nil {
			return err
		}
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("\n   ")
		b.Write(v)
	}
	if n > 0 {
		b.WriteString("\n  ")
	}
	b.WriteString("]\n }")
	return nil
}

// colIndex maps the stored column order onto the one this build expects.
func colIndex(cols []string) map[string]int {
	m := make(map[string]int, len(cols))
	for i, c := range cols {
		m[c] = i
	}
	return m
}

type rowReader struct {
	vals []json.RawMessage
	idx  map[string]int
	err  error
}

func (r *rowReader) str(name string) string {
	var s string
	r.get(name, &s)
	return s
}

func (r *rowReader) i64(name string) int64 {
	var v int64
	r.get(name, &v)
	return v
}

func (r *rowReader) num(name string) int {
	var v int
	r.get(name, &v)
	return v
}

func (r *rowReader) f64(name string) float64 {
	var v float64
	r.get(name, &v)
	return v
}

// verified defaults to "not checked" for rows written before the field existed.
func (r *rowReader) verified() int {
	if _, ok := r.idx["v"]; !ok {
		return -1
	}
	return r.num("v")
}

// pin defaults to "not recorded" for rows written before the field existed.
func (r *rowReader) pin() int {
	if _, ok := r.idx["pin"]; !ok {
		return -1
	}
	return r.num("pin")
}

func (r *rowReader) get(name string, dst any) {
	i, ok := r.idx[name]
	if !ok || i >= len(r.vals) {
		if r.err == nil {
			r.err = fmt.Errorf("missing column %q", name)
		}
		return
	}
	if err := json.Unmarshal(r.vals[i], dst); err != nil && r.err == nil {
		r.err = fmt.Errorf("column %q: %w", name, err)
	}
}

func decodeEncRows(t table) ([]EncRow, error) {
	idx := colIndex(t.Cols)
	out := make([]EncRow, 0, len(t.Rows))
	for i, raw := range t.Rows {
		var vals []json.RawMessage
		if err := json.Unmarshal(raw, &vals); err != nil {
			return nil, fmt.Errorf("row %d: %w", i, err)
		}
		r := &rowReader{vals: vals, idx: idx}
		out = append(out, EncRow{
			Codec: r.str("codec"), Level: r.num("level"), Window: r.num("window"), Threads: r.num("threads"),
			Lib: r.num("lib"), In: r.i64("in"), Out: r.i64("out"), NS: r.i64("ns"),
			Passes: r.num("passes"), SD: r.f64("sd"), Verified: r.verified(), Pin: r.pin(),
		})
		if r.err != nil {
			return nil, fmt.Errorf("row %d: %w", i, r.err)
		}
	}
	return out, nil
}

func decodeDecRows(t table) ([]DecRow, error) {
	idx := colIndex(t.Cols)
	out := make([]DecRow, 0, len(t.Rows))
	for i, raw := range t.Rows {
		var vals []json.RawMessage
		if err := json.Unmarshal(raw, &vals); err != nil {
			return nil, fmt.Errorf("row %d: %w", i, err)
		}
		r := &rowReader{vals: vals, idx: idx}
		out = append(out, DecRow{
			Codec: r.str("codec"), Src: r.str("src"), Window: r.num("window"), SrcLib: r.num("srclib"),
			SrcThreads: r.num("srcthreads"), Threads: r.num("threads"), Lib: r.num("lib"),
			In: r.i64("in"), Out: r.i64("out"), NS: r.i64("ns"),
			Passes: r.num("passes"), SD: r.f64("sd"), Verified: r.verified(), Pin: r.pin(),
		})
		if r.err != nil {
			return nil, fmt.Errorf("row %d: %w", i, r.err)
		}
	}
	return out, nil
}
