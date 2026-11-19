// Processus : dernier instantané de chaque hôte (GET /processes), vue détaillée ou regroupée par nom.
import { h, clear } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, asList } from '../api.js';
import { fmtPct, fmtBytes, fmtBps, fmtInt, fmtRel, fmtDateTime, isNum } from '../format.js';
import { section, searchInput, pills, panel } from '../components/ui.js';
import { dataTable, barCell } from '../components/table.js';
import { loadHosts, hostSelect, hostLink } from './common.js';

const started = (r) => r.started !== undefined ? r.started : r.started_at;

export function processColumns(ctx, opts = {}) {
  const cols = [];
  if (opts.withHost) cols.push({ key: 'hostname', label: t('col.host'), value: (r) => r.hostname || r.host_id, render: (r) => hostLink(ctx.href, r.host_id, r.hostname) });
  cols.push(
    { key: 'pid', label: 'PID', align: 'r' },
    { key: 'name', label: t('col.process'), render: (r) => h('b', { style: { fontWeight: '500' } }, r.name || '-') },
    { key: 'user', label: t('col.user'), value: (r) => r.user || r.username, render: (r) => r.user || r.username || '-' },
    { key: 'cpu_pct', label: t('col.cpu_core'), align: 'r', title: t('col.cpu_core_title'), render: (r) => barCell(Math.min(100, r.cpu_pct || 0), fmtPct(r.cpu_pct)) },
    { key: 'mem_rss', label: t('col.rss'), align: 'r', render: (r) => fmtBytes(r.mem_rss) },
    { key: 'mem_pct', label: t('col.mem'), align: 'r', render: (r) => fmtPct(r.mem_pct) },
    { key: 'threads', label: t('col.threads'), align: 'r', render: (r) => fmtInt(r.threads) },
    { key: 'io', label: t('col.io_rw'), align: 'r', value: (r) => (r.io_read_bps || 0) + (r.io_write_bps || 0), render: (r) => fmtBps(r.io_read_bps) + ' / ' + fmtBps(r.io_write_bps) },
    { key: 'state', label: t('col.state') },
    { key: 'started', label: t('col.started'), align: 'r', value: started, render: (r) => h('span', { title: fmtDateTime(started(r)) }, fmtRel(started(r))) },
    { key: 'cmdline', label: t('col.cmdline'), tdCls: 'trunc', render: (r) => h('span', { class: 'mono small', title: r.cmdline || '' }, r.cmdline || '-') },
  );
  return cols;
}

function groupByName(rows) {
  const m = new Map();
  for (const r of rows) {
    const k = r.name || '?';
    if (!m.has(k)) m.set(k, { name: k, count: 0, hosts: new Set(), cpu: 0, rss: 0, threads: 0 });
    const g = m.get(k);
    g.count++;
    g.hosts.add(r.hostname || r.host_id);
    if (isNum(r.cpu_pct)) g.cpu += r.cpu_pct;
    if (isNum(r.mem_rss)) g.rss += r.mem_rss;
    if (isNum(r.threads)) g.threads += r.threads;
  }
  return Array.from(m.values()).map((g) => Object.assign(g, { hostCount: g.hosts.size }));
}

export function filterProcesses(rows, q) {
  if (!q) return rows;
  const words = q.toLowerCase().split(/\s+/).filter(Boolean);
  return rows.filter((r) => {
    const hay = [r.name, r.cmdline, r.user, r.username, r.hostname, String(r.pid)].join(' ').toLowerCase();
    return words.every((w) => hay.includes(w));
  });
}

export async function render(root, ctx) {
  ctx.setCrumbs([{ label: t('nav.processes') }]);
  const { params, signal } = ctx;
  const hostId = params.get('host_id') || '';
  let q = params.get('q') || '';
  let mode = params.get('view') || 'list';

  const hostSlot = h('span');
  const countEl = h('span', { class: 'sub' });
  const body = h('div');
  const search = searchInput({ placeholder: t('proc.search_ph'), value: q, onInput: (v) => { q = v; ctx.setParams({ q: v || null }, { silent: true }); draw(); } });
  search.style.flex = '1 1 300px';
  root.appendChild(h('div', { class: 'querybar' }, hostSlot, search,
    pills([{ key: 'list', label: t('proc.view.list') }, { key: 'group', label: t('proc.view.group') }], mode, (k) => { mode = k; ctx.setParams({ view: k === 'list' ? null : k }, { silent: true }); draw(); })));
  root.appendChild(panel(t('proc.title'), body, { flush: true, actions: countEl, sub: t('proc.snapshot_note') }));

  let rows = [];
  loadHosts(signal).then((hosts) => {
    hostSlot.appendChild(hostSelect(hosts, hostId, (v) => ctx.setParams({ host_id: v || null })));
  }).catch(() => {});

  function draw() {
    clear(body);
    const list = filterProcesses(rows, q);
    if (mode === 'group') {
      const groups = groupByName(list);
      countEl.textContent = tn('proc.groups', groups.length);
      body.appendChild(dataTable({
        columns: [
          { key: 'name', label: t('col.process'), render: (r) => h('b', { style: { fontWeight: '500' } }, r.name) },
          { key: 'count', label: t('col.instances'), align: 'r', render: (r) => fmtInt(r.count) },
          { key: 'hostCount', label: t('col.hosts'), align: 'r', render: (r) => fmtInt(r.hostCount) },
          { key: 'cpu', label: t('col.cpu_sum'), align: 'r', render: (r) => fmtPct(r.cpu) },
          { key: 'rss', label: t('col.rss_sum'), align: 'r', render: (r) => fmtBytes(r.rss) },
          { key: 'threads', label: t('col.threads'), align: 'r', render: (r) => fmtInt(r.threads) },
        ],
        rows: groups, sortKey: 'cpu', sortDir: 'desc',
        onRowClick: (r) => { q = r.name; mode = 'list'; ctx.setParams({ q: r.name, view: null }); },
      }));
      return;
    }
    countEl.textContent = tn('proc.count', list.length);
    body.appendChild(dataTable({ columns: processColumns(ctx, { withHost: !hostId }), rows: list, sortKey: 'cpu_pct', sortDir: 'desc', maxRows: 500, emptyText: t('proc.none') }));
  }

  await section(body, async () => asList(await get('/processes', { host_id: hostId || undefined, q: q || undefined }, { signal }), 'processes'), (list) => {
    rows = list;
    draw();
    return null;
  });
}
