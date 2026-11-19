// Moniteurs : liste (état, priorité, problèmes et acquittements, sourdine), détail (définition,
// options, groupes avec acquittement, maintenance, incident, historique, graphique).
import { h, clear, icon } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, post, del, asList, isAbort } from '../api.js';
import { fmtRel, fmtDateTime, fmtUnit, fmtDuration, isNum, toMs } from '../format.js';
import { section, empty, panel, searchInput, pills, select, badgeState, tagList, dropdown, menuItem, toast, confirmDialog, errorMessage, kv, errorState, loading } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { monitorState, isMuted, mutedUntilMs, stateRank, alertGroup, alertState, MONITOR_TYPES } from './monitors-shared.js';
import { metricChart } from './common.js';
import { histogramChart } from './logs-shared.js';
import { explainButton, idValue } from '../components/explain.js';
import { previewMonitor, withDefaultOptions, etaText } from './monitor-extras.js';
import {
  PRIORITIES, prioNum, priorityBadge, priorityLabel, liveDuration, ackedInfo, ackDialog, unackItems, downtimeButton, downtimeBanner,
  downtimeDialog, coversMonitor, incidentFromAlert, canWrite,
} from './ops-shared.js';
import { refreshProblemCount } from '../components/problem-count.js';

const FOREVER = Date.UTC(2100, 0, 1);

export function querySummary(m) {
  const q = m.query || {};
  const th = thresholdText(m);
  const win = (s2) => (s2 ? fmtDuration(s2 * 1000, true) : '');
  switch (m.type) {
    case 'metric': return (q.aggregation || 'avg') + '(' + win(q.window_s) + ') ' + (q.q || '') + ' ' + (q.comparator || '>') + ' ' + th;
    case 'host': return t('monitors.sum.host', { tags: Object.entries(q.tags || {}).map(([k, v]) => k + ':' + v).join(', ') || '*' });
    case 'service_check': return (q.check || '') + (q.by && q.by.length ? ' by ' + q.by.join(',') : '');
    case 'log': return 'count(' + (q.q || '*') + ')' + (q.by && q.by.length ? ' by ' + q.by.join(',') : '') + ' ' + (q.comparator || '>') + ' ' + th;
    case 'synthetic': return t('monitors.sum.synthetic', { id: q.test_id, n: q.failures || 1 });
    case 'apm': return (q.service || '') + ' ' + (q.metric || '') + ' ' + (q.comparator || '>') + ' ' + th;
    case 'slo': return 'SLO #' + (q.slo_id ?? '') + ' ' + (q.on === 'burn_rate' ? 'burn_rate(' + (q.window || '1h') + ') > ' : 'budget < ') + th;
    case 'anomaly': return 'anomalies(' + (q.q || '') + ', ' + (q.algorithm || 'agile') + ', ' + (q.bounds ?? 2) + ') ' + t('mon.sum.anomaly', { pct: th || '50', w: win(q.window_s) });
    case 'forecast': return 'forecast(' + (q.q || '') + ', ' + (q.algorithm || 'linear') + ') ' + (q.comparator || '>') + ' ' + th + ' ' + t('mon.sum.within', { d: win(q.horizon_s) });
    case 'outlier': return 'outliers(' + (q.q || '') + ', ' + (q.algorithm || 'mad') + ', ' + (q.tolerance ?? 3) + ') ' + t('mon.sum.outlier', { pct: q.pct ?? 20 });
    case 'composite': return q.expression || '';
    case 'change': return 'change(' + (q.q || '') + ', ' + win(q.shift_s) + ', ' + (q.mode || 'pct') + ') ' + (q.comparator || '>') + ' ' + th + (q.mode === 'abs' ? '' : ' %');
    case 'event': return 'events(' + (q.q || '*') + ')' + (q.by && q.by.length ? ' by ' + q.by.join(',') : '') + ' ' + (q.comparator || '>') + ' ' + th;
    case 'process': return t('mon.sum.process', { name: q.name || '?', min: isNum(q.min) ? q.min : '-', max: isNum(q.max) ? q.max : '∞' });
    case 'error_tracking': return 'errors(' + (q.service || '*') + (q.new_only !== false ? ', new' : '') + ') ' + (q.comparator || '>') + ' ' + th;
    default: return JSON.stringify(q);
  }
}

