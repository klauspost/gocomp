// In-memory database: ingest, scope resolution and querying. Pure data — this
// module never touches the DOM and never fetches anything itself.

import { levelWord, windowLabel } from '../format.js';
import { METRICS, paretoFront, ratio, speed } from './metrics.js';

export class QueryError extends Error {}

// Level classes in display order; the registry's names for them.
export const LEVEL_CLASSES = ['regular', 'uncompressed', 'special'];

export async function openDB(loader) {
  const index = await loader.index();
  const rows = []; // flat, denormalised; one entry per measurement
  const ingested = new Set();

  const corpora = new Map(index.corpora.map((c) => [c.id, c]));
  const codecs = new Map(index.codecs.map((c) => [c.id, c]));
  const machines = new Map(Object.entries(index.machines || {}));
  const vfields = variantFields(index.runs);
  const variantOf = (r) => vfields.map((f) => `${f}=${r.variant[f] ?? ''}`).join(' · ');
  const libsOf = (r) => Object.entries(r.libs || {}).flatMap(([m, vs]) => vs.map((v) => `${m}@${v}`));
  // A level's word only means anything next to its codec -- level 1 is
  // "fastest" for zstd and "default" for s2 -- so it is resolved here, where
  // the codec is known, and rides on the row. Views then name a measurement
  // without being handed the whole database to do it.
  const levelOf = (codec, n) => levelWord(codecs.get(codec), n);

  // The class each codec declares for its levels. Uncompressed ones are real
  // measurements -- the memcpy ceiling -- but one of them sets the top of the
  // throughput axis and wins the ranking outright; special ones (deflate's
  // huffman-only and stateless) compress by another method than the dial the
  // regular levels sit on. Declared in the registry, never inferred from the
  // level number: lz4-0 and snappy-0 are level 0 and compress perfectly well.
  const classes = new Map();
  const codecClasses = new Map();
  // The size window 0 stands for at each level: minlz's default is 2M, so it
  // is what the 2M chip shows of minlz. The database never holds both, since
  // a sweep size equal to the default is stored as the default.
  const defWindows = new Map();
  for (const c of index.codecs) {
    const cs = new Set();
    for (const l of c.encode?.levels ?? []) {
      classes.set(`${c.id}/${l.n}`, l.class || 'regular');
      cs.add(l.class || 'regular');
      if (l.window) defWindows.set(`${c.id}/${l.n}`, l.window);
    }
    codecClasses.set(c.id, cs);
  }
  const classOf = (codec, level) => classes.get(`${codec}/${level}`) || 'regular';
  const defSizes = new Set(defWindows.values());

  // The filters that choose which runs to read. Corpus kind is not among them
  // because it partitions everything below it: narrowing it by the corpus
  // already picked would delete the way back to the other half of the data.
  const has = (sel, v) => !sel || !sel.length || sel.includes(v);
  const SCOPE = {
    corpora: (r, s) => has(s.corpora, r.corpus),
    machines: (r, s) => has(s.machines, r.machine),
    gos: (r, s) => has(s.gos, r.go),
    bench: (r, s) => !s.bench || r.bench === s.bench,
    variants: (r, s) => has(s.variants, variantOf(r)),
  };

  // scoped applies the kind and every scope filter but one, so a control can
  // offer the alternatives its own selection is hiding. Threads, codecs and
  // libraries deliberately do not appear: they select rows inside the runs,
  // and letting "32 threads" delete corpora strands the reader in whichever
  // slice they happened to open.
  const scoped = (s, except) => (s
    ? index.runs.filter((r) => (!s.kind || r.kind === s.kind)
      && Object.entries(SCOPE).every(([f, ok]) => f === except || ok(r, s)))
    : index.runs);

  function ingest(stub, file) {
    if (ingested.has(stub.id)) return;
    ingested.add(stub.id);
    const libs = file.libs || [];
    const base = {
      runId: stub.id, corpus: stub.corpus, kind: stub.kind, machine: stub.machine,
      go: stub.go, goarch: stub.goarch, goamd64: stub.goamd64 || '', bench: stub.bench,
      variant: variantOf(stub),
    };

    const push = (op, r) => {
      // A zero or negative time cannot be plotted on a log axis and cannot be
      // a real measurement; drop it loudly rather than poisoning the scale.
      if (!(r.ns > 0) || !(r.in > 0)) return;
      const sp = speed(op, r.in, r.out, r.ns);
      const rt = ratio(op, r.in, r.out);
      rows.push({
        ...base, op,
        codec: r.codec, level: r.level ?? null, threads: r.threads, window: r.window,
        // The window in bytes, the default resolved; 0 where the codec's is
        // fixed. A decode row's window is its source's.
        size: r.window || defWindows.get(op === 'c' ? `${r.codec}/${r.level}` : `${r.srcCodec}/${r.srcLevel}`) || 0,
        levelName: levelOf(r.codec, r.level ?? null),
        src: r.src || null, srcCodec: r.srcCodec || null, srcLevel: r.srcLevel ?? null,
        // "minlz--1" is a storage key, not a label. The decode source is named
        // the same way an encode row is, so the two read alike.
        srcLabel: r.src ? `${r.srcCodec} ${levelOf(r.srcCodec, r.srcLevel ?? null)}`.trimEnd() : null,
        lib: libs[r.lib] || '', libVer: verOf(libs[r.lib]),
        // The library that built the bytes being decoded. Not displayed, but
        // it is half of the decode row's identity: one unchanged stdlib
        // decoder reading gzip from two versions of klauspost/compress is two
        // rows that are otherwise the same measurement.
        srcLibVer: verOf(libs[r.srclib]),
        in: r.in, out: r.out, ns: r.ns, passes: r.passes, sd: r.sd,
        verified: r.verified,
        // A decode measurement takes its *source's* class: a decoder reading
        // a stored frame is measuring a copy, whatever the decoder's own id.
        levelClass: op === 'c' ? classOf(r.codec, r.level) : classOf(r.srcCodec, r.srcLevel),
        ratio: rt, pct: rt ? 100 - 100 / rt : 0,
        cspeed: op === 'c' ? sp : 0, dspeed: op === 'd' ? sp : 0,
      });
    };

    for (const r of tableRows(file.encode)) push('c', r);
    for (const r of tableRows(file.decode)) {
      const [sc, sl] = splitVariant(r.src);
      push('d', { ...r, srcCodec: sc, srcLevel: sl });
    }
  }

  const db = {
    index,
    corpora,
    codecs,
    machines,
    rowCount: () => rows.length,

    // domains drive the filter controls and come from the manifest alone, so
    // they are available before any run file is fetched. A scope filter's
    // domain leaves its own selection out, so a list still offers what it is
    // currently hiding. Everything below the scope -- threads, codecs,
    // libraries, which operations exist -- is simply what the chosen runs
    // hold, which is how a Go version takes its codecs with it when it goes.
    // Pass no state for the unconstrained set.
    domains(s) {
      const all = scoped(s, null);
      const threads = uniq(all.flatMap((r) => r.threads)).sort((a, b) => a - b);
      return {
        kinds: uniq(index.runs.map((r) => r.kind)),
        corpora: uniq(scoped(s, 'corpora').map((r) => r.corpus)),
        machines: uniq(scoped(s, 'machines').map((r) => r.machine)),
        gos: uniq(scoped(s, 'gos').map((r) => r.go)),
        benches: uniq(scoped(s, 'bench').map((r) => r.bench)),
        variants: vfields.length ? uniq(scoped(s, 'variants').map(variantOf)) : [],
        goamd64: uniq(all.map((r) => r.goamd64 || '')).filter(Boolean),
        threads,
        windows: uniq(all.flatMap((r) => r.windows)).sort((a, b) => a - b),
        // What an unset filter becomes. Mixing CPUs or thread counts splits
        // every line and raises the banner, so the default is one of each:
        // the CPU with the most measurements, and one thread -- the codec
        // alone -- unless the slice has none, then the busiest thread count.
        picks: {
          machines: most(scoped(s, 'machines'), (r) => [r.machine]),
          threads: threads.includes(1) ? [1] : most(all, (r) => r.threads),
        },
        codecs: uniq(all.flatMap((r) => r.codecs)),
        libs: uniq(all.flatMap(libsOf)),
        ops: ['c', 'd'].filter((op) => all.some((r) => (op === 'c' ? r.n_encode : r.n_decode) > 0)),
        // Index-only, so this says a codec in scope *has* a level of the
        // class, not that one was measured -- generous in the same direction
        // as the row filters above, and never hiding a control that is needed.
        levelClasses: LEVEL_CLASSES.filter((cl) => all.some((r) => r.codecs.some((id) => codecClasses.get(id)?.has(cl)))),
      };
    },

    // resolveScope is synchronous and index-only: which run files a state needs
    // and what is questionable about combining them.
    resolveScope(s) {
      // Threads stays out of SCOPE because it must not narrow the corpus or
      // machine lists -- it selects rows inside a run. But a run whose sweep
      // does not contain a single selected thread count contributes no rows at
      // all, and leaving it here made the banner warn about data that is not
      // on screen: two enwik9 runs swept [1] and [16], so picking 16 threads
      // reported that src_threads differed when only the 16-thread run had a
      // row in view. It is also a run file fetched for nothing.
      // Windows follow the same rule: most runs hold only the default, and a
      // run without the selected window has nothing to put on screen. Default
      // rows count for every size that is some codec's default.
      const runs = scoped(s, null).filter((r) => (!s?.threads?.length
        || !r.threads?.length || r.threads.some((n) => s.threads.includes(n)))
        && (!s?.windows?.length || r.windows.some((w) => s.windows.includes(w)
          || (w === 0 && s.windows.some((x) => defSizes.has(x))))));

      const warnings = [];
      const note = (code, field, vals) => {
        if (vals.length > 1) {
          warnings.push({ code, text: `${field} differs across the selected runs: ${vals.join(', ')}` });
        }
      };
      note('MIXED_MACHINE', 'CPU', uniq(runs.map((r) => machines.get(r.machine)?.label || r.machine)));
      note('MIXED_GO', 'Go version', uniq(runs.map((r) => r.go)));
      note('MIXED_GOAMD64', 'GOAMD64', uniq(runs.map((r) => r.goamd64 || '(unset)')));
      note('MIXED_BENCH', 'Benchmark version', uniq(runs.map((r) => r.bench)));

      // One line per setting that actually differs here, rather than each
      // run's whole environment: the reader is looking for the difference, and
      // spelling out the dozen settings the runs agree on buries it. The field
      // names are the raw ones so they match the flag and the merge message
      // that split the runs apart in the first place.
      //
      // Computed from the selection, not from vfields: vfields only sees
      // variance inside a corpus/CPU/Go/bench group, so a setting that differs
      // across two groups being compared would otherwise go unreported, and
      // under-reporting is the failure that matters in a warning.
      const manyCorpora = uniq(runs.map((r) => r.corpus)).length > 1;
      for (const f of uniq(runs.flatMap((r) => Object.keys(r.variant)))) {
        // GOAMD64 is already reported above, from the run stub. A corpus hash
        // that differs because the corpora differ is not news.
        if (f === 'goamd64' || (f === 'corpus_hash' && manyCorpora)) continue;
        note('MIXED_VARIANT', f, uniq(runs.map((r) => r.variant[f] ?? '(unset)')));
      }
      return { runs, warnings };
    },

    needsFetch: (runs) => runs.some((r) => !ingested.has(r.id)),

    async ensure(runStubs) {
      const missing = runStubs.filter((r) => !ingested.has(r.id));
      const files = await Promise.all(missing.map((r) => loader.run(r)));
      missing.forEach((stub, i) => ingest(stub, files[i]));
    },

    query(q) {
      return runQuery(rows, q, db);
    },
  };
  return db;
}

