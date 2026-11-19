// Réglages › Audit (§ 24) : qui a fait quoi, quand, d'où. Filtres, pagination, détail avant/après.
import { h, clear, icon, debounce } from '../dom.js';
import { t, tn, has } from '../i18n.js';
import { get, isAbort } from '../api.js';
import { fmtDateTime, fmtDateTimeFull, fmtRel, toMs } from '../format.js';
import { panel, empty, select, drawer, kv, searchInput, loading } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { jsonTree } from '../components/jsontree.js';
import { isAdmin, listErrorState } from './ops-shared.js';

const OBJECT_TYPES = ['monitor', 'dashboard', 'key', 'channel', 'member', 'downtime', 'incident', 'action', 'status_page', 'report', 'slo', 'synthetic',
  'log_filter', 'log_metric', 'sensitive_rule', 'view', 'service', 'agent', 'fleet', 'error', 'org', 'session'];
const ACTIONS = ['create', 'update', 'delete', 'login', 'logout', 'ack', 'unack', 'mute', 'approve', 'reject', 'run', 'test', 'send', 'rotate', 'upgrade'];
const PAGE = 100;

const label = (prefix, k) => (has(prefix + k) ? t(prefix + k) : k);
const isObj = (v) => v && typeof v === 'object' && !Array.isArray(v);

function diffParts(diff) {
  const d = isObj(diff) ? diff : {};
  const before = d.avant !== undefined ? d.avant : d.before;
  const after = d.apres !== undefined ? d.apres : d.after;
  return { before, after };
}

// Clés changées entre avant et après (premier niveau ; les objets sont comparés en entier).
function changedKeys(before, after) {
  if (!isObj(before) && !isObj(after)) return [];
  const keys = new Set(Object.keys(isObj(before) ? before : {}).concat(Object.keys(isObj(after) ? after : {})));
  const out = [];
  for (const k of keys) {
    const a = isObj(before) ? before[k] : undefined;
    const b = isObj(after) ? after[k] : undefined;
    if (JSON.stringify(a) !== JSON.stringify(b)) out.push({ key: k, a, b });
  }
  return out.sort((x, y) => x.key.localeCompare(y.key));
}

function valueCell(v) {
  if (v === undefined) return h('span', { class: 'muted' }, t('audit.absent'));
  if (v === null) return h('span', { class: 'muted mono' }, 'null');
  if (typeof v === 'object') return h('code', { class: 'audit-json' }, JSON.stringify(v));
  return h('span', { class: 'mono small' }, String(v));
}

function openEntry(e) {
  const { before, after } = diffParts(e.diff);
  const changes = changedKeys(before, after);
  const body = h('div', { class: 'col gap-4' },
    kv([
      [t('col.date'), fmtDateTimeFull(e.ts)],
      [t('audit.col.actor'), e.actor || '-'],
      [t('audit.col.action'), label('audit.action.', e.action || '')],
      [t('audit.col.object'), h('span', label('audit.obj.', e.object_type || ''), e.object_id !== null && e.object_id !== undefined ? h('span', { class: 'mono muted' }, ' #' + e.object_id) : null)],
      [t('audit.col.summary'), e.summary || null],
      [t('audit.col.ip'), e.ip ? h('span', { class: 'mono small' }, e.ip) : null],
      [t('audit.col.agent'), e.user_agent ? h('span', { class: 'xsmall muted' }, e.user_agent) : undefined],
    ]));
  if (changes.length) {
    body.appendChild(h('div', h('h4', { class: 'mb-2' }, tn('audit.changes', changes.length)),
      h('div', { class: 'table-wrap' }, h('table', { class: 'tbl audit-diff' },
        h('thead', h('tr', h('th', t('audit.field')), h('th', t('audit.before')), h('th', t('audit.after')))),
        h('tbody', changes.map((c) => h('tr', h('td', { class: 'mono small' }, c.key), h('td', { class: 'wrap del' }, valueCell(c.a)), h('td', { class: 'wrap add' }, valueCell(c.b)))))))));
  } else if (before === undefined && after === undefined) {
    body.appendChild(h('div', { class: 'small muted' }, t('audit.no_diff')));
  }
  if (before !== undefined || after !== undefined) {
    body.appendChild(h('div', { class: 'grid c2' },
      panel(t('audit.before'), before === undefined ? h('span', { class: 'muted small' }, t('audit.absent')) : jsonTree(before)),
      panel(t('audit.after'), after === undefined ? h('span', { class: 'muted small' }, t('audit.absent')) : jsonTree(after))));
  }
  body.appendChild(h('div', { class: 'xsmall muted' }, t('audit.secrets_note')));
  drawer({ title: label('audit.action.', e.action || '') + ' · ' + label('audit.obj.', e.object_type || ''), body });
}

