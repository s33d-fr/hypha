// SLO (§ 13) : liste, page d'un SLO (SLI cumulé, budget, vitesses), formulaire des types moniteur et métrique.
import { h, clear, icon, debounce, cssVar } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, post, put, del, asList, queryMetrics, isAbort } from '../api.js';
import { fmtPct, fmtNum, fmtRel, fmtDateTime, isNum, toMs } from '../format.js';
import { section, empty, panel, searchInput, pills, select, field, toast, confirmDialog, errorMessage, errorState, loading, kv, tagList } from '../components/ui.js';
import { dataTable, barCell } from '../components/table.js';
import { timeChart, alignPoints } from '../components/chart.js';

export const TIMEFRAMES = { '7d': 7 * 86400e3, '30d': 30 * 86400e3, '90d': 90 * 86400e3 };

// Résumé d'un SLO : à plat dans l'objet, ou dans « status » selon le serveur.
export function sloStatus(s) {
  return s && s.status && typeof s.status === 'object' ? s.status : (s || {});
}

// SLI lisible : 99,95 % et non 100 %.
export function fmtSli(v) {
  if (!isNum(v)) return '-';
  return fmtPct(v, v >= 99 ? 3 : v >= 90 ? 2 : 1);
}

export function fmtBurn(v) {
  return isNum(v) ? fmtNum(v, v >= 100 ? 0 : v >= 10 ? 1 : 2) + ' ×' : '-';
}

export function stateColor(st) {
  return st === 'breached' ? 'var(--crit)' : st === 'warning' ? 'var(--warn)' : st === 'ok' ? 'var(--ok)' : 'var(--nodata)';
}

export function sloBadge(st) {
  const cls = st === 'breached' ? 'alert' : st === 'warning' ? 'warn' : st === 'ok' ? 'ok' : 'nodata';
  return h('span', { class: ['badge', cls] }, h('span', { class: ['dot', cls] }), t('slo.state.' + (['ok', 'warning', 'breached'].includes(st) ? st : 'unknown')));
}

export function budgetBar(v, st) {
  return barCell(isNum(v) ? Math.max(0, Math.min(100, v)) : null, isNum(v) ? fmtPct(v, 1) : '-', stateColor(st));
}

function burnColor(v) { return !isNum(v) ? null : v >= 10 ? 'var(--crit)' : v > 1 ? 'var(--warn)' : null; }

export async function render(root, ctx) {
  if (ctx.route.view === 'form') return renderForm(root, ctx);
  if (ctx.route.view === 'detail') return renderDetail(root, ctx);
  return renderList(root, ctx);
}

