// Exploitation (§§ 16, 17, 20, 28) : priorités, rôles, durées qui courent, acquittement, maintenance
// rapide, bandeau des maintenances, incident depuis une alerte, proposition de réparation.
import { h, clear, icon } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, post, del, asList, pool, isAbort } from '../api.js';
import { fmtDuration, fmtDateTime, toMs, isNum, toLocalInput, fromLocalInput } from '../format.js';
import { modal, field, select, toast, errorMessage, confirmDialog, errorState, pills } from '../components/ui.js';
import { explainEnabled, openExplain, idValue } from '../components/explain.js';
import { confirmAndRun, ACTION_KINDS } from '../components/remote-action.js';

// ── Rôles (CONTRAT-04 : viewer lit, member écrit, admin gère) ─────────────
export function canWrite(me) { return ['owner', 'admin', 'member'].includes(me && me.role); }
export function isAdmin(me) { return ['owner', 'admin'].includes(me && me.role); }

// ── Erreurs : une liste ne rend jamais 404, sauf si la route manque au serveur ──
export function routeMissing(e) { return !!(e && (e.status === 404 || e.status === 405 || e.status === 501)); }

export function listErrorState(e, retry) {
  if (routeMissing(e)) {
    return h('div', { class: 'state error' },
      h('div', t('ops.err.route_missing')),
      h('div', { class: 'detail' }, t('ops.err.route_missing_hint')),
      retry ? h('div', { class: 'actions' }, h('button', { class: 'btn sm', type: 'button', onclick: retry }, t('action.retry'))) : null);
  }
  return errorState(e, retry);
}

// ── Priorités P1 … P5 (Zabbix : sévérités ; NULL = non classée) ───────────
export const PRIORITIES = [1, 2, 3, 4, 5];

export function prioNum(p) {
  const n = Number(p);
  return Number.isInteger(n) && n >= 1 && n <= 5 ? n : null;
}

export function priorityBadge(p, opts = {}) {
  const n = prioNum(p);
  if (n === null) return opts.hideNone ? null : h('span', { class: 'prio p0', title: t('prio.none_title') }, t('prio.none'));
  return h('span', { class: ['prio', 'p' + n], title: t('prio.name.' + n) }, 'P' + n);
}

export function priorityLabel(p) {
  const n = prioNum(p);
  return n === null ? t('prio.none_title') : 'P' + n + ' · ' + t('prio.name.' + n);
}

export function prioritySelect(value, onChange, attrs) {
  return select([{ value: '', label: t('prio.none_title') }].concat(PRIORITIES.map((n) => ({ value: n, label: priorityLabel(n) }))),
    prioNum(value) === null ? '' : prioNum(value), (v) => onChange(v === '' ? null : Number(v)), attrs);
}

export function severityBadge(sev) {
  const n = prioNum(sev) || 3;
  return h('span', { class: ['prio', 'sev', 'p' + n], title: t('inc.sev.' + n) }, 'SEV-' + n);
}

// ── Durée qui court : un seul minuteur pour toute l'application ───────────
let ticker = null;
function tickAll() {
  const els = document.querySelectorAll('[data-live-since]');
  if (!els.length) return;
  const now = Date.now();
  els.forEach((el) => {
    const since = Number(el.getAttribute('data-live-since'));
    if (Number.isFinite(since)) el.textContent = fmtLive(now - since);
  });
}
export function fmtLive(ms) {
  if (!isNum(ms) || ms < 0) return fmtDuration(0);
  return fmtDuration(ms < 60000 ? Math.round(ms / 1000) * 1000 : ms);
}
export function liveDuration(since, opts = {}) {
  const ms = toMs(since);
  if (ms === null) return h('span', { class: 'muted' }, '-');
  if (!ticker) ticker = setInterval(tickAll, 1000);
  return h('span', { class: ['live-dur', 'num', opts.cls || null], 'data-live-since': String(ms), title: opts.title || fmtDateTime(ms) }, fmtLive(Date.now() - ms));
}

// ── Groupes d'alerte : « host:web-01,unit:nginx.service » ──────────────────
export function groupTags(group) {
  const out = {};
  const g = String(group || '');
  if (!g || g === '*') return out;
  for (const part of g.split(',')) {
    const i = part.indexOf(':');
    if (i > 0) out[part.slice(0, i).trim()] = part.slice(i + 1).trim();
  }
  return out;
}

