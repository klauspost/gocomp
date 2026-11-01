// Filter state, its store, and the URL-hash round trip that makes a view
// shareable. Theme is deliberately absent: a shared link must not force the
// recipient's colour scheme.

import { LATEST, LEVEL_CLASSES } from './data/db.js';
import { SLOTS } from './view/theme.js';

// fitSlots pads or trims to the current cap. Links shared before the cap grew
// carry three entries, and a hand-edited one can carry any number.
function fitSlots(list) {
  return Array.from({ length: SLOTS }, (_, i) => (list ?? [])[i] ?? null);
}

export const DEFAULTS = {
  v: 1,
  kind: 'file',
  corpora: [],   // [] means "all of this kind"
  machines: null, // null means "the data's pick" (domains().picks); [] is all
  // The newest Go each measurement was taken on, as libs below. New Go
  // releases arrive to measure the standard library, and an older one should
  // not double every line once a newer one has measured the same point.
  gos: [LATEST],
  bench: null,   // null means "newest"
  threads: null,
  // The default window alone, unlike every other list here where empty means
  // all: a window sweep is a deliberate experiment, and mixing its sizes into
  // the headline charts would put one codec on screen several times.
  windows: [0],
  op: 'both',
  compareBy: 'codec',
  codecs: [],
  levels: [],
  // The newest version of each measurement. Another version only matters when
  // it is what is being compared, and then [] (all) is one click away.
  libs: [LATEST],
  variants: [], // build/measurement settings that kept two runs in separate files
  // Regular levels only by default. An uncompressed level tops the throughput
  // axis and wins the ranking outright, which buries every codec that
  // compresses; a special one is off the dial the chart draws a codec along.
  levelClasses: ['regular'],
  // A log speed axis makes the slow codecs legible but flatters them; off
  // unless asked for, so nobody reads one without knowing it is on.
  logSpeed: false,
  metric: 'cspeed',
  srcPolicy: 'own',
  frontier: false,
  // Positional: the index IS the colour slot, so removing one entry cannot
  // repaint the survivors. Length is the palette's validated cap.
  emphasis: Array.from({ length: SLOTS }, () => null),
  tables: [],  // card ids whose table twin is expanded
  sorts: [],   // "cardId:column:dir", so a shared link opens on the same sort
};

export function createStore(initial) {
  let state = { ...DEFAULTS, ...initial };
  const subs = new Set();
  return {
    get: () => state,
    set(patch) {
      state = { ...state, ...patch };
      for (const fn of subs) fn(state);
    },
    subscribe(fn) {
      subs.add(fn);
      return () => subs.delete(fn);
    },
  };
}

// emphasise toggles an entity in the fixed-length slot array. A pick takes
// `slot`, the colour it is drawn in while nothing is picked, when that is
// free, so highlighting it does not repaint it. Once every slot is taken the
// next pick evicts the oldest and takes its freed slot, so survivors never
// move -- and never change colour.
export function emphasise(emphasis, id, slot = -1) {
  const next = fitSlots(emphasis);
  const at = next.indexOf(id);
  if (at >= 0) {
    next[at] = null;
    return next;
  }
  const free = slot >= 0 && next[slot] == null ? slot : next.indexOf(null);
  if (free >= 0) {
    next[free] = id;
    return next;
  }
  next.shift();
  next.push(id);
  return next;
}

const KEYS = {
  kind: 'k', corpora: 'c', machines: 'm', gos: 'g', bench: 'b', threads: 't',
  windows: 'w', op: 'op', compareBy: 'by', codecs: 'cd', levels: 'lv', libs: 'lb',
  variants: 'va', levelClasses: 'lc', logSpeed: 'lg',
  metric: 'me', srcPolicy: 'sp', frontier: 'f', emphasis: 'em',
  tables: 'tb', sorts: 'so',
};

const LISTS = new Set([
  'corpora', 'machines', 'gos', 'threads', 'windows', 'codecs', 'levels', 'libs',
  'variants', 'levelClasses', 'emphasis', 'tables', 'sorts',
]);
const NUMS = new Set(['threads', 'windows', 'levels']);

// sortOf/withSort keep the per-card sort in the URL as "card:column:dir".
export function sortOf(state, card, fallback) {
  const hit = (state.sorts || []).find((s) => s.startsWith(`${card}:`));
  if (!hit) return fallback;
  const [, key, dir] = hit.split(':');
  return { key, dir: Number(dir) || 1 };
}