async function renderList(root, ctx) {
  const { signal, href, params } = ctx;
  ctx.setCrumbs([{ label: t('nav.slos') }]);
  let q = params.get('q') || '';
  let state = params.get('state') || 'all';
  const body = h('div');
  const countEl = h('span', { class: 'sub' });
  const search = searchInput({ placeholder: t('slo.search_ph'), value: q, onInput: (v) => { q = v; ctx.setParams({ q: v || null }, { silent: true }); draw(); } });
  search.style.flex = '1 1 280px';
  const pillSlot = h('span');
  root.appendChild(h('div', { class: 'querybar' }, search, pillSlot, h('a', { class: 'btn primary', href: href('/slos/new') }, icon('plus', 14), t('slo.new'))));
  root.appendChild(panel(t('nav.slos'), body, { flush: true, actions: countEl }));
  let rows = [];
  function drawPills() {
    clear(pillSlot);
    const n = (st) => rows.filter((s) => sloStatus(s).state === st).length;
    pillSlot.appendChild(pills([{ key: 'all', label: t('slo.all') + ' ' + rows.length }].concat(['breached', 'warning', 'ok'].map((k) => ({ key: k, label: t('slo.state.' + k) + ' ' + n(k) }))), state,
      (k) => { state = k; ctx.setParams({ state: k === 'all' ? null : k }, { silent: true }); drawPills(); draw(); }));
  }
  function draw() {
    const words = q.toLowerCase().split(/\s+/).filter(Boolean);
    const list = rows.filter((s) => (state === 'all' || sloStatus(s).state === state)
      && words.every((w) => [s.name, s.description, s.type, JSON.stringify(s.tags || {})].join(' ').toLowerCase().includes(w)));
    countEl.textContent = tn('slo.count', list.length);
    clear(body);
    if (!rows.length) {
      body.appendChild(empty(t('slo.none_title'), t('slo.none_text'), h('a', { class: 'btn primary', href: href('/slos/new') }, icon('plus', 14), t('slo.new'))));
      return;
    }
    const rank = { breached: 0, warning: 1, ok: 2 };
    body.appendChild(dataTable({
      columns: [
        { key: 'state', label: t('col.state'), value: (s) => rank[sloStatus(s).state] ?? 3, render: (s) => sloBadge(sloStatus(s).state) },
        { key: 'name', label: t('col.name'), render: (s) => h('a', { href: href('/slos/' + s.id) }, s.name || '#' + s.id) },
        { key: 'type', label: t('col.type'), render: (s) => t('slo.type.' + (s.type === 'monitor' ? 'monitor' : 'metric')) },
        { key: 'target', label: t('slo.target'), align: 'r', render: (s) => fmtSli(s.target) },
        { key: 'timeframe', label: t('slo.timeframe'), render: (s) => t('slo.tf.' + (TIMEFRAMES[s.timeframe] ? s.timeframe : '30d')) },
        { key: 'sli', label: 'SLI', align: 'r', value: (s) => sloStatus(s).sli, render: (s) => { const st = sloStatus(s); return h('b', { class: 'num', style: { color: stateColor(st.state) } }, fmtSli(st.sli)); } },
        { key: 'budget', label: t('slo.budget'), align: 'r', value: (s) => sloStatus(s).error_budget_remaining, render: (s) => { const st = sloStatus(s); return budgetBar(st.error_budget_remaining, st.state); } },
        { key: 'burn1', label: t('slo.burn_1h'), align: 'r', value: (s) => sloStatus(s).burn_rate_1h, render: (s) => { const v = sloStatus(s).burn_rate_1h; return h('span', { style: { color: burnColor(v) } }, fmtBurn(v)); } },
        { key: 'burn6', label: t('slo.burn_6h'), align: 'r', value: (s) => sloStatus(s).burn_rate_6h, render: (s) => { const v = sloStatus(s).burn_rate_6h; return h('span', { style: { color: burnColor(v) } }, fmtBurn(v)); } },
        { key: 'tags', label: t('col.tags'), sort: false, render: (s) => tagList(s.tags) },
      ],
      rows: list, sortKey: 'state', sortDir: 'asc',
      onRowClick: (s) => ctx.navigate('/slos/' + s.id),
      emptyText: t('slo.no_match'),
    }));
  }
  await section(body, async () => asList(await get('/slos', null, { signal }), 'slos'), (list) => { rows = list; drawPills(); draw(); return null; });
}

// Graphique du SLI cumulé, avec la cible (rouge) et le seuil d'avertissement (orange).
export function sliChart(history, slo, opts = {}) {
  const pts = asList(history).filter((p) => Array.isArray(p)).map((p) => [toMs(p[0]), p[1]]);
  const { xs, ys } = alignPoints([pts]);
  const vals = ys[0] ? ys[0].filter(isNum) : [];
  const marks = [slo.target, slo.warning].filter(isNum);
  const lo = Math.min(...vals.concat(marks).concat([100]));
  const pad = Math.max(0.02, (100 - lo) * 0.25);
  const now = Date.now();
  const span = TIMEFRAMES[slo.timeframe] || TIMEFRAMES['30d'];
  const thresholds = [];
  if (isNum(slo.warning)) thresholds.push({ value: slo.warning, color: 'warn' });
  if (isNum(slo.target)) thresholds.push({ value: slo.target, color: 'alert' });
  return timeChart({
    xs, unit: 'percent', height: opts.height || 240, legend: false,
    lines: [{ label: 'SLI', values: ys[0] || [], color: cssVar('--s1'), display: 'area' }],
    xRange: [xs.length ? Math.min(xs[0], now - span) : now - span, now],
    yMin: Math.max(0, lo - pad), yMax: 100, thresholds, valueFmt: fmtSli, axisFmt: fmtSli,
    emptyText: t('slo.no_history'),
  });
}

