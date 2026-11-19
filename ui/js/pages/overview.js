// Vue d'ensemble : hôtes en ligne et hors ligne, alertes en cours, événements récents, hôtes les plus chargés.
import { h, clear } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, asList } from '../api.js';
import { fmtPct, fmtRel, fmtUnit, fmtInt, isNum } from '../format.js';
import { section, panel, empty, badgeState, pills } from '../components/ui.js';
import { dataTable, barCell, usageColor } from '../components/table.js';
import { chartPanel, loadHosts, hostLink } from './common.js';
import { histogramChart } from './logs-shared.js';
import { alertName, alertGroup, alertState } from './monitors-shared.js';
import { eventRow } from './events.js';
import { explainButton, idValue } from '../components/explain.js';

export async function render(root, ctx) {
  ctx.setCrumbs([{ label: t('nav.overview') }]);
  const { range, signal, href } = ctx;

  const startEl = h('div');
  const tilesEl = h('div', { class: 'tiles mb-3' });
  const alertsBody = h('div');
  const topBody = h('div');
  const eventsBody = h('div');
  const logsBody = h('div', { class: 'panel-body' });

  let topMetric = ctx.params.get('top') || 'cpu';

  root.appendChild(startEl);
  root.appendChild(tilesEl);
  root.appendChild(h('div', { class: 'grid c2 mb-3' },
    chartPanel(t('overview.cpu_fleet'), {
      queries: [{ id: 'a', q: 'avg:system.cpu.usage{*}' }, { id: 'b', q: 'max:system.cpu.usage{*}' }],
      labels: { a: t('overview.avg'), b: t('overview.max') }, unit: 'percent', range, signal, onRange: ctx.onRange, height: 170,
    }),
    h('section', { class: 'panel' }, h('div', { class: 'panel-head' }, h('h3', t('overview.logs_by_status')), h('span', { class: 'spacer' }),
      h('a', { class: 'btn sm ghost', href: href('/logs') }, t('action.open_logs'))), logsBody)));
  root.appendChild(h('div', { class: 'grid c2' },
    panel(t('overview.active_alerts'), alertsBody, { flush: true, actions: h('a', { class: 'btn sm ghost', href: href('/monitors') }, t('nav.monitors')) }),
    panel(t('overview.top_hosts'), topBody, {
      flush: true,
      actions: pills([{ key: 'cpu', label: t('map.metric.cpu') }, { key: 'mem', label: t('map.metric.mem') }, { key: 'disk', label: t('map.metric.disk') }], topMetric,
        (k) => { topMetric = k; ctx.setParams({ top: k }, { silent: true }); renderTop(); }),
    })));
  root.appendChild(h('div', { class: 'mt-3' }, panel(t('overview.recent_events'), eventsBody, { flush: true, actions: h('a', { class: 'btn sm ghost', href: href('/events') }, t('nav.events')) })));

  let hosts = [];
  let alerts = [];
  let logErrors = null;

  // Espace sans agent : la page dit par où commencer, au lieu de tuiles à zéro.
  function renderStart() {
    clear(startEl);
    if (hosts.length) return;
    const step = (n, title, text, action) => h('li', { class: 'start-step' },
      h('span', { class: 'start-num' }, String(n)),
      h('div', h('div', { class: 'start-title' }, title), h('div', { class: 'muted small' }, text), action || null));
    startEl.appendChild(h('section', { class: 'panel start mb-3' },
      h('div', { class: 'panel-head' }, h('h3', t('start.title'))),
      h('div', { class: 'panel-body' },
        h('p', { class: 'muted' }, t('start.lead')),
        h('ol', { class: 'start-steps' },
          step(1, t('start.s1'), t('start.s1_text'), h('a', { class: 'btn primary', href: href('/settings/install') }, t('start.s1_action'))),
          step(2, t('start.s2'), t('start.s2_text')),
          step(3, t('start.s3'), t('start.s3_text'), h('a', { class: 'btn', href: href('/monitors') }, t('start.s3_action')))))));
  }

  function renderTiles() {
    clear(tilesEl);
    const up = hosts.filter((x) => x.status === 'up').length;
    const down = hosts.filter((x) => x.status === 'down').length;
    const alerting = alerts.filter((a) => alertState(a) === 'ALERT').length;
    const warn = alerts.filter((a) => alertState(a) === 'WARN').length;
    const tile = (label, value, sub, color, link) => h(link ? 'a' : 'div', { class: 'tile', href: link || null },
      h('div', { class: 't-label' }, color ? h('span', { class: 'dot', style: { background: color } }) : null, label),
      h('div', { class: 't-value num' }, value), sub ? h('div', { class: 't-sub' }, sub) : null);
    tilesEl.appendChild(tile(t('overview.hosts_up'), fmtInt(up), tn('overview.hosts_total', hosts.length), 'var(--ok)', href('/infrastructure')));
    tilesEl.appendChild(tile(t('overview.hosts_down'), fmtInt(down), down ? t('overview.hosts_down_sub') : t('overview.all_reporting'), down ? 'var(--crit)' : 'var(--nodata)', href('/infrastructure', { status: 'down' })));
    tilesEl.appendChild(tile(t('overview.alerts'), fmtInt(alerting), tn('overview.warn_count', warn), alerting ? 'var(--crit)' : 'var(--ok)', href('/monitors', { state: 'ALERT' })));
    tilesEl.appendChild(tile(t('overview.log_errors'), logErrors === null ? '-' : fmtInt(logErrors), t('overview.on_range'), logErrors ? 'var(--log-error)' : 'var(--nodata)', href('/logs', { q: 'status:error' })));
  }

  function renderTop() {
    clear(topBody);
    const field = topMetric === 'mem' ? 'mem_pct' : topMetric === 'disk' ? 'disk_max_pct' : 'cpu';
    const rows = hosts.filter((x) => isNum(x[field])).sort((a, b) => b[field] - a[field]).slice(0, 10);
    if (!rows.length) { topBody.appendChild(empty(null, t('overview.no_host_values'))); return; }
    topBody.appendChild(dataTable({
      columns: [
        { key: 'hostname', label: t('col.host'), render: (r) => hostLink(href, r.id, r.hostname), sort: false },
        { key: field, label: t('map.metric.' + topMetric), align: 'r', sort: false, render: (r) => barCell(r[field], fmtPct(r[field]), usageColor(r[field])) },
        { key: 'cpu_cores', label: t('col.cores'), align: 'r', sort: false, render: (r) => fmtInt(r.cpu_cores) },
      ],
      rows,
      onRowClick: (r) => ctx.navigate('/infrastructure/host/' + r.id),
    }));
  }

  renderTiles();

  await Promise.all([
    section(topBody, () => loadHosts(signal), (list) => { hosts = list; renderStart(); renderTiles(); renderTop(); return null; }),
    section(alertsBody, async () => asList(await get('/alerts', null, { signal }), 'alerts'), (list) => {
      alerts = list;
      renderTiles();
      if (!list.length) return empty(t('overview.no_alert_title'), t('overview.no_alert_text'));
      const sorted = list.slice().sort((a, b) => ['ALERT', 'NODATA', 'WARN', 'OK'].indexOf(alertState(a)) - ['ALERT', 'NODATA', 'WARN', 'OK'].indexOf(alertState(b)));
      return h('ul', { class: 'list-plain' }, sorted.slice(0, 15).map((a) => h('li',
        badgeState(alertState(a)),
        h('a', { class: 'grow ellipsis', href: a.monitor_id !== undefined ? href('/monitors/' + a.monitor_id) : null }, alertName(a)),
        h('span', { class: 'muted small ellipsis', style: { maxWidth: '40%' }, title: alertGroup(a) }, alertGroup(a)),
        isNum(a.value) ? h('span', { class: 'num small' }, fmtUnit(a.value, a.unit || null)) : null,
        h('span', { class: 'muted xsmall nowrap' }, fmtRel(a.since || a.ts)),
        a.monitor_id !== undefined ? explainButton(ctx.me, { kind: 'alert', monitor_id: idValue(a.monitor_id), group: alertGroup(a) }, { iconOnly: true, subject: alertName(a) + ' (' + alertGroup(a) + ')' }) : null)));
    }),
    section(eventsBody, async () => asList(await get('/events', { from: range.from, to: range.to }, { signal }), 'events'), (list) => {
      if (!list.length) return empty(null, t('events.none_range'));
      const sorted = list.slice().sort((a, b) => (b.ts || 0) - (a.ts || 0)).slice(0, 12);
      return h('div', sorted.map((ev) => eventRow(ev, ctx)));
    }),
    section(logsBody, () => get('/logs/histogram', { q: '', from: range.from, to: range.to, buckets: 60 }, { signal }), (resp) => {
      let errors = 0;
      for (const b of (resp && resp.buckets) || []) {
        const c = (Array.isArray(b) && b[1]) || {};
        for (const k of ['error', 'critical', 'alert', 'emergency']) if (typeof c[k] === 'number') errors += c[k];
      }
      logErrors = errors;
      renderTiles();
      return histogramChart(resp, range, { onRange: ctx.onRange, height: 140 });
    }),
  ]);
}
