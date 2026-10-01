# compress-site

Benchmarks Go compressors and decompressors purely in memory, and maintains the
JSON database behind the static comparison site in [`web/`](web).

```
go tool task build                 # or: go build -o compress-site .
./compress-site                    # interactive selection
./compress-site run                # small + medium corpora, every codec
./compress-site serve              # view the results at http://localhost:8080/
```

Everything is driven by [Task](https://taskfile.dev), declared as a `tool` in
`go.mod`, so a checkout needs no setup beyond a Go toolchain — `go tool task`
builds it on first use. `go tool task` on its own lists what there is.

## Build settings that change the numbers

Each of these is recorded with every run and most of them gate the merge, so a
difference produces separate rows instead of contaminated ones. Nothing here
needs acting on; it needs knowing.

- **`GOAMD64`.** A `v1` binary on a `v4` machine is 20-40% slower on some
  codecs. `compress-site version` shows both the level the binary was built for
  and the level the CPU supports. Build with `GOAMD64=v4 go build` if you want
  the faster one.
- **The Go toolchain is pinned** to `go1.27.1` in `go.mod`. The Go version is a
  database dimension, so leaving it to whatever is installed would split one
  machine's history across buckets for no reason.
- **`GO111MODULE=off` does not work.** `debug.BuildInfo.Deps` is empty in GOPATH
  mode, so no library version can be recorded and every measurement becomes
  unattributable. Build in module mode.

`GOGC` and `GOMEMLIMIT` are recorded too, as is whether the machine reports a
hypervisor.

## Corpora

`compress-site corpus list` shows the catalog with its state:

| state | meaning |
|---|---|
| `adopted` | an existing plain file or directory in the cache root is used as-is |
| `cached` | downloaded by this tool, and extracted where the format needs it |
| `generated` | synthesised locally, never downloaded (`rand2gb`) |
| `missing` | run `compress-site corpus fetch <id>` |
| `manual` | not hosted; place the file in the cache root yourself — no corpus needs this any more |

The cache root defaults to `./testdata`, and `-cache` or `$COMPRESS_SITE_CACHE`
overrides it. **If you already have these files, point the cache at them**:

```
./compress-site corpus list -cache /path/to/your/corpora
```

Every corpus entry lists the plain filenames it will accept, so a directory that
already holds `enwik9`, `silesia.tar`, `gofiles/` and the rest is adopted in
place with nothing downloaded and nothing copied.

Otherwise `corpus fetch` downloads from <https://klauspost.com/files/compress/>
into `dl/`, and everything is handled in pure Go, so there is no manual
repackaging step. Downloads resume if interrupted.

**A single-file corpus in a `.zst` is never extracted.** Loading decompresses
the download straight into the memory the benchmark uses anyway, which costs
about what reading the plain file off disk would, so the cache holds downloads
only — 16 GB for every hosted corpus rather than another 86 GB of copies beside
them. Directory corpora (`.7z`, `.zip`) and the one `.xz` are slow enough to
decode that they are unpacked into `ex/` once, as before.

Exactly one compression container is unwrapped, so `10gb.tar.zst` yields
`10gb.tar` — the tar *is* the corpus and is never unpacked further.

Two corpora need a word:

- **`silesia`** is a tar of the 12 files that upstream ships loose, so the
  packing is a choice rather than a canonical artifact. One packing is hosted as
  `silesia.tar.zst`; an existing `silesia.tar` in the cache root is still
  adopted ahead of it. Every run records the corpus xxh3, so a tar built
  differently shows up as a different hash and the merge gate holds those runs
  apart rather than averaging them together.
- **`coronavirus`** is a 1.8 MB download that expands to 1.3 GB. Download size
  is not a useful guide to memory; `corpus list` shows the real size.

### Memory

Benchmarks run entirely in RAM. Peak is roughly 3.5× the corpus, because the
worst case is a store-only level where the "compressed" copy is as large as the
source. The tool refuses a corpus needing more than 60% of physical memory;
`-force` overrides. The 10 GB-class corpora want ~48 GB.

## Running

```
compress-site run       run benchmarks and merge them into the JSON database
compress-site tui       interactive selection (the default with no arguments)
compress-site corpus    list | fetch <id|tier|all> | verify | rm | path
compress-site validate  round-trip integrity check with xxh3; writes nothing
compress-site list      codecs | corpora | runs | presets
compress-site serve     serve web/ over HTTP so the site loads as ES modules
compress-site version   tool, Go, library and environment information
```

Useful invocations:

```
# Everything, at every thread count, including the slow codecs.
compress-site run -corpus=all -cpu=sweep -slow

# One corpus, two codecs, single- and 16-thread.
compress-site run -corpus=enwik9 -codec=zskp,s2 -cpu=1,16

# Level sweeps with an explicit list per codec.
compress-site run -codec="zskp:1-4,s2,gzkp:1,5,9"

# Window and block sizes, next to each codec's default.
compress-site run -codec=zskp,s2,minlz -window=default,64K,1M,8M

# Check that everything round-trips, write nothing.
compress-site validate -corpus=gofiles -codec=all

# Run a saved selection unattended.
compress-site run -preset quick
```

`-codec` items are comma separated; an item with a colon starts a new codec and a
purely numeric item continues the previous one's level list. Codec ids are never
numeric, so `zskp:1-4,s2,gzkp:1,5,9` is unambiguous. In a level list a leading
`-` is a sign and any other `-` is a range, so `-2` is one level and `-3--1` is
three.

Decoders follow the codec selection. Each selected encoder's output is decoded
by its own implementation (`br2`'s by `br`) and by every other selected codec
of the same format, so `-codec=zskp` times one decoder and
`-codec=zskp,stdzstd` has each decode both. The two readers with no encoder of
their own come with the codec they belong to: the standard library's bzip2
reader with `bz2`, S2's snappy-frame reader with `s2s`. `-decoders=all` decodes
every output with every decoder of its format.

Progress goes to stderr; one TSV row per measurement goes to stdout.

### Window sizes

`-window` sweeps the window — or, for the codecs that split their input into
independent blocks, the block size — like `-cpu` sweeps threads. Sizes are
powers of two written `8K`, `64KB`, `1M`; `default` is each codec's own choice
and is what runs when the flag is absent. `-window=64K,1M` measures only those
two; `-window=default,64K,1M` measures them beside the default.

Each codec lists the sizes it takes, and a requested size it does not take is
**skipped, never clamped**, so one list can drive every codec without measuring
a clamped setting twice under two names. A codec with a fixed window runs at
`default` only, and not at all if `default` is not in the list. Once the corpus
is loaded, windows that cannot differ are dropped too: of the sizes at or above
the largest single input (the file, or a directory's largest member), only the
smallest runs. A size equal to the codec's default *is* the default: `2M` on
`minlz` measures and stores its default row, never a second copy of it.

| codec | setting | sizes | `default`, as the library sets it |
|---|---|---|---|
| `zskp` | window | 1K–512M | 4M at fastest, 8M above |
| `stdzstd` | window | 128K–512M | 2M–8M by level |
| `s2` | block | 4K–4M | 1M |
| `s2s` | block | 4K–64K | 64K−8 (snappy's limit) |
| `minlz` | block | 4K–8M | 2M |
| `lz4` | block | 64K, 256K, 1M, 4M | 4M |
| `pgzip` | block | 32K–64M | 1M |
| `br` | window | 1K–16M | 4M |
| `xz`, `lzma2` | dictionary | 64K–2G | 8M |

gzip and deflate (fixed 32K), snappy, bzip2 (its level *is* the block size),
`br2` (no window option) and the block-API codecs have no window to vary.
`compress-site run -dry-run` lists the sizes per codec.

Large windows cost memory the RAM guard does not count: `zskp` with concurrent
blocks keeps a window per worker, so 512M at 32 threads is 16 GB.

**Use `run`, not the TUI, for numbers you intend to publish.** A terminal
redrawing during a measurement is noise the batch path does not have.

## Presets

A preset is a saved selection — corpora, codecs and levels, threads, operations,
pass settings — in one JSON file under `presets/`. The TUI writes them and the
command line runs them, so the suite you assembled interactively is the suite
that runs unattended:

```
compress-site list presets                 # what is saved
compress-site run -preset quick            # run it, no TUI
compress-site run -preset quick -corpus=enwik9   # the flag wins over the file
compress-site validate -preset quick
```

In the TUI, `p` opens the preset browser from any selection screen: `enter`
loads, `R` loads and runs, `s` saves the current selection under a name, `d`
deletes. `-preset` also takes a path, so
`run -preset testdata/tui-state.json` replays the last interactive selection.

Presets live in the repo rather than the cache because they are meant to be
committed: a suite only means something together with the benchmark version it
was defined against. `-presets` points somewhere else.

Any flag named explicitly on the command line overrides the preset, and any
field the preset omits keeps its default — a three-line preset naming two
corpora is valid.

A preset holds **what to measure, never where to put it**. `-out` and `-cache`
are absent by design, so a committed preset works on a machine whose corpora
live elsewhere. Thread counts are an explicit list rather than `sweep`, because
`sweep` means different numbers on different machines and that is exactly the
incomparability the merge gate exists to catch. `-cpu=sweep` still works on the
command line.

## The database

`web/data/index.json` is the main database: one entry per run, plus the codec,
corpus and machine dictionaries the site needs to render its filters. Each run's
measurements live in `web/data/runs/<corpus>-<id>.json`, fetched lazily.

A run is identified by **corpus, CPU model, Go version and benchmark version**.
Re-running the same combination merges: the better time wins, pass counts
accumulate, and newly added codecs are unioned in. `-new-run` stores a run
separately instead.

Library version is part of each measurement's identity, so rebuilding against a
new `klauspost/compress` adds rows beside the old ones rather than overwriting
them — that is what makes "compare library versions" work on the site. Window
size is part of it the same way.

Runs do not merge when `GOOS`, `GOARCH`, `GOAMD64`, `GOARM64`, build tags,
`GOEXPERIMENT`, cgo, source thread count, core count, `GOGC`,
`GOMEMLIMIT` or the corpus hash differ. Those are excluded from the run id on
purpose, and the gate is what stops a `GOAMD64=v1` run silently averaging into
a `v4` one — or a `taskset`-limited run, which has the same CPU brand and only
differs in core count.

**Nothing is lost when the gate trips.** The run is stored beside the one it
could not join, under an id suffixed with a hash of those settings, and a note
says which field differed. Because the suffix is the settings themselves, the
next run with the same settings merges into that file rather than making a new
one. The site lists them under **Settings** and plots them as separate lines,
so two `-src-threads` values never average together. `-new-run` still forces a
separate entry even when the run would have merged cleanly.

### Cleaning up

In the TUI, `D` opens the database from any selection screen. Tick runs with
`space` (`a` ticks all), then choose what to delete from them:

- a **codec**, by `space`: its own rows, meaning its encode rows and the decode
  rows it decoded;
- a codec **as source**, by `s`: the decode rows of its output, whichever
  decoder read it;
- a **library version**: every row measured with it, including decodes of
  output it produced.

`d` deletes the ticked rows from the ticked runs, and `X` deletes the ticked
runs outright. Both show what they will remove and wait for `y`. A run left
with no rows is removed, and so is a machine no run is left on. Beside each
codec and library is the number of runs holding it: of the ticked runs, or of
all runs while none is ticked. Runs are listed newest first.
Each run is re-read from disk before it is written, so a merge
made while the pane was open is kept. `web/data` is in git, which is the undo.

## The site

`web/` is a plain static directory: no build step, no npm, no framework. Chart.js
is vendored unmodified under `web/vendor/` (see `PROVENANCE.txt` for versions and
hashes) and resolved through an import map.

ES modules do not load from `file://`, so serve it:

```
compress-site serve                 # opens a browser at http://localhost:8080/
compress-site serve -open=false     # just listen
```

Up to eight codecs are highlighted at a time; the rest stay as grey context and
are still readable by hover, the table twin and the CSV. Click a codec in the
legend, the chart or the table to move it into a colour slot. Eight is not a
round number: it is the largest palette that still clears the colourblind
separation floors with every pair on screen at once, which is what a scatter
puts there, and dark mode is what caps it — its lightness band is the narrower
one. Each slot also carries its own marker shape and dash pattern, so identity
never rests on hue alone; that matters more at eight slots than at five,
because eight separable hues cost tritan separation.

Sizes and throughput are SI — `MB` is 10^6 bytes, not 2^20 — compression
ratios read `3.412:1`, and size is quoted as **% reduction** so that, like
every other measure on the page, more is better and a default sort puts the
best codec on top. The database stores raw bytes and nanoseconds, so all of
this is purely a display choice.

The main chart puts **throughput on the vertical axis and compression ratio on
the horizontal**, one line per codec through its levels; up and to the right is
better. Both panels share a ratio scale so a codec sits in the same column in
each and you read down, while the speed scales stay independent because
decoding is several times faster and one shared scale would flatten the
compression panel. The speed axis is **linear from zero unless you switch it to
log** — throughput spans two orders of magnitude, so log is often the readable
choice, but it flatters the slow end and nobody should read one without having
chosen it.

**Only regular levels are shown by default.** Every level is declared in the
codec registry as one of three classes, and `Levels` has an on/off button for
each:

- **Regular** — a point on the codec's speed/size dial.
- **Uncompressed** — stores rather than compresses. Nine codecs have one, and a
  single such measurement sets the top of the throughput axis and wins the
  ranking outright, which buries every codec doing actual work. Turn it on when
  the memcpy ceiling is what you want.
- **Special** — compresses, but by a different method than the dial:
  deflate's `huffman` (-2, all five deflate-family codecs) and `stateless` (-3,
  `gzkp` and `flkp`).

A decode measurement takes its source's class, so decoding a stored frame is
Uncompressed whatever the decoder. The class is declared, never inferred from
the level number: `lz4` level 0 is `fast` and compresses, so it is Regular.

**Only the default window is shown by default.** A window sweep is an
experiment, and mixing its sizes into the headline charts would put one codec on
screen several times. The `Window` chips appear once a run holds more than one
size; `Compare by → Window size` draws one series per window, best read with a
single codec selected. A default row shows the size it stands for — `minlz`'s
as 2M — and that size's chip selects it too.

The filter row describes what the data holds, not what the schema allows. A
control with one option is not shown, and the options cascade: narrowing to one
Go version drops the codecs only the other one ever ran — including any that
were ticked — and hides Decompress entirely if nothing in the slice was
decoded. Corpus kind and corpus are exempt from being narrowed by the filters
below them, so picking one corpus can never delete the route to another. Lists
are collapsed checkbox groups with a `clear` beside the label; nothing ticked
means no filter, which is why the summary reads `all`.

The whole filter state lives in the URL hash, so copying the address bar shares
the exact view — corpus, CPU, Go version, threads, windows, operation, codecs, levels,
library versions, which codecs are highlighted, which tables are open and how
they are sorted. Theme is deliberately excluded: a shared link should not force
the recipient's colour scheme.

## Development

```
go tool task              list every task
go tool task check        what CI runs: lint, race tests, build
go tool task test         all tests, including the concurrency-declaration probe
go tool task lint:fix     golangci-lint with --fix
go tool task bench -- -corpus=enwik8 -codec=zskp
```

`task`, `golangci-lint` and `gofumpt` are `tool` directives in `go.mod`, so
their versions are pinned like any dependency and nothing needs installing.

`task test` includes `TestConcurrencyDeclarations`, which times each codec at 1
and 8 threads on a 256 MB buffer and fails when a `Conc` declaration disagrees
with reality. CI skips it — a two-core shared runner cannot measure whether a
codec parallelises — so run it locally before trusting a new registration.

## What is measured

- Everything is in memory. No file I/O inside a timed region.
- `runtime.GC()` runs immediately before the clock starts, never inside it.
- Measurements are interleaved — every one runs once, then the set repeats — so
  thermal drift does not favour whichever codec happened to go first.
- The recorded time is the **minimum** over N passes. Fast measurements get more
  passes automatically, up to `-max-passes`.
- Thread count is swept with `-cpu`, Go-benchmark style. `GOMAXPROCS` is set per
  measurement, and each library's own concurrency option is set to match.
- Decode inputs are produced **once** at a fixed thread count (`-src-threads`),
  so the decode sweep measures decoder scaling rather than a changing block
  layout. For a single-file corpus that input is the timed encode's own
  output: a variant's decoders run against it the moment its last encode pass
  at that thread count lands, so it is never encoded a second time.
- Single-file and many-file corpora are separate: the first uses streaming APIs,
  the second block APIs with instance reuse, and they are never mixed in a chart.

`compress-site list codecs` shows, per codec and direction, which corpus kinds
actually parallelise. For the others the thread count is recorded but changes
nothing in the codec itself.
