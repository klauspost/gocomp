// The chart cards. Every card exposes the same shape:
//   { root, update(results, state), retheme(), setLoading(bool) }

import { METRICS, fmtRatio } from '../data/metrics.js';
import { entitiesOf } from '../queries.js';
import * as fmt from '../format.js';
import { axis, createChart, el, tooltipFor } from './chart.js';
import { SLOTS, colorsFor, markFor, refreshTokens, tokens } from './theme.js';
import { copyCSV, renderTable, tableTwin } from './table.js';

// emptyState swaps a card's body for a message when the current slice has
// nothing in it, rather than leaving a blank axis frame that reads as broken.
function emptyState(root, body, msg) {
  let note = root.querySelector('.empty');
  if (msg) {
    if (!note) {
      note = el('div', 'empty');
      root.append(note);
    }
    note.textContent = msg;
    body.style.display = 'none';
  } else {
    if (note) note.remove();
    body.style.display = '';
  }
  return !!msg;
}

function card(title, sub) {
  const root = el('figure', 'card');
  const cap = el('figcaption');
  const h = el('h2');
  h.textContent = title;
  const s = el('span', 'sub');
  s.textContent = sub || '';
  const spacer = el('span', 'spacer');
  cap.append(h, s, spacer);
  root.append(cap);
  return { root, cap, sub: s, spacer };
}

function panel(label) {
  const p = el('div', 'panel');
  const h = el('h3');
  h.textContent = label;
  const box = el('div', 'chart-box');
  const canvas = el('canvas');
  const tip = el('div', 'tip');
  box.append(canvas, tip);
  p.append(h, box);
  return { root: p, canvas, tip, heading: h };
}

// ------------------------------------------------------------------ legend

function legend(host, entities, state, onToggle) {
  host.replaceChildren();
  for (const e of entities) {
    const c = colorsFor(e.id, state.emphasis);
    const b = el('button');
    b.type = 'button';
    b.setAttribute('aria-pressed', String(c.emph));
    const key = el('span', `key ${c.shape}`);
    key.style.background = c.emph ? c.stroke : tokens().dim;
    const txt = document.createTextNode(e.label);
    b.append(key, txt);
    b.addEventListener('click', () => onToggle(e.id));
    host.append(b);
  }
  const hint = el('span', 'hint');
  const n = state.emphasis.filter(Boolean).length;
  hint.textContent = `${n}/${SLOTS} highlighted — the rest stay as context`;
  host.append(hint);
}

// baseLabel names one measurement. A decode row has no level of its own --
// the level is in the variant it decoded -- so labelling it by codec alone put
// six identical "lz4" bars on the decompression ranking. The decoder is named
// separately only when it is not the one that produced the bytes, which is
// every row once the decode-source policy is "every encoder variant".
// The one sentence that tells a reader what a click does, in the legend's
// vocabulary rather than a second one. It has to appear in both states or the
// way back out is never discoverable.
const clickHint = (emph) => (emph ? 'highlighted — click to clear' : 'click to highlight');

// clickToToggle makes marks clickable using the chart's own configured hit
// test, so what you can hover is exactly what you can click. The entity rides
// on the dataset, except on the ranking, where one dataset holds every bar and
// it rides per index.
function clickToToggle(canvas, chartOf, onToggle) {
  const hit = (ev) => {
    const ch = chartOf();
    if (!ch) return [];
    const { mode = 'nearest', ...opts } = ch.options.interaction ?? {};
    return ch.getElementsAtEventForMode(ev, mode, opts, true);
  };
  const idOf = (h) => {
    const ds = chartOf().data.datasets[h.datasetIndex];
    return ds.__entity ?? ds.__entities?.[h.index];
  };
  canvas.addEventListener('mousemove', (ev) => {
    canvas.style.cursor = hit(ev).length ? 'pointer' : '';
  });
  canvas.addEventListener('click', (ev) => {
    const h = hit(ev)[0];
    const id = h && idOf(h);
    if (id) onToggle(id);
  });
}

