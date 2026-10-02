// The single filter row. Every chart, table and stat below re-renders against
// the same slice, so the numbers always agree.

import { variantLabel } from '../data/db.js';
import { windowName } from '../format.js';
import { el } from './chart.js';

// A control with one option filters nothing, so it is not shown. That is the
// same rule that hides a Go-version list once only one Go version is left in
// reach -- the filter row describes what the data still offers, not what the
// schema allows.
export function createControls(host, db, onChange) {
  // Capture, so this runs before the checkbox handler rebuilds the row and
  // detaches the element the click came from. A panel left hanging over a
  // chart is worse than one click to reopen it.
  document.addEventListener('click', (e) => {
    if (host.contains(e.target)) return;
    for (const d of host.querySelectorAll('details.drop[open]')) d.open = false;
  }, true);

  return {
    render(state) {
      const d = db.domains(state);
      host.replaceChildren();
      const add = (node) => { if (node) host.append(node); };

      add(some(d.kinds) && segment('Corpus kind', [
        { v: 'file', label: 'Single file' },
        { v: 'dir', label: 'Many files' },
      ].filter((o) => d.kinds.includes(o.v)), state.kind,
      (v) => onChange({ kind: v, corpora: [], codecs: [] })));

      add(some(d.corpora) && multi('Corpus', d.corpora, state.corpora,
        (v) => onChange({ corpora: v }), (id) => db.corpora.get(id)?.name || id));

      add(some(d.machines) && multi('CPU', d.machines, state.machines,
        (v) => onChange({ machines: v }), (id) => db.machines.get(id)?.label || id));

      add(some(d.gos) && multi('Go version', d.gos, state.gos, (v) => onChange({ gos: v })));

      add(some(d.benches) && single('Bench version', ['', ...d.benches], state.bench ?? '',
        (v) => onChange({ bench: v || null }), (v) => (v === '' ? 'newest' : v)));

      // The settings the database refused to merge together. Present only when
      // some run actually disagrees with another, which is almost never.
      add(some(d.variants) && multi('Settings', d.variants, state.variants,
        (v) => onChange({ variants: v }), variantLabel));

      add(some(d.threads) && chips('Threads', d.threads, state.threads,
        (v) => onChange({ threads: v }), (n) => String(n)));

      add(some(d.windows) && chips('Window', d.windows, state.windows,
        (v) => onChange({ windows: v }), windowName));

      // Only the classes the scope actually has a level of: a Special button
      // with no special level behind it would do nothing.
      add(some(d.levelClasses) && toggles('Levels', d.levelClasses, state.levelClasses,
        (v) => onChange({ levelClasses: v }), (c) => ({
          regular: 'Regular', uncompressed: 'Uncompressed', special: 'Special',
        }[c])));

      add(some(d.ops) && segment('Operation', [
        { v: 'both', label: 'Both' },
        { v: 'c', label: 'Compress' },
        { v: 'd', label: 'Decompress' },
      ], state.op, (v) => onChange({ op: v })));

      // Comparing along a filter its default holds to one value -- the default
      // window, the picked CPU or thread count -- would draw one series, so
      // choosing it opens that filter at the same time.
      const opens = { window: 'windows', machine: 'machines', threads: 'threads' };
      add(single('Compare by', ['codec', 'go', 'corpus', 'machine', 'threads', 'lib', 'window'],
        state.compareBy, (v) => onChange({
          compareBy: v, ...(state[opens[v]]?.length === 1 ? { [opens[v]]: [] } : {}),
        }), (v) => ({
          codec: 'Codec', go: 'Go version', corpus: 'Corpus',
          machine: 'CPU', threads: 'Threads', lib: 'Library version', window: 'Window size',
        }[v])));

      const measures = ['cspeed', 'dspeed', 'ratio', 'pct']
        .filter((m) => (m === 'cspeed' ? d.ops.includes('c') : true))
        .filter((m) => (m === 'dspeed' ? d.ops.includes('d') : true));
      add(single('Measure', measures, state.metric,
        (v) => onChange({ metric: v }), (v) => ({
          cspeed: 'Compression MB/s', dspeed: 'Decompression MB/s',
          ratio: 'Compression ratio', pct: '% reduction',
        }[v])));

      add(segment('Speed axis', [
        { v: false, label: 'Linear' },
        { v: true, label: 'Log' },
      ], state.logSpeed, (v) => onChange({ logSpeed: v })));

      if (state.op !== 'c' && d.ops.includes('d')) {
        add(single('Decode source', ['own', 'all'], state.srcPolicy,
          (v) => onChange({ srcPolicy: v }), (v) => (v === 'own'
            ? 'each decoder on its own output'
            : 'every encoder variant')));
      }

      add(some(d.libs) && multi('Library version', d.libs, state.libs,
        (v) => onChange({ libs: v }), shortLib));

      add(some(d.codecs) && multi('Codec', d.codecs, state.codecs, (v) => onChange({ codecs: v })));
    },
  };
}

