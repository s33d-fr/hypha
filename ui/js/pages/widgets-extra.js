// Widgets de la 0.4.0 (§ 30) : requêtes avec formules et variables à valeurs multiples, comparaison avec la
// période précédente, repères d'événements, et les nouveaux types (heatmap, distribution, table, change,
// scatter, pie, event_timeline, alert_graph, alert_value, service_map, network_map, problems, slo_list,
// iframe, image, group).
import { h, s, clear, cssVar } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, asList, queryMetrics } from '../api.js';
import { fmtUnit, fmtPct, fmtDuration, fmtDateTime, fmtTime, fmtRel, isNum, toMs } from '../format.js';
import { empty, badgeState } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { timeChart, linesFromMetrics, summarize, colorAt, groupLabel } from '../components/chart.js';
import { fmtSli, sloBadge, budgetBar, sloStatus } from './slo.js';
import { monitorState } from './monitors-shared.js';

const IDS = 'abcdefgh'.split('');

export const V04_TYPES = ['heatmap', 'distribution', 'table', 'change', 'scatter', 'pie', 'event_timeline', 'alert_graph', 'alert_value', 'service_map', 'network_map', 'problems', 'slo_list', 'iframe', 'image', 'group'];

export const V04_DEFAULTS = {
  heatmap: { w: 6, h: 4, queries: [{ q: 'avg:system.cpu.usage{$host} by {host}' }], options: { unit: 'percent', buckets: 12 } },
  distribution: { w: 4, h: 4, queries: [{ q: 'avg:system.cpu.usage{$host} by {host}' }], options: { unit: 'percent', bins: 20 } },
  table: { w: 6, h: 4, queries: [{ q: 'avg:system.cpu.usage{$host} by {host}' }, { q: 'avg:system.mem.pct_used{$host} by {host}' }], options: { aggs: ['avg', 'avg'], aliases: ['CPU', 'Mémoire'], unit: 'percent', precision: 1 } },
  change: { w: 4, h: 3, queries: [{ q: 'sum:trace.hits{*} by {service}' }], options: { shift: 'period', mode: 'pct', increase_good: true } },
  scatter: { w: 6, h: 4, queries: [{ q: 'avg:system.cpu.usage{$host} by {host}' }, { q: 'avg:system.mem.pct_used{$host} by {host}' }], options: { x_label: 'CPU', y_label: 'Mémoire', x_unit: 'percent', y_unit: 'percent' } },
  pie: { w: 4, h: 4, queries: [{ q: 'sum:system.disk.used{$host} by {host}' }], options: { agg: 'last', unit: 'byte' } },
  event_timeline: { w: 12, h: 2, queries: [], options: { events_q: 'source:deploy' } },
  alert_graph: { w: 6, h: 4, queries: [], options: { monitor_id: null } },
  alert_value: { w: 3, h: 2, queries: [], options: { monitor_id: null } },
  service_map: { w: 6, h: 5, queries: [], options: {} },
  network_map: { w: 6, h: 5, queries: [], options: { tag: '' } },
  problems: { w: 6, h: 4, queries: [], options: { priority: '', acked: 'false', limit: 15 } },
  slo_list: { w: 6, h: 4, queries: [], options: { q: '' } },
  iframe: { w: 6, h: 5, queries: [], options: { url: '/status/' } },
  image: { w: 3, h: 3, queries: [], options: { url: '', fit: 'contain', alt: '' } },
  group: { w: 12, h: 5, queries: [], options: { widgets: [], color: '' } },
};

// ── Variables à valeurs multiples ─────────────────────────────────────────
function varUsed(str, name) {
  return new RegExp('\\$' + name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '(?![\\w.])').test(String(str || ''));
}

// Combinaisons de valeurs uniques pour les variables multiples utilisées (10 au plus).
export function varCombos(strings, vars, values) {
  let combos = [Object.assign({}, values)];
  for (const v of vars || []) {
    const val = values[v.name];
    if (!Array.isArray(val)) continue;
    const list = val.filter((x) => x !== '' && x !== null && x !== undefined);
    if (!strings.some((str) => varUsed(str, v.name))) { combos.forEach((c) => { c[v.name] = list.length === 1 ? list[0] : '*'; }); continue; }
    if (!list.length || list.includes('*')) { combos.forEach((c) => { c[v.name] = '*'; }); continue; }
    const next = [];
    for (const c of combos) for (const x of list) next.push(Object.assign({}, c, { [v.name]: x }));
    combos = next.slice(0, 10);
  }
  return combos;
}

// $var.value → valeur(s) de la variable (titres, notes).
export function valueRefs(str, vars, values) {
  let out = String(str || '');
  for (const v of vars || []) {
    if (!v || !v.name) continue;
    const val = values[v.name] !== undefined ? values[v.name] : v.default;
    const shown = Array.isArray(val) ? (val.length ? val.join(', ') : '*') : (val === undefined || val === null ? '*' : String(val));
    out = out.replace(new RegExp('\\$' + v.name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '\\.value\\b', 'g'), shown);
  }
  return out;
}