function thresholdText(m) {
  const th = m.thresholds || {};
  return isNum(th.critical) ? String(th.critical) : '';
}

export function muteMenu(m, onDone) {
  const muted = isMuted(m);
  const btn = h('button', { class: ['btn', 'sm', muted ? 'on' : null], type: 'button', title: muted ? t('monitors.muted_until', { d: fmtDateTime(mutedUntilMs(m)) }) : t('monitors.mute') },
    icon('mute', 13), muted ? t('monitors.muted') : t('monitors.mute'));
  const call = async (until) => {
    try {
      await post('/monitors/' + m.id + '/mute', { until });
      toast(until ? t('monitors.mute_done') : t('monitors.unmute_done'), 'ok');
      onDone();
    } catch (e) { toast(errorMessage(e), 'error'); }
  };
  return dropdown(btn, (close) => h('div',
    muted ? menuItem(t('monitors.unmute'), () => { close(); call(null); }, { icon: 'bell' }) : null,
    muted ? h('div', { class: 'menu-sep' }) : null,
    h('div', { class: 'menu-head' }, t('monitors.mute_for')),
    [['1h', 3600e3], ['4h', 4 * 3600e3], ['1d', 86400e3], ['7d', 7 * 86400e3]].map(([k, ms]) => menuItem(t('monitors.mute.' + k), () => { close(); call(Date.now() + ms); })),
    menuItem(t('monitors.mute.forever'), () => { close(); call(FOREVER); })));
}

// Problèmes en cours (GET /problems, sinon GET /alerts du serveur 0.3).
async function loadProblems(signal) {
  try { return asList(await get('/problems', null, { signal }), 'problems'); } catch (e) {
    if (isAbort(e)) throw e;
    return asList(await get('/alerts', null, { signal }), 'alerts');
  }
}

export async function render(root, ctx) {
  if (ctx.route.view === 'detail') return renderDetail(root, ctx);
  return renderList(root, ctx);
}

