// Incidents (§ 20), pensé ITIL : déclarer, suivre, résoudre, tirer la leçon.
// Liste (actifs / stables / résolus) et page d'un incident : en-tête modifiable, chronologie avec
// notes et mises à jour publiques, liens, graphiques des moniteurs liés, post-mortem rédigé par l'IA.
import { h, clear, icon, cssVar } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, post, put, del, asList, isAbort, queryMetrics } from '../api.js';
import { fmtDateTime, fmtDuration, fmtRel, toMs, isNum } from '../format.js';
import { panel, empty, field, select, pills, tabs, modal, toast, confirmDialog, errorMessage, errorState, loading, searchInput, badgeState, kv } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { timeChart, linesFromMetrics } from '../components/chart.js';
import { renderMarkdown, explainEnabled } from '../components/explain.js';
import { histogramChart } from './logs-shared.js';
import { monitorState } from './monitors-shared.js';
import { canWrite, isAdmin, listErrorState, liveDuration, severityBadge, PRIORITIES } from './ops-shared.js';

const STATUSES = ['active', 'stable', 'resolved'];
const LINK_KINDS = ['monitor', 'host', 'slo', 'synthetic', 'service', 'issue'];
const METRIC_LIKE = ['metric', 'anomaly', 'forecast', 'outlier', 'change'];
const AI_TIMEOUT_MS = 240000;

export async function render(root, ctx) {
  if (ctx.route.view === 'detail') return renderDetail(root, ctx);
  return renderList(root, ctx);
}

function incLabel(inc) { return 'INC-' + (inc.number !== undefined && inc.number !== null ? inc.number : inc.id); }

function statusBadge(st) {
  const cls = { active: 'alert', stable: 'warn', resolved: 'ok' }[st] || 'muted';
  return h('span', { class: ['badge', cls] }, h('span', { class: ['dot', cls === 'alert' ? 'alert' : cls] }), t('inc.status.' + (STATUSES.includes(st) ? st : 'active')));
}

function durationNode(inc) {
  const a = toMs(inc.detected_at) || toMs(inc.created_at);
  const b = toMs(inc.resolved_at);
  if (!a) return h('span', { class: 'muted' }, '-');
  if (b) return h('span', { class: 'num', title: fmtDateTime(a) + ' → ' + fmtDateTime(b) }, fmtDuration(b - a, true));
  return liveDuration(a);
}

async function loadMembers(signal) {
  try { return asList(await get('/org/members', null, { signal }), 'members'); } catch (e) { if (isAbort(e)) throw e; return []; }
}
const memberId = (u) => (u.id !== undefined ? u.id : u.user_id);
function memberName(members, id) {
  if (id === null || id === undefined) return null;
  const u = members.find((x) => String(memberId(x)) === String(id));
  return u ? (u.name || u.email) : '#' + id;
}

function severitySelect(value, onChange, attrs) {
  return select(PRIORITIES.map((n) => ({ value: n, label: 'SEV-' + n + ' · ' + t('inc.sev.' + n) })), value || 3, (v) => onChange(Number(v)), attrs);
}

function commanderSelect(members, value, onChange, attrs) {
  return select([{ value: '', label: t('inc.no_commander') }].concat(members.map((u) => ({ value: memberId(u), label: u.name || u.email || '#' + memberId(u) }))),
    value === null || value === undefined ? '' : value, (v) => onChange(v === '' ? null : (/^\d+$/.test(v) ? Number(v) : v)), attrs);
}

// ── Déclarer ──────────────────────────────────────────────────────────────
export function declareDialog(ctx, members, preset = {}) {
  const title = h('input', { class: 'input', maxlength: 200, value: preset.title || '', placeholder: t('inc.f.title_ph') });
  let severity = preset.severity || 3;
  let commander = ctx.me && ctx.me.user ? ctx.me.user.id : null;
  const impact = h('textarea', { class: 'input', rows: 3, maxlength: 4000, placeholder: t('inc.f.impact_ph') });
  const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
  const btn = h('button', { class: 'btn primary', type: 'button' }, icon('siren', 14), t('inc.declare'));
  const m = modal({
    title: t('inc.declare_title'),
    body: h('div', { class: 'col gap-3' },
      h('p', { class: 'small text-2' }, t('inc.declare_text')),
      field(t('inc.f.title'), title),
      h('div', { class: 'form-grid' },
        field(t('inc.f.severity'), severitySelect(severity, (v) => { severity = v; }), t('inc.f.severity_hint')),
        field(t('inc.f.commander'), commanderSelect(members, commander, (v) => { commander = v; }))),
      field(t('inc.f.impact'), impact), err),
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), btn],
  });
  btn.addEventListener('click', async () => {
    if (!title.value.trim()) { err.textContent = t('form.required', { field: t('inc.f.title') }); err.hidden = false; return; }
    btn.disabled = true;
    try {
      const body = { title: title.value.trim(), severity, customer_impact: impact.value.trim(), links: preset.links || [] };
      if (commander !== null) body.commander_id = commander;
      const r = await post('/incidents', body);
      const inc = r && r.incident ? r.incident : r;
      m.close();
      toast(t('inc.declared', { n: inc && inc.number ? 'INC-' + inc.number : '' }), 'ok');
      if (inc && inc.id !== undefined) ctx.navigate('/incidents/' + inc.id); else ctx.refresh();
    } catch (e) { btn.disabled = false; err.textContent = errorMessage(e); err.hidden = false; }
  });
}