// A level is named by the codec's own word for it -- "balanced", "default" --
// and by its number only where the codec numbers its levels. "minlz 2" tells
// the reader nothing they can act on; "minlz balanced" is the setting they
// would actually pass.
const codecLevel = (r) => (r.levelName ? `${r.codec} ${r.levelName}` : r.codec);

function baseLabel(r) {
  if (!r.src) return codecLevel(r);
  return r.srcCodec === r.codec ? r.srcLabel : `${r.codec}←${r.srcLabel}`;
}

// describeBar is the ranking card's hover. A bar is one measure by definition,
// so the tooltip is where the rest of the measurement lives: how fast, how
// small, and how small compared with what. Whichever measure the bars are
// sorted by, the other numbers are the ones a reader is about to want.
function describeBar(r, emph) {
  // For an encode row `in` is the original and `out` the compressed bytes;
  // decoding feeds the compressed side in and gets the original back out.
  const [plain, comp] = r.op === 'd' ? [r.out, r.in] : [r.in, r.out];
  const speed = r.op === 'd' ? r.dspeed : r.cspeed;
  return {
    head: codecLevel(r),
    rows: [
      {
        value: METRICS[r.op === 'd' ? 'dspeed' : 'cspeed'].fmt(speed),
        name: r.op === 'd' ? `decompressing ${r.srcLabel}` : 'compressing',
      },
      { value: fmtRatio(r.ratio), name: `${r.pct.toFixed(1)}% smaller` },
      { value: fmt.bytes(comp), name: `from ${fmt.bytes(plain)}` },
    ],
    note: `${r.threads} thread${r.threads === 1 ? '' : 's'} · best of ${r.passes}`
      + `${r.size ? ` · ${fmt.windowLabel(r)}` : ''}`
      + ` · ${r.go} · ${r.libVer || 'unknown library'} · ${clickHint(emph)}`,
  };
}

// ----------------------------------------------------------- pareto scatter

