// Derived measures. Nothing else in the site divides bytes by nanoseconds:
// getting the denominator wrong on decompression silently reverses the ranking
// between a high-ratio and a low-ratio codec, so it is defined exactly once.

// SI throughout: MB is 10^6 bytes, not 2^20. The database stores raw bytes and
// nanoseconds, so the unit is purely a display choice and changing it here
// changes it everywhere.
const MB = 1e6;

export const METRICS = {
  ratio: {
    id: 'ratio', label: 'Compression ratio', unit: ':1',
    scale: 'linear', better: 'up', fmt: (v) => fmtRatio(v),
  },
  // Reduction rather than remaining size, so this measure points the same way
  // as every other one on the page: more is better, and a default sort puts
  // the best codec at the top instead of the worst.
  pct: {
    id: 'pct', label: '% reduction', unit: '%',
    scale: 'linear', better: 'up', fmt: (v) => v.toFixed(2) + '%',
  },
  cspeed: {
    id: 'cspeed', label: 'Compression throughput', unit: 'MB/s',
    scale: 'log', better: 'up', fmt: (v) => fmtRate(v),
  },
  dspeed: {
    id: 'dspeed', label: 'Decompression throughput', unit: 'MB/s',
    scale: 'log', better: 'up', fmt: (v) => fmtRate(v),
  },
  speedup: {
    id: 'speedup', label: 'Speed-up vs 1 thread', unit: '×',
    scale: 'linear', better: 'up', fmt: (v) => v.toFixed(2) + '×',
  },
};

// A compression ratio is a proportion, so it reads as "3.412:1" rather than
// "3.412×". Speed-up keeps the × -- that one really is a multiplier.
export function fmtRatio(v) {
  return v.toFixed(3) + ':1';
}

function fmtRate(v) {
  if (v >= 1000) return Math.round(v).toLocaleString() + ' MB/s';
  if (v >= 100) return v.toFixed(0) + ' MB/s';
  return v.toFixed(1) + ' MB/s';
}

// speed is MB/s over the meaningful side of the transform: uncompressed input
// for encoding, decompressed output for decoding.
export function speed(op, inBytes, outBytes, ns) {
  const n = op === 'd' ? outBytes : inBytes;
  if (!ns) return 0;
  return n / MB / (ns / 1e9);
}

export function ratio(op, inBytes, outBytes) {
  const [u, c] = op === 'd' ? [outBytes, inBytes] : [inBytes, outBytes];
  return c ? u / c : 0;
}

// paretoFronts returns, per slice, the points no other point in it beats on
// both axes, in ascending x. Ratio depends on the corpus and speed on the CPU,
// thread count and harness, so a front never spans those; codec, level,
// window, versions and the encoder a decoder read are the choices it ranks. Query rows are shared between
// queries, so membership is returned rather than stamped on them.
export function paretoFronts(points, xKey, yKey) {
  const slices = Map.groupBy(points, (p) =>
    [p.corpus, p.machine, p.threads, p.bench, p.variant].join('|'));
  return [...slices.values()].map((ps) => {
    const front = [];
    for (const p of ps.toSorted((a, b) => b[yKey] - a[yKey] || b[xKey] - a[xKey])) {
      if (!front.length || p[xKey] > front.at(-1)[xKey]) front.push(p);
    }
    return front;
  });
}