// tableRows turns the on-disk column arrays back into objects, using the
// stored "cols" header rather than a hard-coded order.
function tableRows(table) {
  if (!table || !table.rows) return [];
  const idx = {};
  table.cols.forEach((c, i) => { idx[c] = i; });
  return table.rows.map((r) => {
    const o = {};
    for (const [k, i] of Object.entries(idx)) o[k] = r[i];
    return o;
  });
}

// splitVariant splits on the first '-', which is unambiguous because codec ids
// never contain one and levels may be negative ("gzkp--1" is gzkp level -1).
export function splitVariant(v) {
  if (!v) return [null, null];
  const i = v.indexOf('-');
  if (i <= 0) return [v, null];
  const n = Number(v.slice(i + 1));
  return [v.slice(0, i), Number.isNaN(n) ? null : n];
}

function verOf(lib) {
  if (!lib) return '';
  const i = lib.lastIndexOf('@');
  return i < 0 ? '' : lib.slice(i + 1);
}

function uniq(a) {
  return [...new Set(a)].sort();
}

// most returns the key with the most measurements, as a one-value selection.
// The index counts rows per run, not per key, so a run's rows are split evenly
// over the keys it holds.
function most(runs, keysOf) {
  const n = new Map();
  for (const r of runs) {
    const ks = keysOf(r);
    for (const k of ks) n.set(k, (n.get(k) || 0) + (r.n_encode + r.n_decode) / ks.length);
  }
  const best = [...n].sort((a, b) => b[1] - a[1])[0];
  return best ? [best[0]] : [];
}