// Compression and decompression share a Y scale across two canvases rather
// than sharing one plot with two X axes. A dual axis invents a correlation;
// two panels at the same height let the reader scan across honestly.
export function paretoCard(ctx) {
  const onToggleEntity = ctx.onToggleEntity;
  // "A against B" puts A on the vertical: throughput is the vertical axis now,
  // so the title had to turn round with the chart.
  const c = card('Throughput against ratio',
    'up and to the right is better · each line is one codec across its levels');
  const panels = el('div', 'panels');
  const pc = panel('Compression');
  const pd = panel('Decompression');
  panels.append(pc.root, pd.root);
  const leg = el('div', 'legend');
  const twin = tableTwin('Table view', (open) => ctx.onTable('pareto', open));
  c.root.append(panels, leg, twin.root);

  let charts = { c: null, d: null };
  let last = null;

  function build(p, res, state, metricId) {
    const t = tokens();
    const datasets = [];
    for (const s of res.series) {
      const col = colorsFor(s.entity, state.emphasis);
      datasets.push({
        label: s.label,
        __label: s.label,
        __endLabel: s.endLabel,
        __emph: col.emph,
        __entity: s.entity,
        __markColor: col.emph ? col.stroke : t.dim,
        __points: s.points,
        // Speed is the vertical axis: it is the measurement being compared,
        // and a reader scanning a column of codecs at one ratio is the
        // question this chart is usually asked.
        data: s.points.map((r) => ({ x: r.ratio, y: r[metricId] })),
        showLine: true,
        borderColor: col.stroke,
        // Context is drawn in ink at full contrast, so it has to give the
        // weight back or it would shout louder than the series it frames.
        borderWidth: col.emph ? 2 : 1,
        borderJoinStyle: 'round',
        borderCapStyle: 'round',
        tension: 0,
        pointRadius: 5,
        pointHoverRadius: 7,
        pointHitRadius: 0, // the custom mode owns hit-testing
        // Secondary encoding: eight slots is far past what hue alone can
        // separate under CVD, so the marker shape and the dash pattern say it
        // too. markFor carries the shape, because whether a shape is open also
        // decides how its mark is coloured.
        ...markFor(col),
        borderDash: col.dash,
        order: col.emph ? 0 : 1,
      });
    }
    // Emphasised last: they draw on top and win hover ties.
    datasets.sort((a, b) => Number(a.__emph) - Number(b.__emph));

    const m = METRICS[metricId];
    const cfg = {
      type: 'scatter',
      data: { datasets },
      options: {
        parsing: false,
        interaction: { mode: 'nearestWithin', intersect: false, maxDistance: 24 },
        plugins: {
          tooltip: {
            enabled: false,
            mode: 'nearestWithin',
            intersect: false,
            external: tooltipFor(p.tip, describePoint),
          },
        },
        scales: {
          x: axis(t, { title: 'Compression ratio (n:1, higher is smaller output)' }),
          // Linear from zero unless asked otherwise. Throughput spans two
          // orders of magnitude across codecs, so a log axis is often the
          // readable one -- but it flatters the slow end, and a reader who
          // did not choose it should not have to notice that it is on.
          y: axis(t, {
            title: `${m.label} (MB/s${state.logSpeed ? ', log' : ''})`,
            type: state.logSpeed ? 'logarithmic' : 'linear',
            beginAtZero: !state.logSpeed,
            short: true,
          }),
        },
      },
    };
    return cfg;
  }

  function describePoint(ds, dp) {
    const r = ds.__points[dp.dataIndex];
    const rows = [
      { value: METRICS[r.op === 'd' ? 'dspeed' : 'cspeed'].fmt(r.op === 'd' ? r.dspeed : r.cspeed), name: ds.__label },
      { value: fmtRatio(r.ratio), name: `${fmt.bytes(r.op === 'd' ? r.in : r.out)} output` },
    ];
    const head = r.levelName ? `${ds.__label} · ${r.levelName}` : ds.__label;
    const note = [
      r.src ? `decoding ${r.srcLabel}` : null,
      r.size ? fmt.windowLabel(r) : null,
      `${r.threads} thread${r.threads === 1 ? '' : 's'} · best of ${r.passes}`,
      clickHint(ds.__emph),
    ].filter(Boolean).join(' · ');
    return { head, rows, note };
  }

  function drawPanel(p, chartKey, res, state, metricId) {
    if (!res || !res.series.length) {
      p.root.style.display = 'none';
      return;
    }
    p.root.style.display = '';
    const cfg = build(p, res, state, metricId);
    if (charts[chartKey]) {
      charts[chartKey].data.datasets = cfg.data.datasets;
      charts[chartKey].options = cfg.options;
      charts[chartKey].update('none');
    } else {
      charts[chartKey] = createChart(p.canvas, cfg);
      clickToToggle(p.canvas, () => charts[chartKey], onToggleEntity);
    }
  }

  return {
    root: c.root,
    update(results, state) {
      last = { results, state };
      const any = [results.encode, results.decode].filter(Boolean)
        .some((r) => r.series.length);
      if (emptyState(c.root, panels, any ? '' : 'No measurements for this combination of corpus, threads and codecs.')) {
        leg.replaceChildren();
        twin.root.style.display = 'none';
        return;
      }
      twin.root.style.display = '';
      // Shared ratio domain so a codec sits in the same column in both panels
      // and the reader scans down; rounded so the axis reads 2 / 4 / 6 rather
      // than 1.4598912774400588. The speed axes stay independent: decoding is
      // several times faster, and forcing one scale on both would flatten the
      // compression panel to nothing.
      const all = [results.encode, results.decode].filter(Boolean)
        .flatMap((r) => r.series.flatMap((s) => s.points.map((p) => p.ratio)));
      const { min: xmin, max: xmax } = niceRange(all);

      drawPanel(pc, 'c', results.encode, state, 'cspeed');
      drawPanel(pd, 'd', results.decode, state, 'dspeed');
      for (const k of ['c', 'd']) {
        if (charts[k]) {
          charts[k].options.scales.x.min = xmin;
          charts[k].options.scales.x.max = xmax;
          charts[k].update('none');
        }
      }

      const entities = entitiesOf(results);
      legend(leg, entities, state, onToggleEntity);

      const rows = [results.encode, results.decode].filter(Boolean)
        .flatMap((r) => r.series.flatMap((s) => s.points));
      twin.setOpen((state.tables || []).includes('pareto'));
      renderTable(twin.body, rows, {
        columns: measurementColumns(results.dim),
        sort: ctx.sortOf(state, 'pareto', { key: 'ratio', dir: -1 }),
        onSort: (s) => ctx.onSort('pareto', s),
        emphasis: state.emphasis,
        onRow: (r) => onToggleEntity(r[results.dim] ?? r.codec),
      });
    },
    retheme() {
      refreshTokens();
      for (const k of ['c', 'd']) {
        if (charts[k]) { charts[k].destroy(); charts[k] = null; }
      }
      if (last) this.update(last.results, last.state);
    },
    setLoading(on) { c.root.dataset.loading = on ? '1' : '0'; },
  };
}

