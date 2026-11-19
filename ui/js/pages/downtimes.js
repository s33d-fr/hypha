// Maintenances planifiées (§ 16) : liste (actives en tête), création et modification avec portée
// (moniteurs, étiquettes, hôte, groupes), récurrence et aperçu des moniteurs touchés.
import { h, clear, icon } from '../dom.js';
import { t, tn, locale } from '../i18n.js';
import { get, post, put, del, asList, isAbort } from '../api.js';
import { fmtDateTime, fmtDuration, fmtRel, fmtHM, toMs, toLocalInput, fromLocalInput } from '../format.js';
import { panel, empty, field, select, pills, toast, confirmDialog, errorMessage, errorState, loading, searchInput, badgeState } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { monitorState } from './monitors-shared.js';
import { canWrite, listErrorState, groupTags, problemHost, priorityBadge } from './ops-shared.js';

const DURATIONS = [900, 1800, 3600, 7200, 14400, 28800, 43200, 86400, 172800, 604800];
const LOCAL_TZ = (() => { try { return Intl.DateTimeFormat().resolvedOptions().timeZone || 'Europe/Paris'; } catch (e) { return 'Europe/Paris'; } })();

export async function render(root, ctx) {
  if (ctx.route.view === 'form') return renderForm(root, ctx);
  return renderList(root, ctx);
}

// ── Aides ─────────────────────────────────────────────────────────────────
export function weekdayName(n, style = 'long') {
  // 1er janvier 2024 = lundi : n = 1 (lundi) … 7 (dimanche).
  return new Intl.DateTimeFormat(locale(), { weekday: style, timeZone: 'UTC' }).format(Date.UTC(2024, 0, n));
}

function scopeOf(d) {
  const s2 = d && d.scope && typeof d.scope === 'object' ? d.scope : {};
  return {
    monitor_ids: Array.isArray(s2.monitor_ids) ? s2.monitor_ids : [],
    tags: s2.tags && typeof s2.tags === 'object' ? s2.tags : {},
    groups: Array.isArray(s2.groups) ? s2.groups : [],
  };
}

export function dtStatus(d, now = Date.now()) {
  if (d.enabled === false) return 'disabled';
  if (d.active) return 'active';
  const end = toMs(d.end_ts);
  const until = d.recurrence ? toMs(d.recurrence.until) : null;
  if ((end && end <= now) || (until && until <= now)) return 'ended';
  return 'planned';
}

function statusBadge(st) {
  const cls = { active: 'info', planned: 'warn', ended: 'muted', disabled: 'muted' }[st] || 'muted';
  return h('span', { class: ['badge', cls] }, st === 'active' ? icon('wrench', 12) : null, t('dt.status.' + st));
}

export function describeSchedule(d) {
  const r = d.recurrence;
  if (r && r.type) {
    const dur = fmtDuration((Number(r.duration_s) || 0) * 1000, true);
    let s2;
    if (r.type === 'weekly') s2 = t('dt.rec.weekly_desc', { days: (r.days || []).slice().sort().map((n) => weekdayName(n)).join(', '), time: r.start || '00:00', dur });
    else if (r.type === 'monthly') s2 = t('dt.rec.monthly_desc', { day: r.day_of_month || 1, time: r.start || '00:00', dur });
    else s2 = t('dt.rec.daily_desc', { time: r.start || '00:00', dur });
    if (r.tz && r.tz !== LOCAL_TZ) s2 += ' (' + r.tz + ')';
    const until = toMs(r.until) || toMs(d.end_ts);
    if (until) s2 += ', ' + t('dt.rec.until_desc', { d: new Intl.DateTimeFormat(locale(), { day: 'numeric', month: 'short', year: 'numeric' }).format(until) });
    return s2;
  }
  const a = toMs(d.start_ts);
  const b = toMs(d.end_ts);
  if (!b) return t('dt.once_open', { a: fmtDateTime(a) });
  return fmtDateTime(a) + ' → ' + fmtDateTime(b) + ' (' + fmtDuration(b - a, true) + ')';
}

function scopeSummary(d, monitorsById) {
  const sc = scopeOf(d);
  const parts = [];
  if (!sc.monitor_ids.length) parts.push(h('span', { class: 'badge muted' }, t('dt.scope.all_monitors')));
  else {
    const names = sc.monitor_ids.map((id) => { const m = monitorsById.get(String(id)); return m ? m.name : '#' + id; });
    parts.push(h('span', { class: 'small', title: names.join('\n') }, names.length <= 2 ? names.join(', ') : tn('dt.scope.n_monitors', names.length)));
  }
  for (const [k, v] of Object.entries(sc.tags)) parts.push(h('span', { class: 'tag' }, h('b', k + ':'), String(v)));
  if (sc.groups.length) parts.push(h('span', { class: 'small mono', title: sc.groups.join('\n') }, sc.groups.length === 1 ? sc.groups[0] : tn('dt.scope.n_groups', sc.groups.length)));
  return h('div', { class: 'tags' }, parts);
}

