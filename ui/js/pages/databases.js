// Bases de données (§ 30, DBM) : instances, requêtes les plus coûteuses (pg_stat_statements), détail d'une
// requête (appels et durée moyenne dans le temps), verrous et attentes de l'instance.
import { h, clear, icon, cssVar } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, asList } from '../api.js';
import { fmtDuration, fmtCompact, fmtInt, fmtPct, isNum } from '../format.js';
import { empty, panel, searchInput, select, pills, copyButton, kv } from '../components/ui.js';
import { dataTable, barCell } from '../components/table.js';
import { timeChart, alignPoints } from '../components/chart.js';
import { chartPanel, loadHosts, hostSelect, tagFilter } from './common.js';
import { load, tile, toPoints } from './kit.js';

const SORTS = ['total_ms', 'calls', 'mean_ms'];

function hitColor(r) {
  if (!isNum(r)) return null;
  const p = r <= 1 ? r * 100 : r;
  return p >= 99 ? 'var(--ok)' : p >= 90 ? 'var(--warn)' : 'var(--crit)';
}
function hitPct(r) { return !isNum(r) ? null : r <= 1 ? r * 100 : r; }

function hostsOf(q) {
  const list = asList(q.hosts);
  return list.map((x) => (typeof x === 'object' ? x.hostname || x.name || x.id : x)).filter(Boolean);
}

export async function render(root, ctx) {
  if (ctx.route.view === 'query') return renderQuery(root, ctx);
  return renderList(root, ctx);
}

