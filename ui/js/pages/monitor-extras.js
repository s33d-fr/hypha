// Formulaire de moniteur, parties 0.4.0 : aperçu par POST /monitors/preview (bornes d'anomalie,
// prévision), priorité et options (§ 17), éditeur du message (§ 18), catalogue de modèles (§ 19),
// expression composite.
import { h, clear, icon, cssVar } from '../dom.js';
import { t, tn, has } from '../i18n.js';
import { get, post, asList, isAbort } from '../api.js';
import { fmtUnit, fmtDuration, isNum } from '../format.js';
import { field, select, pills, modal, toast, errorMessage, errorState, badgeState, dropdown, menuItem, searchInput, empty } from '../components/ui.js';
import { timeChart, alignPoints, groupLabel, colorAt, alpha } from '../components/chart.js';
import { dataTable } from '../components/table.js';
import { prioritySelect, priorityBadge, routeMissing } from './ops-shared.js';
import { monitorState, stateRank } from './monitors-shared.js';

export const CLASSIC_TYPES = ['metric', 'host', 'service_check', 'log', 'synthetic', 'apm', 'slo'];
export const ADVANCED_TYPES = ['anomaly', 'forecast', 'outlier', 'composite', 'change', 'event', 'process', 'error_tracking'];
export const THRESHOLD_TYPES = ['metric', 'log', 'apm', 'slo', 'anomaly', 'forecast', 'change', 'event', 'error_tracking'];
// Types dont le seuil se lit sur l'échelle de la courbe de l'aperçu.
const THRESHOLD_ON_CHART = ['metric', 'forecast', 'log', 'event', 'error_tracking', 'apm'];

export const DEFAULT_OPTIONS = {
  depends_on_host: true, depends_on: [], flapping: { changes: 5, window_s: 1800 }, escalations: [],
  require_consecutive: 1, recovery_consecutive: 1, evaluation_delay_s: 0,
};

export function withDefaultOptions(o) {
  const src = o && typeof o === 'object' ? o : {};
  const out = Object.assign({}, DEFAULT_OPTIONS, src);
  out.flapping = Object.assign({}, DEFAULT_OPTIONS.flapping, src.flapping && typeof src.flapping === 'object' ? src.flapping : {});
  out.escalations = Array.isArray(src.escalations) ? src.escalations.map((e) => Object.assign({ after_s: 900, channel_ids: [], message: '' }, e)) : [];
  out.depends_on = Array.isArray(src.depends_on) ? src.depends_on.slice() : [];
  return out;
}

const DELAYS = [300, 600, 900, 1800, 3600, 7200, 14400, 28800, 86400];
const FLAP_WINDOWS = [600, 900, 1800, 3600, 7200];
const EVAL_DELAYS = [0, 30, 60, 120, 300, 600, 900];

// ── Aperçu en direct ──────────────────────────────────────────────────────
function seriesItems(resp) {
  let series = resp && resp.series;
  if (series && !Array.isArray(series) && Array.isArray(series.series)) series = series.series;
  if (!Array.isArray(series)) return [];
  if (series.length && Array.isArray(series[0]) && isNum(series[0][0])) series = [{ points: series }];
  return series.map((s2, i) => {
    const pts = Array.isArray(s2) ? s2 : (s2.points || s2.values || []);
    const g = s2 && typeof s2.group === 'object' ? groupLabel(s2.group) : (s2 && s2.group) || '';
    return { label: (s2 && s2.label) || g || (s2 && (s2.metric || s2.query)) || t('chart.value'), points: pts, color: colorAt(i), unit: s2 && s2.unit };
  });
}

