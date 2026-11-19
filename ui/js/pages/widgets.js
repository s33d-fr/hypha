// Widgets des tableaux de bord (§§ 7, 30) : rendu des types, substitution des variables de modèle
// (valeurs multiples comprises), formules, comparaison avec la période précédente, repères d'événements.
import { h, clear } from '../dom.js';
import { t } from '../i18n.js';
import { get, asList, isAbort } from '../api.js';
import { fmtUnit, fmtPct, isNum } from '../format.js';
import { empty, errorState, badgeState, checkStatusBadge } from '../components/ui.js';
import { timeChart, linesFromMetrics, summarize, colorAt } from '../components/chart.js';
import { hexMap } from '../components/hexmap.js';
import { compactLogRow } from './logs-shared.js';
import { monitorState, STATES } from './monitors-shared.js';
import { tagFilter } from './common.js';
import { fmtSli, fmtBurn, sloBadge, budgetBar, stateColor } from './slo.js';
import { V04_TYPES, V04_DEFAULTS, renderV04Widget, runQueries, overlayEvents, previousPeriod, valueRefs } from './widgets-extra.js';

export const WIDGET_TYPES = ['timeseries', 'query_value', 'toplist', 'log_stream', 'monitor_summary', 'note', 'host_map', 'check_status', 'slo'].concat(V04_TYPES);

export const WIDGET_DEFAULTS = Object.assign({
  timeseries: { w: 6, h: 4, queries: [{ q: 'avg:system.cpu.usage{$host} by {host}', display: 'line' }], options: { unit: 'percent' } },
  query_value: { w: 3, h: 2, queries: [{ q: 'avg:system.cpu.usage{$host}', display: 'line' }], options: { unit: 'percent', precision: 1, thresholds: [{ value: 80, color: 'warn' }, { value: 90, color: 'alert' }] } },
  toplist: { w: 4, h: 4, queries: [{ q: 'avg:system.cpu.usage{$host} by {host}', display: 'bars' }], options: { unit: 'percent', precision: 1 } },
  log_stream: { w: 6, h: 4, queries: [], options: { log_query: 'status:error' } },
  monitor_summary: { w: 4, h: 3, queries: [], options: {} },
  note: { w: 3, h: 2, queries: [], options: { text: '' } },
  host_map: { w: 6, h: 4, queries: [], options: { metric: 'cpu' } },
  check_status: { w: 3, h: 2, queries: [], options: { check: 'hypha.agent.up' } },
  slo: { w: 3, h: 3, queries: [], options: { slo_id: null } },
}, V04_DEFAULTS);

// Types qui lisent des métriques (seuls rendus dans un tableau partagé publiquement).
export const METRIC_TYPES = ['timeseries', 'query_value', 'toplist', 'heatmap', 'distribution', 'table', 'change', 'scatter', 'pie'];
const PUBLIC_OK = METRIC_TYPES.concat(['note', 'image', 'group']);

// $nom → nom:valeur, ou disparaît si la valeur est « * » ; accolades vides → {*}. Une valeur unique par variable.
export function applyVars(q, vars, values) {
  let out = String(q || '');
  for (const v of vars || []) {
    if (!v || !v.name) continue;
    let val = values[v.name] !== undefined ? values[v.name] : (v.default !== undefined ? v.default : '*');
    if (Array.isArray(val)) val = val.length === 1 ? val[0] : '*';
    const re = new RegExp('\\$' + v.name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '(?![\\w.])', 'g');
    out = out.replace(re, val === '*' || val === '' ? '\u0000' : tagFilter(v.tag || v.name, val));
  }
  out = out.replace(/\{([^}]*)\}/g, (m, inner) => {
    const parts = inner.split(',').map((x) => x.trim()).filter((x) => x && x !== '\u0000');
    return '{' + (parts.length ? parts.join(',') : '*') + '}';
  });
  return out.replace(/\u0000/g, '');
}

// Variables dans une requête de journaux : $host → host:valeur ; plusieurs valeurs → host:a OR host:b.
export function applyVarsLogs(q, vars, values) {
  let out = String(q || '');
  const quote = (val) => (/\s/.test(val) ? '"' + val + '"' : val);
  for (const v of vars || []) {
    if (!v || !v.name) continue;
    const val = values[v.name] !== undefined ? values[v.name] : (v.default !== undefined ? v.default : '*');
    const re = new RegExp('\\$' + v.name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '(?![\\w.])', 'g');
    const key = v.tag || v.name;
    let rep = '';
    if (Array.isArray(val)) { const list = val.filter((x) => x && x !== '*'); rep = list.map((x) => key + ':' + quote(x)).join(' OR '); } else if (val && val !== '*') rep = key + ':' + quote(val);
    out = out.replace(re, rep);
  }
  return out.replace(/\s+/g, ' ').trim();
}

