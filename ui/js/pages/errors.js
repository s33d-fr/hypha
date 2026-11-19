// Suivi des erreurs (§ 25) : problèmes regroupés par empreinte (traces, journaux, RUM), nouveaux et régressés
// en avant, mini-histogramme, détail avec pile colorée, occurrences, liens traces et journaux.
import { h, clear, icon, cssVar } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, put, asList, pool, isAbort } from '../api.js';
import { fmtInt, fmtCompact, fmtRel, fmtDateTime, toMs, isNum } from '../format.js';
import { empty, panel, searchInput, pills, select, toast, errorMessage, copyButton, kv, errorState } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { timeChart, alignPoints } from '../components/chart.js';
import { sparkbars } from '../components/spark.js';
import { explainButton, idValue } from '../components/explain.js';
import { qv } from './common.js';
import { load, tile, canWrite, toPoints, routeErrorState } from './kit.js';
import { viewsMenu } from '../components/views.js';

const STATUSES = ['open', 'ignored', 'resolved'];

function inRange(ms, range) { return isNum(ms) && ms >= range.from && ms <= range.to; }
export function isNew(e, range) { return inRange(toMs(e.first_seen), range); }
export function isRegressed(e, range) { return e.status === 'open' && isNum(toMs(e.regressed_at)) && (inRange(toMs(e.regressed_at), range) || toMs(e.regressed_at) > toMs(e.first_seen)); }

function originBadge(o) {
  const key = ['trace', 'log', 'rum'].includes(o) ? o : 'other';
  return h('span', { class: 'badge muted', title: t('errors.origin.' + key) }, icon(key === 'trace' ? 'trace' : key === 'rum' ? 'rum' : 'logs', 11), t('errors.origin_short.' + key));
}

function statusBadge(st) {
  const cls = st === 'open' ? 'alert' : st === 'resolved' ? 'ok' : 'muted';
  return h('span', { class: ['badge', cls] }, t('errors.status.' + (STATUSES.includes(st) ? st : 'open')));
}

export async function render(root, ctx) {
  if (ctx.route.view === 'detail') return renderDetail(root, ctx);
  return renderList(root, ctx);
}