// def : {type, query, thresholds, options}. opts : {signal, height, fallback(container)}.
export async function previewMonitor(container, def, opts = {}) {
  clear(container);
  const height = opts.height || 200;
  container.appendChild(h('div', { class: 'skeleton', style: { height: height + 'px' } }));
  let resp;
  try {
    resp = await post('/monitors/preview', { type: def.type, query: def.query, thresholds: def.thresholds || {}, options: def.options || {} }, { signal: opts.signal });
  } catch (e) {
    if (isAbort(e)) return null;
    clear(container);
    if (routeMissing(e) && opts.fallback) { opts.fallback(container); return null; }
    if (routeMissing(e)) { container.appendChild(h('div', { class: 'callout' }, icon('info', 16), h('span', { class: 'small' }, t('mon.preview.unavailable')))); return null; }
    if (e && e.status === 400) {
      container.appendChild(h('div', { class: 'callout warn' }, icon('warn', 16), h('div', { class: 'small' }, h('div', t('mon.preview.invalid')), e.detail ? h('div', { class: 'mono xsmall mt-2' }, e.detail) : null)));
      return null;
    }
    container.appendChild(errorState(e, () => previewMonitor(container, def, opts)));
    return null;
  }
  clear(container);
  const items = seriesItems(resp);
  const unit = (items.find((x) => x.unit) || {}).unit || null;
  const bands = [];
  const muted = cssVar('--muted') || '#888';
  if (Array.isArray(resp.bounds) && resp.bounds.length) {
    const hi = items.length;
    items.push({ label: t('mon.preview.upper'), points: resp.bounds.map((b) => [b[0], b[2]]), color: muted, dash: [5, 4], width: 1 });
    items.push({ label: t('mon.preview.lower'), points: resp.bounds.map((b) => [b[0], b[1]]), color: muted, dash: [5, 4], width: 1 });
    bands.push({ hi, lo: hi + 1, fill: alpha(muted, 0.14) });
  }
  if (Array.isArray(resp.forecast) && resp.forecast.length) {
    const fc = cssVar('--s7') || '#9085e9';
    items.push({ label: t('mon.preview.forecast'), points: resp.forecast.map((p) => [p[0], p[1]]), color: fc, dash: [6, 4], width: 1.6 });
    if (resp.forecast.some((p) => isNum(p[2]) && isNum(p[3]))) {
      const hi = items.length;
      items.push({ label: t('mon.preview.forecast_hi'), points: resp.forecast.map((p) => [p[0], p[3]]), color: alpha(fc, 0.5), width: 0.5 });
      items.push({ label: t('mon.preview.forecast_lo'), points: resp.forecast.map((p) => [p[0], p[2]]), color: alpha(fc, 0.5), width: 0.5 });
      bands.push({ hi, lo: hi + 1, fill: alpha(fc, 0.12) });
    }
  }
  const groups = Array.isArray(resp.groups) ? resp.groups : [];
  const counts = {};
  groups.forEach((g) => { const st = String(g.state || 'OK').toUpperCase(); counts[st] = (counts[st] || 0) + 1; });
  container.appendChild(h('div', { class: 'row wrap mb-2' }, h('span', { class: 'small text-2' }, tn('mon.preview.groups', groups.length)),
    ['ALERT', 'WARN', 'NODATA', 'OK'].filter((k) => counts[k]).map((k) => h('span', { class: 'row', style: { gap: '4px' } }, badgeState(k), h('b', { class: 'num small' }, String(counts[k]))))));
  if (items.length) {
    const { xs, ys } = alignPoints(items.map((it) => it.points));
    const lines = items.map((it, i) => ({ label: it.label, values: ys[i], color: it.color, dash: it.dash, width: it.width, unit }));
    const th = [];
    if (THRESHOLD_ON_CHART.includes(def.type)) {
      const tt = def.thresholds || {};
      if (isNum(tt.warning)) th.push({ value: tt.warning, color: 'warn' });
      if (isNum(tt.critical)) th.push({ value: tt.critical, color: 'alert' });
    }
    container.appendChild(timeChart({ xs, lines, unit, height, thresholds: th, bands, legendMax: 12 }));
    if (Array.isArray(resp.forecast) && resp.forecast.length) container.appendChild(h('div', { class: 'xsmall muted mt-2' }, t('mon.preview.forecast_note')));
  } else if (!groups.length) {
    container.appendChild(h('div', { class: 'chart-empty', style: { height: '80px' } }, t('mon.preview.nothing')));
  }
  if (groups.length) {
    container.appendChild(h('div', { class: 'mt-2' }, dataTable({
      columns: [
        { key: 'state', label: t('col.state'), value: (g) => stateRank(String(g.state || 'OK').toUpperCase()), defaultDir: 'asc', render: (g) => badgeState(String(g.state || 'OK').toUpperCase()) },
        { key: 'group', label: t('col.group'), value: (g) => String(g.group || '*'), render: (g) => h('span', { class: 'mono small' }, typeof g.group === 'object' ? groupLabel(g.group) : g.group || '*') },
        { key: 'value', label: t('col.value'), align: 'r', render: (g) => (isNum(g.value) ? (def.type === 'forecast' ? etaText(g.value) : fmtUnit(g.value, def.type === 'anomaly' || def.type === 'outlier' ? 'percent' : unit)) : '-') },
      ],
      rows: groups, sortKey: 'state', sortDir: 'asc', maxRows: 25,
    })));
  }
  return resp;
}

