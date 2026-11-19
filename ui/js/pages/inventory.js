// Inventaire (§ 15) : parc matériel et logiciel filtrable, export CSV, recherche d'un paquet sur tout le parc,
// et onglet « Inventaire » de la fiche hôte (matériel, système, mises à jour, paquets).
import { h, clear, icon, debounce } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, asList, isAbort } from '../api.js';
import { fmtBytes, fmtRel, fmtDateTime, fmtInt, fmtDate, isNum } from '../format.js';
import { empty, panel, searchInput, select, kv, toast, pills } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { hostLink } from './common.js';
import { load, tile, download, toCsv, stamp, routeErrorState, splitTag } from './kit.js';

function cpuLabel(cpu) {
  if (!cpu) return '-';
  if (typeof cpu === 'string') return cpu;
  const parts = [cpu.model, isNum(cpu.cores) ? tn('inv.cores', cpu.cores) : null, isNum(cpu.threads) && cpu.threads !== cpu.cores ? tn('inv.threads', cpu.threads) : null];
  return parts.filter(Boolean).join(' · ') || '-';
}

function osLabel(r) {
  const o = r.os && typeof r.os === 'object' ? r.os : null;
  if (o) return [o.name, o.version].filter(Boolean).join(' ') || '-';
  return [r.os, r.platform, r.platform_version].filter((x, i, a) => x && a.indexOf(x) === i).join(' ') || '-';
}

function updatesCell(pending, security) {
  if (!isNum(pending) && !isNum(security)) return h('span', { class: 'muted' }, '-');
  return h('span', { class: 'row', style: { gap: '6px', justifyContent: 'flex-end' } },
    h('span', { class: 'num' }, fmtInt(pending || 0)),
    isNum(security) && security > 0 ? h('span', { class: 'badge alert', title: t('inv.security_title') }, icon('shield', 11), fmtInt(security)) : null);
}