// ── Liste ─────────────────────────────────────────────────────────────────
async function renderList(root, ctx) {
  const { signal, href, params } = ctx;
  ctx.setCrumbs([{ label: t('nav.downtimes') }]);
  const writer = canWrite(ctx.me);
  let filter = ['current', 'ended', 'all'].includes(params.get('f')) ? params.get('f') : 'current';
  let q = params.get('q') || '';
  const body = h('div');
  const pillSlot = h('span');
  const countEl = h('span', { class: 'sub' });
  const search = searchInput({ placeholder: t('dt.search_ph'), value: q, onInput: (v) => { q = v; ctx.setParams({ q: v || null }, { silent: true }); draw(); } });
  search.style.flex = '1 1 240px';
  root.appendChild(h('div', { class: 'page-head' }, h('h1', t('nav.downtimes')), h('span', { class: 'desc' }, t('dt.desc'))));
  root.appendChild(h('div', { class: 'querybar' }, search, pillSlot, h('span', { class: 'spacer' }),
    writer ? h('a', { class: 'btn primary', href: href('/downtimes/new') }, icon('plus', 14), t('dt.new')) : null));
  root.appendChild(panel(t('nav.downtimes'), body, { flush: true, actions: countEl }));

  let rows = [];
  let monitorsById = new Map();
  const now = Date.now();

  async function act(d, patch, okKey) {
    const body2 = Object.assign({ name: d.name, message: d.message || '', scope: scopeOf(d), start_ts: toMs(d.start_ts), end_ts: toMs(d.end_ts), recurrence: d.recurrence || null, enabled: d.enabled !== false }, patch);
    try { await put('/downtimes/' + d.id, body2); toast(t(okKey), 'ok'); ctx.refresh(); } catch (e) { toast(errorMessage(e), 'error'); }
  }

  function drawPills() {
    clear(pillSlot);
    const counts = { current: 0, ended: 0, all: rows.length };
    rows.forEach((d) => { if (dtStatus(d, now) === 'ended') counts.ended++; else counts.current++; });
    pillSlot.appendChild(pills(['current', 'ended', 'all'].map((k) => ({ key: k, label: t('dt.filter.' + k) + ' ' + counts[k] })), filter,
      (k) => { filter = k; ctx.setParams({ f: k === 'current' ? null : k }, { silent: true }); drawPills(); draw(); }));
  }

  function draw() {
    clear(body);
    const words = q.toLowerCase().split(/\s+/).filter(Boolean);
    const list = rows.filter((d) => {
      const st = dtStatus(d, now);
      if (filter === 'current' && st === 'ended') return false;
      if (filter === 'ended' && st !== 'ended') return false;
      return words.every((w) => [d.name, d.message, JSON.stringify(d.scope || {})].join(' ').toLowerCase().includes(w));
    });
    countEl.textContent = tn('dt.count', list.length);
    if (!rows.length) {
      body.appendChild(empty(t('dt.none_title'), t('dt.none_text'), writer ? h('a', { class: 'btn primary', href: href('/downtimes/new') }, icon('plus', 14), t('dt.new')) : null));
      return;
    }
    const rank = { active: 0, planned: 1, disabled: 2, ended: 3 };
    body.appendChild(dataTable({
      columns: [
        { key: 'status', label: t('col.status'), value: (d) => rank[dtStatus(d, now)] * 1e13 + (toMs(d.next_start) || toMs(d.start_ts) || 0), defaultDir: 'asc', render: (d) => statusBadge(dtStatus(d, now)) },
        { key: 'name', label: t('col.name'), tdCls: 'trunc', render: (d) => h('div', { class: 'col', style: { gap: '0' } }, h('a', { href: href('/downtimes/' + d.id + '/edit') }, d.name || '#' + d.id),
          d.message ? h('span', { class: 'xsmall muted ellipsis', title: d.message }, d.message) : null) },
        { key: 'scope', label: t('dt.col.scope'), sort: false, tdCls: 'wrap', render: (d) => scopeSummary(d, monitorsById) },
        { key: 'schedule', label: t('dt.col.schedule'), sort: false, tdCls: 'wrap', render: (d) => h('span', { class: 'small' }, describeSchedule(d)) },
        { key: 'next', label: t('dt.col.next'), align: 'r', value: (d) => toMs(d.next_end) || toMs(d.next_start), render: (d) => {
          const st = dtStatus(d, now);
          if (st === 'active') { const e = toMs(d.next_end) || toMs(d.end_ts); return e ? h('span', { title: fmtDateTime(e) }, t('dt.ends', { d: fmtRel(e) })) : h('span', { class: 'muted' }, t('dt.no_end')); }
          if (st === 'planned') { const s2 = toMs(d.next_start) || toMs(d.start_ts); return s2 ? h('span', { title: fmtDateTime(s2) }, t('dt.starts', { d: fmtRel(s2) })) : '-'; }
          return h('span', { class: 'muted' }, '-');
        } },
        { key: 'created_at', label: t('col.created'), align: 'r', value: (d) => toMs(d.created_at), render: (d) => (d.created_at ? h('span', { title: fmtDateTime(d.created_at) }, fmtRel(d.created_at)) : '-') },
        { key: 'act', label: '', sort: false, render: (d) => {
          if (!writer) return null;
          const st = dtStatus(d, now);
          return h('span', { class: 'row', style: { gap: '2px', justifyContent: 'flex-end' } },
            st === 'active' && !d.recurrence ? h('button', { class: 'btn sm', type: 'button', title: t('dt.end_now_title'), onclick: () => act(d, { end_ts: Date.now() }, 'dt.ended_done') }, t('dt.end_now')) : null,
            st !== 'ended' ? h('button', { class: 'btn sm ghost', type: 'button', onclick: () => act(d, { enabled: d.enabled === false }, d.enabled === false ? 'dt.enabled_done' : 'dt.disabled_done') }, t(d.enabled === false ? 'dt.enable' : 'dt.disable')) : null,
            h('a', { class: 'btn ghost sm icon', href: href('/downtimes/' + d.id + '/edit'), title: t('action.edit') }, icon('edit', 13)),
            h('button', { class: 'btn ghost sm icon danger', type: 'button', title: t('action.delete'), onclick: async () => {
              if (!(await confirmDialog({ title: t('dt.delete_title'), text: t('dt.delete_text', { name: d.name || '' }), confirmLabel: t('action.delete'), danger: true }))) return;
              try { await del('/downtimes/' + d.id); toast(t('dt.deleted'), 'ok'); ctx.refresh(); } catch (e) { toast(errorMessage(e), 'error'); }
            } }, icon('trash', 13)));
        } },
      ],
      rows: list, sortKey: 'status', sortDir: 'asc', emptyText: t('dt.no_match'),
      onRowClick: (d) => ctx.navigate('/downtimes/' + d.id + '/edit'),
      rowClass: (d) => (dtStatus(d, now) === 'active' ? 'dt-active' : null),
    }));
  }

  body.appendChild(loading());
  get('/monitors', null, { signal }).then((r) => { monitorsById = new Map(asList(r, 'monitors').map((m) => [String(m.id), m])); if (rows.length) draw(); }).catch(() => {});
  try {
    rows = asList(await get('/downtimes', null, { signal }), 'downtimes');
  } catch (e) {
    if (isAbort(e)) return;
    clear(body); body.appendChild(listErrorState(e, () => ctx.refresh())); return;
  }
  drawPills();
  draw();
}