export function etaText(seconds) {
  if (!isNum(seconds) || seconds < 0) return t('mon.eta.never');
  if (seconds === 0) return t('mon.eta.crossed');
  return t('mon.eta.in', { d: fmtDuration(seconds * 1000, true) });
}

// ── Expression composite : 12 && (13 || !14) ──────────────────────────────
export function parseComposite(expr) {
  const src = String(expr || '');
  const toks = [];
  const re = /\s*(?:(\d{1,18})|(&&)|(\|\|)|(!)|(\()|(\)))/y;
  let i = 0;
  const text = src.replace(/\s+$/, '');
  if (!text.trim()) return { ok: false, error: t('mon.comp.empty'), ids: [] };
  while (i < text.length) {
    re.lastIndex = i;
    const m = re.exec(text);
    if (!m || re.lastIndex === i) return { ok: false, error: t('mon.comp.bad_char', { pos: i + 1, txt: text.slice(i, i + 8).trim() }), ids: [] };
    const kind = ['id', 'and', 'or', 'not', 'lpar', 'rpar'][[1, 2, 3, 4, 5, 6].find((k) => m[k] !== undefined) - 1];
    toks.push({ kind, v: m[1] });
    i = re.lastIndex;
  }
  let p = 0;
  const ids = [];
  function orE(depth) {
    if (depth > 20) throw new Error(t('mon.comp.too_deep'));
    const parts = [andE(depth)];
    while (toks[p] && toks[p].kind === 'or') { p++; parts.push(andE(depth)); }
    return parts.length === 1 ? parts[0] : { op: 'or', parts };
  }
  function andE(depth) {
    const parts = [unary(depth)];
    while (toks[p] && toks[p].kind === 'and') { p++; parts.push(unary(depth)); }
    return parts.length === 1 ? parts[0] : { op: 'and', parts };
  }
  function unary(depth) {
    const tk = toks[p];
    if (!tk) throw new Error(t('mon.comp.unexpected_end'));
    if (tk.kind === 'not') { p++; return { op: 'not', parts: [unary(depth + 1)] }; }
    if (tk.kind === 'lpar') {
      p++;
      const n = orE(depth + 1);
      if (!toks[p] || toks[p].kind !== 'rpar') throw new Error(t('mon.comp.missing_paren'));
      p++;
      return n;
    }
    if (tk.kind === 'id') { p++; ids.push(tk.v); return { op: 'id', id: tk.v }; }
    throw new Error(t('mon.comp.unexpected', { tok: tk.kind === 'rpar' ? ')' : tk.kind === 'and' ? '&&' : '||' }));
  }
  try {
    const tree = orE(0);
    if (p < toks.length) throw new Error(t('mon.comp.trailing'));
    return { ok: true, tree, ids: Array.from(new Set(ids)) };
  } catch (e) {
    return { ok: false, error: e.message, ids: [] };
  }
}

export function compositeReadable(tree, nameOf) {
  if (!tree) return '';
  if (tree.op === 'id') return '« ' + nameOf(tree.id) + ' »';
  if (tree.op === 'not') return t('mon.comp.not') + ' ' + compositeReadable(tree.parts[0], nameOf);
  const sep = ' ' + t(tree.op === 'and' ? 'mon.comp.and' : 'mon.comp.or') + ' ';
  return '(' + tree.parts.map((x) => compositeReadable(x, nameOf)).join(sep) + ')';
}