export function widgetFormulas(w) {
  return asList((w.options || {}).formulas).filter((f) => f && String(f.formula || '').trim())
    .map((f, i) => ({ id: 'f' + (i + 1), formula: String(f.formula).trim(), alias: f.alias || undefined }));
}

// Exécute les requêtes d'un widget : variables (une requête par valeur choisie), formules, route publique.
// opts : {range, maxPoints, keepQueries}
export async function runQueries(w, env, opts = {}) {
  const queries = (w.queries || []).filter((q) => q && q.q);
  const ids = queries.map((q, i) => IDS[i]);
  const formulas = widgetFormulas(w);
  const combos = varCombos(queries.map((q) => q.q), env.vars, env.values);
  const allQ = [];
  const allF = [];
  const back = new Map();
  combos.forEach((vals, k) => {
    const suffix = combos.length > 1 ? 'x' + k : '';
    queries.forEach((q, i) => { const id = ids[i] + suffix; back.set(id, ids[i]); allQ.push({ id, q: env.applyVars(q.q, env.vars, vals) }); });
    formulas.forEach((f) => { const id = f.id + suffix; back.set(id, f.id); allF.push({ id, formula: suffix ? f.formula.replace(/\b([a-h])\b/g, '$1' + suffix) : f.formula, alias: f.alias }); });
  });
  const resp = await queryMetrics(allQ, opts.range || env.range, opts.maxPoints || 300, env.signal, { formulas: allF, path: env.publicPath });
  let series = ((resp && resp.series) || []).map((sr) => Object.assign({}, sr, { query: back.get(sr.query) || sr.query }));
  if (formulas.length && !opts.keepQueries && !(w.options || {}).show_queries) series = series.filter((sr) => !ids.includes(sr.query));
  const names = {};
  queries.forEach((q, i) => { names[ids[i]] = (w.options && Array.isArray(w.options.aliases) && w.options.aliases[i]) || null; });
  formulas.forEach((f) => { names[f.id] = f.alias || f.formula; });
  return Object.assign({}, resp, { series, names });
}

// Repères d'événements (options.events_q), mis en cache pour tout le tableau.
export async function overlayEvents(w, env) {
  const q = (w.options || {}).events_q;
  if (!q || env.publicPath) return [];
  env.eventsCache = env.eventsCache || new Map();
  const key = q + '|' + env.range.from + '|' + env.range.to;
  if (!env.eventsCache.has(key)) {
    env.eventsCache.set(key, get('/events', { q: env.applyVarsLogs ? env.applyVarsLogs(q, env.vars, env.values) : q, from: env.range.from, to: env.range.to }, { signal: env.signal })
      .then((r) => asList(r, 'events').map((ev) => ({ ts: toMs(ev.ts), title: ev.title || ev.source || '', color: ev.alert_type === 'error' ? '--crit' : ev.alert_type === 'warning' ? '--warn' : '--s7' })))
      .catch(() => []));
  }
  return env.eventsCache.get(key);
}

// Période précédente : même requête décalée d'une durée de période, points ramenés sur la période courante.
export async function previousPeriod(w, env, maxPoints) {
  const span = env.range.to - env.range.from;
  const resp = await runQueries(w, env, { range: { from: env.range.from - span, to: env.range.to - span }, maxPoints });
  return Object.assign({}, resp, { series: resp.series.map((sr) => Object.assign({}, sr, { points: (sr.points || []).map((p) => [p[0] + span, p[1]]) })) });
}

function aggOf(values, agg) {
  const sm = summarize(values);
  if (agg === 'last') return sm.last;
  if (agg === 'max') return sm.max;
  if (agg === 'min') return sm.min;
  if (agg === 'sum') return sm.count ? sm.avg * sm.count : null;
  return sm.avg;
}

function seriesLabel(sr, names) {
  const g = groupLabel(sr.group);
  const n = (names && names[sr.query]) || sr.metric || sr.query || '';
  return g || n;
}

// ── Rendus ────────────────────────────────────────────────────────────────
const PRIO_CLS = { 1: 'p1', 2: 'p2', 3: 'p3', 4: 'p4', 5: 'p5' };

export async function renderV04Widget(body, w, env, renderChild) {
  const o = w.options || {};
  const publicOnly = !!env.publicPath;
  const nonMetric = ['event_timeline', 'alert_graph', 'alert_value', 'service_map', 'network_map', 'problems', 'slo_list', 'iframe'];
  if (publicOnly && nonMetric.includes(w.type)) { body.appendChild(empty(null, t('share.widget_private'))); return; }
  switch (w.type) {
    case 'heatmap': return heatmap(body, w, env);
    case 'distribution': return distribution(body, w, env);
    case 'table': return tableWidget(body, w, env);
    case 'change': return changeWidget(body, w, env);
    case 'scatter': return scatter(body, w, env);
    case 'pie': return pie(body, w, env);
    case 'event_timeline': return eventTimeline(body, w, env);
    case 'alert_graph': return alertGraph(body, w, env);
    case 'alert_value': return alertValue(body, w, env);
    case 'service_map': return mapWidget(body, w, env, 'service');
    case 'network_map': return mapWidget(body, w, env, 'network');
    case 'problems': return problems(body, w, env);
    case 'slo_list': return sloList(body, w, env);
    case 'iframe': return iframe(body, o);
    case 'image': return image(body, o);
    case 'group': return group(body, w, env, renderChild);
    default: body.appendChild(empty(null, t('widget.unknown_type', { type: w.type })));
  }
  return undefined;
}