async function renderList(root, ctx) {
  const { params, signal, href } = ctx;
  ctx.setCrumbs([{ label: t('nav.monitors') }]);
  let q = params.get('q') || '';
  let state = params.get('state') || 'all';
  let type = params.get('type') || '';
  let prio = params.get('prio') || '';
  const writer = canWrite(ctx.me);
  const body = h('div');
  const countEl = h('span', { class: 'sub' });
  const search = searchInput({ placeholder: t('monitors.search_ph'), value: q, onInput: (v) => { q = v; ctx.setParams({ q: v || null }, { silent: true }); draw(); } });
  search.style.flex = '1 1 260px';
  const pillSlot = h('span');
  root.appendChild(h('div', { class: 'querybar' }, search, pillSlot,
    select([{ value: '', label: t('monitors.all_types') }].concat(MONITOR_TYPES.map((k) => ({ value: k, label: t('monitors.type.' + k) }))), type,
      (v) => { type = v; ctx.setParams({ type: v || null }, { silent: true }); draw(); }, { style: { width: 'auto' } }),
    select([{ value: '', label: t('prio.all') }].concat(PRIORITIES.map((n) => ({ value: String(n), label: priorityLabel(n) }))).concat([{ value: '0', label: t('prio.none_title') }]), prio,
      (v) => { prio = v; ctx.setParams({ prio: v || null }, { silent: true }); draw(); }, { style: { width: 'auto' } }),
    writer ? h('a', { class: 'btn', href: href('/monitors/new', { catalog: '1' }) }, icon('layers', 14), t('tpl.button')) : null,
    writer ? h('a', { class: 'btn primary', href: href('/monitors/new') }, icon('plus', 14), t('monitors.new')) : null));
  const summaryEl = h('div');
  root.appendChild(summaryEl);
  root.appendChild(panel(t('nav.monitors'), body, { flush: true, actions: countEl }));

  let rows = [];
  let problems = [];
  const probByMonitor = () => {
    const map = new Map();
    for (const p of problems) {
      if (alertState(p) === 'OK') continue;
      const k = String(p.monitor_id);
      const e = map.get(k) || { n: 0, acked: 0, suppressed: 0 };
      e.n++;
      if (ackedInfo(p)) e.acked++;
      if (p.suppressed) e.suppressed++;
      map.set(k, e);
    }
    return map;
  };

  function drawSummary() {
    clear(summaryEl);
    const open = problems.filter((p) => alertState(p) !== 'OK');
    if (!open.length) return;
    const unacked = open.filter((p) => !ackedInfo(p) && !p.suppressed).length;
    summaryEl.appendChild(h('a', { class: 'callout mb-3 problems-link', href: href('/problems') }, icon('problem', 16),
      h('span', { class: 'grow small' }, tn('monitors.problems_summary', open.length, { u: unacked })), h('span', { class: 'small' }, t('monitors.see_problems'), ' →')));
  }

  function drawPills() {
    clear(pillSlot);
    const counts = { ALERT: 0, WARN: 0, NODATA: 0, OK: 0 };
    rows.forEach((m) => { const s2 = monitorState(m); if (counts[s2] !== undefined) counts[s2]++; });
    pillSlot.appendChild(pills([{ key: 'all', label: t('monitors.all') + ' ' + rows.length }].concat(['ALERT', 'WARN', 'NODATA', 'OK'].map((k) => ({ key: k, label: t('state.' + k.toLowerCase()) + ' ' + counts[k] }))), state,
      (k) => { state = k; ctx.setParams({ state: k === 'all' ? null : k }, { silent: true }); drawPills(); draw(); }));
  }
  function draw() {
    const words = q.toLowerCase().split(/\s+/).filter(Boolean);
    const pb = probByMonitor();
    const list = rows.filter((m) => (state === 'all' || monitorState(m) === state) && (!type || m.type === type)
      && (!prio || String(prioNum(m.priority) || 0) === prio)
      && words.every((w) => [m.name, m.type, querySummary(m), JSON.stringify(m.tags || {})].join(' ').toLowerCase().includes(w)));
    countEl.textContent = tn('monitors.count', list.length);
    clear(body);
    if (!rows.length) {
      body.appendChild(empty(t('monitors.none_title'), t('monitors.none_text'), writer ? h('div', { class: 'row' },
        h('a', { class: 'btn', href: href('/monitors/new', { catalog: '1' }) }, icon('layers', 14), t('tpl.button')),
        h('a', { class: 'btn primary', href: href('/monitors/new') }, icon('plus', 14), t('monitors.new'))) : null));
      return;
    }
    body.appendChild(dataTable({
      columns: [
        { key: 'state', label: t('col.state'), value: (m) => stateRank(monitorState(m)) * 10 + (prioNum(m.priority) || 9), render: (m) => h('span', { class: 'row', style: { gap: '4px' } }, badgeState(monitorState(m)), isMuted(m) ? h('span', { title: t('monitors.muted_until', { d: fmtDateTime(mutedUntilMs(m)) }), class: 'muted' }, icon('mute', 13)) : null) },
        { key: 'priority', label: t('col.priority'), value: (m) => prioNum(m.priority) || 9, defaultDir: 'asc', render: (m) => priorityBadge(m.priority) },
        { key: 'name', label: t('col.name'), render: (m) => h('a', { href: href('/monitors/' + m.id) }, m.name || '#' + m.id) },
        { key: 'type', label: t('col.type'), render: (m) => t('monitors.type.' + m.type) },
        { key: 'query', label: t('col.definition'), sort: false, tdCls: 'trunc', render: (m) => h('span', { class: 'mono small', title: querySummary(m) }, querySummary(m)) },
        { key: 'problems', label: t('monitors.col.problems'), align: 'r', value: (m) => (pb.get(String(m.id)) || { n: 0 }).n, render: (m) => {
          const e = pb.get(String(m.id));
          if (!e) return h('span', { class: 'muted' }, '-');
          return h('a', { class: 'row', style: { gap: '4px', justifyContent: 'flex-end' }, href: href('/problems', { q: m.name || '' }), title: t('monitors.problems_title', { n: e.n, a: e.acked, s: e.suppressed }) },
            h('b', { class: 'num' }, String(e.n)), e.acked ? h('span', { class: 'badge ok' }, icon('ack', 11), String(e.acked)) : null, e.suppressed ? h('span', { class: 'badge info' }, icon('wrench', 11), String(e.suppressed)) : null);
        } },
        { key: 'tags', label: t('col.tags'), sort: false, render: (m) => tagList(m.tags) },
        { key: 'enabled', label: t('col.enabled'), value: (m) => (m.enabled === false ? 0 : 1), render: (m) => (m.enabled === false ? h('span', { class: 'badge muted' }, t('monitors.disabled')) : h('span', { class: 'muted small' }, t('monitors.enabled'))) },
        { key: 'updated_at', label: t('col.updated'), align: 'r', value: (m) => toMs(m.updated_at), render: (m) => fmtRel(m.updated_at) },
        { key: 'actions', label: '', sort: false, render: (m) => (writer ? muteMenu(m, () => ctx.refresh()) : null) },
      ],
      rows: list, sortKey: 'state', sortDir: 'asc',
      onRowClick: (m) => ctx.navigate('/monitors/' + m.id),
      emptyText: t('monitors.no_match'),
    }));
  }
  loadProblems(signal).then((list) => { problems = list; drawSummary(); if (rows.length) draw(); }).catch(() => { /* la liste reste utilisable */ });
  await section(body, async () => asList(await get('/monitors', null, { signal }), 'monitors'), (list) => { rows = list; drawPills(); draw(); return null; });
}