// niceRange snaps a data range to round tick values.
function niceRange(vals) {
  if (!vals.length) return {};
  const lo = Math.min(...vals);
  const hi = Math.max(...vals);
  const span = (hi - lo) || hi || 1;
  const mag = 10 ** Math.floor(Math.log10(span / 4));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((m) => span / m <= 6) ?? mag * 10;
  return {
    min: Math.max(0, (Math.floor(lo / step) - 0.5) * step),
    max: (Math.ceil(hi / step) + 0.5) * step,
  };
}

export function measurementColumns(dim) {
  return [
    { key: 'codec', label: 'Codec', ident: true, entity: dim, get: (r) => r.codec },
    // The table is the precise view and the CSV source, so it keeps the number
    // the -level flag takes, with the word beside it where they differ.
    { key: 'level', label: 'Level', get: (r) => (r.level == null ? '—'
      : (r.levelName === String(r.level) ? r.levelName : `${r.level} ${r.levelName}`)) },
    { key: 'src', label: 'Source', get: (r) => r.srcLabel || '—' },
    { key: 'op', label: 'Op', get: (r) => (r.op === 'c' ? 'compress' : 'decompress') },
    { key: 'corpus', label: 'Corpus', get: (r) => r.corpus },
    { key: 'threads', label: 'Threads', num: true, get: (r) => String(r.threads) },
    { key: 'window', label: 'Window', num: true, get: (r) => (r.window || !r.size ? fmt.windowName(r.window) : `${fmt.windowName(r.size)} (default)`) },
    { key: 'ratio', label: 'Ratio', num: true, get: (r) => fmtRatio(r.ratio) },
    { key: 'pct', label: '% reduction', num: true, get: (r) => r.pct.toFixed(2) },
    { key: 'cspeed', label: 'MB/s', num: true, get: (r) => (r.op === 'c' ? r.cspeed : r.dspeed).toFixed(1) },
    { key: 'ns', label: 'ms', num: true, get: (r) => fmt.ms(r.ns) },
    { key: 'out', label: 'Output', num: true, get: (r) => fmt.bytes(r.op === 'c' ? r.out : r.in) },
    { key: 'passes', label: 'Passes', num: true, get: (r) => String(r.passes) },
    { key: 'go', label: 'Go', get: (r) => r.go },
    { key: 'libVer', label: 'Library', get: (r) => r.libVer },
  ];
}

// ------------------------------------------------------------ ranking bars