async function renderList(root, ctx) {
  const { params, range, signal, href } = ctx;
  ctx.setCrumbs([{ label: t('nav.errors') }]);
  let status = STATUSES.concat(['all']).includes(params.get('status')) ? params.get('status') : 'open';
  const service = params.get('service') || '';
  let sort = ['last_seen', 'count', 'first_seen'].includes(params.get('sort')) ? params.get('sort') : 'last_seen';
  const q = params.get('q') || '';
  let only = params.get('only') || 'all';

  const search = searchInput({ placeholder: t('errors.search_ph'), value: q, onEnter: (v) => ctx.setParams({ q: v.trim() || null }) });
  search.style.flex = '1 1 280px';
  const svcSlot = h('span');
  const views = viewsMenu({
    page: 'errors', me: ctx.me,
    getState: () => ({ q: search.input.value.trim() || q, status, service, sort, only }),
    apply: (st) => ctx.setParams({ q: st.q || null, status: st.status || null, service: st.service || null, sort: st.sort || null, only: st.only && st.only !== 'all' ? st.only : null }),
  });
  root.appendChild(h('div', { class: 'querybar' }, search, svcSlot,
    pills(STATUSES.concat(['all']).map((k) => ({ key: k, label: t('errors.status.' + k) })), status, (k) => ctx.setParams({ status: k === 'open' ? null : k })),
    select(['last_seen', 'count', 'first_seen'].map((k) => ({ value: k, label: t('errors.sort.' + k) })), sort, (v) => ctx.setParams({ sort: v === 'last_seen' ? null : v }), { style: { width: 'auto' } }),
    views));
  const tilesEl = h('div', { class: 'tiles mb-3' });
  const onlyEl = h('div', { class: 'row mb-3' });
  const listEl = h('div');
  const countEl = h('span', { class: 'sub' });
  root.appendChild(tilesEl);
  root.appendChild(onlyEl);
  root.appendChild(panel(t('errors.issues'), listEl, { flush: true, actions: countEl }));

  let rows = [];
  const hist = new Map();

  function drawOnly() {
    clear(onlyEl);
    const nNew = rows.filter((e) => isNew(e, range)).length;
    const nReg = rows.filter((e) => isRegressed(e, range)).length;
    onlyEl.appendChild(pills([
      { key: 'all', label: t('errors.only.all') + ' (' + fmtInt(rows.length) + ')' },
      { key: 'new', label: t('errors.only.new') + ' (' + fmtInt(nNew) + ')' },
      { key: 'regressed', label: t('errors.only.regressed') + ' (' + fmtInt(nReg) + ')' },
    ], only, (k) => { only = k; ctx.setParams({ only: k === 'all' ? null : k }, { silent: true }); drawOnly(); draw(); }));
  }

  function draw() {
    let list = rows;
    if (only === 'new') list = rows.filter((e) => isNew(e, range));
    if (only === 'regressed') list = rows.filter((e) => isRegressed(e, range));
    // Nouveaux et régressés d'abord, puis l'ordre demandé au serveur.
    const rank = (e) => (isRegressed(e, range) ? 0 : isNew(e, range) ? 1 : 2);
    list = list.map((e, i) => ({ e, i })).sort((a, b) => rank(a.e) - rank(b.e) || a.i - b.i).map((x) => x.e);
    countEl.textContent = tn('errors.count', list.length);
    clear(listEl);
    if (!rows.length) { listEl.appendChild(empty(t('errors.none_title'), status === 'open' ? t('errors.none_open') : t('errors.none_text'))); return; }
    listEl.appendChild(dataTable({
      columns: [
        { key: 'flag', label: '', width: '86px', sort: false, render: (e) => (isRegressed(e, range) ? h('span', { class: 'badge alert', title: fmtDateTime(e.regressed_at) }, t('errors.regressed')) : isNew(e, range) ? h('span', { class: 'badge warn' }, t('errors.new')) : null) },
        { key: 'error_type', label: t('errors.col.issue'), sort: false, tdCls: 'wrap', render: (e) => h('div', { class: 'err-cell' },
          h('a', { href: href('/errors/' + encodeURIComponent(e.id)) }, h('b', e.error_type || t('errors.untyped'))),
          h('div', { class: 'small text-2 ellipsis', title: e.message || '' }, e.message || '-'),
          h('div', { class: 'row xsmall muted', style: { gap: '6px' } }, originBadge(e.origin), e.service ? h('span', e.service) : null, Array.isArray(e.hosts) && e.hosts.length ? h('span', tn('errors.on_hosts', e.hosts.length)) : null)) },
        { key: 'hist', label: t('errors.col.trend'), sort: false, render: (e) => {
          const pts = hist.get(String(e.id));
          if (pts === undefined) return h('span', { class: 'skeleton', style: { display: 'inline-block', width: '110px', height: '22px' } });
          return sparkbars(pts.map((p) => p[1]), { width: 110, height: 22, color: e.status === 'open' ? 'var(--crit)' : 'var(--nodata)' });
        } },
        { key: 'count_in_range', label: t('errors.col.in_range'), align: 'r', render: (e) => fmtCompact(e.count_in_range) },
        { key: 'count', label: t('errors.col.total'), align: 'r', render: (e) => fmtCompact(e.count) },
        { key: 'first_seen', label: t('errors.col.first'), align: 'r', value: (e) => toMs(e.first_seen), render: (e) => h('span', { title: fmtDateTime(e.first_seen) }, fmtRel(e.first_seen)) },
        { key: 'last_seen', label: t('errors.col.last'), align: 'r', value: (e) => toMs(e.last_seen), render: (e) => h('span', { title: fmtDateTime(e.last_seen) }, fmtRel(e.last_seen)) },
        { key: 'status', label: t('col.status'), render: (e) => statusBadge(e.status) },
      ],
      rows: list, sortKey: null,
      rowClass: (e) => (isRegressed(e, range) ? 'row-regressed' : isNew(e, range) ? 'row-new' : null),
      onRowClick: (e) => ctx.navigate('/errors/' + encodeURIComponent(e.id)),
      emptyText: t('errors.no_match'),
    }));
  }

  function drawTiles() {
    clear(tilesEl);
    const occ = rows.reduce((a, e) => a + (isNum(e.count_in_range) ? e.count_in_range : 0), 0);
    tilesEl.appendChild(tile(t('errors.tile.issues'), fmtInt(rows.length), t('errors.status.' + status)));
    tilesEl.appendChild(tile(t('errors.tile.new'), fmtInt(rows.filter((e) => isNew(e, range)).length), t('errors.tile.in_range'), 'var(--warn)'));
    tilesEl.appendChild(tile(t('errors.tile.regressed'), fmtInt(rows.filter((e) => isRegressed(e, range)).length), t('errors.tile.regressed_sub'), 'var(--crit)'));
    tilesEl.appendChild(tile(t('errors.tile.occurrences'), fmtCompact(occ), t('errors.tile.in_range')));
    const svcs = new Set(rows.map((e) => e.service).filter(Boolean));
    tilesEl.appendChild(tile(t('errors.tile.services'), fmtInt(svcs.size)));
  }

  await load(listEl, async () => asList(await get('/errors', { status: status === 'all' ? undefined : status, service: service || undefined, q: q || undefined, from: range.from, to: range.to, sort }, { signal }), 'errors', 'issues'), (list) => {
    rows = list;
    const svcs = Array.from(new Set(list.map((e) => e.service).filter(Boolean).concat(service ? [service] : []))).sort();
    clear(svcSlot);
    svcSlot.appendChild(select([{ value: '', label: t('apm.all_services') }].concat(svcs.map((x) => ({ value: x, label: x }))), service, (v) => ctx.setParams({ service: v || null }), { style: { width: 'auto', minWidth: '160px' } }));
    for (const e of list) {
      const own = e.histogram || e.hits || e.hourly;
      if (Array.isArray(own)) hist.set(String(e.id), toPoints(own));
    }
    drawTiles();
    drawOnly();
    draw();
    return null;
  });

  // Mini-histogrammes absents de la liste : lus dans le détail des 25 premiers, 3 requêtes à la fois.
  const missing = rows.filter((e) => !hist.has(String(e.id))).slice(0, 25);
  if (missing.length) {
    let dirty = false;
    const timer = setInterval(() => { if (dirty && ctx.isCurrent()) { dirty = false; draw(); } }, 400);
    ctx.onCleanup(() => clearInterval(timer));
    await pool(missing, 3, async (e) => {
      try {
        const d = await get('/errors/' + encodeURIComponent(e.id), { from: range.from, to: range.to }, { signal });
        hist.set(String(e.id), toPoints(d && (d.histogram || d.hits || d.hourly)));
      } catch (err) { if (!isAbort(err)) hist.set(String(e.id), []); }
      dirty = true;
    });
    for (const e of rows) if (!hist.has(String(e.id))) hist.set(String(e.id), []);
    if (ctx.isCurrent()) draw();
  }
}

