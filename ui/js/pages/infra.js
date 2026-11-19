// Infrastructure : liste des hôtes (tri, filtres d'étiquettes) et carte en hexagones.
import { h, clear, icon } from '../dom.js';
import { t, tn } from '../i18n.js';
import { fmtPct, fmtBytes, fmtRel, fmtInt, fmtNum, isNum } from '../format.js';
import { section, empty, tagList, searchInput, pills, dropdown, menuItem, select, statusDot } from '../components/ui.js';
import { dataTable, barCell, usageColor } from '../components/table.js';
import { hexMap, legend, MAP_METRICS } from '../components/hexmap.js';
import { loadHosts, hostLink } from './common.js';
import { parseHash } from '../router.js';
import { viewsMenu } from '../components/views.js';

// Équipement réseau interrogé en SNMP par un agent (§ 14).
export function isNetworkDevice(x) {
  return !!x && (x.kind === 'network_device' || (x.device && typeof x.device === 'object' && !!x.device.sys_object_id));
}

// Ressource lue par un connecteur (§ 34-35) : serveur OVH, hôte ESXi, VM vSphere.
export function isCloudResource(x) {
  return !!x && x.kind === 'cloud_resource';
}

function kindOf(x) {
  return isCloudResource(x) ? 'cloud_resource' : isNetworkDevice(x) ? 'network_device' : 'agent';
}

function resourcePlatform(x) {
  const d = x.device || {};
  return [t('res.provider.' + (d.provider || 'autre')), t('res.type.' + (d.type || 'autre'))].join(' · ');
}

export function platformLabel(x) {
  const p = [x.platform || x.os, x.platform_version].filter(Boolean).join(' ');
  return p || '-';
}

// § 51 : connecteurs qui voient aussi la machine (ou, pour une ressource, l'hôte de son agent).
function seenBy(x) {
  if (Array.isArray(x.seen_by) && x.seen_by.length) {
    return h('span', { class: 'muted small', title: t('infra.seen_by_title') }, ' · ' + x.seen_by.map((r) => t('res.provider.' + (r.provider || 'autre'))).join(', '));
  }
  if (x.same_as) return h('span', { class: 'muted small', title: t('res.same_as') }, ' → ' + (x.same_as.hostname || ''));
  return null;
}

function matchesText(host, q) {
  if (!q) return true;
  const needle = q.toLowerCase();
  const hay = [host.hostname, host.platform, host.os, (host.ip_addresses || []).join(' '),
    (host.seen_by || []).map((r) => [r.hostname, r.display_name, r.provider].join(' ')).join(' '),
    Object.entries(host.tags || {}).map(([k, v]) => k + ':' + v).join(' ')].join(' ').toLowerCase();
  return needle.split(/\s+/).every((w) => hay.includes(w));
}

function matchesTags(host, tags) {
  return tags.every(([k, v]) => host.tags && String(host.tags[k]) === v);
}