const some = (values) => values.length > 1;

function shortLib(l) {
  const at = l.lastIndexOf('@');
  const mod = l.slice(0, at);
  return `${mod.split('/').pop()} ${l.slice(at + 1)}`;
}

// A bare <label> next to a <select> is not an association: a screen reader
// announces the control as unlabelled. The ids are generated because the whole
// filter row is rebuilt on every render.
let fieldSeq = 0;

// forControl picks the element: a <label> only when there is one control to
// point `for` at. A button group gets a <span> the group references with
// aria-labelledby instead -- a <label> associated with nothing is itself an
// accessibility error, which is what the first attempt at this traded up to.
// The clear button sits beside the label rather than inside it, so it stays
// out of the accessible name aria-labelledby builds from that label.
function field(label, forControl = true, note) {
  const wrap = el('div', 'filter');
  const head = el('div', 'head');
  const lab = document.createElement(forControl ? 'label' : 'span');
  lab.className = 'lab';
  lab.id = `flab-${++fieldSeq}`;
  lab.textContent = note ? `${label} (${note})` : label;
  head.append(lab);
  wrap.append(head);
  wrap.__lab = lab;
  wrap.__head = head;
  return wrap;
}

// labelled ties a control to the label field() just made.
function labelled(wrap, control) {
  const id = wrap.__lab.id.replace('flab-', 'fctl-');
  control.id = id;
  control.name = id;
  wrap.__lab.htmlFor = id;
  wrap.append(control);
  return wrap;
}

function segment(label, options, value, onPick) {
  const wrap = field(label, false);
  const seg = el('div', 'segment');
  seg.setAttribute('role', 'group');
  seg.setAttribute('aria-labelledby', wrap.__lab.id);
  for (const o of options) {
    const b = el('button');
    b.type = 'button';
    b.textContent = o.label;
    b.setAttribute('aria-pressed', String(o.v === value));
    b.addEventListener('click', () => onPick(o.v));
    seg.append(b);
  }
  wrap.append(seg);
  return wrap;
}

// toggles is a row of independent on/off buttons. Unlike chips, nothing on
// means nothing shown rather than no filter, so there is no "all" and no clear.
function toggles(label, values, selected, onPick, fmt) {
  const wrap = field(label, false);
  const box = el('div', 'segment');
  box.setAttribute('role', 'group');
  box.setAttribute('aria-labelledby', wrap.__lab.id);
  for (const v of values) {
    const b = el('button');
    b.type = 'button';
    b.textContent = fmt(v);
    const on = selected.includes(v);
    b.setAttribute('aria-pressed', String(on));
    b.addEventListener('click', () => onPick(on
      ? selected.filter((x) => x !== v)
      : values.filter((x) => x === v || selected.includes(x))));
    box.append(b);
  }
  wrap.append(box);
  return wrap;
}

