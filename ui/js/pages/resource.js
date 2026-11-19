// Fiche d'une ressource lue par un connecteur (§ 34-35) : serveur dédié OVH, hôte ESXi, VM vSphere.
// Pas d'agent dessus : identité tenue par le fournisseur, courbes de son type, contrôles, événements.
import { h, icon } from '../dom.js';
import { t } from '../i18n.js';
import { get, asList } from '../api.js';
import { fmtRel, fmtDateTime, fmtBytes, fmtInt, toMs } from '../format.js';
import { empty, tagList, kv, panel, badgeState, checkStatusBadge } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { chartPanel, tagFilter } from './common.js';
import { eventRow } from './events.js';
import { explainButton, idValue } from '../components/explain.js';
import { load } from './kit.js';
import { downtimeButton, downtimeBanner, coversHost } from './ops-shared.js';

// Courbes par type de ressource : [libellé, requête, unité].
const CHARTS = {
  esxi_host: [['res.chart.cpu', 'avg:vsphere.host.cpu.usage', 'percent'], ['res.chart.mem', 'avg:vsphere.host.mem.usage', 'percent']],
  virtual_machine: [['res.chart.cpu_mhz', 'avg:vsphere.vm.cpu.usage', 'megahertz'], ['res.chart.mem_mb', 'avg:vsphere.vm.mem.guest', 'megabyte']],
  dedicated_server: [['res.chart.days_left', 'min:ovh.service.days_left', 'day']],
  ec2_instance: [['res.chart.cpu', 'avg:aws.ec2.cpu.usage', 'percent']],
};

export function isCloudResource(x) {
  return !!x && x.kind === 'cloud_resource';
}

export function resourceLabel(x) {
  const d = x && x.device && typeof x.device === 'object' ? x.device : {};
  return [t('res.provider.' + (d.provider || 'autre')), t('res.type.' + (d.type || 'autre'))].join(' · ');
}