// The value is what the URL carries and what rows are keyed on; this is how it
// reads on screen.
export function variantLabel(v) {
  if (!v) return '';
  return v.split(' · ').map((p) => p.replace('=', ' ')).join(', ');
}

// variantFields names the build and measurement settings that actually differ
// somewhere in the database. A run whose environment could not merge into its
// base keeps its own file, and these are what tell the two apart; without them
// the site would union straight back together what the database kept separate.
//
// Variance is measured inside a corpus/CPU/Go/bench group, because that is
// where two runs compete. Across groups the fields differ for dull reasons --
// every corpus has its own corpus_hash -- and labelling every series with one
// would be noise.
function variantFields(runs) {
  const groups = new Map();
  for (const r of runs) {
    const k = [r.corpus, r.machine, r.go, r.bench].join(' | ');
    if (!groups.has(k)) groups.set(k, []);
    groups.get(k).push(r);
  }
  const vary = new Set();
  for (const g of groups.values()) {
    const fields = uniq(g.flatMap((r) => Object.keys(r.variant)));
    for (const f of fields) {
      if (uniq(g.map((r) => r.variant[f] ?? '')).length > 1) vary.add(f);
    }
  }
  return [...vary].sort();
}

// ------------------------------------------------------------------ query

// The environment a measurement was taken in. Two of these in one series is
// two lines under one label, which the reader has no way to tell apart -- the
// banner says the runs differ but the chart does not show where. They are not
// dropped the way a superseded library version is: a second CPU or Go version
// is another result, not the same one measured twice.
const ENV_SPLIT = ['machine', 'go', 'bench', 'variant'];