async function renderDetail(root, ctx) {
  const { args, signal, href } = ctx;
  ctx.setCrumbs([{ label: t('nav.slos'), href: href('/slos') }, { label: '#' + args.id }]);
  root.appendChild(loading());
  let slo; let st;
  try {
    const [r, s2] = await Promise.all([get('/slos/' + encodeURIComponent(args.id), null, { signal }), get('/slos/' + encodeURIComponent(args.id) + '/status', null, { signal })]);
    slo = r && r.slo ? r.slo : r;
    st = s2 || {};
  } catch (e) {
    if (isAbort(e)) return;
    clear(root); root.appendChild(errorState(e, () => ctx.refresh())); return;
  }
  clear(root);
  slo = slo || {};
  const target = isNum(st.target) ? st.target : slo.target;
  ctx.setCrumbs([{ label: t('nav.slos'), href: href('/slos') }, { label: slo.name || '#' + slo.id }]);
  root.appendChild(h('div', { class: 'page-head' },
    sloBadge(st.state), h('h1', slo.name || '#' + slo.id),
    h('span', { class: 'badge muted' }, t('slo.type.' + (slo.type === 'monitor' ? 'monitor' : 'metric'))),
    h('span', { class: 'desc' }, t('slo.window_of', { tf: t('slo.tf.' + (TIMEFRAMES[slo.timeframe] ? slo.timeframe : '30d')) })),
    h('span', { class: 'spacer' }),
    h('a', { class: 'btn sm', href: href('/monitors/new', { type: 'slo', slo_id: slo.id }) }, icon('monitor', 13), t('slo.create_monitor')),
    h('a', { class: 'btn sm', href: href('/slos/' + slo.id + '/edit') }, icon('edit', 13), t('action.edit')),
    h('button', { class: 'btn sm danger', type: 'button', onclick: async () => {
      if (!(await confirmDialog({ title: t('slo.delete_title'), text: t('slo.delete_text', { name: slo.name || '' }), confirmLabel: t('action.delete'), danger: true }))) return;
      try { await del('/slos/' + slo.id); toast(t('slo.deleted'), 'ok'); ctx.navigate('/slos'); } catch (e) { toast(errorMessage(e), 'error'); }
    } }, icon('trash', 13), t('action.delete'))));
  if (slo.description) root.appendChild(h('p', { class: 'text-2 mb-3' }, slo.description));

  const tile = (label, value, sub, color) => h('div', { class: 'tile' }, h('div', { class: 't-label' }, label), h('div', { class: 't-value num', style: { fontSize: 'var(--fs-xl)', color } }, value), sub ? h('div', { class: 't-sub' }, sub) : null);
  root.appendChild(h('div', { class: 'tiles mb-3' },
    tile('SLI', fmtSli(st.sli), t('slo.on_window'), stateColor(st.state)),
    tile(t('slo.target'), fmtSli(target), isNum(slo.warning) ? t('slo.warning_at', { v: fmtSli(slo.warning) }) : null),
    tile(t('slo.budget'), isNum(st.error_budget_remaining) ? fmtPct(st.error_budget_remaining, 1) : '-', t('slo.budget_hint'), stateColor(st.state)),
    tile(t('slo.burn_1h'), fmtBurn(st.burn_rate_1h), t('slo.burn_hint'), burnColor(st.burn_rate_1h)),
    tile(t('slo.burn_6h'), fmtBurn(st.burn_rate_6h), t('slo.burn_hint'), burnColor(st.burn_rate_6h))));

  root.appendChild(h('div', { class: 'mb-3' }, panel(t('slo.chart'), sliChart(st.history, Object.assign({}, slo, { target })), { sub: t('slo.chart_sub') })));

  let monitorLink = null;
  if (slo.type === 'monitor' && slo.monitor_id !== null && slo.monitor_id !== undefined) {
    monitorLink = h('a', { href: href('/monitors/' + slo.monitor_id) }, '#' + slo.monitor_id);
    get('/monitors/' + slo.monitor_id, null, { signal }).then((m) => { const mm = m && m.monitor ? m.monitor : m; if (mm && mm.name) monitorLink.textContent = mm.name; }).catch(() => {});
  }
  root.appendChild(panel(t('slo.definition'), kv([
    [t('col.type'), t('slo.type.' + (slo.type === 'monitor' ? 'monitor' : 'metric'))],
    slo.type === 'monitor' ? [t('slo.f.monitor'), monitorLink] : [t('slo.f.good'), h('span', { class: 'mono small' }, slo.good_query || '-')],
    slo.type === 'monitor' ? [t('slo.calc'), t('slo.calc_monitor')] : [t('slo.f.total'), h('span', { class: 'mono small' }, slo.total_query || '-')],
    [t('slo.target'), fmtSli(target)],
    [t('slo.f.warning'), isNum(slo.warning) ? fmtSli(slo.warning) : null],
    [t('slo.timeframe'), t('slo.tf.' + (TIMEFRAMES[slo.timeframe] ? slo.timeframe : '30d'))],
    [t('col.tags'), tagList(slo.tags)],
    [t('col.updated'), slo.updated_at ? h('span', { title: fmtDateTime(slo.updated_at) }, fmtRel(slo.updated_at)) : null],
  ])));
}