function optionsSummary(m, monitorsById, channelsById) {
  const o = withDefaultOptions(m.options);
  const items = [];
  items.push([t('mon.f.depends_on_host'), o.depends_on_host !== false ? t('mon.yes') : t('mon.no')]);
  if (o.depends_on.length) items.push([t('mon.f.depends_on'), o.depends_on.map((id) => { const x = monitorsById.get(String(id)); return x ? x.name : '#' + id; }).join(', ')]);
  items.push([t('mon.opt.flapping'), t('mon.sum.flapping', { n: o.flapping.changes, d: fmtDuration(o.flapping.window_s * 1000, true) })]);
  if (o.require_consecutive > 1 || o.recovery_consecutive > 1) items.push([t('mon.opt.tolerance'), t('mon.sum.tolerance', { a: o.require_consecutive, r: o.recovery_consecutive })]);
  if (o.evaluation_delay_s) items.push([t('mon.f.evaluation_delay'), fmtDuration(o.evaluation_delay_s * 1000, true)]);
  if (o.escalations.length) {
    items.push([t('mon.opt.escalations'), h('ol', { class: 'esc-sum' }, o.escalations.map((e) => h('li', t('mon.esc.after_v', { d: fmtDuration((e.after_s || 0) * 1000, true) }), ' → ',
      (e.channel_ids || []).map((id) => { const c = channelsById.get(String(id)); return c ? c.name : '#' + id; }).join(', ') || '-')))]);
  }
  return kv(items);
}

