// Canvas draws with strings, not CSS custom properties, so the tokens are read
// out of the stylesheet once per theme and the charts rebuild from that.

const CHROME = {
  surface: '--surface-1',
  plane: '--surface-0',
  grid: '--grid',
  axis: '--axis',
  textPrimary: '--text-primary',
  textSecondary: '--text-secondary',
  textMuted: '--text-muted',
  dim: '--dim-mark',
  spare: '--spare-mark',
};

let T = null;

export function readTokens(root = document.documentElement) {
  const cs = getComputedStyle(root);
  const t = {};
  for (const [k, v] of Object.entries(CHROME)) t[k] = cs.getPropertyValue(v).trim();
  t.series = Array.from({ length: SLOTS }, (_, i) => cs.getPropertyValue(`--series-${i + 1}`).trim());
  return t;
}

// SLOTS is how many series get a colour of their own. Slots 1-8 clear the
// all-pairs floors a scatter needs in both modes (worst CVD dE 8.1, worst
// normal-vision dE 15.1 light / 15.6 dark). 9-16 were added on request with
// the floors knowingly broken: no 16 colours in the lightness band can clear
// them. They are the greedy farthest-point picks, each maximising its worst
// separation from everything before it under normal, protan and deutan
// vision, which bottoms out at normal 8.8 and CVD 4.7 (#ae6072 against slot 2
// and slot 9). Past slot 8 the marker shape and dash carry identity where hue
// cannot, which is why every slot has its own.
export const SLOTS = 16;

// Secondary encoding, one per slot: colour alone is never allowed to carry
// identity, and at eight slots it certainly cannot. `style` is the Chart.js
// pointStyle, `css` names the matching swatch class, and the two triangles are
// told apart by rotation rather than by reaching for rectRounded, which is
// indistinguishable from a circle at this size.
// Slots 9-16 reuse the filled shapes hollow, then turn the triangle sideways
// and stand a line up, since Chart.js has no further shapes that stay
// distinct at marker size.
const MARKS = [
  { style: 'circle', css: 'circle' },
  { style: 'rect', css: 'square' },
  { style: 'triangle', css: 'triangle' },
  { style: 'rectRot', css: 'diamond' },
  { style: 'crossRot', css: 'ex' },
  { style: 'cross', css: 'plus' },
  { style: 'triangle', css: 'triangleDown', rotation: 180 },
  { style: 'star', css: 'star' },
  { style: 'circle', css: 'circleOpen', hollow: true },
  { style: 'rect', css: 'squareOpen', hollow: true },
  { style: 'triangle', css: 'triangleOpen', hollow: true },
  { style: 'rectRot', css: 'diamondOpen', hollow: true },
  { style: 'triangle', css: 'triangleRight', rotation: 90 },
  { style: 'triangle', css: 'triangleLeft', rotation: 270 },
  { style: 'triangle', css: 'triangleDownOpen', rotation: 180, hollow: true },
  { style: 'line', css: 'bar', rotation: 90 },
];
const DASHES = [
  [], [6, 3], [2, 3], [9, 3, 2, 3], [1, 3], [12, 4], [4, 2, 1, 2, 1, 2], [3, 3, 9, 3],
  [10, 2], [4, 4], [1, 5], [7, 2, 1, 2, 1, 2], [2, 2], [5, 2, 1, 2], [8, 5], [1, 2, 1, 2, 6, 3],
];

// Chart.js builds cross, crossRot, star, line and dash as *unclosed* paths, so
// ctx.fill() paints nothing and ctx.stroke() is the entire mark. Handing those
// the 2px surface ring every filled shape gets painted them in the background
// colour, which is how slot 5 came to be invisible on both themes.
const OPEN = new Set(['cross', 'crossRot', 'star', 'line', 'dash']);

export function tokens() {
  return (T ??= readTokens());
}

export function refreshTokens() {
  T = readTokens();
  return T;
}

// colorsFor takes an entity id and nothing else: there is no index-based API to
// reach for, so a filter that changes the series count cannot repaint the
// survivors. The slot is the position in the emphasis array. spare says an
// entity without a slot is overflow rather than context, so it recedes.
export function colorsFor(entityId, emphasis, spare = false) {
  const t = tokens();
  const slot = emphasis.indexOf(entityId);
  if (slot < 0) {
    const ink = spare ? t.spare : t.dim;
    return {
      stroke: ink, fill: t.surface, ring: ink, emph: false,
      shape: 'circle', pointStyle: 'circle', rotation: 0, dash: [], open: false,
    };
  }
  const c = t.series[slot];
  const m = MARKS[slot];
  return {
    stroke: c, fill: c, ring: t.surface, emph: true, slot,
    shape: m.css, pointStyle: m.style, rotation: m.rotation ?? 0,
    dash: DASHES[slot], open: OPEN.has(m.style) || !!m.hollow,
  };
}

// markFor is how a point is painted, in one place, because getting it wrong is
// invisible rather than loud: an open shape handed the surface ring is drawn
// in the background colour and simply is not there.
export function markFor(col) {
  const t = tokens();
  const base = { pointStyle: col.pointStyle, pointRotation: col.rotation };
  if (col.open) {
    return {
      ...base,
      pointBackgroundColor: 'transparent',
      pointBorderColor: col.emph ? col.stroke : t.dim,
      pointBorderWidth: 2,
    };
  }
  return {
    ...base,
    pointBackgroundColor: col.emph ? col.fill : t.surface,
    pointBorderColor: col.emph ? t.surface : col.ring,
    pointBorderWidth: 2,
  };
}

const MODES = ['auto', 'light', 'dark'];
const KEY = 'cs-theme';

export function initTheme(onChange) {
  let mode = localStorage.getItem(KEY) || 'auto';
  apply(mode);
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    if (mode === 'auto') onChange(mode);
  });
  return {
    mode: () => mode,
    cycle() {
      mode = MODES[(MODES.indexOf(mode) + 1) % MODES.length];
      localStorage.setItem(KEY, mode);
      apply(mode);
      onChange(mode);
      return mode;
    },
  };
}

function apply(mode) {
  if (mode === 'auto') delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = mode;
}
