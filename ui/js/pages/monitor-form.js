// Création et édition d'un moniteur : 15 types (§§ 7, 13, 19), priorité et options (§ 17), message
// avec variables (§ 18), aperçu en direct (POST /monitors/preview), catalogue « Depuis un modèle ».
import { h, clear, icon, debounce } from '../dom.js';
import { t } from '../i18n.js';
import { get, post, put, asList, isAbort } from '../api.js';
import { fmtDuration, fmtNum, isNum } from '../format.js';
import { field, select, toast, errorMessage, errorState, loading, empty } from '../components/ui.js';
import { autocomplete } from '../components/autocomplete.js';
import { metricChart } from './common.js';
import { histogramChart } from './logs-shared.js';
import { MONITOR_TYPES } from './monitors-shared.js';
import { buildQuery, parseQuery } from './metrics.js';
import { fmtSli } from './slo.js';
import {
  CLASSIC_TYPES, ADVANCED_TYPES, THRESHOLD_TYPES, withDefaultOptions, previewMonitor, parseComposite, compositeReadable,
  optionsSection, messageEditor, openTemplateCatalog, monitorPickList,
} from './monitor-extras.js';
import { prioNum } from './ops-shared.js';

const WINDOWS = [60, 300, 600, 900, 1800, 3600, 14400, 86400];
const COMPARATORS = ['>', '>=', '<', '<='];
const RENOTIFY = [0, 600, 1800, 3600, 14400, 86400];
const NODATA = [120, 300, 600, 900, 1800, 3600];
const HISTORIES = [86400, 3 * 86400, 7 * 86400, 14 * 86400, 30 * 86400];
const HORIZONS = [3600, 6 * 3600, 12 * 3600, 86400, 2 * 86400, 7 * 86400, 14 * 86400, 30 * 86400];
const SHIFTS = [300, 3600, 86400, 7 * 86400];
const METRIC_LIKE = ['metric', 'anomaly', 'forecast', 'outlier', 'change'];

function parseTags(str) {
  const out = {};
  for (const part of String(str || '').split(',')) {
    const p = part.trim();
    if (!p) continue;
    const i = p.indexOf(':');
    if (i > 0) out[p.slice(0, i).trim()] = p.slice(i + 1).trim();
  }
  return out;
}
const tagsText = (o) => Object.entries(o || {}).map(([k, v]) => k + ':' + v).join(', ');
const listText = (a) => (Array.isArray(a) ? a.join(', ') : '');
const parseList = (s2) => String(s2 || '').split(',').map((x) => x.trim()).filter(Boolean);
const num = (v) => (v === '' || v === null || v === undefined || !Number.isFinite(Number(v)) ? null : Number(v));

function defaults(type, params) {
  const m = {
    name: '', type, query: {}, thresholds: {}, message: '', channel_ids: [], notify_no_data: false, no_data_s: 600, renotify_s: 0, tags: {}, enabled: true,
    priority: null, options: withDefaultOptions({}),
  };
  const pq = params.get('q');
  switch (type) {
    case 'metric': m.query = { q: pq || 'avg:system.cpu.usage{*} by {host}', window_s: 300, aggregation: 'avg', comparator: '>' }; m.thresholds = { critical: 90, warning: 80 }; break;
    case 'host': m.query = { tags: {} }; m.no_data_s = 300; m.notify_no_data = true; break;
    case 'service_check': m.query = { check: 'systemd.unit', tags: {}, by: ['host', 'unit'] }; break;
    case 'log': m.query = { q: pq || 'status:error', window_s: 300, comparator: '>' }; m.thresholds = { critical: 10 }; break;
    case 'synthetic': m.query = { test_id: num(params.get('test_id')), failures: 2 }; break;
    case 'apm': m.query = { service: params.get('service') || '', metric: 'error_rate', window_s: 300, comparator: '>' }; m.thresholds = { critical: 5 }; break;
    case 'slo': m.query = { slo_id: num(params.get('slo_id')), on: 'budget', window: '1h' }; m.thresholds = { critical: 10, warning: 25 }; break;
    case 'anomaly': m.query = { q: pq || 'avg:system.cpu.usage{*} by {host}', window_s: 900, algorithm: 'agile', bounds: 2, direction: 'both', seasonality: 'daily' }; m.thresholds = { critical: 50 }; break;
    case 'forecast': m.query = { q: pq || 'max:system.disk.in_use{*} by {host,mount}', history_s: 7 * 86400, horizon_s: 2 * 86400, algorithm: 'linear', comparator: '>' }; m.thresholds = { critical: 90 }; break;
    case 'outlier': m.query = { q: pq || 'avg:system.load.norm.1{*} by {host}', window_s: 900, algorithm: 'mad', tolerance: 3, pct: 20 }; break;
    case 'composite': m.query = { expression: '', warn_as_true: false }; break;
    case 'change': m.query = { q: pq || 'sum:trace.hits{*}', window_s: 300, shift_s: 3600, mode: 'pct', comparator: '>' }; m.thresholds = { critical: 50 }; break;
    case 'event': m.query = { q: params.get('events_q') || 'source:snmp type:snmp_trap', window_s: 300, by: ['host'], comparator: '>' }; m.thresholds = { critical: 0 }; break;
    case 'process': m.query = { name: params.get('process') || '', match: 'exact', tags: {}, by: ['host'], min: 1, max: null }; break;
    case 'error_tracking': m.query = { service: params.get('service') || '', new_only: true, window_s: 300, comparator: '>' }; m.thresholds = { critical: 0 }; break;
    default: break;
  }
  return m;
}

