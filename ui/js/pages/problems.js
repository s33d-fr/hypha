// Problèmes (§ 17) : tous les groupes hors OK de l'organisation, par priorité puis ancienneté.
// Durée qui court, filtres, acquittement (seul ou en lot), Expliquer, maintenance, incident, réparation.
import { h, clear, icon } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, asList, isAbort } from '../api.js';
import { fmtUnit, fmtDateTime, fmtRel, fmtInt, isNum, toMs } from '../format.js';
import { panel, searchInput, pills, select, badgeState, dropdown, menuItem, loading } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { explainButton, explainEnabled, openExplain, idValue } from '../components/explain.js';
import { refreshProblemCount } from '../components/problem-count.js';
import { stateRank } from './monitors-shared.js';
import {
  PRIORITIES, prioNum, priorityBadge, liveDuration, problemHost, problemKey, ackedInfo, ackDialog, unackItems,
  downtimeDialog, incidentFromAlert, repairDialog, canWrite, listErrorState,
} from './ops-shared.js';

const REFRESH_MS = 30000;
const STATES = ['ALERT', 'WARN', 'NODATA'];

function stateOf(p) { return String(p.state || 'ALERT').toUpperCase(); }
function sinceOf(p) {
  const s2 = toMs(p.since);
  if (s2 !== null) return s2;
  return isNum(p.duration_s) ? Date.now() - p.duration_s * 1000 : null;
}