async function renderDetail(root, ctx) {
  const { args, signal, href, range } = ctx;
  ctx.setCrumbs([{ label: t('nav.monitors'), href: href('/monitors') }, { label: '#' + args.id }]);
  root.appendChild(loading());
  let m;
  try {
    m = await get('/monitors/' + encodeURIComponent(args.id), null, { signal });
    if (m && m.monitor) m = Object.assign({}, m.monitor, { states: m.states || m.monitor.states });
  } catch (e) {
    if (isAbort(e)) return;
    clear(root); root.appendChild(errorState(e, () => ctx.refresh())); return;
  }
  clear(root);
  const writer = canWrite(ctx.me);
  ctx.setCrumbs([{ label: t('nav.monitors'), href: href('/monitors') }, { label: m.name || '#' + m.id }]);
  const st = monitorState(m);
  root.appendChild(h('div', { class: 'page-head' },
    badgeState(st), priorityBadge(m.priority, { hideNone: true }), h('h1', m.name || '#' + m.id), h('span', { class: 'badge muted' }, t('monitors.type.' + m.type)),
    m.enabled === false ? h('span', { class: 'badge muted' }, t('monitors.disabled')) : null,
    h('span', { class: 'spacer' }),
    downtimeButton(ctx, { name: m.name || '#' + m.id, subject: m.name || '#' + m.id, scope: { monitor_ids: [idValue(m.id)] } }),
    writer ? muteMenu(m, () => ctx.refresh()) : null,
    writer ? h('a', { class: 'btn sm', href: href('/monitors/' + m.id + '/edit') }, icon('edit', 13), t('action.edit')) : null,
    writer ? h('button', {
      class: 'btn sm danger', type: 'button',
      onclick: async () => {
        if (!(await confirmDialog({ title: t('monitors.delete_title'), text: t('monitors.delete_text', { name: m.name || '' }), confirmLabel: t('action.delete'), danger: true }))) return;
        try { await del('/monitors/' + m.id); toast(t('monitors.deleted'), 'ok'); ctx.navigate('/monitors'); } catch (e) { toast(errorMessage(e), 'error'); }
      },
    }, icon('trash', 13), t('action.delete')) : null));
  root.appendChild(downtimeBanner(ctx, (d) => coversMonitor(d, m.id)));

  const th = m.thresholds || {};
  const q = m.query || {};
  const chartEl = h('div', { class: 'panel-body' });
  const groupsEl = h('div');
  const histEl = h('div');

  const thresholds = [];
  if (isNum(th.warning)) thresholds.push({ value: th.warning, color: 'warn' });
  if (isNum(th.critical)) thresholds.push({ value: th.critical, color: 'alert' });
  if (['anomaly', 'forecast', 'outlier', 'change'].includes(m.type) && q.q) {
    root.appendChild(h('section', { class: 'panel mb-3' }, h('div', { class: 'panel-head' }, h('h3', t('mon.detail.live_chart')), h('span', { class: 'sub mono' }, q.q)), chartEl));
    previewMonitor(chartEl, { type: m.type, query: q, thresholds: th, options: m.options || {} }, {
      signal, height: 220,
      fallback: (c) => metricChart(c, { queries: [{ id: 'a', q: q.q }], range, signal, onRange: ctx.onRange, height: 220, thresholds: m.type === 'forecast' ? thresholds : [] }),
    });
  } else if (m.type === 'metric' && q.q) {
    root.appendChild(h('section', { class: 'panel mb-3' }, h('div', { class: 'panel-head' }, h('h3', t('monitors.chart')), h('span', { class: 'sub mono' }, q.q)), chartEl));
    metricChart(chartEl, { queries: [{ id: 'a', q: q.q }], range, signal, onRange: ctx.onRange, height: 220, thresholds });
  } else if (m.type === 'log' && q.q !== undefined) {
    root.appendChild(h('section', { class: 'panel mb-3' }, h('div', { class: 'panel-head' }, h('h3', t('monitors.chart_logs')), h('span', { class: 'sub mono' }, q.q || '*')), chartEl));
    section(chartEl, () => get('/logs/histogram', { q: q.q || '', from: range.from, to: range.to, buckets: 60 }, { signal }), (resp) => histogramChart(resp, range, { onRange: ctx.onRange, height: 160 }));
  }

  const [channels, monitorsList, problems] = await Promise.all([
    get('/channels', null, { signal }).then((r) => asList(r, 'channels')).catch(() => []),
    m.options && Array.isArray(m.options.depends_on) && m.options.depends_on.length ? get('/monitors', null, { signal }).then((r) => asList(r, 'monitors')).catch(() => []) : Promise.resolve([]),
    loadProblems(signal).catch(() => []),
  ]);
  if (!root.isConnected) return;
  const channelsById = new Map(channels.map((c) => [String(c.id), c]));
  const monitorsById = new Map(monitorsList.map((x) => [String(x.id), x]));
  const chanNames = (m.channel_ids || []).map((id) => { const c = channelsById.get(String(id)); return c ? c.name : '#' + id; });

  root.appendChild(h('div', { class: 'grid c2 mb-3' },
    panel(t('monitors.definition'), kv([
      [t('col.type'), t('monitors.type.' + m.type)],
      [t('col.priority'), priorityLabel(m.priority)],
      [t('col.definition'), h('span', { class: 'mono small' }, querySummary(m))],
      [t('monitors.f.thresholds'), [isNum(th.critical) ? t('monitors.th.critical') + ' ' + th.critical : null, isNum(th.warning) ? t('monitors.th.warning') + ' ' + th.warning : null,
        isNum(th.critical_recovery) ? t('monitors.th.critical_recovery') + ' ' + th.critical_recovery : null].filter(Boolean).join(' · ') || null],
      [t('monitors.f.no_data'), m.notify_no_data ? t('monitors.no_data_after', { d: fmtDuration((m.no_data_s || 0) * 1000, true) }) : t('monitors.no_data_off')],
      [t('monitors.f.renotify'), m.renotify_s ? fmtDuration(m.renotify_s * 1000, true) : t('monitors.renotify_off')],
      [t('monitors.f.channels'), chanNames.length ? chanNames.join(', ') : null],
      [t('col.tags'), tagList(m.tags)],
      [t('monitors.muted'), isMuted(m) ? t('monitors.muted_until', { d: fmtDateTime(mutedUntilMs(m)) }) : t('monitors.not_muted')],
    ])),
    h('div', { class: 'split-v' },
      panel(t('mon.detail.behaviour'), optionsSummary(m, monitorsById, channelsById)),
      panel(t('monitors.f.message'), m.message ? h('pre', { class: 'mono small', style: { whiteSpace: 'pre-wrap', margin: '0' } }, m.message) : h('div', { class: 'muted small' }, t('monitors.no_message'))))));

  root.appendChild(h('div', { class: 'grid c2' },
    panel(t('monitors.groups'), groupsEl, { flush: true }),
    panel(t('monitors.history'), histEl, { flush: true })));

  // Groupes : état du moniteur, complété par la vue Problèmes (acquittement, suppression, escalade).
  const probs = new Map(problems.filter((p) => String(p.monitor_id) === String(m.id)).map((p) => [String(p.group || '*'), p]));
  const states = (Array.isArray(m.states) ? m.states : []).map((s2) => Object.assign({}, probs.get(alertGroup(s2)) || {}, s2, {
    acked: s2.acked !== undefined ? s2.acked : (probs.get(alertGroup(s2)) || {}).acked,
  }));
  const afterChange = () => { refreshProblemCount(); ctx.refresh(); };
  const flagsOf = (r) => {
    const out = [];
    const a = ackedInfo(r) || (r.acked_at ? { by: r.acked_by_name || r.acked_by || '', at: r.acked_at, comment: r.ack_comment || '' } : null);
    if (a) out.push(h('span', { class: 'badge ok', title: [t('ack.by', { who: a.by || '?', d: fmtDateTime(a.at) }), a.comment].filter(Boolean).join('\n') }, icon('ack', 12), t('ack.acked')));
    if (r.suppressed) out.push(h('span', { class: 'badge info', title: t('problems.sup_title.' + (['downtime', 'dependency', 'flapping'].includes(r.suppressed) ? r.suppressed : 'other')) }, icon('wrench', 12), t('problems.sup_label.' + (['downtime', 'dependency', 'flapping'].includes(r.suppressed) ? r.suppressed : 'other'))));
    if (r.flapping) out.push(h('span', { class: 'badge warn' }, t('problems.flapping')));
    if (isNum(r.escalation_step) && r.escalation_step > 0) out.push(h('span', { class: 'badge alert' }, t('problems.escalation', { n: r.escalation_step })));
    if (r.incident_id) out.push(h('a', { class: 'badge info', href: href('/incidents/' + r.incident_id) }, icon('siren', 12), t('inc.short')));
    return out.length ? h('div', { class: 'tags' }, out) : null;
  };
  groupsEl.appendChild(dataTable({
    columns: [
      { key: 'state', label: t('col.state'), value: (r) => stateRank(alertState(r)), render: (r) => badgeState(alertState(r)) },
      { key: 'group_key', label: t('col.group'), value: alertGroup, render: (r) => h('div', { class: 'col', style: { gap: '2px' } }, h('span', { class: 'mono small' }, alertGroup(r)), flagsOf(r)) },
      { key: 'value', label: t('col.value'), align: 'r', render: (r) => (isNum(r.value) ? (m.type === 'forecast' ? etaText(r.value) : fmtUnit(r.value, r.unit || null)) : '-') },
      { key: 'since', label: t('col.since'), align: 'r', value: (r) => toMs(r.since), render: (r) => (alertState(r) !== 'OK' && toMs(r.since) ? liveDuration(r.since) : h('span', { title: fmtDateTime(r.since) }, fmtRel(r.since))) },
      { key: 'act', label: '', sort: false, render: (r) => {
        if (alertState(r) === 'OK') return null;
        const item = { monitor_id: m.id, group: alertGroup(r), monitor_name: m.name };
        const acked = !!(ackedInfo(r) || r.acked_at);
        return h('span', { class: 'row', style: { gap: '2px', justifyContent: 'flex-end' } },
          writer && !acked ? h('button', { class: 'btn sm', type: 'button', title: t('ack.do_title'), onclick: () => ackDialog([item], afterChange) }, icon('check', 13), t('ack.do')) : null,
          writer && acked ? h('button', { class: 'btn sm ghost', type: 'button', onclick: () => unackItems([item], afterChange) }, t('ack.remove')) : null,
          explainButton(ctx.me, { kind: 'alert', monitor_id: idValue(m.id), group: alertGroup(r) }, { iconOnly: true, subject: (m.name || '#' + m.id) + ' (' + alertGroup(r) + ')' }),
          writer ? dropdown(h('button', { class: 'btn sm ghost icon', type: 'button', title: t('problems.actions'), 'aria-label': t('problems.actions') }, icon('chevronDown', 13)), (close) => h('div',
            menuItem(t('problems.dt_group'), () => { close(); downtimeDialog(ctx, { name: (m.name || '') + ' · ' + alertGroup(r), subject: (m.name || '') + ' (' + alertGroup(r) + ')', scope: { monitor_ids: [idValue(m.id)], groups: [alertGroup(r)] }, onDone: afterChange }); }, { icon: 'wrench' }),
            r.incident_id ? menuItem(t('inc.open'), () => { close(); ctx.navigate('/incidents/' + r.incident_id); }, { icon: 'siren' })
              : menuItem(t('inc.from_alert'), () => { close(); incidentFromAlert(ctx, Object.assign({}, item, { incident_id: r.incident_id })); }, { icon: 'siren' }))) : null);
      } },
    ],
    rows: states, sortKey: 'state', sortDir: 'asc', emptyText: t('monitors.no_groups'),
  }));

  section(histEl, async () => asList(await get('/monitors/' + m.id + '/history', null, { signal }), 'history', 'events'), (list) => dataTable({
    columns: [
      { key: 'ts', label: t('col.date'), value: (r) => toMs(r.ts), render: (r) => h('span', { class: 'num' }, fmtDateTime(r.ts)) },
      { key: 'group_key', label: t('col.group'), value: alertGroup, render: (r) => h('span', { class: 'mono small' }, alertGroup(r)) },
      { key: 'transition', label: t('col.transition'), sort: false, render: (r) => h('span', { class: 'row', style: { gap: '4px' } }, r.from_state ? badgeState(r.from_state) : h('span', { class: 'muted' }, '-'), icon('chevronRight', 12), badgeState(r.to_state || r.state),
        r.suppressed ? h('span', { class: 'badge info', title: t('problems.sup_title.' + (['downtime', 'dependency', 'flapping'].includes(r.suppressed) ? r.suppressed : 'other')) }, icon('wrench', 11)) : null) },
      { key: 'value', label: t('col.value'), align: 'r', render: (r) => (isNum(r.value) ? fmtUnit(r.value, null) : '-') },
      { key: 'message', label: t('col.message'), tdCls: 'trunc', render: (r) => h('span', { title: r.message || '' }, r.message || '-') },
    ],
    rows: list, sortKey: 'ts', sortDir: 'desc', maxRows: 500, emptyText: t('monitors.no_history'),
  }));
}
