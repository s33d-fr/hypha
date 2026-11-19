// Événements : flux chronologique, filtres, publication manuelle (POST /events).
import { h, clear, icon } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, post, asList } from '../api.js';
import { fmtTime, fmtWeekday, fmtDateTime, toMs } from '../format.js';
import { section, panel, empty, tagList, searchInput, pills, modal, field, select, toast, errorMessage } from '../components/ui.js';
import { timeChart } from '../components/chart.js';
import { qv } from './common.js';

const TYPES = ['error', 'warning', 'success', 'info'];
const AGENT_TYPES = ['agent_start', 'host_reboot', 'service_state', 'disk_full', 'custom'];

// Types d'événements de l'agent (§ 3.7) traduits ; tout autre type s'affiche tel quel.
function eventTypeLabel(type) {
  return AGENT_TYPES.includes(type) ? t('events.etype.' + type) : String(type);
}

export function eventRow(ev, ctx, opts = {}) {
  const type = TYPES.includes(ev.alert_type) ? ev.alert_type : 'info';
  const text = ev.text ? String(ev.text) : '';
  const long = text.length > 280;
  const textEl = text ? h('div', { class: 'ev-text' }, long ? text.slice(0, 280) + '…' : text) : null;
  if (long && textEl) {
    const more = h('button', { class: 'btn ghost sm', type: 'button' }, t('action.show_more'));
    more.addEventListener('click', () => { textEl.textContent = text; more.remove(); });
    textEl.appendChild(h('div', more));
  }
  const hostName = ev.hostname || (ev.tags && ev.tags.host) || null;
  return h('div', { class: 'ev' },
    h('span', { class: ['ev-bar', 'ev-' + type] }),
    h('span', { class: 'ev-time', title: fmtDateTime(ev.ts) }, opts.dateTime ? fmtDateTime(ev.ts) : fmtTime(ev.ts)),
    h('div', { style: { minWidth: '0' } },
      h('div', { class: 'row wrap', style: { gap: '6px' } },
        h('span', { class: 'ev-title' }, ev.title || t('events.untitled')),
        ev.source ? h('span', { class: 'badge muted' }, ev.source) : null,
        ev.event_type || ev.type ? h('span', { class: 'badge muted' }, eventTypeLabel(ev.event_type || ev.type)) : null,
        hostName && ev.host_id ? h('a', { class: 'small', href: ctx.href('/infrastructure/host/' + ev.host_id) }, hostName) : hostName ? h('span', { class: 'small muted' }, hostName) : null),
      textEl,
      ev.tags && Object.keys(ev.tags).length ? h('div', { class: 'mt-2' }, tagList(ev.tags, opts.onTag || null)) : null));
}

function bucketize(events, range, n = 60) {
  const step = (range.to - range.from) / n;
  const xs = [];
  const by = Object.fromEntries(TYPES.map((k) => [k, new Array(n).fill(0)]));
  for (let i = 0; i < n; i++) xs.push(Math.round(range.from + i * step));
  for (const ev of events) {
    const ms = toMs(ev.ts);
    if (ms === null) continue;
    const i = Math.min(n - 1, Math.max(0, Math.floor((ms - range.from) / step)));
    const type = TYPES.includes(ev.alert_type) ? ev.alert_type : 'info';
    by[type][i]++;
  }
  return { xs, by };
}

function publishDialog(onDone) {
  const title = h('input', { class: 'input', maxlength: 300 });
  const text = h('textarea', { class: 'input', rows: 4 });
  const type = select(TYPES.map((k) => ({ value: k, label: t('events.type.' + k) })), 'info');
  const tags = h('input', { class: 'input mono', placeholder: 'env:prod, service:api' });
  const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
  const btn = h('button', { class: 'btn primary', type: 'button' }, t('events.publish'));
  const m = modal({
    title: t('events.publish_title'),
    body: h('div', { class: 'col gap-3' }, field(t('events.f.title'), title), field(t('events.f.text'), text), field(t('events.f.type'), type),
      field(t('events.f.tags'), tags, t('events.f.tags_hint')), err),
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), btn],
  });
  btn.addEventListener('click', async () => {
    if (!title.value.trim()) { err.textContent = t('form.required', { field: t('events.f.title') }); err.hidden = false; return; }
    const tagObj = {};
    for (const part of tags.value.split(',')) {
      const p = part.trim();
      if (!p) continue;
      const i = p.indexOf(':');
      if (i > 0) tagObj[p.slice(0, i).trim()] = p.slice(i + 1).trim();
    }
    btn.disabled = true;
    try {
      await post('/events', { ts: Date.now(), title: title.value.trim(), text: text.value, alert_type: type.value, priority: 'normal', source: 'user', tags: tagObj });
      m.close();
      toast(t('events.published'), 'ok');
      onDone();
    } catch (e) {
      btn.disabled = false;
      err.textContent = errorMessage(e);
      err.hidden = false;
    }
  });
}

