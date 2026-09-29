// Composition root. The render pipeline is scope -> ensure -> query -> paint,
// which puts the one await in exactly one place.

import { openDB, QueryError } from './data/db.js';
import { createLoader } from './data/loader.js';
import { buildQueries } from './queries.js';
import {
  createStore, DEFAULTS, decode, emphasise, encode, reconcile, sortOf, withSort, withTable,
} from './state.js';
import { barsCard, paretoCard, provenanceCard, threadsCard } from './view/cards.js';
import { createControls } from './view/controls.js';
import { el } from './view/chart.js';
import { SLOTS, initTheme, refreshTokens } from './view/theme.js';

const $ = (id) => document.getElementById(id);

async function main() {
  let db;
  try {
    db = await openDB(createLoader());
  } catch (err) {
    fatal(`Could not load the database: ${err.message}. `
      + 'Run "compress-site run" to produce web/data, then "compress-site serve".');
    return;
  }
  if (!db.index.runs.length) {
    fatal('No benchmark runs recorded yet. Run "compress-site run" to populate web/data.');
    return;
  }

  // Open on one corpus at the widest thread count that corpus actually has: a
  // Pareto line only reads as a level sweep when the rest of the slice is held
  // still, and the thread domain is per-corpus, not global.
  const seeded = decode(location.hash, db.domains());
  if (!location.hash) {
    const d = db.domains(seeded);
    if (d.corpora.length) seeded.corpora = [d.corpora[0]];
    const have = db.index.runs
      .filter((r) => r.kind === seeded.kind && seeded.corpora.includes(r.corpus))
      .flatMap((r) => r.threads);
    if (have.length) seeded.threads = [Math.max(...have)];
  }
  const store = createStore(seeded);
  // Everything a card can change goes through the store, which is what keeps
  // the URL a faithful description of what is on screen.
  const ctx = {
    onToggleEntity: (id) => store.set({ emphasis: emphasise(store.get().emphasis, id) }),
    onSort: (cardId, sort) => store.set({ sorts: withSort(store.get(), cardId, sort) }),
    onTable: (cardId, open) => store.set({ tables: withTable(store.get(), cardId, open) }),
    sortOf,
  };

  const cards = [
    paretoCard(ctx),
    barsCard(ctx),
    threadsCard(db, ctx),
    provenanceCard(db),
  ];
  for (const c of cards) $('cards').append(c.root);

  const controls = createControls($('filters'), db, (patch) => store.set(patch));

  const theme = initTheme(() => {
    refreshTokens();
    for (const c of cards) c.retheme();
  });
  $('theme-toggle').textContent = `Theme: ${theme.mode()}`;
  $('theme-toggle').addEventListener('click', () => {
    $('theme-toggle').textContent = `Theme: ${theme.cycle()}`;
  });
  $('copy-link').addEventListener('click', async () => {
    await navigator.clipboard.writeText(location.href);
    $('copy-link').textContent = 'Copied';
    setTimeout(() => { $('copy-link').textContent = 'Copy link'; }, 1200);
  });

  let seq = 0;
  let applyingHash = false;

  // Dropping a selection widens the domains that selection was narrowing, so
  // reconciling can make more work for itself: losing a corpus of the wrong
  // kind puts every kind back in reach. It runs to a fixed point before
  // anything is painted. Two rounds is the most it has needed; the cap is
  // there so a future filter cannot spin the render.
  function settle(state) {
    for (let i = 0; i < 4; i++) {
      const next = reconcile(state, db.domains(state));
      if (encode(next) === encode(state)) break;
      state = next;
    }
    return state;
  }

  async function render(state) {
    const settled = settle(state);
    if (encode(settled) !== encode(state)) {
      store.set(settled); // re-enters render with the repaired state
      return;
    }
    const my = ++seq;
    controls.render(state);

    const scope = db.resolveScope(state);
    banner(scope.warnings, scope.runs.length);
    if (!scope.runs.length) {
      for (const c of cards) c.root.style.display = 'none';
      $('subtitle').textContent = 'no runs match the current filters';
      return;
    }
    for (const c of cards) c.root.style.display = '';

    if (db.needsFetch(scope.runs)) for (const c of cards) c.setLoading(true);
    await db.ensure(scope.runs);
    if (my !== seq) return; // a newer render started while this one awaited

    let results;
    try {
      results = buildQueries(db, state);
    } catch (err) {
      if (err instanceof QueryError) {
        banner([{ code: err.message, text: 'Single-file and many-file corpora cannot share a chart.' }], scope.runs.length);
        return;
      }
      throw err;
    }

    // Seed the emphasis with what is in scope, so the first view is not all grey.
    const entities = [...new Set([results.encode, results.decode].filter(Boolean)
      .flatMap((r) => r.series.map((s) => s.entity)))];
    if (!state.emphasis.some(Boolean) && entities.length) {
      const seed = ['zskp', 's2', 'gzkp', 'lz4', 'minlz', 'br', 'stdzstd', 'snappy']
        .filter((x) => entities.includes(x));
      const pick = [...seed, ...entities.filter((x) => !seed.includes(x))].slice(0, SLOTS);
      state = { ...state, emphasis: Array.from({ length: SLOTS }, (_, i) => pick[i] ?? null) };
    }

    for (const c of cards) {
      c.update(results, state, scope);
      c.setLoading(false);
    }

    $('subtitle').textContent =
      `${state.kind === 'dir' ? 'Many-file corpora' : 'Single-file corpora'} · `
      + `${scope.runs.length} run${scope.runs.length === 1 ? '' : 's'} · ${db.rowCount().toLocaleString()} measurements loaded`;

    applyingHash = true;
    history.replaceState(null, '', `#${encode(state)}`);
    queueMicrotask(() => { applyingHash = false; });
  }

  function banner(warnings, runCount) {
    const host = $('banner');
    host.replaceChildren();
    if (!warnings.length) return;
    const p = el('div');
    const strong = el('strong');
    strong.textContent = `Comparing ${runCount} runs that are not directly comparable: `;
    p.append(strong);
    const ul = el('ul');
    for (const w of warnings) {
      const li = el('li');
      li.textContent = w.text;
      ul.append(li);
    }
    host.append(p, ul);
  }

  window.addEventListener('hashchange', () => {
    if (applyingHash) return;
    store.set(decode(location.hash, db.domains()));
  });

  store.subscribe(render);
  await render(store.get());
}

function fatal(msg) {
  const d = el('div', 'empty');
  d.textContent = msg;
  $('cards').replaceChildren(d);
}

main().catch((err) => {
  console.error(err);
  fatal(`Unexpected error: ${err.message}`);
});

export { DEFAULTS };