async function renderList(root, ctx) {
  const { params, range, signal, href } = ctx;
  ctx.setCrumbs([{ label: t('nav.databases') }]);
  const hostId = params.get('host_id') || '';
  const db = params.get('db') || '';
  let sort = SORTS.includes(params.get('sort')) ? params.get('sort') : 'total_ms';
  let q = params.get('q') || '';
  const hostSlot = h('span');
  const dbSlot = h('span');
  const search = searchInput({ placeholder: t('dbm.search_ph'), value: q, onInput: (v) => { q = v; ctx.setParams({ q: v || null }, { silent: true }); draw(); } });
  search.style.flex = '1 1 260px';
  root.appendChild(h('div', { class: 'querybar' }, hostSlot, dbSlot, search,
    pills(SORTS.map((k) => ({ key: k, label: t('dbm.sort.' + k) })), sort, (k) => ctx.setParams({ sort: k === 'total_ms' ? null : k }))));
  const tilesEl = h('div', { class: 'tiles mb-3' });
  const instEl = h('div');
  const listEl = h('div');
  const countEl = h('span', { class: 'sub' });
  const chartsEl = h('div', { class: 'charts-grid mb-3' });
  root.appendChild(tilesEl);
  root.appendChild(h('div', { class: 'mb-3' }, panel(t('dbm.instances'), instEl, { flush: true })));
  root.appendChild(chartsEl);
  root.appendChild(panel(t('dbm.top_queries'), listEl, { flush: true, actions: countEl }));

  loadHosts(signal).then((hosts) => hostSlot.appendChild(hostSelect(hosts, hostId, (v) => ctx.setParams({ host_id: v || null }), t('dbm.all_hosts')))).catch(() => {});

  // Santé PostgreSQL de l'hôte choisi (ou de tout le parc) : verrous, attentes, plus longue requête.
  const hf = hostId ? null : '{*}';
  const hostName = { v: null };
  const addCharts = (f) => {
    const common = { range, signal, onRange: ctx.onRange, sync: 'dbm', height: 140 };
    chartsEl.appendChild(chartPanel(t('dbm.chart.locks'), Object.assign({}, common, { queries: [{ id: 'l', q: 'sum:postgresql.locks' + f + ' by {mode}' }], unit: 'count' })));
    chartsEl.appendChild(chartPanel(t('dbm.chart.waiting'), Object.assign({}, common, { queries: [{ id: 'w', q: 'sum:postgresql.waiting_queries' + f + ' by {host}' }], unit: 'count' })));
    chartsEl.appendChild(chartPanel(t('dbm.chart.longest'), Object.assign({}, common, { queries: [{ id: 'd', q: 'max:postgresql.longest_query_s' + f + ' by {host}' }], unit: 'second' })));
    chartsEl.appendChild(chartPanel(t('dbm.chart.idle_tx'), Object.assign({}, common, { queries: [{ id: 'i', q: 'sum:postgresql.idle_in_transaction' + f + ' by {host}' }], unit: 'count' })));
  };
  if (hf) addCharts(hf);
  else get('/hosts/' + encodeURIComponent(hostId), null, { signal }).then((r) => { const x = r && r.host ? r.host : r; hostName.v = x && x.hostname; addCharts(hostName.v ? '{' + tagFilter('host', hostName.v) + '}' : '{*}'); }).catch(() => addCharts('{*}'));

  let rows = [];
  function draw() {
    const needle = q.trim().toLowerCase();
    const list = rows.filter((r) => !needle || String(r.query || '').toLowerCase().includes(needle) || String(r.db || '').toLowerCase().includes(needle));
    countEl.textContent = tn('dbm.count', list.length);
    clear(listEl);
    if (!rows.length) { listEl.appendChild(empty(t('dbm.none_title'), t('dbm.none_text'))); return; }
    const maxTotal = Math.max(1, ...list.map((r) => r.total_ms || 0));
    listEl.appendChild(dataTable({
      columns: [
        { key: 'query', label: t('dbm.query'), tdCls: 'wrap', sort: false, render: (r) => h('div', { class: 'mono small sql-cell', title: r.query || '' }, String(r.query || '').replace(/\s+/g, ' ').slice(0, 220)) },
        { key: 'db', label: t('dbm.db'), render: (r) => h('span', { class: 'mono small' }, r.db || '-') },
        { key: 'instance', label: t('dbm.instance'), render: (r) => h('span', { class: 'small text-2' }, r.instance || '-') },
        { key: 'calls', label: t('dbm.calls'), align: 'r', render: (r) => fmtCompact(r.calls) },
        { key: 'total_ms', label: t('dbm.total'), align: 'r', render: (r) => barCell((r.total_ms || 0) / maxTotal * 100, fmtDuration(r.total_ms)) },
        { key: 'mean_ms', label: t('dbm.mean'), align: 'r', render: (r) => fmtDuration(r.mean_ms) },
        { key: 'rows', label: t('dbm.rows'), align: 'r', render: (r) => fmtCompact(r.rows) },
        { key: 'hit_ratio', label: t('dbm.hit_ratio'), align: 'r', title: t('dbm.hit_title'), value: (r) => hitPct(r.hit_ratio), render: (r) => h('span', { style: { color: hitColor(r.hit_ratio) } }, fmtPct(hitPct(r.hit_ratio), 1)) },
      ],
      rows: list, sortKey: sort, sortDir: 'desc', maxRows: 500,
      onRowClick: (r) => ctx.navigate('/databases/query/' + encodeURIComponent(r.query_id), { host_id: hostId || null, db: db || null }),
      emptyText: t('dbm.no_match'),
    }));
  }

  await load(listEl, async () => asList(await get('/dbm/queries', { from: range.from, to: range.to, host_id: hostId || undefined, db: db || undefined, sort }, { signal }), 'queries'), (list) => {
    rows = list;
    const dbs = Array.from(new Set(list.map((r) => r.db).filter(Boolean).concat(db ? [db] : []))).sort();
    clear(dbSlot);
    dbSlot.appendChild(select([{ value: '', label: t('dbm.all_dbs') }].concat(dbs.map((x) => ({ value: x, label: x }))), db, (v) => ctx.setParams({ db: v || null }), { style: { width: 'auto' } }));
    const total = list.reduce((a, r) => a + (r.total_ms || 0), 0);
    const calls = list.reduce((a, r) => a + (r.calls || 0), 0);
    const hits = list.filter((r) => isNum(r.hit_ratio));
    clear(tilesEl);
    tilesEl.appendChild(tile(t('dbm.tile.time'), fmtDuration(total), t('dbm.tile.time_sub')));
    tilesEl.appendChild(tile(t('dbm.calls'), fmtCompact(calls)));
    tilesEl.appendChild(tile(t('dbm.tile.mean'), fmtDuration(calls ? total / calls : null)));
    tilesEl.appendChild(tile(t('dbm.tile.queries'), fmtInt(list.length)));
    tilesEl.appendChild(tile(t('dbm.hit_ratio'), fmtPct(hits.length ? hits.reduce((a, r) => a + hitPct(r.hit_ratio), 0) / hits.length : null, 1), null, hits.length ? hitColor(hits.reduce((a, r) => a + hitPct(r.hit_ratio), 0) / hits.length / 100) : null));
    // Instances : regroupement côté navigateur
    const inst = new Map();
    for (const r of list) {
      const k = (r.instance || '-') + '|' + (r.db || '-');
      if (!inst.has(k)) inst.set(k, { instance: r.instance || '-', db: r.db || '-', total_ms: 0, calls: 0, queries: 0, hosts: new Set() });
      const g = inst.get(k);
      g.total_ms += r.total_ms || 0; g.calls += r.calls || 0; g.queries++;
      hostsOf(r).forEach((x) => g.hosts.add(x));
    }
    clear(instEl);
    const instRows = Array.from(inst.values());
    const maxI = Math.max(1, ...instRows.map((g) => g.total_ms));
    instEl.appendChild(dataTable({
      columns: [
        { key: 'instance', label: t('dbm.instance'), render: (g) => h('span', { class: 'mono small' }, g.instance) },
        { key: 'db', label: t('dbm.db'), render: (g) => h('a', { href: href('/databases', { host_id: hostId || null, db: g.db === '-' ? null : g.db }) }, g.db) },
        { key: 'hosts', label: t('col.hosts'), value: (g) => g.hosts.size, render: (g) => h('span', { class: 'small text-2' }, Array.from(g.hosts).slice(0, 3).join(', ') || '-') },
        { key: 'queries', label: t('dbm.tile.queries'), align: 'r' },
        { key: 'calls', label: t('dbm.calls'), align: 'r', render: (g) => fmtCompact(g.calls) },
        { key: 'total_ms', label: t('dbm.total'), align: 'r', render: (g) => barCell(g.total_ms / maxI * 100, fmtDuration(g.total_ms)) },
      ], rows: instRows, sortKey: 'total_ms', sortDir: 'desc', emptyText: t('dbm.none_title'),
    }));
    draw();
    return null;
  });
}

