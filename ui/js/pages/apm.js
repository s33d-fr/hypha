// APM : services (débit, erreurs, latences), page d'un service, liste de traces filtrable.
import { h, clear, icon, cssVar } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, asList } from '../api.js';
import { fmtDuration, fmtPct, fmtNum, fmtCompact, fmtDateTime, isNum, toMs } from '../format.js';
import { section, empty, panel, searchInput, select, pills, field } from '../components/ui.js';
import { dataTable, barCell } from '../components/table.js';
import { timeChart, alignPoints } from '../components/chart.js';
import { viewsMenu } from '../components/views.js';

// Taux d'erreur en pourcentage : calculé depuis errors/hits quand c'est possible.
export function errorPct(r) {
  if (isNum(r.hits) && r.hits > 0 && isNum(r.errors)) return (r.errors / r.hits) * 100;
  if (isNum(r.error_rate)) return r.error_rate <= 1 ? r.error_rate * 100 : r.error_rate;
  return null;
}

function errColor(p) {
  if (!isNum(p)) return null;
  return p >= 5 ? 'var(--crit)' : p >= 1 ? 'var(--warn)' : 'var(--ok)';
}

export async function render(root, ctx) {
  const view = ctx.route.view;
  if (view === 'service') return renderService(root, ctx);
  if (view === 'traces') return renderTraces(root, ctx);
  return renderServices(root, ctx);
}

async function renderServices(root, ctx) {
  const { range, signal, href, params } = ctx;
  ctx.setCrumbs([{ label: t('nav.apm'), href: href('/apm') }, { label: t('nav.apm.services') }]);
  let q = params.get('q') || '';
  const body = h('div');
  const countEl = h('span', { class: 'sub' });
  const search = searchInput({ placeholder: t('apm.search_services'), value: q, onInput: (v) => { q = v; ctx.setParams({ q: v || null }, { silent: true }); draw(); } });
  search.style.flex = '1 1 300px';
  root.appendChild(h('div', { class: 'querybar' }, search, h('a', { class: 'btn', href: href('/apm/traces') }, icon('trace', 14), t('nav.apm.traces'))));
  root.appendChild(panel(t('apm.services'), body, { flush: true, actions: countEl }));
  let rows = [];
  function draw() {
    const list = rows.filter((r) => !q || String(r.service || '').toLowerCase().includes(q.toLowerCase()));
    countEl.textContent = tn('apm.service_count', list.length);
    clear(body);
    const maxP95 = Math.max(1, ...list.map((r) => (isNum(r.p95_ms) ? r.p95_ms : 0)));
    body.appendChild(dataTable({
      columns: [
        { key: 'service', label: t('col.service'), render: (r) => h('a', { href: href('/apm/service/' + encodeURIComponent(r.service)) }, r.service) },
        { key: 'rps', label: t('col.rps'), align: 'r', render: (r) => (isNum(r.rps) ? fmtNum(r.rps) + ' ' + t('unit.req_s') : '-') },
        { key: 'hits', label: t('col.hits'), align: 'r', render: (r) => fmtCompact(r.hits) },
        { key: 'errors', label: t('col.errors'), align: 'r', render: (r) => fmtCompact(r.errors) },
        { key: 'error_rate', label: t('col.error_rate'), align: 'r', value: errorPct, render: (r) => { const p = errorPct(r); return h('span', { style: { color: errColor(p) } }, fmtPct(p, 2)); } },
        { key: 'p50_ms', label: 'p50', align: 'r', render: (r) => fmtDuration(r.p50_ms) },
        { key: 'p95_ms', label: 'p95', align: 'r', render: (r) => barCell((r.p95_ms || 0) / maxP95 * 100, fmtDuration(r.p95_ms)) },
        { key: 'p99_ms', label: 'p99', align: 'r', render: (r) => fmtDuration(r.p99_ms) },
      ],
      rows: list, sortKey: 'hits', sortDir: 'desc',
      onRowClick: (r) => ctx.navigate('/apm/service/' + encodeURIComponent(r.service)),
      emptyText: rows.length ? t('apm.no_match') : t('apm.none'),
    }));
    if (!rows.length) body.appendChild(h('div', { class: 'panel-body' }, h('div', { class: 'callout' }, icon('info', 16), h('div', { class: 'small' }, t('apm.howto'), h('div', { class: 'mono mt-2' }, 'OTEL_EXPORTER_OTLP_ENDPOINT=' + location.origin + '  OTEL_EXPORTER_OTLP_HEADERS=X-Hypha-Api-Key=hph_…')))));
  }
  await section(body, async () => asList(await get('/apm/services', { from: range.from, to: range.to }, { signal }), 'services'), (list) => { rows = list; draw(); return null; });
}

function seriesChart(label, pts, unit, color, display, range, onRange, sync, events) {
  const { xs, ys } = alignPoints([pts || []]);
  return timeChart({ xs, lines: [{ label, values: ys[0], color, unit }], unit, height: 150, display, xRange: [range.from, range.to], onRange, sync, legend: false, events });
}

