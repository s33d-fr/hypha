// Actions à distance (§ 28) : geste nommé, confirmation, création (approuvée d'un clic par un
// administrateur), suivi en direct (en attente → envoyée → en cours → réussie/échouée) et sortie.
import { h, clear, icon } from '../dom.js';
import { t } from '../i18n.js';
import { get, post, asList, isAbort } from '../api.js';
import { href } from '../router.js';
import { fmtDateTime, fmtDuration, toMs } from '../format.js';
import { modal, errorMessage, copyButton } from './ui.js';

export const ACTION_KINDS = ['restart_service', 'start_service', 'stop_service', 'run_script', 'update_agent'];
export const FINAL = ['succeeded', 'failed', 'rejected', 'expired', 'refused_by_agent'];
const STEPS = ['pending_approval', 'approved', 'sent', 'running', 'done'];
const POLL_MS = 2000;

const isAdminRole = (me) => ['owner', 'admin'].includes(me && me.role);

export function actionTarget(a) {
  const p = (a && a.params) || {};
  return p.unit || p.service || p.name || p.script || p.version || '';
}

export function actionLabel(a, hostname) {
  const host = hostname || (a && (a.hostname || (a.host && a.host.hostname))) || '';
  const kind = a && ACTION_KINDS.includes(a.kind) ? a.kind : 'unknown';
  return t('act.label.' + kind + (host ? '' : '_nohost'), { target: actionTarget(a), host });
}

export function isFinal(status) { return FINAL.includes(status); }

export function actionStatusBadge(status) {
  const map = {
    pending_approval: 'warn', approved: 'info', sent: 'info', running: 'info',
    succeeded: 'ok', failed: 'alert', rejected: 'muted', expired: 'muted', refused_by_agent: 'alert',
  };
  const cls = map[status] || 'muted';
  return h('span', { class: ['badge', cls] }, ['sent', 'running', 'approved'].includes(status) ? h('span', { class: 'spinner xs' }) : null, t('act.status.' + (map[status] ? status : 'unknown')));
}

export async function resolveHostId(hostname, signal) {
  if (!hostname) return null;
  const list = asList(await get('/hosts', { q: hostname }, { signal }), 'hosts');
  const hit = list.find((x) => String(x.hostname).toLowerCase() === String(hostname).toLowerCase());
  return hit ? hit.id : null;
}

function actionErrorText(e) {
  if (e && e.status === 409) return t('act.err.409') + (e.detail ? ' (' + e.detail + ')' : '');
  if (e && e.status === 403) return t('act.err.403');
  return errorMessage(e);
}

// Crée l'action ; un administrateur l'approuve aussitôt (« valider d'un clic »).
export async function createAction(me, body) {
  const created = await post('/actions', body);
  const a = created && created.action ? created.action : created;
  if (!a || a.id === undefined) return a;
  if (isAdminRole(me) && a.status === 'pending_approval') {
    try {
      const ap = await post('/actions/' + encodeURIComponent(a.id) + '/approve', {});
      return (ap && ap.action) || ap || a;
    } catch (e) {
      a._approveError = e;
      return a;
    }
  }
  return a;
}

function stepState(status, step) {
  const failedAt = { rejected: 'pending_approval', expired: null, refused_by_agent: 'running', failed: 'running' };
  const order = STEPS.indexOf(step);
  if (status === 'succeeded') return 'done';
  if (FINAL.includes(status)) {
    let at = failedAt[status];
    if (status === 'expired') at = 'sent';
    const ai = STEPS.indexOf(at);
    if (order < ai) return 'done';
    if (order === ai) return 'failed';
    return 'todo';
  }
  const cur = STEPS.indexOf(status === 'approved' ? 'sent' : status);
  if (order < cur) return 'done';
  if (order === cur) return 'current';
  return 'todo';
}