function runQuery(all, q, db) {
  const has = (vals, v) => !vals || vals.length === 0 || vals.includes(v);

  let rows = all.filter((r) =>
    r.kind === q.kind &&
    r.op === q.op &&
    has(q.corpora, r.corpus) &&
    has(q.machines, r.machine) &&
    has(q.gos, r.go) &&
    (!q.bench || r.bench === q.bench) &&
    has(q.threads, r.threads) &&
    (has(q.windows, r.window) || q.windows.includes(r.size)) &&
    has(q.codecs, r.codec) &&
    has(q.libs, r.lib) &&
    has(q.variants, r.variant) &&
    // Not has(): nothing selected shows nothing, since every class is an
    // explicit on/off choice rather than a narrowing filter.
    q.levelClasses.includes(r.levelClass) &&
    (q.levels == null || q.levels.length === 0 || q.levels.includes(r.level)));

  // The corpus kinds are never mixed in one chart, and the gate lives here,
  // below the UI, so no hand-edited URL or future card can bypass it.
  const kinds = new Set(rows.map((r) => r.kind));
  if (kinds.size > 1) throw new QueryError('MIXED_CORPUS_KIND');

  // Chosen by size alone, a default row is that size: with only 2M picked,
  // minlz's default is a 2M row like s2's, not a series of its own.
  if (q.windows?.length && !q.windows.includes(0)) rows = rows.map((r) => (r.window ? r : { ...r, window: r.size }));

  if (q.op === 'd') rows = applySrcPolicy(rows, q);

  // One codec measured against two versions of its library draws the same
  // curve twice, identical ratio and slightly different speed, one on top of
  // the other. Unless library version is what the reader asked to compare,
  // only the newest survives.
  if (!q.seriesBy.includes('lib') && q.entityBy !== 'lib') rows = newestLib(rows);

  // A series line only means something within one corpus at one thread count,
  // measured the same way. Whichever of those actually vary in the result join
  // the series key, so a line never connects unrelated slices; entityBy keeps
  // the colour on the codec.
  // ENV_SPLIT is appended to whatever the caller asked for rather than being
  // one of the defaults: a line through two CPUs, two Go versions or two
  // src_threads settings is never meaningful, whatever else the card plots.
  // Nor is one through two windows -- a level curve that steps between block
  // sizes is two settings drawn as one.
  const seriesBy = [...q.seriesBy];
  for (const d of [...(q.autoSplit ?? ['corpus', 'threads']), 'window', ...ENV_SPLIT]) {
    if (seriesBy.includes(d)) continue;
    if (new Set(rows.map((r) => r[d])).size > 1) seriesBy.push(d);
  }
  q = { ...q, seriesBy };

  const groups = new Map();
  for (const r of rows) {
    const key = q.seriesBy.map((d) => String(r[d])).join(' · ');
    let g = groups.get(key);
    if (!g) {
      // sample carries the first row so a dimension whose label needs context
      // can reach it; a level number says nothing without its codec.
      g = { key, entity: String(r[q.entityBy ?? q.seriesBy[0]]), dims: {}, sample: r, points: [] };
      for (const d of q.seriesBy) g.dims[d] = r[d];
      groups.set(key, g);
    }
    g.points.push(r);
  }

  const pointKey = q.pointBy ?? ['level'];
  const ent = q.entityBy ?? q.seriesBy[0];
  // The legend names an entity across all of its series, so a default window
  // is sized from every row the entity covers.
  const entRows = new Map();
  for (const r of rows) {
    const k = String(r[ent]);
    if (!entRows.has(k)) entRows.set(k, []);
    entRows.get(k).push(r);
  }
  const series = [...groups.values()].map((g) => {
    g.points.sort((a, b) => cmpBy(a, b, pointKey));
    if (q.metric) paretoFront(g.points, q.metric, 'ratio');
    g.parts = q.seriesBy.map((d) => dimLabel(d, g.dims[d], db, g.sample, g.points));
    g.label = g.parts.join(' · ');
    // The label drawn on the chart leaves the default window unsaid: it is
    // what a line is unless told otherwise, and at every line end it buries
    // the part that names the line. The hover keeps the full label.
    const drawn = g.parts.filter((_, i) => q.seriesBy[i] !== 'window' || g.dims.window);
    g.endLabel = drawn.length ? drawn.join(' · ') : 'default';
    // The legend keys colour to the entity, so it names the entity alone --
    // every slice of one codec shares that colour, and spelling out which
    // slice happened to be built first would be arbitrary.
    g.entityLabel = dimLabel(ent, g.dims[ent] ?? g.entity, db, null, entRows.get(g.entity));
    return g;
  });
  // A numeric dimension sorts by value: by label, "1M window" lands before
  // "8K window" and "16 threads" before "2 threads".
  series.sort((a, b) => {
    for (const [i, d] of q.seriesBy.entries()) {
      const x = a.dims[d], y = b.dims[d];
      const c = typeof x === 'number' && typeof y === 'number' ? x - y : a.parts[i].localeCompare(b.parts[i]);
      if (c) return c;
    }
    return 0;
  });
  return { q, series, rows };
}

