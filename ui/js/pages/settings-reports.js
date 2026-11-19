// Réglages › Rapports programmés (§ 22) : liste, création, aperçu dans un cadre isolé, envoi immédiat.
import { h, clear, icon } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, post, put, del, asList, isAbort } from '../api.js';
import { fmtDateTime, fmtRel, fmtDuration, toMs } from '../format.js';
import { panel, empty, field, select, modal, toast, confirmDialog, errorMessage, errorState, loading } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { canWrite, listErrorState } from './ops-shared.js';
import { weekdayName } from './downtimes.js';

const KINDS = ['summary', 'slo', 'dashboard'];
const PERIODS = [86400, 7 * 86400, 30 * 86400];
const LOCAL_TZ = (() => { try { return Intl.DateTimeFormat().resolvedOptions().timeZone || 'Europe/Paris'; } catch (e) { return 'Europe/Paris'; } })();

function scheduleText(r) {
  const s2 = r.schedule || {};
  const hour = Number.isFinite(Number(s2.hour)) ? Number(s2.hour) : 8;
  const hh = String(hour).padStart(2, '0') + ':00';
  let txt;
  if (s2.every === 'week') txt = t('rep.sched.week_desc', { day: weekdayName(Number(s2.weekday) || 1), time: hh });
  else if (s2.every === 'month') txt = t('rep.sched.month_desc', { day: Number(s2.day) || 1, time: hh });
  else txt = t('rep.sched.day_desc', { time: hh });
  if (s2.tz && s2.tz !== LOCAL_TZ) txt += ' (' + s2.tz + ')';
  return txt;
}

function periodText(s2) { return t('rep.period_v', { d: fmtDuration((Number(s2) || 0) * 1000, true) }); }

function previewDialog(rep, ctx) {
  const frame = h('iframe', { class: 'rep-frame', title: t('rep.preview_title', { name: rep.name || '' }), sandbox: '', referrerpolicy: 'no-referrer' });
  const body = h('div', { class: 'col gap-3' }, h('div', { class: 'state sm' }, h('span', { class: 'spinner' }), h('div', { class: 'mt-2 small' }, t('rep.preview_loading'))));
  const m = modal({ title: t('rep.preview_title', { name: rep.name || '' }), body, wide: true,
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.close')),
      canWrite(ctx.me) ? h('button', { class: 'btn primary', type: 'button', onclick: async () => { m.close(); await sendNow(rep, ctx); } }, icon('send', 13), t('rep.send_now')) : null].filter(Boolean) });
  get('/reports/' + encodeURIComponent(rep.id) + '/preview', null, { signal: ctx.signal }).then((html) => {
    clear(body);
    const text = typeof html === 'string' ? html : (html && (html.html || html.preview)) || '';
    if (!text) { body.appendChild(empty(null, t('rep.preview_empty'))); return; }
    // Cadre sans script ni origine : le HTML du rapport ne peut rien exécuter ni lire.
    frame.srcdoc = text;
    body.appendChild(h('div', { class: 'xsmall muted' }, t('rep.preview_note')));
    body.appendChild(frame);
  }).catch((e) => { if (isAbort(e)) return; clear(body); body.appendChild(errorState(e)); });
  return m;
}

async function sendNow(rep, ctx) {
  if (!(await confirmDialog({ title: t('rep.send_title'), text: t('rep.send_text', { name: rep.name || '' }), confirmLabel: t('rep.send_now') }))) return;
  try {
    const r = await post('/reports/' + encodeURIComponent(rep.id) + '/send', {});
    const n = r && (r.sent ?? r.recipients ?? null);
    toast(n !== null && n !== undefined ? tn('rep.sent_n', Number(n)) : t('rep.sent'), 'ok');
    ctx.refresh();
  } catch (e) { toast(errorMessage(e), 'error'); }
}