// Suivi en direct d'une action : interroge GET /actions/{id} toutes les 2 s jusqu'à l'état final.
export function actionTracker(action, opts = {}) {
  const el = h('div', { class: 'act-track', role: 'status', 'aria-live': 'polite' });
  let cur = action;
  let timer = null;
  let stopped = false;
  const stop = () => { stopped = true; if (timer) clearTimeout(timer); timer = null; };

  function draw() {
    clear(el);
    const st = cur.status || 'pending_approval';
    el.appendChild(h('div', { class: 'row wrap' }, h('b', actionLabel(cur, opts.hostname)), h('span', { class: 'spacer' }), actionStatusBadge(st)));
    const steps = h('ol', { class: 'act-steps' });
    for (const s2 of STEPS) {
      const state = stepState(st, s2);
      const label = s2 === 'done' ? t(st === 'succeeded' ? 'act.step.succeeded' : isFinal(st) ? 'act.status.' + st : 'act.step.done') : t('act.step.' + s2);
      steps.appendChild(h('li', { class: state }, h('span', { class: 'b' }, state === 'done' ? icon('check', 11) : state === 'failed' ? icon('x', 11) : null), h('span', label)));
    }
    el.appendChild(steps);
    const meta = [];
    if (cur.created_at) meta.push(t('act.meta.created', { d: fmtDateTime(cur.created_at) }));
    if (cur.approved_at) meta.push(t('act.meta.approved', { d: fmtDateTime(cur.approved_at) }));
    if (cur.finished_at) meta.push(t('act.meta.finished', { d: fmtDateTime(cur.finished_at) }));
    const a0 = toMs(cur.sent_at) || toMs(cur.approved_at);
    const a1 = toMs(cur.finished_at);
    if (a0 && a1 && a1 >= a0) meta.push(t('act.meta.took', { d: fmtDuration(a1 - a0) }));
    if (cur.exit_code !== null && cur.exit_code !== undefined) meta.push(t('act.meta.exit', { n: cur.exit_code }));
    if (meta.length) el.appendChild(h('div', { class: 'xsmall muted' }, meta.join(' · ')));
    if (st === 'pending_approval') el.appendChild(h('div', { class: 'callout mt-2' }, icon('info', 16), h('span', { class: 'small' }, t('act.waiting_approval'))));
    if (cur._approveError) el.appendChild(h('div', { class: 'callout crit mt-2' }, icon('warn', 16), h('span', { class: 'small' }, t('act.err.approve') + ' ' + actionErrorText(cur._approveError))));
    if (st === 'expired') el.appendChild(h('div', { class: 'callout warn mt-2' }, icon('clock', 16), h('span', { class: 'small' }, t('act.expired_text'))));
    const out = typeof cur.output === 'string' ? cur.output : '';
    if (out || isFinal(st) || st === 'running') {
      el.appendChild(h('div', { class: 'act-out-head' }, h('span', { class: 'small text-2' }, t('act.output')), h('span', { class: 'spacer' }), out ? copyButton(() => out, { label: false }) : null));
      el.appendChild(h('pre', { class: 'act-out' }, out || (st === 'running' ? t('act.output_wait') : t('act.output_none'))));
    }
  }

  let polls = 0;
  async function poll() {
    if (stopped) return;
    // Fenêtre fermée ou page quittée : on cesse d'interroger le serveur.
    if (!el.isConnected && polls > 0) { stop(); return; }
    polls++;
    if (isFinal(cur.status)) { if (opts.onFinal) opts.onFinal(cur); return; }
    try {
      const r = await get('/actions/' + encodeURIComponent(cur.id), null, { signal: opts.signal });
      const next = (r && r.action) || r;
      if (next && typeof next === 'object') { const ae = cur._approveError; cur = Object.assign({}, next); if (ae && cur.status === 'pending_approval') cur._approveError = ae; }
      draw();
      if (opts.onUpdate) opts.onUpdate(cur);
    } catch (e) {
      if (isAbort(e)) { stop(); return; }
    }
    if (isFinal(cur.status)) { if (opts.onFinal) opts.onFinal(cur); return; }
    timer = setTimeout(poll, POLL_MS);
  }

  draw();
  if (cur && cur.id !== undefined) timer = setTimeout(poll, opts.immediate ? 0 : POLL_MS);
  el.stop = stop;
  return el;
}

// Boîte de confirmation (geste, hôte, raison), puis suivi dans la même fenêtre.
// spec : {host_id, hostname, kind, params, reason, origin, label, proposed_by_ai}
export function confirmAndRun(me, spec) {
  const admin = isAdminRole(me);
  const reason = h('textarea', { class: 'input', rows: 2, maxlength: 1000 });
  reason.value = spec.reason || '';
  const err = h('div', { class: 'callout crit', hidden: true });
  const label = spec.label || actionLabel(spec, spec.hostname);
  const body = h('div', { class: 'col gap-3' },
    h('div', { class: 'act-confirm' }, icon(spec.kind === 'run_script' ? 'play' : 'refresh', 18), h('div', h('b', label),
      h('div', { class: 'small text-2' }, t('act.kind.' + spec.kind)))),
    h('dl', { class: 'kv' },
      h('dt', t('col.host')), h('dd', h('b', spec.hostname || (spec.host_id !== undefined ? '#' + spec.host_id : '-'))),
      h('dt', t('act.f.kind')), h('dd', t('act.kind.' + spec.kind)),
      h('dt', t('act.f.target')), h('dd', h('span', { class: 'mono small' }, actionTarget(spec) || '-'))),
    h('label', { class: 'field' }, h('span', { class: 'lbl' }, t('act.f.reason')), reason),
    h('div', { class: 'callout' }, icon('shield', 16), h('span', { class: 'small' }, t(admin ? 'act.confirm_admin' : 'act.confirm_member'))),
    err);
  const go = h('button', { class: 'btn primary', type: 'button' }, icon('check', 14), t(admin ? 'act.run' : 'act.propose'));
  const closeBtn = h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel'));
  let tracker = null;
  const m = modal({ title: t('act.confirm_title'), body, actions: [closeBtn, go], onClose: () => { if (tracker) tracker.stop(); } });
  go.addEventListener('click', async () => {
    err.hidden = true;
    go.disabled = true;
    let hostId = spec.host_id;
    try {
      if (hostId === null || hostId === undefined || hostId === '') hostId = await resolveHostId(spec.hostname);
      if (hostId === null || hostId === undefined) throw Object.assign(new Error('host'), { status: 0, code: 'host_unknown', detail: t('act.err.host_unknown', { host: spec.hostname || '' }) });
      const a = await createAction(me, {
        host_id: hostId, kind: spec.kind, params: spec.params || {}, reason: reason.value.trim(),
        origin: Object.assign({}, spec.origin || {}, spec.proposed_by_ai ? { explain: true } : {}),
      });
      clear(body);
      tracker = actionTracker(Object.assign({ kind: spec.kind, params: spec.params }, a || {}), { hostname: spec.hostname, immediate: true });
      body.appendChild(tracker);
      body.appendChild(h('div', { class: 'small' }, h('a', { href: href('/settings/actions'), onclick: () => m.close() }, t('act.see_all'))));
      go.remove();
      closeBtn.textContent = t('action.close');
    } catch (e) {
      go.disabled = false;
      clear(err);
      err.appendChild(icon('warn', 16));
      err.appendChild(h('span', { class: 'small' }, e && e.code === 'host_unknown' ? e.detail : actionErrorText(e)));
      err.hidden = false;
    }
  });
  return m;
}