export function problemHost(p) {
  if (p && p.host) return typeof p.host === 'object' ? (p.host.hostname || p.host.name || '') : String(p.host);
  return groupTags(p && p.group).host || '';
}

export function problemKey(p) { return String(p.monitor_id) + '|' + String(p.group || '*'); }

// ── Acquittement (§ 17) ───────────────────────────────────────────────────
export function ackedInfo(p) {
  const a = p && p.acked;
  if (!a) return null;
  if (typeof a === 'object') return { by: a.by || a.email || a.name || '', at: a.at, comment: a.comment || '' };
  return { by: p.acked_by_name || p.acked_by || '', at: p.acked_at, comment: p.ack_comment || '' };
}

export function ackDialog(items, onDone) {
  const comment = h('textarea', { class: 'input', rows: 3, maxlength: 2000, placeholder: t('ack.comment_ph') });
  const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
  const btn = h('button', { class: 'btn primary', type: 'button' }, icon('check', 14), t('ack.do'));
  const list = items.slice(0, 6).map((it) => h('li', h('b', it.monitor_name || '#' + it.monitor_id), ' ', h('span', { class: 'mono small muted' }, it.group || '*')));
  const m = modal({
    title: tn('ack.title', items.length),
    body: h('div', { class: 'col gap-3' },
      h('p', { class: 'small text-2' }, t('ack.text')),
      h('ul', { class: 'list-plain ack-list' }, list, items.length > 6 ? h('li', { class: 'muted small' }, t('ack.more', { n: items.length - 6 })) : null),
      field(t('ack.comment'), comment, t('ack.comment_hint')), err),
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), btn],
  });
  btn.addEventListener('click', async () => {
    btn.disabled = true;
    err.hidden = true;
    const res = await pool(items, 4, (it) => post('/monitors/' + encodeURIComponent(it.monitor_id) + '/groups/ack', { group: it.group || '*', comment: comment.value.trim() }));
    const failed = res.filter((r) => r instanceof Error);
    if (failed.length === items.length) {
      btn.disabled = false;
      err.textContent = errorMessage(failed[0]);
      err.hidden = false;
      return;
    }
    m.close();
    if (failed.length) toast(t('ack.partial', { ok: items.length - failed.length, ko: failed.length }), 'error');
    else toast(tn('ack.done', items.length), 'ok');
    if (onDone) onDone();
  });
  return m;
}

export async function unackItems(items, onDone) {
  const res = await pool(items, 4, (it) => del('/monitors/' + encodeURIComponent(it.monitor_id) + '/groups/ack', { query: { group: it.group || '*' } }));
  const failed = res.filter((r) => r instanceof Error);
  if (failed.length) toast(errorMessage(failed[0]), 'error');
  else toast(tn('ack.removed', items.length), 'ok');
  if (onDone) onDone();
}

// ── Maintenances (§ 16) ───────────────────────────────────────────────────
const QUICK = [['1h', 3600], ['4h', 4 * 3600], ['1d', 86400]];