// ── Priorité et options (§ 17) ────────────────────────────────────────────
export function optionsSection(m, { monitorsP, channelsP, selfId }) {
  m.options = withDefaultOptions(m.options);
  const o = m.options;
  const box = h('div', { class: 'col gap-4' });

  const prioHelp = h('div', { class: 'row wrap xsmall muted' }, [1, 2, 3, 4, 5].map((n) => h('span', { class: 'row', style: { gap: '4px' } }, priorityBadge(n), t('prio.name.' + n))));
  box.appendChild(h('div', { class: 'form-grid' },
    field(t('mon.f.priority'), prioritySelect(m.priority, (v) => { m.priority = v; }), t('mon.f.priority_hint')),
    h('div', { class: 'full' }, prioHelp)));

  // Dépendances
  const dep = h('input', { type: 'checkbox', checked: o.depends_on_host !== false });
  dep.addEventListener('change', () => { o.depends_on_host = dep.checked; });
  const depList = h('div', { class: 'tags' });
  const depSel = h('select', { class: 'input' }, h('option', { value: '' }, t('mon.f.depends_add')));
  let monitors = [];
  const drawDeps = () => {
    clear(depList);
    if (!o.depends_on.length) depList.appendChild(h('span', { class: 'small muted' }, t('mon.f.depends_none')));
    for (const id of o.depends_on) {
      const mm = monitors.find((x) => String(x.id) === String(id));
      depList.appendChild(h('span', { class: 'chip' }, mm ? mm.name : '#' + id,
        h('button', { type: 'button', 'aria-label': t('action.remove'), onclick: () => { o.depends_on = o.depends_on.filter((x) => String(x) !== String(id)); drawDeps(); fillSel(); } }, icon('x', 11))));
    }
  };
  const fillSel = () => {
    while (depSel.options.length > 1) depSel.remove(1);
    monitors.filter((x) => String(x.id) !== String(selfId) && !o.depends_on.map(String).includes(String(x.id)))
      .sort((a, b) => String(a.name).localeCompare(String(b.name))).forEach((x) => depSel.appendChild(h('option', { value: x.id }, (x.name || '#' + x.id) + ' (#' + x.id + ')')));
  };
  depSel.addEventListener('change', () => {
    if (!depSel.value) return;
    o.depends_on.push(/^\d+$/.test(depSel.value) ? Number(depSel.value) : depSel.value);
    depSel.value = '';
    drawDeps(); fillSel();
  });
  monitorsP.then((list) => { monitors = list; drawDeps(); fillSel(); });
  drawDeps();
  box.appendChild(h('div', h('h4', { class: 'mb-2' }, t('mon.opt.dependencies')),
    h('div', { class: 'col gap-3' },
      h('label', { class: 'check' }, dep, t('mon.f.depends_on_host')), h('div', { class: 'xsmall muted', style: { marginTop: '-6px' } }, t('mon.f.depends_on_host_hint')),
      field(t('mon.f.depends_on'), h('div', { class: 'col', style: { gap: '6px' } }, depList, depSel), t('mon.f.depends_on_hint')))));

  // Battement et tolérances
  const changes = h('input', { class: 'input', type: 'number', min: 2, max: 100, value: o.flapping.changes });
  changes.addEventListener('input', () => { const n = Number(changes.value); if (n >= 2) o.flapping.changes = Math.round(n); });
  const mkNum = (key, min, max) => {
    const inp = h('input', { class: 'input', type: 'number', min, max, value: o[key] });
    inp.addEventListener('input', () => { const n = Number(inp.value); if (Number.isFinite(n) && n >= min) o[key] = Math.round(n); });
    return inp;
  };
  box.appendChild(h('div', h('h4', { class: 'mb-2' }, t('mon.opt.stability')),
    h('div', { class: 'form-grid' },
      field(t('mon.f.flap_changes'), changes, t('mon.f.flap_changes_hint')),
      field(t('mon.f.flap_window'), select(FLAP_WINDOWS.concat(FLAP_WINDOWS.includes(o.flapping.window_s) ? [] : [o.flapping.window_s]).sort((a, b) => a - b).map((s2) => ({ value: s2, label: fmtDuration(s2 * 1000, true) })), o.flapping.window_s, (v) => { o.flapping.window_s = Number(v); })),
      field(t('mon.f.require_consecutive'), mkNum('require_consecutive', 1, 60), t('mon.f.require_consecutive_hint')),
      field(t('mon.f.recovery_consecutive'), mkNum('recovery_consecutive', 1, 60), t('mon.f.recovery_consecutive_hint')),
      field(t('mon.f.evaluation_delay'), select(EVAL_DELAYS.concat(EVAL_DELAYS.includes(o.evaluation_delay_s) ? [] : [o.evaluation_delay_s]).sort((a, b) => a - b).map((s2) => ({ value: s2, label: s2 ? fmtDuration(s2 * 1000, true) : t('mon.f.no_delay') })), o.evaluation_delay_s || 0, (v) => { o.evaluation_delay_s = Number(v); }), t('mon.f.evaluation_delay_hint')))));

  // Escalades
  const escBox = h('div', { class: 'col gap-3' });
  let channels = [];
  const drawEsc = () => {
    clear(escBox);
    if (!o.escalations.length) escBox.appendChild(h('div', { class: 'small muted' }, t('mon.esc.none')));
    o.escalations.forEach((e, i) => {
      const chans = h('div', { class: 'tags' });
      if (!channels.length) chans.appendChild(h('span', { class: 'small muted' }, t('monitors.f.no_channels')));
      for (const c of channels) {
        const cb = h('input', { type: 'checkbox', checked: (e.channel_ids || []).map(String).includes(String(c.id)) });
        cb.addEventListener('change', () => {
          const set = new Set((e.channel_ids || []).map(String));
          if (cb.checked) set.add(String(c.id)); else set.delete(String(c.id));
          e.channel_ids = Array.from(set).map((x) => (/^\d+$/.test(x) ? Number(x) : x));
        });
        chans.appendChild(h('label', { class: 'check tag' }, cb, c.name || '#' + c.id));
      }
      const msg = h('input', { class: 'input', value: e.message || '', placeholder: t('mon.esc.message_ph') });
      msg.addEventListener('input', () => { e.message = msg.value; });
      escBox.appendChild(h('div', { class: 'esc-row' },
        h('div', { class: 'row' }, h('span', { class: 'step-num' }, String(i + 1)), h('b', { class: 'small' }, t('mon.esc.step', { n: i + 1 })), h('span', { class: 'spacer' }),
          h('button', { class: 'btn ghost sm icon danger', type: 'button', title: t('action.remove'), onclick: () => { o.escalations.splice(i, 1); drawEsc(); } }, icon('trash', 13))),
        h('div', { class: 'form-grid' },
          field(t('mon.esc.after'), select(DELAYS.concat(DELAYS.includes(e.after_s) ? [] : [e.after_s]).sort((a, b) => a - b).map((s2) => ({ value: s2, label: t('mon.esc.after_v', { d: fmtDuration(s2 * 1000, true) }) })), e.after_s, (v) => { e.after_s = Number(v); })),
          h('div', { class: 'full' }, field(t('mon.esc.channels'), chans)),
          h('div', { class: 'full' }, field(t('mon.esc.message'), msg)))));
    });
    escBox.appendChild(h('div', h('button', { class: 'btn sm', type: 'button', onclick: () => {
      const last = o.escalations[o.escalations.length - 1];
      o.escalations.push({ after_s: last ? Math.min(86400, last.after_s * 2) : 900, channel_ids: [], message: '' });
      drawEsc();
    } }, icon('plus', 13), t('mon.esc.add'))));
  };
  channelsP.then((list) => { channels = list; drawEsc(); });
  drawEsc();
  box.appendChild(h('div', h('h4', { class: 'mb-2' }, t('mon.opt.escalations')), h('p', { class: 'xsmall muted' }, t('mon.esc.hint')), escBox));
  return box;
}