// ── Formulaire ────────────────────────────────────────────────────────────
function parseTags(str) {
  const out = {};
  for (const part of String(str || '').split(',')) {
    const p = part.trim();
    const i = p.indexOf(':');
    if (i > 0) out[p.slice(0, i).trim()] = p.slice(i + 1).trim();
  }
  return out;
}

function durationField(valueS, onChange) {
  const v = Number(valueS) || 3600;
  const isPreset = DURATIONS.includes(v);
  const custom = h('input', { class: 'input', type: 'number', min: 1, step: 1, value: Math.round(v / 60), hidden: isPreset, 'aria-label': t('dt.f.minutes') });
  const sel = select(DURATIONS.map((s2) => ({ value: s2, label: fmtDuration(s2 * 1000, true) })).concat([{ value: 'custom', label: t('dt.f.custom_minutes') }]), isPreset ? v : 'custom', (x) => {
    if (x === 'custom') { custom.hidden = false; onChange((Number(custom.value) || 60) * 60); } else { custom.hidden = true; onChange(Number(x)); }
  });
  custom.addEventListener('input', () => { const n = Number(custom.value); if (n > 0) onChange(Math.round(n * 60)); });
  return h('div', { class: 'row' }, sel, custom);
}

function nextOccurrences(rec, fromMs, untilMs, n) {
  const [hh, mm] = String(rec.start || '00:00').split(':').map(Number);
  const out = [];
  const base = new Date(Math.max(fromMs || 0, Date.now() - 86400e3 * 31));
  for (let i = 0; i < 400 && out.length < n; i++) {
    const day = new Date(base.getFullYear(), base.getMonth(), base.getDate() + i, hh || 0, mm || 0);
    const dow = ((day.getDay() + 6) % 7) + 1;
    // Jour 31 en avril : le dernier jour du mois, comme le serveur (downtime.py).
    const lastDay = new Date(day.getFullYear(), day.getMonth() + 1, 0).getDate();
    const ok = rec.type === 'daily' || (rec.type === 'weekly' && (rec.days || []).includes(dow)) || (rec.type === 'monthly' && day.getDate() === Math.min(Number(rec.day_of_month) || 1, lastDay));
    if (!ok) continue;
    const a = day.getTime();
    const b = a + (Number(rec.duration_s) || 0) * 1000;
    if (b <= Date.now() || (fromMs && a < fromMs)) continue;
    if (untilMs && a > untilMs) break;
    out.push([a, b]);
  }
  return out;
}

