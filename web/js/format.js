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

// windowName spells a window the way the -window flag does. Binary, unlike
// bytes(): every window is a power of two, and "65.5 kB" would hide that.
export function windowName(n) {
  if (!n) return 'default';
  for (const [shift, unit] of [[30, 'G'], [20, 'M'], [10, 'K']]) {
    if (n >= 2 ** shift && n % 2 ** shift === 0) return `${n / 2 ** shift}${unit}`;
  }
  return String(n);
}

// windowLabel names a row's window, the default by the size it stands for
// where the codec declares one.
export function windowLabel(r) {
  if (r.window) return `${windowName(r.window)} window`;
  return r.size ? `default window (${windowName(r.size)})` : 'default window';
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