// ── Éditeur du message (§ 18) ─────────────────────────────────────────────
const VARS = ['name', 'group', 'host', 'value', 'state', 'priority', 'threshold', 'url', 'last_value', 'duration', 'eta'];
const FLAGS = ['is_alert', 'is_warning', 'is_recovery', 'is_nodata', 'is_renotify', 'is_escalation'];

export function renderMessage(tpl, vars) {
  let text = String(tpl || '');
  const blockRe = new RegExp('\\{\\{\\s*([#^])\\s*(' + FLAGS.join('|') + ')\\s*\\}\\}([\\s\\S]*?)\\{\\{\\s*/\\s*\\2\\s*\\}\\}', 'g');
  for (let i = 0; i < 10; i++) {
    const next = text.replace(blockRe, (m0, sign, flag, inner) => (!!vars[flag] === (sign === '#') ? inner : ''));
    if (next === text) break;
    text = next;
  }
  text = text.replace(/\{\{\s*([a-z_]+)(?:\.([a-z0-9_.\-/]{1,100}))?\s*\}\}/g, (m0, name, sub) => {
    if (name === 'tags' && sub) { const v = (vars.tags || {})[sub]; return v === undefined || v === null ? '' : String(v); }
    if (!sub && VARS.includes(name)) return vars[name] === undefined || vars[name] === null ? '' : String(vars[name]);
    return m0;
  });
  const mentions = [];
  text = text.replace(/(^|[^\w@])@canal-(\d{1,18})\b/g, (m0, pre, id) => { if (!mentions.includes(id)) mentions.push(id); return pre; });
  return { text: text.replace(/[ \t]+\n/g, '\n').trim(), mentions };
}

function insertAt(ta, before, after = '') {
  const s0 = ta.selectionStart ?? ta.value.length;
  const e0 = ta.selectionEnd ?? s0;
  const v = ta.value;
  const sel = v.slice(s0, e0);
  ta.value = v.slice(0, s0) + before + sel + after + v.slice(e0);
  ta.focus();
  ta.selectionStart = s0 + before.length;
  ta.selectionEnd = s0 + before.length + sel.length;
  ta.dispatchEvent(new Event('input'));
}