function noQuery(body, w) {
  if ((w.queries || []).some((q) => q && q.q)) return false;
  body.appendChild(empty(null, t('widget.no_query')));
  return true;
}

async function heatmap(body, w, env) {
  if (noQuery(body, w)) return;
  const o = w.options || {};
  const resp = await runQueries(w, env, { maxPoints: 120 });
  const { xs, lines } = linesFromMetrics(resp, { names: resp.names });
  clear(body);
  const vals = lines.flatMap((l) => l.values.filter(isNum));
  if (!xs.length || !vals.length) { body.appendChild(empty(null, t('chart.no_data'))); return; }
  const unit = o.unit || (lines[0] && lines[0].unit) || null;
  const B = Math.max(4, Math.min(40, Number(o.buckets) || 12));
  let lo = Math.min(...vals); let hi = Math.max(...vals);
  if (hi === lo) hi = lo + 1;
  const grid = xs.map((_, j) => {
    const col = new Array(B).fill(0);
    for (const l of lines) { const v = l.values[j]; if (isNum(v)) col[Math.min(B - 1, Math.floor((v - lo) / (hi - lo) * B))]++; }
    return col;
  });
  const maxC = Math.max(1, ...grid.flat());
  const W = Math.max(200, body.clientWidth || 600);
  const H = Math.max(80, env.height - 22);
  const left = 54;
  const cw = (W - left) / xs.length;
  const ch = H / B;
  const svg = s('svg', { class: 'hm', width: W, height: H + 18, viewBox: '0 0 ' + W + ' ' + (H + 18) });
  const tip = h('div', { class: 'chart-tip map-tip', hidden: true });
  document.body.appendChild(tip);
  env.ctx.onCleanup(() => tip.remove());
  grid.forEach((col, j) => col.forEach((c, b) => {
    if (!c) return;
    const r = s('rect', { x: (left + j * cw).toFixed(1), y: (H - (b + 1) * ch).toFixed(1), width: Math.max(1, cw - 0.5).toFixed(1), height: Math.max(1, ch - 0.5).toFixed(1), class: 'hm-cell', style: { fillOpacity: (0.15 + 0.85 * c / maxC).toFixed(2) } });
    r.addEventListener('mouseenter', (e) => {
      clear(tip);
      tip.appendChild(h('div', { class: 'tt' }, fmtDateTime(xs[j])));
      tip.appendChild(h('div', { class: 'tr' }, h('span', { class: 'l' }, fmtUnit(lo + b * (hi - lo) / B, unit) + ' → ' + fmtUnit(lo + (b + 1) * (hi - lo) / B, unit)), h('span', { class: 'v' }, tn('widget.hm.series', c))));
      tip.hidden = false;
      tip.style.left = Math.min(window.innerWidth - 220, e.clientX + 12) + 'px';
      tip.style.top = (e.clientY + 12) + 'px';
    });
    r.addEventListener('mouseleave', () => { tip.hidden = true; });
    svg.appendChild(r);
  }));
  svg.appendChild(s('text', { x: left - 6, y: 10, class: 'hm-axis', 'text-anchor': 'end' }, fmtUnit(hi, unit)));
  svg.appendChild(s('text', { x: left - 6, y: H, class: 'hm-axis', 'text-anchor': 'end' }, fmtUnit(lo, unit)));
  svg.appendChild(s('text', { x: left, y: H + 14, class: 'hm-axis' }, fmtTime(xs[0])));
  svg.appendChild(s('text', { x: W - 2, y: H + 14, class: 'hm-axis', 'text-anchor': 'end' }, fmtTime(xs[xs.length - 1])));
  body.appendChild(svg);
}

