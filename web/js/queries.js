// The seam. This is the only module that turns UI state into data-layer
// queries; views receive results and never build a query themselves.

import { METRICS } from './data/metrics.js';

const COMPARE_DIM = {
  codec: 'codec',
  go: 'go',
  corpus: 'corpus',
  machine: 'machine',
  threads: 'threads',
  lib: 'lib',
  window: 'window',
};

function base(state) {
  return {
    kind: state.kind,
    corpora: state.corpora,
    machines: state.machines,
    gos: state.gos,
    bench: state.bench,
    threads: state.threads,
    windows: state.windows,
    codecs: state.codecs,
    levels: state.levels,
    libs: state.libs,
    variants: state.variants,
    levelClasses: state.levelClasses,
    srcPolicy: state.srcPolicy,
  };
}

// dimension is what a series represents and, by default, what owns its colour.
// They are separate so "one codec across Go versions" can still colour by codec.
function shaping(state) {
  const dim = COMPARE_DIM[state.compareBy] || 'codec';
  const pointBy = dim === 'threads' ? ['threads'] : ['level'];
  return { seriesBy: [dim], entityBy: dim, pointBy };
}

export function buildQueries(db, state) {
  const b = base(state);
  const sh = shaping(state);
  const out = {};

  if (state.op !== 'd') {
    out.encode = db.query({ ...b, ...sh, op: 'c', metric: 'cspeed' });
  }
  if (state.op !== 'c') {
    out.decode = db.query({ ...b, ...sh, op: 'd', metric: 'dspeed' });
  }

  // Thread scaling plots threads along X, so threads must stay out of the
  // series key even though every other chart splits on it.
  const scalingOp = state.op === 'd' ? 'd' : 'c';
  out.scaling = db.query({
    ...b,
    op: scalingOp,
    seriesBy: [sh.entityBy],
    entityBy: sh.entityBy,
    pointBy: ['threads'],
    autoSplit: ['corpus', 'level'],
    metric: scalingOp === 'd' ? 'dspeed' : 'cspeed',
  });
  out.scalingOp = scalingOp;
  out.dim = sh.entityBy;
  out.metric = METRICS[state.metric] || METRICS.cspeed;
  return out;
}

// entitiesOf lists the colourable entities currently in scope, in a stable
// order, so the legend and the emphasis picker agree.
export function entitiesOf(results) {
  const seen = new Map();
  for (const r of [results.encode, results.decode]) {
    if (!r) continue;
    for (const s of r.series) if (!seen.has(s.entity)) seen.set(s.entity, s.entityLabel ?? s.label);
  }
  return [...seen].map(([id, label]) => ({ id, label }));
}
