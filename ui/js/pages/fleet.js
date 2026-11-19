// Flotte d'agents (§ 27, Réglages › Agents) : versions (en retard en couleur), collecteurs, capacités,
// mise à jour en lot vers la version publiée (signée), suivi en direct de l'état de mise à jour.
import { h, clear, icon } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, post, asList, isAbort } from '../api.js';
import { fmtRel, fmtDateTime, fmtInt, toMs } from '../format.js';
import { empty, panel, searchInput, select, pills, toast, errorMessage, modal, field } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { load, tile, isAdmin, cmpVersion, versionLag, lagColor } from './kit.js';

const BUSY = ['downloading', 'verifying', 'installing'];

function agentUp(a) {
  if (a.status === 'up' || a.status === 'down') return a.status;
  const ms = toMs(a.last_seen);
  return ms !== null && Date.now() - ms < 2 * 60e3 ? 'up' : 'down';
}

function upd(a) { return a.update_status && typeof a.update_status === 'object' ? a.update_status : {}; }
function pending(a) { return !!a.target_version && cmpVersion(a.version, a.target_version) !== 0; }
function busy(a) { return BUSY.includes(upd(a).state) || (pending(a) && upd(a).state !== 'failed'); }

function updateBadge(a) {
  const u = upd(a);
  const st = u.state || (pending(a) ? 'queued' : 'idle');
  if (st === 'idle' && !pending(a)) return h('span', { class: 'muted small' }, t('fleet.upd.idle'));
  const cls = st === 'failed' ? 'alert' : BUSY.includes(st) || st === 'queued' ? 'info' : 'muted';
  return h('span', { class: 'row', style: { gap: '6px' } },
    h('span', { class: ['badge', cls], title: u.error || '' }, BUSY.includes(st) ? h('span', { class: 'spinner', style: { width: '10px', height: '10px', borderWidth: '2px' } }) : null, t('fleet.upd.' + (['downloading', 'verifying', 'installing', 'failed', 'queued', 'idle'].includes(st) ? st : 'idle'))),
    a.target_version ? h('span', { class: 'xsmall muted mono' }, '→ ' + a.target_version) : null);
}

function collectors(a) {
  const c = a.collectors && typeof a.collectors === 'object' ? a.collectors
    : a.status && typeof a.status === 'object' && a.status.collectors ? a.status.collectors : null;
  if (!c || !Object.keys(c).length) return h('span', { class: 'muted' }, '-');
  return h('div', { class: 'tags' }, Object.entries(c).slice(0, 10).map(([k, v]) => h('span', { class: ['badge', v === 'ok' ? 'ok' : v === 'absent' || v === 'disabled' ? 'muted' : 'warn'], title: k + ' : ' + v }, k)));
}

function capabilities(a) {
  const c = a.capabilities && typeof a.capabilities === 'object' ? a.capabilities : {};
  const out = [];
  const act = c.actions && typeof c.actions === 'object' ? c.actions : null;
  if (act && act.enabled) out.push(h('span', { class: 'badge info', title: asList(act.kinds).join(', ') + (asList(act.scripts).length ? ' · ' + asList(act.scripts).join(', ') : '') }, t('fleet.cap.actions')));
  if (c.synthetics) out.push(h('span', { class: 'badge info' }, t('fleet.cap.synthetics')));
  if (c.auto_update) out.push(h('span', { class: 'badge ok' }, t('fleet.cap.auto_update')));
  if (c.snmp_devices > 0) out.push(h('span', { class: 'badge muted' }, tn('fleet.cap.snmp', c.snmp_devices)));
  if (asList(c.integrations).length) out.push(h('span', { class: 'badge muted', title: asList(c.integrations).join(', ') }, tn('fleet.cap.integrations', asList(c.integrations).length)));
  return out.length ? h('div', { class: 'tags' }, out) : h('span', { class: 'muted' }, '-');
}

function lastError(a) {
  const u = upd(a);
  if (u.state === 'failed' && u.error) return u.error;
  const st = a.status && typeof a.status === 'object' ? a.status : {};
  const errs = asList(st.errors);
  return errs.length ? String(errs[errs.length - 1]) : '';
}