export async function render(root, ctx) {
  const { args, params, signal, href, range } = ctx;
  const editing = !!args.id;
  ctx.setCrumbs([{ label: t('nav.monitors'), href: href('/monitors') }, { label: editing ? t('monitors.edit_title') : t('monitors.new') }]);

  let m;
  if (editing) {
    root.appendChild(loading());
    try {
      const r = await get('/monitors/' + encodeURIComponent(args.id), null, { signal });
      m = r && r.monitor ? r.monitor : r;
    } catch (e) {
      if (isAbort(e)) return;
      clear(root); root.appendChild(errorState(e, () => ctx.refresh())); return;
    }
    clear(root);
  } else {
    const type = MONITOR_TYPES.includes(params.get('type')) ? params.get('type') : 'metric';
    m = defaults(type, params);
  }
  m.query = m.query && typeof m.query === 'object' ? m.query : {};
  m.thresholds = m.thresholds && typeof m.thresholds === 'object' ? m.thresholds : {};
  m.priority = prioNum(m.priority);
  m.options = withDefaultOptions(m.options);

  const channelsP = get('/channels', null, { signal }).then((r) => asList(r, 'channels')).catch(() => []);
  const monitorsP = get('/monitors', null, { signal }).then((r) => asList(r, 'monitors')).catch(() => []);

  const typeEl = h('div', { class: 'col gap-3' });
  const defEl = h('div', { class: 'form-grid' });
  const previewEl = h('div');
  const thEl = h('div', { class: 'form-grid' });
  const optEl = h('div');
  const notifEl = h('div', { class: 'col gap-3' });
  const errEl = h('div', { class: 'callout crit mt-3', hidden: true });
  const saveBtn = h('button', { class: 'btn primary', type: 'button' }, editing ? t('action.save') : t('monitors.create'));

  root.appendChild(h('div', { class: 'page-head' }, h('h1', editing ? t('monitors.edit_title') : t('monitors.new')), h('span', { class: 'spacer' }),
    !editing ? h('button', { class: 'btn', type: 'button', onclick: () => openCatalog() }, icon('layers', 14), t('tpl.button')) : null));
  const section = (n, title, ...body) => h('div', { class: 'form-section', style: n === 1 ? { borderTop: '0', marginTop: '0', paddingTop: '0' } : null }, h('h3', h('span', { class: 'step-num' }, String(n)), title), ...body);
  const form = h('section', { class: 'panel', 'data-no-autorefresh': '1' }, h('div', { class: 'panel-body' },
    section(1, t('monitors.step.type'), typeEl),
    section(2, t('monitors.step.define'), defEl, previewEl),
    section(3, t('monitors.step.thresholds'), thEl),
    section(4, t('mon.step.behaviour'), optEl),
    section(5, t('monitors.step.notify'), notifEl),
    errEl,
    h('div', { class: 'row mt-4' }, h('span', { class: 'spacer' }), h('a', { class: 'btn', href: editing ? href('/monitors/' + args.id) : href('/monitors') }, t('action.cancel')), saveBtn)));
  root.appendChild(form);

  function openCatalog() {
    openTemplateCatalog(ctx, {
      onPrefill: (mon) => {
        const base = defaults(MONITOR_TYPES.includes(mon.type) ? mon.type : 'metric', new URLSearchParams());
        m = Object.assign(base, mon, { options: withDefaultOptions(mon.options), priority: prioNum(mon.priority) });
        m.query = m.query && typeof m.query === 'object' ? m.query : {};
        m.thresholds = m.thresholds && typeof m.thresholds === 'object' ? m.thresholds : {};
        drawAll();
      },
    });
  }

  function drawTypes() {
    clear(typeEl);
    const group = (title, list) => {
      const grid = h('div', { class: 'type-pick' });
      for (const ty of list) {
        const b = h('button', { type: 'button', class: m.type === ty ? 'sel' : null, disabled: editing && m.type !== ty, title: editing && m.type !== ty ? t('monitors.type_locked') : null },
          h('b', t('monitors.type.' + ty)), h('span', t('monitors.type_desc.' + ty)));
        b.addEventListener('click', () => {
          if (m.type === ty || editing) return;
          const d = defaults(ty, new URLSearchParams());
          m.type = ty; m.query = d.query; m.thresholds = d.thresholds; m.notify_no_data = d.notify_no_data; m.no_data_s = d.no_data_s;
          drawAll();
        });
        grid.appendChild(b);
      }
      return h('div', h('div', { class: 'xsmall muted mb-2' }, title), grid);
    };
    typeEl.appendChild(group(t('mon.types.classic'), CLASSIC_TYPES));
    typeEl.appendChild(group(t('mon.types.advanced'), ADVANCED_TYPES));
  }

  const refreshPreview = debounce(() => drawPreview(), 700);

  function durSelect(key, choices, labelFn) {
    const v = m.query[key] || choices[0];
    const opts = choices.includes(v) ? choices : choices.concat([v]).sort((a, b) => a - b);
    return select(opts.map((s2) => ({ value: s2, label: labelFn ? labelFn(s2) : fmtDuration(s2 * 1000, true) })), v, (x) => { m.query[key] = Number(x); refreshPreview(); });
  }
  const windowSelect = (key = 'window_s') => durSelect(key, WINDOWS, (s2) => t('monitors.last', { d: fmtDuration(s2 * 1000, true) }));
  const comparatorSelect = () => select(COMPARATORS.map((c) => ({ value: c, label: t('cmp.' + c) })), m.query.comparator || '>', (x) => { m.query.comparator = x; refreshPreview(); });
  const choice = (key, values, prefix, onChange) => select(values.map((k) => ({ value: k, label: t(prefix + k) })), m.query[key] || values[0], (v) => { m.query[key] = v; refreshPreview(); if (onChange) onChange(v); });
  const numberIn = (key, attrs, onChange) => {
    const inp = h('input', Object.assign({ class: 'input', type: 'number', step: 'any', value: isNum(m.query[key]) ? m.query[key] : '' }, attrs || {}));
    inp.addEventListener('input', () => { m.query[key] = num(inp.value); refreshPreview(); if (onChange) onChange(); });
    return inp;
  };

  // Requête de métrique en champs (agrégat, métrique, filtres, regroupement) + texte libre.
  function metricQueryFields(byHint) {
    const q = m.query;
    const parsed = parseQuery(q.q || '') || { agg: 'avg', metric: '', filters: [], by: [], rollup: null };
    const qInput = h('input', { class: 'input mono', value: q.q || '', spellcheck: 'false' });
    const sync = () => { q.q = buildQuery(parsed); qInput.value = q.q; refreshPreview(); };
    qInput.addEventListener('input', () => { q.q = qInput.value; const p = parseQuery(q.q); if (p) Object.assign(parsed, p); refreshPreview(); });
    const metricAc = autocomplete({
      value: parsed.metric, placeholder: t('metrics.metric_ph'),
      fetcher: async (text, sig) => asList(await get('/metrics/names', { q: text }, { signal: sig }), 'names').map(String),
      onSelect: (v) => { parsed.metric = v.trim(); sync(); },
    });
    metricAc.input.addEventListener('change', () => { parsed.metric = metricAc.input.value.trim(); sync(); });
    const filters = h('input', { class: 'input mono', value: parsed.filters.join(', '), placeholder: 'env:prod, !host:web-03' });
    filters.addEventListener('change', () => { parsed.filters = parseList(filters.value); sync(); });
    const by = h('input', { class: 'input mono', value: parsed.by.join(', '), placeholder: 'host' });
    by.addEventListener('change', () => { parsed.by = parseList(by.value); sync(); });
    defEl.appendChild(field(t('monitors.f.space_agg'), select(['avg', 'sum', 'min', 'max', 'count'].map((a) => ({ value: a, label: a })), parsed.agg, (v) => { parsed.agg = v; sync(); })));
    defEl.appendChild(field(t('monitors.f.metric'), metricAc));
    defEl.appendChild(field(t('monitors.f.filters'), filters, t('monitors.f.filters_hint')));
    defEl.appendChild(field(t('monitors.f.by'), by, byHint || t('monitors.f.by_hint')));
    defEl.appendChild(h('div', { class: 'full' }, field(t('monitors.f.query'), qInput, t('monitors.f.query_hint'))));
  }

  function drawDefinition() {
    clear(defEl);
    const q = m.query;
    if (METRIC_LIKE.includes(m.type)) metricQueryFields(m.type === 'outlier' ? t('mon.f.outlier_by_hint') : null);
    if (m.type === 'metric') {
      defEl.appendChild(field(t('monitors.f.eval_agg'), select(['avg', 'max', 'min', 'sum', 'last'].map((a) => ({ value: a, label: t('agg.' + a) })), q.aggregation || 'avg', (v) => { q.aggregation = v; refreshPreview(); })));
      defEl.appendChild(field(t('monitors.f.window'), windowSelect()));
      defEl.appendChild(field(t('monitors.f.comparator'), comparatorSelect()));
    } else if (m.type === 'anomaly') {
      const algoHint = h('span', { class: 'hint' }, t('mon.anomaly.algo_hint.' + (q.algorithm || 'agile')));
      defEl.appendChild(h('label', { class: 'field' }, h('span', { class: 'lbl' }, t('mon.f.algorithm')),
        choice('algorithm', ['basic', 'agile', 'robust'], 'mon.anomaly.algo.', (v) => { algoHint.textContent = t('mon.anomaly.algo_hint.' + v); }), algoHint));
      defEl.appendChild(field(t('mon.f.bounds'), numberIn('bounds', { min: 0.5, max: 10, step: 0.5 }), t('mon.f.bounds_hint')));
      defEl.appendChild(field(t('mon.f.direction'), choice('direction', ['both', 'above', 'below'], 'mon.dir.')));
      defEl.appendChild(field(t('mon.f.seasonality'), choice('seasonality', ['hourly', 'daily', 'weekly'], 'mon.season.'), t('mon.f.seasonality_hint')));
      defEl.appendChild(field(t('monitors.f.window'), windowSelect()));
    } else if (m.type === 'forecast') {
      defEl.appendChild(field(t('mon.f.history'), durSelect('history_s', HISTORIES), t('mon.f.history_hint')));
      defEl.appendChild(field(t('mon.f.horizon'), durSelect('horizon_s', HORIZONS, (s2) => t('mon.f.horizon_v', { d: fmtDuration(s2 * 1000, true) })), t('mon.f.horizon_hint')));
      defEl.appendChild(field(t('mon.f.algorithm'), choice('algorithm', ['linear', 'seasonal'], 'mon.forecast.algo.')));
      defEl.appendChild(field(t('monitors.f.comparator'), comparatorSelect()));
    } else if (m.type === 'outlier') {
      defEl.appendChild(field(t('mon.f.algorithm'), choice('algorithm', ['mad', 'dbscan'], 'mon.outlier.algo.')));
      defEl.appendChild(field(t('mon.f.tolerance'), numberIn('tolerance', { min: 0.5, max: 10, step: 0.5 }), t('mon.f.tolerance_hint')));
      defEl.appendChild(field(t('mon.f.pct'), numberIn('pct', { min: 1, max: 100, step: 1 }), t('mon.f.pct_hint')));
      defEl.appendChild(field(t('monitors.f.window'), windowSelect()));
    } else if (m.type === 'change') {
      defEl.appendChild(field(t('monitors.f.window'), windowSelect()));
      defEl.appendChild(field(t('mon.f.shift'), durSelect('shift_s', SHIFTS, (s2) => t('mon.f.shift_v', { d: fmtDuration(s2 * 1000, true) }))));
      defEl.appendChild(field(t('mon.f.mode'), choice('mode', ['pct', 'abs'], 'mon.change.mode.', () => drawThresholds())));
      defEl.appendChild(field(t('monitors.f.comparator'), comparatorSelect()));
    } else if (m.type === 'composite') {
      drawComposite();
    } else if (m.type === 'event') {
      const eq = h('input', { class: 'input mono', value: q.q || '', spellcheck: 'false', placeholder: 'source:snmp type:snmp_trap' });
      eq.addEventListener('input', () => { q.q = eq.value; refreshPreview(); });
      const by = h('input', { class: 'input mono', value: listText(q.by), placeholder: 'host' });
      by.addEventListener('change', () => { q.by = parseList(by.value); refreshPreview(); });
      defEl.appendChild(h('div', { class: 'full' }, field(t('mon.f.event_query'), eq, t('mon.f.event_query_hint'))));
      defEl.appendChild(field(t('monitors.f.by'), by, t('mon.f.event_by_hint')));
      defEl.appendChild(field(t('monitors.f.window'), windowSelect()));
      defEl.appendChild(field(t('monitors.f.comparator'), comparatorSelect()));
    } else if (m.type === 'process') {
      const name = h('input', { class: 'input mono', value: q.name || '', placeholder: 'nginx' });
      name.addEventListener('input', () => { q.name = name.value.trim(); refreshPreview(); });
      const tags = h('input', { class: 'input mono', value: tagsText(q.tags), placeholder: 'role:web' });
      tags.addEventListener('change', () => { q.tags = parseTags(tags.value); refreshPreview(); });
      const by = h('input', { class: 'input mono', value: listText(q.by), placeholder: 'host' });
      by.addEventListener('change', () => { q.by = parseList(by.value); refreshPreview(); });
      defEl.appendChild(field(t('mon.f.process_name'), name, t('mon.f.process_name_hint')));
      defEl.appendChild(field(t('mon.f.match'), choice('match', ['exact', 'contains', 'regex'], 'mon.match.')));
      defEl.appendChild(field(t('mon.f.process_tags'), tags, t('monitors.f.host_tags_hint')));
      defEl.appendChild(field(t('monitors.f.by'), by));
      defEl.appendChild(field(t('mon.f.min'), numberIn('min', { min: 0, step: 1 }), t('mon.f.min_hint')));
      defEl.appendChild(field(t('mon.f.max'), numberIn('max', { min: 0, step: 1 }), t('mon.f.max_hint')));
    } else if (m.type === 'error_tracking') {
      const svc = h('input', { class: 'input mono', value: q.service || '', list: 'mf-services', placeholder: t('mon.f.all_services') });
      svc.addEventListener('input', () => { q.service = svc.value.trim(); refreshPreview(); });
      const dl = h('datalist', { id: 'mf-services' });
      get('/apm/services', { from: range.from, to: range.to }, { signal }).then((r) => asList(r, 'services').map((x) => x.service).filter(Boolean).sort().forEach((n) => dl.appendChild(h('option', { value: n })))).catch(() => {});
      const newOnly = h('input', { type: 'checkbox', checked: q.new_only !== false });
      newOnly.addEventListener('change', () => { q.new_only = newOnly.checked; refreshPreview(); });
      defEl.appendChild(field(t('col.service'), h('div', svc, dl), t('mon.f.error_service_hint')));
      defEl.appendChild(field(t('monitors.f.window'), windowSelect()));
      defEl.appendChild(field(t('monitors.f.comparator'), comparatorSelect()));
      defEl.appendChild(h('div', { class: 'full' }, h('label', { class: 'check' }, newOnly, t('mon.f.new_only')), h('div', { class: 'xsmall muted' }, t('mon.f.new_only_hint'))));
    } else if (m.type === 'host') {
      const tags = h('input', { class: 'input mono', value: tagsText(q.tags), placeholder: 'env:prod, role:web' });
      tags.addEventListener('change', () => { q.tags = parseTags(tags.value); refreshPreview(); });
      defEl.appendChild(h('div', { class: 'full' }, field(t('monitors.f.host_tags'), tags, t('monitors.f.host_tags_hint'))));
      defEl.appendChild(field(t('monitors.f.no_data_delay'), select(NODATA.map((s2) => ({ value: s2, label: fmtDuration(s2 * 1000, true) })), m.no_data_s || 300, (v) => { m.no_data_s = Number(v); })));
    } else if (m.type === 'service_check') {
      const listId = 'checknames';
      const check = h('input', { class: 'input mono', value: q.check || '', list: listId });
      const by = h('input', { class: 'input mono', value: listText(q.by), placeholder: 'host, unit' });
      const dl = h('datalist', { id: listId });
      get('/checks', null, { signal }).then((r) => {
        const names = Array.from(new Set(asList(r, 'checks').map((c) => c.name || c.check_name).filter(Boolean))).sort();
        names.forEach((n) => dl.appendChild(h('option', { value: n })));
      }).catch(() => {});
      check.addEventListener('change', () => {
        q.check = check.value.trim();
        const sys = { 'systemd.unit': 'unit', 'windows.service': 'windows_service', 'snmp.interface.status': 'interface' };
        const cur = (q.by || []).join(',');
        if (sys[q.check] && (['host,unit', 'host,windows_service', 'host,interface'].includes(cur) || !cur)) { q.by = ['host', sys[q.check]]; by.value = q.by.join(', '); }
        refreshPreview();
      });
      const tags = h('input', { class: 'input mono', value: tagsText(q.tags), placeholder: 'unit:nginx.service' });
      tags.addEventListener('change', () => { q.tags = parseTags(tags.value); refreshPreview(); });
      by.addEventListener('change', () => { q.by = parseList(by.value); refreshPreview(); });
      defEl.appendChild(field(t('monitors.f.check'), h('div', check, dl), t('monitors.f.check_hint')));
      defEl.appendChild(field(t('monitors.f.check_tags'), tags));
      defEl.appendChild(field(t('monitors.f.by'), by, t('monitors.f.check_by_hint')));
    } else if (m.type === 'log') {
      const lq = h('input', { class: 'input mono', value: q.q || '', spellcheck: 'false', placeholder: 'status:error service:nginx' });
      lq.addEventListener('input', () => { q.q = lq.value; refreshPreview(); });
      const by = h('input', { class: 'input mono', value: listText(q.by), placeholder: 'host, @network.client.ip' });
      by.addEventListener('change', () => { q.by = parseList(by.value).slice(0, 5); refreshPreview(); });
      defEl.appendChild(h('div', { class: 'full' }, field(t('monitors.f.log_query'), lq, t('monitors.f.log_query_hint'))));
      defEl.appendChild(field(t('monitors.f.by'), by, t('mon.f.log_by_hint')));
      defEl.appendChild(field(t('monitors.f.window'), windowSelect()));
      defEl.appendChild(field(t('monitors.f.comparator'), comparatorSelect()));
    } else if (m.type === 'synthetic') {
      const slot = h('div');
      const failures = h('input', { class: 'input', type: 'number', min: 1, max: 20, value: q.failures || 2 });
      failures.addEventListener('change', () => { q.failures = Math.max(1, Number(failures.value) || 1); refreshPreview(); });
      const locMin = h('input', { class: 'input', type: 'number', min: 1, max: 20, value: q.locations_min_failing || 1 });
      locMin.addEventListener('change', () => { q.locations_min_failing = Math.max(1, Number(locMin.value) || 1); });
      get('/synthetics', null, { signal }).then((r) => {
        const tests = asList(r, 'synthetics', 'tests');
        if (!tests.length) { slot.appendChild(h('div', { class: 'small muted' }, t('monitors.f.no_tests'), ' ', h('a', { href: href('/synthetics/new') }, t('synth.new')))); return; }
        if (!q.test_id) q.test_id = tests[0].id;
        slot.appendChild(select(tests.map((x) => ({ value: x.id, label: (x.name || '#' + x.id) + ' · ' + (x.target || '') })), q.test_id, (v) => { q.test_id = Number(v); refreshPreview(); }));
      }).catch((e) => slot.appendChild(h('div', { class: 'small', style: { color: 'var(--crit)' } }, errorMessage(e))));
      defEl.appendChild(field(t('monitors.f.test'), slot));
      defEl.appendChild(field(t('monitors.f.failures'), failures, t('monitors.f.failures_hint')));
      defEl.appendChild(field(t('mon.f.locations_min_failing'), locMin, t('mon.f.locations_min_failing_hint')));
    } else if (m.type === 'slo') {
      const slot = h('div', h('span', { class: 'spinner' }));
      get('/slos', null, { signal }).then((r) => {
        const list = asList(r, 'slos');
        clear(slot);
        if (!list.length) { slot.appendChild(h('div', { class: 'small muted' }, t('slo.none_title'), ' ', h('a', { href: href('/slos/new') }, t('slo.new')))); return; }
        if (q.slo_id === null || q.slo_id === undefined) q.slo_id = list[0].id;
        slot.appendChild(select(list.map((x) => ({ value: x.id, label: (x.name || '#' + x.id) + ' · ' + t('slo.target') + ' ' + fmtSli(x.target) })), q.slo_id, (v) => { q.slo_id = /^\d+$/.test(v) ? Number(v) : v; }));
      }).catch((e) => { clear(slot); slot.appendChild(h('div', { class: 'small', style: { color: 'var(--crit)' } }, errorMessage(e))); });
      defEl.appendChild(field(t('monitors.f.slo'), slot));
      defEl.appendChild(field(t('monitors.f.slo_on'), select(['budget', 'burn_rate'].map((k) => ({ value: k, label: t('monitors.slo_on.' + k) })), q.on || 'budget', (v) => { q.on = v; drawDefinition(); drawThresholds(); })));
      if (q.on === 'burn_rate') defEl.appendChild(field(t('monitors.f.slo_window'), select(['1h', '6h'].map((k) => ({ value: k, label: t('slo.burn_' + k) })), q.window || '1h', (v) => { q.window = v; })));
    } else if (m.type === 'apm') {
      const slot = h('div');
      const current = h('div', { class: 'hint' });
      let svcList = [];
      const showCurrent = () => {
        const s2 = svcList.find((x) => x.service === q.service);
        const v = s2 ? s2[q.metric] : null;
        current.textContent = isNum(v) ? t('monitors.f.current', { v: q.metric === 'p95_ms' ? fmtDuration(v) : fmtNum(v) }) : '';
      };
      const textInput = () => { const inp = h('input', { class: 'input mono', value: q.service || '' }); inp.addEventListener('change', () => { q.service = inp.value.trim(); refreshPreview(); }); slot.appendChild(inp); };
      get('/apm/services', { from: range.from, to: range.to }, { signal }).then((r) => {
        svcList = asList(r, 'services');
        const names = svcList.map((x) => x.service).filter(Boolean).sort();
        if (q.service && !names.includes(q.service)) names.unshift(q.service);
        if (!names.length) { textInput(); return; }
        if (!q.service) q.service = names[0];
        slot.appendChild(select(names.map((n) => ({ value: n, label: n })), q.service, (v) => { q.service = v; showCurrent(); refreshPreview(); }));
        showCurrent();
      }).catch(textInput);
      defEl.appendChild(field(t('col.service'), slot));
      defEl.appendChild(h('label', { class: 'field' }, h('span', { class: 'lbl' }, t('monitors.f.apm_metric')),
        select(['error_rate', 'p95_ms', 'hits'].map((k) => ({ value: k, label: t('monitors.apm.' + k) })), q.metric || 'error_rate', (v) => { q.metric = v; showCurrent(); refreshPreview(); }), current));
      defEl.appendChild(field(t('monitors.f.window'), windowSelect()));
      defEl.appendChild(field(t('monitors.f.comparator'), comparatorSelect()));
    }
  }

  function drawComposite() {
    const q = m.query;
    const expr = h('input', { class: 'input mono', value: q.expression || '', spellcheck: 'false', placeholder: '12 && (13 || !14)' });
    const check = h('div', { class: 'small' });
    const warnTrue = h('input', { type: 'checkbox', checked: q.warn_as_true === true });
    warnTrue.addEventListener('change', () => { q.warn_as_true = warnTrue.checked; refreshPreview(); });
    let monitors = [];
    const validate = () => {
      const r = parseComposite(expr.value);
      clear(check);
      if (!r.ok) { check.appendChild(h('span', { style: { color: 'var(--crit)' } }, icon('warn', 13), ' ', r.error)); return; }
      const byId = new Map(monitors.map((x) => [String(x.id), x]));
      const unknown = r.ids.filter((id) => !byId.has(id));
      const self = editing && r.ids.includes(String(args.id));
      check.appendChild(h('div', { class: 'col', style: { gap: '2px' } },
        h('span', { style: { color: unknown.length || self ? 'var(--crit)' : 'var(--ok)' } }, icon(unknown.length || self ? 'warn' : 'check', 13), ' ',
          self ? t('mon.comp.self') : unknown.length ? t('mon.comp.unknown', { ids: unknown.join(', ') }) : t('mon.comp.valid')),
        h('span', { class: 'text-2' }, t('mon.comp.reads'), ' ', compositeReadable(r.tree, (id) => (byId.get(id) ? byId.get(id).name : '#' + id)))));
    };
    expr.addEventListener('input', () => { q.expression = expr.value; validate(); refreshPreview(); });
    const ops = h('div', { class: 'row wrap' }, ['&&', '||', '!', '(', ')'].map((op) => h('button', { class: 'btn sm mono', type: 'button', title: t('mon.comp.op.' + ({ '&&': 'and', '||': 'or', '!': 'not', '(': 'lpar', ')': 'rpar' })[op]), onclick: () => {
      const s0 = expr.selectionStart ?? expr.value.length;
      const ins = op === '!' || op === '(' || op === ')' ? op : ' ' + op + ' ';
      expr.value = expr.value.slice(0, s0) + ins + expr.value.slice(expr.selectionEnd ?? s0);
      expr.focus(); expr.selectionStart = expr.selectionEnd = s0 + ins.length;
      expr.dispatchEvent(new Event('input'));
    } }, op)));
    const pickSlot = h('div', h('span', { class: 'spinner' }));
    defEl.appendChild(h('div', { class: 'full' }, field(t('mon.f.expression'), expr, t('mon.f.expression_hint'))));
    defEl.appendChild(h('div', { class: 'full' }, h('div', { class: 'row wrap' }, ops, h('span', { class: 'spacer' }), h('label', { class: 'check' }, warnTrue, t('mon.f.warn_as_true'))), check));
    defEl.appendChild(h('div', { class: 'full' }, field(t('mon.comp.insert'), pickSlot)));
    monitorsP.then((list) => {
      monitors = list;
      clear(pickSlot);
      pickSlot.appendChild(monitorPickList(list.filter((x) => x.type !== 'composite' || String(x.id) !== String(args.id)), {
        selfId: editing ? args.id : null,
        onPick: (x) => {
          const v = expr.value.trim();
          expr.value = v ? (/(&&|\|\||!|\()\s*$/.test(v) ? v + ' ' + x.id : v + ' && ' + x.id) : String(x.id);
          expr.dispatchEvent(new Event('input'));
        },
      }));
      validate();
    });
    validate();
  }

  let previewCtrl = null;
  function previewBody() {
    return { type: m.type, query: m.query, thresholds: m.thresholds, options: m.options };
  }
  function legacyPreview(box, sig) {
    const th = [];
    if (isNum(m.thresholds.warning)) th.push({ value: m.thresholds.warning, color: 'warn' });
    if (isNum(m.thresholds.critical)) th.push({ value: m.thresholds.critical, color: 'alert' });
    if (METRIC_LIKE.includes(m.type) && m.query.q) {
      metricChart(box, { queries: [{ id: 'a', q: m.query.q }], range, signal: sig, height: 200, thresholds: m.type === 'metric' || m.type === 'forecast' ? th : [] });
    } else if (m.type === 'log') {
      get('/logs/histogram', { q: m.query.q || '', from: range.from, to: range.to, buckets: 60 }, { signal: sig })
        .then((r) => { clear(box); box.appendChild(histogramChart(r, range, { height: 140 })); })
        .catch((e) => { if (!isAbort(e)) { clear(box); box.appendChild(errorState(e)); } });
    } else {
      box.appendChild(h('div', { class: 'callout' }, icon('info', 16), h('span', { class: 'small' }, t('mon.preview.unavailable'))));
    }
  }
  function drawPreview() {
    clear(previewEl);
    if (previewCtrl) previewCtrl.abort();
    previewCtrl = new AbortController();
    const sig = previewCtrl.signal;
    if (m.type === 'composite' && !parseComposite(m.query.expression).ok) return;
    if (m.type === 'process' && !m.query.name) return;
    const box = h('div');
    previewEl.appendChild(h('div', { class: 'mt-3' }, h('div', { class: 'chart-title' }, t('monitors.preview'), h('span', { class: 'unit' }, t('mon.preview.live'))), box));
    previewMonitor(box, previewBody(), { signal: sig, height: 200, fallback: (c) => legacyPreview(c, sig) });
  }
  signal.addEventListener('abort', () => { if (previewCtrl) previewCtrl.abort(); }, { once: true });

  function drawThresholds() {
    clear(thEl);
    if (!THRESHOLD_TYPES.includes(m.type)) {
      thEl.appendChild(h('div', { class: 'full callout' }, icon('info', 16), h('span', { class: 'small' }, t('monitors.th_none.' + m.type))));
      return;
    }
    const mk = (key, hint) => {
      const inp = h('input', { class: 'input', type: 'number', step: 'any', value: isNum(m.thresholds[key]) ? m.thresholds[key] : '' });
      inp.addEventListener('input', () => { const v = num(inp.value); if (v === null) delete m.thresholds[key]; else m.thresholds[key] = v; refreshPreview(); });
      return field(t('monitors.th.' + key), inp, hint);
    };
    if (m.type === 'slo') thEl.appendChild(h('div', { class: 'full callout' }, icon('info', 16), h('span', { class: 'small' }, t('monitors.slo_th.' + (m.query.on === 'burn_rate' ? 'burn_rate' : 'budget')))));
    const meaning = { anomaly: 'mon.th.anomaly', forecast: 'mon.th.forecast', change: m.query.mode === 'abs' ? 'mon.th.change_abs' : 'mon.th.change_pct', event: 'mon.th.event', error_tracking: 'mon.th.error_tracking' }[m.type];
    if (meaning) thEl.appendChild(h('div', { class: 'full callout' }, icon('info', 16), h('span', { class: 'small' }, t(meaning))));
    thEl.appendChild(mk('critical', t('monitors.th.critical_hint')));
    thEl.appendChild(mk('warning', t('monitors.th.optional')));
    thEl.appendChild(mk('critical_recovery', t('monitors.th.recovery_hint')));
    thEl.appendChild(mk('warning_recovery', t('monitors.th.optional')));
  }

  function drawOptions() {
    clear(optEl);
    optEl.appendChild(optionsSection(m, { monitorsP, channelsP, selfId: editing ? args.id : null }));
  }

  async function drawNotify() {
    clear(notifEl);
    const name = h('input', { class: 'input', value: m.name || '', maxlength: 200, placeholder: t('monitors.f.name_ph') });
    name.addEventListener('input', () => { m.name = name.value; });
    const tags = h('input', { class: 'input mono', value: tagsText(m.tags), placeholder: 'team:ops, service:api' });
    tags.addEventListener('change', () => { m.tags = parseTags(tags.value); });
    const chanBox = h('div', { class: 'col', style: { gap: '4px' } }, h('span', { class: 'spinner' }));
    const noData = h('input', { type: 'checkbox', checked: !!m.notify_no_data });
    noData.addEventListener('change', () => { m.notify_no_data = noData.checked; });
    const enabled = h('input', { type: 'checkbox', checked: m.enabled !== false });
    enabled.addEventListener('change', () => { m.enabled = enabled.checked; });

    notifEl.appendChild(h('div', { class: 'form-grid' },
      h('div', { class: 'full' }, field(t('monitors.f.name'), name)),
      h('div', { class: 'full' }, h('div', { class: 'field' }, h('span', { class: 'lbl small text-2', style: { fontWeight: '500' } }, t('monitors.f.message')),
        messageEditor(m, { channelsP }), h('span', { class: 'hint xsmall muted' }, t('mon.msg.hint')))),
      field(t('monitors.f.channels'), chanBox),
      h('div', { class: 'col' },
        field(t('monitors.f.renotify'), select(RENOTIFY.map((s2) => ({ value: s2, label: s2 ? t('monitors.every', { d: fmtDuration(s2 * 1000, true) }) : t('monitors.renotify_off') })), m.renotify_s || 0, (v) => { m.renotify_s = Number(v); })),
        m.type !== 'host' ? h('label', { class: 'check' }, noData, t('monitors.f.notify_no_data')) : null,
        m.type !== 'host' ? field(t('monitors.f.no_data_delay'), select(NODATA.map((s2) => ({ value: s2, label: fmtDuration(s2 * 1000, true) })), m.no_data_s || 600, (v) => { m.no_data_s = Number(v); })) : null,
        h('label', { class: 'check' }, enabled, t('monitors.f.enabled'))),
      field(t('col.tags'), tags, t('mon.f.tags_hint'))));

    const channels = await channelsP;
    clear(chanBox);
    if (!channels.length) {
      chanBox.appendChild(h('div', { class: 'small muted' }, t('monitors.f.no_channels'), ' ', h('a', { href: href('/settings/channels') }, t('nav.settings.channels'))));
    } else {
      const ids = new Set((m.channel_ids || []).map(String));
      for (const c of channels) {
        const cb = h('input', { type: 'checkbox', checked: ids.has(String(c.id)) });
        cb.addEventListener('change', () => {
          const set = new Set((m.channel_ids || []).map(String));
          if (cb.checked) set.add(String(c.id)); else set.delete(String(c.id));
          m.channel_ids = Array.from(set).map((x) => (/^\d+$/.test(x) ? Number(x) : x));
        });
        chanBox.appendChild(h('label', { class: 'check' }, cb, c.name || '#' + c.id, h('span', { class: 'badge muted' }, t('channels.kind.' + c.kind)), c.enabled === false ? h('span', { class: 'badge muted' }, t('channels.disabled')) : null));
      }
    }
  }

  function drawAll() { drawTypes(); drawDefinition(); drawThresholds(); drawPreview(); drawOptions(); drawNotify(); }

  function validate() {
    const errs = [];
    if (!String(m.name || '').trim()) errs.push(t('form.required', { field: t('monitors.f.name') }));
    const q = m.query;
    if (METRIC_LIKE.includes(m.type) && !String(q.q || '').trim()) errs.push(t('form.required', { field: t('monitors.f.query') }));
    if (m.type === 'outlier') { const p = parseQuery(q.q || ''); if (p && !p.by.length) errs.push(t('mon.err.outlier_by')); }
    if (m.type === 'service_check' && !String(q.check || '').trim()) errs.push(t('form.required', { field: t('monitors.f.check') }));
    if (m.type === 'synthetic' && !q.test_id) errs.push(t('form.required', { field: t('monitors.f.test') }));
    if (m.type === 'apm' && !String(q.service || '').trim()) errs.push(t('form.required', { field: t('col.service') }));
    if (m.type === 'slo' && (q.slo_id === null || q.slo_id === undefined)) errs.push(t('form.required', { field: t('monitors.f.slo') }));
    if (m.type === 'event' && !String(q.q || '').trim()) errs.push(t('form.required', { field: t('mon.f.event_query') }));
    if (m.type === 'process') {
      if (!String(q.name || '').trim()) errs.push(t('form.required', { field: t('mon.f.process_name') }));
      if (q.match === 'regex') { try { new RegExp(q.name); } catch (e) { errs.push(t('mon.err.bad_regex')); } }
      if (isNum(q.min) && isNum(q.max) && q.max < q.min) errs.push(t('mon.err.min_max'));
      if (!isNum(q.min) && !isNum(q.max)) errs.push(t('mon.err.min_or_max'));
    }
    if (m.type === 'composite') {
      const r = parseComposite(q.expression);
      if (!r.ok) errs.push(t('mon.err.composite', { e: r.error }));
      else if (editing && r.ids.includes(String(args.id))) errs.push(t('mon.comp.self'));
    }
    if (THRESHOLD_TYPES.includes(m.type) && !isNum(m.thresholds.critical)) errs.push(t('form.required', { field: t('monitors.th.critical') }));
    for (const e of m.options.escalations) if (!(e.channel_ids || []).length) { errs.push(t('mon.err.escalation_channels')); break; }
    return errs;
  }

  function cleanOptions(o) {
    const out = Object.assign({}, o);
    out.escalations = (o.escalations || []).map((e) => ({ after_s: Number(e.after_s) || 900, channel_ids: e.channel_ids || [], message: e.message || '' })).sort((a, b) => a.after_s - b.after_s);
    out.depends_on = (o.depends_on || []).filter((x) => x !== '' && x !== null);
    return out;
  }

  saveBtn.addEventListener('click', async () => {
    errEl.hidden = true;
    const errs = validate();
    if (errs.length) { clear(errEl); errEl.appendChild(icon('warn', 16)); errEl.appendChild(h('div', errs.map((e) => h('div', e)))); errEl.hidden = false; return; }
    const query = Object.assign({}, m.query);
    if (m.type === 'process' && !isNum(query.max)) query.max = null;
    const body = {
      name: m.name.trim(), type: m.type, query, thresholds: m.thresholds, message: m.message || '', channel_ids: m.channel_ids || [],
      notify_no_data: m.type === 'host' ? true : !!m.notify_no_data, no_data_s: m.no_data_s || 600, renotify_s: m.renotify_s || 0, tags: m.tags || {}, enabled: m.enabled !== false,
      priority: prioNum(m.priority), options: cleanOptions(m.options),
    };
    saveBtn.disabled = true;
    try {
      const r = editing ? await put('/monitors/' + args.id, body) : await post('/monitors', body);
      toast(editing ? t('monitors.saved') : t('monitors.created'), 'ok');
      const id = editing ? args.id : (r && (r.id || (r.monitor && r.monitor.id)));
      ctx.navigate(id ? '/monitors/' + id : '/monitors');
    } catch (e) {
      saveBtn.disabled = false;
      clear(errEl); errEl.appendChild(icon('warn', 16)); errEl.appendChild(h('div', errorMessage(e), e && e.detail && e.detail !== errorMessage(e) ? h('div', { class: 'mono xsmall mt-2' }, e.detail) : null)); errEl.hidden = false;
    }
  });

  drawAll();
  if (!MONITOR_TYPES.includes(m.type)) root.appendChild(empty(t('monitors.unknown_type'), m.type));
  if (!editing && params.get('catalog') === '1') {
    ctx.setParams({ catalog: null }, { silent: true });
    openCatalog();
  }
}
