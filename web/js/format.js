// Pure formatters. No DOM, no data-layer knowledge.

// SI, so kB is 1000 bytes and a 100,000,000-byte corpus reads as 100.0 MB
// rather than 95.4 MiB.
export function bytes(n) {
  if (n == null) return '';
  const u = ['B', 'kB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = n;
  while (v >= 1000 && i < u.length - 1) { v /= 1000; i++; }
  return `${i === 0 ? v : v.toFixed(1)} ${u[i]}`;
}

export function ms(ns) {
  return (ns / 1e6).toFixed(3);
}

export function int(n) {
  return n.toLocaleString();
}

// levelWord is the codec's own name for a level -- "balanced", "default",
// "huffman" -- which index.json carries straight from the Go registry, where
// it is the library's own constant. A codec that numbers its levels names them
// with the number, so "6" falls out of the same lookup and there is no second
// table here to drift from the registry.
export function levelWord(codecInfo, level) {
  if (level == null) return '';
  const l = codecInfo?.encode?.levels?.find((x) => x.n === level);
  return l ? l.name : String(level);
}