// Déploiements du service (événements source:deploy, § 30) superposés aux courbes.
function deployEvents(service, range, signal) {
  return get('/events', { q: 'source:deploy', from: range.from, to: range.to }, { signal })
    .then((r) => asList(r, 'events').filter((ev) => !ev.tags || !ev.tags.service || ev.tags.service === service)
      .map((ev) => ({ ts: toMs(ev.ts), title: ev.title || (ev.tags && ev.tags.version ? t('svc.deploy_of', { v: ev.tags.version }) : t('svc.deploy')) })))
    .catch(() => []);
}

async function renderService(root, ctx) {
  const { range, signal, href, args } = ctx;
  const service = args.name;
  ctx.setCrumbs([{ label: t('nav.apm'), href: href('/apm') }, { label: service }]);
  root.appendChild(h('div', { class: 'page-head' }, h('h1', service), h('span', { class: 'spacer' }),
    h('a', { class: 'btn sm', href: href('/service/' + encodeURIComponent(service)) }, icon('graph', 13), t('svc.open_catalog')),
    h('a', { class: 'btn sm', href: href('/logs', { q: 'service:' + (/\s/.test(service) ? '"' + service + '"' : service) }) }, icon('logs', 13), t('apm.service_logs')),
    h('a', { class: 'btn sm', href: href('/monitors/new', { type: 'apm', service }) }, icon('monitor', 13), t('apm.create_monitor'))));
  const chartsEl = h('div', { class: 'mb-3' });
  const opsEl = h('div');
  const resEl = h('div');
  const tracesEl = h('div');
  root.appendChild(chartsEl);
  root.appendChild(h('div', { class: 'grid c2 mb-3' }, panel(t('apm.operations'), opsEl, { flush: true }), panel(t('apm.resources'), resEl, { flush: true })));
  root.appendChild(panel(t('apm.recent_traces'), tracesEl, { flush: true, actions: h('a', { class: 'btn sm ghost', href: href('/apm/traces', { service }) }, t('apm.all_traces')) }));

  await Promise.all([
    section(chartsEl, async () => {
      const [resp, evs] = await Promise.all([get('/apm/services/' + encodeURIComponent(service), { from: range.from, to: range.to }, { signal }), deployEvents(service, range, signal)]);
      return Object.assign({}, resp, { _deploys: evs });
    }, (resp) => {
      const ts = (resp && resp.timeseries) || {};
      const evs = (resp && resp._deploys) || [];
      const sync = 'svc-' + service;
      const frag = document.createDocumentFragment();
      frag.appendChild(panel(t('apm.chart.hits'), seriesChart(t('col.hits'), ts.hits, 'count', cssVar('--s1'), 'bars', range, ctx.onRange, sync, evs)));
      frag.appendChild(panel(t('apm.chart.errors'), seriesChart(t('col.errors'), ts.errors, 'count', cssVar('--crit'), 'bars', range, ctx.onRange, sync, evs)));
      frag.appendChild(panel(t('apm.chart.p95'), seriesChart('p95', ts.p95_ms, 'millisecond', cssVar('--s7'), 'line', range, ctx.onRange, sync, evs)));
      const opCols = (nameKey) => [
        { key: nameKey, label: nameKey === 'name' ? t('col.operation') : t('col.resource'), tdCls: 'trunc', render: (r) => h('span', { class: 'mono small', title: r[nameKey] || '' }, r[nameKey] || '-') },
        { key: 'hits', label: t('col.hits'), align: 'r', render: (r) => fmtCompact(r.hits) },
        { key: 'errors', label: t('col.errors'), align: 'r', render: (r) => fmtCompact(r.errors) },
        { key: 'error_rate', label: t('col.error_rate'), align: 'r', value: errorPct, render: (r) => { const p = errorPct(r); return h('span', { style: { color: errColor(p) } }, fmtPct(p, 2)); } },
        { key: 'p95_ms', label: 'p95', align: 'r', render: (r) => fmtDuration(r.p95_ms) },
      ];
      clear(opsEl);
      opsEl.appendChild(dataTable({ columns: opCols('name'), rows: asList(resp && resp.operations), sortKey: 'hits', sortDir: 'desc', onRowClick: (r) => ctx.navigate('/apm/traces', { service, operation: r.name }), emptyText: t('apm.no_ops') }));
      clear(resEl);
      resEl.appendChild(dataTable({ columns: opCols('resource'), rows: asList(resp && resp.resources), sortKey: 'p95_ms', sortDir: 'desc', maxRows: 200, emptyText: t('apm.no_resources') }));
      const wrap = h('div', { class: 'grid c3' });
      wrap.appendChild(frag);
      return wrap;
    }),
    section(tracesEl, async () => asList(await get('/apm/traces', { service, from: range.from, to: range.to, limit: 50 }, { signal }), 'traces'), (list) => tracesTable(list, ctx)),
  ]);
}