// One measure at a time, linear from zero. Bars encode length from a baseline,
// so a log scale would break the contract the form makes with the reader.
export function barsCard(ctx) {
  const onToggleEntity = ctx.onToggleEntity;
  const c = card('Ranking', '');
  const p = panel('');
  p.heading.style.display = 'none';
  const twin = tableTwin('Table view', (open) => ctx.onTable('bars', open));
  c.root.append(p.root, twin.root);
  let chart = null;
  let last = null;

  return {
    root: c.root,
    update(results, state) {
      last = { results, state };
      const m = METRICS[state.metric] || METRICS.cspeed;
      c.sub.textContent = `${m.label}, ${m.better === 'up' ? 'higher' : 'lower'} is better`;

      const src = m.id === 'dspeed' ? results.decode : results.encode;
      if (emptyState(c.root, p.root, src && src.series.length ? '' : `No ${m.label.toLowerCase()} measurements in this slice.`)) {
        twin.root.style.display = 'none';
        return;
      }
      twin.root.style.display = '';

      const t = tokens();
      const rows = src.series.flatMap((s) => s.points.map((r) => ({ r, entity: s.entity, label: s.label })));
      rows.sort((a, b) => (b.r[m.id] ?? 0) - (a.r[m.id] ?? 0));
      const top = rows.slice(0, 24);

      // Codec and level alone repeat as soon as anything else is in scope: a
      // thread sweep, a window sweep, two Go versions, two library versions.
      // Whatever actually varies among the bars on screen joins the label, so
      // no two bars are labelled the same.
      const varies = (get) => new Set(top.map((x) => get(x.r))).size > 1;
      const extra = [
        [varies((r) => r.threads), (r) => `${r.threads}t`],
        [varies((r) => r.window), (r) => `w${fmt.windowName(r.size)}`],
        [varies((r) => r.go), (r) => r.go],
        [varies((r) => r.libVer), (r) => r.libVer],
      ].filter(([on]) => on);
      const barLabel = (r) => [baseLabel(r),
        ...extra.map(([, get]) => get(r))].join(' · ');

      // The box grows with the data instead of holding 24 bars in a fixed 340
      // px: too little room and Chart.js quietly drops tick labels, leaving a
      // ranking whose names do not line up with its bars. 26px a row is the
      // 24px bar plus the 2px surface gap.
      p.canvas.parentElement.style.height = `${top.length * 26 + 52}px`;

      const barCols = top.map((x) => colorsFor(x.entity, state.emphasis));
      const cfg = {
        type: 'bar',
        data: {
          labels: top.map((x) => barLabel(x.r)),
          datasets: [{
            label: m.label,
            __label: m.label,
            __points: top.map((x) => x.r),
            // One dataset holds every bar, so the entity is per index here.
            __entities: top.map((x) => x.entity),
            data: top.map((x) => x.r[m.id] ?? 0),
            // Context bars are outlined, not filled. A bar's weight is its
            // area, so at ink contrast a solid one would read as the most
            // important thing on the chart -- the same trade the lines make
            // by dropping to 1px.
            backgroundColor: barCols.map((c) => (c.emph ? c.fill : 'transparent')),
            borderColor: barCols.map((c) => (c.emph ? c.fill : t.dim)),
            borderWidth: barCols.map((c) => (c.emph ? 0 : 1)),
            borderRadius: 4,
            borderSkipped: 'start', // rounded data-end, square at the baseline
            maxBarThickness: 24,
          }],
        },
        options: {
          indexAxis: 'y',
          interaction: { mode: 'nearest', intersect: true },
          plugins: {
            tooltip: {
              enabled: false,
              external: tooltipFor(p.tip,
                (ds, dp) => describeBar(ds.__points[dp.dataIndex], barCols[dp.dataIndex]?.emph)),
            },
          },
          scales: {
            x: axis(t, { title: `${m.label} (${m.unit})`, beginAtZero: true }),
            y: { ...axis(t, { type: 'category' }), grid: { display: false } },
          },
        },
      };

      if (chart) {
        chart.data = cfg.data;
        chart.options = cfg.options;
        chart.update('none');
      } else {
        chart = createChart(p.canvas, cfg);
        clickToToggle(p.canvas, () => chart, onToggleEntity);
      }

      twin.setOpen((state.tables || []).includes('bars'));
      renderTable(twin.body, top.map((x) => x.r), {
        columns: measurementColumns(results.dim),
        sort: ctx.sortOf(state, 'bars', { key: m.id === 'dspeed' ? 'cspeed' : m.id, dir: -1 }),
        onSort: (s) => ctx.onSort('bars', s),
        emphasis: state.emphasis,
        onRow: (r) => onToggleEntity(r[results.dim] ?? r.codec),
      });
    },
    retheme() {
      refreshTokens();
      if (chart) { chart.destroy(); chart = null; }
      if (last) this.update(last.results, last.state);
    },
    setLoading(on) { c.root.dataset.loading = on ? '1' : '0'; },
  };
}

