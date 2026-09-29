// In-memory database: ingest, scope resolution and querying. Pure data — this
// module never touches the DOM and never fetches anything itself.

import { levelWord } from '../format.js';
import { METRICS, paretoFront, ratio, speed } from './metrics.js';

export class QueryError extends Error {}

export async function openDB(loader) {
  const index = await loader.index();
  const rows = []; // flat, denormalised; one entry per measurement
  const ingested = new Set();

  const corpora = new Map(index.corpora.map((c) => [c.id, c]));
  const codecs = new Map(index.codecs.map((c) => [c.id, c]));
  const machines = new Map(Object.entries(index.machines || {}));
  const vfields = variantFields(index.runs);
  // A run stored before the index carried these settings cannot claim to match
  // one that does, so it says so rather than passing for an empty value.
  const variantOf = (r) => {
    if (!vfields.length) return '';
    if (!r.variant) return UNRECORDED;
    return vfields.map((f) => `${f}=${r.variant[f] ?? ''}`).join(' · ');
  };
  const libsOf = (r) => Object.entries(r.libs || {}).flatMap(([m, vs]) => vs.map((v) => `${m}@${v}`));
  // A level's word only means anything next to its codec -- level 1 is
  // "fastest" for zstd and "default" for s2 -- so it is resolved here, where
  // the codec is known, and rides on the row. Views then name a measurement
  // without being handed the whole database to do it.
  const levelOf = (codec, n) => levelWord(codecs.get(codec), n);

  // Levels a codec declares as doing no compression at all. They are real
  // measurements -- the memcpy ceiling -- but one of them sets the top of a
  // log throughput axis and squashes every codec that does actual work into
  // the left of the chart, and it wins the ranking card outright. Matched by
  // declared name, never by level number: lz4-0 and snappy-0 are level 0 and
  // compress perfectly well.
  const stored = new Set();
  const storeCodecs = new Set();
  for (const c of index.codecs) {
    for (const l of c.encode?.levels ?? []) {
      if (l.name === 'store' || l.name === 'uncompressed') {
        stored.add(`${c.id}/${l.n}`);
        storeCodecs.add(c.id);
      }
    }
  }
  const isStored = (codec, level) => stored.has(`${codec}/${level}`);

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
        codec: r.codec, level: r.level ?? null, threads: r.threads,
        levelName: levelOf(r.codec, r.level ?? null),
        src: r.src || null, srcCodec: r.srcCodec || null, srcLevel: r.srcLevel ?? null,
        // "minlz--1" is a storage key, not a label. The decode source is named
        // the same way an encode row is, so the two read alike.
        srcLabel: r.src ? `${r.srcCodec} ${levelOf(r.srcCodec, r.srcLevel ?? null)}`.trimEnd() : null,
        lib: libs[r.lib] || '', libVer: verOf(libs[r.lib]),
        in: r.in, out: r.out, ns: r.ns, passes: r.passes, sd: r.sd,
        verified: r.verified,
        // A decode measurement is uncompressed when its *source* was: the
        // decoder is reading a stored frame whatever the decoder's own id.
        store: op === 'c' ? isStored(r.codec, r.level) : isStored(r.srcCodec, r.srcLevel),
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
      return {
        kinds: uniq(index.runs.map((r) => r.kind)),
        corpora: uniq(scoped(s, 'corpora').map((r) => r.corpus)),
        machines: uniq(scoped(s, 'machines').map((r) => r.machine)),
        gos: uniq(scoped(s, 'gos').map((r) => r.go)),
        benches: uniq(scoped(s, 'bench').map((r) => r.bench)),
        variants: vfields.length ? uniq(scoped(s, 'variants').map(variantOf)) : [],
        goamd64: uniq(all.map((r) => r.goamd64 || '')).filter(Boolean),
        threads: uniq(all.flatMap((r) => r.threads)).sort((a, b) => a - b),
        codecs: uniq(all.flatMap((r) => r.codecs)),
        libs: uniq(all.flatMap(libsOf)),
        ops: ['c', 'd'].filter((op) => all.some((r) => (op === 'c' ? r.n_encode : r.n_decode) > 0)),
        // Index-only, so this says a codec in scope *has* a no-compression
        // level, not that one was measured -- generous in the same direction
        // as the row filters above, and never hiding a control that is needed.
        hasStore: all.some((r) => r.codecs.some((id) => storeCodecs.has(id))),
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
      const runs = scoped(s, null).filter((r) => !s?.threads?.length
        || !r.threads?.length || r.threads.some((n) => s.threads.includes(n)));

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
      const recorded = runs.filter((r) => r.variant);
      if (recorded.length && recorded.length < runs.length) {
        warnings.push({
          code: 'MIXED_VARIANT',
          text: 'Some of the selected runs predate the recording of build and measurement'
            + ' settings, so what differs between them is not known.',
        });
      }
      const manyCorpora = uniq(runs.map((r) => r.corpus)).length > 1;
      for (const f of uniq(recorded.flatMap((r) => Object.keys(r.variant)))) {
        // GOAMD64 is already reported above, from the run stub. A corpus hash
        // that differs because the corpora differ is not news.
        if (f === 'goamd64' || (f === 'corpus_hash' && manyCorpora)) continue;
        note('MIXED_VARIANT', f, uniq(recorded.map((r) => r.variant[f] ?? '(unset)')));
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

// variantFields names the build and measurement settings that actually differ
// somewhere in the database. A run whose environment could not merge into its
// base keeps its own file, and these are what tell the two apart; without them
// the site would union straight back together what the database kept separate.
//
// Variance is measured inside a corpus/CPU/Go/bench group, because that is
// where two runs compete. Across groups the fields differ for dull reasons --
// every corpus has its own corpus_hash -- and labelling every series with one
// would be noise.
const UNRECORDED = 'unrecorded';

// The value is what the URL carries and what rows are keyed on; this is how it
// reads on screen.
export function variantLabel(v) {
  if (!v) return '';
  if (v === UNRECORDED) return 'settings not recorded';
  return v.split(' · ').map((p) => p.replace('=', ' ')).join(', ');
}

function variantFields(runs) {
  const groups = new Map();
  for (const r of runs) {
    // A run that recorded nothing would make every field look like it varies,
    // against one that recorded them all, and that set is global: a single
    // such run turns every variant label in the site into the full
    // environment. It cannot say which setting differs, so it says nothing
    // here and variantOf marks it UNRECORDED instead.
    if (!r.variant) continue;
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
    has(q.codecs, r.codec) &&
    has(q.libs, r.lib) &&
    has(q.variants, r.variant) &&
    (q.uncompressed || !r.store) &&
    (q.levels == null || q.levels.length === 0 || q.levels.includes(r.level)));

  // The corpus kinds are never mixed in one chart, and the gate lives here,
  // below the UI, so no hand-edited URL or future card can bypass it.
  const kinds = new Set(rows.map((r) => r.kind));
  if (kinds.size > 1) throw new QueryError('MIXED_CORPUS_KIND');

  if (q.op === 'd') rows = applySrcPolicy(rows, q);

  // A series line only means something within one corpus at one thread count,
  // measured the same way. Whichever of those actually vary in the result join
  // the series key, so a line never connects unrelated slices; entityBy keeps
  // the colour on the codec.
  // 'variant' is appended to whatever the caller asked for rather than being
  // one of the defaults: a line through two different src_threads settings is
  // never meaningful, whatever else the card is plotting.
  const seriesBy = [...q.seriesBy];
  for (const d of [...(q.autoSplit ?? ['corpus', 'threads']), 'variant']) {
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
  const series = [...groups.values()].map((g) => {
    g.points.sort((a, b) => cmpBy(a, b, pointKey));
    if (q.metric) paretoFront(g.points, q.metric, 'ratio');
    g.label = q.seriesBy.map((d) => dimLabel(d, g.dims[d], db, g.sample)).join(' · ');
    // The legend keys colour to the entity, so it names the entity alone --
    // every slice of one codec shares that colour, and spelling out which
    // slice happened to be built first would be arbitrary.
    g.entityLabel = dimLabel(ent, g.dims[ent] ?? g.entity, db);
    return g;
  });
  series.sort((a, b) => a.label.localeCompare(b.label));
  return { q, series, rows };
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

function dimLabel(d, v, db, row) {
  if (d === 'codec') return db.codecs.get(v)?.id || v;
  if (d === 'machine') return db.machines.get(v)?.label || v;
  if (d === 'corpus') return db.corpora.get(v)?.name || v;
  if (d === 'threads') return `${v} thread${v === 1 ? '' : 's'}`;
  if (d === 'variant') return variantLabel(v);
  if (d === 'level') return row?.levelName || String(v);
  return String(v);
}

export { METRICS };