export function tracesTable(list, ctx) {
  const max = Math.max(1, ...list.map((r) => (isNum(r.duration_ms) ? r.duration_ms : 0)));
  return dataTable({
    columns: [
      { key: 'ts', label: t('col.date'), value: (r) => toMs(r.ts), render: (r) => h('span', { class: 'num' }, fmtDateTime(r.ts)) },
      { key: 'service', label: t('col.service') },
      { key: 'name', label: t('col.operation'), render: (r) => h('span', { class: 'mono small' }, r.name || '-') },
      { key: 'resource', label: t('col.resource'), tdCls: 'trunc', render: (r) => h('span', { class: 'mono small', title: r.resource || '' }, r.resource || '-') },
      { key: 'duration_ms', label: t('col.duration'), align: 'r', render: (r) => barCell((r.duration_ms || 0) / max * 100, fmtDuration(r.duration_ms), r.error ? 'var(--crit)' : null) },
      { key: 'span_count', label: t('col.spans'), align: 'r' },
      { key: 'error', label: t('col.status'), value: (r) => (r.error ? 1 : 0), render: (r) => (r.error ? h('span', { class: 'badge alert' }, t('apm.error')) : h('span', { class: 'badge ok' }, t('apm.ok'))) },
    ],
    rows: list, sortKey: 'ts', sortDir: 'desc',
    onRowClick: (r) => ctx.navigate('/apm/trace/' + encodeURIComponent(r.trace_id)),
    emptyText: t('apm.no_traces'),
  });
}

async function renderTraces(root, ctx) {
  const { range, signal, href, params } = ctx;
  ctx.setCrumbs([{ label: t('nav.apm'), href: href('/apm') }, { label: t('nav.apm.traces') }]);
  const f = {
    service: params.get('service') || '',
    operation: params.get('operation') || '',
    status: params.get('status') || '',
    min_ms: params.get('min_ms') || '',
  };
  const svcSlot = h('span');
  const op = h('input', { class: 'input mono', value: f.operation, placeholder: t('apm.operation_ph'), style: { width: '220px' } });
  const minMs = h('input', { class: 'input', type: 'number', min: 0, value: f.min_ms, placeholder: '0', style: { width: '110px' } });
  const apply = () => ctx.setParams({ service: f.service || null, operation: op.value.trim() || null, status: f.status || null, min_ms: minMs.value || null });
  op.addEventListener('keydown', (e) => { if (e.key === 'Enter') apply(); });
  minMs.addEventListener('keydown', (e) => { if (e.key === 'Enter') apply(); });
  const body = h('div');
  const countEl = h('span', { class: 'sub' });
  root.appendChild(h('div', { class: 'querybar', style: { alignItems: 'flex-end' } },
    field(t('col.service'), svcSlot),
    field(t('col.operation'), op),
    field(t('col.status'), pills([{ key: '', label: t('apm.status.all') }, { key: 'error', label: t('apm.error') }, { key: 'ok', label: t('apm.ok') }], f.status, (k) => { f.status = k; apply(); })),
    field(t('apm.min_duration'), minMs),
    h('button', { class: 'btn primary', type: 'button', onclick: apply }, t('action.apply')),
    viewsMenu({
      page: 'traces', me: ctx.me,
      getState: () => ({ service: f.service, operation: op.value.trim(), status: f.status, min_ms: minMs.value, t: range.preset || null }),
      apply: (st) => { const up = { service: st.service || null, operation: st.operation || null, status: st.status || null, min_ms: st.min_ms || null }; if (st.t) Object.assign(up, { t: st.t, from: null, to: null }); ctx.setParams(up); },
    })));
  root.appendChild(panel(t('nav.apm.traces'), body, { flush: true, actions: countEl }));

  get('/apm/services', { from: range.from, to: range.to }, { signal }).then((resp) => {
    const list = asList(resp, 'services').map((s2) => s2.service).filter(Boolean).sort();
    if (f.service && !list.includes(f.service)) list.unshift(f.service);
    svcSlot.appendChild(select([{ value: '', label: t('apm.all_services') }].concat(list.map((s2) => ({ value: s2, label: s2 }))), f.service, (v) => { f.service = v; apply(); }, { style: { width: '200px' } }));
  }).catch(() => {
    const inp = h('input', { class: 'input', value: f.service, style: { width: '200px' } });
    inp.addEventListener('change', () => { f.service = inp.value.trim(); apply(); });
    svcSlot.appendChild(inp);
  });

  await section(body, async () => asList(await get('/apm/traces', {
    service: f.service || undefined, operation: f.operation || undefined, status: f.status || undefined,
    min_ms: f.min_ms || undefined, from: range.from, to: range.to, limit: 50,
  }, { signal }), 'traces'), (list) => {
    countEl.textContent = tn('apm.trace_count', list.length);
    return tracesTable(list, ctx);
  });
}