// ── Pile colorée ──────────────────────────────────────────────────────────
const LIB = /(site-packages|dist-packages|node_modules|\/usr\/lib\/|\/usr\/local\/lib\/|<frozen |java\.base|jdk\.|\bjava\.|\bjavax\.|\bsun\.|runtime\/|\/go\/pkg\/mod\/|\binternal\/)/;

function frameKind(line) {
  if (/^\s*(Traceback|Caused by|goroutine \d+|panic:|Exception in thread)/.test(line)) return 'head';
  if (/^\s*File ".*", line \d+/.test(line) || /^\s+at\s/.test(line) || /^\s*[\w./\-]+\.go:\d+/.test(line) || /\(\S+:\d+(:\d+)?\)\s*$/.test(line)) return LIB.test(line) ? 'lib' : 'app';
  if (/^\s*([\w.$]+(Error|Exception)|[A-Z]\w+Error)\b/.test(line)) return 'exc';
  return 'code';
}

function highlightFrame(line) {
  const out = [];
  const re = /("[^"]+"|\(?[\w./\\\-<>]+\.(?:py|js|ts|mjs|go|java|kt|rb|php|cs|rs|c|cc|cpp)\)?)(:\d+(?::\d+)?)?|(line \d+)|(\bin \w+)/g;
  let last = 0;
  let m;
  while ((m = re.exec(line))) {
    if (m.index > last) out.push(line.slice(last, m.index));
    if (m[1]) { out.push(h('span', { class: 'st-file' }, m[1])); if (m[2]) out.push(h('span', { class: 'st-line' }, m[2])); } else if (m[3]) out.push(h('span', { class: 'st-line' }, m[3])); else if (m[4]) out.push(h('span', { class: 'st-fn' }, m[4]));
    last = re.lastIndex;
  }
  if (last < line.length) out.push(line.slice(last));
  return out;
}