export async function render(root, ctx) {
  const { params, signal, href } = ctx;
  ctx.setCrumbs([{ label: t('nav.infra'), href: href('/infrastructure') }, { label: t('nav.infra.inventory') }]);
  let q = params.get('q') || '';
  let os = params.get('os') || '';
  const tagParam = params.get('tag') || '';
  let only = params.get('only') || 'all';

  const tilesEl = h('div', { class: 'mb-3' });
  const osEl = h('div');
  const vendEl = h('div');
  const listEl = h('div');
  const countEl = h('span', { class: 'sub' });
  const osSlot = h('span');
  const search = searchInput({ placeholder: t('inv.search_ph'), value: q, onInput: (v) => { q = v; ctx.setParams({ q: v || null }, { silent: true }); draw(); } });
  search.style.flex = '1 1 280px';
  const tagInput = h('input', { class: 'input mono', value: tagParam, placeholder: 'env:prod', style: { width: '160px' }, title: t('inv.tag_title') });
  tagInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') ctx.setParams({ tag: tagInput.value.trim() || null }); });
  const csvBtn = h('button', { class: 'btn', type: 'button', onclick: () => exportCsv() }, icon('download', 13), t('inv.export_csv'));
  root.appendChild(h('div', { class: 'querybar' }, search, osSlot, tagInput,
    pills([{ key: 'all', label: t('inv.only.all') }, { key: 'updates', label: t('inv.only.updates') }, { key: 'security', label: t('inv.only.security') }, { key: 'reboot', label: t('inv.only.reboot') }], only,
      (k) => { only = k; ctx.setParams({ only: k === 'all' ? null : k }, { silent: true }); draw(); }),
    csvBtn));
  root.appendChild(tilesEl);
  root.appendChild(h('div', { class: 'grid c2 mb-3' }, panel(t('inv.by_os'), osEl, { flush: true }), panel(t('inv.by_vendor'), vendEl, { flush: true })));
  root.appendChild(h('div', { class: 'mb-3' }, panel(t('inv.hosts'), listEl, { flush: true, actions: countEl })));
  root.appendChild(packageSearch(ctx));

  let rows = [];
  function filtered() {
    const needle = q.trim().toLowerCase();
    return rows.filter((r) => {
      if (only === 'updates' && !(r.updates_pending > 0)) return false;
      if (only === 'security' && !(r.updates_security > 0)) return false;
      if (only === 'reboot' && !r.reboot_required) return false;
      if (!needle) return true;
      const hay = [r.hostname, osLabel(r), r.vendor, r.product, r.serial, cpuLabel(r.cpu)].join(' ').toLowerCase();
      return needle.split(/\s+/).every((w) => hay.includes(w));
    });
  }

  const columns = [
    { key: 'hostname', label: t('col.host'), render: (r) => hostLink(href, r.host_id, r.hostname) },
    { key: 'os', label: t('col.os'), value: osLabel, render: (r) => h('span', { class: 'text-2' }, osLabel(r)) },
    { key: 'vendor', label: t('inv.vendor'), value: (r) => [r.vendor, r.product].filter(Boolean).join(' '), render: (r) => h('span', { class: 'ellipsis', style: { maxWidth: '240px', display: 'inline-block' }, title: [r.vendor, r.product].filter(Boolean).join(' ') }, [r.vendor, r.product].filter(Boolean).join(' ') || '-') },
    { key: 'serial', label: t('inv.serial'), render: (r) => h('span', { class: 'mono small' }, r.serial || '-') },
    { key: 'cpu', label: t('col.cpu'), value: cpuLabel, tdCls: 'trunc', render: (r) => h('span', { class: 'small', title: cpuLabel(r.cpu) }, cpuLabel(r.cpu)) },
    { key: 'mem_total', label: t('col.mem_total'), align: 'r', render: (r) => fmtBytes(r.mem_total) },
    { key: 'packages_count', label: t('inv.packages'), align: 'r', render: (r) => fmtInt(r.packages_count) },
    { key: 'updates_pending', label: t('inv.updates'), align: 'r', value: (r) => (r.updates_security || 0) * 1e6 + (r.updates_pending || 0), render: (r) => updatesCell(r.updates_pending, r.updates_security) },
    { key: 'reboot_required', label: t('inv.reboot'), value: (r) => (r.reboot_required ? 1 : 0), render: (r) => (r.reboot_required ? h('span', { class: 'badge warn' }, t('inv.reboot_yes')) : h('span', { class: 'muted small' }, t('inv.reboot_no'))) },
    { key: 'ts', label: t('inv.collected'), align: 'r', value: (r) => Number(new Date(r.ts)) || r.ts, render: (r) => h('span', { title: fmtDateTime(r.ts) }, fmtRel(r.ts)) },
  ];

  function draw() {
    const list = filtered();
    countEl.textContent = tn('inv.count', list.length, { total: fmtInt(rows.length) });
    clear(listEl);
    if (!rows.length) { listEl.appendChild(empty(t('inv.none_title'), t('inv.none_text'))); return; }
    listEl.appendChild(dataTable({
      columns, rows: list, sortKey: params.get('sort') || 'hostname', sortDir: params.get('dir') || 'asc',
      onSort: (k, d) => ctx.setParams({ sort: k, dir: d }, { silent: true }),
      onRowClick: (r) => ctx.navigate('/infrastructure/host/' + r.host_id, { tab: 'inventory' }),
      emptyText: t('inv.no_match'),
    }));
  }

  function exportCsv() {
    const list = filtered();
    if (!list.length) { toast(t('inv.export_empty'), 'info'); return; }
    const csv = toCsv([
      { label: 'hostname', value: (r) => r.hostname },
      { label: 'os', value: (r) => osLabel(r) },
      { label: 'vendor', value: (r) => r.vendor },
      { label: 'product', value: (r) => r.product },
      { label: 'serial', value: (r) => r.serial },
      { label: 'cpu', value: (r) => cpuLabel(r.cpu) },
      { label: 'mem_total_bytes', value: (r) => r.mem_total },
      { label: 'packages', value: (r) => r.packages_count },
      { label: 'updates_pending', value: (r) => r.updates_pending },
      { label: 'updates_security', value: (r) => r.updates_security },
      { label: 'reboot_required', value: (r) => (r.reboot_required ? 1 : 0) },
      { label: 'collected_at', value: (r) => (r.ts ? new Date(typeof r.ts === 'number' ? r.ts : Date.parse(r.ts)).toISOString() : '') },
    ], list);
    download('hypha-inventaire-' + stamp() + '.csv', csv, 'text/csv;charset=utf-8');
  }

  // Synthèse
  load(tilesEl, () => get('/inventory/summary', null, { signal }), (sum) => {
    sum = sum || {};
    const osList = asList(sum.os).slice().sort((a, b) => (b.count || 0) - (a.count || 0));
    const vendors = asList(sum.vendors).slice().sort((a, b) => (b.count || 0) - (a.count || 0));
    const total = osList.reduce((a, x) => a + (x.count || 0), 0);
    clear(osSlot);
    osSlot.appendChild(select([{ value: '', label: t('inv.all_os') }].concat(osList.map((o) => ({ value: o.name || '', label: [o.name, o.version].filter(Boolean).join(' ') + ' (' + fmtInt(o.count) + ')' }))).filter((o, i, a) => a.findIndex((x) => x.value === o.value) === i), os, (v) => ctx.setParams({ os: v || null }), { style: { width: 'auto', minWidth: '160px' } }));
    const bars = (list, labelOf) => {
      const box = h('div', { class: 'panel-body' });
      if (!list.length) { box.appendChild(h('div', { class: 'muted small' }, t('inv.no_summary'))); return box; }
      const max = Math.max(1, ...list.map((x) => x.count || 0));
      box.appendChild(h('div', { class: 'toplist' }, list.slice(0, 12).map((x) => h('div', { class: 'tl-row' },
        h('div', { class: 'tl-bar', title: labelOf(x) }, h('i', { style: { width: Math.max(2, (x.count || 0) / max * 100) + '%' } }), h('span', labelOf(x) || '-')),
        h('span', { class: 'num', style: { textAlign: 'right' } }, fmtInt(x.count))))));
      return box;
    };
    clear(osEl); osEl.appendChild(bars(osList, (x) => [x.name, x.version].filter(Boolean).join(' ')));
    clear(vendEl); vendEl.appendChild(bars(vendors, (x) => x.name || x.vendor || ''));
    const f = h('div', { class: 'tiles' },
      tile(t('inv.tile.hosts'), fmtInt(total || rows.length)),
      tile(t('inv.tile.updates'), fmtInt(sum.updates_pending_hosts), t('inv.tile.updates_sub'), sum.updates_pending_hosts > 0 ? 'var(--warn)' : null),
      tile(t('inv.tile.reboot'), fmtInt(sum.reboot_required_hosts), t('inv.tile.reboot_sub'), sum.reboot_required_hosts > 0 ? 'var(--warn)' : null),
      tile(t('inv.tile.os'), fmtInt(osList.length)));
    return f;
  });

  const query = { q: undefined, os: os || undefined, tag: splitTag(tagParam) ? tagParam : undefined };
  await load(listEl, async () => asList(await get('/inventory', query, { signal }), 'hosts', 'inventory'), (list) => { rows = list; draw(); return null; });
}