function parseTags(str) {
  const out = {};
  for (const part of String(str || '').split(',')) {
    const p = part.trim();
    const i = p.indexOf(':');
    if (i > 0) out[p.slice(0, i).trim()] = p.slice(i + 1).trim();
  }
  return out;
}

async function renderForm(root, ctx) {
  const { args, signal, href, params } = ctx;
  const editing = !!args.id;
  ctx.setCrumbs([{ label: t('nav.slos'), href: href('/slos') }, { label: editing ? t('slo.edit') : t('slo.new') }]);
  let slo = { name: '', description: '', type: params.get('type') === 'monitor' ? 'monitor' : 'metric', monitor_id: params.get('monitor_id') ? Number(params.get('monitor_id')) : null,
    good_query: 'sum:trace.hits{service:api,!status:error}', total_query: 'sum:trace.hits{service:api}', target: 99.9, warning: null, timeframe: '30d', tags: {} };
  if (editing) {
    root.appendChild(loading());
    try {
      const r = await get('/slos/' + encodeURIComponent(args.id), null, { signal });
      slo = Object.assign(slo, r && r.slo ? r.slo : r);
    } catch (e) {
      if (isAbort(e)) return;
      clear(root); root.appendChild(errorState(e, () => ctx.refresh())); return;
    }
    clear(root);
  }
  const monitorsP = get('/monitors', null, { signal }).then((r) => asList(r, 'monitors')).catch(() => []);
  const fields = h('div', { class: 'form-grid' });
  const defEl = h('div', { class: 'col gap-3' });
  const previewEl = h('div');
  const err = h('div', { class: 'callout crit mt-3', hidden: true });
  const saveBtn = h('button', { class: 'btn primary', type: 'button' }, editing ? t('action.save') : t('slo.create'));
  root.appendChild(h('div', { class: 'page-head' }, h('h1', editing ? t('slo.edit') : t('slo.new'))));
  root.appendChild(h('section', { class: 'panel', 'data-no-autorefresh': '1' }, h('div', { class: 'panel-body' },
    h('div', { class: 'form-section', style: { borderTop: '0', marginTop: '0', paddingTop: '0' } }, h('h3', h('span', { class: 'step-num' }, '1'), t('slo.step.source')), defEl, previewEl),
    h('div', { class: 'form-section' }, h('h3', h('span', { class: 'step-num' }, '2'), t('slo.step.target')), fields),
    err,
    h('div', { class: 'row mt-4' }, h('span', { class: 'spacer' }), h('a', { class: 'btn', href: editing ? href('/slos/' + args.id) : href('/slos') }, t('action.cancel')), saveBtn))));

  const refreshPreview = debounce(() => drawPreview(), 600);

  async function drawDef() {
    clear(defEl);
    defEl.appendChild(field(t('col.type'), pills([{ key: 'metric', label: t('slo.type.metric') }, { key: 'monitor', label: t('slo.type.monitor') }], slo.type,
      (k) => { if (editing) return; slo.type = k; drawDef(); drawPreview(); }), editing ? t('slo.type_locked') : t('slo.type_hint.' + slo.type)));
    if (slo.type === 'monitor') {
      const slot = h('div', h('span', { class: 'spinner' }));
      defEl.appendChild(field(t('slo.f.monitor'), slot, t('slo.f.monitor_hint')));
      const list = await monitorsP;
      clear(slot);
      if (!list.length) { slot.appendChild(h('div', { class: 'small muted' }, t('slo.f.no_monitors'), ' ', h('a', { href: href('/monitors/new') }, t('monitors.new')))); return; }
      if (slo.monitor_id === null || slo.monitor_id === undefined) slo.monitor_id = list[0].id;
      slot.appendChild(select(list.map((m) => ({ value: m.id, label: (m.name || '#' + m.id) + ' · ' + t('monitors.type.' + m.type) })), slo.monitor_id, (v) => { slo.monitor_id = /^\d+$/.test(v) ? Number(v) : v; }));
    } else {
      const good = h('input', { class: 'input mono', value: slo.good_query || '', spellcheck: 'false' });
      const total = h('input', { class: 'input mono', value: slo.total_query || '', spellcheck: 'false' });
      good.addEventListener('input', () => { slo.good_query = good.value; refreshPreview(); });
      total.addEventListener('input', () => { slo.total_query = total.value; refreshPreview(); });
      defEl.appendChild(field(t('slo.f.good'), good, t('slo.f.good_hint')));
      defEl.appendChild(field(t('slo.f.total'), total, t('slo.f.total_hint')));
    }
  }

  let pctrl = null;
  async function drawPreview() {
    if (pctrl) pctrl.abort();
    clear(previewEl);
    if (slo.type !== 'metric') return;
    const g = String(slo.good_query || '').trim();
    const tq = String(slo.total_query || '').trim();
    if (!g || !tq) return;
    pctrl = new AbortController();
    const box = h('div', { class: 'panel-body', style: { padding: '0' } }, h('div', { class: 'skeleton', style: { height: '160px' } }));
    const head = h('div', { class: 'chart-title' }, t('slo.preview'), h('span', { class: 'unit' }, t('slo.preview_sub', { tf: t('slo.tf.' + slo.timeframe) })));
    previewEl.appendChild(h('div', { class: 'mt-3' }, head, box));
    const to = Date.now();
    const range = { from: to - (TIMEFRAMES[slo.timeframe] || TIMEFRAMES['30d']), to };
    try {
      const resp = await queryMetrics([{ id: 'g', q: g }, { id: 't', q: tq }], range, 120, pctrl.signal);
      const series = (resp && resp.series) || [];
      // Somme de toutes les séries de chaque requête (un regroupement « by » éventuel est additionné).
      const sumBy = (id) => {
        const m = new Map();
        for (const s2 of series.filter((x) => x.query === id)) for (const p of s2.points || []) if (p && isNum(p[1])) m.set(p[0], (m.get(p[0]) || 0) + p[1]);
        return m;
      };
      const gm = sumBy('g');
      const tm = sumBy('t');
      let sg = 0; let st2 = 0;
      for (const v of gm.values()) sg += v;
      for (const v of tm.values()) st2 += v;
      const ratio = st2 > 0 ? sg / st2 * 100 : null;
      const pts = Array.from(tm.entries()).sort((a, b) => a[0] - b[0]).map(([ts, tv]) => [ts, tv > 0 ? Math.min(100, (gm.get(ts) || 0) / tv * 100) : null]);
      clear(box);
      const bad = isNum(ratio) && isNum(slo.target) && ratio < slo.target;
      box.appendChild(h('div', { class: 'row mb-2' },
        h('span', { class: 'small text-2' }, t('slo.preview_ratio')),
        h('b', { class: 'num', style: { fontSize: 'var(--fs-lg)', color: isNum(ratio) ? (bad ? 'var(--crit)' : 'var(--ok)') : null } }, isNum(ratio) ? fmtSli(ratio) : '-'),
        h('span', { class: 'xsmall muted' }, isNum(ratio) ? t('slo.preview_sums', { g: fmtNum(sg, 0), t: fmtNum(st2, 0) }) : t('slo.preview_no_total'))));
      box.appendChild(sliChart(pts, slo, { height: 170 }));
    } catch (e) {
      if (isAbort(e)) return;
      clear(box);
      box.appendChild(errorState(e));
    }
  }

  function drawFields() {
    clear(fields);
    const name = h('input', { class: 'input', value: slo.name || '', maxlength: 200, placeholder: t('slo.f.name_ph') });
    name.addEventListener('input', () => { slo.name = name.value; });
    const desc = h('textarea', { class: 'input', rows: 2 });
    desc.value = slo.description || '';
    desc.addEventListener('input', () => { slo.description = desc.value; });
    const target = h('input', { class: 'input', type: 'number', min: 0, max: 100, step: 'any', value: isNum(slo.target) ? slo.target : '' });
    target.addEventListener('input', () => { slo.target = target.value === '' ? null : Number(target.value); refreshPreview(); });
    const warn = h('input', { class: 'input', type: 'number', min: 0, max: 100, step: 'any', value: isNum(slo.warning) ? slo.warning : '' });
    warn.addEventListener('input', () => { slo.warning = warn.value === '' ? null : Number(warn.value); });
    const tags = h('input', { class: 'input mono', value: Object.entries(slo.tags || {}).map(([k, v]) => k + ':' + v).join(', '), placeholder: 'team:ops' });
    tags.addEventListener('change', () => { slo.tags = parseTags(tags.value); });
    fields.appendChild(h('div', { class: 'full' }, field(t('slo.f.name'), name)));
    fields.appendChild(h('div', { class: 'full' }, field(t('slo.f.description'), desc)));
    fields.appendChild(field(t('slo.f.target'), target, t('slo.f.target_hint')));
    fields.appendChild(field(t('slo.f.warning'), warn, t('slo.f.warning_hint')));
    fields.appendChild(field(t('slo.timeframe'), select(Object.keys(TIMEFRAMES).map((k) => ({ value: k, label: t('slo.tf.' + k) })), slo.timeframe, (v) => { slo.timeframe = v; drawPreview(); })));
    fields.appendChild(field(t('col.tags'), tags));
  }

  drawDef();
  drawFields();
  drawPreview();
  ctx.onCleanup(() => { refreshPreview.cancel(); if (pctrl) pctrl.abort(); });

  saveBtn.addEventListener('click', async () => {
    err.hidden = true;
    const errs = [];
    if (!String(slo.name || '').trim()) errs.push(t('form.required', { field: t('slo.f.name') }));
    if (!(isNum(slo.target) && slo.target > 0 && slo.target < 100)) errs.push(t('slo.err.target'));
    if (isNum(slo.warning) && !(slo.warning > slo.target && slo.warning < 100)) errs.push(t('slo.err.warning'));
    if (slo.type === 'monitor' && (slo.monitor_id === null || slo.monitor_id === undefined)) errs.push(t('form.required', { field: t('slo.f.monitor') }));
    if (slo.type === 'metric' && (!String(slo.good_query || '').trim() || !String(slo.total_query || '').trim())) errs.push(t('slo.err.queries'));
    if (errs.length) { clear(err); err.appendChild(icon('warn', 16)); err.appendChild(h('div', errs.map((e) => h('div', e)))); err.hidden = false; return; }
    const body = {
      name: slo.name.trim(), description: slo.description || '', type: slo.type,
      monitor_id: slo.type === 'monitor' ? slo.monitor_id : null,
      good_query: slo.type === 'metric' ? slo.good_query.trim() : null,
      total_query: slo.type === 'metric' ? slo.total_query.trim() : null,
      target: slo.target, warning: isNum(slo.warning) ? slo.warning : null, timeframe: slo.timeframe, tags: slo.tags || {},
    };
    saveBtn.disabled = true;
    try {
      const r = editing ? await put('/slos/' + args.id, body) : await post('/slos', body);
      toast(editing ? t('slo.saved') : t('slo.created'), 'ok');
      const id = editing ? args.id : r && (r.id || (r.slo && r.slo.id));
      ctx.navigate(id ? '/slos/' + id : '/slos');
    } catch (e) {
      saveBtn.disabled = false;
      clear(err); err.appendChild(icon('warn', 16)); err.appendChild(h('div', errorMessage(e))); err.hidden = false;
    }
  });
}
