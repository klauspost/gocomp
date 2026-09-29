// Chart.js setup: registration, shared options, the distance-capped hit mode,
// the external tooltip and the two plugins Chart.js has no native equivalent for.

import {
  BarController, BarElement, CategoryScale, Chart, Interaction, LinearScale,
  LineController, LineElement, LogarithmicScale, PointElement, ScatterController, Tooltip,
} from 'chart.js';
import { getRelativePosition } from 'chart.js/helpers';
import { tokens } from './theme.js';

Chart.register(
  ScatterController, LineController, BarController,
  PointElement, LineElement, BarElement,
  LinearScale, LogarithmicScale, CategoryScale, Tooltip,
);
Chart.defaults.font.family = 'system-ui, -apple-system, "Segoe UI", sans-serif';
Chart.defaults.font.size = 12;
Chart.defaults.animation = false;
Chart.defaults.maintainAspectRatio = false;

// Built-in 'nearest' has no distance limit, so the tooltip never lets go in an
// empty corner. This caps it at the mandated 24px hit target.
Interaction.modes.nearestWithin = (chart, e, opts) => {
  const { x, y } = getRelativePosition(e, chart);
  let best = null;
  let bestD = opts.maxDistance ?? 24;
  for (let i = 0; i < chart.data.datasets.length; i++) {
    const meta = chart.getDatasetMeta(i);
    if (meta.hidden) continue;
    for (let j = 0; j < meta.data.length; j++) {
      const el = meta.data[j];
      const d = Math.hypot(el.x - x, el.y - y);
      // <= means later datasets win ties, and emphasised series are pushed
      // last, so a highlighted point always beats a dim one underneath it.
      if (d <= bestD) { bestD = d; best = { element: el, datasetIndex: i, index: j }; }
    }
  }
  return best ? [best] : [];
};

// endpointLabels draws a direct label on the last point of emphasised series
// only. Labelling every point is chaos and goes unread.
const endpointLabels = {
  id: 'endpointLabels',
  afterDatasetsDraw(chart) {
    const t = tokens();
    const { ctx } = chart;
    ctx.save();
    ctx.font = `600 11px ${Chart.defaults.font.family}`;
    ctx.fillStyle = t.textSecondary;
    ctx.textBaseline = 'middle';
    // Nudging colliding labels apart detaches them from their lines and reads
    // as noise, so a label that will not fit cleanly is simply dropped; the
    // legend, tooltip and table view still carry it.
    const placed = [];
    for (let i = 0; i < chart.data.datasets.length; i++) {
      const ds = chart.data.datasets[i];
      if (!ds.__label || !ds.__emph) continue;
      const pts = chart.getDatasetMeta(i).data;
      if (!pts.length) continue;
      const p = pts[pts.length - 1];
      const label = ds.__endLabel ?? ds.__label;
      const w = ctx.measureText(label).width;
      let x = p.x + 9;
      if (x + w > chart.chartArea.right) x = p.x - 9 - w;
      const box = { x, y: p.y - 7, w, h: 14 };
      if (placed.some((q) => box.x < q.x + q.w && q.x < box.x + box.w
        && box.y < q.y + q.h && q.y < box.y + box.h)) continue;
      placed.push(box);
      // Text never wears the series colour; the coloured mark beside it carries identity.
      ctx.fillText(label, x, p.y);
    }
    ctx.restore();
  },
};

// barGap keeps a fixed 2px of surface between adjacent bars. Chart.js only
// offers proportional spacing, which drifts with the category count.
const barGap = {
  id: 'barGap',
  afterLayout(chart) {
    if (chart.config.type !== 'bar') return;
    const scale = chart.scales.y || chart.scales.x;
    const n = Math.max(1, scale.ticks.length);
    const slot = (scale.height || scale.width) / n;
    const thickness = Math.max(4, Math.min(24, slot - 2));
    for (const ds of chart.data.datasets) ds.barThickness = thickness;
  },
};

Chart.register(endpointLabels, barGap);