export async function render(root, ctx) {
  ctx.setCrumbs([{ label: t('nav.events') }]);
  const { range, signal, params } = ctx;
  const q = params.get('q') || '';
  let typeFilter = params.get('type') || 'all';
  let sourceFilter = params.get('source') || '';

  const chartEl = h('div', { class: 'panel-body' });
  const listEl = h('div');
  const countEl = h('span', { class: 'sub' });
  const sourceSlot = h('span');
  const search = searchInput({
    placeholder: t('events.search_ph'), value: q,
    onEnter: (v) => ctx.setParams({ q: v.trim() || null }),
  });
  search.style.flex = '1 1 320px';

  root.appendChild(h('div', { class: 'querybar' },
    search,
    pills([{ key: 'all', label: t('events.type.all') }].concat(TYPES.map((k) => ({ key: k, label: t('events.type.' + k) }))), typeFilter,
      (k) => { typeFilter = k; ctx.setParams({ type: k === 'all' ? null : k }, { silent: true }); draw(); }),
    sourceSlot,
    h('button', { class: 'btn primary', type: 'button', onclick: () => publishDialog(() => ctx.refresh()) }, icon('plus', 14), t('events.publish'))));
  root.appendChild(h('section', { class: 'panel mb-3' }, chartEl));
  root.appendChild(panel(t('events.stream'), listEl, { flush: true, actions: countEl }));

  let events = [];

  function filtered() {
    return events.filter((ev) => (typeFilter === 'all' || (ev.alert_type || 'info') === typeFilter) && (!sourceFilter || ev.source === sourceFilter));
  }

  function draw() {
    const list = filtered().slice().sort((a, b) => (toMs(b.ts) || 0) - (toMs(a.ts) || 0));
    countEl.textContent = tn('events.count', list.length);
    clear(chartEl);
    const { xs, by } = bucketize(list, range);
    const colors = { error: 'var(--crit)', warning: 'var(--warn)', success: 'var(--ok)', info: 'var(--accent)' };
    const css = getComputedStyle(document.documentElement);
    const resolve = (v) => css.getPropertyValue(v.slice(4, -1)).trim();
    chartEl.appendChild(timeChart({
      xs, unit: 'count', height: 90, display: 'bars', stacked: true, xRange: [range.from, range.to], onRange: ctx.onRange, precision: 0,
      lines: ['info', 'success', 'warning', 'error'].map((k) => ({ label: t('events.type.' + k), values: by[k], color: resolve(colors[k]) })),
      emptyText: t('events.none_range'),
    }));
    clear(listEl);
    if (!list.length) { listEl.appendChild(empty(t('events.none_title'), t('events.none_range'))); return; }
    let day = null;
    for (const ev of list.slice(0, 500)) {
      const d = fmtWeekday(ev.ts);
      if (d !== day) { day = d; listEl.appendChild(h('div', { class: 'ev-day' }, d)); }
      listEl.appendChild(eventRow(ev, ctx, { onTag: (k, v) => ctx.setParams({ q: (q ? q + ' ' : '') + k + ':' + qv(v) }) }));
    }
    if (list.length > 500) listEl.appendChild(h('div', { class: 'tbl-foot' }, t('table.truncated', { shown: 500, total: list.length })));
  }

  await section(listEl, async () => asList(await get('/events', { q, from: range.from, to: range.to }, { signal }), 'events'), (list) => {
    events = list;
    const sources = Array.from(new Set(list.map((e) => e.source).filter(Boolean))).sort();
    clear(sourceSlot);
    if (sources.length > 1) {
      sourceSlot.appendChild(select([{ value: '', label: t('events.all_sources') }].concat(sources.map((s2) => ({ value: s2, label: s2 }))), sourceFilter,
        (v) => { sourceFilter = v; ctx.setParams({ source: v || null }, { silent: true }); draw(); }, { style: { width: 'auto' } }));
    }
    draw();
    return null;
  });
}
