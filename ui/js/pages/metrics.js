// Explorateur de métriques : plusieurs requêtes superposées, formules (a / b * 100) et fonctions (§ 30),
// unité et description des métriques, vues enregistrées ; un graphique par unité (un seul axe Y chacun).
import { h, clear, icon, debounce } from '../dom.js';
import { t } from '../i18n.js';
import { get, put, asList, queryMetrics, isAbort } from '../api.js';
import { fmtUnit, unitLabel, toMs } from '../format.js';
import { select, errorState, empty, panel, modal, field, toast, errorMessage, dropdown, menuItem } from '../components/ui.js';
import { autocomplete } from '../components/autocomplete.js';
import { timeChart, linesFromMetrics, summarize, colorAt } from '../components/chart.js';
import { dataTable } from '../components/table.js';
import { viewsMenu } from '../components/views.js';

const AGGS = ['avg', 'sum', 'min', 'max', 'count'];
const ROLLUPS = ['', 'avg', 'sum', 'min', 'max', 'last'];
const DISPLAYS = ['line', 'area', 'bars'];
const IDS = 'abcdefgh'.split('');
const UNITS = ['', 'percent', 'byte', 'byte_per_second', 'operation_per_second', 'packet_per_second', 'second', 'millisecond', 'count'];

// Fonctions du § 30 : nom, arguments par défaut (texte inséré après la requête).
export const FUNCTIONS = [
  ['abs', ''], ['log2', ''], ['log10', ''], ['cumsum', ''], ['per_second', ''], ['rate', ''], ['derivative', ''],
  ['moving_avg', '5'], ['ewma', '5'], ['median', '5'], ['timeshift', '-86400'], ['top', "5, 'mean', 'desc'"],
  ['clamp_min', '0'], ['clamp_max', '100'], ['fill', "'zero'"], ['forecast', "'linear', 86400"], ['anomalies', "'basic', 2"], ['outliers', "'mad', 3"],
];
const FN_NAMES = new Set(FUNCTIONS.map(([n]) => n));

export function buildQuery(q) {
  if (q.raw) return q.raw.trim();
  const filters = (q.filters || []).filter(Boolean);
  let s2 = (q.agg || 'avg') + ':' + (q.metric || '') + '{' + (filters.length ? filters.join(',') : '*') + '}';
  if (q.by && q.by.length) s2 += ' by {' + q.by.join(',') + '}';
  if (q.rollup && q.rollup.fn) s2 += '.rollup(' + q.rollup.fn + ', ' + (Number(q.rollup.s) || 60) + ')';
  for (const fn of q.fns || []) {
    if (!fn || !FN_NAMES.has(fn.name)) continue;
    const args = String(fn.args || '').trim();
    s2 = fn.name + '(' + s2 + (args ? ', ' + args : '') + ')';
  }
  return s2;
}

// Lecture inverse (requête texte vers champs), pour les liens et le mode texte.
export function parseQuery(str) {
  const m = /^\s*(avg|sum|min|max|count):([^{\s]+)\{([^}]*)\}(?:\s+by\s+\{([^}]*)\})?(?:\.rollup\(\s*(\w+)\s*,\s*(\d+)\s*\))?\s*$/.exec(str || '');
  if (!m) return null;
  return {
    agg: m[1], metric: m[2],
    filters: m[3].trim() === '*' || !m[3].trim() ? [] : m[3].split(',').map((x) => x.trim()).filter(Boolean),
    by: m[4] ? m[4].split(',').map((x) => x.trim()).filter(Boolean) : [],
    rollup: m[5] ? { fn: m[5], s: Number(m[6]) } : null,
  };
}

// Comme parseQuery, en ôtant les fonctions qui enveloppent la requête : moving_avg(avg:x{*}, 5).
export function parseQueryFull(str) {
  let cur = String(str || '').trim();
  const fns = [];
  for (let guard = 0; guard < 10; guard++) {
    const m = /^(\w+)\((.*)\)$/.exec(cur);
    if (!m || !FN_NAMES.has(m[1])) break;
    let depth = 0; let cut = -1;
    for (let i = 0; i < m[2].length; i++) {
      const ch = m[2][i];
      if (ch === '(' || ch === '{') depth++;
      else if (ch === ')' || ch === '}') depth--;
      else if (ch === ',' && depth === 0) { cut = i; break; }
    }
    const inner = cut >= 0 ? m[2].slice(0, cut) : m[2];
    fns.unshift({ name: m[1], args: cut >= 0 ? m[2].slice(cut + 1).trim() : '' });
    cur = inner.trim();
  }
  const base = parseQuery(cur);
  return base ? Object.assign(base, { fns }) : null;
}