// ── Liste ─────────────────────────────────────────────────────────────────
async function renderList(root, ctx) {
  const { signal, href, params } = ctx;
  ctx.setCrumbs([{ label: t('nav.incidents') }]);
  const writer = canWrite(ctx.me);
  let tab = [...STATUSES, 'all'].includes(params.get('status')) ? params.get('status') : 'open';
  let sev = params.get('severity') || '';
  let q = params.get('q') || '';
  let members = [];
  const membersP = loadMembers(signal).then((x) => { members = x; return x; });
  const body = h('div');
  const tabSlot = h('div', { class: 'mb-3' });
  const countEl = h('span', { class: 'sub' });
  const search = searchInput({ placeholder: t('inc.search_ph'), value: q, onInput: (v) => { q = v; ctx.setParams({ q: v || null }, { silent: true }); draw(); } });
  search.style.flex = '1 1 240px';
  root.appendChild(h('div', { class: 'page-head' }, h('h1', t('nav.incidents')), h('span', { class: 'desc' }, t('inc.desc'))));
  root.appendChild(tabSlot);
  root.appendChild(h('div', { class: 'querybar' }, search,
    select([{ value: '', label: t('inc.all_sev') }].concat(PRIORITIES.map((n) => ({ value: String(n), label: 'SEV-' + n + ' · ' + t('inc.sev.' + n) }))), sev,
      (v) => { sev = v; ctx.setParams({ severity: v || null }, { silent: true }); draw(); }, { style: { width: 'auto' } }),
    h('span', { class: 'spacer' }),
    writer ? h('button', { class: 'btn primary', type: 'button', onclick: async () => declareDialog(ctx, await membersP) }, icon('siren', 14), t('inc.declare')) : null));
  root.appendChild(panel(t('nav.incidents'), body, { flush: true, actions: countEl }));
  body.appendChild(loading());

  let rows = [];
  function drawTabs() {
    clear(tabSlot);
    const c = { open: 0, active: 0, stable: 0, resolved: 0, all: rows.length };
    rows.forEach((x) => { const st = STATUSES.includes(x.status) ? x.status : 'active'; c[st]++; if (st !== 'resolved') c.open++; });
    tabSlot.appendChild(tabs(['open', 'active', 'stable', 'resolved', 'all'].map((k) => ({ key: k, label: t('inc.tab.' + k), count: c[k] })), tab,
      (k) => { tab = k; ctx.setParams({ status: k === 'open' ? null : k }, { silent: true }); drawTabs(); draw(); }));
  }
  function draw() {
    clear(body);
    const words = q.toLowerCase().split(/\s+/).filter(Boolean);
    const list = rows.filter((x) => {
      const st = STATUSES.includes(x.status) ? x.status : 'active';
      if (tab === 'open' && st === 'resolved') return false;
      if (STATUSES.includes(tab) && st !== tab) return false;
      if (sev && String(x.severity) !== sev) return false;
      return words.every((w) => [incLabel(x), x.title, x.customer_impact, x.summary].join(' ').toLowerCase().includes(w));
    });
    countEl.textContent = tn('inc.count', list.length);
    if (!rows.length) {
      body.appendChild(empty(t('inc.none_title'), t('inc.none_text'), writer ? h('button', { class: 'btn primary', type: 'button', onclick: async () => declareDialog(ctx, await membersP) }, icon('siren', 14), t('inc.declare')) : null));
      return;
    }
    const rank = { active: 0, stable: 1, resolved: 2 };
    body.appendChild(dataTable({
      columns: [
        { key: 'number', label: t('inc.col.number'), value: (x) => Number(x.number) || 0, render: (x) => h('a', { class: 'mono', href: href('/incidents/' + x.id) }, incLabel(x)) },
        { key: 'severity', label: t('inc.col.severity'), value: (x) => Number(x.severity) || 3, defaultDir: 'asc', render: (x) => severityBadge(x.severity) },
        { key: 'title', label: t('col.title'), tdCls: 'trunc', render: (x) => h('a', { href: href('/incidents/' + x.id), title: x.title || '' }, x.title || '-') },
        { key: 'status', label: t('col.status'), value: (x) => rank[x.status] ?? 0, defaultDir: 'asc', render: (x) => statusBadge(x.status) },
        { key: 'duration', label: t('col.duration'), align: 'r', value: (x) => (toMs(x.resolved_at) || Date.now()) - (toMs(x.detected_at) || toMs(x.created_at) || Date.now()), render: durationNode },
        { key: 'commander', label: t('inc.col.commander'), value: (x) => memberName(members, x.commander_id) || '', render: (x) => (x.commander_id ? (x.commander_name || memberName(members, x.commander_id)) : h('span', { class: 'muted' }, '-')) },
        { key: 'impact', label: t('inc.col.impact'), sort: false, tdCls: 'trunc', render: (x) => (x.customer_impact ? h('span', { class: 'small', title: x.customer_impact }, x.customer_impact) : h('span', { class: 'muted' }, '-')) },
        { key: 'detected_at', label: t('inc.col.declared'), align: 'r', value: (x) => toMs(x.detected_at) || toMs(x.created_at), render: (x) => h('span', { title: fmtDateTime(x.detected_at || x.created_at) }, fmtRel(x.detected_at || x.created_at)) },
      ],
      rows: list, sortKey: 'status', sortDir: 'asc', emptyText: t('inc.no_match'),
      onRowClick: (x) => ctx.navigate('/incidents/' + x.id),
      rowClass: (x) => (x.status === 'resolved' ? 'inc-resolved' : null),
    }));
  }
  try {
    rows = asList(await get('/incidents', null, { signal }), 'incidents');
  } catch (e) {
    if (isAbort(e)) return;
    clear(body); body.appendChild(listErrorState(e, () => ctx.refresh())); return;
  }
  drawTabs();
  draw();
  membersP.then(() => { if (body.isConnected) draw(); }).catch(() => {});
}

// ── Détail ────────────────────────────────────────────────────────────────
const KIND_ICON = { note: 'edit', status: 'flag', severity: 'problem', alert: 'bell', link: 'link', public_update: 'globe', system: 'info', ai: 'spark' };