export async function renderResource(root, ctx, host) {
  const { signal, range, href } = ctx;
  const id = host.id !== undefined ? host.id : ctx.args.id;
  const name = host.hostname || String(id);
  const dev = host.device && typeof host.device === 'object' ? host.device : {};
  ctx.setCrumbs([{ label: t('nav.infra'), href: href('/infrastructure') }, { label: name }]);
  const hf = '{' + tagFilter('host', name) + '}';

  const viaEl = h('span', { class: 'small text-2' });
  root.appendChild(h('div', { class: 'page-head' },
    h('span', { class: 'device-ico', title: t('infra.kind.cloud_resource') }, icon('cloud', 18)),
    h('h1', name),
    host.status ? badgeState(host.status === 'up' ? 'UP' : host.status === 'down' ? 'DOWN' : 'NODATA') : null,
    h('span', { class: 'desc' }, [resourceLabel(host), dev.display_name && dev.display_name !== name ? dev.display_name : null].filter(Boolean).join(' · ')),
    viaEl,
    h('span', { class: 'spacer' }),
    explainButton(ctx.me, { kind: 'host', host_id: idValue(id) }, { subject: name }),
    downtimeButton(ctx, { name: t('dt.host_name', { host: name }), subject: t('dt.host_subject', { host: name }), scope: { tags: { host: name } } })));

  if (host.via_host_id !== undefined && host.via_host_id !== null) {
    const link = h('a', { href: href('/infrastructure/host/' + encodeURIComponent(host.via_host_id)) }, host.via_hostname || '#' + host.via_host_id);
    viaEl.appendChild(document.createTextNode(t('res.via') + ' '));
    viaEl.appendChild(link);
    if (!host.via_hostname) get('/hosts/' + encodeURIComponent(host.via_host_id), null, { signal }).then((r) => { const x = r && r.host ? r.host : r; if (x && x.hostname) link.textContent = x.hostname; }).catch(() => {});
  }

  root.appendChild(downtimeBanner(ctx, (d) => coversHost(d, name, host.tags)));
  // § 51 : la même machine porte un agent HYPHA ; sa fiche réunit les deux vues.
  if (host.same_as && host.same_as.id !== undefined) {
    root.appendChild(h('div', { class: 'callout mb-3' }, icon('info', 16), h('span', { class: 'small' },
      t('res.same_as') + ' ', h('a', { href: href('/infrastructure/host/' + encodeURIComponent(host.same_as.id)) }, host.same_as.hostname || '#' + host.same_as.id))));
  }
  if (host.tags && Object.keys(host.tags).length) root.appendChild(h('div', { class: 'mb-3' }, tagList(host.tags, (k, v) => ctx.navigate('/infrastructure', { tag: k + ':' + v }))));

  const platform = [host.platform, host.platform_version].filter(Boolean).join(' ');
  const charts = (CHARTS[dev.type] || []).map(([label, q, unit]) => chartPanel(t(label), {
    queries: [{ id: 'a', q: q + hf }], labels: { a: t(label) }, unit, range, signal, onRange: ctx.onRange, sync: 'res-' + id, height: 120,
  }));
  root.appendChild(h('div', { class: 'grid c2 mb-3' },
    panel(t('res.identity'), kv([
      [t('res.f.provider'), t('res.provider.' + (dev.provider || 'autre'))],
      [t('res.f.type'), t('res.type.' + (dev.type || 'autre'))],
      [t('res.f.service'), dev.service ? h('span', { class: 'mono small' }, dev.service) : null],
      [t('res.f.display_name'), dev.display_name || null],
      [t('res.f.ip'), dev.ip ? h('span', { class: 'mono' }, dev.ip) : null],
      [t('res.f.region'), dev.region || null],
      [t('res.f.parent'), dev.parent || null],
      [t('res.f.power'), dev.power_state || null],
      [t('res.f.model'), [dev.vendor, dev.model].filter(Boolean).join(' ') || null],
      [t('col.os'), platform || null],
      [t('res.f.cpu'), host.cpu_cores ? fmtInt(host.cpu_cores) : null],
      [t('res.f.mem'), host.mem_total ? fmtBytes(host.mem_total) : null],
      [t('col.last_seen'), host.last_seen ? h('span', { title: fmtDateTime(host.last_seen) }, fmtRel(host.last_seen)) : null],
    ])),
    charts.length ? h('div', { class: 'col gap-3' }, charts) : h('div')));

  const checksBody = h('div');
  root.appendChild(h('div', { class: 'mb-3' }, panel(t('host.tab.checks'), checksBody, { flush: true })));
  load(checksBody, async () => asList(await get('/checks', { host_id: id }, { signal }), 'checks'), (list) => dataTable({
    columns: [
      { key: 'status', label: t('col.status'), sort: (r) => -(r.status ?? 3), render: (r) => checkStatusBadge(r.status) },
      { key: 'name', label: t('col.check'), render: (r) => h('span', { class: 'mono small' }, r.name || r.check_name || '-') },
      { key: 'message', label: t('col.message'), tdCls: 'trunc', render: (r) => h('span', { title: r.message || '' }, r.message || '-') },
      { key: 'ts', label: t('col.last_run'), align: 'r', value: (r) => toMs(r.ts), render: (r) => h('span', { title: fmtDateTime(r.ts) }, fmtRel(r.ts)) },
    ],
    rows: list, sortKey: 'status', sortDir: 'asc', emptyText: t('checks.none'),
  }));

  // Événements du fournisseur (interventions OVH, événements vSphere) qui citent la ressource.
  const evBody = h('div');
  root.appendChild(panel(t('res.events'), evBody, { flush: true }));
  const source = dev.provider === 'ovh' ? 'ovhcloud' : dev.provider || 'vsphere';
  load(evBody, async () => asList(await get('/events', { q: 'source:' + source, from: range.from, to: range.to }, { signal }), 'events'), (list) => {
    const mine = list.filter((ev) => String(ev.host_id) === String(id) || (ev.tags && (ev.tags.host === name || ev.tags.ovh_service === dev.service && dev.provider === 'ovh' || ev.tags.vsphere_vm === name || ev.tags.vsphere_host === name)));
    if (!mine.length) return empty(null, t('res.no_events'));
    return h('div', mine.slice(0, 200).map((ev) => eventRow(ev, ctx, { dateTime: true })));
  });
  return null;
}