function reportDialog(ctx, existing, { channels, dashboards }) {
  const r = existing ? JSON.parse(JSON.stringify(existing)) : {
    name: '', kind: 'summary', dashboard_id: null, channel_ids: [], schedule: { every: 'week', weekday: 1, day: 1, hour: 8, tz: LOCAL_TZ }, period_s: 7 * 86400, enabled: true,
  };
  r.schedule = Object.assign({ every: 'week', weekday: 1, day: 1, hour: 8, tz: LOCAL_TZ }, r.schedule || {});
  const emails = channels.filter((c) => c.kind === 'email');
  const box = h('div', { class: 'col gap-3' });
  const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
  function draw() {
    clear(box);
    const name = h('input', { class: 'input', maxlength: 200, value: r.name || '', placeholder: t('rep.f.name_ph') });
    name.addEventListener('input', () => { r.name = name.value; });
    box.appendChild(field(t('rep.f.name'), name));
    box.appendChild(field(t('rep.f.kind'), select(KINDS.map((k) => ({ value: k, label: t('rep.kind.' + k) })), r.kind, (v) => { r.kind = v; draw(); })));
    box.appendChild(h('div', { class: 'xsmall muted', style: { marginTop: '-6px' } }, t('rep.kind_desc.' + r.kind)));
    if (r.kind === 'dashboard') {
      if (!dashboards.length) box.appendChild(h('div', { class: 'small muted' }, t('rep.no_dashboards')));
      else {
        if (!r.dashboard_id) r.dashboard_id = dashboards[0].id;
        box.appendChild(field(t('rep.f.dashboard'), select(dashboards.map((d) => ({ value: d.id, label: d.name || d.title || '#' + d.id })), r.dashboard_id, (v) => { r.dashboard_id = /^\d+$/.test(v) ? Number(v) : v; })));
      }
    }
    const s2 = r.schedule;
    const every = select(['day', 'week', 'month'].map((k) => ({ value: k, label: t('rep.sched.' + k) })), s2.every, (v) => { s2.every = v; draw(); });
    const hour = select(Array.from({ length: 24 }, (_, i) => ({ value: i, label: String(i).padStart(2, '0') + ':00' })), s2.hour, (v) => { s2.hour = Number(v); });
    const grid = h('div', { class: 'form-grid' }, field(t('rep.f.every'), every));
    if (s2.every === 'week') grid.appendChild(field(t('rep.f.weekday'), select([1, 2, 3, 4, 5, 6, 7].map((n) => ({ value: n, label: weekdayName(n) })), s2.weekday || 1, (v) => { s2.weekday = Number(v); })));
    if (s2.every === 'month') grid.appendChild(field(t('rep.f.day'), select(Array.from({ length: 28 }, (_, i) => ({ value: i + 1, label: String(i + 1) })), s2.day || 1, (v) => { s2.day = Number(v); }), t('rep.f.day_hint')));
    grid.appendChild(field(t('rep.f.hour'), hour));
    const tz = h('input', { class: 'input mono', value: s2.tz || LOCAL_TZ });
    tz.addEventListener('change', () => { s2.tz = tz.value.trim() || LOCAL_TZ; });
    grid.appendChild(field(t('rep.f.tz'), tz));
    grid.appendChild(field(t('rep.f.period'), select(PERIODS.concat(PERIODS.includes(r.period_s) ? [] : [r.period_s]).sort((a, b) => a - b).map((p) => ({ value: p, label: periodText(p) })), r.period_s, (v) => { r.period_s = Number(v); }), t('rep.f.period_hint')));
    box.appendChild(h('div', h('h4', { class: 'mb-2' }, t('rep.f.when')), grid));
    const chans = h('div', { class: 'col', style: { gap: '4px' } });
    if (!emails.length) chans.appendChild(h('div', { class: 'small muted' }, t('rep.no_email_channel'), ' ', h('a', { href: ctx.href('/settings/channels') }, t('nav.settings.channels'))));
    for (const c of emails) {
      const cb = h('input', { type: 'checkbox', checked: (r.channel_ids || []).map(String).includes(String(c.id)) });
      cb.addEventListener('change', () => {
        const set = new Set((r.channel_ids || []).map(String));
        if (cb.checked) set.add(String(c.id)); else set.delete(String(c.id));
        r.channel_ids = Array.from(set).map((x) => (/^\d+$/.test(x) ? Number(x) : x));
      });
      const to = Array.isArray(c.config && c.config.to) ? c.config.to.join(', ') : '';
      chans.appendChild(h('label', { class: 'check' }, cb, c.name || '#' + c.id, to ? h('span', { class: 'xsmall muted mono' }, to) : null));
    }
    box.appendChild(field(t('rep.f.channels'), chans, t('rep.f.channels_hint')));
    const en = h('input', { type: 'checkbox', checked: r.enabled !== false });
    en.addEventListener('change', () => { r.enabled = en.checked; });
    box.appendChild(h('label', { class: 'check' }, en, t('rep.f.enabled')));
    box.appendChild(err);
  }
  draw();
  const btn = h('button', { class: 'btn primary', type: 'button' }, existing ? t('action.save') : t('rep.create'));
  const m = modal({ title: existing ? t('rep.edit') : t('rep.new'), body: box, actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), btn] });
  btn.addEventListener('click', async () => {
    err.hidden = true;
    if (!String(r.name || '').trim()) { err.textContent = t('form.required', { field: t('rep.f.name') }); err.hidden = false; return; }
    if (!(r.channel_ids || []).length) { err.textContent = t('rep.err.channels'); err.hidden = false; return; }
    if (r.kind === 'dashboard' && !r.dashboard_id) { err.textContent = t('form.required', { field: t('rep.f.dashboard') }); err.hidden = false; return; }
    const body = { name: r.name.trim(), kind: r.kind, dashboard_id: r.kind === 'dashboard' ? r.dashboard_id : null, channel_ids: r.channel_ids, schedule: r.schedule, period_s: r.period_s, enabled: r.enabled !== false };
    btn.disabled = true;
    try {
      if (existing) await put('/reports/' + existing.id, body); else await post('/reports', body);
      m.close(); toast(existing ? t('rep.saved') : t('rep.created'), 'ok'); ctx.refresh();
    } catch (e) { btn.disabled = false; err.textContent = errorMessage(e); err.hidden = false; }
  });
}