export async function render(root, ctx) {
  const { params, signal, href } = ctx;
  const isMap = ctx.route.view === 'map';
  ctx.setCrumbs([{ label: t('nav.infra'), href: href('/infrastructure') }, { label: t(isMap ? 'nav.infra.map' : 'nav.infra.list') }]);

  let q = params.get('q') || '';
  const tags = params.getAll('tag').map((s2) => { const i = s2.indexOf(':'); return i > 0 ? [s2.slice(0, i), s2.slice(i + 1)] : null; }).filter(Boolean);
  let status = params.get('status') || 'all';
  let metric = params.get('metric') || 'cpu';
  let groupBy = params.get('group') || '';
  let kind = ['agent', 'network_device', 'cloud_resource'].includes(params.get('kind')) ? params.get('kind') : 'all';

  const body = h('div');
  const countEl = h('span', { class: 'muted small' });
  const tagSlot = h('div', { class: 'row wrap' });
  const search = searchInput({
    placeholder: t('infra.search_ph'), value: q,
    onInput: (v) => { q = v; ctx.setParams({ q: v || null }, { silent: true }); draw(); },
  });
  search.style.flex = '1 1 280px';

  const bar = h('div', { class: 'querybar' },
    h('div', { class: 'pill-group' },
      h('button', { type: 'button', class: !isMap ? 'on' : null, onclick: () => switchView('/infrastructure') }, icon('list', 13), ' ', t('nav.infra.list')),
      h('button', { type: 'button', class: isMap ? 'on' : null, onclick: () => switchView('/infrastructure/map') }, icon('map', 13), ' ', t('nav.infra.map'))),
    search,
    pills([{ key: 'all', label: t('infra.status.all') }, { key: 'up', label: t('host.status.up') }, { key: 'down', label: t('host.status.down') }], status,
      (k) => { status = k; ctx.setParams({ status: k === 'all' ? null : k }, { silent: true }); draw(); }),
    h('label', { class: 'row small muted', title: t('infra.kind.title') }, t('infra.kind.label'),
      select([{ value: 'all', label: t('infra.kind.all') }, { value: 'agent', label: t('infra.kind.agent_pl') }, { value: 'network_device', label: t('infra.kind.network_device_pl') }, { value: 'cloud_resource', label: t('infra.kind.cloud_resource_pl') }], kind,
        (v) => { kind = v; ctx.setParams({ kind: v === 'all' ? null : v }, { silent: true }); draw(); }, { class: 'input sm', style: { width: 'auto' } })),
    tagSlot,
    countEl,
    viewsMenu({
      page: 'hosts', me: ctx.me,
      getState: () => ({ q, status, kind, tags: tags.map(([k, v]) => k + ':' + v), view: isMap ? 'map' : 'list', metric, group: groupBy }),
      apply: (st) => {
        const p = new URLSearchParams(stickyRange());
        if (st.q) p.set('q', st.q);
        if (st.status && st.status !== 'all') p.set('status', st.status);
        if (st.kind && st.kind !== 'all') p.set('kind', st.kind);
        if (st.metric) p.set('metric', st.metric);
        if (st.group) p.set('group', st.group);
        (Array.isArray(st.tags) ? st.tags : []).forEach((x) => p.append('tag', x));
        location.hash = '#' + (st.view === 'map' ? '/infrastructure/map' : '/infrastructure') + (p.toString() ? '?' + p.toString() : '');
      },
    }));
  root.appendChild(bar);
  const mapBar = h('div', { class: 'row wrap mb-3' });
  if (isMap) root.appendChild(mapBar);
  root.appendChild(h('section', { class: 'panel' }, body));

  // Plusieurs paramètres « tag » : le fragment est reconstruit à la main, filtres conservés.
  function go(path, tagList2) {
    const p = parseHash().params;
    if (tagList2) { p.delete('tag'); tagList2.forEach(([k, v]) => p.append('tag', k + ':' + v)); }
    const qs = p.toString();
    location.hash = '#' + path + (qs ? '?' + qs : '');
  }
  const switchView = (path) => go(path, null);
  const stickyRange = () => { const p = parseHash().params; const out = new URLSearchParams(); for (const k of ['t', 'from', 'to']) if (p.has(k)) out.set(k, p.get(k)); return out; };
  const setTags = (list) => go(isMap ? '/infrastructure/map' : '/infrastructure', list);

  let hosts = [];

  function renderTagPicker() {
    clear(tagSlot);
    for (const [k, v] of tags) {
      tagSlot.appendChild(h('span', { class: 'chip' }, k + ':' + v,
        h('button', { type: 'button', title: t('action.remove'), onclick: () => setTags(tags.filter((x) => !(x[0] === k && x[1] === v))) }, icon('x', 11))));
    }
    const all = new Map();
    for (const x of hosts) for (const [k, v] of Object.entries(x.tags || {})) {
      if (!all.has(k)) all.set(k, new Set());
      all.get(k).add(String(v));
    }
    if (!all.size) return;
    const btn = h('button', { class: 'btn sm', type: 'button' }, icon('filter', 12), t('infra.add_tag'));
    tagSlot.appendChild(dropdown(btn, (close) => {
      const box = h('div');
      for (const [k, vals] of Array.from(all.entries()).sort()) {
        box.appendChild(h('div', { class: 'menu-head' }, k));
        for (const v of Array.from(vals).sort().slice(0, 30)) {
          box.appendChild(menuItem(k + ':' + v, () => { close(); setTags(tags.concat([[k, v]])); }));
        }
      }
      return box;
    }, { left: true }));
  }

  // § 51 : une ressource jumelle d'un hôte à agent ne fait qu'une ligne avec lui, sauf quand la
  // liste ne montre que les ressources.
  function filteredHosts() {
    return hosts.filter((x) => (status === 'all' || x.status === status) && (kind === 'all' || kind === kindOf(x)) && !(x.same_as && kind !== 'cloud_resource') && matchesText(x, q) && matchesTags(x, tags));
  }

  function draw() {
    const list = filteredHosts();
    countEl.textContent = tn('infra.count', list.length, { total: fmtInt(hosts.filter((x) => !(x.same_as && kind !== 'cloud_resource')).length) });
    clear(body);
    if (!hosts.length) {
      body.appendChild(empty(t('infra.none_title'), t('infra.none_text'), h('a', { class: 'btn primary', href: href('/settings/install') }, icon('download', 14), t('infra.install_agent'))));
      return;
    }
    if (isMap) { drawMap(list); return; }
    body.appendChild(dataTable({
      columns: [
        { key: 'status', label: '', width: '24px', render: (r) => statusDot(r.status), sort: (r) => (r.status === 'down' ? 0 : 1) },
        { key: 'hostname', label: t('col.host'), render: (r) => h('span', { class: 'nowrap' }, h('span', { class: ['kind-ico', kindOf(r) !== 'agent' ? 'dev' : null], title: t('infra.kind.' + kindOf(r)) }, icon({ agent: 'server', network_device: 'device', cloud_resource: 'cloud' }[kindOf(r)], 13)), hostLink(href, r.id, r.hostname), seenBy(r)) },
        { key: 'platform', label: t('col.os'), value: (r) => (isCloudResource(r) ? resourcePlatform(r) : isNetworkDevice(r) ? [r.device && r.device.vendor, r.device && r.device.model].filter(Boolean).join(' ') : platformLabel(r)), render: (r) => h('span', { class: 'text-2' }, isCloudResource(r) ? resourcePlatform(r) : isNetworkDevice(r) ? ([r.device && r.device.vendor, r.device && r.device.model].filter(Boolean).join(' ') || t('infra.kind.network_device')) : platformLabel(r)) },
        { key: 'cpu', label: t('col.cpu'), align: 'r', render: (r) => barCell(r.cpu, fmtPct(r.cpu), usageColor(r.cpu)) },
        { key: 'mem_pct', label: t('col.mem'), align: 'r', render: (r) => barCell(r.mem_pct, fmtPct(r.mem_pct), usageColor(r.mem_pct)) },
        { key: 'disk_max_pct', label: t('col.disk_max'), align: 'r', render: (r) => barCell(r.disk_max_pct, fmtPct(r.disk_max_pct), usageColor(r.disk_max_pct)) },
        { key: 'load_norm', label: t('col.load_norm'), align: 'r', render: (r) => (isNum(r.load_norm) ? fmtNum(r.load_norm, 2) : '-'), title: t('col.load_norm_title') },
        { key: 'cpu_cores', label: t('col.cores'), align: 'r', render: (r) => (r.cpu_cores ? fmtInt(r.cpu_cores) : '-') },
        { key: 'mem_total', label: t('col.mem_total'), align: 'r', render: (r) => (r.mem_total ? fmtBytes(r.mem_total) : '-') },
        { key: 'ip_addresses', label: t('col.ip'), sort: (r) => (r.ip_addresses || [])[0] || '', render: (r) => h('span', { class: 'mono small' }, (r.ip_addresses || []).slice(0, 2).join(', ') || '-') },
        // Hôte sans agent : la version affichée serait celle de l'agent qui l'interroge.
        { key: 'agent_version', label: t('col.agent'), render: (r) => (kindOf(r) === 'agent' ? (r.agent_version || '-') : '-') },
        { key: 'tags', label: t('col.tags'), sort: false, render: (r) => tagList(r.tags, (k, v) => setTags(tags.concat([[k, v]]))) },
        { key: 'last_seen', label: t('col.last_seen'), align: 'r', value: (r) => Number(new Date(r.last_seen)) || r.last_seen, render: (r) => fmtRel(r.last_seen) },
      ],
      rows: list,
      sortKey: params.get('sort') || 'hostname',
      sortDir: params.get('dir') || 'asc',
      onSort: (k, d) => ctx.setParams({ sort: k, dir: d }, { silent: true }),
      onRowClick: (r) => ctx.navigate('/infrastructure/host/' + r.id),
      emptyText: t('infra.no_match'),
    }));
  }

  function drawMap(list) {
    clear(mapBar);
    const keys = Array.from(new Set(hosts.flatMap((x) => Object.keys(x.tags || {})))).sort();
    mapBar.appendChild(h('span', { class: 'muted small' }, t('map.color_by')));
    mapBar.appendChild(pills(MAP_METRICS.map((m) => ({ key: m.key, label: t(m.label) })), metric,
      (k) => { metric = k; ctx.setParams({ metric: k }, { silent: true }); draw(); }));
    mapBar.appendChild(h('span', { class: 'muted small', style: { marginLeft: '12px' } }, t('map.group_by')));
    mapBar.appendChild(select([{ value: '', label: t('map.no_group') }].concat(keys.map((k) => ({ value: k, label: k }))), groupBy,
      (v) => { groupBy = v; ctx.setParams({ group: v || null }, { silent: true }); draw(); }, { style: { width: 'auto' } }));
    mapBar.appendChild(h('span', { class: 'spacer' }));
    mapBar.appendChild(legend());
    if (!list.length) { body.appendChild(empty(null, t('infra.no_match'))); return; }
    const map = hexMap(list, { metric, groupBy, onClick: (x) => ctx.navigate('/infrastructure/host/' + x.id) });
    ctx.onCleanup(() => map.destroy());
    body.appendChild(h('div', { class: 'panel-body' }, map));
  }

  // Le texte libre filtre dans le navigateur (instantané) ; les étiquettes partent aussi au serveur.
  await section(body, () => loadHosts(signal, tags.length ? { tag: tags.map(([k, v]) => k + ':' + v) } : null), (list) => {
    hosts = list;
    renderTagPicker();
    draw();
    return null;
  });
}