async function distribution(body, w, env) {
  if (noQuery(body, w)) return;
  const o = w.options || {};
  const resp = await runQueries(w, env, { maxPoints: 200 });
  const vals = [];
  let unit = o.unit || null;
  for (const sr of resp.series || []) { unit = unit || sr.unit; for (const p of sr.points || []) if (isNum(p[1])) vals.push(p[1]); }
  clear(body);
  if (!vals.length) { body.appendChild(empty(null, t('chart.no_data'))); return; }
  vals.sort((a, b) => a - b);
  const q = (p) => vals[Math.min(vals.length - 1, Math.floor(p * vals.length))];
  const B = Math.max(5, Math.min(60, Number(o.bins) || 20));
  const lo = vals[0]; let hi = vals[vals.length - 1];
  if (hi === lo) hi = lo + 1;
  const bins = new Array(B).fill(0);
  for (const v of vals) bins[Math.min(B - 1, Math.floor((v - lo) / (hi - lo) * B))]++;
  const maxB = Math.max(1, ...bins);
  const W = Math.max(200, body.clientWidth || 400);
  const H = Math.max(60, env.height - 40);
  const svg = s('svg', { class: 'dist', width: W, height: H + 16, viewBox: '0 0 ' + W + ' ' + (H + 16) });
  const bw = W / B;
  bins.forEach((c, i) => {
    if (!c) return;
    const bh = c / maxB * (H - 4);
    const r = s('rect', { x: (i * bw + 0.5).toFixed(1), y: (H - bh).toFixed(1), width: Math.max(1, bw - 1).toFixed(1), height: bh.toFixed(1), class: 'dist-bar' });
    r.appendChild(s('title', fmtUnit(lo + i * (hi - lo) / B, unit) + ' → ' + fmtUnit(lo + (i + 1) * (hi - lo) / B, unit) + ' : ' + c));
    svg.appendChild(r);
  });
  for (const [p, label] of [[0.5, 'p50'], [0.95, 'p95']]) {
    const x = ((q(p) - lo) / (hi - lo)) * W;
    svg.appendChild(s('line', { x1: x, x2: x, y1: 0, y2: H, class: 'dist-mark' }));
    svg.appendChild(s('text', { x: Math.min(W - 30, x + 3), y: 10, class: 'hm-axis' }, label));
  }
  svg.appendChild(s('text', { x: 0, y: H + 13, class: 'hm-axis' }, fmtUnit(lo, unit)));
  svg.appendChild(s('text', { x: W, y: H + 13, class: 'hm-axis', 'text-anchor': 'end' }, fmtUnit(hi, unit)));
  body.appendChild(svg);
  body.appendChild(h('div', { class: 'xsmall muted row', style: { gap: '10px' } }, tn('widget.dist.points', vals.length), 'p50 ' + fmtUnit(q(0.5), unit), 'p95 ' + fmtUnit(q(0.95), unit), 'max ' + fmtUnit(vals[vals.length - 1], unit)));
}

