// Fiche hôte : synthèse, graphiques système, puis onglets (processus, connexions, conteneurs, contrôles, journaux, métadonnées).
import { h, clear, icon } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, asList, isAbort } from '../api.js';
import { fmtPct, fmtBytes, fmtRel, fmtDateTime, fmtNum, fmtDuration, toMs, isNum } from '../format.js';
import { section, empty, tagList, tabs, kv, checkStatusBadge, panel, searchInput, badgeState, errorState } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { jsonTree } from '../components/jsontree.js';
import { chartPanel, tagFilter, qv } from './common.js';
import { processColumns, filterProcesses } from './processes.js';
import { connectionColumns } from './network.js';
import { containerColumns } from './containers.js';
import { compactLogRow } from './logs-shared.js';
import { platformLabel } from './infra.js';
import { explainButton, idValue } from '../components/explain.js';
import { downtimeButton, downtimeBanner, coversHost } from './ops-shared.js';
import { inventoryTab } from './inventory.js';

const TABS = ['processes', 'connections', 'containers', 'checks', 'inventory', 'logs', 'meta'];

export function checkColumns(ctx, opts = {}) {
  const cols = [
    { key: 'status', label: t('col.status'), sort: (r) => -(r.status ?? 3), render: (r) => checkStatusBadge(r.status) },
    { key: 'name', label: t('col.check'), value: (r) => r.name || r.check_name, render: (r) => h('span', { class: 'mono small' }, r.name || r.check_name || '-') },
  ];
  if (opts.withHost) cols.push({ key: 'hostname', label: t('col.host'), value: (r) => r.hostname || (r.tags && r.tags.host) || '', render: (r) => r.host_id ? h('a', { href: ctx.href('/infrastructure/host/' + r.host_id) }, r.hostname || (r.tags && r.tags.host) || String(r.host_id)) : (r.hostname || '-') });
  cols.push(
    { key: 'message', label: t('col.message'), tdCls: 'trunc', render: (r) => h('span', { title: r.message || '' }, r.message || '-') },
    { key: 'tags', label: t('col.tags'), sort: false, render: (r) => tagList(r.tags) },
    { key: 'ts', label: t('col.last_run'), align: 'r', value: (r) => toMs(r.ts), render: (r) => h('span', { title: fmtDateTime(r.ts) }, fmtRel(r.ts)) },
  );
  return cols;
}

