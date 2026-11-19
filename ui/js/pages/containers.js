// Conteneurs : dernier instantané (GET /containers), tous hôtes ou un seul.
import { h, clear } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, asList } from '../api.js';
import { fmtPct, fmtBytes, fmtBps, isNum } from '../format.js';
import { section, searchInput, pills, panel, tagList } from '../components/ui.js';
import { dataTable, barCell, usageColor } from '../components/table.js';
import { loadHosts, hostSelect, hostLink } from './common.js';

export function containerColumns(ctx, opts = {}) {
  const cols = [
    { key: 'state', label: t('col.state'), render: (r) => h('span', { class: ['badge', r.state === 'running' ? 'ok' : r.state === 'exited' || r.state === 'dead' ? 'alert' : 'muted'] }, r.state || '-') },
    { key: 'name', label: t('col.container'), render: (r) => h('b', { style: { fontWeight: '500' } }, r.name || String(r.id || r.container_id || '-').slice(0, 12)) },
    { key: 'image', label: t('col.image'), tdCls: 'trunc', render: (r) => h('span', { class: 'mono small', title: r.image || '' }, r.image || '-') },
  ];
  if (opts.withHost) cols.push({ key: 'hostname', label: t('col.host'), value: (r) => r.hostname || r.host_id, render: (r) => hostLink(ctx.href, r.host_id, r.hostname) });
  cols.push(
    { key: 'cpu_pct', label: t('col.cpu'), align: 'r', render: (r) => barCell(Math.min(100, r.cpu_pct || 0), fmtPct(r.cpu_pct)) },
    { key: 'mem_usage', label: t('col.mem_used'), align: 'r', render: (r) => fmtBytes(r.mem_usage) },
    { key: 'mem_limit', label: t('col.mem_limit'), align: 'r', render: (r) => (r.mem_limit ? fmtBytes(r.mem_limit) : t('containers.no_limit')) },
    { key: 'mem_ratio', label: t('col.mem_of_limit'), align: 'r', value: (r) => (r.mem_limit ? r.mem_usage / r.mem_limit * 100 : null),
      render: (r) => { const v = r.mem_limit && isNum(r.mem_usage) ? r.mem_usage / r.mem_limit * 100 : null; return v === null ? '-' : barCell(v, fmtPct(v), usageColor(v)); } },
    { key: 'net', label: t('col.net_rx_tx'), align: 'r', value: (r) => (r.net_rx_bps || 0) + (r.net_tx_bps || 0), render: (r) => fmtBps(r.net_rx_bps) + ' / ' + fmtBps(r.net_tx_bps) },
    { key: 'labels', label: t('col.labels'), sort: false, render: (r) => tagList(r.labels) },
  );
  return cols;
}

export async function render(root, ctx) {
  ctx.setCrumbs([{ label: t('nav.containers') }]);
  const { params, signal } = ctx;
  const hostId = params.get('host_id') || '';
  let q = params.get('q') || '';
  let st = params.get('state') || 'all';

  const hostSlot = h('span');
  const body = h('div');
  const countEl = h('span', { class: 'sub' });
  const search = searchInput({ placeholder: t('containers.search_ph'), value: q, onInput: (v) => { q = v; ctx.setParams({ q: v || null }, { silent: true }); draw(); } });
  search.style.flex = '1 1 300px';
  root.appendChild(h('div', { class: 'querybar' }, hostSlot, search,
    pills([{ key: 'all', label: t('containers.all') }, { key: 'running', label: t('containers.running') }, { key: 'other', label: t('containers.stopped') }], st,
      (k) => { st = k; ctx.setParams({ state: k === 'all' ? null : k }, { silent: true }); draw(); })));
  root.appendChild(panel(t('containers.title'), body, { flush: true, actions: countEl }));
  loadHosts(signal).then((hosts) => hostSlot.appendChild(hostSelect(hosts, hostId, (v) => ctx.setParams({ host_id: v || null })))).catch(() => {});

  let rows = [];
  function draw() {
    const words = q.toLowerCase().split(/\s+/).filter(Boolean);
    const list = rows.filter((r) => {
      if (st === 'running' && r.state !== 'running') return false;
      if (st === 'other' && r.state === 'running') return false;
      const hay = [r.name, r.image, r.hostname, r.id, JSON.stringify(r.labels || {})].join(' ').toLowerCase();
      return words.every((w) => hay.includes(w));
    });
    countEl.textContent = tn('containers.count', list.length);
    clear(body);
    body.appendChild(dataTable({ columns: containerColumns(ctx, { withHost: !hostId }), rows: list, sortKey: 'cpu_pct', sortDir: 'desc', emptyText: t('containers.none') }));
  }

  await section(body, async () => asList(await get('/containers', { host_id: hostId || undefined }, { signal }), 'containers'), (list) => {
    rows = list;
    draw();
    return null;
  });
}