async function renderQuery(root, ctx) {
  const { args, range, signal, href, params } = ctx;
  const id = args.id;
  ctx.setCrumbs([{ label: t('nav.databases'), href: href('/databases', { host_id: params.get('host_id') || null, db: params.get('db') || null }) }, { label: t('dbm.query_n', { id: String(id).slice(0, 12) }) }]);
  const head = h('div');
  const chartsEl = h('div', { class: 'grid c2 mb-3' });
  root.appendChild(head);
  root.appendChild(chartsEl);
  // Ligne de la liste (texte, totaux) et séries du détail.
  const [rowR, detR] = await Promise.all([
    get('/dbm/queries', { from: range.from, to: range.to, host_id: params.get('host_id') || undefined, db: params.get('db') || undefined, sort: 'total_ms' }, { signal }).then((r) => asList(r, 'queries')).catch(() => []),
    get('/dbm/queries/' + encodeURIComponent(id), { from: range.from, to: range.to }, { signal }).then((r) => ({ ok: r })).catch((e) => ({ err: e })),
  ]);
  const row = rowR.find((r) => String(r.query_id) === String(id)) || {};
  const det = detR.ok || {};
  const text = det.query || row.query || '';
  head.appendChild(h('div', { class: 'page-head' }, h('h1', t('dbm.query_n', { id: String(id).slice(0, 12) })), row.db || det.db ? h('span', { class: 'tag' }, row.db || det.db) : null, h('span', { class: 'spacer' }), copyButton(() => text)));
  head.appendChild(h('div', { class: 'tiles mb-3' },
    tile(t('dbm.calls'), fmtCompact(row.calls)), tile(t('dbm.total'), fmtDuration(row.total_ms)), tile(t('dbm.mean'), fmtDuration(row.mean_ms)),
    tile(t('dbm.rows'), fmtCompact(row.rows)), tile(t('dbm.hit_ratio'), fmtPct(hitPct(row.hit_ratio), 1), null, hitColor(row.hit_ratio))));
  head.appendChild(h('div', { class: 'mb-3' }, panel(t('dbm.text'), h('pre', { class: 'sql-pre' }, text || t('dbm.no_text')), { sub: t('dbm.text_sub') })));
  if (detR.err) { load(chartsEl, () => Promise.reject(detR.err), () => null); return; }
  const series = det.series && typeof det.series === 'object' ? det.series : det;
  const calls = toPoints(series.calls || series.calls_series);
  const mean = toPoints(series.mean_ms || series.mean || series.avg_ms);
  const mk = (label, pts, unit, color, display) => {
    const { xs, ys } = alignPoints([pts]);
    return timeChart({ xs, lines: [{ label, values: ys[0], color }], unit, height: 170, display, legend: false, xRange: [range.from, range.to], onRange: ctx.onRange, sync: 'dbq' });
  };
  chartsEl.appendChild(panel(t('dbm.chart.calls'), mk(t('dbm.calls'), calls, 'count', cssVar('--s1'), 'bars')));
  chartsEl.appendChild(panel(t('dbm.chart.mean'), mk(t('dbm.mean'), mean, 'millisecond', cssVar('--s7'), 'line')));
  const hosts = hostsOf(row);
  root.appendChild(panel(t('dbm.about'), kv([[t('dbm.instance'), row.instance || det.instance || null], [t('dbm.db'), row.db || det.db || null], [t('col.hosts'), hosts.length ? hosts.join(', ') : null], ['query_id', h('span', { class: 'mono small' }, String(id))]]),
    { actions: h('a', { class: 'btn sm', href: href('/logs', { q: 'service:postgresql' }) }, icon('logs', 12), t('dbm.pg_logs')) }));
}
