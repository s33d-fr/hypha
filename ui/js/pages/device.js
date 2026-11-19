// Fiche d'équipement réseau (§ 14) : identité SNMP, interfaces (état, vitesse, débits, erreurs, mini-courbes),
// CPU, mémoire, températures, stockage, onduleur, contrôles et traps.
import { h, clear, icon } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, asList, queryMetrics } from '../api.js';
import { fmtPct, fmtRel, fmtDateTime, fmtDuration, fmtNum, isNum, toMs } from '../format.js';
import { empty, tagList, tabs, kv, panel, badgeState, checkStatusBadge, searchInput } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { sparkline } from '../components/spark.js';
import { summarize } from '../components/chart.js';
import { chartPanel, metricChart, tagFilter } from './common.js';
import { eventRow } from './events.js';
import { explainButton, idValue } from '../components/explain.js';
import { load, tile } from './kit.js';
import { downtimeButton, downtimeBanner, coversHost } from './ops-shared.js';

export function isDevice(host) {
  return !!host && (host.kind === 'network_device' || (host.device && typeof host.device === 'object' && (host.device.sys_object_id || host.device.address)));
}

// Débit en bits par seconde, unités décimales du réseau.
export function fmtBits(bitsPerS) {
  if (!isNum(bitsPerS)) return '-';
  const units = ['bit/s', 'kbit/s', 'Mbit/s', 'Gbit/s', 'Tbit/s'];
  let v = Math.abs(bitsPerS);
  let i = 0;
  while (v >= 1000 && i < units.length - 1) { v /= 1000; i++; }
  return fmtNum(v, v >= 100 ? 0 : 1) + ' ' + units[i];
}

const STD_SPEEDS = [10e6, 100e6, 1e9, 2.5e9, 5e9, 10e9, 25e9, 40e9, 100e9];

// Vitesse d'une interface : déclarée par l'agent, sinon déduite du débit et du taux d'occupation.
function ifSpeed(declared, inBps, inPct, outBps, outPct) {
  if (isNum(declared) && declared > 0) return { bits: declared, estimated: false };
  const est = [];
  if (isNum(inBps) && isNum(inPct) && inPct > 0.2) est.push(inBps * 8 / (inPct / 100));
  if (isNum(outBps) && isNum(outPct) && outPct > 0.2) est.push(outBps * 8 / (outPct / 100));
  if (!est.length) return null;
  const raw = est.reduce((a, b) => a + b, 0) / est.length;
  const near = STD_SPEEDS.find((sp) => Math.abs(sp - raw) / sp < 0.15);
  return { bits: near || raw, estimated: true };
}

const OPER = { 1: 'up', 2: 'down', 3: 'testing', 4: 'unknown', 5: 'dormant', 6: 'notPresent', 7: 'lowerLayerDown' };

function ifState(oper, admin) {
  if (admin === 2) return { cls: 'muted', key: 'device.if.admin_down' };
  if (oper === 1) return { cls: 'ok', key: 'device.if.up' };
  if (oper === 2 || oper === 7) return { cls: 'alert', key: 'device.if.down' };
  if (isNum(oper) && OPER[oper]) return { cls: 'nodata', key: 'device.if.' + OPER[oper] };
  return { cls: 'nodata', key: 'device.if.unknown' };
}

function seriesByIface(resp, id) {
  const out = new Map();
  for (const sr of (resp && resp.series) || []) {
    if (sr.query !== id) continue;
    const g = sr.group || {};
    const name = g.interface || g.ifName || g.ifname || '';
    if (!name) continue;
    out.set(String(name), { points: sr.points || [], group: g, sum: summarize((sr.points || []).map((p) => p[1])) });
  }
  return out;
}

