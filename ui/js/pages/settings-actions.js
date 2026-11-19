// Réglages › Actions (§ 28) : gestes à distance en attente d'approbation (Approuver / Rejeter),
// historique filtrable, détail avec suivi en direct et sortie de l'agent.
import { h, clear, icon } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, post, asList, isAbort } from '../api.js';
import { fmtDateTime, fmtRel, toMs } from '../format.js';
import { panel, empty, select, tabs, drawer, kv, toast, errorMessage, loading, modal, field } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { actionLabel, actionStatusBadge, actionTracker, isFinal, ACTION_KINDS } from '../components/remote-action.js';
import { isAdmin, canWrite, listErrorState } from './ops-shared.js';

const STATUSES = ['pending_approval', 'approved', 'sent', 'running', 'succeeded', 'failed', 'rejected', 'expired', 'refused_by_agent'];
const POLL_MS = 5000;

function originCell(a, href) {
  const o = a.origin && typeof a.origin === 'object' ? a.origin : {};
  const parts = [];
  if (a.proposed_by_ai || o.explain) parts.push(h('span', { class: 'badge info' }, icon('spark', 11), t('act.origin.ai')));
  if (o.monitor_id !== undefined && o.monitor_id !== null) parts.push(h('a', { class: 'small', href: href('/monitors/' + o.monitor_id) }, t('act.origin.monitor', { id: o.monitor_id })));
  if (o.group) parts.push(h('span', { class: 'mono xsmall muted' }, o.group));
  return parts.length ? h('div', { class: 'tags' }, parts) : h('span', { class: 'muted' }, t('act.origin.manual'));
}

async function decide(a, verdict, ctx, onDone) {
  if (verdict === 'reject') {
    const reason = h('textarea', { class: 'input', rows: 2, maxlength: 500, placeholder: t('act.reject_reason_ph') });
    const btn = h('button', { class: 'btn danger solid', type: 'button' }, t('act.reject'));
    const m = modal({
      title: t('act.reject_title'),
      body: h('div', { class: 'col gap-3' }, h('p', { class: 'small' }, actionLabel(a)), field(t('act.reject_reason'), reason)),
      actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), btn],
    });
    btn.addEventListener('click', async () => {
      btn.disabled = true;
      try { await post('/actions/' + encodeURIComponent(a.id) + '/reject', { reason: reason.value.trim() }); m.close(); toast(t('act.rejected_done'), 'ok'); onDone(); } catch (e) { btn.disabled = false; toast(errorMessage(e), 'error'); }
    });
    return;
  }
  try {
    await post('/actions/' + encodeURIComponent(a.id) + '/approve', {});
    toast(t('act.approved_done'), 'ok');
    onDone();
  } catch (e) { toast(e && e.status === 409 ? t('act.err.409') : errorMessage(e), 'error'); }
}

function openDetail(a, ctx, members, onChange) {
  const body = h('div', { class: 'col gap-4' });
  const who = (id) => { if (id === null || id === undefined) return null; const u = members.find((x) => String(x.id ?? x.user_id) === String(id)); return u ? (u.name || u.email) : '#' + id; };
  body.appendChild(kv([
    [t('col.host'), a.hostname || (a.host && a.host.hostname) || (a.host_id !== undefined ? '#' + a.host_id : null)],
    [t('act.f.kind'), t('act.kind.' + (ACTION_KINDS.includes(a.kind) ? a.kind : 'unknown'))],
    [t('act.f.target'), h('span', { class: 'mono small' }, JSON.stringify(a.params || {}))],
    [t('act.f.reason'), a.reason || null],
    [t('act.col.origin'), originCell(a, ctx.href)],
    [t('act.col.proposed_by'), a.proposed_by_ai ? t('act.origin.ai') + (a.proposed_by ? ' · ' + (a.proposed_by_name || who(a.proposed_by)) : '') : (a.proposed_by_name || who(a.proposed_by))],
    [t('act.col.approved_by'), a.approved_by ? (a.approved_by_name || who(a.approved_by)) : null],
    [t('act.col.expires'), a.expires_at && !isFinal(a.status) ? fmtDateTime(a.expires_at) : undefined],
  ]));
  const tracker = actionTracker(a, { onFinal: onChange, onUpdate: onChange, immediate: true });
  body.appendChild(tracker);
  const d = drawer({
    title: actionLabel(a), body,
    actions: isAdmin(ctx.me) && a.status === 'pending_approval' ? h('span', { class: 'row', style: { gap: '4px' } },
      h('button', { class: 'btn sm primary', type: 'button', onclick: () => decide(a, 'approve', ctx, () => { d.close(); onChange(); }) }, icon('check', 13), t('act.approve')),
      h('button', { class: 'btn sm danger', type: 'button', onclick: () => decide(a, 'reject', ctx, () => { d.close(); onChange(); }) }, t('act.reject'))) : null,
    onClose: () => tracker.stop(),
  });
}