export function messageEditor(m, { channelsP, origin }) {
  const ta = h('textarea', { class: 'input mono', rows: 6, placeholder: t('monitors.f.message_ph') });
  ta.value = m.message || '';
  let previewState = 'ALERT';
  let channels = [];
  const out = h('div', { class: 'msg-preview' });
  const mentionInfo = h('div', { class: 'xsmall muted' });
  const pillSlot = h('span');
  function sample() {
    const th = m.thresholds || {};
    const tags = Object.assign({ env: 'prod' }, m.tags || {});
    return {
      name: m.name || t('mon.msg.sample_name'), group: 'host:web-01', host: 'web-01', value: previewState === 'OK' ? '42,1' : '93,4',
      state: previewState === 'OK' ? 'OK' : previewState, priority: m.priority ? 'P' + m.priority : '',
      threshold: isNum(th.critical) ? String(th.critical) : '', url: (origin || location.origin) + '/#/monitors/' + (m.id || '…'),
      last_value: '91,8', duration: '12 min', eta: m.type === 'forecast' ? t('mon.eta.in', { d: '2 j 4 h' }) : '', tags,
      is_alert: previewState === 'ALERT', is_warning: previewState === 'WARN', is_recovery: previewState === 'OK', is_nodata: previewState === 'NODATA',
      is_renotify: false, is_escalation: false,
    };
  }
  function draw() {
    const r = renderMessage(ta.value, sample());
    clear(out);
    out.appendChild(r.text ? document.createTextNode(r.text) : h('span', { class: 'muted' }, t('mon.msg.empty_render')));
    if (r.mentions.length) {
      const names = r.mentions.map((id) => { const c = channels.find((x) => String(x.id) === id); return c ? c.name : t('mon.msg.unknown_channel', { id }); });
      mentionInfo.textContent = t('mon.msg.mentions', { list: names.join(', ') });
    } else mentionInfo.textContent = '';
    clear(pillSlot);
    pillSlot.appendChild(pills(['ALERT', 'WARN', 'OK', 'NODATA'].map((k) => ({ key: k, label: t('mon.msg.state.' + k) })), previewState, (k) => { previewState = k; draw(); }));
  }
  ta.addEventListener('input', () => { m.message = ta.value; draw(); });

  const varsMenu = dropdown(h('button', { class: 'btn sm', type: 'button' }, '{{ }}', t('mon.msg.variables'), icon('chevronDown', 12)), (close) => h('div',
    h('div', { class: 'menu-head' }, t('mon.msg.variables_head')),
    VARS.filter((v) => v !== 'eta' || m.type === 'forecast').map((v) => menuItem(h('span', h('span', { class: 'mono' }, '{{' + v + '}}'), ' ', h('span', { class: 'muted xsmall' }, t('mon.var.' + v))), () => { close(); insertAt(ta, '{{' + v + '}}'); })),
    menuItem(h('span', h('span', { class: 'mono' }, '{{tags.env}}'), ' ', h('span', { class: 'muted xsmall' }, t('mon.var.tags'))), () => { close(); insertAt(ta, '{{tags.', '}}'); })), { left: true });
  const blocksMenu = dropdown(h('button', { class: 'btn sm', type: 'button' }, t('mon.msg.conditions'), icon('chevronDown', 12)), (close) => h('div',
    h('div', { class: 'menu-head' }, t('mon.msg.conditions_head')),
    FLAGS.map((f) => menuItem(h('span', h('span', { class: 'mono' }, '{{#' + f + '}}'), ' ', h('span', { class: 'muted xsmall' }, t('mon.flag.' + f))), () => { close(); insertAt(ta, '{{#' + f + '}}', '{{/' + f + '}}'); })),
    h('div', { class: 'menu-sep' }),
    menuItem(h('span', h('span', { class: 'mono' }, '{{^is_recovery}}'), ' ', h('span', { class: 'muted xsmall' }, t('mon.flag.negate'))), () => { close(); insertAt(ta, '{{^is_recovery}}', '{{/is_recovery}}'); })), { left: true });
  const mentionMenu = dropdown(h('button', { class: 'btn sm', type: 'button' }, '@', t('mon.msg.mention'), icon('chevronDown', 12)), (close) => h('div',
    h('div', { class: 'menu-head' }, t('mon.msg.mention_head')),
    channels.length ? channels.map((c) => menuItem(h('span', c.name || '#' + c.id, ' ', h('span', { class: 'mono xsmall muted' }, '@canal-' + c.id)), () => { close(); insertAt(ta, '@canal-' + c.id + ' '); }))
      : h('div', { class: 'menu-head' }, t('monitors.f.no_channels'))), { left: true });
  channelsP.then((list) => { channels = list; draw(); });
  draw();
  return h('div', { class: 'msg-editor' },
    h('div', { class: 'row wrap mb-2' }, varsMenu, blocksMenu, mentionMenu, h('span', { class: 'spacer' }), h('span', { class: 'xsmall muted' }, t('mon.msg.toolbar_hint'))),
    h('div', { class: 'msg-grid' },
      h('div', { class: 'col', style: { gap: '4px' } }, ta),
      h('div', { class: 'col', style: { gap: '6px' } }, h('div', { class: 'row wrap' }, h('span', { class: 'small text-2' }, t('mon.msg.preview')), h('span', { class: 'spacer' }), pillSlot), out, mentionInfo)));
}