export async function render(root, ctx) {
  const { args, params, signal, range, href } = ctx;
  const id = args.id;
  ctx.setCrumbs([{ label: t('nav.infra'), href: href('/infrastructure') }, { label: t('host.loading') }]);

  let host;
  try {
    const resp = await get('/hosts/' + encodeURIComponent(id), null, { signal });
    host = resp && resp.host && typeof resp.host === 'object' ? Object.assign({}, resp, resp.host) : resp;
  } catch (e) {
    if (isAbort(e)) return;
    root.appendChild(errorState(e, () => ctx.refresh()));
    return;
  }
  if (!host || typeof host !== 'object') { root.appendChild(empty(t('err.404'), null)); return; }
  // Équipement réseau (§ 14) : fiche propre (identité SNMP, interfaces, capteurs, traps).
  // Ressource d'un connecteur (§ 34-35) : fiche propre (fournisseur, type, courbes, contrôles).
  if (host.kind === 'cloud_resource') return (await import('./resource.js')).renderResource(root, ctx, host);
  if (host.kind === 'network_device' || (host.device && typeof host.device === 'object' && host.device.sys_object_id)) return (await import('./device.js')).renderDevice(root, ctx, host);
  const name = host.hostname || String(id);
  ctx.setCrumbs([{ label: t('nav.infra'), href: href('/infrastructure') }, { label: name }]);
  const hf = '{' + tagFilter('host', name) + '}';
  const isWindows = String(host.os || '').toLowerCase() === 'windows';

  // En-tête
  const bootMs = toMs(host.boot_time);
  root.appendChild(h('div', { class: 'page-head' },
    h('h1', name),
    host.status ? badgeState(host.status === 'up' ? 'UP' : host.status === 'down' ? 'DOWN' : 'NODATA') : null,
    h('span', { class: 'desc' }, [platformLabel(host), host.arch, host.cpu_cores ? tn('host.cores', host.cpu_cores) : null, isNum(host.mem_total) ? fmtBytes(host.mem_total) : null].filter(Boolean).join(' · ')),
    h('span', { class: 'spacer' }),
    explainButton(ctx.me, { kind: 'host', host_id: idValue(host.id !== undefined ? host.id : id) }, { subject: name }),
    downtimeButton(ctx, { name: t('dt.host_name', { host: name }), subject: t('dt.host_subject', { host: name }), scope: { tags: { host: name } } }),
    h('a', { class: 'btn sm', href: href('/logs', { q: 'host:' + qv(name) }) }, icon('logs', 13), t('host.open_logs')),
    h('a', { class: 'btn sm', href: href('/metrics', { e: JSON.stringify([{ agg: 'avg', metric: 'system.cpu.usage', filters: ['host:' + name], by: [] }]) }) }, icon('metrics', 13), t('host.open_metrics'))));

  // Maintenance active qui couvre l'hôte (§ 16) : bandeau au-dessus de la synthèse.
  root.appendChild(downtimeBanner(ctx, (d) => coversHost(d, name, host.tags)));

  const facts = h('div', { class: 'tiles mb-3' },
    tile(t('col.cpu'), fmtPct(host.cpu)),
    tile(t('col.mem'), fmtPct(host.mem_pct)),
    tile(t('col.disk_max'), fmtPct(host.disk_max_pct)),
    tile(t('col.load_norm'), isNum(host.load_norm) ? fmtNum(host.load_norm, 2) : '-'),
    tile(t('host.uptime'), bootMs ? fmtDuration(Date.now() - bootMs, true) : '-', bootMs ? fmtDateTime(bootMs) : null),
    tile(t('col.last_seen'), fmtRel(host.last_seen), fmtDateTime(host.last_seen)));
  root.appendChild(facts);
  if (host.tags && Object.keys(host.tags).length) root.appendChild(h('div', { class: 'mb-3' }, tagList(host.tags, (k, v) => ctx.navigate('/infrastructure', { tag: k + ':' + v }))));
  if (Array.isArray(host.seen_by) && host.seen_by.length) root.appendChild(seenByPanel(host.seen_by, href));

  // Graphiques synchronisés
  const sync = 'host-' + id;
  const common = { range, signal, onRange: ctx.onRange, sync, height: 170 };
  root.appendChild(h('div', { class: 'charts-grid mb-3' },
    chartPanel(t('host.chart.cpu'), Object.assign({}, common, {
      queries: (isWindows ? ['user', 'system', 'interrupt'] : ['user', 'system', 'iowait', 'steal']).map((m, i) => ({ id: 'c' + i, q: 'avg:system.cpu.' + m + hf })),
      labels: Object.fromEntries((isWindows ? ['user', 'system', 'interrupt'] : ['user', 'system', 'iowait', 'steal']).map((m, i) => ['c' + i, t('host.cpu.' + m)])),
      unit: 'percent', stacked: true, display: 'area',
    })),
    chartPanel(t('host.chart.mem'), Object.assign({}, common, {
      queries: [{ id: 'u', q: 'avg:system.mem.used' + hf }, { id: 'c', q: 'avg:system.mem.cached' + hf }, { id: 't', q: 'avg:system.mem.total' + hf }],
      labels: { u: t('host.mem.used'), c: t('host.mem.cached'), t: t('host.mem.total') }, unit: 'byte', displayById: { u: 'area' },
    })),
    chartPanel(t('host.chart.load'), Object.assign({}, common, {
      queries: [{ id: 'l1', q: 'avg:system.load.1' + hf }, { id: 'l5', q: 'avg:system.load.5' + hf }, { id: 'l15', q: 'avg:system.load.15' + hf }],
      labels: { l1: '1 min', l5: '5 min', l15: '15 min' }, unit: 'count', emptyText: isWindows ? t('host.load_windows') : undefined,
    })),
    chartPanel(t('host.chart.disk'), Object.assign({}, common, {
      queries: [{ id: 'd', q: 'max:system.disk.in_use' + hf + ' by {mount}' }], unit: 'percent', thresholds: [{ value: 90, color: 'alert' }],
    })),
    chartPanel(t('host.chart.io'), Object.assign({}, common, {
      queries: [{ id: 'r', q: 'avg:system.io.read_bytes' + hf + ' by {device}' }, { id: 'w', q: 'avg:system.io.write_bytes' + hf + ' by {device}' }],
      names: { r: t('host.io.read'), w: t('host.io.write') }, unit: 'byte_per_second',
    })),
    chartPanel(t('host.chart.net'), Object.assign({}, common, {
      queries: [{ id: 'i', q: 'avg:system.net.bytes_rcvd' + hf + ' by {interface}' }, { id: 'o', q: 'avg:system.net.bytes_sent' + hf + ' by {interface}' }],
      names: { i: t('host.net.in'), o: t('host.net.out') }, unit: 'byte_per_second',
    }))));

  // Onglets
  let tab = TABS.includes(params.get('tab')) ? params.get('tab') : 'processes';
  const tabBar = h('div');
  const tabBody = h('div');
  root.appendChild(h('section', { class: 'panel' }, tabBar, tabBody));

  function drawTabs() {
    clear(tabBar);
    tabBar.appendChild(tabs(TABS.map((k) => ({ key: k, label: t('host.tab.' + k) })), tab, (k) => { tab = k; ctx.setParams({ tab: k }, { silent: true }); drawTabs(); loadTab(); }));
  }

  function loadTab() {
    clear(tabBody);
    if (tab === 'processes') {
      let rows = [];
      let q = '';
      const listEl = h('div');
      const search = searchInput({ placeholder: t('proc.search_ph'), onInput: (v) => { q = v; draw(); } });
      tabBody.appendChild(h('div', { class: 'panel-body', style: { paddingBottom: '0' } }, search));
      tabBody.appendChild(listEl);
      const draw = () => { clear(listEl); listEl.appendChild(dataTable({ columns: processColumns(ctx), rows: filterProcesses(rows, q), sortKey: 'cpu_pct', sortDir: 'desc', emptyText: t('proc.none') })); };
      section(listEl, async () => asList(await get('/processes', { host_id: id }, { signal }), 'processes'), (list) => { rows = list; draw(); return null; });
    } else if (tab === 'connections') {
      section(tabBody, async () => asList(await get('/connections', { host_id: id, from: range.from, to: range.to }, { signal }), 'connections'),
        (list) => dataTable({ columns: connectionColumns().filter((c) => c.key !== 'hosts'), rows: list, sortKey: 'count', sortDir: 'desc', maxRows: 500, emptyText: t('net.none') }));
    } else if (tab === 'containers') {
      section(tabBody, async () => asList(await get('/containers', { host_id: id }, { signal }), 'containers'),
        (list) => dataTable({ columns: containerColumns(ctx), rows: list, sortKey: 'cpu_pct', sortDir: 'desc', emptyText: t('containers.none_host') }));
    } else if (tab === 'checks') {
      section(tabBody, async () => {
        if (Array.isArray(host.checks) && host.checks.length) return host.checks;
        return asList(await get('/checks', { host_id: id }, { signal }), 'checks');
      }, (list) => dataTable({ columns: checkColumns(ctx), rows: list, sortKey: 'status', sortDir: 'asc', emptyText: t('checks.none') }));
    } else if (tab === 'logs') {
      const q = 'host:' + qv(name);
      section(tabBody, () => get('/logs/search', { q, from: range.from, to: range.to, limit: 100 }, { signal }), (resp) => {
        const logs = (resp && Array.isArray(resp.logs)) ? resp.logs : asList(resp, 'logs');
        const box = h('div', { class: 'log-list' });
        if (!logs.length) box.appendChild(empty(null, t('logs.no_logs_range')));
        logs.forEach((l) => box.appendChild(compactLogRow(l, () => ctx.navigate('/logs', { q, focus: l.id }))));
        box.appendChild(h('div', { class: 'tbl-foot' }, h('a', { href: href('/logs', { q }) }, t('host.open_logs'))));
        return box;
      });
    } else if (tab === 'inventory') {
      inventoryTab(tabBody, ctx, id);
    } else {
      tabBody.appendChild(metaView(host));
    }
  }
  drawTabs();
  loadTab();
}