export async function renderActions(root, ctx) {
  const { params, signal, href } = ctx;
  const admin = isAdmin(ctx.me);
  let view = params.get('view') === 'history' ? 'history' : 'pending';
  let status = STATUSES.includes(params.get('status')) ? params.get('status') : '';
  let hostId = params.get('host_id') || '';
  const tabSlot = h('div', { class: 'mb-3' });
  const filterSlot = h('div', { class: 'querybar' });
  const listEl = h('div');
  const countEl = h('span', { class: 'sub' });
  root.appendChild(h('div', { class: 'callout mb-3' }, icon('shield', 16), h('div', { class: 'small' }, h('div', t('act.intro')), h('div', { class: 'muted mt-2' }, t(admin ? 'act.intro_admin' : canWrite(ctx.me) ? 'act.intro_member' : 'act.intro_viewer')))));
  root.appendChild(tabSlot);
  root.appendChild(filterSlot);
  root.appendChild(panel(t('nav.settings.actions'), listEl, { flush: true, actions: countEl }));

  let rows = [];
  let members = [];
  let hosts = [];
  get('/org/members', null, { signal }).then((r) => { members = asList(r, 'members'); }).catch(() => {});
  get('/hosts', null, { signal }).then((r) => { hosts = asList(r, 'hosts'); drawFilters(); }).catch(() => {});

  function drawTabs(pendingCount) {
    clear(tabSlot);
    tabSlot.appendChild(tabs([{ key: 'pending', label: t('act.tab.pending'), count: pendingCount }, { key: 'history', label: t('act.tab.history') }], view,
      (k) => { view = k; ctx.setParams({ view: k === 'pending' ? null : k }, { silent: true }); drawTabs(pendingCount); drawFilters(); load(); }));
  }
  function drawFilters() {
    clear(filterSlot);
    filterSlot.appendChild(select([{ value: '', label: t('host.all') }].concat(hosts.slice().sort((a, b) => String(a.hostname).localeCompare(String(b.hostname))).map((x) => ({ value: String(x.id), label: x.hostname }))), hostId,
      (v) => { hostId = v; ctx.setParams({ host_id: v || null }, { silent: true }); load(); }, { style: { width: 'auto', minWidth: '180px' } }));
    if (view === 'history') {
      filterSlot.appendChild(select([{ value: '', label: t('act.all_statuses') }].concat(STATUSES.map((s2) => ({ value: s2, label: t('act.status.' + s2) }))), status,
        (v) => { status = v; ctx.setParams({ status: v || null }, { silent: true }); load(); }, { style: { width: 'auto' } }));
    }
  }

  function draw() {
    clear(listEl);
    countEl.textContent = tn('act.count', rows.length);
    if (!rows.length) {
      listEl.appendChild(view === 'pending' ? empty(t('act.none_pending_title'), t('act.none_pending_text')) : empty(t('act.none_title'), t('act.none_text')));
      return;
    }
    listEl.appendChild(dataTable({
      columns: [
        { key: 'created_at', label: t('col.created'), value: (a) => toMs(a.created_at), render: (a) => h('span', { title: fmtDateTime(a.created_at) }, fmtRel(a.created_at)) },
        { key: 'host', label: t('col.host'), value: (a) => a.hostname || '', render: (a) => (a.host_id !== undefined && a.host_id !== null ? h('a', { href: href('/infrastructure/host/' + a.host_id) }, a.hostname || '#' + a.host_id) : (a.hostname || '-')) },
        { key: 'what', label: t('act.col.what'), value: (a) => actionLabel(a), tdCls: 'trunc', render: (a) => h('b', { class: 'small', title: actionLabel(a) }, actionLabel(a)) },
        { key: 'reason', label: t('act.f.reason'), sort: false, tdCls: 'trunc', render: (a) => h('span', { class: 'small text-2', title: a.reason || '' }, a.reason || '-') },
        { key: 'origin', label: t('act.col.origin'), sort: false, render: (a) => originCell(a, href) },
        { key: 'status', label: t('col.status'), value: (a) => STATUSES.indexOf(a.status), render: (a) => actionStatusBadge(a.status) },
        { key: 'finished_at', label: t('act.col.finished'), align: 'r', value: (a) => toMs(a.finished_at), render: (a) => (a.finished_at ? h('span', { title: fmtDateTime(a.finished_at) }, fmtRel(a.finished_at)) : h('span', { class: 'muted' }, '-')) },
        { key: 'act', label: '', sort: false, render: (a) => (admin && a.status === 'pending_approval' ? h('span', { class: 'row', style: { gap: '2px', justifyContent: 'flex-end' } },
          h('button', { class: 'btn sm primary', type: 'button', onclick: () => decide(a, 'approve', ctx, load) }, icon('check', 13), t('act.approve')),
          h('button', { class: 'btn sm danger', type: 'button', onclick: () => decide(a, 'reject', ctx, load) }, t('act.reject'))) : null) },
      ],
      rows, sortKey: 'created_at', sortDir: 'desc', onRowClick: (a) => openDetail(a, ctx, members, () => load()),
      rowClass: (a) => (a.status === 'pending_approval' ? 'act-pending' : null),
    }));
  }

  let ctrl = null;
  signal.addEventListener('abort', () => { if (ctrl) ctrl.abort(); }, { once: true });
  let first = true;
  async function load() {
    if (ctrl) ctrl.abort();
    ctrl = new AbortController();
    const my = ctrl;
    if (first) { clear(listEl); listEl.appendChild(loading()); }
    try {
      const [list, pending] = await Promise.all([
        get('/actions', { status: view === 'pending' ? 'pending_approval' : status, host_id: hostId }, { signal: my.signal }).then((r) => asList(r, 'actions')),
        view === 'pending' ? Promise.resolve(null) : get('/actions', { status: 'pending_approval' }, { signal: my.signal }).then((r) => asList(r, 'actions')).catch(() => null),
      ]);
      if (my !== ctrl) return;
      rows = view === 'pending' ? list.filter((a) => a.status === 'pending_approval') : list;
      first = false;
      drawTabs(view === 'pending' ? rows.length : pending ? pending.length : null);
      draw();
    } catch (e) {
      if (isAbort(e) || my !== ctrl) return;
      clear(listEl);
      listEl.appendChild(listErrorState(e, () => load()));
    }
  }
  drawTabs(null);
  drawFilters();
  await load();
  // Actions en cours ou en attente : la liste se met à jour tant que la page est ouverte.
  const timer = setInterval(() => {
    if (document.visibilityState !== 'visible' || document.querySelector('.modal-back, .drawer')) return;
    if (view === 'pending' || rows.some((a) => !isFinal(a.status))) load();
  }, POLL_MS);
  ctx.onCleanup(() => clearInterval(timer));
}