// createChart refuses any scale other than x and y. A dual axis invents a
// correlation that is not in the data, and there is now no code path to it.
export function createChart(canvas, cfg) {
  const scales = Object.keys(cfg.options?.scales ?? {});
  const bad = scales.filter((k) => k !== 'x' && k !== 'y');
  if (bad.length) {
    throw new Error(`dual-axis blocked: unexpected scales ${bad.join(', ')}; use two panels`);
  }
  return new Chart(canvas, cfg);
}

export function axis(t, { title, type = 'linear', beginAtZero = false, min, max, short = false }) {
  const log = type === 'logarithmic';
  return {
    type,
    beginAtZero,
    min, max,
    title: { display: !!title, text: title, color: t.textSecondary, font: { size: 12 } },
    // An explicit `callback: undefined` shadows the built-in formatter, which
    // makes a category axis print indices, so a key is only ever added when
    // there is something to say.
    ticks: Object.assign(
      { color: t.textMuted, maxRotation: 0, autoSkip: !log },
      log
        ? {
          // Chart.js emits every minor log tick, which renders as corduroy.
          // Keep 1/2/5 per decade and drop the rest. Compared with a
          // tolerance, not rounded: Math.round(1.5) is 2, which let every
          // 1.5/15/150 back in and put the corduroy back on a tall axis.
          callback: (v) => {
            const m = v / 10 ** Math.floor(Math.log10(v));
            return [1, 2, 5].some((k) => Math.abs(m - k) < 1e-6) ? compact(v) : null;
          },
        }
        // Same 2k/5k shorthand on a linear axis, so toggling the scale does
        // not also change how the numbers are written.
        : short ? { callback: (v) => compact(v) } : {},
    ),
    grid: {
      drawTicks: false,
      tickLength: 0,
      lineWidth: 1,
      color: log ? (c) => (isDecade(c.tick.value) ? t.grid : 'transparent') : t.grid,
    },
    border: { color: t.axis, width: 1 },
  };
}

function isDecade(v) {
  const l = Math.log10(v);
  return Math.abs(l - Math.round(l)) < 1e-9;
}

// Log ticks are exact 1/2/5 per decade, but a linear axis picks its own steps
// and 1666.6666666666667 must not reach the axis as "1.6666666666666667k".
function compact(v) {
  if (Math.abs(v) >= 1000) return `${+(v / 1000).toFixed(1)}k`;
  return String(+v.toFixed(2));
}

// tooltipFor renders HTML instead of canvas: CSS tokens, a stroke-shaped series
// key and textContent insertion are all impossible in the built-in tooltip.
export function tooltipFor(host, describe) {
  return ({ chart, tooltip }) => {
    if (!tooltip.opacity) { host.dataset.show = '0'; return; }
    host.replaceChildren();

    for (const dp of tooltip.dataPoints) {
      const ds = chart.data.datasets[dp.datasetIndex];
      const info = describe(ds, dp);
      if (info.head) {
        const h = el('div', 'tt-head');
        h.textContent = info.head;
        host.append(h);
      }
      for (const line of info.rows) {
        const row = el('div', 'tt-row');
        const key = el('span', 'tt-key');
        key.style.background = ds.__markColor;
        const val = el('span', 'tt-val');
        val.textContent = line.value; // labels are generated data, never innerHTML
        const name = el('span', 'tt-name');
        name.textContent = line.name;
        row.append(key, val, name);
        host.append(row);
      }
      if (info.note) {
        const n = el('div', 'tt-note');
        n.textContent = info.note;
        host.append(n);
      }
    }

    const area = chart.chartArea;
    const flip = tooltip.caretX > area.left + (area.right - area.left) * 0.6;
    const x = tooltip.caretX + (flip ? -12 - host.offsetWidth : 12);
    host.style.transform = `translate(${Math.max(0, x)}px, ${tooltip.caretY - 10}px)`;
    host.dataset.show = '1';
  };
}

export function el(tag, cls) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  return e;
}