function tile(label, value, sub) {
  return h('div', { class: 'tile' }, h('div', { class: 't-label' }, label), h('div', { class: 't-value num', style: { fontSize: 'var(--fs-xl)' } }, value), sub ? h('div', { class: 't-sub' }, sub) : null);
}

function metaView(host) {
  const agent = host.agent && typeof host.agent === 'object' ? host.agent : {};
  const status = agent.status && typeof agent.status === 'object' ? agent.status : {};
  const collectors = status.collectors && typeof status.collectors === 'object' ? status.collectors : {};
  const errors = Array.isArray(status.errors) ? status.errors : [];
  return h('div', { class: 'panel-body' },
    h('div', { class: 'grid c2' },
      panel(t('host.meta.system'), kv([
        [t('host.meta.hostname'), host.hostname],
        [t('host.meta.os'), [host.os, host.platform, host.platform_version].filter(Boolean).join(' ')],
        [t('host.meta.kernel'), host.kernel],
        [t('host.meta.arch'), host.arch],
        [t('host.meta.cpu'), [host.cpu_model, host.cpu_cores ? tn('host.cores', host.cpu_cores) : null].filter(Boolean).join(' · ')],
        [t('host.meta.mem'), fmtBytes(host.mem_total)],
        [t('host.meta.ips'), (host.ip_addresses || []).join(', ')],
        [t('host.meta.boot'), fmtDateTime(host.boot_time)],
        [t('host.meta.first_seen'), fmtDateTime(host.first_seen)],
        [t('host.meta.last_seen'), fmtDateTime(host.last_seen)],
      ])),
      panel(t('host.meta.agent'), h('div', { class: 'col' },
        kv([
          [t('host.meta.agent_id'), h('span', { class: 'mono small' }, agent.id || host.agent_id || '-')],
          [t('host.meta.version'), agent.version || host.agent_version],
          [t('host.meta.last_ip'), agent.last_ip],
          [t('host.meta.agent_seen'), fmtDateTime(agent.last_seen)],
        ]),
        Object.keys(collectors).length ? h('div', { class: 'mt-2' }, h('h4', { class: 'mb-2' }, t('host.meta.collectors')),
          h('div', { class: 'tags' }, Object.entries(collectors).map(([k, v]) => h('span', { class: ['badge', v === 'ok' ? 'ok' : v === 'absent' || v === 'disabled' ? 'muted' : 'warn'] }, k + ' : ' + v)))) : null,
        errors.length ? h('div', { class: 'mt-2' }, h('h4', { class: 'mb-2' }, t('host.meta.agent_errors')),
          h('ul', { class: 'list-plain' }, errors.slice(0, 20).map((e) => h('li', { class: 'mono small' }, String(e))))) : null))),
    h('div', { class: 'grid c2 mt-3' },
      panel(t('host.meta.meta'), jsonTree(host.meta || {})),
      panel(t('col.tags'), jsonTree(host.tags || {}))));
}