// ── Catalogue « Depuis un modèle » (§ 19) ─────────────────────────────────
function normVars(v) {
  const one = (name, d) => {
    if (d && typeof d === 'object' && !Array.isArray(d)) {
      return { name, label: d.label || d.title || name, def: d.default !== undefined ? d.default : '', hint: d.description || d.hint || '', options: Array.isArray(d.options || d.choices) ? (d.options || d.choices) : null, type: d.type || (typeof d.default === 'number' ? 'number' : 'text'), required: d.required === true };
    }
    return { name, label: name, def: d === undefined || d === null ? '' : d, hint: '', options: null, type: typeof d === 'number' ? 'number' : 'text', required: false };
  };
  if (Array.isArray(v)) return v.map((x) => (typeof x === 'string' ? one(x, '') : one(x.name || x.key, x)));
  if (v && typeof v === 'object') return Object.entries(v).map(([k, d]) => one(k, d));
  return [];
}

function normTemplate(x) {
  return {
    key: x.key || x.id || x.template || x.slug || x.name,
    title: x.title || x.label || x.name || x.key || '',
    family: x.family || x.category || x.group || 'other',
    description: x.description || x.summary || '',
    type: x.type || (x.monitor && x.monitor.type) || '',
    priority: x.priority !== undefined ? x.priority : (x.monitor && x.monitor.priority),
    vars: normVars(x.vars || x.variables),
  };
}

export function familyLabel(f) { return has('tpl.family.' + f) ? t('tpl.family.' + f) : String(f); }