function linkHref(href, l) {
  const ref = String(l.ref || '');
  const id = ref.split('|')[0];
  switch (l.kind) {
    case 'monitor': return href('/monitors/' + encodeURIComponent(id));
    case 'host': return href('/infrastructure/host/' + encodeURIComponent(id));
    case 'slo': return href('/slos/' + encodeURIComponent(id));
    case 'synthetic': return href('/synthetics/' + encodeURIComponent(id));
    case 'service': return href('/service/' + encodeURIComponent(id));
    case 'issue': return href('/errors/' + encodeURIComponent(id));
    default: return null;
  }
}

const PM_SKELETON = () => [
  '## ' + t('inc.pm.h.summary'), '', '', '## ' + t('inc.pm.h.impact'), '', '', '## ' + t('inc.pm.h.timeline'), '', '- ', '',
  '## ' + t('inc.pm.h.root'), '', '', '## ' + t('inc.pm.h.worked'), '', '- ', '', '## ' + t('inc.pm.h.actions'), '',
  '| ' + t('inc.pm.col.action') + ' | ' + t('inc.pm.col.owner') + ' | ' + t('inc.pm.col.due') + ' |', '|---|---|---|', '|  |  |  |', '',
].join('\n');

async function renderDetail(root, ctx) {
  const { args, signal, href } = ctx;
  const id = args.id;
  ctx.setCrumbs([{ label: t('nav.incidents'), href: href('/incidents') }, { label: '#' + id }]);
  const writer = canWrite(ctx.me);
  root.appendChild(loading());
  let inc;
  let members = [];
  try {
    const [r, mem] = await Promise.all([get('/incidents/' + encodeURIComponent(id), null, { signal }), loadMembers(signal)]);
    inc = r && r.incident ? Object.assign({}, r.incident, { timeline: r.timeline || r.incident.timeline, links: r.links || r.incident.links }) : r;
    members = mem;
  } catch (e) {
    if (isAbort(e)) return;
    clear(root); root.appendChild(errorState(e, () => ctx.refresh())); return;
  }
  clear(root);
  root.setAttribute('data-no-autorefresh', '1');
  let tab = ['timeline', 'graphs', 'postmortem'].includes(ctx.params.get('tab')) ? ctx.params.get('tab') : 'timeline';

  const headEl = h('div');
  const tabBar = h('div');
  const tabBody = h('div', { class: 'panel-body' });
  const sideEl = h('div', { class: 'split-v' });
  root.appendChild(headEl);
  root.appendChild(h('div', { class: 'split inc-layout' },
    h('section', { class: 'panel' }, tabBar, tabBody),
    sideEl));

  async function reload() {
    try {
      const r = await get('/incidents/' + encodeURIComponent(id), null, { signal });
      inc = r && r.incident ? Object.assign({}, r.incident, { timeline: r.timeline || r.incident.timeline, links: r.links || r.incident.links }) : r;
      drawHead();
      drawSide();
      drawTabs();
      if (tab === 'timeline') drawTimelineList();
    } catch (e) { if (!isAbort(e)) toast(errorMessage(e), 'error'); }
  }

  async function update(patch, okMsg) {
    try {
      const r = await put('/incidents/' + encodeURIComponent(id), patch);
      if (okMsg) toast(okMsg, 'ok');
      if (r && typeof r === 'object' && (r.id !== undefined || r.incident)) {
        const n = r.incident ? r.incident : r;
        inc = Object.assign({}, inc, n, { timeline: r.timeline || n.timeline || inc.timeline, links: r.links || n.links || inc.links });
      }
      await reload();
      return true;
    } catch (e) { toast(errorMessage(e), 'error'); return false; }
  }

  // En-tête : numéro, titre, statut, sévérité, commandant, durée.
  function drawHead() {
    clear(headEl);
    ctx.setCrumbs([{ label: t('nav.incidents'), href: href('/incidents') }, { label: incLabel(inc) }]);
    const titleEl = h('h1', { class: 'inc-title' }, inc.title || '-');
    const editTitle = () => {
      const inp = h('input', { class: 'input', value: inc.title || '', maxlength: 200, style: { minWidth: '320px', fontSize: 'var(--fs-lg)' } });
      let finished = false;
      const done = async (save) => {
        if (finished) return;
        finished = true;
        if (save && inp.value.trim() && inp.value.trim() !== inc.title) await update({ title: inp.value.trim() }, t('inc.saved'));
        else drawHead();
      };
      inp.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); done(true); } else if (e.key === 'Escape') { e.stopPropagation(); done(false); } });
      inp.addEventListener('blur', () => done(true));
      titleEl.replaceWith(inp);
      inp.focus();
      inp.select();
    };
    const statusPick = h('div', { class: 'inc-status-pick' }, writer ? pills(STATUSES.map((k) => ({ key: k, label: t('inc.status.' + k) })), inc.status, (k) => changeStatus(k)) : statusBadge(inc.status));
    headEl.appendChild(h('div', { class: 'page-head inc-head' },
      h('span', { class: 'badge muted mono' }, incLabel(inc)),
      writer ? severitySelect(inc.severity, (v) => update({ severity: v }, t('inc.saved')), { class: 'input sm sev-select p' + (inc.severity || 3), 'aria-label': t('inc.f.severity'), style: { width: 'auto' } }) : severityBadge(inc.severity),
      titleEl,
      writer ? h('button', { class: 'btn ghost sm icon', type: 'button', title: t('inc.edit_title'), onclick: editTitle }, icon('edit', 13)) : null,
      h('span', { class: 'spacer' }),
      statusPick));
    headEl.appendChild(h('div', { class: 'row wrap mb-3 small text-2 inc-meta' },
      h('span', { class: 'row', style: { gap: '4px' } }, icon('clock', 13), t('inc.duration'), ' ', h('b', durationNode(inc))),
      h('span', t('inc.declared_at', { d: fmtDateTime(inc.detected_at || inc.created_at) })),
      inc.resolved_at ? h('span', t('inc.resolved_at', { d: fmtDateTime(inc.resolved_at) })) : null,
      h('span', { class: 'row', style: { gap: '6px' } }, t('inc.f.commander'), writer ? commanderSelect(members, inc.commander_id, (v) => update({ commander_id: v }, t('inc.saved')), { class: 'input sm', style: { width: 'auto' } })
        : h('b', memberName(members, inc.commander_id) || t('inc.no_commander')))));
  }

  async function changeStatus(k) {
    if (k === inc.status) return;
    if (k !== 'resolved') { await update({ status: k }, t('inc.status_changed', { s: t('inc.status.' + k) })); return; }
    const note = h('textarea', { class: 'input', rows: 3, placeholder: t('inc.resolve_note_ph') });
    const pub = h('input', { type: 'checkbox' });
    const btn = h('button', { class: 'btn primary', type: 'button' }, icon('check', 14), t('inc.resolve'));
    const m = modal({
      title: t('inc.resolve_title'),
      body: h('div', { class: 'col gap-3' }, h('p', { class: 'small text-2' }, t('inc.resolve_text')), field(t('inc.resolve_note'), note), h('label', { class: 'check' }, pub, t('inc.resolve_public'))),
      actions: [h('button', { class: 'btn', type: 'button', onclick: () => { m.close(); drawHead(); } }, t('action.cancel')), btn],
      onClose: () => drawHead(),
    });
    btn.addEventListener('click', async () => {
      btn.disabled = true;
      const text = note.value.trim();
      if (text) {
        try { await post('/incidents/' + encodeURIComponent(id) + '/timeline', { kind: pub.checked ? 'public_update' : 'note', body: text }); } catch (e) { toast(errorMessage(e), 'error'); }
      }
      m.close();
      await update({ status: 'resolved' }, t('inc.resolved_done'));
    });
  }

  // ── Chronologie ─────────────────────────────────────────────────────────
  let tlFilter = 'all';
  const tlList = h('div', { class: 'inc-timeline' });
  function entryBody(e) {
    const kind = e.kind || 'system';
    const d = e.data && typeof e.data === 'object' ? e.data : {};
    const parts = [];
    if (kind === 'status' && (d.from || d.to)) parts.push(h('div', { class: 'row', style: { gap: '6px' } }, d.from ? statusBadge(d.from) : null, icon('chevronRight', 12), statusBadge(d.to)));
    if (kind === 'severity' && (d.from || d.to)) parts.push(h('div', { class: 'row', style: { gap: '6px' } }, d.from ? severityBadge(d.from) : null, icon('chevronRight', 12), severityBadge(d.to)));
    if (kind === 'alert' && (d.to_state || d.state)) {
      parts.push(h('div', { class: 'row wrap', style: { gap: '6px' } }, d.from_state ? badgeState(d.from_state) : null, d.from_state ? icon('chevronRight', 12) : null, badgeState(d.to_state || d.state),
        d.monitor_id ? h('a', { href: href('/monitors/' + d.monitor_id) }, d.monitor_name || '#' + d.monitor_id) : null, d.group ? h('span', { class: 'mono xsmall muted' }, d.group) : null));
    }
    const text = String(e.body || '').trim();
    if (text) parts.push(['note', 'public_update', 'ai'].includes(kind) ? renderMarkdown(text) : h('div', { class: 'small', style: { whiteSpace: 'pre-wrap' } }, text));
    return parts;
  }
  function drawTimelineList() {
    clear(tlList);
    const entries = (Array.isArray(inc.timeline) ? inc.timeline : []).slice().sort((a, b) => (toMs(b.ts) || 0) - (toMs(a.ts) || 0) || (Number(b.id) || 0) - (Number(a.id) || 0));
    const shown = entries.filter((e) => tlFilter === 'all' || (tlFilter === 'notes' ? ['note', 'ai'].includes(e.kind) : tlFilter === 'alerts' ? e.kind === 'alert' : e.kind === 'public_update'));
    if (!shown.length) { tlList.appendChild(empty(null, t('inc.tl.none'))); return; }
    for (const e of shown) {
      const kind = KIND_ICON[e.kind] ? e.kind : 'system';
      const author = e.author_name || memberName(members, e.author_id);
      tlList.appendChild(h('div', { class: ['tl-entry', 'k-' + kind] },
        h('div', { class: 'tl-icon', title: t('inc.kind.' + kind) }, icon(KIND_ICON[kind], 13)),
        h('div', { class: 'tl-main' },
          h('div', { class: 'tl-head' }, h('b', t('inc.kind.' + kind)), author ? h('span', { class: 'text-2' }, author) : null, h('span', { class: 'spacer' }),
            h('span', { class: 'xsmall muted num', title: fmtDateTime(e.ts) }, fmtDateTime(e.ts))),
          h('div', { class: 'tl-body' }, entryBody(e)))));
    }
  }
  function drawTimeline() {
    clear(tabBody);
    if (writer) {
      let kind = 'note';
      const ta = h('textarea', { class: 'input', rows: 3, maxlength: 10000, placeholder: t('inc.tl.note_ph') });
      const hint = h('span', { class: 'xsmall muted' }, t('inc.tl.hint.note'));
      const kindPick = h('span');
      const drawKind = () => {
        clear(kindPick);
        kindPick.appendChild(pills([{ key: 'note', label: t('inc.kind.note') }, { key: 'public_update', label: t('inc.kind.public_update') }], kind, (k) => { kind = k; hint.textContent = t('inc.tl.hint.' + k); ta.placeholder = t(k === 'note' ? 'inc.tl.note_ph' : 'inc.tl.public_ph'); drawKind(); }));
      };
      drawKind();
      const send = h('button', { class: 'btn primary sm', type: 'button' }, t('inc.tl.add'));
      const submit = async () => {
        const body = ta.value.trim();
        if (!body) return;
        send.disabled = true;
        try {
          await post('/incidents/' + encodeURIComponent(id) + '/timeline', { kind, body });
          ta.value = '';
          toast(t(kind === 'note' ? 'inc.tl.added' : 'inc.tl.published'), 'ok');
          await reload();
        } catch (e) { toast(errorMessage(e), 'error'); }
        send.disabled = false;
      };
      send.addEventListener('click', submit);
      ta.addEventListener('keydown', (e) => { if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) { e.preventDefault(); submit(); } });
      tabBody.appendChild(h('div', { class: 'tl-composer' }, ta, h('div', { class: 'row wrap' }, kindPick, hint, h('span', { class: 'spacer' }), h('span', { class: 'xsmall muted' }, t('inc.tl.shortcut')), send)));
    }
    const filterSlot = h('div', { class: 'row mt-3 mb-2' });
    const drawFilter = () => {
      clear(filterSlot);
      filterSlot.appendChild(pills(['all', 'notes', 'alerts', 'public'].map((k) => ({ key: k, label: t('inc.tl.filter.' + k) })), tlFilter, (k) => { tlFilter = k; drawFilter(); drawTimelineList(); }));
    };
    drawFilter();
    tabBody.appendChild(filterSlot);
    tabBody.appendChild(tlList);
    drawTimelineList();
  }

  // ── Graphiques des moniteurs liés ───────────────────────────────────────
  function incRange() {
    const a = toMs(inc.detected_at) || toMs(inc.created_at) || Date.now() - 3600e3;
    const b = toMs(inc.resolved_at) || Date.now();
    const pad = Math.max(15 * 60e3, (b - a) * 0.15);
    return { from: a - pad, to: Math.min(Date.now(), b + pad) };
  }
  function markers() {
    const ev = [];
    const a = toMs(inc.detected_at) || toMs(inc.created_at);
    if (a) ev.push({ ts: a, title: t('inc.marker.start'), color: '--crit' });
    const b = toMs(inc.resolved_at);
    if (b) ev.push({ ts: b, title: t('inc.marker.end'), color: '--ok' });
    return ev;
  }
  function stateStrip(history, range, current) {
    const strip = h('div', { class: 'state-strip', role: 'img', 'aria-label': t('inc.graph.strip') });
    const sorted = history.slice().sort((x, y) => (toMs(x.ts) || 0) - (toMs(y.ts) || 0));
    let st = sorted.length ? String(sorted[0].from_state || 'OK').toUpperCase() : String(current || 'OK').toUpperCase();
    let at = range.from;
    const span = range.to - range.from;
    const seg = (a, b, s2) => { if (b > a) strip.appendChild(h('i', { class: 's-' + s2.toLowerCase(), style: { width: ((b - a) / span * 100) + '%' }, title: t('state.' + s2.toLowerCase()) + ' · ' + fmtDateTime(a) + ' → ' + fmtDateTime(b) })); };
    for (const e of sorted) {
      const ts = Math.max(range.from, Math.min(range.to, toMs(e.ts) || range.from));
      seg(at, ts, st);
      st = String(e.to_state || e.state || st).toUpperCase();
      at = ts;
    }
    seg(at, range.to, st);
    return strip;
  }
  async function monitorGraph(container, monId, group) {
    const range = incRange();
    try {
      const r = await get('/monitors/' + encodeURIComponent(monId), null, { signal });
      const mon = r && r.monitor ? r.monitor : r;
      const q = (mon && mon.query) || {};
      clear(container);
      container.appendChild(h('div', { class: 'row mb-2' }, badgeState(monitorState(mon)), h('a', { href: href('/monitors/' + monId) }, h('b', mon.name || '#' + monId)),
        group ? h('span', { class: 'mono xsmall muted' }, group) : null, h('span', { class: 'spacer' }), h('span', { class: 'xsmall muted' }, t('monitors.type.' + mon.type))));
      const body = h('div');
      container.appendChild(body);
      if (METRIC_LIKE.includes(mon.type) && q.q) {
        let qq = q.q;
        // Groupe lié (« host:web-01 ») : la courbe se limite à ce groupe quand la requête le permet.
        if (group && group !== '*' && /\{([^}]*)\}/.test(qq)) {
          const filt = group.split(',').map((x) => x.trim()).filter(Boolean).join(',');
          qq = qq.replace(/\{([^}]*)\}/, (m0, inner) => '{' + (inner.trim() === '*' || !inner.trim() ? filt : inner + ',' + filt) + '}');
        }
        body.appendChild(h('div', { class: 'skeleton', style: { height: '160px' } }));
        const resp = await queryMetrics([{ id: 'a', q: qq }], range, 300, signal);
        const { xs, lines } = linesFromMetrics(resp);
        const th = [];
        const tt = mon.thresholds || {};
        if (['metric', 'forecast'].includes(mon.type)) {
          if (isNum(tt.warning)) th.push({ value: tt.warning, color: 'warn' });
          if (isNum(tt.critical)) th.push({ value: tt.critical, color: 'alert' });
        }
        clear(body);
        body.appendChild(timeChart({ xs, lines, unit: (lines.find((l) => l.unit) || {}).unit || null, height: 160, xRange: [range.from, range.to], thresholds: th, events: markers() }));
      } else if (mon.type === 'log') {
        const resp = await get('/logs/histogram', { q: q.q || '', from: range.from, to: range.to, buckets: 60 }, { signal });
        clear(body);
        body.appendChild(histogramChart(resp, range, { height: 130 }));
      } else {
        const hist = asList(await get('/monitors/' + encodeURIComponent(monId) + '/history', { from: range.from, to: range.to }, { signal }), 'history', 'events')
          .filter((e) => !group || group === '*' || String(e.group || e.group_key || '*') === group);
        clear(body);
        body.appendChild(stateStrip(hist, range, monitorState(mon)));
        body.appendChild(h('div', { class: 'row xsmall muted mt-2' }, h('span', fmtDateTime(range.from)), h('span', { class: 'spacer' }), h('span', fmtDateTime(range.to))));
      }
    } catch (e) {
      if (isAbort(e)) return;
      clear(container);
      container.appendChild(h('div', { class: 'small' }, h('a', { href: href('/monitors/' + monId) }, '#' + monId), ' · ', h('span', { style: { color: 'var(--crit)' } }, errorMessage(e))));
    }
  }
  function drawGraphs() {
    clear(tabBody);
    const links = (Array.isArray(inc.links) ? inc.links : []).filter((l) => l.kind === 'monitor');
    const range = incRange();
    tabBody.appendChild(h('div', { class: 'small text-2 mb-3' }, t('inc.graph.range', { a: fmtDateTime(range.from), b: fmtDateTime(range.to) }),
      h('span', { class: 'inc-legend' }, h('i', { style: { background: cssVar('--crit') } }), t('inc.marker.start')),
      inc.resolved_at ? h('span', { class: 'inc-legend' }, h('i', { style: { background: cssVar('--ok') } }), t('inc.marker.end')) : null));
    if (!links.length) { tabBody.appendChild(empty(t('inc.graph.none_title'), t('inc.graph.none_text'))); return; }
    const grid = h('div', { class: 'col gap-4' });
    tabBody.appendChild(grid);
    for (const l of links.slice(0, 12)) {
      const [monId, group] = String(l.ref).split('|');
      const box = h('div', { class: 'inc-graph' }, h('div', { class: 'skeleton', style: { height: '190px' } }));
      grid.appendChild(box);
      monitorGraph(box, monId, group || null);
    }
    if (links.length > 12) tabBody.appendChild(h('div', { class: 'xsmall muted mt-2' }, t('ack.more', { n: links.length - 12 })));
  }

  // ── Post-mortem ─────────────────────────────────────────────────────────
  let pmText = inc.postmortem || '';
  let pmSaved = inc.postmortem || '';
  let pmMode = 'split';
  function drawPostmortem() {
    clear(tabBody);
    const ta = h('textarea', { class: 'input mono pm-editor', rows: 22, spellcheck: 'true', placeholder: t('inc.pm.placeholder') });
    ta.value = pmText;
    const preview = h('div', { class: 'pm-preview' });
    const status = h('span', { class: 'xsmall muted' });
    const saveBtn = h('button', { class: 'btn primary sm', type: 'button', disabled: true }, t('action.save'));
    const drawPreview = () => {
      clear(preview);
      preview.appendChild(pmText.trim() ? renderMarkdown(pmText) : h('div', { class: 'muted small' }, t('inc.pm.empty_preview')));
      saveBtn.disabled = !writer || pmText === pmSaved;
      status.textContent = pmText === pmSaved ? (pmSaved ? t('inc.pm.saved_state') : '') : t('inc.pm.unsaved');
    };
    ta.addEventListener('input', () => { pmText = ta.value; drawPreview(); });
    saveBtn.addEventListener('click', async () => {
      saveBtn.disabled = true;
      const ok = await update({ postmortem: pmText }, t('inc.pm.saved'));
      if (ok) pmSaved = pmText;
      drawPreview();
    });
    const aiBtn = writer && explainEnabled(ctx.me) ? h('button', { class: 'btn sm explain-btn', type: 'button' }, icon('spark', 13), t('inc.pm.ai')) : null;
    const aiState = h('div');
    if (aiBtn) aiBtn.addEventListener('click', async () => {
      if (pmText.trim() && !(await confirmDialog({ title: t('inc.pm.ai_replace_title'), text: t('inc.pm.ai_replace_text'), confirmLabel: t('inc.pm.ai') }))) return;
      aiBtn.disabled = true;
      const started = Date.now();
      const elapsed = h('span', { class: 'num' }, '0 s');
      clear(aiState);
      aiState.appendChild(h('div', { class: 'explain-wait mb-3', role: 'status', 'aria-live': 'polite' }, h('span', { class: 'spinner' }),
        h('div', h('b', t('inc.pm.ai_running')), h('div', { class: 'small text-2' }, t('inc.pm.ai_hint')), h('div', { class: 'xsmall muted' }, elapsed))));
      const timer = setInterval(() => { if (!aiState.isConnected) { clearInterval(timer); return; } elapsed.textContent = Math.round((Date.now() - started) / 1000) + ' s'; }, 1000);
      const ctrl = new AbortController();
      const deadline = setTimeout(() => ctrl.abort(), AI_TIMEOUT_MS);
      signal.addEventListener('abort', () => ctrl.abort(), { once: true });
      try {
        const r = await post('/incidents/' + encodeURIComponent(id) + '/postmortem', {}, { signal: ctrl.signal });
        const text = (r && (r.postmortem || (r.incident && r.incident.postmortem) || r.markdown || r.text)) || '';
        clearInterval(timer); clearTimeout(deadline);
        if (!tabBody.isConnected) return;
        clear(aiState);
        if (text) {
          pmText = text; pmSaved = text; ta.value = text;
          aiState.appendChild(h('div', { class: 'callout mb-3' }, icon('spark', 16), h('span', { class: 'small' }, t('inc.pm.ai_done', { d: fmtDuration(Date.now() - started) }) + (r && r.model ? ' · ' + r.model : ''), ' ', t('explain.ai_note'))));
          drawPreview();
          reload();
        } else aiState.appendChild(h('div', { class: 'callout warn mb-3' }, icon('warn', 16), h('span', { class: 'small' }, t('explain.empty'))));
      } catch (e) {
        clearInterval(timer); clearTimeout(deadline);
        if (!tabBody.isConnected) return;
        clear(aiState);
        const msg = e && e.status === 503 ? t('inc.pm.err503') : isAbort(e) ? t('inc.pm.err_timeout') : e && e.status === 429 ? t('explain.err.429') : errorMessage(e);
        aiState.appendChild(h('div', { class: 'callout crit mb-3' }, icon('warn', 16), h('span', { class: 'small' }, msg)));
      }
      aiBtn.disabled = false;
    });
    const modePick = h('span');
    const editorWrap = h('div', { class: ['pm-wrap', 'mode-' + pmMode] }, h('div', { class: 'pm-edit' }, ta), preview);
    const drawMode = () => {
      clear(modePick);
      modePick.appendChild(pills(['edit', 'split', 'preview'].map((k) => ({ key: k, label: t('inc.pm.mode.' + k) })), pmMode, (k) => { pmMode = k; editorWrap.className = 'pm-wrap mode-' + k; drawMode(); }));
    };
    drawMode();
    tabBody.appendChild(h('div', { class: 'row wrap mb-3' }, aiBtn,
      writer ? h('button', { class: 'btn sm', type: 'button', title: t('inc.pm.skeleton_title'), onclick: () => { if (!ta.value.trim()) { ta.value = PM_SKELETON(); pmText = ta.value; drawPreview(); } else toast(t('inc.pm.skeleton_not_empty'), 'info'); } }, icon('list', 13), t('inc.pm.skeleton')) : null,
      h('span', { class: 'spacer' }), modePick, status, saveBtn));
    if (inc.status !== 'resolved') tabBody.appendChild(h('div', { class: 'callout mb-3' }, icon('info', 16), h('span', { class: 'small' }, t('inc.pm.not_resolved'))));
    tabBody.appendChild(aiState);
    if (!writer) ta.readOnly = true;
    tabBody.appendChild(editorWrap);
    drawPreview();
  }

  // ── Colonne latérale : détails, liens, pages de statut ──────────────────
  function drawSide() {
    clear(sideEl);
    const fields = inc.fields && typeof inc.fields === 'object' ? inc.fields : {};
    const impact = h('textarea', { class: 'input', rows: 3, maxlength: 4000, readOnly: !writer });
    impact.value = inc.customer_impact || '';
    const summary = h('textarea', { class: 'input', rows: 3, maxlength: 4000, readOnly: !writer });
    summary.value = inc.summary || '';
    const listIn = (k) => { const inp = h('input', { class: 'input mono', value: Array.isArray(fields[k]) ? fields[k].join(', ') : (fields[k] || ''), readOnly: !writer, placeholder: t('inc.f.' + k + '_ph') }); return inp; };
    const svc = listIn('services');
    const hosts = listIn('hosts');
    const teams = listIn('teams');
    const save = h('button', { class: 'btn sm primary', type: 'button' }, t('action.save'));
    save.addEventListener('click', async () => {
      save.disabled = true;
      const split = (v) => v.split(',').map((x) => x.trim()).filter(Boolean);
      await update({ customer_impact: impact.value.trim(), summary: summary.value.trim(), fields: Object.assign({}, fields, { services: split(svc.value), hosts: split(hosts.value), teams: split(teams.value) }) }, t('inc.saved'));
      save.disabled = false;
    });
    sideEl.appendChild(panel(t('inc.details'), h('div', { class: 'col gap-3' },
      field(t('inc.f.impact'), impact), field(t('inc.f.summary'), summary),
      field(t('inc.f.services'), svc), field(t('inc.f.hosts'), hosts), field(t('inc.f.teams'), teams),
      kv([
        [t('inc.f.created_by'), memberName(members, inc.created_by)],
        [t('inc.f.updated'), inc.updated_at ? fmtDateTime(inc.updated_at) : null],
      ]),
      writer ? h('div', { class: 'row' }, h('span', { class: 'spacer' }), save) : null)));

    // Liens
    const links = Array.isArray(inc.links) ? inc.links : [];
    const linksEl = h('ul', { class: 'list-plain inc-links' });
    if (!links.length) linksEl.appendChild(h('li', { class: 'muted small' }, t('inc.links.none')));
    for (const l of links) {
      const hr = linkHref(href, l);
      const label = l.label || l.name || String(l.ref);
      linksEl.appendChild(h('li', h('span', { class: 'badge muted' }, t('inc.link.' + (LINK_KINDS.includes(l.kind) ? l.kind : 'other'))),
        hr ? h('a', { class: 'grow ellipsis', href: hr, title: label }, label) : h('span', { class: 'grow ellipsis' }, label),
        writer ? h('button', { class: 'btn ghost sm icon danger', type: 'button', title: t('action.remove'), onclick: async () => {
          try { await del('/incidents/' + encodeURIComponent(id) + '/links', { query: { kind: l.kind, ref: l.ref } }); toast(t('inc.links.removed'), 'ok'); await reload(); if (tab === 'graphs') drawGraphs(); } catch (e) { toast(errorMessage(e), 'error'); }
        } }, icon('x', 12)) : null));
    }
    sideEl.appendChild(panel(t('inc.links.title'), linksEl, { flush: true, actions: writer ? h('button', { class: 'btn sm', type: 'button', onclick: () => linkDialog() }, icon('plus', 13), t('inc.links.add')) : null }));

    // Pages de statut qui citent l'incident (administrateurs)
    if (isAdmin(ctx.me)) {
      const spEl = h('div', { class: 'col', style: { gap: '4px' } }, h('span', { class: 'spinner' }));
      sideEl.appendChild(panel(t('inc.sp.title'), spEl, { sub: t('inc.sp.sub') }));
      get('/status-pages', null, { signal }).then((r) => {
        const pages = asList(r, 'status_pages', 'pages');
        clear(spEl);
        if (!pages.length) { spEl.appendChild(h('div', { class: 'small muted' }, t('inc.sp.none'), ' ', h('a', { href: href('/settings/status-pages') }, t('nav.settings.status-pages')))); return; }
        const cur = new Set((inc.status_page_ids || []).map(String));
        for (const p of pages) {
          const cb = h('input', { type: 'checkbox', checked: cur.has(String(p.id)) });
          cb.addEventListener('change', async () => {
            if (cb.checked) cur.add(String(p.id)); else cur.delete(String(p.id));
            await update({ status_page_ids: Array.from(cur).map((x) => (/^\d+$/.test(x) ? Number(x) : x)) }, t('inc.saved'));
          });
          spEl.appendChild(h('label', { class: 'check' }, cb, p.title || p.slug, p.public ? h('a', { class: 'xsmall', href: '/status/?p=' + encodeURIComponent(p.slug), target: '_blank', rel: 'noopener' }, icon('external', 11)) : h('span', { class: 'badge muted' }, t('sp.private'))));
        }
        spEl.appendChild(h('div', { class: 'xsmall muted mt-2' }, t('inc.sp.hint')));
      }).catch((e) => { clear(spEl); spEl.appendChild(h('div', { class: 'small muted' }, errorMessage(e))); });
    }
  }

  function linkDialog() {
    let kind = 'monitor';
    const slot = h('div', { class: 'col gap-3' });
    const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
    let ref = '';
    const btn = h('button', { class: 'btn primary', type: 'button' }, t('inc.links.add'));
    const loaders = {
      monitor: () => get('/monitors', null, { signal }).then((r) => asList(r, 'monitors').map((x) => ({ value: String(x.id), label: x.name + ' (#' + x.id + ')' }))),
      host: () => get('/hosts', null, { signal }).then((r) => asList(r, 'hosts').map((x) => ({ value: String(x.id), label: x.hostname }))),
      slo: () => get('/slos', null, { signal }).then((r) => asList(r, 'slos').map((x) => ({ value: String(x.id), label: x.name }))),
      synthetic: () => get('/synthetics', null, { signal }).then((r) => asList(r, 'synthetics', 'tests').map((x) => ({ value: String(x.id), label: x.name || x.target }))),
    };
    function drawKind() {
      clear(slot);
      ref = '';
      slot.appendChild(field(t('inc.links.kind'), select(LINK_KINDS.map((k) => ({ value: k, label: t('inc.link.' + k) })), kind, (v) => { kind = v; drawKind(); })));
      if (loaders[kind]) {
        const pickSlot = h('div', h('span', { class: 'spinner' }));
        slot.appendChild(field(t('inc.links.object'), pickSlot));
        let groupIn = null;
        if (kind === 'monitor') {
          groupIn = h('input', { class: 'input mono', placeholder: 'host:web-01' });
          groupIn.addEventListener('input', () => { ref = refBase + (groupIn.value.trim() ? '|' + groupIn.value.trim() : ''); });
          slot.appendChild(field(t('inc.links.group'), groupIn, t('inc.links.group_hint')));
        }
        let refBase = '';
        loaders[kind]().then((opts) => {
          clear(pickSlot);
          if (!opts.length) { pickSlot.appendChild(h('span', { class: 'small muted' }, t('empty.no_rows'))); return; }
          opts.sort((a, b) => a.label.localeCompare(b.label));
          refBase = opts[0].value;
          ref = refBase;
          pickSlot.appendChild(select(opts, refBase, (v) => { refBase = v; ref = v + (groupIn && groupIn.value.trim() ? '|' + groupIn.value.trim() : ''); }));
        }).catch((e) => { clear(pickSlot); pickSlot.appendChild(h('span', { class: 'small', style: { color: 'var(--crit)' } }, errorMessage(e))); });
      } else {
        const inp = h('input', { class: 'input mono', placeholder: kind === 'service' ? 'api' : '1234' });
        inp.addEventListener('input', () => { ref = inp.value.trim(); });
        slot.appendChild(field(t('inc.links.ref.' + kind), inp));
      }
    }
    drawKind();
    const m = modal({ title: t('inc.links.add_title'), body: h('div', { class: 'col gap-3' }, slot, err), actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), btn] });
    btn.addEventListener('click', async () => {
      if (!ref) { err.textContent = t('form.required', { field: t('inc.links.object') }); err.hidden = false; return; }
      btn.disabled = true;
      try {
        await post('/incidents/' + encodeURIComponent(id) + '/links', { kind, ref });
        m.close(); toast(t('inc.links.added'), 'ok');
        await reload();
        if (tab === 'graphs') drawGraphs();
      } catch (e) { btn.disabled = false; err.textContent = errorMessage(e); err.hidden = false; }
    });
  }

  function drawTabs() {
    clear(tabBar);
    const tl = Array.isArray(inc.timeline) ? inc.timeline.length : null;
    const ml = (Array.isArray(inc.links) ? inc.links : []).filter((l) => l.kind === 'monitor').length;
    tabBar.appendChild(tabs([
      { key: 'timeline', label: t('inc.tab_timeline'), count: tl },
      { key: 'graphs', label: t('inc.tab_graphs'), count: ml || null },
      { key: 'postmortem', label: t('inc.tab_postmortem') },
    ], tab, (k) => {
      if (tab === 'postmortem' && pmText !== pmSaved) toast(t('inc.pm.unsaved_kept'), 'info');
      tab = k; ctx.setParams({ tab: k === 'timeline' ? null : k }, { silent: true }); drawTabs(); drawTab();
    }));
  }
  function drawTab() {
    if (tab === 'graphs') drawGraphs();
    else if (tab === 'postmortem') drawPostmortem();
    else drawTimeline();
  }

  drawHead();
  drawTabs();
  drawTab();
  drawSide();

  // Tant que l'incident n'est pas résolu, la chronologie reçoit les changements d'état des moniteurs liés.
  const timer = setInterval(async () => {
    if (document.visibilityState !== 'visible' || inc.status === 'resolved' || document.querySelector('.modal-back')) return;
    if (tab !== 'timeline') return;
    const active = document.activeElement;
    if (active && (active.tagName === 'TEXTAREA' || active.tagName === 'INPUT' || active.tagName === 'SELECT')) return;
    try {
      const r = await get('/incidents/' + encodeURIComponent(id), null, { signal });
      const n = r && r.incident ? Object.assign({}, r.incident, { timeline: r.timeline || r.incident.timeline, links: r.links || r.incident.links }) : r;
      const before = (inc.timeline || []).length;
      inc = Object.assign({}, inc, n);
      if ((inc.timeline || []).length !== before) { drawTimelineList(); drawTabs(); }
    } catch (e) { /* nouvelle tentative au prochain tour */ }
  }, 30000);
  ctx.onCleanup(() => clearInterval(timer));
}