// newestLib keeps the newest-library row of each measurement. The key is the
// database's own row-merge key with the library columns taken out, because
// rows that collide on it were kept apart for the library alone; anything
// differing in any other way is left untouched. Keyed within a run, so a
// second run is never emptied out by a newer library in the first.
function newestLib(rows) {
  const best = new Map();
  for (const r of rows) {
    const k = [r.runId, r.op, r.codec, r.level, r.window, r.threads, r.src].join('|');
    const cur = best.get(k);
    // The codec's own library decides. The source's breaks the tie, which is
    // what chooses between two encoders' output for one unchanged decoder.
    if (!cur || (cmpVer(r.libVer, cur.libVer) || cmpVer(r.srcLibVer, cur.srcLibVer)) > 0) best.set(k, r);
  }
  const keep = new Set(best.values());
  return rows.filter((r) => keep.has(r));
}

// cmpVer orders on the numbers in a version string, so v1.9.0 sorts below
// v1.20.0 where a string compare puts it above, and a pseudo-version sorts on
// its timestamp. Only ever asked to compare two versions of one module.
function cmpVer(a, b) {
  const x = (a || '').match(/\d+/g) || [];
  const y = (b || '').match(/\d+/g) || [];
  for (let i = 0; i < Math.max(x.length, y.length); i++) {
    const d = (Number(x[i]) || 0) - (Number(y[i]) || 0);
    if (d) return Math.sign(d);
  }
  return 0;
}