export function openTemplateCatalog(ctx, { onPrefill }) {
  const body = h('div', { class: 'col gap-3' }, h('div', { class: 'state sm' }, h('span', { class: 'spinner' })));
  const m = modal({ title: t('tpl.title'), body, wide: true });
  let all = [];
  let q = '';
  function drawList() {
    clear(body);
    const search = searchInput({ placeholder: t('tpl.search_ph'), value: q, slash: false, onInput: (v) => { q = v; drawCards(); } });
    const cards = h('div');
    body.appendChild(h('p', { class: 'small text-2' }, t('tpl.intro')));
    body.appendChild(search);
    body.appendChild(cards);
    function drawCards() {
      clear(cards);
      const words = q.toLowerCase().split(/\s+/).filter(Boolean);
      const list = all.filter((x) => words.every((w) => [x.title, x.description, x.type, familyLabel(x.family)].join(' ').toLowerCase().includes(w)));
      if (!list.length) { cards.appendChild(empty(null, t('tpl.no_match'))); return; }
      const fams = Array.from(new Set(list.map((x) => x.family)));
      for (const f of fams) {
        cards.appendChild(h('h4', { class: 'tpl-family' }, familyLabel(f)));
        cards.appendChild(h('div', { class: 'tpl-grid' }, list.filter((x) => x.family === f).map((x) => h('button', { type: 'button', class: 'tpl-card', onclick: () => drawVars(x) },
          h('div', { class: 'row' }, h('b', { class: 'grow' }, x.title), priorityBadge(x.priority, { hideNone: true })),
          x.description ? h('span', { class: 'small text-2' }, x.description) : null,
          x.type ? h('span', { class: 'badge muted' }, has('monitors.type.' + x.type) ? t('monitors.type.' + x.type) : x.type) : null))));
      }
    }
    drawCards();
    setTimeout(() => search.input.focus(), 0);
  }
  function drawVars(tp) {
    clear(body);
    const vals = {};
    const err = h('div', { class: 'callout crit', hidden: true });
    const grid = h('div', { class: 'form-grid' });
    for (const v of tp.vars) {
      vals[v.name] = v.def;
      let ctl;
      if (v.options) {
        ctl = select(v.options.map((o) => (typeof o === 'object' ? { value: o.value, label: o.label || String(o.value) } : { value: o, label: String(o) })), v.def, (x) => { vals[v.name] = v.type === 'number' ? Number(x) : x; });
      } else {
        ctl = h('input', { class: ['input', v.type === 'number' ? null : 'mono'], type: v.type === 'number' ? 'number' : 'text', step: 'any', value: v.def === null ? '' : v.def });
        ctl.addEventListener('input', () => { vals[v.name] = v.type === 'number' ? (ctl.value === '' ? null : Number(ctl.value)) : ctl.value; });
      }
      grid.appendChild(field(v.label + (v.required ? ' *' : ''), ctl, v.hint || null));
    }
    const create = h('button', { class: 'btn primary', type: 'button' }, icon('plus', 14), t('tpl.create'));
    body.appendChild(h('div', { class: 'row' }, h('button', { class: 'btn sm ghost', type: 'button', onclick: drawList }, icon('chevronLeft', 13), t('tpl.back')),
      h('b', { class: 'grow' }, tp.title), tp.type ? h('span', { class: 'badge muted' }, has('monitors.type.' + tp.type) ? t('monitors.type.' + tp.type) : tp.type) : null));
    if (tp.description) body.appendChild(h('p', { class: 'small text-2' }, tp.description));
    body.appendChild(tp.vars.length ? grid : h('div', { class: 'small muted' }, t('tpl.no_vars')));
    body.appendChild(h('div', { class: 'callout' }, icon('info', 16), h('span', { class: 'small' }, t('tpl.after_hint'))));
    body.appendChild(err);
    body.appendChild(h('div', { class: 'row' }, h('span', { class: 'spacer' }), create));
    create.addEventListener('click', async () => {
      const missing = tp.vars.filter((v) => v.required && (vals[v.name] === '' || vals[v.name] === null || vals[v.name] === undefined));
      if (missing.length) { clear(err); err.appendChild(icon('warn', 16)); err.appendChild(h('span', { class: 'small' }, t('form.required', { field: missing.map((v) => v.label).join(', ') }))); err.hidden = false; return; }
      create.disabled = true;
      err.hidden = true;
      try {
        const r = await post('/monitors/from-template', { template: tp.key, vars: vals });
        const mon = r && r.monitor ? r.monitor : r;
        if (mon && mon.id !== undefined && mon.id !== null) {
          m.close();
          toast(t('tpl.created'), 'ok');
          ctx.navigate('/monitors/' + mon.id + '/edit');
        } else if (mon && mon.type) {
          m.close();
          toast(t('tpl.prefilled'), 'ok');
          onPrefill(mon);
        } else throw new Error(t('err.unknown'));
      } catch (e) {
        create.disabled = false;
        clear(err); err.appendChild(icon('warn', 16)); err.appendChild(h('span', { class: 'small' }, errorMessage(e))); err.hidden = false;
      }
    });
  }
  get('/monitors/templates', null, { signal: ctx.signal }).then((r) => {
    all = asList(r, 'templates').map(normTemplate).filter((x) => x.key);
    if (!all.length) { clear(body); body.appendChild(empty(t('tpl.none_title'), t('tpl.none_text'))); return; }
    drawList();
  }).catch((e) => {
    if (isAbort(e)) return;
    clear(body);
    body.appendChild(routeMissing(e) ? h('div', { class: 'callout' }, icon('info', 16), h('span', { class: 'small' }, t('ops.err.route_missing'))) : errorState(e));
  });
  return m;
}

// Moniteurs pour les listes (composite, dépendances) : état et priorité.
export function monitorPickList(monitors, { selfId, onPick }) {
  const box = h('div', { class: 'pick-list' });
  let q = '';
  const listEl = h('div');
  function draw() {
    clear(listEl);
    const words = q.toLowerCase().split(/\s+/).filter(Boolean);
    const list = monitors.filter((x) => String(x.id) !== String(selfId) && words.every((w) => (String(x.name) + ' #' + x.id).toLowerCase().includes(w)));
    if (!list.length) { listEl.appendChild(h('div', { class: 'small muted', style: { padding: '8px' } }, t('monitors.no_match'))); return; }
    for (const x of list.slice(0, 200)) {
      listEl.appendChild(h('button', { type: 'button', class: 'pick-row btnlike', onclick: () => onPick(x) },
        h('span', { class: 'mono xsmall muted' }, '#' + x.id), badgeState(monitorState(x)), priorityBadge(x.priority, { hideNone: true }), h('span', { class: 'ellipsis' }, x.name || '')));
    }
  }
  const s2 = searchInput({ placeholder: t('mon.comp.search'), slash: false, onInput: (v) => { q = v; draw(); } });
  box.appendChild(s2);
  box.appendChild(listEl);
  draw();
  return box;
}