// Recherche d'un paquet sur tout le parc (§ 15) : nom exact, version « inférieure à » facultative.
function packageSearch(ctx) {
  const { signal, href, params } = ctx;
  const name = h('input', { class: 'input mono', value: params.get('pkg') || '', placeholder: 'openssl', style: { width: '220px' }, spellcheck: 'false' });
  const below = h('input', { class: 'input mono', value: params.get('pkg_lt') || '', placeholder: '3.5.2', style: { width: '140px' }, spellcheck: 'false' });
  const body = h('div');
  const countEl = h('span', { class: 'sub' });
  let rows = [];
  const csvBtn = h('button', { class: 'btn sm', type: 'button', disabled: true, onclick: () => {
    download('hypha-paquet-' + (name.value.trim() || 'paquet') + '-' + stamp() + '.csv', toCsv([
      { label: 'hostname', value: (r) => r.hostname }, { label: 'name', value: (r) => r.name }, { label: 'version', value: (r) => r.version }, { label: 'arch', value: (r) => r.arch },
    ], rows), 'text/csv;charset=utf-8');
  } }, icon('download', 12), t('inv.export_csv'));
  const run = async () => {
    const n = name.value.trim();
    ctx.setParams({ pkg: n || null, pkg_lt: below.value.trim() || null }, { silent: true });
    if (!n) { clear(body); body.appendChild(h('div', { class: 'state sm' }, t('inv.pkg.type_name'))); countEl.textContent = ''; csvBtn.disabled = true; return; }
    await load(body, async () => asList(await get('/inventory/packages', { name: n, version_lt: below.value.trim() || undefined }, { signal }), 'packages', 'hosts'), (list) => {
      rows = list;
      csvBtn.disabled = !list.length;
      const hosts = new Set(list.map((r) => r.host_id));
      countEl.textContent = tn('inv.pkg.hosts', hosts.size);
      return dataTable({
        columns: [
          { key: 'hostname', label: t('col.host'), render: (r) => hostLink(href, r.host_id, r.hostname) },
          { key: 'name', label: t('inv.pkg.name'), render: (r) => h('span', { class: 'mono small' }, r.name || '-') },
          { key: 'version', label: t('inv.pkg.version'), render: (r) => h('span', { class: 'mono small', style: { color: below.value.trim() ? 'var(--warn)' : null } }, r.version || '-') },
          { key: 'arch', label: t('inv.pkg.arch'), render: (r) => h('span', { class: 'mono small' }, r.arch || '-') },
        ],
        rows: list, sortKey: 'hostname', sortDir: 'asc', maxRows: 1000,
        onRowClick: (r) => ctx.navigate('/infrastructure/host/' + r.host_id, { tab: 'inventory', pq: r.name }),
        emptyText: below.value.trim() ? t('inv.pkg.none_below', { v: below.value.trim() }) : t('inv.pkg.none'),
      });
    });
  };
  for (const inp of [name, below]) inp.addEventListener('keydown', (e) => { if (e.key === 'Enter') run(); });
  const bar = h('div', { class: 'row wrap panel-body', style: { gap: '8px', borderBottom: '1px solid var(--border)' } },
    h('label', { class: 'field' }, h('span', { class: 'lbl' }, t('inv.pkg.name')), name),
    h('label', { class: 'field' }, h('span', { class: 'lbl' }, t('inv.pkg.below')), below),
    h('button', { class: 'btn primary', type: 'button', style: { alignSelf: 'flex-end' }, onclick: run }, icon('search', 13), t('action.search')),
    h('span', { class: 'xsmall muted', style: { alignSelf: 'flex-end', maxWidth: '420px' } }, t('inv.pkg.hint')));
  const el = panel(t('inv.pkg.title'), h('div', bar, body), { flush: true, actions: h('span', { class: 'row' }, countEl, csvBtn) });
  if (name.value.trim()) run(); else body.appendChild(h('div', { class: 'state sm' }, t('inv.pkg.type_name')));
  return el;
}