// applySrcPolicy decides which encoder's output a decoder is measured against.
// Leaving it implicit is how "gzkp decoding gzkp-9 vs gzstd decoding gzstd-6"
// gets mistaken for a decoder comparison.
function applySrcPolicy(rows, q) {
  const policy = q.srcPolicy || 'own';
  if (policy === 'all') return rows;
  if (policy.startsWith('fixed:')) {
    const want = policy.slice(6);
    return rows.filter((r) => r.src === want);
  }
  // "own": each decoder against its own implementation's output where that
  // exists, so every series is the codec decoding what it produced.
  return rows.filter((r) => r.srcCodec === r.codec);
}

function cmpBy(a, b, keys) {
  for (const k of keys) {
    const x = a[k], y = b[k];
    if (x === y) continue;
    if (typeof x === 'number' && typeof y === 'number') return x - y;
    return String(x).localeCompare(String(y));
  }
  return 0;
}

function dimLabel(d, v, db, row, points) {
  if (d === 'codec') return db.codecs.get(v)?.id || v;
  if (d === 'machine') return db.machines.get(v)?.label || v;
  if (d === 'corpus') return db.corpora.get(v)?.name || v;
  if (d === 'threads') return `${v} thread${v === 1 ? '' : 's'}`;
  if (d === 'bench') return `bench v${v}`;
  if (d === 'window') {
    // A default series names its size only when all of it shares one: zstd's
    // default is 4M at its fastest level and 8M above.
    const sizes = new Set(points?.map((p) => p.size));
    return windowLabel({ window: v, size: sizes.size === 1 ? [...sizes][0] : 0 });
  }
  if (d === 'variant') return variantLabel(v);
  if (d === 'level') return row?.levelName || String(v);
  return String(v);
}

export { METRICS };