export async function renderAudit(root, ctx) {
  const { range, params, signal } = ctx;
  if (!isAdmin(ctx.me)) { root.appendChild(h('div', { class: 'callout' }, icon('lock', 16), h('span', { class: 'small' }, t('audit.admin_only')))); return; }
  const f = { actor: params.get('actor') || '', object_type: params.get('object_type') || '', action: params.get('action') || '', q: params.get('q') || '' };
  const persist = () => ctx.setParams({ actor: f.actor || null, object_type: f.object_type || null, action: f.action || null, q: f.q || null }, { silent: true });
  const listEl = h('div');
  const countEl = h('span', { class: 'sub' });
  const moreEl = h('div');
  const actor = h('input', { class: 'input', value: f.actor, placeholder: t('audit.actor_ph'), style: { width: '200px' } });
  const reload = debounce(() => load(true), 400);
  actor.addEventListener('input', () => { f.actor = actor.value.trim(); persist(); reload(); });
  const search = searchInput({ placeholder: t('audit.search_ph'), value: f.q, onInput: (v) => { f.q = v.trim(); persist(); reload(); } });
  search.style.flex = '1 1 240px';
  root.appendChild(h('div', { class: 'querybar' }, search, actor,
    select([{ value: '', label: t('audit.all_objects') }].concat(OBJECT_TYPES.map((k) => ({ value: k, label: label('audit.obj.', k) }))), f.object_type, (v) => { f.object_type = v; persist(); load(true); }, { style: { width: 'auto' } }),
    select([{ value: '', label: t('audit.all_actions') }].concat(ACTIONS.map((k) => ({ value: k, label: label('audit.action.', k) }))), f.action, (v) => { f.action = v; persist(); load(true); }, { style: { width: 'auto' } })));
  root.appendChild(h('div', { class: 'xsmall muted mb-2' }, t('audit.range_hint'), ' ', t('audit.retention')));
  root.appendChild(panel(t('nav.settings.audit'), h('div', listEl, moreEl), { flush: true, actions: countEl }));

  let rows = [];
  let cursor = null;
  let ctrl = null;
  signal.addEventListener('abort', () => { if (ctrl) ctrl.abort(); }, { once: true });

  function draw() {
    clear(listEl);
    countEl.textContent = tn('audit.count', rows.length) + (cursor ? '+' : '');
    if (!rows.length) { listEl.appendChild(empty(t('audit.none_title'), t('audit.none_text'))); return; }
    listEl.appendChild(dataTable({
      columns: [
        { key: 'ts', label: t('col.date'), value: (e) => toMs(e.ts), render: (e) => h('span', { class: 'num', title: fmtRel(e.ts) }, fmtDateTime(e.ts)) },
        { key: 'actor', label: t('audit.col.actor'), tdCls: 'trunc', render: (e) => h('span', { title: e.actor || '' }, e.actor || '-') },
        { key: 'action', label: t('audit.col.action'), render: (e) => h('span', { class: ['badge', 'audit-' + (e.action || '')] }, label('audit.action.', e.action || '')) },
        { key: 'object', label: t('audit.col.object'), value: (e) => (e.object_type || '') + (e.object_id || ''), render: (e) => h('span', label('audit.obj.', e.object_type || ''), e.object_id !== null && e.object_id !== undefined ? h('span', { class: 'mono muted small' }, ' #' + e.object_id) : null) },
        { key: 'summary', label: t('audit.col.summary'), tdCls: 'trunc', render: (e) => h('span', { title: e.summary || '' }, e.summary || '-') },
        { key: 'ip', label: t('audit.col.ip'), render: (e) => h('span', { class: 'mono small muted' }, e.ip || '-') },
        { key: 'diff', label: '', sort: false, render: (e) => { const d = diffParts(e.diff); return d.before !== undefined || d.after !== undefined ? h('span', { class: 'muted', title: t('audit.has_diff') }, icon('history', 13)) : null; } },
      ],
      rows, sortKey: 'ts', sortDir: 'desc', onRowClick: openEntry,
    }));
  }

  async function load(reset) {
    if (ctrl) ctrl.abort();
    ctrl = new AbortController();
    const my = ctrl;
    if (reset) { rows = []; cursor = null; clear(listEl); listEl.appendChild(loading()); clear(moreEl); }
    try {
      const r = await get('/audit', {
        from: range.from, to: range.to, actor: f.actor, object_type: f.object_type, action: f.action, q: f.q, limit: PAGE, cursor: reset ? null : cursor,
      }, { signal: my.signal });
      if (my !== ctrl) return;
      const entries = Array.isArray(r) ? r : (r && Array.isArray(r.entries) ? r.entries : []);
      rows = rows.concat(entries);
      cursor = r && !Array.isArray(r) ? r.next_cursor || null : null;
      draw();
      clear(moreEl);
      if (cursor) moreEl.appendChild(h('div', { class: 'tbl-foot' }, h('button', { class: 'btn sm', type: 'button', onclick: () => load(false) }, t('action.show_more'))));
    } catch (e) {
      if (isAbort(e) || my !== ctrl) return;
      clear(listEl);
      listEl.appendChild(listErrorState(e, () => load(true)));
    }
  }
  await load(true);
}