// Maintenance immédiate : 1 h, 4 h, 1 j ou personnalisée. scope = {monitor_ids, tags, groups}.
export function downtimeDialog(ctx, { name, scope, subject, onDone }) {
  let mode = '1h';
  const nameIn = h('input', { class: 'input', maxlength: 200, value: name || '' });
  const msg = h('textarea', { class: 'input', rows: 2, maxlength: 2000, placeholder: t('dt.f.message_ph') });
  const startIn = h('input', { class: 'input', type: 'datetime-local', value: toLocalInput(Date.now()) });
  const endIn = h('input', { class: 'input', type: 'datetime-local', value: toLocalInput(Date.now() + 2 * 3600e3) });
  const custom = h('div', { class: 'form-grid', hidden: true }, field(t('dt.f.start'), startIn), field(t('dt.f.end'), endIn));
  const pillSlot = h('div');
  const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
  const drawPills = () => {
    clear(pillSlot);
    pillSlot.appendChild(pills(QUICK.map(([k]) => ({ key: k, label: t('dt.quick.' + k) })).concat([{ key: 'custom', label: t('dt.quick.custom') }]), mode,
      (k) => { mode = k; custom.hidden = k !== 'custom'; drawPills(); }));
  };
  drawPills();
  const btn = h('button', { class: 'btn primary', type: 'button' }, icon('wrench', 14), t('dt.put'));
  const plan = h('a', {
    class: 'small', href: ctx.href('/downtimes/new', { scope: JSON.stringify(scope || {}), name: name || '' }),
    onclick: () => m.close(),
  }, t('dt.plan_recurring'));
  const m = modal({
    title: t('dt.quick_title'),
    body: h('div', { class: 'col gap-3' },
      subject ? h('div', { class: 'callout' }, icon('wrench', 16), h('div', { class: 'small' }, h('div', t('dt.quick_scope')), h('b', subject))) : null,
      h('p', { class: 'small text-2' }, t('dt.quick_text')),
      field(t('dt.f.duration'), pillSlot), custom,
      field(t('dt.f.name'), nameIn), field(t('dt.f.message'), msg, t('dt.f.message_hint')), err, plan),
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), btn],
  });
  btn.addEventListener('click', async () => {
    err.hidden = true;
    const body = { name: nameIn.value.trim() || name || t('dt.default_name'), message: msg.value.trim(), scope: scope || {}, enabled: true };
    if (mode === 'custom') {
      const a = fromLocalInput(startIn.value);
      const b = fromLocalInput(endIn.value);
      if (!a || !b || b <= a) { err.textContent = t('dt.err.end_before'); err.hidden = false; return; }
      body.start_ts = a;
      body.end_ts = b;
    } else {
      body.duration_s = QUICK.find(([k]) => k === mode)[1];
    }
    btn.disabled = true;
    try {
      const r = await post('/downtimes', body);
      m.close();
      const end = r && (toMs(r.next_end) || toMs(r.end_ts));
      toast(end ? t('dt.put_done_until', { d: fmtDateTime(end) }) : t('dt.put_done'), 'ok');
      if (onDone) onDone(r);
    } catch (e) { btn.disabled = false; err.textContent = errorMessage(e); err.hidden = false; }
  });
  return m;
}

export function downtimeButton(ctx, opts) {
  if (!canWrite(ctx.me)) return null;
  return h('button', { class: 'btn sm', type: 'button', title: t('dt.put_title'), onclick: () => downtimeDialog(ctx, Object.assign({ onDone: () => ctx.refresh() }, opts)) },
    icon('wrench', 13), t('dt.put'));
}

function scopeOf(dt) {
  const s2 = dt && dt.scope && typeof dt.scope === 'object' ? dt.scope : {};
  return {
    monitor_ids: Array.isArray(s2.monitor_ids) ? s2.monitor_ids.map(String) : [],
    tags: s2.tags && typeof s2.tags === 'object' ? s2.tags : {},
    groups: Array.isArray(s2.groups) ? s2.groups.map(String) : [],
  };
}

// Couverture d'un hôte : 'full' (tout l'hôte), 'partial' (une partie de ses moniteurs ou groupes) ou null.
export function coversHost(dt, hostname, hostTags) {
  const sc = scopeOf(dt);
  const tags = Object.assign({}, hostTags || {}, { host: hostname });
  const tagKeys = Object.keys(sc.tags);
  const tagsOk = tagKeys.every((k) => String(tags[k]) === String(sc.tags[k]));
  const groupsHit = sc.groups.some((g) => groupTags(g).host === hostname);
  if (tagKeys.length && !tagsOk) return groupsHit ? 'partial' : null;
  if (sc.groups.length) return groupsHit ? 'partial' : null;
  if (!tagKeys.length && !sc.groups.length && !sc.monitor_ids.length) return 'full';
  if (tagKeys.length) return sc.monitor_ids.length ? 'partial' : 'full';
  return sc.monitor_ids.length ? 'partial' : null;
}

// Couverture d'un moniteur : 'full', 'partial' (certains groupes) ou null.
export function coversMonitor(dt, monitorId) {
  const sc = scopeOf(dt);
  if (sc.monitor_ids.length && !sc.monitor_ids.includes(String(monitorId))) return null;
  if (Object.keys(sc.tags).length || sc.groups.length) return 'partial';
  return 'full';
}