export async function renderDevice(root, ctx, host) {
  const { params, signal, range, href } = ctx;
  const id = host.id !== undefined ? host.id : ctx.args.id;
  const name = host.hostname || String(id);
  const dev = host.device && typeof host.device === 'object' ? host.device : {};
  ctx.setCrumbs([{ label: t('nav.infra'), href: href('/infrastructure') }, { label: name }]);
  const hf = '{' + tagFilter('host', name) + '}';

  const viaEl = h('span', { class: 'small text-2' });
  root.appendChild(h('div', { class: 'page-head' },
    h('span', { class: 'device-ico', title: t('infra.kind.network_device') }, icon('device', 18)),
    h('h1', name),
    host.status ? badgeState(host.status === 'up' ? 'UP' : host.status === 'down' ? 'DOWN' : 'NODATA') : null,
    h('span', { class: 'desc' }, [dev.vendor, dev.model, dev.address || (host.ip_addresses || [])[0]].filter(Boolean).join(' · ')),
    viaEl,
    h('span', { class: 'spacer' }),
    explainButton(ctx.me, { kind: 'host', host_id: idValue(id) }, { subject: name }),
    downtimeButton(ctx, { name: t('dt.host_name', { host: name }), subject: t('dt.host_subject', { host: name }), scope: { tags: { host: name } } }),
    h('a', { class: 'btn sm', href: href('/metrics', { e: JSON.stringify([{ agg: 'avg', metric: 'snmp.ifBandwidthIn', filters: ['host:' + name], by: ['interface'] }]) }) }, icon('metrics', 13), t('host.open_metrics')),
    h('a', { class: 'btn sm', href: href('/events', { q: 'source:snmp' }) }, icon('events', 13), t('device.traps'))));

  if (host.via_host_id !== undefined && host.via_host_id !== null) {
    const link = h('a', { href: href('/infrastructure/host/' + encodeURIComponent(host.via_host_id)) }, host.via_hostname || '#' + host.via_host_id);
    viaEl.appendChild(document.createTextNode(t('device.via') + ' '));
    viaEl.appendChild(link);
    if (!host.via_hostname) get('/hosts/' + encodeURIComponent(host.via_host_id), null, { signal }).then((r) => { const x = r && r.host ? r.host : r; if (x && x.hostname) link.textContent = x.hostname; }).catch(() => {});
  }

  // Maintenance active qui couvre l'équipement (§ 16).
  root.appendChild(downtimeBanner(ctx, (d) => coversHost(d, name, host.tags)));

  const tilesEl = h('div', { class: 'tiles mb-3' });
  root.appendChild(tilesEl);
  if (host.tags && Object.keys(host.tags).length) root.appendChild(h('div', { class: 'mb-3' }, tagList(host.tags, (k, v) => ctx.navigate('/infrastructure', { tag: k + ':' + v }))));

  root.appendChild(h('div', { class: 'grid c2 mb-3' },
    panel(t('device.identity'), kv([
      [t('device.f.address'), dev.address ? h('span', { class: 'mono' }, dev.address) : null],
      [t('device.f.sys_name'), dev.sys_name || null],
      [t('device.f.vendor'), dev.vendor || null],
      [t('device.f.model'), dev.model || null],
      [t('device.f.serial'), dev.serial ? h('span', { class: 'mono' }, dev.serial) : null],
      [t('device.f.firmware'), dev.firmware || null],
      [t('device.f.location'), dev.location || null],
      [t('device.f.contact'), dev.contact || null],
      [t('device.f.sys_object_id'), dev.sys_object_id ? h('span', { class: 'mono small' }, dev.sys_object_id) : null],
      [t('device.f.sys_descr'), dev.sys_descr ? h('span', { class: 'small' }, dev.sys_descr) : null],
      [t('col.last_seen'), host.last_seen ? h('span', { title: fmtDateTime(host.last_seen) }, fmtRel(host.last_seen)) : null],
    ])),
    h('div', { class: 'col gap-3' },
      chartPanel(t('device.chart.cpu'), { queries: [{ id: 'c', q: 'avg:snmp.cpu.usage' + hf }], labels: { c: t('col.cpu') }, unit: 'percent', range, signal, onRange: ctx.onRange, sync: 'dev-' + id, height: 120 }),
      chartPanel(t('device.chart.mem'), { queries: [{ id: 'm', q: 'avg:snmp.memory.usage' + hf }], labels: { m: t('col.mem') }, unit: 'percent', range, signal, onRange: ctx.onRange, sync: 'dev-' + id, height: 120 }))));

  // Interfaces
  const ifBody = h('div');
  let ifRows = [];
  let ifQ = params.get('ifq') || '';
  const ifCount = h('span', { class: 'sub' });
  const ifSearch = searchInput({ placeholder: t('device.if.search_ph'), value: ifQ, slash: false, onInput: (v) => { ifQ = v; ctx.setParams({ ifq: v || null }, { silent: true }); drawIfaces(); } });
  ifSearch.style.width = '240px';
  root.appendChild(h('div', { class: 'mb-3' }, panel(t('device.interfaces'), ifBody, { flush: true, actions: h('span', { class: 'row' }, ifCount, ifSearch) })));

  function drawIfaces() {
    clear(ifBody);
    const needle = ifQ.trim().toLowerCase();
    const list = ifRows.filter((r) => !needle || (r.name + ' ' + (r.alias || '')).toLowerCase().includes(needle));
    ifCount.textContent = tn('device.if.count', list.length);
    const maxBps = Math.max(1, ...ifRows.map((r) => Math.max(r.inMax || 0, r.outMax || 0)));
    ifBody.appendChild(dataTable({
      columns: [
        { key: 'state', label: t('col.state'), value: (r) => (r.st.cls === 'alert' ? 0 : r.st.cls === 'ok' ? 2 : 1), render: (r) => h('span', { class: ['badge', r.st.cls] }, h('span', { class: ['dot', r.st.cls === 'muted' ? 'nodata' : r.st.cls] }), t(r.st.key)) },
        { key: 'name', label: t('device.if.name'), render: (r) => h('span', { class: 'mono small' }, r.name) },
        { key: 'alias', label: t('device.if.alias'), tdCls: 'trunc', render: (r) => h('span', { class: 'text-2', title: r.alias || '' }, r.alias || '-') },
        { key: 'speed', label: t('device.if.speed'), align: 'r', value: (r) => (r.speed ? r.speed.bits : null), render: (r) => (r.speed ? h('span', { title: r.speed.estimated ? t('device.if.speed_est') : null }, (r.speed.estimated ? '≈ ' : '') + fmtBits(r.speed.bits)) : '-') },
        { key: 'in', label: t('device.if.in'), align: 'r', value: (r) => r.inLast, render: (r) => trafficCell(r.inPts, r.inLast, r.uin, maxBps, 'var(--s1)') },
        { key: 'out', label: t('device.if.out'), align: 'r', value: (r) => r.outLast, render: (r) => trafficCell(r.outPts, r.outLast, r.uout, maxBps, 'var(--s3)') },
        { key: 'errors', label: t('device.if.errors'), align: 'r', value: (r) => r.errors, render: (r) => (isNum(r.errors) ? h('span', { style: { color: r.errors > 0 ? 'var(--crit)' : null } }, fmtNum(r.errors, 2) + ' ' + t('unit.pps')) : '-') },
      ],
      rows: list, sortKey: 'name', sortDir: 'asc', maxRows: 500,
      onRowClick: (r) => ctx.navigate('/metrics', { e: JSON.stringify([
        { agg: 'avg', metric: 'snmp.ifBandwidthIn', filters: ['host:' + name, 'interface:' + r.name], by: [] },
        { agg: 'avg', metric: 'snmp.ifBandwidthOut', filters: ['host:' + name, 'interface:' + r.name], by: [] }]) }),
      emptyText: ifRows.length ? t('device.if.no_match') : t('device.if.none'),
    }));
  }

  function trafficCell(pts, last, usage, maxBps, color) {
    if (!isNum(last) && !(pts && pts.length)) return '-';
    return h('div', { class: 'row', style: { gap: '8px', justifyContent: 'flex-end' } },
      sparkline((pts || []).map((p) => p[1]), { width: 90, height: 20, color, min: 0, max: maxBps }),
      h('span', { class: 'num', style: { minWidth: '86px', textAlign: 'right' } }, fmtBits(isNum(last) ? last * 8 : null),
        isNum(usage) ? h('div', { class: 'xsmall muted' }, fmtPct(usage, 1)) : null));
  }

  load(ifBody, async () => {
    const qs = [
      { id: 'in', q: 'avg:snmp.ifBandwidthIn' + hf + ' by {interface}' },
      { id: 'out', q: 'avg:snmp.ifBandwidthOut' + hf + ' by {interface}' },
      { id: 'uin', q: 'avg:snmp.ifBandwidthInUsage' + hf + ' by {interface}' },
      { id: 'uout', q: 'avg:snmp.ifBandwidthOutUsage' + hf + ' by {interface}' },
      { id: 'ein', q: 'sum:snmp.ifInErrors' + hf + ' by {interface}' },
      { id: 'eout', q: 'sum:snmp.ifOutErrors' + hf + ' by {interface}' },
      { id: 'op', q: 'max:snmp.ifOperStatus' + hf + ' by {interface,interface_alias}' },
      { id: 'adm', q: 'max:snmp.ifAdminStatus' + hf + ' by {interface}' },
    ];
    return queryMetrics(qs, range, 60, signal);
  }, (resp) => {
    const m = {};
    for (const k of ['in', 'out', 'uin', 'uout', 'ein', 'eout', 'op', 'adm']) m[k] = seriesByIface(resp, k);
    const names = new Set();
    Object.values(m).forEach((mp) => mp.forEach((v, k) => names.add(k)));
    const declared = new Map((Array.isArray(dev.interfaces) ? dev.interfaces : []).filter((x) => x && x.name).map((x) => [String(x.name), x]));
    ifRows = Array.from(names).map((nm) => {
      const g = (k) => m[k].get(nm);
      const decl = declared.get(nm) || {};
      const inS = g('in'); const outS = g('out');
      const uin = g('uin') ? g('uin').sum.last : null;
      const uout = g('uout') ? g('uout').sum.last : null;
      const errs = [g('ein'), g('eout')].filter(Boolean).map((x) => x.sum.avg).filter(isNum);
      const oper = g('op') ? g('op').sum.last : (isNum(decl.oper_status) ? decl.oper_status : null);
      const admin = g('adm') ? g('adm').sum.last : (isNum(decl.admin_status) ? decl.admin_status : null);
      const alias = (g('op') && g('op').group.interface_alias) || decl.alias || '';
      const speedDecl = isNum(decl.speed_bps) ? decl.speed_bps : isNum(decl.high_speed) ? decl.high_speed * 1e6 : isNum(decl.speed) ? decl.speed : null;
      return {
        name: nm, alias, st: ifState(oper, admin),
        inPts: inS ? inS.points : [], outPts: outS ? outS.points : [],
        inLast: inS ? inS.sum.last : null, outLast: outS ? outS.sum.last : null,
        inMax: inS ? inS.sum.max : 0, outMax: outS ? outS.sum.max : 0,
        uin, uout,
        errors: errs.length ? errs.reduce((a, b) => a + b, 0) : null,
        speed: ifSpeed(speedDecl, inS ? inS.sum.last : null, uin, outS ? outS.sum.last : null, uout),
      };
    });
    drawTiles();
    drawIfaces();
    return null;
  });

  function drawTiles() {
    clear(tilesEl);
    const up = ifRows.filter((r) => r.st.cls === 'ok').length;
    const down = ifRows.filter((r) => r.st.cls === 'alert').length;
    const errSum = ifRows.reduce((a, r) => a + (isNum(r.errors) ? r.errors : 0), 0);
    tilesEl.appendChild(tile(t('device.tile.interfaces'), up + ' / ' + ifRows.length, down ? tn('device.tile.down', down) : t('device.tile.all_up'), down ? 'var(--crit)' : null));
    tilesEl.appendChild(tile(t('device.tile.errors'), fmtNum(errSum, 2), t('unit.pps'), errSum > 0 ? 'var(--warn)' : null));
    const upEl = tile(t('device.tile.uptime'), '…');
    const cpuEl = tile(t('col.cpu'), '…');
    const memEl = tile(t('col.mem'), '…');
    tilesEl.appendChild(cpuEl);
    tilesEl.appendChild(memEl);
    tilesEl.appendChild(upEl);
    tilesEl.appendChild(tile(t('col.last_seen'), fmtRel(host.last_seen), fmtDateTime(host.last_seen)));
    queryMetrics([{ id: 'up', q: 'max:snmp.sysUpTimeInstance' + hf }, { id: 'cpu', q: 'avg:snmp.cpu.usage' + hf }, { id: 'mem', q: 'avg:snmp.memory.usage' + hf }], { from: Date.now() - 15 * 60e3, to: Date.now() }, 20, signal).then((r) => {
      const last = (id2) => { const sr = ((r && r.series) || []).find((x) => x.query === id2); return sr ? summarize((sr.points || []).map((p) => p[1])).last : null; };
      const setV = (el, v) => { el.querySelector('.t-value').textContent = v; };
      const up2 = last('up');
      setV(upEl, isNum(up2) ? fmtDuration(up2 * 1000, true) : '-');
      setV(cpuEl, fmtPct(last('cpu')));
      setV(memEl, fmtPct(last('mem')));
    }).catch(() => { [upEl, cpuEl, memEl].forEach((el) => { el.querySelector('.t-value').textContent = '-'; }); });
  }

  // Capteurs et équipements particuliers : un graphique seulement s'il a des données.
  const extraEl = h('div', { class: 'charts-grid mb-3' });
  root.appendChild(extraEl);
  const optional = [
    [t('device.chart.temp'), [{ id: 'tp', q: 'max:snmp.temperature' + hf + ' by {sensor}' }], '°C'],
    [t('device.chart.storage'), [{ id: 'st', q: 'max:snmp.storage.usage' + hf + ' by {storage}' }], 'percent'],
    [t('device.chart.fans'), [{ id: 'fn', q: 'avg:snmp.fan.speed' + hf + ' by {sensor}' }], t('device.unit.rpm')],
    [t('device.chart.ups_charge'), [{ id: 'bc', q: 'avg:snmp.ups.battery.charge' + hf }, { id: 'bl', q: 'avg:snmp.ups.battery.load' + hf }], 'percent', { bc: t('device.ups.charge'), bl: t('device.ups.load') }],
    [t('device.chart.ups_runtime'), [{ id: 'rt', q: 'avg:snmp.ups.battery.runtime' + hf }], 'second'],
    [t('device.chart.poll'), [{ id: 'pd', q: 'avg:snmp.poll.duration' + hf }], 'millisecond'],
  ];
  for (const [title, queries, unit, labels] of optional) {
    const body = h('div', { class: 'panel-body' });
    const pnl = h('section', { class: 'panel', hidden: true }, h('div', { class: 'panel-head' }, h('h3', title)), body);
    extraEl.appendChild(pnl);
    metricChart(body, { queries, range, signal, unit, labels, onRange: ctx.onRange, sync: 'dev-' + id, height: 150,
      onData: (resp) => { if ((resp && resp.series || []).some((x) => (x.points || []).some((p) => isNum(p[1])))) pnl.hidden = false; } });
  }

  // Contrôles et traps
  let tab = ['checks', 'traps'].includes(params.get('dtab')) ? params.get('dtab') : 'checks';
  const tabBar = h('div');
  const tabBody = h('div');
  root.appendChild(h('section', { class: 'panel' }, tabBar, tabBody));
  function drawTabs() {
    clear(tabBar);
    tabBar.appendChild(tabs([{ key: 'checks', label: t('host.tab.checks') }, { key: 'traps', label: t('device.traps') }], tab, (k) => { tab = k; ctx.setParams({ dtab: k }, { silent: true }); drawTabs(); loadTab(); }));
  }
  function loadTab() {
    clear(tabBody);
    if (tab === 'checks') {
      load(tabBody, async () => {
        if (Array.isArray(host.checks) && host.checks.length) return host.checks;
        return asList(await get('/checks', { host_id: id }, { signal }), 'checks');
      }, (list) => dataTable({
        columns: [
          { key: 'status', label: t('col.status'), sort: (r) => -(r.status ?? 3), render: (r) => checkStatusBadge(r.status) },
          { key: 'name', label: t('col.check'), render: (r) => h('span', { class: 'mono small' }, r.name || r.check_name || '-') },
          { key: 'iface', label: t('device.if.name'), value: (r) => (r.tags && r.tags.interface) || '', render: (r) => h('span', { class: 'mono small' }, (r.tags && r.tags.interface) || '-') },
          { key: 'message', label: t('col.message'), tdCls: 'trunc', render: (r) => h('span', { title: r.message || '' }, r.message || '-') },
          { key: 'ts', label: t('col.last_run'), align: 'r', value: (r) => toMs(r.ts), render: (r) => h('span', { title: fmtDateTime(r.ts) }, fmtRel(r.ts)) },
        ],
        rows: list, sortKey: 'status', sortDir: 'asc', emptyText: t('checks.none'),
      }));
    } else {
      load(tabBody, async () => asList(await get('/events', { q: 'source:snmp', from: range.from, to: range.to }, { signal }), 'events'), (list) => {
        const mine = list.filter((ev) => String(ev.host_id) === String(id) || (ev.tags && (ev.tags.host === name || ev.tags.snmp_device === dev.address)));
        if (!mine.length) return empty(null, t('device.no_traps'));
        return h('div', mine.slice(0, 200).map((ev) => eventRow(ev, ctx, { dateTime: true })));
      });
    }
  }
  drawTabs();
  loadTab();
  return null;
}