// ----------------------------------------------------------- thread scaling

// Indexed to one thread, because absolute speeds differ by two orders of
// magnitude and indexing is the sanctioned way to share one axis.
export function threadsCard(db, ctx) {
  const onToggleEntity = ctx.onToggleEntity;
  const c = card('Thread scaling', 'speed-up against the same codec at one thread');
  const p = panel('');
  p.heading.style.display = 'none';
  const note = el('p', 'sub');
  const twin = tableTwin('Table view', (open) => ctx.onTable('threads', open));
  c.root.append(p.root, note, twin.root);
  let chart = null;
  let last = null;

  return {
    root: c.root,
    update(results, state) {
      last = { results, state };
      const src = results.scaling;
      const metric = results.scalingOp === 'd' ? 'dspeed' : 'cspeed';
      const threads = [...new Set((src?.series ?? [])
        .flatMap((s) => s.points.map((r) => r.threads)))].sort((a, b) => a - b);
      if (!src || threads.length < 2) { c.root.style.display = 'none'; return; }
      c.root.style.display = '';

      const t = tokens();
      const datasets = [];
      const flat = [];
      const nonConc = [];
      for (const s of src.series) {
        const byThread = new Map();
        for (const r of s.points) {
          const cur = byThread.get(r.threads);
          if (!cur || r[metric] > cur[metric]) byThread.set(r.threads, r);
        }
        const base = byThread.get(threads[0]);
        if (!base) continue;
        const col = colorsFor(s.entity, state.emphasis);
        const pts = threads.map((n) => {
          const r = byThread.get(n);
          return r ? r[metric] / base[metric] : null;
        });
        threads.forEach((n, i) => {
          const r = byThread.get(n);
          if (r) flat.push({ ...r, speedup: pts[i] });
        });
        const info = db.codecs.get(s.entity);
        const role = results.scalingOp === 'd' ? info?.decode : info?.encode;
        if (info && role && (role.conc ?? []).length === 0) nonConc.push(s.label);
        datasets.push({
          label: s.label,
          __label: s.label,
          __endLabel: s.endLabel,
          __emph: col.emph,
          __entity: s.entity,
          __markColor: col.emph ? col.stroke : t.dim,
          __threads: threads,
          data: pts,
          borderColor: col.stroke,
          borderWidth: col.emph ? 2 : 1,
          borderDash: col.dash,
          pointRadius: 5,
          ...markFor(col),
          pointHitRadius: 0,
          tension: 0,
        });
      }
      datasets.sort((a, b) => Number(a.__emph) - Number(b.__emph));

      note.textContent = nonConc.length
        ? `Declared single-threaded, so a flat line is the expected result: ${nonConc.join(', ')}.`
        : '';

      const cfg = {
        type: 'line',
        data: { labels: threads.map(String), datasets },
        options: {
          interaction: { mode: 'nearestWithin', intersect: false, maxDistance: 24 },
          plugins: {
            tooltip: {
              enabled: false,
              mode: 'nearestWithin',
              intersect: false,
              external: tooltipFor(p.tip, (ds, dp) => ({
                head: `${ds.__label} at ${ds.__threads[dp.dataIndex]} threads`,
                rows: [{ value: `${(dp.raw ?? 0).toFixed(2)}×`, name: 'vs 1 thread' }],
                note: clickHint(ds.__emph),
              })),
            },
          },
          scales: {
            x: { ...axis(t, { title: 'Threads', type: 'category' }), grid: { display: false } },
            y: axis(t, { title: 'Speed-up vs 1 thread (×)', beginAtZero: true }),
          },
        },
      };

      if (chart) {
        chart.data = cfg.data;
        chart.options = cfg.options;
        chart.update('none');
      } else {
        chart = createChart(p.canvas, cfg);
        clickToToggle(p.canvas, () => chart, onToggleEntity);
      }

      twin.setOpen((state.tables || []).includes('threads'));
      renderTable(twin.body, flat, {
        columns: [
          ...measurementColumns(results.dim).slice(0, 6),
          { key: 'speedup', label: 'Speed-up', num: true, get: (r) => `${r.speedup.toFixed(2)}×` },
        ],
        sort: ctx.sortOf(state, 'threads', { key: 'threads', dir: 1 }),
        onSort: (s) => ctx.onSort('threads', s),
        emphasis: state.emphasis,
        onRow: (r) => onToggleEntity(r[results.dim] ?? r.codec),
      });
    },
    retheme() {
      refreshTokens();
      if (chart) { chart.destroy(); chart = null; }
      if (last) this.update(last.results, last.state);
    },
    setLoading(on) { c.root.dataset.loading = on ? '1' : '0'; },
  };
}