// § 51 : la même machine lue par un connecteur (serveur OVH, VM vSphere, instance cloud) ; son état
// chez le fournisseur et ses contrôles, sans quitter la fiche de l'agent.
function seenByPanel(list, href) {
  const body = h('div', { class: 'col gap-3' }, list.map((r) => {
    const label = [t('res.provider.' + (r.provider || 'autre')), t('res.type.' + (r.type || 'autre'))].join(' · ');
    const checks = Array.isArray(r.checks) ? r.checks : [];
    return h('div', { class: 'col' },
      kv([
        [t('host.seen_by.resource'), h('a', { href: href('/infrastructure/host/' + encodeURIComponent(r.id)) }, r.hostname || '#' + r.id)],
        [t('res.f.type'), label],
        [t('res.f.display_name'), r.display_name && r.display_name !== r.hostname ? r.display_name : null],
        [t('res.f.power'), r.power_state || null],
        [t('res.f.region'), r.region || null],
        [t('res.f.parent'), r.parent || null],
        [t('res.f.model'), r.model || null],
      ]),
      checks.length ? dataTable({
        columns: [
          { key: 'status', label: t('col.status'), sort: (c) => -(c.status ?? 3), render: (c) => checkStatusBadge(c.status) },
          { key: 'name', label: t('col.check'), render: (c) => h('span', { class: 'mono small' }, c.name || '-') },
          { key: 'message', label: t('col.message'), tdCls: 'trunc', render: (c) => h('span', { title: c.message || '' }, c.message || '-') },
          { key: 'ts', label: t('col.last_run'), align: 'r', value: (c) => toMs(c.ts), render: (c) => h('span', { title: fmtDateTime(c.ts) }, fmtRel(c.ts)) },
        ],
        rows: checks, sortKey: 'status', sortDir: 'asc', emptyText: t('checks.none'),
      }) : null);
  }));
  return h('div', { class: 'mb-3' }, panel(t('host.seen_by.title'), body));
}