async function renderForm(root, ctx) {
  const { args, params, signal, href } = ctx;
  const editing = !!args.id;
  ctx.setCrumbs([{ label: t('nav.downtimes'), href: href('/downtimes') }, { label: editing ? t('dt.edit_title') : t('dt.new') }]);
  const writer = canWrite(ctx.me);
  const now = Date.now();
  let d;
  if (editing) {
    root.appendChild(loading());
    try { const r = await get('/downtimes/' + encodeURIComponent(args.id), null, { signal }); d = r && r.downtime ? r.downtime : r; } catch (e) {
      if (isAbort(e)) return;
      clear(root); root.appendChild(errorState(e, () => ctx.refresh())); return;
    }
    clear(root);
  } else {
    let scope = {};
    try { const raw = params.get('scope'); if (raw) { const o = JSON.parse(raw); if (o && typeof o === 'object') scope = o; } } catch (e) { scope = {}; }
    d = { name: params.get('name') || '', message: '', scope, start_ts: now, end_ts: now + 3600e3, recurrence: null, enabled: true };
  }
  const sc = scopeOf(d);
  const st = {
    name: d.name || '', message: d.message || '', enabled: d.enabled !== false,
    monitorIds: new Set(sc.monitor_ids.map(String)),
    scopeMode: sc.monitor_ids.length ? 'monitors' : 'all',
    host: sc.tags.host || '',
    tags: Object.entries(sc.tags).filter(([k]) => k !== 'host').map(([k, v]) => k + ':' + v).join(', '),
    groups: sc.groups.join('\n'),
    when: d.recurrence && d.recurrence.type ? 'recurring' : 'once',
    startMode: editing ? 'at' : 'now',
    start: toMs(d.start_ts) || now,
    endMode: d.end_ts === null && !d.recurrence ? 'none' : 'at',
    end: toMs(d.end_ts) || now + 3600e3,
    durationS: 3600,
    rec: Object.assign({ type: 'weekly', days: [7], day_of_month: 1, start: '02:00', duration_s: 3600, tz: LOCAL_TZ, until: null }, d.recurrence || {}),
  };
  if (!editing) st.endMode = 'duration';
  if (!Array.isArray(st.rec.days)) st.rec.days = [7];

  root.appendChild(h('div', { class: 'page-head' }, h('h1', editing ? t('dt.edit_title') : t('dt.new')), editing ? statusBadge(dtStatus(d)) : null));
  const scopeEl = h('div', { class: 'col gap-3' });
  const whenEl = h('div', { class: 'col gap-3' });
  const previewEl = h('div', { class: 'col gap-3' });
  const errEl = h('div', { class: 'callout crit mt-3', hidden: true });
  const saveBtn = h('button', { class: 'btn primary', type: 'button', disabled: !writer }, editing ? t('action.save') : t('dt.create'));

  const nameIn = h('input', { class: 'input', maxlength: 200, value: st.name, placeholder: t('dt.f.name_ph') });
  nameIn.addEventListener('input', () => { st.name = nameIn.value; });
  const msgIn = h('textarea', { class: 'input', rows: 2, maxlength: 2000, placeholder: t('dt.f.message_ph') });
  msgIn.value = st.message;
  msgIn.addEventListener('input', () => { st.message = msgIn.value; });
  const enIn = h('input', { type: 'checkbox', checked: st.enabled });
  enIn.addEventListener('change', () => { st.enabled = enIn.checked; });

  root.appendChild(h('div', { class: 'split', 'data-no-autorefresh': '1' },
    h('section', { class: 'panel' }, h('div', { class: 'panel-body' },
      h('div', { class: 'form-section', style: { borderTop: '0', marginTop: '0', paddingTop: '0' } }, h('h3', h('span', { class: 'step-num' }, '1'), t('dt.step.what')),
        h('div', { class: 'form-grid' }, h('div', { class: 'full' }, field(t('dt.f.name'), nameIn)), h('div', { class: 'full' }, field(t('dt.f.message'), msgIn, t('dt.f.message_hint'))))),
      h('div', { class: 'form-section' }, h('h3', h('span', { class: 'step-num' }, '2'), t('dt.step.scope')), scopeEl),
      h('div', { class: 'form-section' }, h('h3', h('span', { class: 'step-num' }, '3'), t('dt.step.when')), whenEl),
      h('label', { class: 'check mt-4' }, enIn, t('dt.f.enabled')),
      errEl,
      h('div', { class: 'row mt-4' },
        editing && writer ? h('button', { class: 'btn danger', type: 'button', onclick: async () => {
          if (!(await confirmDialog({ title: t('dt.delete_title'), text: t('dt.delete_text', { name: d.name || '' }), confirmLabel: t('action.delete'), danger: true }))) return;
          try { await del('/downtimes/' + args.id); toast(t('dt.deleted'), 'ok'); ctx.navigate('/downtimes'); } catch (e) { toast(errorMessage(e), 'error'); }
        } }, icon('trash', 13), t('action.delete')) : null,
        h('span', { class: 'spacer' }), h('a', { class: 'btn', href: href('/downtimes') }, t('action.cancel')), saveBtn))),
    panel(t('dt.preview'), previewEl, { icon: 'eye' })));

  // Données de l'aperçu : moniteurs, problèmes en cours, hôtes.
  let monitors = [];
  let problems = [];
  let hosts = [];
  const dataP = Promise.all([
    get('/monitors', null, { signal }).then((r) => { monitors = asList(r, 'monitors'); }).catch(() => {}),
    get('/problems', null, { signal }).then((r) => { problems = asList(r, 'problems'); })
      .catch(() => get('/alerts', null, { signal }).then((r) => { problems = asList(r, 'alerts'); }).catch(() => {})),
    get('/hosts', null, { signal }).then((r) => { hosts = asList(r, 'hosts'); }).catch(() => {}),
  ]);

  function currentScope() {
    const tags = parseTags(st.tags);
    if (st.host) tags.host = st.host;
    return {
      monitor_ids: st.scopeMode === 'monitors' ? Array.from(st.monitorIds).map((x) => (/^\d+$/.test(x) ? Number(x) : x)) : [],
      tags,
      groups: st.groups.split(/\n+/).map((x) => x.trim()).filter(Boolean),
    };
  }

  function drawScope() {
    clear(scopeEl);
    scopeEl.appendChild(pills([{ key: 'all', label: t('dt.scope.all_monitors') }, { key: 'monitors', label: t('dt.scope.some_monitors') }], st.scopeMode,
      (k) => { st.scopeMode = k; drawScope(); drawPreview(); }));
    if (st.scopeMode === 'monitors') {
      const listBox = h('div', { class: 'pick-list' });
      let q = '';
      const drawList = () => {
        clear(listBox);
        const words = q.toLowerCase().split(/\s+/).filter(Boolean);
        const list = monitors.filter((m) => words.every((w) => String(m.name || '').toLowerCase().includes(w)));
        if (!monitors.length) { listBox.appendChild(h('div', { class: 'small muted', style: { padding: '8px' } }, t('dt.no_monitors'))); return; }
        for (const m of list.slice(0, 300)) {
          const cb = h('input', { type: 'checkbox', checked: st.monitorIds.has(String(m.id)) });
          cb.addEventListener('change', () => { if (cb.checked) st.monitorIds.add(String(m.id)); else st.monitorIds.delete(String(m.id)); count.textContent = tn('dt.scope.n_selected', st.monitorIds.size); drawPreview(); });
          listBox.appendChild(h('label', { class: 'check pick-row' }, cb, badgeState(monitorState(m)), priorityBadge(m.priority, { hideNone: true }), h('span', { class: 'ellipsis' }, m.name || '#' + m.id), h('span', { class: 'xsmall muted' }, '#' + m.id)));
        }
      };
      const count = h('span', { class: 'xsmall muted' }, tn('dt.scope.n_selected', st.monitorIds.size));
      const s2 = searchInput({ placeholder: t('dt.scope.search_monitors'), slash: false, onInput: (v) => { q = v; drawList(); } });
      scopeEl.appendChild(h('div', { class: 'col', style: { gap: '6px' } }, h('div', { class: 'row' }, s2, count), listBox));
      dataP.then(drawList);
      drawList();
    }
    const hostSel = h('select', { class: 'input' }, h('option', { value: '' }, t('dt.scope.any_host')));
    const fillHosts = () => {
      const names = Array.from(new Set(hosts.map((x) => x.hostname).filter(Boolean).concat(st.host ? [st.host] : []))).sort();
      while (hostSel.options.length > 1) hostSel.remove(1);
      names.forEach((n) => hostSel.appendChild(h('option', { value: n, selected: n === st.host }, n)));
    };
    hostSel.addEventListener('change', () => { st.host = hostSel.value; drawPreview(); });
    fillHosts();
    dataP.then(fillHosts);
    const tagsIn = h('input', { class: 'input mono', value: st.tags, placeholder: 'env:prod, role:web' });
    tagsIn.addEventListener('input', () => { st.tags = tagsIn.value; drawPreview(); });
    const groupsIn = h('textarea', { class: 'input mono', rows: 2, placeholder: 'host:web-01,unit:nginx.service' });
    groupsIn.value = st.groups;
    groupsIn.addEventListener('input', () => { st.groups = groupsIn.value; drawPreview(); });
    scopeEl.appendChild(h('div', { class: 'form-grid' },
      field(t('dt.scope.host'), hostSel, t('dt.scope.host_hint')),
      field(t('dt.scope.tags'), tagsIn, t('dt.scope.tags_hint')),
      h('div', { class: 'full' }, field(t('dt.scope.groups'), groupsIn, t('dt.scope.groups_hint')))));
  }

  function drawWhen() {
    clear(whenEl);
    whenEl.appendChild(pills([{ key: 'once', label: t('dt.when.once') }, { key: 'recurring', label: t('dt.when.recurring') }], st.when, (k) => { st.when = k; drawWhen(); drawPreview(); }));
    if (st.when === 'once') {
      const startIn = h('input', { class: 'input', type: 'datetime-local', value: toLocalInput(st.start), hidden: st.startMode === 'now' });
      startIn.addEventListener('change', () => { st.start = fromLocalInput(startIn.value) || st.start; drawPreview(); });
      const startPick = pills([{ key: 'now', label: t('dt.when.now') }, { key: 'at', label: t('dt.when.at') }], st.startMode, (k) => { st.startMode = k; drawWhen(); drawPreview(); });
      const endIn = h('input', { class: 'input', type: 'datetime-local', value: toLocalInput(st.end) });
      endIn.addEventListener('change', () => { st.end = fromLocalInput(endIn.value) || st.end; drawPreview(); });
      const endPick = pills([{ key: 'duration', label: t('dt.when.for') }, { key: 'at', label: t('dt.when.until') }, { key: 'none', label: t('dt.when.no_end') }], st.endMode, (k) => { st.endMode = k; drawWhen(); drawPreview(); });
      whenEl.appendChild(h('div', { class: 'form-grid' },
        field(t('dt.f.start'), h('div', { class: 'col', style: { gap: '6px' } }, startPick, startIn)),
        field(t('dt.f.end'), h('div', { class: 'col', style: { gap: '6px' } }, endPick,
          st.endMode === 'duration' ? durationField(st.durationS, (v) => { st.durationS = v; drawPreview(); }) : st.endMode === 'at' ? endIn : h('span', { class: 'small muted' }, t('dt.when.no_end_hint'))))));
    } else {
      const r = st.rec;
      const freq = pills(['daily', 'weekly', 'monthly'].map((k) => ({ key: k, label: t('dt.rec.' + k) })), r.type, (k) => { r.type = k; drawWhen(); drawPreview(); });
      const days = h('div', { class: 'row wrap' }, [1, 2, 3, 4, 5, 6, 7].map((n) => {
        const cb = h('input', { type: 'checkbox', checked: r.days.includes(n) });
        cb.addEventListener('change', () => { r.days = cb.checked ? Array.from(new Set(r.days.concat([n]))).sort() : r.days.filter((x) => x !== n); drawPreview(); });
        return h('label', { class: 'check day-pick' }, cb, weekdayName(n, 'short'));
      }));
      const dom = h('input', { class: 'input', type: 'number', min: 1, max: 31, value: r.day_of_month || 1 });
      dom.addEventListener('input', () => { r.day_of_month = Math.max(1, Math.min(31, Number(dom.value) || 1)); drawPreview(); });
      const startIn = h('input', { class: 'input', type: 'time', value: r.start || '02:00' });
      startIn.addEventListener('change', () => { r.start = startIn.value || '00:00'; drawPreview(); });
      const tzIn = h('input', { class: 'input mono', value: r.tz || LOCAL_TZ, list: 'dt-tz-list' });
      tzIn.addEventListener('change', () => { r.tz = tzIn.value.trim() || LOCAL_TZ; drawPreview(); });
      let zones = [];
      try { zones = typeof Intl.supportedValuesOf === 'function' ? Intl.supportedValuesOf('timeZone') : []; } catch (e) { zones = []; }
      const tzList = h('datalist', { id: 'dt-tz-list' }, (zones.length ? zones : [LOCAL_TZ, 'UTC', 'Europe/Paris']).map((z) => h('option', { value: z })));
      const fromIn = h('input', { class: 'input', type: 'datetime-local', value: toLocalInput(st.start) });
      fromIn.addEventListener('change', () => { st.start = fromLocalInput(fromIn.value) || st.start; drawPreview(); });
      const untilIn = h('input', { class: 'input', type: 'datetime-local', value: toMs(r.until) ? toLocalInput(toMs(r.until)) : '' });
      untilIn.addEventListener('change', () => { r.until = fromLocalInput(untilIn.value); drawPreview(); });
      whenEl.appendChild(h('div', { class: 'form-grid' },
        h('div', { class: 'full' }, field(t('dt.rec.freq'), freq)),
        r.type === 'weekly' ? h('div', { class: 'full' }, field(t('dt.rec.days'), days)) : null,
        r.type === 'monthly' ? field(t('dt.rec.day_of_month'), dom, t('dt.rec.dom_hint')) : null,
        field(t('dt.rec.start'), startIn),
        field(t('dt.f.duration'), durationField(r.duration_s, (v) => { r.duration_s = v; drawPreview(); })),
        field(t('dt.rec.tz'), h('div', tzIn, tzList)),
        field(t('dt.rec.from'), fromIn),
        field(t('dt.rec.until'), untilIn, t('dt.rec.until_hint'))));
    }
  }

  function buildBody() {
    const body = { name: st.name.trim(), message: st.message.trim(), scope: currentScope(), enabled: st.enabled };
    if (st.when === 'once') {
      const a = st.startMode === 'now' ? Date.now() : st.start;
      body.start_ts = a;
      body.end_ts = st.endMode === 'none' ? null : st.endMode === 'duration' ? a + st.durationS * 1000 : st.end;
      body.recurrence = null;
    } else {
      const r = st.rec;
      const rec = { type: r.type, start: r.start || '00:00', duration_s: Number(r.duration_s) || 3600, tz: r.tz || LOCAL_TZ, until: toMs(r.until) || null };
      if (r.type === 'weekly') rec.days = r.days.slice().sort();
      if (r.type === 'monthly') rec.day_of_month = Number(r.day_of_month) || 1;
      body.start_ts = st.start || Date.now();
      body.end_ts = rec.until;
      body.recurrence = rec;
    }
    return body;
  }

  function validate(b) {
    const errs = [];
    if (!b.name) errs.push(t('form.required', { field: t('dt.f.name') }));
    if (!b.recurrence && b.end_ts !== null && b.end_ts <= b.start_ts) errs.push(t('dt.err.end_before'));
    if (b.recurrence) {
      if (b.recurrence.type === 'weekly' && !(b.recurrence.days || []).length) errs.push(t('dt.err.no_day'));
      if (!/^\d{2}:\d{2}$/.test(b.recurrence.start)) errs.push(t('dt.err.bad_time'));
      if (!(b.recurrence.duration_s > 0)) errs.push(t('dt.err.bad_duration'));
      if (b.recurrence.until && b.recurrence.until <= b.start_ts) errs.push(t('dt.err.until_before'));
    }
    return errs;
  }

  function scopeMatchesProblem(scope, p) {
    if (scope.monitor_ids.length && !scope.monitor_ids.map(String).includes(String(p.monitor_id))) return false;
    const gt = groupTags(p.group);
    const host = problemHost(p);
    for (const [k, v] of Object.entries(scope.tags)) {
      const val = k === 'host' ? (gt.host || host) : gt[k];
      if (String(val) !== String(v)) return false;
    }
    if (scope.groups.length && !scope.groups.includes(String(p.group || '*'))) return false;
    return true;
  }

  function drawPreview() {
    clear(previewEl);
    const b = buildBody();
    const scope = b.scope;
    const targeted = scope.monitor_ids.length ? monitors.filter((m) => scope.monitor_ids.map(String).includes(String(m.id))) : monitors;
    const restricted = Object.keys(scope.tags).length || scope.groups.length;
    previewEl.appendChild(h('div', h('div', { class: 'small text-2' }, t('dt.preview.monitors')),
      h('div', { class: 't-value num', style: { fontSize: 'var(--fs-xl)', fontWeight: '600' } }, scope.monitor_ids.length ? String(targeted.length) : t('dt.preview.all', { n: monitors.length })),
      restricted ? h('div', { class: 'xsmall muted' }, t('dt.preview.restricted')) : null));
    if (targeted.length && scope.monitor_ids.length) {
      previewEl.appendChild(h('ul', { class: 'list-plain small' }, targeted.slice(0, 12).map((m) => h('li', badgeState(monitorState(m)), h('span', { class: 'ellipsis' }, m.name || '#' + m.id))),
        targeted.length > 12 ? h('li', { class: 'muted' }, t('ack.more', { n: targeted.length - 12 })) : null));
    }
    const hit = problems.filter((p) => String(p.state || '').toUpperCase() !== 'OK' && scopeMatchesProblem(scope, p));
    previewEl.appendChild(h('div', { class: 'hr' }));
    previewEl.appendChild(h('div', { class: 'small text-2' }, tn('dt.preview.problems', hit.length)));
    if (hit.length) {
      previewEl.appendChild(h('ul', { class: 'list-plain small' }, hit.slice(0, 10).map((p) => h('li', badgeState(p.state), h('span', { class: 'ellipsis grow' }, (p.monitor_name || '#' + p.monitor_id)), h('span', { class: 'mono xsmall muted ellipsis', style: { maxWidth: '45%' } }, p.group || '*'))),
        hit.length > 10 ? h('li', { class: 'muted' }, t('ack.more', { n: hit.length - 10 })) : null));
    }
    previewEl.appendChild(h('div', { class: 'hr' }));
    if (b.recurrence) {
      previewEl.appendChild(h('div', { class: 'small text-2' }, t('dt.preview.next')));
      previewEl.appendChild(h('div', { class: 'small' }, describeSchedule(b)));
      if ((b.recurrence.tz || LOCAL_TZ) === LOCAL_TZ) {
        const occ = nextOccurrences(b.recurrence, b.start_ts, b.recurrence.until, 5);
        previewEl.appendChild(occ.length ? h('ul', { class: 'list-plain small' }, occ.map(([a, e]) => h('li', h('span', { class: 'num' }, fmtDateTime(a)), h('span', { class: 'muted' }, '→ ' + fmtHM(e)))))
          : h('div', { class: 'small muted' }, t('dt.preview.no_next')));
      } else previewEl.appendChild(h('div', { class: 'xsmall muted' }, t('dt.preview.other_tz', { tz: b.recurrence.tz })));
    } else {
      previewEl.appendChild(h('div', { class: 'small text-2' }, t('dt.preview.window')));
      previewEl.appendChild(h('div', { class: 'small' }, describeSchedule(b)));
    }
    previewEl.appendChild(h('div', { class: 'callout mt-2' }, icon('info', 16), h('span', { class: 'xsmall' }, t('dt.preview.note'))));
  }

  saveBtn.addEventListener('click', async () => {
    errEl.hidden = true;
    const b = buildBody();
    const errs = validate(b);
    if (errs.length) { clear(errEl); errEl.appendChild(icon('warn', 16)); errEl.appendChild(h('div', errs.map((e) => h('div', e)))); errEl.hidden = false; return; }
    saveBtn.disabled = true;
    try {
      if (editing) await put('/downtimes/' + args.id, b); else await post('/downtimes', b);
      toast(editing ? t('dt.saved') : t('dt.created'), 'ok');
      ctx.navigate('/downtimes');
    } catch (e) {
      saveBtn.disabled = false;
      clear(errEl); errEl.appendChild(icon('warn', 16)); errEl.appendChild(h('div', errorMessage(e))); errEl.hidden = false;
    }
  });

  drawScope();
  drawWhen();
  drawPreview();
  dataP.then(() => { if (previewEl.isConnected) drawPreview(); });
}
