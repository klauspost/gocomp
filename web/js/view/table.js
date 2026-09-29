// One generic table implementation serving every card. This is the WCAG-clean
// twin of each chart and the keyboard path to every value, which is what lets
// the charts use hover at all.

import { colorsFor } from './theme.js';
import { el } from './chart.js';

export function renderTable(host, rows, { columns, sort, onRow, onSort, emphasis = [] }) {
  host.replaceChildren();
  if (!rows.length) {
    const p = el('div', 'empty');
    p.textContent = 'No measurements match the current filters.';
    host.append(p);
    return;
  }

  const wrap = el('div', 'table-wrap');
  const table = el('table');
  const thead = el('thead');
  const hr = el('tr');

  for (const c of columns) {
    const th = el('th', c.num ? 'num' : '');
    th.textContent = c.label;
    th.scope = 'col';
    if (sort?.key === c.key) th.setAttribute('aria-sort', sort.dir > 0 ? 'ascending' : 'descending');
    th.addEventListener('click', () => {
      const next = { key: c.key, dir: sort?.key === c.key ? -sort.dir : (c.num ? -1 : 1) };
      // Sort lives in the URL, so re-render goes through state rather than
      // mutating this table in place.
      if (onSort) onSort(next);
      else renderTable(host, rows, { columns, sort: next, onRow, emphasis });
    });
    hr.append(th);
  }
  thead.append(hr);
  table.append(thead);

  const data = [...rows];
  if (sort) {
    const { key, dir } = sort;
    data.sort((a, b) => {
      const x = a[key], y = b[key];
      if (typeof x === 'number' && typeof y === 'number') return (x - y) * dir;
      return String(x).localeCompare(String(y)) * dir;
    });
  }

  const tbody = el('tbody');
  for (const r of data) {
    const tr = el('tr');
    for (const c of columns) {
      const td = el('td', c.num ? 'num' : '');
      if (c.ident) {
        // Identity is a coloured dot beside text, never coloured text.
        const box = el('span', 'ident');
        const col = colorsFor(r[c.entity ?? 'codec'], emphasis);
        const dot = el('span', `dot ${col.shape}`);
        dot.style.background = col.emph ? col.fill : 'transparent';
        dot.style.boxShadow = `inset 0 0 0 2px ${col.emph ? col.fill : col.ring}`;
        const txt = document.createTextNode(c.get(r));
        box.append(dot, txt);
        td.append(box);
      } else {
        td.textContent = c.get(r);
      }
      tr.append(td);
    }
    if (onRow) tr.addEventListener('click', () => onRow(r));
    tbody.append(tr);
  }
  table.append(tbody);
  wrap.append(table);
  host.append(wrap);
}

// tableTwin wraps a table in the collapsible disclosure every chart card gets.
export function tableTwin(label = 'Table view', onToggle) {
  const details = el('details', 'table-twin');
  const summary = el('summary');
  summary.textContent = label;
  const body = el('div');
  details.append(summary, body);
  if (onToggle) details.addEventListener('toggle', () => onToggle(details.open));
  return {
    root: details,
    body,
    setOpen(open) { if (details.open !== open) details.open = open; },
  };
}

export function copyCSV(rows, columns) {
  const esc = (s) => (/[",\n]/.test(s) ? `"${s.replaceAll('"', '""')}"` : s);
  const lines = [columns.map((c) => esc(c.label)).join(',')];
  for (const r of rows) lines.push(columns.map((c) => esc(String(c.get(r)))).join(','));
  return navigator.clipboard.writeText(lines.join('\n'));
}