// ── Onglet « Inventaire » de la fiche hôte ─────────────────────────────────
export function inventoryTab(container, ctx, hostId) {
  const { signal, params } = ctx;
  clear(container);
  const body = h('div', { class: 'panel-body' });
  container.appendChild(body);
  load(body, () => get('/hosts/' + encodeURIComponent(hostId) + '/inventory', null, { signal }), (inv) => {
    inv = inv && inv.inventory && typeof inv.inventory === 'object' ? Object.assign({}, inv, inv.inventory) : (inv || {});
    const hw = inv.hardware && typeof inv.hardware === 'object' ? inv.hardware : {};
    const os = inv.os && typeof inv.os === 'object' ? inv.os : {};
    const up = inv.updates && typeof inv.updates === 'object' ? inv.updates : {};
    if (!Object.keys(hw).length && !Object.keys(os).length && !asList(inv.packages, 'items').length) return empty(t('inv.host.none_title'), t('inv.host.none_text'));
    const cpu = hw.cpu && typeof hw.cpu === 'object' ? hw.cpu : {};
    const mods = asList(hw.memory_modules);
    const disks = asList(hw.disks);
    const out = h('div', { class: 'col gap-3' });
    out.appendChild(h('div', { class: 'row small muted' }, t('inv.host.collected', { when: fmtRel(inv.ts) }), inv.ts ? h('span', { title: fmtDateTime(inv.ts) }, '(' + fmtDateTime(inv.ts) + ')') : null));
    out.appendChild(h('div', { class: 'grid c2' },
      panel(t('inv.host.hardware'), kv([
        [t('inv.vendor'), hw.vendor || null],
        [t('inv.product'), hw.product || null],
        [t('inv.serial'), hw.serial ? h('span', { class: 'mono' }, hw.serial) : null],
        [t('inv.bios'), [hw.bios_vendor, hw.bios_version, hw.bios_date].filter(Boolean).join(' · ') || null],
        [t('col.cpu'), cpuLabel(cpu)],
        [t('inv.sockets'), isNum(cpu.sockets) ? fmtInt(cpu.sockets) : null],
        [t('inv.virtual'), hw.virtual ? (hw.virtual === 'none' ? t('inv.physical') : hw.virtual) : null],
        [t('col.mem_total'), mods.length ? fmtBytes(mods.reduce((a, m) => a + (isNum(m.size) ? m.size : 0), 0)) : null],
      ])),
      panel(t('inv.host.system'), kv([
        [t('inv.os_name'), [os.name, os.version].filter(Boolean).join(' ') || null],
        [t('inv.codename'), os.codename || null],
        [t('host.meta.kernel'), os.kernel ? h('span', { class: 'mono small' }, os.kernel) : null],
        [t('inv.installed_at'), os.installed_at ? fmtDate(os.installed_at) : null],
        [t('inv.last_boot'), os.last_boot ? fmtDateTime(os.last_boot) : null],
        [t('inv.reboot'), os.reboot_required === true ? h('span', { class: 'badge warn' }, t('inv.reboot_yes')) : os.reboot_required === false ? t('inv.reboot_no') : null],
        [t('inv.timezone'), os.timezone || null],
      ]))));
    if (mods.length || disks.length) {
      out.appendChild(h('div', { class: 'grid c2' },
        panel(t('inv.host.memory_modules'), dataTable({
          columns: [
            { key: 'slot', label: t('inv.slot'), render: (m) => h('span', { class: 'mono small' }, m.slot || '-') },
            { key: 'size', label: t('inv.size'), align: 'r', render: (m) => fmtBytes(m.size) },
            { key: 'type', label: t('col.type') },
            { key: 'speed_mhz', label: t('inv.speed'), align: 'r', render: (m) => (isNum(m.speed_mhz) ? fmtInt(m.speed_mhz) + ' MHz' : '-') },
          ], rows: mods, sortKey: 'slot', sortDir: 'asc', emptyText: t('inv.host.no_modules'),
        }), { flush: true }),
        panel(t('inv.host.disks'), dataTable({
          columns: [
            { key: 'name', label: t('col.name'), render: (d) => h('span', { class: 'mono small' }, d.name || '-') },
            { key: 'model', label: t('inv.model'), tdCls: 'trunc', render: (d) => h('span', { title: d.serial ? t('inv.serial') + ' ' + d.serial : '' }, d.model || '-') },
            { key: 'size', label: t('inv.size'), align: 'r', render: (d) => fmtBytes(d.size) },
            { key: 'rotational', label: t('col.type'), value: (d) => (d.rotational ? 1 : 0), render: (d) => (d.rotational === true ? 'HDD' : d.rotational === false ? 'SSD' : '-') },
            { key: 'smart', label: 'SMART', render: (d) => (d.smart ? h('span', { class: ['badge', /pass|ok/i.test(d.smart) ? 'ok' : 'alert'] }, String(d.smart)) : h('span', { class: 'muted' }, '-')) },
          ], rows: disks, sortKey: 'name', sortDir: 'asc', emptyText: t('inv.host.no_disks'),
        }), { flush: true })));
    }
    // Mises à jour
    const upList = asList(up.list);
    out.appendChild(panel(t('inv.host.updates'), h('div',
      h('div', { class: 'row wrap panel-body', style: { gap: '16px', borderBottom: upList.length ? '1px solid var(--border)' : null } },
        h('span', h('b', { class: 'num' }, isNum(up.pending) ? fmtInt(up.pending) : '-'), ' ', t('inv.pending')),
        h('span', { style: { color: up.security > 0 ? 'var(--crit)' : null } }, h('b', { class: 'num' }, isNum(up.security) ? fmtInt(up.security) : '-'), ' ', t('inv.security')),
        !isNum(up.pending) ? h('span', { class: 'xsmall muted' }, t('inv.pending_unknown')) : null),
      upList.length ? dataTable({
        columns: [
          { key: 'security', label: '', width: '28px', value: (u) => (u.security ? 0 : 1), render: (u) => (u.security ? h('span', { class: 'badge alert', title: t('inv.security_title') }, icon('shield', 11)) : '') },
          { key: 'name', label: t('inv.pkg.name'), render: (u) => h('span', { class: 'mono small' }, u.name || '-') },
          { key: 'current', label: t('inv.current'), render: (u) => h('span', { class: 'mono small' }, u.current || '-') },
          { key: 'candidate', label: t('inv.candidate'), render: (u) => h('span', { class: 'mono small' }, u.candidate || '-') },
        ], rows: upList, sortKey: 'security', sortDir: 'asc', maxRows: 500,
      }) : null), { flush: true }));
    // Paquets cherchables (paginés par le serveur)
    out.appendChild(packagesPanel(ctx, hostId, inv, params.get('pq') || ''));
    return out;
  });
}