function metricOf(q) {
  if (q.raw) { const p = parseQueryFull(q.raw); if (p) return p.metric; const m = /(?:avg|sum|min|max|count):([^{\s(),]+)\{/.exec(q.raw); return m ? m[1] : ''; }
  return q.metric || '';
}

const tagCache = new Map();
async function tagsFor(metric, signal) {
  if (!metric) return {};
  if (tagCache.has(metric)) return tagCache.get(metric);
  const r = await get('/metrics/tags', { metric }, { signal });
  const v = r && typeof r === 'object' && !Array.isArray(r) ? r : {};
  tagCache.set(metric, v);
  return v;
}

// Métadonnées (unité, description) : cache par métrique ; null = aucune.
const metaCache = new Map();
export async function metricMeta(metric, signal) {
  if (!metric) return null;
  if (metaCache.has(metric)) return metaCache.get(metric);
  let v = null;
  try {
    const r = await get('/metrics/metadata', { metric }, { signal });
    v = r && typeof r === 'object' ? (r.metadata && typeof r.metadata === 'object' ? r.metadata : r) : null;
  } catch (e) {
    if (isAbort(e)) throw e;
    v = null;
  }
  metaCache.set(metric, v);
  return v;
}

async function metricNames(q, signal) {
  return asList(await get('/metrics/names', { q }, { signal }), 'names', 'metrics').map(String);
}

function defaultQuery(id) {
  return { id, agg: 'avg', metric: 'system.cpu.usage', filters: [], by: ['host'], rollup: null, display: 'line', raw: null, fns: [], hidden: false };
}

function readState(params) {
  try {
    const raw = params.get('e');
    if (raw) {
      const arr = JSON.parse(raw);
      if (Array.isArray(arr) && arr.length) {
        return arr.slice(0, IDS.length).map((q, i) => Object.assign(defaultQuery(IDS[i]), q, { id: IDS[i], filters: Array.isArray(q.filters) ? q.filters : [], by: Array.isArray(q.by) ? q.by : [], fns: Array.isArray(q.fns) ? q.fns : [] }));
      }
    }
    const q = params.get('q');
    if (q) {
      const p = parseQueryFull(q);
      return [Object.assign(defaultQuery('a'), p || { raw: q })];
    }
  } catch (e) { /* état illisible : requête par défaut */ }
  return [defaultQuery('a')];
}

function readFormulas(params) {
  try {
    const arr = JSON.parse(params.get('f') || '[]');
    return Array.isArray(arr) ? arr.filter((f) => f && typeof f.formula === 'string').slice(0, 6) : [];
  } catch (e) { return []; }
}

// Contrôle léger d'une formule : identifiants connus, opérateurs, parenthèses équilibrées.
export function checkFormula(formula, ids) {
  const f = String(formula || '').trim();
  if (!f) return 'empty';
  let depth = 0;
  for (const ch of f) { if (ch === '(') depth++; else if (ch === ')') { depth--; if (depth < 0) return 'parens'; } }
  if (depth !== 0) return 'parens';
  const stripped = f.replace(/'[^']*'/g, '0');
  const words = stripped.match(/[A-Za-z_]\w*/g) || [];
  for (const w of words) if (!FN_NAMES.has(w) && !ids.includes(w)) return 'unknown:' + w;
  if (/[^\w\s+\-*/().,']/.test(stripped)) return 'chars';
  return null;
}

function tagBox({ values, placeholder, suggest, onChange, keysOnly }) {
  const box = h('div', { class: 'tagbox' });
  let list = values.slice();
  function redraw() {
    clear(box);
    for (const v of list) {
      box.appendChild(h('span', { class: ['chip', v.startsWith('!') ? 'neg' : null] }, h('span', { class: 'mono', style: { fontSize: '11px' } }, v),
        h('button', { type: 'button', title: t('action.remove'), onclick: () => { list = list.filter((x) => x !== v); redraw(); onChange(list); } }, icon('x', 10))));
    }
    const ac = autocomplete({
      placeholder: list.length ? '' : placeholder, mono: true, cls: 'sm',
      fetcher: async (text) => {
        const all = await suggest();
        const neg = text.startsWith('!');
        const needle = (neg ? text.slice(1) : text).toLowerCase();
        const out = [];
        if (keysOnly) {
          for (const k of Object.keys(all)) if (!list.includes(k) && k.toLowerCase().includes(needle)) out.push(k);
        } else {
          for (const [k, vals] of Object.entries(all)) {
            for (const v of (vals || []).slice(0, 200)) {
              const s2 = k + ':' + v;
              if (s2.toLowerCase().includes(needle) && !list.includes((neg ? '!' : '') + s2)) out.push((neg ? '!' : '') + s2);
              if (out.length > 60) break;
            }
          }
        }
        return out.sort();
      },
      onSelect: (v) => {
        const val = v.trim();
        if (!val) return;
        if (!keysOnly && !/^!?[^:]+:.+$/.test(val)) return;
        if (!list.includes(val)) { list.push(val); onChange(list); }
        redraw();
        setTimeout(() => { const i = box.querySelector('input'); if (i) i.focus(); }, 0);
      },
    });
    ac.input.style.border = '0';
    ac.input.style.background = 'transparent';
    ac.input.style.boxShadow = 'none';
    ac.input.addEventListener('keydown', (e) => {
      if (e.key === 'Backspace' && !ac.input.value && list.length) { list.pop(); onChange(list); redraw(); setTimeout(() => { const i = box.querySelector('input'); if (i) i.focus(); }, 0); }
    });
    ac.style.flex = '1 1 90px';
    box.appendChild(ac);
  }
  redraw();
  return box;
}

// Aide intégrée : opérateurs, fonctions, exemples ; un clic insère dans la cible choisie.
function helpPanel(onInsert) {
  const rows = FUNCTIONS.map(([name, args]) => h('tr', { class: 'clickable', title: t('fx.insert'), onclick: () => onInsert(name, args) },
    h('td', h('span', { class: 'mono small' }, name + '(x' + (args ? ', ' + args : '') + ')')),
    h('td', { class: 'wrap small text-2' }, t('fx.d.' + name))));
  return h('div', { class: 'fx-help' },
    h('div', { class: 'grid c2' },
      h('div', h('h4', { class: 'mb-2' }, t('fx.formulas')), h('div', { class: 'small text-2 col', style: { gap: '4px' } },
        h('div', t('fx.formulas_text')),
        h('div', { class: 'mono' }, 'a / b * 100'), h('div', { class: 'mono' }, '(a - b) / 1024'), h('div', { class: 'mono' }, 'moving_avg(a, 10)'),
        h('div', { class: 'mt-2' }, t('fx.operators')))),
      h('div', h('h4', { class: 'mb-2' }, t('fx.functions')), h('div', { class: 'small text-2' }, t('fx.functions_text')),
        h('div', { class: 'mono small mt-2' }, 'timeshift(avg:system.cpu.usage{host:web-01}, -604800)'))),
    h('div', { class: 'table-wrap mt-3', style: { maxHeight: '300px' } }, h('table', { class: 'tbl' },
      h('thead', h('tr', h('th', t('fx.signature')), h('th', t('col.description')))), h('tbody', rows))));
}

function metaDialog(metric, meta, onSaved) {
  const unit = select(UNITS.map((u) => ({ value: u, label: u ? t('unitname.' + u) + ' (' + u + ')' : t('dash.w.unit_auto') })), (meta && meta.unit) || '');
  const desc = h('textarea', { class: 'input', rows: 3, maxlength: 1000 });
  desc.value = (meta && meta.description) || '';
  const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
  const btn = h('button', { class: 'btn primary', type: 'button' }, t('action.save'));
  const m = modal({
    title: t('metrics.meta.edit_title', { metric }),
    body: h('div', { class: 'col gap-3' }, field(t('metrics.meta.unit'), unit), field(t('col.description'), desc), err),
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), btn],
  });
  btn.addEventListener('click', async () => {
    btn.disabled = true;
    try {
      await put('/metrics/metadata', { unit: unit.value || null, description: desc.value.trim() }, { query: { metric } });
      metaCache.delete(metric);
      m.close();
      toast(t('metrics.meta.saved'), 'ok');
      if (onSaved) onSaved();
    } catch (e) { btn.disabled = false; err.textContent = errorMessage(e); err.hidden = false; }
  });
}

export async function render(root, ctx) {
  ctx.setCrumbs([{ label: t('nav.metrics') }]);
  const { range, signal } = ctx;
  let queries = readState(ctx.params);
  let formulas = readFormulas(ctx.params);
  const canEditMeta = !!ctx.me && ['owner', 'admin', 'member'].includes(ctx.me.role);

  const editor = h('div');
  const formulaEl = h('div');
  const helpEl = h('div', { class: 'panel-body', style: { borderTop: '1px solid var(--border)' }, hidden: true });
  const chartsEl = h('div', { class: 'col gap-3' });
  const tableEl = h('div');
  let lastInput = null;

  const views = viewsMenu({
    page: 'metrics', me: ctx.me,
    getState: () => ({ e: queries.map(({ id, ...rest }) => rest), f: formulas, t: range.preset || null }),
    apply: (st) => {
      const up = { e: JSON.stringify(Array.isArray(st.e) ? st.e : []), f: Array.isArray(st.f) && st.f.length ? JSON.stringify(st.f) : null, q: null };
      if (st.t) Object.assign(up, { t: st.t, from: null, to: null });
      ctx.setParams(up);
    },
  });

  root.appendChild(h('section', { class: 'panel mb-3' },
    h('div', { class: 'panel-head' }, h('h2', t('metrics.queries')), h('span', { class: 'sub' }, t('metrics.syntax_hint')), h('span', { class: 'spacer' }),
      views,
      h('button', { class: 'btn sm', type: 'button', onclick: () => addQuery() }, icon('plus', 12), t('metrics.add_query')),
      h('button', { class: 'btn sm', type: 'button', onclick: () => addFormula() }, icon('fx', 12), t('metrics.add_formula')),
      h('button', { class: 'btn sm ghost', type: 'button', onclick: () => { helpEl.hidden = !helpEl.hidden; } }, icon('info', 12), t('fx.help')),
      h('button', { class: 'btn sm', type: 'button', onclick: () => createMonitor() }, icon('monitor', 12), t('metrics.create_monitor'))),
    h('div', { class: 'panel-body', style: { paddingTop: '0', paddingBottom: '0' } }, editor, formulaEl),
    helpEl));
  helpEl.appendChild(helpPanel((name, args) => insertFunction(name, args)));
  root.appendChild(chartsEl);
  root.appendChild(h('div', { class: 'mt-3' }, tableEl));

  function save() {
    ctx.setParams({ e: JSON.stringify(queries.map(({ id, ...rest }) => rest)), f: formulas.length ? JSON.stringify(formulas) : null, q: null }, { silent: true });
  }

  function addQuery() {
    if (queries.length >= IDS.length) return;
    const last = queries[queries.length - 1];
    queries.push(Object.assign({}, last ? JSON.parse(JSON.stringify(last)) : defaultQuery('a'), { id: IDS[queries.length], raw: null, hidden: false }));
    drawEditor();
    changed();
  }

  function addFormula() {
    if (formulas.length >= 6) return;
    const ids = queries.map((q) => q.id);
    formulas.push({ id: 'f' + (formulas.length + 1), formula: ids.length > 1 ? ids[0] + ' / ' + ids[1] + ' * 100' : ids[0] + ' * 1', alias: '' });
    drawFormulas();
    changed();
  }

  // Fonction de l'aide : ajoutée à la dernière formule ou requête touchée, sinon à la première requête.
  function insertFunction(name, args) {
    if (lastInput && lastInput.kind === 'formula' && formulas[lastInput.i]) {
      const f = formulas[lastInput.i];
      f.formula = name + '(' + f.formula + (args ? ', ' + args : '') + ')';
      drawFormulas();
    } else {
      const q = (lastInput && lastInput.kind === 'query' && queries[lastInput.i]) || queries[0];
      if (q.raw !== null && q.raw !== undefined) q.raw = name + '(' + q.raw + (args ? ', ' + args : '') + ')';
      else q.fns = (q.fns || []).concat([{ name, args }]);
      drawEditor();
    }
    toast(t('fx.inserted', { name }), 'ok', 1500);
    changed();
  }

  function createMonitor() {
    const first = queries.find((q) => q.metric || q.raw);
    if (!first) return;
    ctx.navigate('/monitors/new', { type: 'metric', q: buildQuery(first) });
  }

  function fnMenu(q) {
    const btn = h('button', { class: 'btn ghost sm icon', type: 'button', title: t('fx.add_function') }, icon('fx', 13));
    return dropdown(btn, (close) => h('div', { style: { maxHeight: '320px' } },
      h('div', { class: 'menu-head' }, t('fx.add_function')),
      FUNCTIONS.map(([name, args]) => menuItem(name + (args ? '(…, ' + args + ')' : '()'), () => { close(); q.fns = (q.fns || []).concat([{ name, args }]); drawEditor(); changed(); }))));
  }

  function drawEditor() {
    clear(editor);
    queries.forEach((q, i) => {
      q.id = IDS[i];
      q.fns = Array.isArray(q.fns) ? q.fns : [];
      const row = h('div', { class: ['mq', q.hidden ? 'mq-hidden' : null] });
      row.appendChild(h('span', { class: 'qid', style: { background: colorAt(i) } }, q.id));
      if (q.raw !== null && q.raw !== undefined) {
        const input = h('input', { class: 'input mono', value: q.raw, spellcheck: 'false', style: { gridColumn: 'span 5' } });
        input.addEventListener('input', () => { q.raw = input.value; changedSlow(); });
        input.addEventListener('focus', () => { lastInput = { kind: 'query', i }; });
        input.addEventListener('keydown', (e) => { if (e.key === 'Enter') changed(); });
        row.appendChild(input);
      } else {
        row.appendChild(select(AGGS.map((a) => ({ value: a, label: a })), q.agg, (v) => { q.agg = v; changed(); }, { title: t('metrics.space_agg') }));
        const ac = autocomplete({
          value: q.metric, placeholder: t('metrics.metric_ph'),
          fetcher: (text, sig) => metricNames(text, sig),
          onSelect: (v) => { if (v && v !== q.metric) { q.metric = v.trim(); drawEditor(); changed(); } },
        });
        ac.input.addEventListener('change', () => { if (ac.input.value.trim() !== q.metric) { q.metric = ac.input.value.trim(); drawEditor(); changed(); } });
        ac.input.addEventListener('focus', () => { lastInput = { kind: 'query', i }; });
        row.appendChild(ac);
        row.appendChild(tagBox({ values: q.filters, placeholder: t('metrics.filters_ph'), suggest: () => tagsFor(q.metric, signal).catch(() => ({})), onChange: (l) => { q.filters = l; changed(); } }));
        row.appendChild(tagBox({ values: q.by, placeholder: t('metrics.by_ph'), keysOnly: true, suggest: () => tagsFor(q.metric, signal).catch(() => ({})), onChange: (l) => { q.by = l; changed(); } }));
        const secs = h('input', { class: 'input', type: 'number', min: 15, step: 15, value: (q.rollup && q.rollup.s) || 60, style: { width: '72px' }, title: t('metrics.rollup_s'), disabled: !(q.rollup && q.rollup.fn) });
        const fnSel = select(ROLLUPS.map((r) => ({ value: r, label: r ? r : t('metrics.rollup_auto') })), (q.rollup && q.rollup.fn) || '', (v) => {
          q.rollup = v ? { fn: v, s: Number(secs.value) || 60 } : null;
          secs.disabled = !v;
          changed();
        }, { title: t('metrics.rollup') });
        secs.addEventListener('change', () => { if (q.rollup) { q.rollup.s = Math.max(15, Number(secs.value) || 60); changed(); } });
        row.appendChild(h('div', { class: 'row', style: { gap: '4px' } }, fnSel, secs));
      }
      row.appendChild(select(DISPLAYS.map((d) => ({ value: d, label: t('display.' + d) })), q.display || 'line', (v) => { q.display = v; changed(); }));
      row.appendChild(h('div', { class: 'row', style: { gap: '2px' } },
        h('button', { class: ['btn', 'ghost', 'sm', 'icon', q.hidden ? 'off' : null], type: 'button', title: q.hidden ? t('metrics.show_query') : t('metrics.hide_query'), onclick: () => { q.hidden = !q.hidden; drawEditor(); changed(); } }, icon(q.hidden ? 'mute' : 'eye', 13)),
        q.raw === null || q.raw === undefined ? fnMenu(q) : null,
        h('button', {
          class: 'btn ghost sm icon', type: 'button', title: q.raw !== null && q.raw !== undefined ? t('metrics.to_builder') : t('metrics.to_text'),
          onclick: () => {
            if (q.raw !== null && q.raw !== undefined) {
              const p = parseQueryFull(q.raw);
              if (p) Object.assign(q, p, { raw: null });
              else { toast(t('metrics.cannot_build'), 'info'); return; }
            } else q.raw = buildQuery(q);
            drawEditor(); changed();
          },
        }, icon('edit', 13)),
        h('button', { class: 'btn ghost sm icon', type: 'button', title: t('action.duplicate'), onclick: () => { if (queries.length < IDS.length) { queries.splice(i + 1, 0, JSON.parse(JSON.stringify(q))); drawEditor(); changed(); } } }, icon('copy', 13)),
        queries.length > 1 ? h('button', { class: 'btn ghost sm icon', type: 'button', title: t('action.remove'), onclick: () => { queries.splice(i, 1); drawEditor(); changed(); } }, icon('trash', 13)) : null));
      editor.appendChild(row);
      // Fonctions appliquées (mode constructeur)
      if ((q.raw === null || q.raw === undefined) && q.fns.length) {
        editor.appendChild(h('div', { class: 'row wrap fn-chips', style: { padding: '0 0 4px 32px', gap: '4px' } }, q.fns.map((fn, j) => {
          const args = h('input', { class: 'mono', value: fn.args || '', placeholder: '', size: Math.max(2, String(fn.args || '').length + 1) });
          args.addEventListener('change', () => { fn.args = args.value.trim(); changed(); });
          return h('span', { class: 'chip fn-chip', title: t('fx.d.' + fn.name) }, h('span', { class: 'mono', style: { fontSize: '11px' } }, fn.name + '('), args, h('span', { class: 'mono', style: { fontSize: '11px' } }, ')'),
            h('button', { type: 'button', title: t('action.remove'), onclick: () => { q.fns.splice(j, 1); drawEditor(); changed(); } }, icon('x', 10)));
        })));
      }
      const metaEl = h('div', { class: 'xsmall muted meta-line', style: { padding: '0 0 2px 32px' } });
      editor.appendChild(h('div', { class: 'mono xsmall muted q-preview', style: { padding: '0 0 2px 32px', wordBreak: 'break-all' } }, buildQuery(q)));
      editor.appendChild(metaEl);
      drawMeta(metaEl, metricOf(q));
      editor.appendChild(h('div', { style: { height: '6px' } }));
    });
  }

  function drawMeta(el, metric) {
    if (!metric) return;
    metricMeta(metric, signal).then((meta) => {
      clear(el);
      const parts = [];
      if (meta && meta.unit) parts.push(h('span', { class: 'badge muted' }, unitLabel(meta.unit) + (meta.per_unit ? ' / ' + meta.per_unit : '')));
      if (meta && meta.description) parts.push(h('span', { class: 'text-2' }, meta.description));
      if (meta && meta.integration) parts.push(h('span', { class: 'tag' }, meta.integration));
      if (!parts.length) parts.push(h('span', t('metrics.meta.none')));
      if (canEditMeta) parts.push(h('button', { class: 'btn ghost sm', type: 'button', onclick: () => metaDialog(metric, meta, () => drawMeta(el, metric)) }, icon('edit', 11), t('metrics.meta.edit')));
      el.appendChild(h('span', { class: 'row wrap', style: { gap: '6px' } }, parts));
    }).catch(() => {});
  }

  function drawFormulas() {
    clear(formulaEl);
    if (!formulas.length) return;
    const ids = queries.map((q) => q.id);
    formulaEl.appendChild(h('div', { class: 'xsmall muted', style: { padding: '6px 0 2px' } }, t('metrics.formulas')));
    formulas.forEach((f, i) => {
      f.id = 'f' + (i + 1);
      const inp = h('input', { class: 'input mono', value: f.formula, spellcheck: 'false', placeholder: 'a / b * 100' });
      const alias = h('input', { class: 'input', value: f.alias || '', placeholder: t('metrics.formula_alias_ph'), style: { width: '180px' } });
      const errEl = h('span', { class: 'xsmall', style: { color: 'var(--crit)' } });
      const check = () => {
        const e = checkFormula(f.formula, ids);
        errEl.textContent = !e ? '' : e.startsWith('unknown:') ? t('metrics.formula_err.unknown', { w: e.slice(8) }) : t('metrics.formula_err.' + e);
        inp.classList.toggle('invalid', !!e);
      };
      inp.addEventListener('input', () => { f.formula = inp.value; check(); changedSlow(); });
      inp.addEventListener('focus', () => { lastInput = { kind: 'formula', i }; });
      alias.addEventListener('input', () => { f.alias = alias.value; changedSlow(); });
      check();
      formulaEl.appendChild(h('div', { class: 'row', style: { gap: '6px', padding: '4px 0' } },
        h('span', { class: 'qid fid', style: { background: colorAt(queries.length + i) } }, f.id),
        h('span', { class: 'grow' }, inp), alias,
        h('button', { class: 'btn ghost sm icon', type: 'button', title: t('action.remove'), onclick: () => { formulas.splice(i, 1); drawFormulas(); changed(); } }, icon('trash', 13))));
      formulaEl.appendChild(h('div', { style: { padding: '0 0 4px 32px' } }, errEl));
    });
  }

  let ctrl = null;
  async function run() {
    save();
    drawPreviews();
    if (ctrl) ctrl.abort();
    ctrl = new AbortController();
    const sig = ctrl.signal;
    signal.addEventListener('abort', () => ctrl && ctrl.abort(), { once: true });
    const valid = queries.filter((q) => (q.raw && q.raw.trim()) || q.metric);
    const ids = valid.map((q) => q.id);
    const validFormulas = formulas.filter((f) => !checkFormula(f.formula, ids));
    clear(chartsEl);
    clear(tableEl);
    if (!valid.length) { chartsEl.appendChild(h('section', { class: 'panel' }, empty(t('metrics.empty_title'), t('metrics.empty_text')))); return; }
    chartsEl.appendChild(h('section', { class: 'panel' }, h('div', { class: 'panel-body' }, h('div', { class: 'skeleton', style: { height: '280px' } }))));
    try {
      const resp = await queryMetrics(valid.map((q) => ({ id: q.id, q: buildQuery(q) })), range, 400, sig,
        { formulas: validFormulas.map((f) => ({ id: f.id, formula: f.formula.trim(), alias: f.alias || undefined })) });
      const names = {};
      const display = {};
      // Nom affiché : la métrique de la requête, ou l'alias de la formule.
      valid.forEach((q) => { names[q.id] = (q.id + ' ' + metricOf(q)).trim(); display[q.id] = q.display; });
      validFormulas.forEach((f) => { names[f.id] = f.alias || f.id + ' = ' + f.formula; display[f.id] = 'line'; });
      const hiddenIds = new Set(valid.filter((q) => q.hidden).map((q) => q.id));
      const shown = Object.assign({}, resp, { series: ((resp && resp.series) || []).filter((s2) => !hiddenIds.has(s2.query)) });
      const { xs: xs0, lines: lines0 } = linesFromMetrics(shown, { names, display, forceMetricName: valid.length + validFormulas.length > 1 });
      let xs = xs0;
      let lines = lines0;
      // Unité par défaut : métadonnées de la métrique.
      for (const l of lines) {
        if (!l.unit && l.metric && metaCache.get(l.metric) && metaCache.get(l.metric).unit) l.unit = metaCache.get(l.metric).unit;
      }
      lines.forEach((l, i) => { l.color = colorAt(i); if (valid.length + validFormulas.length > 1 && !Object.keys(l.group).length) l.label = names[l.query]; });
      // Prévisions (points futurs) en pointillé ; bornes d'anomalie en bande.
      const bands = [];
      ({ xs, lines } = withBoundsAndForecast(shown, xs, lines, bands));
      clear(chartsEl);
      if (!lines.length) { chartsEl.appendChild(h('section', { class: 'panel' }, empty(t('metrics.no_series_title'), t('metrics.no_series_text')))); return; }
      const xMax = Math.max(range.to, xs.length ? xs[xs.length - 1] : range.to);
      const byUnit = new Map();
      lines.forEach((l, i) => { const u = l.unit || ''; if (!byUnit.has(u)) byUnit.set(u, []); byUnit.get(u).push(Object.assign(l, { _i: i })); });
      for (const [u, ls] of byUnit) {
        const idx = new Map(ls.map((l, j) => [l._i, j]));
        const ub = bands.filter((b) => idx.has(b.hi) && idx.has(b.lo)).map((b) => ({ hi: idx.get(b.hi), lo: idx.get(b.lo) }));
        chartsEl.appendChild(panel(byUnit.size > 1 ? (u ? t('metrics.chart_unit', { unit: unitLabel(u) }) : t('metrics.chart_no_unit')) : t('metrics.chart'),
          timeChart({ xs, lines: ls, unit: u || null, height: byUnit.size > 1 ? 220 : 300, xRange: [range.from, xMax], onRange: ctx.onRange, legendMax: 40, bands: ub }),
          { sub: resp && resp.interval_s ? t('metrics.interval', { s: resp.interval_s }) + (u ? ' · ' + unitLabel(u) : '') : unitLabel(u) }));
      }
      const real = lines.filter((l) => !l.aux);
      tableEl.appendChild(panel(t('metrics.series_table'), dataTable({
        columns: [
          { key: 'label', label: t('col.series'), render: (r) => h('span', { class: 'row', style: { gap: '6px' } }, h('span', { class: 'dot', style: { background: r.color } }), h('span', { class: 'mono small' }, r.label)) },
          { key: 'query', label: t('col.query'), render: (r) => r.query },
          { key: 'avg', label: t('col.avg'), align: 'r', render: (r) => fmtUnit(r.avg, r.unit) },
          { key: 'min', label: t('col.min'), align: 'r', render: (r) => fmtUnit(r.min, r.unit) },
          { key: 'max', label: t('col.max'), align: 'r', render: (r) => fmtUnit(r.max, r.unit) },
          { key: 'last', label: t('col.last'), align: 'r', render: (r) => fmtUnit(r.last, r.unit) },
        ],
        rows: real.map((l) => Object.assign({ label: l.label, query: l.query, color: l.color, unit: l.unit }, summarize(l.values))),
        sortKey: 'avg', sortDir: 'desc', maxRows: 200,
      }), { flush: true }));
    } catch (e) {
      if (isAbort(e)) return;
      clear(chartsEl);
      chartsEl.appendChild(h('section', { class: 'panel' }, errorState(e, run)));
    }
  }

  function drawPreviews() {
    const previews = editor.querySelectorAll('.q-preview');
    queries.forEach((q, i) => { if (previews[i]) previews[i].textContent = buildQuery(q); });
  }

  const changedSlow = debounce(run, 700);
  const changed = debounce(run, 150);
  ctx.onCleanup(() => { changed.cancel(); changedSlow.cancel(); if (ctrl) ctrl.abort(); });

  drawEditor();
  drawFormulas();
  await run();
}

// Séries « future » (prévision) en pointillé, bornes d'anomalie (series.bounds [[ts, lo, hi]]) en bande.
export function withBoundsAndForecast(resp, xs, lines, bands) {
  const series = (resp && resp.series) || [];
  const extra = [];
  const stamps = new Set(xs);
  series.forEach((sr, i) => {
    const line = lines[i];
    if (!line) return;
    if (sr.future === true) { line.dash = [6, 4]; line.label = line.label + ' ' + t('metrics.forecast_suffix'); }
    const fut = (sr.points || []).filter((p) => p && (p[2] === true || (p[2] && p[2].future)));
    if (fut.length) { line.dash = undefined; fut.forEach((p) => stamps.add(toMs(p[0]))); extra.push({ kind: 'future', of: i, pts: fut }); }
    if (Array.isArray(sr.bounds) && sr.bounds.length) { sr.bounds.forEach((b) => stamps.add(toMs(b[0]))); extra.push({ kind: 'bounds', of: i, pts: sr.bounds }); }
  });
  if (!extra.length) return { xs, lines };
  const all = Array.from(stamps).filter((x) => x !== null).sort((a, b) => a - b);
  const index = new Map(all.map((x, k) => [x, k]));
  const remap = (vals) => { const out = new Array(all.length).fill(null); xs.forEach((x, k) => { const j = index.get(x); if (j !== undefined) out[j] = vals[k]; }); return out; };
  const outLines = lines.map((l) => Object.assign({}, l, { values: remap(l.values) }));
  for (const ex of extra) {
    const base = outLines[ex.of];
    if (ex.kind === 'future') {
      const vals = new Array(all.length).fill(null);
      ex.pts.forEach((p) => { const j = index.get(toMs(p[0])); if (j !== undefined) vals[j] = p[1]; });
      // Le futur part du dernier point réel pour une courbe continue.
      for (let k = base.values.length - 1; k >= 0; k--) if (base.values[k] !== null) { vals[k] = base.values[k]; break; }
      outLines.push({ label: base.label + ' ' + t('metrics.forecast_suffix'), values: vals, unit: base.unit, color: base.color, dash: [6, 4], query: base.query, group: base.group, aux: true });
    } else {
      const lo = new Array(all.length).fill(null);
      const hi = new Array(all.length).fill(null);
      ex.pts.forEach((b) => { const j = index.get(toMs(b[0])); if (j !== undefined) { lo[j] = b[1]; hi[j] = b[2]; } });
      const hiIdx = outLines.length;
      outLines.push({ label: base.label + ' ' + t('metrics.bound_hi'), values: hi, unit: base.unit, color: base.color, dash: [2, 3], width: 1, query: base.query, group: base.group, aux: true });
      outLines.push({ label: base.label + ' ' + t('metrics.bound_lo'), values: lo, unit: base.unit, color: base.color, dash: [2, 3], width: 1, query: base.query, group: base.group, aux: true });
      bands.push({ hi: hiIdx, lo: hiIdx + 1 });
    }
  }
  return { xs: all, lines: outLines };
}