// -------------------------------------------------------------- provenance

// Not a chart. This is what makes the numbers citable.
export function provenanceCard(db) {
  const c = card('Provenance', 'what produced these numbers');
  const body = el('div');
  const btn = el('button');
  btn.type = 'button';
  btn.textContent = 'Copy CSV';
  c.spacer.after(btn);
  c.root.append(body);
  let lastRows = [];
  let lastDim = 'codec';

  btn.addEventListener('click', () => copyCSV(lastRows, measurementColumns(lastDim)));

  return {
    root: c.root,
    update(results, state, scope) {
      lastDim = results.dim;
      lastRows = [results.encode, results.decode].filter(Boolean)
        .flatMap((r) => r.series.flatMap((s) => s.points));
      body.replaceChildren();

      const dl = el('dl', 'meta');
      const add = (k, v, cls) => {
        const dt = el('dt', cls); dt.textContent = k;
        const dd = el('dd', cls); dd.textContent = v;
        dl.append(dt, dd);
      };
      // The runs used to be told apart by two leading spaces on each library
      // name, which HTML collapses away. A rule between them does the job.
      scope.runs.forEach((run, i) => {
        const m = db.machines.get(run.machine);
        const corpus = db.corpora.get(run.corpus);
        add(`${corpus?.name || run.corpus}`,
          `${m?.label || run.machine} · ${m?.cpu?.physical_cores ?? '?'} cores / ${m?.cpu?.logical_cores ?? '?'} threads`
          + ` · x86-64-v${m?.cpu?.x64level ?? '?'} · L3 ${fmt.bytes(m?.cpu?.cache?.l3 ?? 0)}`
          + ` · ${run.go} ${run.goos}/${run.goarch}${run.goamd64 ? ` GOAMD64=${run.goamd64}` : ''}`
          + ` · bench ${run.bench} · updated ${run.updated.slice(0, 10)}`,
          i ? 'run sep' : 'run');
        for (const [mod, vers] of Object.entries(run.libs || {})) {
          add(mod, vers.join(', '));
        }
      });
      body.append(dl);
    },
    retheme() {},
    setLoading(on) { c.root.dataset.loading = on ? '1' : '0'; },
  };
}