async function tableWidget(body, w, env) {
  if (noQuery(body, w)) return;
  const o = w.options || {};
  const resp = await runQueries(w, env, { maxPoints: 60 });
  const cols = [];
  const rows = new Map();
  const queryIds = Array.from(new Set((resp.series || []).map((sr) => sr.query)));
  queryIds.forEach((qid) => {
    const i = IDS.indexOf(qid);
    const label = (resp.names && resp.names[qid]) || (i >= 0 && w.queries[i] ? (/:([^{\s]+)\{/.exec(w.queries[i].q) || [])[1] : qid) || qid;
    const agg = i >= 0 && Array.isArray(o.aggs) ? o.aggs[i] || 'avg' : 'avg';
    cols.push({ qid, label, agg });
  });
  for (const sr of resp.series || []) {
    const key = groupLabel(sr.group) || '*';
    if (!rows.has(key)) rows.set(key, { group: key });
    const col = cols.find((c) => c.qid === sr.query);
    rows.get(key)[sr.query] = aggOf((sr.points || []).map((p) => p[1]), col ? col.agg : 'avg');
    rows.get(key)['u_' + sr.query] = o.unit || sr.unit;
  }
  clear(body);
  body.classList.add('scroll');
  const list = Array.from(rows.values());
  if (!list.length) { body.appendChild(empty(null, t('chart.no_data'))); return; }
  body.appendChild(dataTable({
    columns: [{ key: 'group', label: t('col.group'), render: (r) => h('span', { class: 'mono small' }, r.group) }].concat(cols.map((c) => ({
      key: c.qid, label: c.label, align: 'r', title: t('widget.table.agg', { agg: t('agg.' + c.agg) }),
      render: (r) => { const v = r[c.qid]; const col = thColor(v, o.thresholds); return h('span', { style: { color: col } }, fmtUnit(v, r['u_' + c.qid], o.precision)); },
    }))),
    rows: list, sortKey: cols[0] ? cols[0].qid : 'group', sortDir: 'desc', maxRows: 200,
  }));
}

function thColor(v, thresholds) {
  if (!isNum(v) || !Array.isArray(thresholds) || !thresholds.length) return null;
  let color = null;
  for (const th of thresholds.slice().sort((a, b) => a.value - b.value)) if (isNum(th.value) && v >= th.value) color = th.color;
  return !color ? null : color === 'warn' ? 'var(--warn)' : color === 'ok' ? 'var(--ok)' : color === 'info' ? 'var(--accent)' : 'var(--crit)';
}

const SHIFTS = { hour: 3600e3, day: 86400e3, week: 7 * 86400e3 };

async function changeWidget(body, w, env) {
  if (noQuery(body, w)) return;
  const o = w.options || {};
  const span = env.range.to - env.range.from;
  const shift = SHIFTS[o.shift] || span;
  const [cur, prev] = await Promise.all([
    runQueries(w, env, { maxPoints: 60 }),
    runQueries(w, env, { maxPoints: 60, range: { from: env.range.from - shift, to: env.range.to - shift } }),
  ]);
  const agg = o.agg || 'avg';
  const prevBy = new Map((prev.series || []).map((sr) => [sr.query + '|' + groupLabel(sr.group), aggOf((sr.points || []).map((p) => p[1]), agg)]));
  const rows = (cur.series || []).map((sr) => {
    const now = aggOf((sr.points || []).map((p) => p[1]), agg);
    const before = prevBy.get(sr.query + '|' + groupLabel(sr.group));
    const delta = isNum(now) && isNum(before) ? (o.mode === 'abs' ? now - before : before !== 0 ? (now - before) / Math.abs(before) * 100 : null) : null;
    return { label: seriesLabel(sr, cur.names) || '*', now, before, delta, unit: o.unit || sr.unit };
  }).sort((a, b) => Math.abs(b.delta || 0) - Math.abs(a.delta || 0));
  clear(body);
  body.classList.add('scroll');
  if (!rows.length) { body.appendChild(empty(null, t('chart.no_data'))); return; }
  body.appendChild(h('div', { class: 'xsmall muted mb-2' }, t('widget.change.vs', { what: o.shift && SHIFTS[o.shift] ? t('widget.change.shift.' + o.shift) : t('widget.change.shift.period') })));
  body.appendChild(h('div', { class: 'change-list' }, rows.slice(0, 30).map((r) => {
    const up = isNum(r.delta) && r.delta > 0;
    const good = o.increase_good === false ? !up : up;
    const color = !isNum(r.delta) || r.delta === 0 ? 'var(--muted)' : good ? 'var(--ok)' : 'var(--crit)';
    return h('div', { class: 'change-row' },
      h('span', { class: 'ellipsis mono small', title: r.label }, r.label),
      h('span', { class: 'num small muted', title: t('widget.change.before') }, fmtUnit(r.before, r.unit, o.precision)),
      h('span', { class: 'num' }, fmtUnit(r.now, r.unit, o.precision)),
      h('span', { class: 'num change-delta', style: { color } }, isNum(r.delta) ? (r.delta > 0 ? '▲ ' : r.delta < 0 ? '▼ ' : '') + (o.mode === 'abs' ? fmtUnit(Math.abs(r.delta), r.unit, o.precision) : fmtPct(Math.abs(r.delta), 1)) : '-'));
  })));
}

async function scatter(body, w, env) {
  const qs = (w.queries || []).filter((q) => q && q.q);
  if (qs.length < 2) { body.appendChild(empty(null, t('widget.scatter.need_two'))); return; }
  const o = w.options || {};
  const resp = await runQueries(Object.assign({}, w, { queries: qs.slice(0, 2), options: Object.assign({}, o, { formulas: [] }) }), env, { maxPoints: 60 });
  const agg = o.agg || 'avg';
  const xs = new Map(); const ys = new Map();
  for (const sr of resp.series || []) {
    const key = groupLabel(sr.group) || '*';
    const v = aggOf((sr.points || []).map((p) => p[1]), agg);
    if (sr.query === 'a') xs.set(key, v); else if (sr.query === 'b') ys.set(key, v);
  }
  const pts = Array.from(xs.keys()).filter((k) => isNum(xs.get(k)) && isNum(ys.get(k))).map((k) => ({ k, x: xs.get(k), y: ys.get(k) }));
  clear(body);
  if (!pts.length) { body.appendChild(empty(null, t('widget.scatter.no_pairs'))); return; }
  const W = Math.max(200, body.clientWidth || 400);
  const H = Math.max(80, env.height - 10);
  const pad = { l: 56, b: 22, t: 6, r: 8 };
  const minX = Math.min(0, ...pts.map((p) => p.x)); const maxX = Math.max(...pts.map((p) => p.x)) || 1;
  const minY = Math.min(0, ...pts.map((p) => p.y)); const maxY = Math.max(...pts.map((p) => p.y)) || 1;
  const px = (v) => pad.l + (v - minX) / ((maxX - minX) || 1) * (W - pad.l - pad.r);
  const py = (v) => H - pad.b - (v - minY) / ((maxY - minY) || 1) * (H - pad.b - pad.t);
  const svg = s('svg', { class: 'scatter', width: W, height: H, viewBox: '0 0 ' + W + ' ' + H });
  svg.appendChild(s('line', { x1: pad.l, y1: H - pad.b, x2: W - pad.r, y2: H - pad.b, class: 'sc-axis' }));
  svg.appendChild(s('line', { x1: pad.l, y1: pad.t, x2: pad.l, y2: H - pad.b, class: 'sc-axis' }));
  svg.appendChild(s('text', { x: pad.l, y: H - 6, class: 'hm-axis' }, fmtUnit(minX, o.x_unit)));
  svg.appendChild(s('text', { x: W - pad.r, y: H - 6, class: 'hm-axis', 'text-anchor': 'end' }, (o.x_label ? o.x_label + ' ' : '') + fmtUnit(maxX, o.x_unit)));
  svg.appendChild(s('text', { x: pad.l - 4, y: H - pad.b, class: 'hm-axis', 'text-anchor': 'end' }, fmtUnit(minY, o.y_unit)));
  svg.appendChild(s('text', { x: pad.l - 4, y: pad.t + 9, class: 'hm-axis', 'text-anchor': 'end' }, fmtUnit(maxY, o.y_unit)));
  if (o.y_label) svg.appendChild(s('text', { x: pad.l + 4, y: pad.t + 9, class: 'hm-axis' }, o.y_label));
  pts.forEach((p, i) => {
    const c = s('circle', { cx: px(p.x).toFixed(1), cy: py(p.y).toFixed(1), r: 4.5, class: 'sc-dot', style: { fill: colorAt(i % 8) } });
    c.appendChild(s('title', p.k + ' · ' + (o.x_label || 'x') + ' ' + fmtUnit(p.x, o.x_unit) + ' · ' + (o.y_label || 'y') + ' ' + fmtUnit(p.y, o.y_unit)));
    svg.appendChild(c);
  });
  body.appendChild(svg);
}

async function pie(body, w, env) {
  if (noQuery(body, w)) return;
  const o = w.options || {};
  const resp = await runQueries(w, env, { maxPoints: 60 });
  let parts = (resp.series || []).map((sr) => ({ label: seriesLabel(sr, resp.names) || '*', v: aggOf((sr.points || []).map((p) => p[1]), o.agg || 'last'), unit: o.unit || sr.unit })).filter((p) => isNum(p.v) && p.v > 0).sort((a, b) => b.v - a.v);
  clear(body);
  if (!parts.length) { body.appendChild(empty(null, t('chart.no_data'))); return; }
  if (parts.length > 8) { const rest = parts.slice(7); parts = parts.slice(0, 7).concat([{ label: t('widget.pie.others', { n: rest.length }), v: rest.reduce((a, p) => a + p.v, 0), unit: rest[0].unit, other: true }]); }
  const total = parts.reduce((a, p) => a + p.v, 0);
  const size = Math.max(80, Math.min(env.height - 8, (body.clientWidth || 300) * 0.5));
  const r = size / 2; const ri = r * 0.58;
  const svg = s('svg', { width: size, height: size, viewBox: '0 0 ' + size + ' ' + size, class: 'pie' });
  let a0 = -Math.PI / 2;
  parts.forEach((p, i) => {
    const a1 = a0 + (p.v / total) * Math.PI * 2;
    const large = a1 - a0 > Math.PI ? 1 : 0;
    const pt = (a, rr) => (r + rr * Math.cos(a)).toFixed(2) + ',' + (r + rr * Math.sin(a)).toFixed(2);
    const d = parts.length === 1 ? 'M' + pt(0, r) + 'A' + r + ',' + r + ' 0 1 1 ' + pt(Math.PI, r) + 'A' + r + ',' + r + ' 0 1 1 ' + pt(0, r) + 'M' + pt(0, ri) + 'A' + ri + ',' + ri + ' 0 1 0 ' + pt(Math.PI, ri) + 'A' + ri + ',' + ri + ' 0 1 0 ' + pt(0, ri) + 'Z'
      : 'M' + pt(a0, r) + 'A' + r + ',' + r + ' 0 ' + large + ' 1 ' + pt(a1, r) + 'L' + pt(a1, ri) + 'A' + ri + ',' + ri + ' 0 ' + large + ' 0 ' + pt(a0, ri) + 'Z';
    const el = s('path', { d, class: 'pie-part', style: { fill: p.other ? cssVar('--s-other') : colorAt(i), fillRule: 'evenodd' } });
    el.appendChild(s('title', p.label + ' : ' + fmtUnit(p.v, p.unit) + ' (' + fmtPct(p.v / total * 100, 1) + ')'));
    svg.appendChild(el);
    a0 = a1;
  });
  svg.appendChild(s('text', { x: r, y: r + 4, class: 'pie-total', 'text-anchor': 'middle' }, fmtUnit(total, parts[0].unit)));
  body.appendChild(h('div', { class: 'pie-wrap' }, svg, h('div', { class: 'pie-legend' }, parts.map((p, i) => h('div', { class: 'row', style: { gap: '6px' } },
    h('span', { class: 'sw', style: { background: p.other ? 'var(--s-other)' : colorAt(i) } }), h('span', { class: 'ellipsis grow small', title: p.label }, p.label),
    h('span', { class: 'num small muted' }, fmtPct(p.v / total * 100, 1)))))));
}

async function eventTimeline(body, w, env) {
  const o = w.options || {};
  const q = env.applyVarsLogs ? env.applyVarsLogs(o.events_q || '', env.vars, env.values) : (o.events_q || '');
  const list = asList(await get('/events', { q, from: env.range.from, to: env.range.to }, { signal: env.signal }), 'events').map((ev) => Object.assign({}, ev, { ms: toMs(ev.ts) })).filter((ev) => ev.ms !== null).sort((a, b) => a.ms - b.ms);
  clear(body);
  body.classList.add('scroll');
  const span = env.range.to - env.range.from;
  const lane = h('div', { class: 'ev-lane' });
  for (const ev of list) {
    const type = ['error', 'warning', 'success', 'info'].includes(ev.alert_type) ? ev.alert_type : 'info';
    lane.appendChild(h('span', { class: ['ev-tick', 'ev-' + type], style: { left: ((ev.ms - env.range.from) / span * 100).toFixed(2) + '%' }, title: fmtDateTime(ev.ms) + ' · ' + (ev.title || '') }));
  }
  body.appendChild(lane);
  body.appendChild(h('div', { class: 'row xsmall muted', style: { justifyContent: 'space-between' } }, h('span', fmtDateTime(env.range.from)), h('span', tn('widget.events.count', list.length)), h('span', fmtDateTime(env.range.to))));
  if (!list.length) return;
  body.appendChild(h('ul', { class: 'list-plain' }, list.slice(-30).reverse().map((ev) => h('li', h('span', { class: ['dot', ev.alert_type === 'error' ? 'alert' : ev.alert_type === 'warning' ? 'warn' : ev.alert_type === 'success' ? 'ok' : 'nodata'] }),
    h('span', { class: 'num xsmall muted', style: { width: '120px', flex: '0 0 120px' } }, fmtDateTime(ev.ms)), h('span', { class: 'ellipsis small' }, ev.title || ev.source || '-')))));
}

async function loadMonitor(w, env) {
  const id = (w.options || {}).monitor_id;
  if (id === null || id === undefined || id === '') return null;
  const r = await get('/monitors/' + encodeURIComponent(id), null, { signal: env.signal });
  return r && r.monitor && typeof r.monitor === 'object' ? Object.assign({}, r, r.monitor) : r;
}

function monitorThresholds(m) {
  const th = m.thresholds && typeof m.thresholds === 'object' ? m.thresholds : m;
  return [
    isNum(th.critical) ? { value: th.critical, color: 'alert' } : null,
    isNum(th.warning) ? { value: th.warning, color: 'warn' } : null,
  ].filter(Boolean);
}

async function alertGraph(body, w, env) {
  const m = await loadMonitor(w, env);
  clear(body);
  if (!m) { body.appendChild(empty(null, t('widget.alert.pick'))); return; }
  const q = m.query && typeof m.query === 'object' ? m.query.q : null;
  const st = monitorState(m);
  body.appendChild(h('div', { class: 'row xsmall', style: { gap: '6px', marginBottom: '4px' } }, badgeState(st), h('a', { class: 'ellipsis', href: env.ctx.href('/monitors/' + m.id) }, m.name || '#' + m.id)));
  if (!q) { body.appendChild(empty(null, t('widget.alert.no_series', { type: m.type || '' }))); return; }
  const resp = await queryMetrics([{ id: 'a', q: env.applyVars(q, env.vars, env.values) }], env.range, 300, env.signal);
  const { xs, lines } = linesFromMetrics(resp, {});
  lines.forEach((l, i) => { l.color = colorAt(i); });
  const evs = await overlayEvents(w, env);
  body.appendChild(timeChart({ xs, lines, unit: (lines[0] && lines[0].unit) || null, height: Math.max(60, env.height - 28), xRange: [env.range.from, env.range.to], onRange: env.ctx.onRange, thresholds: monitorThresholds(m), legend: lines.length > 1 && lines.length < 8, events: evs, sync: 'dash' }));
}

async function alertValue(body, w, env) {
  const m = await loadMonitor(w, env);
  clear(body);
  if (!m) { body.appendChild(empty(null, t('widget.alert.pick'))); return; }
  const st = monitorState(m);
  const groups = asList(m.states || m.groups);
  const rank = { ALERT: 0, WARN: 1, NODATA: 2, OK: 3 };
  const worst = groups.slice().sort((a, b) => (rank[String(a.state || 'OK').toUpperCase()] ?? 4) - (rank[String(b.state || 'OK').toUpperCase()] ?? 4))[0];
  const value = worst && isNum(worst.value) ? worst.value : isNum(m.value) ? m.value : null;
  const color = st === 'ALERT' ? 'var(--crit)' : st === 'WARN' ? 'var(--warn)' : st === 'OK' ? 'var(--ok)' : 'var(--nodata)';
  body.appendChild(h('div', { class: 'qv' },
    h('div', { class: 'v', style: { color } }, isNum(value) ? fmtUnit(value, (w.options || {}).unit || null, (w.options || {}).precision) : t('state.' + st.toLowerCase())),
    h('div', { class: 'u row', style: { gap: '6px' } }, badgeState(st), h('a', { href: env.ctx.href('/monitors/' + m.id), class: 'ellipsis' }, m.name || '#' + m.id)),
    groups.length > 1 ? h('div', { class: 'xsmall muted' }, tn('widget.alert.groups', groups.length)) : null));
}

async function mapWidget(body, w, env, kind) {
  const mod = await import('./services.js');
  const o = w.options || {};
  const data = kind === 'service'
    ? await get('/apm/service-map', { from: env.range.from, to: env.range.to }, { signal: env.signal })
    : await get('/network/map', { from: env.range.from, to: env.range.to, tag: o.tag || undefined }, { signal: env.signal });
  clear(body);
  const height = Math.max(120, env.height - 4);
  const g = kind === 'service'
    ? mod.serviceGraph(asList(data && data.nodes), asList(data && data.edges), env.ctx, { height, compact: true })
    : mod.networkGraph(data, env.ctx, { height, compact: true });
  env.ctx.onCleanup(() => g.destroy());
  body.appendChild(g);
}

async function problems(body, w, env) {
  const o = w.options || {};
  const list = asList(await get('/problems', { priority: o.priority || undefined, acked: o.acked === 'all' ? undefined : o.acked || undefined }, { signal: env.signal }), 'problems');
  clear(body);
  body.classList.add('scroll');
  if (!list.length) { body.appendChild(h('div', { class: 'qv' }, h('div', { class: 'v', style: { color: 'var(--ok)', fontSize: '28px' } }, '✓'), h('div', { class: 'u' }, t('widget.problems.none')))); return; }
  body.appendChild(h('ul', { class: 'list-plain' }, list.slice(0, Number(o.limit) || 15).map((p) => h('li',
    h('span', { class: ['prio', PRIO_CLS[p.priority] || 'p0'] }, isNum(p.priority) ? 'P' + p.priority : '–'),
    badgeState(String(p.state || 'ALERT').toUpperCase()),
    h('a', { class: 'ellipsis grow', href: env.ctx.href('/monitors/' + p.monitor_id), title: p.group || '' }, (p.monitor_name || '#' + p.monitor_id) + (p.group && p.group !== '*' ? ' · ' + p.group : '')),
    p.acked ? h('span', { class: 'badge muted', title: (p.acked.by || '') + ' ' + (p.acked.comment || '') }, t('widget.problems.acked')) : null,
    h('span', { class: 'xsmall muted num' }, isNum(p.duration_s) ? fmtDuration(p.duration_s * 1000, true) : fmtRel(p.since))))));
  if (list.length > (Number(o.limit) || 15)) body.appendChild(h('div', { class: 'xsmall muted mt-2' }, tn('widget.problems.more', list.length - (Number(o.limit) || 15))));
}

async function sloList(body, w, env) {
  const o = w.options || {};
  let list = asList(await get('/slos', null, { signal: env.signal }), 'slos');
  if (o.q) list = list.filter((x) => String(x.name || '').toLowerCase().includes(String(o.q).toLowerCase()));
  clear(body);
  body.classList.add('scroll');
  body.appendChild(dataTable({
    columns: [
      { key: 'name', label: t('col.name'), render: (x) => h('a', { href: env.ctx.href('/slos/' + x.id) }, x.name || '#' + x.id) },
      { key: 'sli', label: 'SLI', align: 'r', value: (x) => sloStatus(x).sli, render: (x) => fmtSli(sloStatus(x).sli) },
      { key: 'target', label: t('widget.slo.target'), align: 'r', render: (x) => fmtSli(x.target) },
      { key: 'budget', label: t('slo.budget'), align: 'r', value: (x) => sloStatus(x).error_budget_remaining, render: (x) => budgetBar(sloStatus(x).error_budget_remaining, sloStatus(x).state) },
      { key: 'state', label: t('col.state'), value: (x) => sloStatus(x).state, render: (x) => sloBadge(sloStatus(x).state) },
    ],
    rows: list, sortKey: 'budget', sortDir: 'asc', emptyText: t('slo.none_title'),
  }));
}

// Cadre : même origine seulement (les pages HYPHA elles-mêmes, une page de statut…).
export function sameOriginUrl(url) {
  const raw = String(url || '').trim();
  if (!raw || /^\s*(javascript|data|vbscript):/i.test(raw)) return null;
  try {
    const u = new URL(raw, location.origin);
    return u.origin === location.origin ? u.pathname + u.search + u.hash : null;
  } catch (e) { return null; }
}

function iframe(body, o) {
  const url = sameOriginUrl(o.url);
  if (!url) { body.appendChild(empty(null, t('widget.iframe.bad_url'))); return; }
  body.classList.add('flush-body');
  body.appendChild(h('iframe', { src: url, class: 'w-iframe', title: o.title || url, loading: 'lazy', referrerpolicy: 'same-origin', sandbox: 'allow-same-origin allow-scripts allow-popups allow-forms' }));
}

export function imageUrl(url) {
  const raw = String(url || '').trim();
  if (!raw) return null;
  if (/^https:\/\//i.test(raw)) return raw;
  return sameOriginUrl(raw);
}

function image(body, o) {
  const url = imageUrl(o.url);
  if (!url) { body.appendChild(empty(null, t('widget.image.bad_url'))); return; }
  body.appendChild(h('img', { src: url, alt: o.alt || '', class: 'w-image', referrerpolicy: 'no-referrer', loading: 'lazy', style: { objectFit: o.fit === 'cover' ? 'cover' : 'contain' } }));
}

// Groupe : cadre titré, grille intérieure de 12 colonnes, widgets enfants dans l'ordre.
function group(body, w, env, renderChild) {
  const kids = asList((w.options || {}).widgets);
  body.classList.add('scroll');
  if (!kids.length) { body.appendChild(empty(null, t('widget.group.empty'))); return; }
  const grid = h('div', { class: 'group-grid' });
  body.appendChild(grid);
  kids.forEach((c) => {
    const cw = Math.max(1, Math.min(12, Number(c.w) || 4));
    const ch = Math.max(1, Math.min(8, Number(c.h) || 2));
    const inner = h('div', { class: 'widget-body' });
    grid.appendChild(h('div', { class: 'widget child', style: { gridColumn: 'span ' + cw, gridRow: 'span ' + ch } },
      h('div', { class: 'widget-head' }, h('h3', { title: c.title || '' }, valueRefs(c.title || '', env.vars, env.values) || t('widget.type.' + c.type))), inner));
    renderChild(inner, c, Object.assign({}, env, { height: ch * 64 + (ch - 1) * 8 - 38 }));
  });
}