export function withSort(state, card, sort) {
  const rest = (state.sorts || []).filter((s) => !s.startsWith(`${card}:`));
  return [...rest, `${card}:${sort.key}:${sort.dir}`].sort();
}

export function withTable(state, card, open) {
  const rest = (state.tables || []).filter((t) => t !== card);
  return open ? [...rest, card].sort() : rest;
}

export function encode(state) {
  const p = [];
  for (const [field, key] of Object.entries(KEYS)) {
    const v = state[field];
    const d = DEFAULTS[field];
    if (JSON.stringify(v) === JSON.stringify(d)) continue;
    if (LISTS.has(field)) {
      p.push(`${key}=${v.map((x) => (x == null ? '' : encodeURIComponent(x))).join(',')}`);
    } else if (typeof v === 'boolean') {
      if (v) p.push(`${key}=1`);
    } else if (v != null) {
      p.push(`${key}=${encodeURIComponent(v)}`);
    }
  }
  return p.join('&');
}

// reconcile drops every selection the data no longer offers. Filters constrain
// each other, so narrowing one can empty another: deselecting a Go version
// takes with it the codecs only it ever ran, and a tick left behind on one of
// those would go on filtering against something that cannot be shown.
//
// It is also the repair for a hand-edited URL, which is why it never throws:
// an unknown value is dropped, not an error.
//
// A selection that loses every value falls back to its default, not to all:
// 32 threads picked on one corpus would otherwise open the next corpus at
// every thread count at once.
export function reconcile(state, d) {
  const out = { ...state };
  if (!d.kinds.includes(out.kind)) out.kind = d.kinds[0] || 'file';
  for (const [field, valid] of [
    ['corpora', d.corpora], ['machines', d.machines], ['gos', [LATEST, ...d.gos]],
    ['threads', d.threads], ['windows', d.windows], ['codecs', d.codecs], ['libs', [LATEST, ...d.libs]],
    ['variants', d.variants],
  ]) {
    const was = out[field];
    if (was == null) continue;
    out[field] = was.filter((v) => valid.includes(v));
    if (was.length && !out[field].length) out[field] = DEFAULTS[field]?.filter((v) => valid.includes(v)) ?? null;
  }
  out.machines ??= d.picks.machines;
  out.threads ??= d.picks.threads;
  // Checked against the fixed set, not the scope: a class the scope lacks
  // selects nothing, and dropping it would lose the choice on the way back.
  out.levelClasses = (out.levelClasses || []).filter((c) => LEVEL_CLASSES.includes(c));
  if (out.bench && !d.benches.includes(out.bench)) out.bench = null;
  // 'both' stays legal as long as anything was measured; a single operation
  // that is not in the data falls back to it rather than to an empty view.
  if (out.op !== 'both' && !d.ops.includes(out.op)) out.op = 'both';
  if (out.metric === 'cspeed' && !d.ops.includes('c')) out.metric = 'dspeed';
  if (out.metric === 'dspeed' && !d.ops.includes('d')) out.metric = 'cspeed';
  return out;
}

// decode validates against the real domains, so a hand-edited URL cannot put
// the app into a state the data layer would reject.
export function decode(hash, domains) {
  const out = { ...DEFAULTS };
  const raw = new URLSearchParams((hash || '').replace(/^#/, ''));
  for (const [field, key] of Object.entries(KEYS)) {
    if (!raw.has(key)) continue;
    const s = raw.get(key);
    if (LISTS.has(field)) {
      let list = s === '' ? [] : s.split(',').map((x) => (x === '' ? null : decodeURIComponent(x)));
      if (NUMS.has(field)) list = list.map((x) => (x == null ? null : Number(x))).filter((x) => !Number.isNaN(x));
      out[field] = list;
    } else if (typeof DEFAULTS[field] === 'boolean') {
      out[field] = s === '1' || s === 'true';
    } else {
      out[field] = s;
    }
  }
  out.emphasis = fitSlots(out.emphasis);
  // The corpus control picks one or all; a link from when it picked several
  // keeps its first, so the dropdown shows what is drawn.
  out.corpora = out.corpora.slice(0, 1);
  return domains ? reconcile(out, domains) : out;
}