export async function renderFleet(root, ctx) {
  const { signal, params, href } = ctx;
  const admin = isAdmin(ctx.me);
  let q = params.get('q') || '';
  let os = params.get('os') || '';
  let only = params.get('only') || 'all';
  const selected = new Set();
  let agents = [];
  let latest = null;

  const tilesEl = h('div', { class: 'mb-3' });
  const distEl = h('div', { class: 'grid c2 mb-3' });
  const tableEl = h('div');
  const countEl = h('span', { class: 'sub' });
  const liveEl = h('span', { class: 'xsmall muted' });
  const actionsEl = h('div', { class: 'row wrap' });
  const search = searchInput({ placeholder: t('fleet.search_ph'), value: q, onInput: (v) => { q = v; ctx.setParams({ q: v || null }, { silent: true }); draw(); } });
  search.style.flex = '1 1 260px';
  root.appendChild(h('div', { class: 'querybar' }, search,
    select([{ value: '', label: t('fleet.all_os') }, { value: 'linux', label: 'Linux' }, { value: 'windows', label: 'Windows' }], os, (v) => { os = v; ctx.setParams({ os: v || null }, { silent: true }); draw(); }, { style: { width: 'auto' } }),
    pills([{ key: 'all', label: t('fleet.only.all') }, { key: 'late', label: t('fleet.only.late') }, { key: 'updating', label: t('fleet.only.updating') }, { key: 'failed', label: t('fleet.only.failed') }], only, (k) => { only = k; ctx.setParams({ only: k === 'all' ? null : k }, { silent: true }); draw(); }),
    liveEl));
  root.appendChild(tilesEl);
  root.appendChild(distEl);
  root.appendChild(panel(t('fleet.agents'), h('div', h('div', { class: 'panel-body fleet-actions' }, actionsEl), tableEl), { flush: true, actions: countEl }));
  root.appendChild(h('div', { class: 'callout mt-3' }, icon('lock', 16), h('div', { class: 'small' }, t('fleet.signed_note'))));

  function filtered() {
    const needle = q.trim().toLowerCase();
    return agents.filter((a) => {
      if (os && String(a.os || '').toLowerCase() !== os) return false;
      if (only === 'late' && versionLag(a.version, latest) === 'ok') return false;
      if (only === 'late' && !latest) return false;
      if (only === 'updating' && !busy(a)) return false;
      if (only === 'failed' && upd(a).state !== 'failed') return false;
      if (!needle) return true;
      return [a.hostname, a.version, a.os, a.arch, a.agent_id].join(' ').toLowerCase().includes(needle);
    });
  }

  function drawTiles() {
    clear(tilesEl);
    const up = agents.filter((a) => agentUp(a) === 'up').length;
    const late = latest ? agents.filter((a) => versionLag(a.version, latest) !== 'ok').length : 0;
    const updating = agents.filter(busy).length;
    const failed = agents.filter((a) => upd(a).state === 'failed').length;
    tilesEl.appendChild(h('div', { class: 'tiles' },
      tile(t('fleet.tile.agents'), fmtInt(agents.length), tn('fleet.tile.up', up)),
      tile(t('fleet.tile.latest'), latest || '-', t('fleet.tile.latest_sub')),
      tile(t('fleet.tile.late'), fmtInt(late), latest ? t('fleet.tile.late_sub', { v: latest }) : t('fleet.tile.no_latest'), late ? 'var(--warn)' : 'var(--ok)'),
      tile(t('fleet.tile.updating'), fmtInt(updating), null, updating ? 'var(--accent)' : null),
      tile(t('fleet.tile.failed'), fmtInt(failed), null, failed ? 'var(--crit)' : null)));
  }

  function drawDist(summary) {
    clear(distEl);
    const versions = new Map();
    const oses = new Map();
    if (summary && Array.isArray(summary.versions)) summary.versions.forEach((v) => versions.set(v.version || '?', v.count || 0));
    else agents.forEach((a) => versions.set(a.version || '?', (versions.get(a.version || '?') || 0) + 1));
    if (summary && Array.isArray(summary.os)) summary.os.forEach((v) => oses.set(v.os || v.name || '?', v.count || 0));
    else agents.forEach((a) => { const k = [a.os, a.arch].filter(Boolean).join(' / ') || '?'; oses.set(k, (oses.get(k) || 0) + 1); });
    const bars = (m, colorOf) => {
      const list = Array.from(m.entries()).sort((a, b) => b[1] - a[1]);
      const max = Math.max(1, ...list.map((x) => x[1]));
      return h('div', { class: 'panel-body' }, list.length ? h('div', { class: 'toplist' }, list.slice(0, 10).map(([k, n]) => h('div', { class: 'tl-row' },
        h('div', { class: 'tl-bar' }, h('i', { style: { width: Math.max(2, n / max * 100) + '%', borderRightColor: colorOf ? colorOf(k) : null } }), h('span', { class: 'mono', style: { color: colorOf ? colorOf(k) : null } }, k)),
        h('span', { class: 'num', style: { textAlign: 'right' } }, fmtInt(n))))) : h('div', { class: 'muted small' }, t('fleet.none')));
    };
    distEl.appendChild(panel(t('fleet.by_version'), bars(versions, (v) => lagColor(versionLag(v, latest))), { flush: true }));
    distEl.appendChild(panel(t('fleet.by_os'), bars(oses), { flush: true }));
  }

  function drawActions() {
    clear(actionsEl);
    if (!admin) { actionsEl.appendChild(h('span', { class: 'small muted' }, t('fleet.read_only'))); return; }
    const sel = agents.filter((a) => selected.has(String(a.agent_id)));
    const noAuto = sel.filter((a) => !(a.capabilities && a.capabilities.auto_update));
    actionsEl.appendChild(h('span', { class: 'small text-2' }, tn('fleet.selected', sel.length)));
    actionsEl.appendChild(h('button', { class: 'btn sm primary', type: 'button', disabled: !sel.length || !latest, onclick: () => upgrade({ agent_ids: sel.map((a) => a.agent_id) }, sel.length) }, icon('download', 12), latest ? t('fleet.upgrade_to', { v: latest }) : t('fleet.upgrade')));
    actionsEl.appendChild(h('button', { class: 'btn sm', type: 'button', disabled: !sel.some(pending), onclick: () => cancel(sel.filter(pending).map((a) => a.agent_id)) }, t('fleet.cancel_upgrade')));
    actionsEl.appendChild(h('span', { class: 'spacer' }));
    actionsEl.appendChild(h('button', { class: 'btn sm', type: 'button', disabled: !latest, onclick: () => upgradeAllDialog() }, t('fleet.upgrade_all')));
    if (noAuto.length) actionsEl.appendChild(h('div', { class: 'callout warn', style: { flexBasis: '100%', padding: '6px 10px' } }, icon('warn', 14), h('span', { class: 'xsmall' }, tn('fleet.no_auto', noAuto.length))));
  }

  async function upgrade(body, n) {
    try {
      await post('/fleet/upgrade', Object.assign({ version: latest }, body));
      toast(tn('fleet.upgrade_sent', n, { v: latest }), 'ok', 5000);
      selected.clear();
      refreshNow();
    } catch (e) { toast(errorMessage(e), 'error'); }
  }

  async function cancel(ids) {
    try { await post('/fleet/cancel-upgrade', { agent_ids: ids }); toast(t('fleet.cancel_done'), 'ok'); refreshNow(); } catch (e) { toast(errorMessage(e), 'error'); }
  }

  function upgradeAllDialog() {
    const osSel = select([{ value: '', label: t('fleet.all_os') }, { value: 'linux', label: 'Linux' }, { value: 'windows', label: 'Windows' }], os);
    const lateOnes = (o) => agents.filter((a) => (!o || String(a.os || '').toLowerCase() === o) && versionLag(a.version, latest) !== 'ok').length;
    const info = h('div', { class: 'small text-2' }, tn('fleet.all_count', lateOnes(os), { v: latest }));
    osSel.addEventListener('change', () => { info.textContent = tn('fleet.all_count', lateOnes(osSel.value), { v: latest }); });
    const ok = h('button', { class: 'btn primary', type: 'button' }, t('fleet.upgrade_all_go'));
    const m = modal({
      title: t('fleet.upgrade_all'),
      body: h('div', { class: 'col gap-3' }, field(t('col.os'), osSel), info, h('div', { class: 'callout' }, icon('info', 16), h('span', { class: 'small' }, t('fleet.all_note')))),
      actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), ok],
    });
    ok.addEventListener('click', () => { m.close(); upgrade({ all: true, os: osSel.value || null }, lateOnes(osSel.value)); });
  }

  function draw() {
    const list = filtered();
    countEl.textContent = tn('fleet.count', list.length);
    clear(tableEl);
    if (!agents.length) { tableEl.appendChild(empty(t('fleet.none_title'), t('fleet.none_text'), h('a', { class: 'btn primary', href: href('/settings/install') }, icon('download', 14), t('infra.install_agent')))); drawActions(); return; }
    const allSel = list.length > 0 && list.every((a) => selected.has(String(a.agent_id)));
    const head = admin ? h('input', { type: 'checkbox', checked: allSel, title: t('fleet.select_all') }) : null;
    if (head) head.addEventListener('change', () => { list.forEach((a) => { if (head.checked) selected.add(String(a.agent_id)); else selected.delete(String(a.agent_id)); }); draw(); });
    tableEl.appendChild(dataTable({
      columns: [
        admin ? { key: 'sel', label: head, sort: false, width: '28px', render: (a) => { const cb = h('input', { type: 'checkbox', checked: selected.has(String(a.agent_id)) }); cb.addEventListener('change', () => { if (cb.checked) selected.add(String(a.agent_id)); else selected.delete(String(a.agent_id)); drawActions(); }); return cb; } } : null,
        { key: 'up', label: '', width: '20px', value: (a) => (agentUp(a) === 'up' ? 1 : 0), render: (a) => h('span', { class: ['dot', agentUp(a)], title: t('host.status.' + agentUp(a)) }) },
        { key: 'hostname', label: t('col.host'), render: (a) => (a.host_id !== undefined && a.host_id !== null ? h('a', { href: href('/infrastructure/host/' + a.host_id) }, a.hostname || '-') : (a.hostname || '-')) },
        { key: 'version', label: t('col.agent'), value: (a) => a.version || '', sort: (a) => String(a.version || '').split('.').map((x) => String(parseInt(x, 10) || 0).padStart(5, '0')).join('.'),
          render: (a) => { const lag = versionLag(a.version, latest); return h('span', { class: 'mono small', style: { color: lagColor(lag), fontWeight: lag && lag !== 'ok' ? '600' : null }, title: lag && lag !== 'ok' ? t('fleet.lag.' + lag, { v: latest }) : '' }, a.version || '-'); } },
        { key: 'update', label: t('fleet.update'), value: (a) => (upd(a).state === 'failed' ? 0 : busy(a) ? 1 : 2), render: updateBadge },
        { key: 'os', label: t('col.os'), value: (a) => [a.os, a.arch].join('/'), render: (a) => h('span', { class: 'small text-2' }, [a.os, a.arch].filter(Boolean).join(' / ') || '-') },
        { key: 'collectors', label: t('install.collectors'), sort: false, render: collectors },
        { key: 'caps', label: t('fleet.capabilities'), sort: false, render: capabilities },
        { key: 'err', label: t('fleet.last_error'), tdCls: 'trunc', value: lastError, render: (a) => { const e = lastError(a); return e ? h('span', { class: 'small', style: { color: 'var(--crit)' }, title: e }, e) : h('span', { class: 'muted' }, '-'); } },
        { key: 'last_seen', label: t('col.last_seen'), align: 'r', value: (a) => toMs(a.last_seen), render: (a) => h('span', { title: fmtDateTime(a.last_seen) }, fmtRel(a.last_seen)) },
      ].filter(Boolean),
      rows: list, sortKey: params.get('sort') || 'hostname', sortDir: params.get('dir') || 'asc',
      onSort: (k, d) => ctx.setParams({ sort: k, dir: d }, { silent: true }),
      rowClass: (a) => (selected.has(String(a.agent_id)) ? 'selected' : null),
      emptyText: t('fleet.no_match'),
    }));
    drawActions();
  }

  async function fetchAll() {
    const [list, summary, inst] = await Promise.all([
      get('/fleet', null, { signal }).then((r) => asList(r, 'agents', 'fleet')),
      get('/fleet/summary', null, { signal }).catch(() => null),
      get('/install', null, { signal }).catch(() => null),
    ]);
    agents = list;
    latest = (summary && (summary.latest || summary.published || summary.latest_version)) || (inst && inst.version) || null;
    return summary;
  }

  // Suivi en direct : toutes les 5 s pendant une mise à jour, sinon toutes les 30 s.
  let timer = null;
  let lastPoll = 0;
  async function refreshNow() {
    try {
      const summary = await fetchAll();
      lastPoll = Date.now();
      drawTiles();
      drawDist(summary);
      draw();
    } catch (e) { if (!isAbort(e)) toast(errorMessage(e), 'error'); }
  }
  function schedule() {
    clearTimeout(timer);
    const fast = agents.some(busy);
    liveEl.textContent = fast ? t('fleet.live_fast') : t('fleet.live_slow');
    timer = setTimeout(async () => {
      if (document.visibilityState === 'visible' && Date.now() - lastPoll > 2000) await refreshNow();
      if (ctx.isCurrent()) schedule();
    }, fast ? 5000 : 30000);
  }
  ctx.onCleanup(() => clearTimeout(timer));

  const ok = await load(tableEl, fetchAll, (summary) => { lastPoll = Date.now(); drawTiles(); drawDist(summary); draw(); return null; });
  if (ok === undefined && !agents.length) { clear(tilesEl); return; }
  schedule();
}