export async function renderReports(root, ctx) {
  const { signal } = ctx;
  const writer = canWrite(ctx.me);
  const listEl = h('div');
  let channels = [];
  let dashboards = [];
  const refsP = Promise.all([
    get('/channels', null, { signal }).then((r) => { channels = asList(r, 'channels'); }).catch(() => {}),
    get('/dashboards', null, { signal }).then((r) => { dashboards = asList(r, 'dashboards'); }).catch(() => {}),
  ]);
  const openDialog = async (rep) => { await refsP; reportDialog(ctx, rep, { channels, dashboards }); };
  root.appendChild(h('div', { class: 'querybar' }, h('span', { class: 'muted small grow' }, t('rep.intro')),
    writer ? h('button', { class: 'btn primary', type: 'button', onclick: () => openDialog(null) }, icon('plus', 14), t('rep.new')) : null));
  root.appendChild(panel(t('nav.settings.reports'), listEl, { flush: true }));
  listEl.appendChild(loading());
  let list;
  try { list = asList(await get('/reports', null, { signal }), 'reports'); } catch (e) {
    if (isAbort(e)) return;
    clear(listEl); listEl.appendChild(listErrorState(e, () => ctx.refresh())); return;
  }
  await refsP;
  clear(listEl);
  if (!list.length) { listEl.appendChild(empty(t('rep.none_title'), t('rep.none_text'), writer ? h('button', { class: 'btn primary', type: 'button', onclick: () => openDialog(null) }, icon('plus', 14), t('rep.new')) : null)); return; }
  const chanName = (id) => { const c = channels.find((x) => String(x.id) === String(id)); return c ? c.name : '#' + id; };
  const dashName = (id) => { const d = dashboards.find((x) => String(x.id) === String(id)); return d ? (d.name || d.title) : '#' + id; };
  listEl.appendChild(dataTable({
    columns: [
      { key: 'enabled', label: t('col.status'), value: (r) => (r.enabled === false ? 0 : 1), render: (r) => (r.enabled === false ? h('span', { class: 'badge muted' }, t('rep.disabled')) : h('span', { class: 'badge ok' }, t('rep.enabled'))) },
      { key: 'name', label: t('col.name'), render: (r) => h('b', r.name || '#' + r.id) },
      { key: 'kind', label: t('col.type'), render: (r) => t('rep.kind.' + r.kind) + (r.kind === 'dashboard' && r.dashboard_id ? ' · ' + dashName(r.dashboard_id) : '') },
      { key: 'schedule', label: t('rep.col.schedule'), sort: false, render: (r) => h('span', { class: 'small' }, scheduleText(r), ' · ', periodText(r.period_s)) },
      { key: 'channels', label: t('rep.col.channels'), sort: false, tdCls: 'trunc', render: (r) => h('span', { class: 'small' }, (r.channel_ids || []).map(chanName).join(', ') || '-') },
      { key: 'last_sent_at', label: t('rep.col.last_sent'), align: 'r', value: (r) => toMs(r.last_sent_at), render: (r) => (r.last_sent_at ? h('span', { title: fmtDateTime(r.last_sent_at) }, fmtRel(r.last_sent_at)) : h('span', { class: 'muted' }, t('rep.never'))) },
      { key: 'act', label: '', sort: false, render: (r) => h('span', { class: 'row', style: { gap: '2px', justifyContent: 'flex-end' } },
        h('button', { class: 'btn ghost sm', type: 'button', onclick: () => previewDialog(r, ctx) }, icon('eye', 13), t('rep.preview')),
        writer ? h('button', { class: 'btn ghost sm', type: 'button', onclick: () => sendNow(r, ctx) }, icon('send', 13), t('rep.send_now')) : null,
        writer ? h('button', { class: 'btn ghost sm icon', type: 'button', title: t('action.edit'), onclick: () => openDialog(r) }, icon('edit', 13)) : null,
        writer ? h('button', { class: 'btn ghost sm icon danger', type: 'button', title: t('action.delete'), onclick: async () => {
          if (!(await confirmDialog({ title: t('rep.delete_title'), text: t('rep.delete_text', { name: r.name || '' }), confirmLabel: t('action.delete'), danger: true }))) return;
          try { await del('/reports/' + r.id); toast(t('rep.deleted'), 'ok'); ctx.refresh(); } catch (e) { toast(errorMessage(e), 'error'); }
        } }, icon('trash', 13)) : null) },
    ],
    rows: list, sortKey: 'name', sortDir: 'asc', onRowClick: (r) => previewDialog(r, ctx),
  }));
}
