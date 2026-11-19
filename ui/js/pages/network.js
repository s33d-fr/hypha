// Réseau : connexions regroupées (GET /connections), extrémités distantes les plus contactées, ports en écoute.
import { h, clear } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, asList } from '../api.js';
import { fmtInt } from '../format.js';
import { section, searchInput, pills, panel, empty } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { loadHosts, hostSelect, hostsDisplay, hostsCount } from './common.js';

export function connectionColumns() {
  return [
    { key: 'process', label: t('col.process'), render: (r) => h('b', { style: { fontWeight: '500' } }, r.process || '-') },
    { key: 'direction', label: t('col.direction'), render: (r) => h('span', { class: ['badge', r.direction === 'listening' ? 'info' : 'muted'] }, t('net.dir.' + (r.direction || 'outgoing'))) },
    { key: 'proto', label: t('col.proto'), render: (r) => String(r.proto || '-').toUpperCase() },
    { key: 'laddr', label: t('col.local'), value: (r) => r.laddr || '', render: (r) => h('span', { class: 'mono small' }, endpoint(r.laddr, r.lport)) },
    { key: 'raddr', label: t('col.remote'), value: (r) => r.raddr || '', render: (r) => h('span', { class: 'mono small' }, endpoint(r.raddr, r.rport)) },
    { key: 'port', label: t('col.port'), align: 'r', value: mainPort, title: t('net.port_title'), render: (r) => {
      const p = mainPort(r);
      if (!p) return '-';
      return h('span', { class: 'row', style: { gap: '4px', justifyContent: 'flex-end' } }, h('span', { class: 'muted xsmall' }, t(r.direction === 'outgoing' ? 'net.port_remote' : 'net.port_local')), h('b', { class: 'num' }, String(p)));
    } },
    { key: 'state', label: t('col.state') },
    { key: 'count', label: t('col.connections'), align: 'r', render: (r) => fmtInt(r.count) },
    { key: 'hosts', label: t('col.hosts'), value: (r) => hostsCount(r.hosts), render: (r) => h('span', { class: 'small text-2', title: Array.isArray(r.hosts) ? r.hosts.join(', ') : '' }, hostsDisplay(r.hosts)) },
  ];
}

// Port significatif (§ 3.7) : incoming et listening gardent le port local, outgoing le port distant ; 0 = non porté.
export function mainPort(r) {
  const p = r.direction === 'outgoing' ? r.rport : r.lport;
  return p ? Number(p) : null;
}

function endpoint(addr, port) {
  if (!addr && !port) return '-';
  const a = addr || '*';
  const host = a.includes(':') && !a.startsWith('[') && port ? '[' + a + ']' : a;
  return port ? host + ':' + port : host;
}

function topRemotes(rows) {
  const m = new Map();
  for (const r of rows) {
    if (r.direction !== 'outgoing' || !r.raddr) continue;
    const key = endpoint(r.raddr, r.rport);
    if (!m.has(key)) m.set(key, { endpoint: key, raddr: r.raddr, rport: r.rport, count: 0, processes: new Set(), hosts: new Set() });
    const g = m.get(key);
    g.count += r.count || 1;
    if (r.process) g.processes.add(r.process);
    (Array.isArray(r.hosts) ? r.hosts : []).forEach((x) => g.hosts.add(x));
  }
  return Array.from(m.values()).sort((a, b) => b.count - a.count);
}

function filterRows(rows, q) {
  if (!q) return rows;
  const words = q.toLowerCase().split(/\s+/).filter(Boolean);
  return rows.filter((r) => {
    const hay = [r.process, r.raddr, r.rport, r.laddr, r.lport, r.state, r.proto, hostsDisplay(r.hosts)].join(' ').toLowerCase();
    return words.every((w) => hay.includes(w));
  });
}