// Bandeau « en maintenance » : rien si aucune maintenance active ne couvre l'objet.
export function downtimeBanner(ctx, matcher) {
  const box = h('div', { class: 'dt-banner-slot' });
  get('/downtimes/active', null, { signal: ctx.signal }).then((resp) => {
    const list = asList(resp, 'downtimes').filter((d) => d.enabled !== false);
    const hits = [];
    for (const d of list) { const cov = matcher(d); if (cov) hits.push({ d, cov }); }
    if (!hits.length || !box.isConnected) return;
    hits.sort((a, b) => (a.cov === 'full' ? 0 : 1) - (b.cov === 'full' ? 0 : 1));
    for (const { d, cov } of hits.slice(0, 3)) {
      const end = toMs(d.next_end) || toMs(d.end_ts);
      box.appendChild(h('div', { class: ['callout', 'dt-banner', cov === 'full' ? 'full' : null] }, icon('wrench', 16),
        h('div', { class: 'grow small' },
          h('b', t(cov === 'full' ? 'dt.banner.full' : 'dt.banner.partial')), ' ',
          h('span', d.name || ''), ' · ',
          end ? t('dt.banner.until', { d: fmtDateTime(end) }) : t('dt.banner.no_end'),
          d.message ? h('div', { class: 'muted mt-2' }, d.message) : null),
        h('a', { class: 'btn sm ghost', href: ctx.href('/downtimes/' + d.id + '/edit') }, t('dt.banner.open'))));
    }
  }).catch(() => { /* bandeau facultatif : la page reste utilisable */ });
  return box;
}

// ── Incident depuis une alerte (§ 20) ─────────────────────────────────────
export async function incidentFromAlert(ctx, p) {
  if (p.incident_id) { ctx.navigate('/incidents/' + p.incident_id); return; }
  const ok = await confirmDialog({
    title: t('inc.from_alert_title'),
    text: t('inc.from_alert_text', { name: p.monitor_name || '#' + p.monitor_id, group: p.group || '*' }),
    confirmLabel: t('inc.declare'),
  });
  if (!ok) return;
  try {
    const r = await post('/incidents/from-alert', { monitor_id: idValue(p.monitor_id), group: p.group || '*' });
    const id = r && (r.id || (r.incident && r.incident.id));
    toast(t('inc.declared', { n: r && r.number ? 'INC-' + r.number : '' }), 'ok');
    if (id) ctx.navigate('/incidents/' + id); else ctx.navigate('/incidents');
  } catch (e) { toast(errorMessage(e), 'error'); }
}

// ── Proposer une réparation (§ 28) ────────────────────────────────────────
async function findHost(hostname, hostId, signal) {
  if (hostId !== null && hostId !== undefined && hostId !== '') {
    try { const r = await get('/hosts/' + encodeURIComponent(hostId), null, { signal }); return r && r.host && typeof r.host === 'object' ? Object.assign({}, r, r.host) : r; } catch (e) { if (isAbort(e)) throw e; }
  }
  if (!hostname) return null;
  const list = asList(await get('/hosts', { q: hostname }, { signal }), 'hosts');
  const hit = list.find((x) => String(x.hostname).toLowerCase() === String(hostname).toLowerCase());
  if (!hit) return null;
  try { const r = await get('/hosts/' + encodeURIComponent(hit.id), null, { signal }); return r && r.host && typeof r.host === 'object' ? Object.assign({}, r, r.host) : r; } catch (e) { return hit; }
}

export function actionCaps(host) {
  const agent = host && host.agent && typeof host.agent === 'object' ? host.agent : {};
  const caps = agent.capabilities || host.capabilities || {};
  const a = caps && caps.actions && typeof caps.actions === 'object' ? caps.actions : null;
  return a ? { enabled: a.enabled === true, kinds: Array.isArray(a.kinds) ? a.kinds : [], scripts: Array.isArray(a.scripts) ? a.scripts : [] } : null;
}