function thresholdColor(v, thresholds) {
  if (!isNum(v) || !Array.isArray(thresholds) || !thresholds.length) return null;
  let color = null;
  for (const th of thresholds.slice().sort((a, b) => a.value - b.value)) if (isNum(th.value) && v >= th.value) color = th.color;
  if (!color) return null;
  return color === 'warn' ? 'var(--warn)' : color === 'ok' ? 'var(--ok)' : color === 'info' ? 'var(--accent)' : 'var(--crit)';
}

// Rendu simple d'un texte de note : titres (#), gras (**), paragraphes. Aucune balise HTML acceptée.
function noteBody(text) {
  const box = h('div', { class: 'note-body' });
  const paras = String(text || '').split(/\n{2,}/);
  for (const p of paras) {
    const line = p.trim();
    if (!line) continue;
    const m = /^(#{1,3})\s+(.*)$/.exec(line);
    if (m) { box.appendChild(h('h' + m[1].length, m[2])); continue; }
    const el = h('p');
    line.split(/(\*\*[^*]+\*\*)/g).forEach((part) => {
      if (/^\*\*[^*]+\*\*$/.test(part)) el.appendChild(h('b', part.slice(2, -2)));
      else part.split('\n').forEach((seg, i) => { if (i) el.appendChild(h('br')); el.appendChild(document.createTextNode(seg)); });
    });
    box.appendChild(el);
  }
  if (!box.childNodes.length) box.appendChild(h('span', { class: 'muted' }, t('widget.note_empty')));
  return box;
}

// env : {range, signal, vars, values, ctx, height, compare, publicPath}
export async function renderWidget(body, w, env) {
  clear(body);
  env = Object.assign({ applyVars, applyVarsLogs }, env);
  const opts = w.options || {};
  const queries = (w.queries || []).filter((q) => q && q.q);
  try {
    if (env.publicPath && !PUBLIC_OK.includes(w.type)) { body.appendChild(empty(null, t('share.widget_private'))); return; }
    switch (w.type) {
      case 'note':
        body.classList.add('scroll');
        body.appendChild(noteBody(valueRefs(opts.text, env.vars, env.values)));
        return;
      case 'timeseries': {
        if (!queries.length) { body.appendChild(empty(null, t('widget.no_query'))); return; }
        body.appendChild(h('div', { class: 'skeleton', style: { height: env.height + 'px' } }));
        const [resp, prev, evs] = await Promise.all([
          runQueries(w, env, { maxPoints: 300 }),
          env.compare ? previousPeriod(w, env, 300).catch(() => null) : Promise.resolve(null),
          overlayEvents(w, env),
        ]);
        const display = Object.fromEntries(queries.map((q, i) => ['abcdefgh'[i], q.display || 'line']));
        const merged = prev ? Object.assign({}, resp, { series: resp.series.concat(prev.series.map((sr) => Object.assign({}, sr, { query: sr.query + '~prev' }))) }) : resp;
        const names = Object.assign({}, resp.names);
        const { xs, lines } = linesFromMetrics(merged, { display, names });
        const real = lines.filter((l) => !String(l.query).endsWith('~prev'));
        real.forEach((l, i) => { l.color = colorAt(i); });
        lines.filter((l) => String(l.query).endsWith('~prev')).forEach((l) => {
          const twin = real.find((r) => r.query + '~prev' === l.query && JSON.stringify(r.group) === JSON.stringify(l.group));
          l.color = twin ? twin.color : colorAt(0);
          l.dash = [4, 4];
          l.width = 1;
          l.display = 'line';
          l.label = (twin ? twin.label : l.label) + ' ' + t('dash.prev_suffix');
        });
        const showLegend = opts.legend !== false && lines.length > 1;
        const legendSpace = showLegend ? 26 : 0;
        clear(body);
        body.appendChild(timeChart({
          xs, lines, unit: opts.unit || (lines[0] && lines[0].unit) || null, height: Math.max(60, env.height - legendSpace),
          xRange: [env.range.from, env.range.to], onRange: env.ctx.onRange, precision: opts.precision, legend: showLegend,
          thresholds: (opts.thresholds || []).map((x) => ({ value: x.value, color: x.color })), sync: 'dash', events: evs, yLog: !!opts.y_log,
        }));
        return;
      }
      case 'query_value': {
        if (!queries.length) { body.appendChild(empty(null, t('widget.no_query'))); return; }
        const qv1 = Object.assign({}, w, { queries: queries.slice(0, 1) });
        const [resp, prev] = await Promise.all([runQueries(qv1, env, { maxPoints: 120 }), env.compare ? previousPeriod(qv1, env, 120).catch(() => null) : Promise.resolve(null)]);
        const value = lastAvg(resp.series);
        const unit = opts.unit || ((resp.series || [])[0] || {}).unit || null;
        const before = prev ? lastAvg(prev.series) : null;
        const color = thresholdColor(value, opts.thresholds);
        clear(body);
        const delta = isNum(value) && isNum(before) && before !== 0 ? (value - before) / Math.abs(before) * 100 : null;
        body.appendChild(h('div', { class: 'qv' }, h('div', { class: 'v', style: { color } }, isNum(value) ? fmtUnit(value, unit, opts.precision) : '-'),
          h('div', { class: 'u' }, isNum(value) ? t('widget.last_value') : t('chart.no_data')),
          isNum(delta) ? h('div', { class: 'xsmall', style: { color: delta > 0 ? 'var(--warn)' : 'var(--ok)' } }, (delta > 0 ? '▲ ' : '▼ ') + fmtPct(Math.abs(delta), 1) + ' ' + t('dash.vs_prev')) : null));
        return;
      }
      case 'toplist': {
        if (!queries.length) { body.appendChild(empty(null, t('widget.no_query'))); return; }
        const resp = await runQueries(Object.assign({}, w, { queries: queries.slice(0, 1) }), env, { maxPoints: 60 });
        const series = resp.series || [];
        const rows = series.map((s2) => {
          const g = s2.group && Object.keys(s2.group).length ? Object.values(s2.group).join(', ') : ((resp.names && resp.names[s2.query]) || s2.metric || '');
          return { label: g, v: summarize((s2.points || []).map((p) => p[1])).avg, unit: opts.unit || s2.unit };
        }).filter((r) => isNum(r.v)).sort((a, b) => b.v - a.v).slice(0, 25);
        clear(body);
        body.classList.add('scroll');
        if (!rows.length) { body.appendChild(empty(null, t('chart.no_data'))); return; }
        const max = Math.max(...rows.map((r) => Math.abs(r.v))) || 1;
        body.appendChild(h('div', { class: 'toplist' }, rows.map((r) => h('div', { class: 'tl-row' },
          h('div', { class: 'tl-bar', title: r.label }, h('i', { style: { width: Math.max(1, Math.abs(r.v) / max * 100) + '%', borderRightColor: thresholdColor(r.v, opts.thresholds) || null } }), h('span', r.label || '-')),
          h('span', { class: 'num', style: { textAlign: 'right' } }, fmtUnit(r.v, r.unit, opts.precision))))));
        return;
      }
      case 'log_stream': {
        const q = applyVarsLogs(opts.log_query || '', env.vars, env.values);
        const resp = await get('/logs/search', { q, from: env.range.from, to: env.range.to, limit: 50 }, { signal: env.signal });
        const logs = resp && Array.isArray(resp.logs) ? resp.logs : asList(resp, 'logs');
        body.classList.add('scroll');
        clear(body);
        if (!logs.length) { body.appendChild(empty(null, t('logs.no_logs_range'))); return; }
        body.appendChild(h('div', { class: 'log-list' }, logs.map((l) => compactLogRow(l, () => env.ctx.navigate('/logs', { q, focus: l.id })))));
        return;
      }
      case 'monitor_summary': {
        const list = asList(await get('/monitors', null, { signal: env.signal }), 'monitors');
        const counts = Object.fromEntries(STATES.map((s2) => [s2, 0]));
        list.forEach((m) => { const s2 = monitorState(m); if (counts[s2] !== undefined) counts[s2]++; });
        body.classList.add('scroll');
        clear(body);
        body.appendChild(h('div', { class: 'row wrap mb-2' }, STATES.map((s2) => h('a', { href: env.ctx.href('/monitors', { state: s2 }), class: 'row', style: { gap: '6px', color: 'inherit' } }, badgeState(s2), h('b', { class: 'num' }, String(counts[s2]))))));
        const bad = list.filter((m) => monitorState(m) !== 'OK').slice(0, 20);
        if (!bad.length) body.appendChild(h('div', { class: 'muted small' }, t('widget.all_ok')));
        else body.appendChild(h('ul', { class: 'list-plain' }, bad.map((m) => h('li', badgeState(monitorState(m)), h('a', { class: 'ellipsis', href: env.ctx.href('/monitors/' + m.id) }, m.name || '#' + m.id)))));
        return;
      }
      case 'host_map': {
        const tagQ = (env.vars || []).map((v) => {
          const val = env.values[v.name] !== undefined ? env.values[v.name] : v.default;
          const list = Array.isArray(val) ? val.filter((x) => x && x !== '*') : (val && val !== '*' ? [val] : []);
          return list.length ? { k: v.tag || v.name, vals: list.map(String) } : null;
        }).filter(Boolean);
        let hosts = asList(await get('/hosts', null, { signal: env.signal }), 'hosts');
        hosts = hosts.filter((x) => tagQ.every(({ k, vals }) => (k === 'host' ? vals.includes(x.hostname) : x.tags && vals.includes(String(x.tags[k])))));
        clear(body);
        body.classList.add('scroll');
        if (!hosts.length) { body.appendChild(empty(null, t('infra.none_title'))); return; }
        const map = hexMap(hosts, { metric: opts.metric || 'cpu', onClick: (x) => env.ctx.navigate('/infrastructure/host/' + x.id), size: hosts.length > 60 ? 10 : undefined });
        env.ctx.onCleanup(() => map.destroy());
        body.appendChild(map);
        return;
      }
      case 'slo': {
        if (opts.slo_id === null || opts.slo_id === undefined || opts.slo_id === '') { body.appendChild(empty(null, t('widget.no_slo'))); return; }
        const st = (await get('/slos/' + encodeURIComponent(opts.slo_id) + '/status', null, { signal: env.signal })) || {};
        clear(body);
        body.classList.add('scroll');
        body.appendChild(h('div', { class: 'qv', style: { height: 'auto', paddingTop: '4px', gap: '6px' } },
          h('div', { class: 'v', style: { color: stateColor(st.state) } }, fmtSli(st.sli)),
          h('div', { class: 'u' }, t('widget.slo_target', { v: fmtSli(st.target) })),
          sloBadge(st.state),
          h('div', { style: { width: '100%', maxWidth: '260px' } }, h('div', { class: 'xsmall muted' }, t('slo.budget')), budgetBar(st.error_budget_remaining, st.state)),
          h('div', { class: 'xsmall muted' }, t('slo.burn_1h') + ' ' + fmtBurn(st.burn_rate_1h) + ' · ' + t('slo.burn_6h') + ' ' + fmtBurn(st.burn_rate_6h)),
          h('a', { class: 'xsmall', href: env.ctx.href('/slos/' + opts.slo_id) }, t('widget.slo_open'))));
        return;
      }
      case 'check_status': {
        const rows = asList(await get('/checks', { name: opts.check || undefined }, { signal: env.signal }), 'checks');
        const counts = [0, 0, 0, 0];
        rows.forEach((r) => { const st = Number(r.status); if (st >= 0 && st <= 3) counts[st]++; });
        clear(body);
        body.classList.add('scroll');
        const worst = counts[2] ? 2 : counts[1] ? 1 : counts[3] ? 3 : 0;
        body.appendChild(h('div', { class: 'qv', style: { height: 'auto', paddingTop: '4px' } },
          rows.length ? checkStatusBadge(worst) : h('span', { class: 'muted' }, t('checks.none')),
          h('div', { class: 'row wrap mt-2', style: { justifyContent: 'center' } },
            [0, 1, 2, 3].map((st) => h('span', { class: 'small row', style: { gap: '4px' } }, checkStatusBadge(st), h('b', { class: 'num' }, String(counts[st])))))));
        const bad = rows.filter((r) => Number(r.status) > 0).slice(0, 10);
        if (bad.length) body.appendChild(h('ul', { class: 'list-plain mt-2' }, bad.map((r) => h('li', checkStatusBadge(Number(r.status)),
          h('span', { class: 'ellipsis small' }, (r.hostname || (r.tags && r.tags.host) || '') + ' ' + Object.entries(r.tags || {}).filter(([k]) => k !== 'host').map(([k, v]) => k + ':' + v).join(' '))))));
        return;
      }
      default:
        await renderV04Widget(body, w, env, renderWidget);
    }
  } catch (e) {
    if (isAbort(e)) return;
    clear(body);
    body.appendChild(errorState(e, () => renderWidget(body, w, env)));
  }
}

// Plusieurs groupes : moyenne des dernières valeurs.
function lastAvg(series) {
  const lasts = (series || []).map((s2) => summarize((s2.points || []).map((p) => p[1])).last).filter(isNum);
  return lasts.length ? lasts.reduce((a, b) => a + b, 0) / lasts.length : null;
}