export function stackView(stack) {
  const text = String(stack || '').replace(/\r\n?/g, '\n');
  const box = h('div', { class: 'stack' });
  const lines = text.split('\n');
  let hideLib = true;
  const pre = h('pre', { class: 'stack-pre' });
  const drawPre = () => {
    clear(pre);
    let hidden = 0;
    lines.forEach((line, i) => {
      const kind = frameKind(line);
      if (hideLib && kind === 'lib') {
        hidden++;
        const next = lines[i + 1];
        if (next !== undefined && frameKind(next) === 'lib') return;
        pre.appendChild(h('div', { class: 'st-row st-collapsed' }, tn('errors.stack.lib_hidden', hidden)));
        hidden = 0;
        return;
      }
      pre.appendChild(h('div', { class: ['st-row', 'st-' + kind] }, kind === 'app' || kind === 'lib' ? highlightFrame(line) : line || ' '));
    });
  };
  drawPre();
  const libCount = lines.filter((l) => frameKind(l) === 'lib').length;
  const toggle = libCount ? h('button', { class: 'btn ghost sm', type: 'button' }, t('errors.stack.show_lib', { n: libCount })) : null;
  if (toggle) toggle.addEventListener('click', () => { hideLib = !hideLib; toggle.textContent = hideLib ? t('errors.stack.show_lib', { n: libCount }) : t('errors.stack.hide_lib'); drawPre(); });
  box.appendChild(h('div', { class: 'row mb-2' }, h('span', { class: 'legend-app small' }, t('errors.stack.app')), h('span', { class: 'legend-lib small' }, t('errors.stack.lib')), h('span', { class: 'spacer' }), toggle, copyButton(() => text)));
  box.appendChild(pre);
  return box;
}