export function repairDialog(ctx, p) {
  const hostname = problemHost(p);
  const gt = groupTags(p.group);
  const body = h('div', { class: 'col gap-3' }, h('div', { class: 'state sm' }, h('span', { class: 'spinner' })));
  const go = h('button', { class: 'btn primary', type: 'button', disabled: true }, icon('play', 13), t('act.propose'));
  const m = modal({
    title: t('act.repair_title'), body, wide: false,
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), go],
  });
  const ctrl = new AbortController();
  (async () => {
    let host = null;
    try { host = await findHost(hostname, p.host_id, ctrl.signal); } catch (e) { if (isAbort(e)) return; }
    clear(body);
    if (!host) {
      body.appendChild(h('div', { class: 'callout warn' }, icon('warn', 16), h('span', { class: 'small' }, hostname ? t('act.err.host_unknown', { host: hostname }) : t('act.err.no_host'))));
      return;
    }
    const caps = actionCaps(host);
    const isWin = String(host.os || '').toLowerCase() === 'windows';
    const kinds = ACTION_KINDS.filter((k) => k !== 'update_agent' && (!caps || !caps.kinds.length || caps.kinds.includes(k)));
    let kind = kinds.includes('restart_service') ? 'restart_service' : kinds[0] || 'restart_service';
    const unitDefault = gt.unit || gt.windows_service || gt.service || gt.process || '';
    const unitIn = h('input', { class: 'input mono', value: unitDefault, placeholder: isWin ? 'W3SVC' : 'nginx.service' });
    const scripts = caps ? caps.scripts : [];
    const scriptSel = select(scripts.length ? scripts.map((s2) => ({ value: s2, label: s2 })) : [{ value: '', label: t('act.no_scripts') }], scripts[0] || '');
    const reason = h('textarea', { class: 'input', rows: 2, maxlength: 1000 });
    reason.value = t('act.reason_default', { name: p.monitor_name || '#' + p.monitor_id, group: p.group || '*' });
    const paramSlot = h('div');
    const drawParam = () => {
      clear(paramSlot);
      if (kind === 'run_script') paramSlot.appendChild(field(t('act.f.script'), scriptSel, t('act.f.script_hint')));
      else paramSlot.appendChild(field(t(isWin ? 'act.f.service' : 'act.f.unit'), unitIn, t('act.f.unit_hint')));
    };
    drawParam();
    body.appendChild(h('div', { class: 'kv' }, h('dt', t('col.host')), h('dd', h('b', host.hostname || hostname)), h('dt', t('col.os')), h('dd', [host.os, host.platform].filter(Boolean).join(' / ') || '-')));
    if (!caps || !caps.enabled) {
      body.appendChild(h('div', { class: 'callout warn' }, icon('warn', 16), h('div', { class: 'small' },
        h('div', t('act.caps_off', { host: host.hostname || hostname })), h('pre', { class: 'mono xsmall mt-2', style: { margin: '6px 0 0', whiteSpace: 'pre' } }, 'actions:\n  enabled: true\n  allow: [restart_service, start_service, stop_service]'))));
    }
    body.appendChild(field(t('act.f.kind'), select(kinds.map((k) => ({ value: k, label: t('act.kind.' + k) })), kind, (v) => { kind = v; drawParam(); })));
    body.appendChild(paramSlot);
    body.appendChild(field(t('act.f.reason'), reason));
    if (explainEnabled(ctx.me)) {
      body.appendChild(h('div', { class: 'small text-2' }, t('act.ask_ai_hint'), ' ',
        h('button', { class: 'btn sm explain-btn', type: 'button', onclick: () => { m.close(); openExplain({ kind: 'alert', monitor_id: idValue(p.monitor_id), group: p.group || '*' }, { subject: (p.monitor_name || '') + ' (' + (p.group || '*') + ')', me: ctx.me }); } },
          icon('spark', 13), t('explain.button'))));
    }
    go.disabled = !canWrite(ctx.me);
    if (!canWrite(ctx.me)) go.title = t('err.403');
    go.addEventListener('click', () => {
      const params = kind === 'run_script' ? { script: scriptSel.value } : (isWin ? { service: unitIn.value.trim() } : { unit: unitIn.value.trim() });
      const val = kind === 'run_script' ? params.script : (params.unit || params.service);
      if (!val) { toast(t('form.required', { field: t(kind === 'run_script' ? 'act.f.script' : isWin ? 'act.f.service' : 'act.f.unit') }), 'error'); return; }
      m.close();
      confirmAndRun(ctx.me, {
        host_id: idValue(host.id), hostname: host.hostname || hostname, kind, params, reason: reason.value.trim(),
        origin: { monitor_id: idValue(p.monitor_id), group: p.group || '*' },
      });
    });
  })();
  return m;
}
