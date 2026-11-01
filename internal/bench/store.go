package bench

import (
	"strconv"

	"github.com/klauspost/compress-site/internal/corpus"
	"github.com/klauspost/compress-site/internal/db"
	"github.com/klauspost/compress-site/internal/sysinfo"
)

// CorpusMeta is what a run records about the data it measured. It is captured
// before the loaded corpus is released, so a 10GB buffer is not still resident
// while the JSON is written.
type CorpusMeta struct {
	ID, Kind, Hash string
	Size           int64
	Files          int
}

// MetaOf snapshots the loaded corpus.
func MetaOf(d *corpus.Data) CorpusMeta {
	m := CorpusMeta{
		ID: d.ID, Kind: d.Kind.String(), Size: d.Total,
		Hash: strconv.FormatUint(d.Hash, 16), Files: len(d.Files),
	}
	if d.Kind == corpus.KindFile {
		m.Files = 1
	}
	return m
}

// ToRun converts measurements into a database run. The library version behind
// each row is part of its identity, which is what lets two versions of the same
// codec coexist after a rebuild.
func ToRun(meta CorpusMeta, results []Result, cfg Config, env sysinfo.Env, libs map[string]string) *db.Run {
	run := &db.Run{
		ID:          db.RunID(meta.ID, env.CPU.Brand, env.Go, Version),
		Corpus:      meta.ID,
		Kind:        meta.Kind,
		CorpusSize:  meta.Size,
		CorpusFiles: meta.Files,
		CorpusHash:  meta.Hash,
		Bench:       Version,
		MinPasses:   cfg.Passes,
		Tool:        sysinfo.ToolInfo(),
		Env:         env,
	}
	for _, r := range results {
		if r.Err != "" {
			run.Errors = append(run.Errors, db.RunError{
				Op: r.Op.String(), Codec: r.Codec, Level: r.Level, Window: r.Window, Threads: r.Threads, Msg: r.Err,
			})
			continue
		}
		lib := run.LibIndex(r.Module, libs[r.Module])
		switch r.Op {
		case OpEncode:
			run.Encode = append(run.Encode, db.EncRow{
				Codec: r.Codec, Level: r.Level, Window: r.Window, Threads: r.Threads, Lib: lib,
				In: r.In, Out: r.Out, NS: int64(r.Best), Passes: r.Passes,
				SD: r.SD, Verified: r.Verified, Pin: r.Pin,
			})
		case OpDecode:
			run.Decode = append(run.Decode, db.DecRow{
				Codec: r.Codec, Src: r.Src, Window: r.Window, Threads: r.Threads, Lib: lib,
				SrcLib: run.LibIndex(r.SrcModule, libs[r.SrcModule]), SrcThreads: r.SrcThreads,
				In: r.In, Out: r.Out, NS: int64(r.Best), Passes: r.Passes,
				SD: r.SD, Verified: r.Verified, Pin: r.Pin,
			})
		}
	}
	return run
}