export async function render(root, ctx) {
  const { params, signal, href, me } = ctx;
  ctx.setCrumbs([{ label: t('nav.problems') }]);
  const writer = canWrite(me);

  // Lien « Expliquer » d'un courriel d'alerte : /#/problems?explain=<moniteur>&group=<groupe>.
  const explainId = params.get('explain');
  if (explainId && /^\d+$/.test(explainId) && explainEnabled(me)) {
    const group = params.get('group') || '*';
    openExplain({ kind: 'alert', monitor_id: idValue(explainId), group }, { subject: group });
  }

  const f = {
    q: params.get('q') || '',
    st: STATES.includes(params.get('st')) ? params.get('st') : '',
    ack: ['unacked', 'acked'].includes(params.get('ack')) ? params.get('ack') : 'all',
    sup: ['show', 'only'].includes(params.get('sup')) ? params.get('sup') : 'hide',
    prio: new Set(String(params.get('prio') || '').split(',').filter((x) => /^[0-5]$/.test(x)).map(Number)),
  };
  const persist = () => ctx.setParams({
    q: f.q || null, st: f.st || null, ack: f.ack === 'all' ? null : f.ack, sup: f.sup === 'hide' ? null : f.sup,
    prio: f.prio.size ? Array.from(f.prio).sort().join(',') : null,
  }, { silent: true });

  let rows = [];
  let sort = { key: 'priority', dir: 'asc' };
  let loaded = false;
  let lastLoad = null;
  const selected = new Set();

  const updatedEl = h('span', { class: 'xsmall muted' });
  root.appendChild(h('div', { class: 'page-head' },
    h('h1', t('nav.problems')), h('span', { class: 'desc' }, t('problems.desc')), h('span', { class: 'spacer' }),
    updatedEl,
    h('button', { class: 'btn sm', type: 'button', onclick: () => load(true) }, icon('refresh', 13), t('action.refresh')),
    h('a', { class: 'btn sm', href: href('/downtimes') }, icon('wrench', 13), t('nav.downtimes')),
    h('a', { class: 'btn sm', href: href('/incidents') }, icon('siren', 13), t('nav.incidents'))));

  const tilesEl = h('div', { class: 'tiles prio-tiles mb-3' });
  const pillSlot = h('span');
  const ackSlot = h('span');
  const supSlot = h('span');
  const search = searchInput({ placeholder: t('problems.search_ph'), value: f.q, onInput: (v) => { f.q = v; persist(); draw(); } });
  search.style.flex = '1 1 260px';
  const resetBtn = h('button', { class: 'btn sm ghost', type: 'button', onclick: () => {
    f.q = ''; f.st = ''; f.ack = 'all'; f.sup = 'hide'; f.prio.clear(); search.input.value = ''; persist(); drawFilters(); draw();
  } }, t('problems.reset'));
  root.appendChild(tilesEl);
  root.appendChild(h('div', { class: 'querybar' }, search, pillSlot, ackSlot, supSlot, resetBtn));
  const bulkEl = h('div', { class: 'bulkbar', hidden: true });
  root.appendChild(bulkEl);
  const countEl = h('span', { class: 'sub' });
  const tableEl = h('div');
  root.appendChild(panel(t('problems.list'), tableEl, { flush: true, actions: countEl }));
  tableEl.appendChild(loading());

  function drawFilters() {
    clear(pillSlot);
    pillSlot.appendChild(pills([{ key: '', label: t('problems.all_states') }].concat(STATES.map((k) => ({ key: k, label: t('state.' + k.toLowerCase()) }))), f.st,
      (k) => { f.st = k; persist(); drawFilters(); draw(); }));
    clear(ackSlot);
    ackSlot.appendChild(select(['all', 'unacked', 'acked'].map((k) => ({ value: k, label: t('problems.ack.' + k) })), f.ack,
      (v) => { f.ack = v; persist(); draw(); }, { style: { width: 'auto' }, 'aria-label': t('problems.ack_filter') }));
    clear(supSlot);
    supSlot.appendChild(select(['hide', 'show', 'only'].map((k) => ({ value: k, label: t('problems.sup.' + k) })), f.sup,
      (v) => { f.sup = v; persist(); draw(); }, { style: { width: 'auto' }, 'aria-label': t('problems.sup_filter') }));
  }

  function matches(p, opts = {}) {
    if (!opts.ignorePrio && f.prio.size && !f.prio.has(prioNum(p.priority) || 0)) return false;
    if (f.st && stateOf(p) !== f.st) return false;
    const acked = !!ackedInfo(p);
    if (f.ack === 'unacked' && acked) return false;
    if (f.ack === 'acked' && !acked) return false;
    if (f.sup === 'hide' && p.suppressed) return false;
    if (f.sup === 'only' && !p.suppressed) return false;
    const words = f.q.toLowerCase().split(/\s+/).filter(Boolean);
    if (words.length) {
      const hay = [p.monitor_name, p.group, problemHost(p), p.type, p.state, p.suppressed, (ackedInfo(p) || {}).comment].join(' ').toLowerCase();
      if (!words.every((w) => hay.includes(w))) return false;
    }
    return true;
  }

  function drawTiles() {
    clear(tilesEl);
    const base = rows.filter((p) => matches(p, { ignorePrio: true }));
    const counts = new Map([[0, 0]].concat(PRIORITIES.map((n) => [n, 0])));
    base.forEach((p) => { const n = prioNum(p.priority) || 0; counts.set(n, counts.get(n) + 1); });
    for (const n of PRIORITIES.concat([0])) {
      const on = f.prio.has(n);
      tilesEl.appendChild(h('button', {
        type: 'button', class: ['tile', 'prio-tile', 'p' + n, on ? 'on' : null, counts.get(n) ? null : 'zero'], 'aria-pressed': on ? 'true' : 'false',
        title: t('problems.tile_title'),
        onclick: () => { if (on) f.prio.delete(n); else f.prio.add(n); persist(); draw(); },
      }, h('div', { class: 't-label' }, n ? priorityBadge(n) : priorityBadge(null), h('span', n ? t('prio.name.' + n) : t('prio.none_title'))),
        h('div', { class: 't-value num' }, fmtInt(counts.get(n)))));
    }
    const hiddenSup = rows.filter((p) => p.suppressed).length;
    const unacked = rows.filter((p) => !ackedInfo(p) && !p.suppressed).length;
    tilesEl.appendChild(h('div', { class: 'tile' }, h('div', { class: 't-label' }, icon('ack', 13), t('problems.unacked')), h('div', { class: 't-value num' }, fmtInt(unacked)),
      h('div', { class: 't-sub' }, tn('problems.total', rows.length))));
    if (hiddenSup) tilesEl.appendChild(h('button', { type: 'button', class: 'tile', onclick: () => { f.sup = f.sup === 'only' ? 'hide' : 'only'; persist(); drawFilters(); draw(); } },
      h('div', { class: 't-label' }, icon('wrench', 13), t('problems.suppressed')), h('div', { class: 't-value num' }, fmtInt(hiddenSup)),
      h('div', { class: 't-sub' }, t(f.sup === 'hide' ? 'problems.suppressed_hidden' : 'problems.suppressed_shown'))));
  }

  function itemOf(p) { return { monitor_id: p.monitor_id, group: p.group || '*', monitor_name: p.monitor_name }; }
  function afterChange() { refreshProblemCount(); load(true); }

  function drawBulk(list) {
    clear(bulkEl);
    const sel = list.filter((p) => selected.has(problemKey(p)));
    bulkEl.hidden = !sel.length;
    if (!sel.length) return;
    const toAck = sel.filter((p) => !ackedInfo(p));
    const toUnack = sel.filter((p) => ackedInfo(p));
    bulkEl.appendChild(h('b', tn('problems.selected', sel.length)));
    bulkEl.appendChild(h('button', { class: 'btn sm primary', type: 'button', disabled: !writer || !toAck.length, onclick: () => ackDialog(toAck.map(itemOf), afterChange) },
      icon('check', 13), tn('problems.ack_n', toAck.length)));
    bulkEl.appendChild(h('button', { class: 'btn sm', type: 'button', disabled: !writer || !toUnack.length, onclick: () => unackItems(toUnack.map(itemOf), afterChange) },
      tn('problems.unack_n', toUnack.length)));
    bulkEl.appendChild(h('span', { class: 'spacer' }));
    bulkEl.appendChild(h('button', { class: 'btn sm ghost', type: 'button', onclick: () => { selected.clear(); draw(); } }, t('problems.clear_sel')));
  }

  function flags(p) {
    const out = [];
    const a = ackedInfo(p);
    if (a) {
      out.push(h('span', { class: 'badge ok', title: [t('ack.by', { who: a.by || '?', d: fmtDateTime(a.at) }), a.comment].filter(Boolean).join('\n') },
        icon('ack', 12), t('ack.acked')));
      if (a.comment) out.push(h('span', { class: 'xsmall text-2 ellipsis ack-comment', title: a.comment }, a.comment));
    }
    if (p.suppressed) out.push(h('span', { class: 'badge info', title: t('problems.sup_title.' + (['downtime', 'dependency', 'flapping'].includes(p.suppressed) ? p.suppressed : 'other')) }, icon('wrench', 12), t('problems.sup_label.' + (['downtime', 'dependency', 'flapping'].includes(p.suppressed) ? p.suppressed : 'other'))));
    if (p.flapping) out.push(h('span', { class: 'badge warn', title: t('problems.flapping_title') }, t('problems.flapping')));
    if (p.muted) out.push(h('span', { class: 'badge muted', title: t('monitors.muted') }, icon('mute', 12)));
    if (isNum(p.escalation_step) && p.escalation_step > 0) out.push(h('span', { class: 'badge alert', title: t('problems.escalation_title') }, t('problems.escalation', { n: p.escalation_step })));
    if (p.incident_id) out.push(h('a', { class: 'badge info', href: href('/incidents/' + p.incident_id), title: t('inc.open') }, icon('siren', 12), p.incident_number ? 'INC-' + p.incident_number : t('inc.short')));
    if (p.runbook_url && /^https?:\/\//i.test(p.runbook_url)) out.push(h('a', { class: 'badge muted', href: p.runbook_url, target: '_blank', rel: 'noopener noreferrer' }, icon('external', 12), t('problems.runbook')));
    return out.length ? h('div', { class: 'tags' }, out) : h('span', { class: 'muted' }, '-');
  }

  function rowMenu(p) {
    const host = problemHost(p);
    const a = ackedInfo(p);
    const btn = h('button', { class: 'btn sm ghost icon', type: 'button', title: t('problems.actions'), 'aria-label': t('problems.actions') }, icon('chevronDown', 13));
    return dropdown(btn, (close) => h('div',
      writer && !a ? menuItem(t('ack.do_dots'), () => { close(); ackDialog([itemOf(p)], afterChange); }, { icon: 'check' }) : null,
      writer && a ? menuItem(t('ack.remove'), () => { close(); unackItems([itemOf(p)], afterChange); }, { icon: 'x' }) : null,
      writer ? h('div', { class: 'menu-sep' }) : null,
      writer ? menuItem(t('problems.dt_group'), () => {
        close();
        downtimeDialog(ctx, { name: (p.monitor_name || '') + ' · ' + (p.group || '*'), subject: (p.monitor_name || '') + ' (' + (p.group || '*') + ')', scope: { monitor_ids: [idValue(p.monitor_id)], groups: [p.group || '*'] }, onDone: afterChange });
      }, { icon: 'wrench' }) : null,
      writer && host ? menuItem(t('problems.dt_host', { host }), () => {
        close();
        downtimeDialog(ctx, { name: t('dt.host_name', { host }), subject: t('dt.host_subject', { host }), scope: { tags: { host } }, onDone: afterChange });
      }, { icon: 'wrench' }) : null,
      p.incident_id ? menuItem(t('inc.open'), () => { close(); ctx.navigate('/incidents/' + p.incident_id); }, { icon: 'siren' })
        : writer ? menuItem(t('inc.from_alert'), () => { close(); incidentFromAlert(ctx, p); }, { icon: 'siren' }) : null,
      writer ? menuItem(t('act.repair'), () => { close(); repairDialog(ctx, p); }, { icon: 'play' }) : null,
      h('div', { class: 'menu-sep' }),
      menuItem(t('problems.open_monitor'), () => { close(); ctx.navigate('/monitors/' + p.monitor_id); }, { icon: 'monitor' }),
      host ? menuItem(t('problems.open_host'), () => { close(); if (p.host_id) ctx.navigate('/infrastructure/host/' + p.host_id); else ctx.navigate('/infrastructure', { q: host }); }, { icon: 'server' }) : null));
  }

  function draw() {
    if (!loaded) return;
    drawTiles();
    const list = rows.filter((p) => matches(p));
    for (const k of Array.from(selected)) if (!rows.some((p) => problemKey(p) === k)) selected.delete(k);
    countEl.textContent = tn('problems.count', list.length);
    drawBulk(list);
    clear(tableEl);
    if (!rows.length) {
      tableEl.appendChild(h('div', { class: 'state' }, h('div', { class: 'ok-mark' }, icon('ack', 28)), h('h3', t('problems.none_title')), h('div', { class: 'small' }, t('problems.none_text'))));
      return;
    }
    const allSel = list.length > 0 && list.every((p) => selected.has(problemKey(p)));
    const head = h('input', { type: 'checkbox', checked: allSel, 'aria-label': t('problems.select_all'), title: t('problems.select_all') });
    head.addEventListener('click', (e) => e.stopPropagation());
    head.addEventListener('change', () => { if (head.checked) list.forEach((p) => selected.add(problemKey(p))); else list.forEach((p) => selected.delete(problemKey(p))); draw(); });
    tableEl.appendChild(dataTable({
      columns: [
        { key: 'sel', label: head, sort: false, width: '28px', render: (p) => {
          const cb = h('input', { type: 'checkbox', checked: selected.has(problemKey(p)), 'aria-label': t('problems.select_row') });
          cb.addEventListener('change', () => { if (cb.checked) selected.add(problemKey(p)); else selected.delete(problemKey(p)); draw(); });
          return cb;
        } },
        { key: 'priority', label: t('col.priority'), value: (p) => (prioNum(p.priority) || 9) * 1e13 + (sinceOf(p) || 0), defaultDir: 'asc', render: (p) => priorityBadge(p.priority) },
        { key: 'state', label: t('col.state'), value: (p) => stateRank(stateOf(p)), defaultDir: 'asc', render: (p) => badgeState(stateOf(p)) },
        { key: 'monitor', label: t('col.monitor'), value: (p) => p.monitor_name || '', tdCls: 'trunc', render: (p) => h('div', { class: 'col', style: { gap: '0' } },
          h('a', { href: href('/monitors/' + p.monitor_id), title: p.monitor_name || '' }, p.monitor_name || '#' + p.monitor_id),
          p.type ? h('span', { class: 'xsmall muted' }, t('monitors.type.' + p.type)) : null) },
        { key: 'group', label: t('col.group'), value: (p) => p.group || '', tdCls: 'trunc', render: (p) => h('span', { class: 'mono small', title: p.group || '*' }, p.group || '*') },
        { key: 'host', label: t('col.host'), value: problemHost, render: (p) => {
          const host = problemHost(p);
          if (!host) return h('span', { class: 'muted' }, '-');
          return p.host_id ? h('a', { href: href('/infrastructure/host/' + p.host_id) }, host) : h('span', host);
        } },
        { key: 'value', label: t('col.value'), align: 'r', value: (p) => (isNum(p.value) ? p.value : null), render: (p) => (isNum(p.value) ? fmtUnit(p.value, p.unit || null) : '-') },
        { key: 'duration', label: t('col.duration'), align: 'r', value: (p) => sinceOf(p), defaultDir: 'asc', render: (p) => (sinceOf(p) ? liveDuration(sinceOf(p), { title: t('problems.since', { d: fmtDateTime(sinceOf(p)) }) }) : '-') },
        { key: 'flags', label: t('problems.col.status'), sort: false, tdCls: 'wrap', render: flags },
        { key: 'notified', label: t('problems.col.notified'), align: 'r', value: (p) => toMs(p.last_notified), render: (p) => (p.last_notified ? h('span', { title: fmtDateTime(p.last_notified) }, fmtRel(p.last_notified)) : h('span', { class: 'muted' }, '-')) },
        { key: 'act', label: '', sort: false, render: (p) => h('span', { class: 'row', style: { gap: '2px', justifyContent: 'flex-end' } },
          writer && !ackedInfo(p) ? h('button', { class: 'btn sm', type: 'button', title: t('ack.do_title'), onclick: () => ackDialog([itemOf(p)], afterChange) }, icon('check', 13), t('ack.do')) : null,
          /^https?:\/\//i.test(p.runbook_url || '') ? h('a', { class: 'btn sm ghost', href: p.runbook_url, target: '_blank', rel: 'noopener noreferrer', title: t('problems.runbook_title', { s: p.service || '' }) }, t('problems.runbook')) : null,
          explainButton(me, { kind: 'alert', monitor_id: idValue(p.monitor_id), group: p.group || '*' }, { iconOnly: true, subject: (p.monitor_name || '') + ' (' + (p.group || '*') + ')' }),
          rowMenu(p)) },
      ],
      rows: list,
      sortKey: sort.key, sortDir: sort.dir, onSort: (k, d) => { sort = { key: k, dir: d }; },
      rowClass: (p) => ['prio-row', 'p' + (prioNum(p.priority) || 0), selected.has(problemKey(p)) ? 'selected' : null, ackedInfo(p) ? 'acked' : null].filter(Boolean).join(' '),
      onRowClick: (p) => ctx.navigate('/monitors/' + p.monitor_id),
      emptyText: t('problems.no_match'),
    }));
  }

  let ctrl = null;
  signal.addEventListener('abort', () => { if (ctrl) ctrl.abort(); }, { once: true });
  async function load(keepScroll) {
    if (ctrl) ctrl.abort();
    ctrl = new AbortController();
    const my = ctrl;
    try {
      const resp = await get('/problems', null, { signal: my.signal });
      if (my !== ctrl) return;
      rows = asList(resp, 'problems').filter((p) => stateOf(p) !== 'OK');
      loaded = true;
      lastLoad = Date.now();
      updatedEl.textContent = t('problems.updated', { d: fmtDateTime(lastLoad) });
      const scroller = keepScroll ? document.getElementById('page') : null;
      const top = scroller ? scroller.scrollTop : 0;
      draw();
      if (scroller) scroller.scrollTop = top;
    } catch (e) {
      if (isAbort(e) || my !== ctrl) return;
      if (!loaded) { clear(tableEl); tableEl.appendChild(listErrorState(e, () => load())); }
      else updatedEl.textContent = t('problems.refresh_failed');
    }
  }

  drawFilters();
  await load();
  const timer = setInterval(() => {
    if (document.visibilityState !== 'visible') return;
    if (document.querySelector('.modal-back, .drawer, .menu')) return;
    load(true);
  }, REFRESH_MS);
  ctx.onCleanup(() => { clearInterval(timer); if (ctrl) ctrl.abort(); });
}