function single(label, values, value, onPick, fmt = (v) => v) {
  const wrap = field(label);
  const sel = el('select');
  for (const v of values) {
    const o = el('option');
    o.value = v;
    o.textContent = fmt(v);
    o.selected = v === value;
    sel.append(o);
  }
  sel.addEventListener('change', () => onPick(sel.value));
  return labelled(wrap, sel);
}

// Checkboxes, not <select multiple>: picking a second codec should not require
// knowing that ctrl+click exists, and a stray click cannot wipe the selection.
// The list is collapsed because a 15-codec box next to a one-line dropdown
// makes the filter row a ragged staircase; closed, every control is one line
// and the row reads as a row. Nothing ticked means no filter, which the
// summary says as "all".
function multi(label, values, selected, onPick, fmt = (v) => v) {
  const wrap = field(label, false);
  if (selected.length) wrap.__head.append(clear(label, () => onPick([])));

  const drop = el('details', 'drop');
  // Native exclusive accordion: opening one list closes the last, so two
  // panels never overlap each other.
  drop.name = 'filter-drop';
  const head = el('summary');
  const shown = summarise(selected, fmt);
  // Its own element so it can ellipsise: text-overflow does not apply to a
  // bare text node beside the chevron in a flex row.
  const val = el('span', 'val');
  val.textContent = shown;
  head.append(val);
  head.setAttribute('aria-label', `${label}: ${shown}`);

  // The whole row is rebuilt on every change, so which list is open and where
  // it is scrolled has to be carried across: otherwise ticking a second codec
  // means reopening the list and finding your place in it again.
  drop.open = openDrop === label;
  drop.addEventListener('toggle', () => { openDrop = drop.open ? label : null; });

  const box = el('div', 'checks');
  box.setAttribute('role', 'group');
  box.setAttribute('aria-labelledby', wrap.__lab.id);
  for (const v of values) {
    const row = el('label');
    const cb = el('input');
    cb.type = 'checkbox';
    cb.checked = selected.includes(v);
    // Rebuilt from the domain rather than appended to, so the order in the URL
    // is the order on screen however the boxes were clicked.
    cb.addEventListener('change', () => onPick(
      values.filter((x) => (x === v ? cb.checked : selected.includes(x)))));
    row.append(cb, document.createTextNode(fmt(v)));
    box.append(row);
  }
  box.addEventListener('scroll', () => { dropScroll = box.scrollTop; });
  if (drop.open) queueMicrotask(() => { box.scrollTop = dropScroll; });

  drop.append(head, box);
  wrap.append(drop);
  return wrap;
}

let openDrop = null;
let dropScroll = 0;

// What the collapsed control says it is doing. Two names fit; more would push
// the row out of shape, so past that it is a count.
function summarise(selected, fmt) {
  if (!selected.length) return 'all';
  if (selected.length <= 2) return selected.map(fmt).join(', ');
  return `${selected.length} selected`;
}

function chips(label, values, selected, onPick, fmt) {
  const wrap = field(label, false, selected.length ? null : 'all');
  if (selected.length) wrap.__head.append(clear(label, () => onPick([])));
  const box = el('div', 'chips');
  box.setAttribute('role', 'group');
  box.setAttribute('aria-labelledby', wrap.__lab.id);
  for (const v of values) {
    const b = el('button');
    b.type = 'button';
    b.textContent = fmt(v);
    const on = selected.includes(v);
    b.setAttribute('aria-pressed', String(on));
    b.addEventListener('click', () => {
      onPick(on ? selected.filter((x) => x !== v) : values.filter((x) => x === v || selected.includes(x)));
    });
    box.append(b);
  }
  wrap.append(box);
  return wrap;
}

function clear(label, onClick) {
  const b = el('button', 'clear');
  b.type = 'button';
  b.textContent = 'clear';
  b.setAttribute('aria-label', `Clear the ${label} filter`);
  b.addEventListener('click', onClick);
  return b;
}