// ── Détail d'un problème ──────────────────────────────────────────────────
async function renderDetail(root, ctx) {
  const { args, range, signal, href } = ctx;
  ctx.setCrumbs([{ label: t('nav.errors'), href: href('/errors') }, { label: '#' + args.id }]);
  let issue;
  try {
    issue = await get('/errors/' + encodeURIComponent(args.id), { from: range.from, to: range.to }, { signal });
    if (issue && issue.issue && typeof issue.issue === 'object') issue = Object.assign({}, issue, issue.issue);
  } catch (e) {
    if (isAbort(e)) return;
    root.appendChild(e && e.status === 404 && e.code ? errorState(e) : routeErrorState(e, () => ctx.refresh()));
    return;
  }
  issue = issue || {};
  ctx.setCrumbs([{ label: t('nav.errors'), href: href('/errors') }, { label: issue.error_type || '#' + args.id }]);
  const writable = canWrite(ctx.me);
  const svcQ = issue.service ? 'service:' + qv(issue.service) : '';

  async function setStatus(st) {
    try {
      await put('/errors/' + encodeURIComponent(issue.id !== undefined ? issue.id : args.id), { status: st });
      toast(t('errors.status_done.' + st), 'ok');
      ctx.refresh();
    } catch (e) { toast(errorMessage(e), 'error'); }
  }

  const actions = [];
  if (writable) {
    if (issue.status !== 'resolved') actions.push(h('button', { class: 'btn sm primary', type: 'button', onclick: () => setStatus('resolved') }, icon('check', 13), t('errors.resolve')));
    if (issue.status !== 'ignored') actions.push(h('button', { class: 'btn sm', type: 'button', onclick: () => setStatus('ignored') }, icon('mute', 13), t('errors.ignore')));
    if (issue.status && issue.status !== 'open') actions.push(h('button', { class: 'btn sm', type: 'button', onclick: () => setStatus('open') }, t('errors.reopen')));
  }
  root.appendChild(h('div', { class: 'page-head' },
    h('div', { style: { minWidth: '0' } },
      h('div', { class: 'row wrap', style: { gap: '8px' } }, h('h1', issue.error_type || t('errors.untyped')), statusBadge(issue.status), originBadge(issue.origin),
        isRegressed(issue, range) ? h('span', { class: 'badge alert' }, t('errors.regressed')) : isNew(issue, range) ? h('span', { class: 'badge warn' }, t('errors.new')) : null),
      h('div', { class: 'desc mono', style: { whiteSpace: 'pre-wrap', wordBreak: 'break-word', marginTop: '4px' } }, issue.message || '')),
    h('span', { class: 'spacer' }),
    explainButton(ctx.me, { kind: 'error', issue_id: idValue(issue.id !== undefined ? issue.id : args.id) }, { subject: issue.error_type || '' }),
    actions));

  const hosts = asList(issue.hosts);
  root.appendChild(h('div', { class: 'tiles mb-3' },
    tile(t('errors.col.in_range'), fmtCompact(issue.count_in_range)),
    tile(t('errors.col.total'), fmtCompact(issue.count)),
    tile(t('errors.col.first'), fmtRel(issue.first_seen), fmtDateTime(issue.first_seen)),
    tile(t('errors.col.last'), fmtRel(issue.last_seen), fmtDateTime(issue.last_seen)),
    issue.regressed_at ? tile(t('errors.regressed_at'), fmtRel(issue.regressed_at), fmtDateTime(issue.regressed_at), 'var(--crit)') : null,
    tile(t('col.hosts'), fmtInt(hosts.length), hosts.slice(0, 3).map((x) => (typeof x === 'object' ? x.hostname || x.name : x)).join(', '))));

  // Histogramme horaire
  const pts = toPoints(issue.histogram || issue.hits || issue.hourly);
  const { xs, ys } = alignPoints([pts]);
  root.appendChild(h('div', { class: 'mb-3' }, panel(t('errors.histogram'), timeChart({ xs, lines: [{ label: t('errors.occurrences'), values: ys[0], color: cssVar('--crit') }], unit: 'count', height: 140, display: 'bars', xRange: [range.from, range.to], onRange: ctx.onRange, legend: false, precision: 0 }), { sub: t('errors.per_hour') })));

  const links = h('div', { class: 'row wrap' },
    issue.service ? h('a', { class: 'btn sm', href: href('/apm/traces', { service: issue.service, status: 'error' }) }, icon('trace', 12), t('errors.see_traces')) : null,
    h('a', { class: 'btn sm', href: href('/logs', { q: [svcQ, 'status:error'].filter(Boolean).join(' ') }) }, icon('logs', 12), t('errors.see_logs')),
    issue.service ? h('a', { class: 'btn sm', href: href('/service/' + encodeURIComponent(issue.service)) }, icon('graph', 12), t('errors.see_service')) : null,
    issue.last_trace_id ? h('a', { class: 'btn sm primary', href: href('/apm/trace/' + encodeURIComponent(issue.last_trace_id)) }, icon('trace', 12), t('errors.last_trace')) : null);

  root.appendChild(h('div', { class: 'split mb-3' },
    panel(t('errors.stack'), issue.stack ? stackView(issue.stack) : h('div', { class: 'muted small' }, t('errors.no_stack'))),
    h('div', { class: 'split-v' },
      panel(t('errors.about'), h('div', { class: 'col gap-3' }, kv([
        [t('col.service'), issue.service || null],
        [t('errors.origin_label'), t('errors.origin.' + (['trace', 'log', 'rum'].includes(issue.origin) ? issue.origin : 'other'))],
        [t('errors.fingerprint'), issue.fingerprint ? h('span', { class: 'mono small' }, String(issue.fingerprint).slice(0, 16)) : null],
        [t('col.hosts'), hosts.length ? h('span', { class: 'small' }, hosts.slice(0, 12).map((x) => (typeof x === 'object' ? x.hostname || x.name : x)).join(', ')) : null],
        [t('errors.resolved_at'), issue.resolved_at ? fmtDateTime(issue.resolved_at) : undefined],
      ]), links)))));

  // Dernières occurrences
  const occ = asList(issue.occurrences || issue.recent || issue.samples);
  root.appendChild(panel(t('errors.recent'), dataTable({
    columns: [
      { key: 'ts', label: t('col.date'), value: (o) => toMs(o.ts), render: (o) => h('span', { class: 'num' }, fmtDateTime(o.ts)) },
      { key: 'host', label: t('col.host'), value: (o) => o.hostname || o.host || '', render: (o) => (o.host_id ? h('a', { href: href('/infrastructure/host/' + o.host_id) }, o.hostname || o.host || '#' + o.host_id) : (o.hostname || o.host || '-')) },
      { key: 'message', label: t('col.message'), tdCls: 'trunc', render: (o) => h('span', { class: 'mono small', title: o.message || '' }, o.message || issue.message || '-') },
      { key: 'link', label: '', sort: false, render: (o) => {
        const ms = toMs(o.ts);
        const around = ms ? { t: null, from: String(ms - 5 * 60e3), to: String(ms + 5 * 60e3) } : {};
        return h('span', { class: 'row', style: { gap: '4px', justifyContent: 'flex-end' } },
          o.trace_id ? h('a', { class: 'btn ghost sm', href: href('/apm/trace/' + encodeURIComponent(o.trace_id)) }, icon('trace', 12), t('errors.trace')) : null,
          o.log_id !== undefined && o.log_id !== null ? h('a', { class: 'btn ghost sm', href: href('/logs', Object.assign({ q: svcQ || null, focus: o.log_id }, around)) }, icon('logs', 12), t('errors.log')) : null,
          o.session_id ? h('span', { class: 'mono xsmall muted' }, String(o.session_id).slice(0, 8)) : null);
      } },
    ],
    rows: occ, sortKey: 'ts', sortDir: 'desc', emptyText: t('errors.no_occurrence'),
  }), { flush: true }));
  return null;
}