export async function render(root, ctx) {
  ctx.setCrumbs([{ label: t('nav.network') }]);
  const { params, signal, range } = ctx;
  const hostId = params.get('host_id') || '';
  let q = params.get('q') || '';
  let dir = params.get('dir') || 'all';

  const hostSlot = h('span');
  const mainBody = h('div');
  const topBody = h('div');
  const listenBody = h('div');
  const countEl = h('span', { class: 'sub' });
  const search = searchInput({ placeholder: t('net.search_ph'), value: q, onInput: (v) => { q = v; ctx.setParams({ q: v || null }, { silent: true }); draw(); } });
  search.style.flex = '1 1 300px';
  root.appendChild(h('div', { class: 'querybar' }, hostSlot, search,
    pills(['all', 'outgoing', 'incoming', 'listening'].map((k) => ({ key: k, label: t('net.dir.' + k) })), dir,
      (k) => { dir = k; ctx.setParams({ dir: k === 'all' ? null : k }, { silent: true }); draw(); })));
  root.appendChild(h('div', { class: 'split' },
    panel(t('net.connections'), mainBody, { flush: true, actions: countEl }),
    h('div', { class: 'split-v' },
      panel(t('net.top_remotes'), topBody, { flush: true }),
      panel(t('net.listening'), listenBody, { flush: true }))));

  loadHosts(signal).then((hosts) => hostSlot.appendChild(hostSelect(hosts, hostId, (v) => ctx.setParams({ host_id: v || null })))).catch(() => {});

  let rows = [];
  function draw() {
    const list = filterRows(rows, q).filter((r) => dir === 'all' || r.direction === dir);
    countEl.textContent = tn('net.count', list.length);
    clear(mainBody);
    mainBody.appendChild(dataTable({ columns: connectionColumns(), rows: list, sortKey: 'count', sortDir: 'desc', maxRows: 1000, emptyText: t('net.none') }));
    clear(topBody);
    const top = topRemotes(filterRows(rows, q)).slice(0, 20);
    if (!top.length) topBody.appendChild(empty(null, t('net.none')));
    else {
      const max = top[0].count || 1;
      topBody.appendChild(h('div', { class: 'toplist', style: { padding: '8px 12px' } }, top.map((r) => h('div', { class: 'tl-row', title: Array.from(r.processes).join(', ') },
        h('div', { class: 'tl-bar' }, h('i', { style: { width: Math.max(2, (r.count / max) * 100) + '%' } }), h('span', { class: 'mono' }, r.endpoint)),
        h('span', { class: 'num r', style: { textAlign: 'right' } }, fmtInt(r.count) + (r.processes.size ? ' · ' + Array.from(r.processes)[0] : ''))))));
    }
    clear(listenBody);
    // Connexions entrantes cumulées par port local, rapprochées des ports en écoute.
    const inByPort = new Map();
    for (const r of rows) if (r.direction === 'incoming' && r.lport) inByPort.set(Number(r.lport), (inByPort.get(Number(r.lport)) || 0) + (r.count || 1));
    const listening = rows.filter((r) => r.direction === 'listening').map((r) => Object.assign({}, r, { incoming: inByPort.get(Number(r.lport)) || 0 }));
    listenBody.appendChild(dataTable({
      columns: [
        { key: 'lport', label: t('col.port'), align: 'r' },
        { key: 'proto', label: t('col.proto'), render: (r) => String(r.proto || '-').toUpperCase() },
        { key: 'process', label: t('col.process') },
        { key: 'incoming', label: t('net.incoming_count'), align: 'r', render: (r) => fmtInt(r.incoming) },
        { key: 'hosts', label: t('col.hosts'), value: (r) => hostsCount(r.hosts), render: (r) => hostsDisplay(r.hosts) },
      ],
      rows: listening, sortKey: 'lport', sortDir: 'asc', maxRows: 200, emptyText: t('net.no_listen'),
    }));
  }

  await section(mainBody, async () => asList(await get('/connections', { host_id: hostId || undefined, from: range.from, to: range.to }, { signal }), 'connections'), (list) => {
    rows = list;
    draw();
    return null;
  });
}