function packagesPanel(ctx, hostId, inv, initialQ) {
  const { signal } = ctx;
  const body = h('div');
  const countEl = h('span', { class: 'sub' });
  let q = initialQ;
  let cursor = null;
  let rows = [];
  const moreEl = h('div');
  const search = searchInput({ placeholder: t('inv.pkg.search_ph'), value: q, slash: false, onInput: debounce((v) => { q = v; fetchPage(true); }, 300) });
  search.style.width = '260px';

  const draw = () => {
    clear(body);
    body.appendChild(dataTable({
      columns: [
        { key: 'name', label: t('inv.pkg.name'), render: (p) => h('span', { class: 'mono small' }, p.name || '-') },
        { key: 'version', label: t('inv.pkg.version'), render: (p) => h('span', { class: 'mono small' }, p.version || '-') },
        { key: 'arch', label: t('inv.pkg.arch'), render: (p) => h('span', { class: 'mono small' }, p.arch || '-') },
        { key: 'source', label: t('col.source'), render: (p) => (p.source ? h('span', { class: 'badge muted' }, p.source) : '-') },
      ], rows, sortKey: 'name', sortDir: 'asc', emptyText: q ? t('inv.pkg.none') : t('inv.pkg.empty_host'),
    }));
  };

  async function fetchPage(reset) {
    if (reset) { cursor = null; rows = []; }
    clear(moreEl);
    moreEl.appendChild(h('div', { class: 'state sm' }, h('span', { class: 'spinner' })));
    try {
      const r = await get('/hosts/' + encodeURIComponent(hostId) + '/inventory', { q: q || undefined, limit: 500, cursor: cursor || undefined }, { signal });
      const pk = r && r.packages && !Array.isArray(r.packages) && typeof r.packages === 'object' ? r.packages : null;
      const list = pk ? asList(pk, 'items') : asList(r && r.packages);
      cursor = (pk && pk.next_cursor) || (r && (r.packages_next_cursor || r.next_cursor)) || null;
      rows = rows.concat(list);
      const total = (r && (r.packages_count || (r.inventory && r.inventory.packages_count))) || inv.packages_count;
      countEl.textContent = isNum(total) ? tn('inv.pkg.count_of', rows.length, { total: fmtInt(total) }) : tn('inv.pkg.count', rows.length);
      draw();
      clear(moreEl);
      if (cursor) moreEl.appendChild(h('div', { class: 'tbl-foot', style: { justifyContent: 'center' } }, h('button', { class: 'btn sm', type: 'button', onclick: () => fetchPage(false) }, t('logs.load_more'))));
    } catch (e) {
      if (isAbort(e)) return;
      clear(moreEl);
      moreEl.appendChild(routeErrorState(e, () => fetchPage(reset)));
    }
  }

  // Premier rendu : les paquets déjà livrés avec l'inventaire, sinon une requête.
  const first = asList(inv.packages && !Array.isArray(inv.packages) ? inv.packages.items : inv.packages);
  if (first.length && !q) {
    rows = first;
    cursor = (inv.packages && inv.packages.next_cursor) || inv.packages_next_cursor || inv.next_cursor || null;
    countEl.textContent = isNum(inv.packages_count) ? tn('inv.pkg.count_of', rows.length, { total: fmtInt(inv.packages_count) }) : tn('inv.pkg.count', rows.length);
    draw();
    if (cursor) moreEl.appendChild(h('div', { class: 'tbl-foot', style: { justifyContent: 'center' } }, h('button', { class: 'btn sm', type: 'button', onclick: () => fetchPage(false) }, t('logs.load_more'))));
  } else fetchPage(true);
  return panel(t('inv.host.packages'), h('div', body, moreEl), { flush: true, actions: h('span', { class: 'row' }, countEl, search) });
}

