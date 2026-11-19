// Catalogue de services, fiche service (Vue, Moniteurs, Erreurs, Dépendances), carte des services et
// carte du réseau (§ 26), sur le graphe à forces maison.
import { h, clear, icon, cssVar } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, post, put, del, asList, isAbort } from '../api.js';
import { fmtDuration, fmtPct, fmtCompact, fmtInt, fmtRel, fmtDateTime, isNum, toMs } from '../format.js';
import { empty, panel, searchInput, select, pills, tabs, modal, field, toast, confirmDialog, errorMessage, badgeState, tagList, kv } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { timeChart, alignPoints } from '../components/chart.js';
import { forceGraph, graphLegend } from '../components/graph.js';
import { errorPct } from './apm.js';
import { load, tile, canWrite, splitTag, matchesTagFilters, routeErrorState } from './kit.js';
import { isNew, isRegressed } from './errors.js';
import { STATES } from './monitors-shared.js';

// ── États ─────────────────────────────────────────────────────────────────
export function normState(st, fallback) {
  const s2 = String(st || '').toLowerCase();
  if (['ok', 'up', 'operational', 'healthy'].includes(s2)) return 'ok';
  if (['warn', 'warning', 'degraded'].includes(s2)) return 'warn';
  if (['alert', 'crit', 'critical', 'error', 'down', 'failed', 'breached'].includes(s2)) return 'alert';
  if (['nodata', 'unknown', 'no_data'].includes(s2)) return 'nodata';
  return fallback || 'nodata';
}

function stateFromRate(hits, errors) {
  if (!isNum(hits) || hits <= 0) return 'nodata';
  const r = isNum(errors) ? errors / hits : 0;
  return r >= 0.05 ? 'alert' : r >= 0.01 ? 'warn' : 'ok';
}

const STATE_KEY = { ok: 'OK', warn: 'WARN', alert: 'ALERT', nodata: 'NODATA' };

// Champs tolérants d'un service du catalogue.
function svcName(s2) { return s2.name || s2.service || ''; }
function svcApm(s2) { const a = s2.apm && typeof s2.apm === 'object' ? s2.apm : s2; return { hits: a.hits, errors: a.errors, p95_ms: a.p95_ms, error_rate: a.error_rate }; }
function svcMonitors(s2) { return asList(s2.monitors); }
function svcWorst(s2) {
  const w = s2.worst_state || s2.monitors_worst || s2.state;
  if (w) return normState(w);
  const list = svcMonitors(s2).map((m) => String(m.state || m.overall_state || 'OK').toUpperCase());
  for (const st of STATES) if (list.includes(st)) return normState(st);
  return list.length ? 'ok' : null;
}
function svcLinks(s2) {
  const l = s2.links && typeof s2.links === 'object' ? s2.links : {};
  return {
    runbook: l.runbook || s2.runbook_url || '', repo: l.repo || s2.repo_url || '',
    docs: l.docs || s2.docs_url || '', chat: l.chat || l.discussion || s2.chat_url || '',
  };
}
function count(v) { return Array.isArray(v) ? v.length : isNum(v) ? v : null; }
function safeUrl(u) { return /^https?:\/\//i.test(String(u || '')) ? String(u) : null; }

function linkIcons(s2) {
  const l = svcLinks(s2);
  const items = [['runbook', 'info'], ['repo', 'link'], ['docs', 'list'], ['chat', 'users']].filter(([k]) => safeUrl(l[k]));
  if (!items.length) return h('span', { class: 'muted' }, '-');
  return h('span', { class: 'row', style: { gap: '2px' } }, items.map(([k, ic]) => h('a', { class: 'btn ghost sm icon', href: safeUrl(l[k]), target: '_blank', rel: 'noopener noreferrer', title: t('svc.link.' + k) }, icon(ic, 13))));
}

export async function render(root, ctx) {
  const v = ctx.route.view;
  if (v === 'service') return renderService(root, ctx);
  if (v === 'service_map') return renderServiceMap(root, ctx);
  if (v === 'network_map') return renderNetworkMap(root, ctx);
  return renderCatalog(root, ctx);
}

// ── Catalogue ─────────────────────────────────────────────────────────────
async function renderCatalog(root, ctx) {
  const { params, signal, href, range } = ctx;
  ctx.setCrumbs([{ label: t('nav.services'), href: href('/services') }, { label: t('nav.services.catalog') }]);
  let q = params.get('q') || '';
  let team = params.get('team') || '';
  let kind = params.get('kind') || 'all';
  const writable = canWrite(ctx.me);
  const search = searchInput({ placeholder: t('svc.search_ph'), value: q, onInput: (v) => { q = v; ctx.setParams({ q: v || null }, { silent: true }); draw(); } });
  search.style.flex = '1 1 280px';
  const teamSlot = h('span');
  const body = h('div');
  const countEl = h('span', { class: 'sub' });
  root.appendChild(h('div', { class: 'querybar' }, search, teamSlot,
    pills([{ key: 'all', label: t('svc.kind.all') }, { key: 'declared', label: t('svc.kind.declared') }, { key: 'seen', label: t('svc.kind.seen') }], kind, (k) => { kind = k; ctx.setParams({ kind: k === 'all' ? null : k }, { silent: true }); draw(); }),
    h('a', { class: 'btn', href: href('/services/map') }, icon('graph', 13), t('nav.services.map')),
    writable ? h('button', { class: 'btn primary', type: 'button', onclick: () => serviceForm(ctx, null, () => ctx.refresh()) }, icon('plus', 14), t('svc.declare')) : null));
  root.appendChild(panel(t('svc.catalog'), body, { flush: true, actions: countEl }));

  let rows = [];
  function draw() {
    const needle = q.trim().toLowerCase();
    const list = rows.filter((s2) => {
      if (team && (s2.team || '') !== team) return false;
      const declared = s2.declared === true || (s2.declared === undefined && s2.id !== undefined && s2.id !== null);
      if (kind === 'declared' && !declared) return false;
      if (kind === 'seen' && declared) return false;
      if (!needle) return true;
      return [svcName(s2), s2.team, s2.description, s2.owner, Object.entries(s2.tags || {}).map(([k, v]) => k + ':' + v).join(' ')].join(' ').toLowerCase().includes(needle);
    });
    countEl.textContent = tn('svc.count', list.length);
    clear(body);
    if (!rows.length) { body.appendChild(empty(t('svc.none_title'), t('svc.none_text'))); return; }
    const maxP95 = Math.max(1, ...list.map((s2) => svcApm(s2).p95_ms || 0));
    body.appendChild(dataTable({
      columns: [
        { key: 'state', label: '', width: '28px', value: (s2) => ['alert', 'warn', 'nodata', 'ok'].indexOf(svcWorst(s2) || 'nodata'), render: (s2) => { const w = svcWorst(s2); return w ? h('span', { class: ['dot', w], title: t('state.' + w) }) : h('span', { class: 'dot nodata', title: t('svc.no_monitor') }); } },
        { key: 'name', label: t('col.service'), value: svcName, render: (s2) => h('div', h('a', { href: href('/service/' + encodeURIComponent(svcName(s2))) }, h('b', { style: { fontWeight: '600' } }, svcName(s2))),
          s2.description ? h('div', { class: 'xsmall muted ellipsis', style: { maxWidth: '320px' }, title: s2.description }, s2.description) : null) },
        { key: 'team', label: t('svc.team'), render: (s2) => (s2.team ? h('span', { class: 'tag' }, s2.team) : h('span', { class: 'muted' }, '-')) },
        { key: 'tier', label: t('svc.tier'), align: 'r', render: (s2) => (isNum(s2.tier) ? h('span', { class: ['badge', s2.tier === 1 ? 'alert' : s2.tier === 2 ? 'warn' : 'muted'] }, t('svc.tier_n', { n: s2.tier })) : '-') },
        { key: 'hits', label: t('col.hits'), align: 'r', value: (s2) => svcApm(s2).hits, render: (s2) => fmtCompact(svcApm(s2).hits) },
        { key: 'err', label: t('col.error_rate'), align: 'r', value: (s2) => errorPct(svcApm(s2)), render: (s2) => { const p = errorPct(svcApm(s2)); return h('span', { style: { color: !isNum(p) ? null : p >= 5 ? 'var(--crit)' : p >= 1 ? 'var(--warn)' : null } }, fmtPct(p, 2)); } },
        { key: 'p95', label: 'p95', align: 'r', value: (s2) => svcApm(s2).p95_ms, render: (s2) => fmtDuration(svcApm(s2).p95_ms) },
        { key: 'log_errors', label: t('svc.log_errors'), align: 'r', value: (s2) => s2.log_errors_1h ?? s2.log_errors, render: (s2) => { const v = s2.log_errors_1h ?? s2.log_errors; return h('span', { style: { color: v > 0 ? 'var(--warn)' : null } }, fmtCompact(v)); } },
        { key: 'monitors', label: t('nav.monitors'), align: 'r', value: (s2) => count(s2.monitors) ?? s2.monitors_count, render: (s2) => { const n = count(s2.monitors) ?? s2.monitors_count; return isNum(n) ? h('span', { class: 'row', style: { gap: '4px', justifyContent: 'flex-end' } }, svcWorst(s2) ? h('span', { class: ['dot', svcWorst(s2)] }) : null, fmtInt(n)) : '-'; } },
        { key: 'slos', label: 'SLO', align: 'r', value: (s2) => count(s2.slos) ?? s2.slos_count, render: (s2) => fmtInt(count(s2.slos) ?? s2.slos_count) },
        { key: 'issues', label: t('svc.open_issues'), align: 'r', value: (s2) => count(s2.open_issues ?? s2.issues_open ?? s2.errors_open), render: (s2) => { const n = count(s2.open_issues ?? s2.issues_open ?? s2.errors_open); return h('span', { style: { color: n > 0 ? 'var(--crit)' : null } }, fmtInt(n)); } },
        { key: 'links', label: t('svc.links'), sort: false, render: linkIcons },
      ],
      rows: list, sortKey: 'name', sortDir: 'asc',
      onRowClick: (s2) => ctx.navigate('/service/' + encodeURIComponent(svcName(s2))),
      emptyText: t('svc.no_match'),
    }));
  }

  await load(body, async () => asList(await get('/services', { from: range.from, to: range.to }, { signal }), 'services'), (list) => {
    rows = list;
    const teams = Array.from(new Set(list.map((s2) => s2.team).filter(Boolean))).sort();
    clear(teamSlot);
    if (teams.length) teamSlot.appendChild(select([{ value: '', label: t('svc.all_teams') }].concat(teams.map((x) => ({ value: x, label: x }))), team, (v) => { team = v; ctx.setParams({ team: v || null }, { silent: true }); draw(); }, { style: { width: 'auto' } }));
    draw();
    return null;
  });
}

// Formulaire de déclaration (member) : le lien runbook apparaît dans les notifications.
function serviceForm(ctx, existing, onDone) {
  const s2 = Object.assign({ name: '', description: '', team: '', owner: '', tier: null, runbook_url: '', repo_url: '', docs_url: '', chat_url: '', tags: {}, depends_on: [] }, existing || {});
  const l = svcLinks(s2);
  const inp = (v, ph, mono) => h('input', { class: ['input', mono ? 'mono' : null], value: v || '', placeholder: ph || '' });
  const name = inp(s2.name || s2.service, 'api', true);
  if (existing) name.disabled = true;
  const desc = h('textarea', { class: 'input', rows: 2 });
  desc.value = s2.description || '';
  const team = inp(s2.team, 'plateforme');
  const owner = inp(s2.owner, 'prenom.nom@example.fr');
  const tier = select([{ value: '', label: t('svc.tier_none') }].concat([1, 2, 3, 4].map((n) => ({ value: n, label: t('svc.tier_n', { n }) + ' · ' + t('svc.tier_desc.' + n) }))), s2.tier ?? '');
  const urls = { runbook: inp(l.runbook, 'https://', true), repo: inp(l.repo, 'https://', true), docs: inp(l.docs, 'https://', true), chat: inp(l.chat, 'https://', true) };
  const tags = inp(Object.entries(s2.tags || {}).map(([k, v]) => k + ':' + v).join(', '), 'env:prod, langage:python', true);
  const deps = inp(asList(s2.depends_on).join(', '), 'postgres, redis', true);
  const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
  const btn = h('button', { class: 'btn primary', type: 'button' }, existing ? t('action.save') : t('svc.declare'));
  const m = modal({
    title: existing ? t('svc.edit') : t('svc.declare'), wide: true,
    body: h('div', { class: 'form-grid' },
      field(t('col.service'), name, t('svc.name_hint')), field(t('svc.team'), team), field(t('svc.owner'), owner), field(t('svc.tier'), tier),
      h('div', { class: 'full' }, field(t('col.description'), desc)),
      field(t('svc.link.runbook'), urls.runbook, t('svc.runbook_hint')), field(t('svc.link.repo'), urls.repo), field(t('svc.link.docs'), urls.docs), field(t('svc.link.chat'), urls.chat),
      h('div', { class: 'full' }, field(t('col.tags'), tags, t('svc.tags_hint'))),
      h('div', { class: 'full' }, field(t('svc.depends_on'), deps, t('svc.depends_hint'))),
      h('div', { class: 'full' }, err)),
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), btn],
  });
  btn.addEventListener('click', async () => {
    err.hidden = true;
    const n = name.value.trim();
    if (!n) { err.textContent = t('form.required', { field: t('col.service') }); err.hidden = false; return; }
    for (const [k, el] of Object.entries(urls)) if (el.value.trim() && !safeUrl(el.value.trim())) { err.textContent = t('svc.bad_url', { field: t('svc.link.' + k) }); err.hidden = false; return; }
    const tagObj = {};
    tags.value.split(',').map((x) => splitTag(x.trim())).filter(Boolean).forEach(([k, v]) => { tagObj[k.trim()] = v.trim(); });
    const body = {
      name: n, description: desc.value, team: team.value.trim(), owner: owner.value.trim(), tier: tier.value ? Number(tier.value) : null,
      runbook_url: urls.runbook.value.trim(), repo_url: urls.repo.value.trim(), docs_url: urls.docs.value.trim(), chat_url: urls.chat.value.trim(),
      tags: tagObj, depends_on: deps.value.split(',').map((x) => x.trim()).filter(Boolean),
    };
    btn.disabled = true;
    try {
      if (existing) await put('/services/' + encodeURIComponent(n), body); else await post('/services', body);
      m.close();
      toast(existing ? t('svc.saved') : t('svc.declared'), 'ok');
      if (onDone) onDone(n);
    } catch (e) { btn.disabled = false; err.textContent = errorMessage(e); err.hidden = false; }
  });
}

// ── Fiche service ─────────────────────────────────────────────────────────
const SVC_TABS = ['overview', 'monitors', 'errors', 'deps'];

async function renderService(root, ctx) {
  const { args, params, range, signal, href } = ctx;
  const name = args.name;
  ctx.setCrumbs([{ label: t('nav.services'), href: href('/services') }, { label: name }]);
  let svc = null;
  let svcErr = null;
  try {
    svc = await get('/services/' + encodeURIComponent(name), { from: range.from, to: range.to }, { signal });
    if (svc && svc.service && typeof svc.service === 'object') svc = Object.assign({}, svc, svc.service);
  } catch (e) {
    if (isAbort(e)) return;
    svcErr = e;
  }
  svc = svc || { name };
  const writable = canWrite(ctx.me);
  const declared = svc.declared === true || (svc.declared === undefined && svc.id !== undefined && svc.id !== null);
  const worst = svcWorst(svc);
  root.appendChild(h('div', { class: 'page-head' },
    h('div', { style: { minWidth: '0' } },
      h('div', { class: 'row wrap', style: { gap: '8px' } }, h('h1', name), worst ? badgeState(STATE_KEY[worst]) : null,
        svc.team ? h('span', { class: 'tag' }, svc.team) : null, isNum(svc.tier) ? h('span', { class: 'badge muted' }, t('svc.tier_n', { n: svc.tier })) : null,
        h('span', { class: ['badge', declared ? 'info' : 'muted'] }, declared ? t('svc.kind.declared_one') : t('svc.kind.seen_one'))),
      svc.description ? h('div', { class: 'desc' }, svc.description) : null),
    h('span', { class: 'spacer' }),
    linkIcons(svc),
    h('a', { class: 'btn sm', href: href('/apm/service/' + encodeURIComponent(name)) }, icon('apm', 13), t('svc.open_apm')),
    h('a', { class: 'btn sm', href: href('/logs', { q: 'service:' + (/\s/.test(name) ? '"' + name + '"' : name) }) }, icon('logs', 13), t('apm.service_logs')),
    writable ? h('button', { class: 'btn sm', type: 'button', onclick: () => serviceForm(ctx, declared ? svc : Object.assign({}, svc, { name }), () => ctx.refresh()) }, icon('edit', 13), declared ? t('action.edit') : t('svc.declare')) : null,
    writable && declared ? h('button', { class: 'btn sm danger', type: 'button', onclick: async () => {
      if (!(await confirmDialog({ title: t('svc.delete_title'), text: t('svc.delete_text', { name }), confirmLabel: t('action.delete'), danger: true }))) return;
      try { await del('/services/' + encodeURIComponent(name)); toast(t('svc.deleted'), 'ok'); ctx.navigate('/services'); } catch (e) { toast(errorMessage(e), 'error'); }
    } }, icon('trash', 13)) : null));
  if (svcErr) root.appendChild(h('div', { class: 'mb-3' }, svcErr.status === 404 && svcErr.code ? h('div', { class: 'callout' }, icon('info', 16), h('span', { class: 'small' }, t('svc.not_in_catalog'))) : routeErrorState(svcErr, () => ctx.refresh())));

  let tab = SVC_TABS.includes(params.get('tab')) ? params.get('tab') : 'overview';
  const tabBar = h('div', { class: 'mb-3' });
  const body = h('div');
  root.appendChild(tabBar);
  root.appendChild(body);
  const mons = svcMonitors(svc);
  function drawTabs() {
    clear(tabBar);
    tabBar.appendChild(tabs(SVC_TABS.map((k) => ({ key: k, label: t('svc.tab.' + k), count: k === 'monitors' ? (mons.length || null) : k === 'errors' ? count(svc.open_issues ?? svc.issues_open) : null })), tab,
      (k) => { tab = k; ctx.setParams({ tab: k === 'overview' ? null : k }, { silent: true }); drawTabs(); drawTab(); }));
  }
  function drawTab() {
    clear(body);
    if (tab === 'monitors') return serviceMonitors(body, ctx, svc, name);
    if (tab === 'errors') return serviceErrors(body, ctx, name);
    if (tab === 'deps') return serviceDeps(body, ctx, svc, name);
    return serviceOverview(body, ctx, svc, name);
  }
  drawTabs();
  drawTab();
}

function serviceOverview(body, ctx, svc, name) {
  const { range, signal } = ctx;
  const a = svcApm(svc);
  const l = svcLinks(svc);
  body.appendChild(h('div', { class: 'tiles mb-3' },
    tile(t('col.hits'), fmtCompact(a.hits)),
    tile(t('col.error_rate'), fmtPct(errorPct(a), 2), null, (() => { const p = errorPct(a); return !isNum(p) ? null : p >= 5 ? 'var(--crit)' : p >= 1 ? 'var(--warn)' : null; })()),
    tile('p95', fmtDuration(a.p95_ms)),
    tile(t('svc.log_errors'), fmtCompact(svc.log_errors_1h ?? svc.log_errors), t('svc.last_hour')),
    tile(t('svc.open_issues'), fmtInt(count(svc.open_issues ?? svc.issues_open ?? svc.errors_open))),
    tile('SLO', fmtInt(count(svc.slos) ?? svc.slos_count))));
  const chartsEl = h('div', { class: 'mb-3' });
  const deployEl = h('div');
  body.appendChild(chartsEl);
  body.appendChild(h('div', { class: 'split' },
    panel(t('svc.deploys'), deployEl, { flush: true, sub: t('svc.deploys_sub') }),
    h('div', { class: 'split-v' },
      panel(t('svc.about'), kv([
        [t('svc.team'), svc.team || null], [t('svc.owner'), svc.owner || null], [t('svc.tier'), isNum(svc.tier) ? t('svc.tier_n', { n: svc.tier }) + ' · ' + t('svc.tier_desc.' + svc.tier) : null],
        [t('svc.link.runbook'), safeUrl(l.runbook) ? h('a', { href: safeUrl(l.runbook), target: '_blank', rel: 'noopener noreferrer' }, l.runbook) : null],
        [t('svc.link.repo'), safeUrl(l.repo) ? h('a', { href: safeUrl(l.repo), target: '_blank', rel: 'noopener noreferrer' }, l.repo) : null],
        [t('svc.link.docs'), safeUrl(l.docs) ? h('a', { href: safeUrl(l.docs), target: '_blank', rel: 'noopener noreferrer' }, l.docs) : null],
        [t('svc.link.chat'), safeUrl(l.chat) ? h('a', { href: safeUrl(l.chat), target: '_blank', rel: 'noopener noreferrer' }, l.chat) : null],
        [t('svc.depends_on'), asList(svc.depends_on).length ? asList(svc.depends_on).join(', ') : null],
      ])),
      svc.tags && Object.keys(svc.tags).length ? panel(t('col.tags'), tagList(svc.tags)) : null)));

  // Événements de déploiement du service, superposés aux courbes APM.
  const deploys = get('/events', { q: 'source:deploy', from: range.from, to: range.to }, { signal })
    .then((r) => asList(r, 'events').filter((ev) => !ev.tags || !ev.tags.service || ev.tags.service === name))
    .catch(() => []);
  load(chartsEl, async () => {
    const [d, evs] = await Promise.all([get('/apm/services/' + encodeURIComponent(name), { from: range.from, to: range.to }, { signal }), deploys]);
    return { d, evs };
  }, ({ d, evs }) => {
    const ts = (d && d.timeseries) || {};
    const marks = evs.map((ev) => ({ ts: toMs(ev.ts), title: ev.title || (ev.tags && ev.tags.version ? t('svc.deploy_of', { v: ev.tags.version }) : t('svc.deploy')) }));
    const one = (label, pts, unit, color, display) => {
      const { xs, ys } = alignPoints([pts || []]);
      return timeChart({ xs, lines: [{ label, values: ys[0], color, unit }], unit, height: 150, display, xRange: [range.from, range.to], onRange: ctx.onRange, sync: 'svcp-' + name, legend: false, events: marks });
    };
    clear(deployEl);
    if (!evs.length) deployEl.appendChild(empty(null, t('svc.no_deploy')));
    else deployEl.appendChild(dataTable({
      columns: [
        { key: 'ts', label: t('col.date'), value: (ev) => toMs(ev.ts), render: (ev) => h('span', { class: 'num' }, fmtDateTime(ev.ts)) },
        { key: 'version', label: t('svc.version'), value: (ev) => (ev.tags && ev.tags.version) || '', render: (ev) => h('span', { class: 'mono small' }, (ev.tags && ev.tags.version) || '-') },
        { key: 'title', label: t('col.title'), tdCls: 'trunc' },
      ], rows: evs, sortKey: 'ts', sortDir: 'desc', maxRows: 50,
    }));
    return h('div', { class: 'grid c3' },
      panel(t('apm.chart.hits'), one(t('col.hits'), ts.hits, 'count', cssVar('--s1'), 'bars')),
      panel(t('apm.chart.errors'), one(t('col.errors'), ts.errors, 'count', cssVar('--crit'), 'bars')),
      panel(t('apm.chart.p95'), one('p95', ts.p95_ms, 'millisecond', cssVar('--s7'), 'line')));
  });
}

function serviceMonitors(body, ctx, svc, name) {
  const { signal, href } = ctx;
  load(body, async () => {
    const own = svcMonitors(svc);
    if (own.length) return own;
    const all = asList(await get('/monitors', null, { signal }), 'monitors');
    return all.filter((m) => (m.tags && m.tags.service === name) || JSON.stringify(m.query || '').includes('service:' + name) || (m.query && m.query.service === name));
  }, (list) => panel(t('svc.tab.monitors'), dataTable({
    columns: [
      { key: 'state', label: t('col.state'), value: (m) => ['ALERT', 'WARN', 'NODATA', 'OK'].indexOf(String(m.state || m.overall_state || 'OK').toUpperCase()), render: (m) => badgeState(String(m.state || m.overall_state || 'OK').toUpperCase()) },
      { key: 'name', label: t('col.name'), render: (m) => h('a', { href: href('/monitors/' + m.id) }, m.name || '#' + m.id) },
      { key: 'type', label: t('col.type'), render: (m) => (m.type ? h('span', { class: 'badge muted' }, m.type) : '-') },
      { key: 'priority', label: t('svc.priority'), align: 'r', render: (m) => (isNum(m.priority) ? 'P' + m.priority : '-') },
    ],
    rows: list, sortKey: 'state', sortDir: 'asc', emptyText: t('svc.no_monitor_linked'),
  }), { flush: true, actions: h('a', { class: 'btn sm', href: href('/monitors/new', { type: 'apm', service: name }) }, icon('plus', 12), t('apm.create_monitor')) }));
}

function serviceErrors(body, ctx, name) {
  const { signal, href, range } = ctx;
  load(body, async () => asList(await get('/errors', { service: name, status: 'open', from: range.from, to: range.to }, { signal }), 'errors', 'issues'), (list) => panel(t('svc.tab.errors'), dataTable({
    columns: [
      { key: 'flag', label: '', sort: false, render: (e) => (isRegressed(e, range) ? h('span', { class: 'badge alert' }, t('errors.regressed')) : isNew(e, range) ? h('span', { class: 'badge warn' }, t('errors.new')) : null) },
      { key: 'error_type', label: t('errors.col.issue'), render: (e) => h('a', { href: href('/errors/' + encodeURIComponent(e.id)) }, e.error_type || t('errors.untyped')) },
      { key: 'message', label: t('col.message'), tdCls: 'trunc', render: (e) => h('span', { class: 'small', title: e.message || '' }, e.message || '-') },
      { key: 'count_in_range', label: t('errors.col.in_range'), align: 'r', render: (e) => fmtCompact(e.count_in_range) },
      { key: 'last_seen', label: t('errors.col.last'), align: 'r', value: (e) => toMs(e.last_seen), render: (e) => fmtRel(e.last_seen) },
    ],
    rows: list, sortKey: 'last_seen', sortDir: 'desc', emptyText: t('errors.none_open'),
  }), { flush: true, actions: h('a', { class: 'btn sm', href: href('/errors', { service: name }) }, t('svc.all_errors')) }));
}

function serviceDeps(body, ctx, svc, name) {
  const { range, signal, href } = ctx;
  load(body, () => get('/apm/service-map', { from: range.from, to: range.to }, { signal }), (map) => {
    const nodes = asList(map && map.nodes);
    const edges = asList(map && map.edges);
    const near = new Set([name]);
    const up = edges.filter((e) => e.to === name);
    const down = edges.filter((e) => e.from === name);
    up.forEach((e) => near.add(e.from));
    down.forEach((e) => near.add(e.to));
    for (const d of asList(svc.depends_on)) near.add(d);
    const sub = nodes.filter((n) => near.has(n.service));
    for (const d of asList(svc.depends_on)) if (!sub.some((n) => n.service === d)) sub.push({ service: d, kind: 'service', state: 'nodata' });
    const subEdges = edges.filter((e) => near.has(e.from) && near.has(e.to) && (e.from === name || e.to === name));
    for (const d of asList(svc.depends_on)) if (!subEdges.some((e) => e.from === name && e.to === d)) subEdges.push({ from: name, to: d, hits: 0, declared: true });
    const graph = serviceGraph(sub, subEdges, ctx, { height: 380 });
    ctx.onCleanup(() => graph.destroy());
    const tbl = (list, other) => dataTable({
      columns: [
        { key: other, label: t('col.service'), render: (e) => h('a', { href: href('/service/' + encodeURIComponent(e[other])) }, e[other]) },
        { key: 'hits', label: t('col.hits'), align: 'r', render: (e) => (e.declared ? h('span', { class: 'badge muted' }, t('svc.declared_dep')) : fmtCompact(e.hits)) },
        { key: 'err', label: t('col.error_rate'), align: 'r', value: (e) => errorPct(e), render: (e) => fmtPct(errorPct(e), 2) },
        { key: 'p95_ms', label: 'p95', align: 'r', render: (e) => fmtDuration(e.p95_ms) },
      ], rows: list, sortKey: 'hits', sortDir: 'desc', emptyText: t('svc.no_dep'),
    });
    return h('div', { class: 'col gap-3' },
      panel(t('svc.deps_graph'), h('div', graph, graphLegend(KIND_LEGEND())), { flush: true }),
      h('div', { class: 'grid c2' },
        panel(t('svc.upstream'), tbl(up, 'from'), { flush: true, sub: t('svc.upstream_sub') }),
        panel(t('svc.downstream'), tbl(subEdges.filter((e) => e.from === name), 'to'), { flush: true, sub: t('svc.downstream_sub') })));
  });
}

const KIND_LEGEND = () => [['service', t('svc.node.service')], ['database', t('svc.node.database')], ['external', t('svc.node.external')]];

// Graphe de services réutilisable (page, onglet Dépendances, widget).
export function serviceGraph(nodes, edges, ctx, opts = {}) {
  return forceGraph(
    nodes.map((n) => ({ id: n.service, label: n.service, kind: ['database', 'external'].includes(n.kind) ? n.kind : 'service', state: n.state ? normState(n.state) : stateFromRate(n.hits, n.errors), size: isNum(n.hits) ? n.hits : 0, data: n })),
    edges.map((e) => ({ from: e.from, to: e.to, weight: isNum(e.hits) ? e.hits : 0, errors: e.errors, data: e })),
    {
      height: opts.height || 560, directed: true, compact: opts.compact, positions: opts.positions,
      onNodeClick: (n) => { if (n.kind === 'service') ctx.navigate('/service/' + encodeURIComponent(n.id)); },
      nodeTip: (n) => [[t('col.type'), t('svc.node.' + n.kind)], [t('col.hits'), fmtCompact(n.data.hits)], [t('col.errors'), fmtCompact(n.data.errors)], ['p95', fmtDuration(n.data.p95_ms)]],
      edgeTip: (e) => [[t('col.hits'), e.data.declared ? t('svc.declared_dep') : fmtCompact(e.data.hits)], [t('col.errors'), fmtCompact(e.data.errors)], ['p95', fmtDuration(e.data.p95_ms)]],
      emptyText: opts.emptyText || t('svc.map_empty'), match: opts.match,
    });
}

// ── Carte des services ────────────────────────────────────────────────────
const positionsCache = new Map();

async function renderServiceMap(root, ctx) {
  const { params, range, signal, href } = ctx;
  ctx.setCrumbs([{ label: t('nav.services'), href: href('/services') }, { label: t('nav.services.map') }]);
  let q = params.get('q') || '';
  const tagF = params.getAll('tag').map(splitTag).filter(Boolean);
  let kinds = params.get('kinds') || 'all';
  const body = h('div');
  const sideEl = h('div');
  const search = searchInput({ placeholder: t('svc.map_search_ph'), value: q, onInput: (v) => { q = v; ctx.setParams({ q: v || null }, { silent: true }); draw(); } });
  search.style.flex = '1 1 240px';
  const tagSlot = h('span', { class: 'row wrap' });
  root.appendChild(h('div', { class: 'querybar' }, search, tagSlot,
    pills([{ key: 'all', label: t('svc.kinds.all') }, { key: 'services', label: t('svc.kinds.services') }], kinds, (k) => { kinds = k; ctx.setParams({ kinds: k === 'all' ? null : k }, { silent: true }); draw(); }),
    h('a', { class: 'btn', href: href('/services') }, icon('list', 13), t('nav.services.catalog'))));
  root.appendChild(h('div', { class: 'map-layout' }, h('section', { class: 'panel' }, body), sideEl));

  let data = { nodes: [], edges: [] };
  let catalog = new Map();
  let graph = null;
  ctx.onCleanup(() => { if (graph) graph.destroy(); });

  function setTags(list) {
    const p = new URLSearchParams(location.hash.split('?')[1] || '');
    p.delete('tag');
    list.forEach(([k, v]) => p.append('tag', k + ':' + v));
    location.hash = '#/services/map' + (p.toString() ? '?' + p.toString() : '');
  }

  function drawTags() {
    clear(tagSlot);
    for (const [k, v] of tagF) tagSlot.appendChild(h('span', { class: 'chip' }, k + ':' + v, h('button', { type: 'button', onclick: () => setTags(tagF.filter((x) => !(x[0] === k && x[1] === v))) }, icon('x', 11))));
    const all = new Map();
    for (const s2 of catalog.values()) {
      const tg = Object.assign({}, s2.tags || {}, s2.team ? { team: s2.team } : {});
      for (const [k, v] of Object.entries(tg)) { if (!all.has(k)) all.set(k, new Set()); all.get(k).add(String(v)); }
    }
    if (!all.size) return;
    const sel = h('select', { class: 'input sm', style: { width: 'auto' } }, h('option', { value: '' }, t('infra.add_tag')));
    for (const [k, vals] of Array.from(all.entries()).sort()) for (const v of Array.from(vals).sort()) sel.appendChild(h('option', { value: k + ':' + v }, k + ':' + v));
    sel.addEventListener('change', () => { const tv = splitTag(sel.value); if (tv) setTags(tagF.concat([tv])); });
    tagSlot.appendChild(sel);
  }

  function draw() {
    if (graph) { graph.destroy(); graph = null; }
    clear(body);
    clear(sideEl);
    const needle = q.trim().toLowerCase();
    let nodes = asList(data.nodes);
    if (kinds === 'services') nodes = nodes.filter((n) => !n.kind || n.kind === 'service');
    if (tagF.length) nodes = nodes.filter((n) => { const c = catalog.get(n.service); const tg = c ? Object.assign({}, c.tags || {}, c.team ? { team: c.team } : {}) : {}; return matchesTagFilters(tg, tagF); });
    // Recherche : les services trouvés, leurs voisins et leurs seules liaisons.
    const found = new Set(needle ? nodes.filter((n) => String(n.service).toLowerCase().includes(needle)).map((n) => n.service) : []);
    if (needle) {
      const allowed = new Set(nodes.map((n) => n.service));
      const hit = new Set(found);
      for (const e of asList(data.edges)) {
        if (found.has(e.from) && allowed.has(e.to)) hit.add(e.to);
        if (found.has(e.to) && allowed.has(e.from)) hit.add(e.from);
      }
      nodes = nodes.filter((n) => hit.has(n.service));
    }
    const keep = new Set(nodes.map((n) => n.service));
    const edges = asList(data.edges).filter((e) => keep.has(e.from) && keep.has(e.to) && (!needle || found.has(e.from) || found.has(e.to)));
    body.appendChild(h('div', { class: 'panel-head' }, h('h3', t('nav.services.map')), h('span', { class: 'sub' }, tn('svc.map_count', nodes.length, { edges: fmtInt(edges.length) }))));
    graph = serviceGraph(nodes, edges, ctx, { positions: positionsCache, match: found, emptyText: needle ? t('svc.map_no_match', { q: q.trim() }) : undefined });
    body.appendChild(graph);
    body.appendChild(h('div', { class: 'panel-body', style: { paddingTop: '0' } }, graphLegend(KIND_LEGEND())));
    // Côté : services les plus en erreur
    const worst = nodes.filter((n) => isNum(n.hits) && n.hits > 0).map((n) => Object.assign({ pct: (n.errors || 0) / n.hits * 100 }, n)).sort((a, b) => b.pct - a.pct).slice(0, 10);
    sideEl.appendChild(panel(t('svc.map_worst'), worst.length ? dataTable({
      columns: [
        { key: 'service', label: t('col.service'), render: (n) => h('a', { href: href('/service/' + encodeURIComponent(n.service)) }, n.service) },
        { key: 'pct', label: t('col.error_rate'), align: 'r', render: (n) => h('span', { style: { color: n.pct >= 5 ? 'var(--crit)' : n.pct >= 1 ? 'var(--warn)' : null } }, fmtPct(n.pct, 2)) },
        { key: 'p95_ms', label: 'p95', align: 'r', render: (n) => fmtDuration(n.p95_ms) },
      ], rows: worst, sortKey: 'pct', sortDir: 'desc',
    }) : empty(null, t('svc.map_no_traffic')), { flush: true }));
  }

  await load(body, async () => {
    const [m, cat] = await Promise.all([
      get('/apm/service-map', { from: range.from, to: range.to }, { signal }),
      get('/services', { from: range.from, to: range.to }, { signal }).then((r) => asList(r, 'services')).catch(() => []),
    ]);
    return { m, cat };
  }, ({ m, cat }) => {
    data = { nodes: asList(m && m.nodes), edges: asList(m && m.edges) };
    catalog = new Map(cat.map((s2) => [svcName(s2), s2]));
    drawTags();
    draw();
    return null;
  });
}

// ── Carte du réseau ───────────────────────────────────────────────────────
const netPositions = new Map();

export function networkGraph(map, ctx, opts = {}) {
  const nodes = asList(map && map.nodes);
  const pairs = new Map();
  for (const e of asList(map && map.edges)) {
    const a = String(e.from); const b = String(e.to);
    const key = a + '>' + b;
    if (!pairs.has(key)) pairs.set(key, { from: a, to: b, weight: 0, ports: new Map() });
    const p = pairs.get(key);
    const c = isNum(e.count) ? e.count : 1;
    p.weight += c;
    const pk = (e.proto ? String(e.proto).toUpperCase() + ' ' : '') + (e.port || '') + (e.process ? ' · ' + e.process : '');
    p.ports.set(pk, (p.ports.get(pk) || 0) + c);
  }
  const deg = new Map();
  for (const p of pairs.values()) { deg.set(p.from, (deg.get(p.from) || 0) + p.weight); deg.set(p.to, (deg.get(p.to) || 0) + p.weight); }
  return forceGraph(
    nodes.map((n) => ({ id: String(n.id), label: n.hostname || String(n.id), kind: ['network_device', 'cloud_resource'].includes(n.kind) ? n.kind : 'host', state: normState(n.state || n.status), size: deg.get(String(n.id)) || 0, data: n })),
    Array.from(pairs.values()).map((p) => ({ from: p.from, to: p.to, weight: p.weight, data: p })),
    {
      height: opts.height || 600, directed: true, compact: opts.compact, positions: opts.positions,
      onNodeClick: (n) => ctx.navigate('/infrastructure/host/' + encodeURIComponent(n.id)),
      nodeTip: (n) => [[t('col.type'), t(['network_device', 'cloud_resource'].includes(n.kind) ? 'infra.kind.' + n.kind : 'infra.kind.agent')], [t('col.os'), n.data.os || null], [t('col.connections'), fmtInt(deg.get(n.id) || 0)],
        [t('col.tags'), Object.entries(n.data.tags || {}).slice(0, 4).map(([k, v]) => k + ':' + v).join(' ') || null]],
      edgeTip: (e) => [[t('col.connections'), fmtInt(e.weight)]].concat(Array.from(e.data.ports.entries()).sort((a, b) => b[1] - a[1]).slice(0, 6).map(([k, v]) => [k, fmtInt(v)])),
      emptyText: opts.emptyText || t('net.map_empty'), match: opts.match,
    });
}

async function renderNetworkMap(root, ctx) {
  const { params, range, signal, href } = ctx;
  ctx.setCrumbs([{ label: t('nav.network'), href: href('/network') }, { label: t('nav.network.map') }]);
  const tagParam = params.get('tag') || '';
  let q = params.get('q') || '';
  const tagInput = h('input', { class: 'input mono', value: tagParam, placeholder: 'role:web', style: { width: '180px' }, title: t('net.map_tag_title') });
  tagInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') ctx.setParams({ tag: tagInput.value.trim() || null }); });
  const search = searchInput({ placeholder: t('net.map_search_ph'), value: q, onInput: (v) => { q = v; ctx.setParams({ q: v || null }, { silent: true }); draw(); } });
  search.style.flex = '1 1 240px';
  root.appendChild(h('div', { class: 'querybar' }, search,
    h('label', { class: 'row small muted' }, t('net.map_tag'), tagInput),
    h('button', { class: 'btn', type: 'button', onclick: () => ctx.setParams({ tag: tagInput.value.trim() || null }) }, t('action.apply')),
    h('a', { class: 'btn', href: href('/network') }, icon('list', 13), t('nav.network.connections'))));
  const body = h('div');
  const sideEl = h('div');
  root.appendChild(h('div', { class: 'map-layout' }, h('section', { class: 'panel' }, body), sideEl));
  let data = null;
  let graph = null;
  ctx.onCleanup(() => { if (graph) graph.destroy(); });
  const tv = splitTag(tagParam);

  function draw() {
    if (graph) { graph.destroy(); graph = null; }
    clear(body);
    let nodes = asList(data.nodes);
    if (tv) nodes = nodes.filter((n) => matchesTagFilters(Object.assign({ host: n.hostname }, n.tags || {}), [tv]) || (n.kind && n.kind !== 'agent'));
    const needle = q.trim().toLowerCase();
    // Recherche : les hôtes trouvés, leurs voisins et leurs seules liaisons.
    const found = new Set(needle ? nodes.filter((n) => String(n.hostname || '').toLowerCase().includes(needle)).map((n) => String(n.id)) : []);
    if (needle) {
      const allowed = new Set(nodes.map((n) => String(n.id)));
      const hit = new Set(found);
      for (const e of asList(data.edges)) {
        const a = String(e.from); const b = String(e.to);
        if (found.has(a) && allowed.has(b)) hit.add(b);
        if (found.has(b) && allowed.has(a)) hit.add(a);
      }
      nodes = nodes.filter((n) => hit.has(String(n.id)));
    }
    const keep = new Set(nodes.map((n) => String(n.id)));
    const edges = asList(data.edges).filter((e) => keep.has(String(e.from)) && keep.has(String(e.to))
      && (!needle || found.has(String(e.from)) || found.has(String(e.to))));
    body.appendChild(h('div', { class: 'panel-head' }, h('h3', t('nav.network.map')), h('span', { class: 'sub' }, tn('net.map_count', nodes.length, { edges: fmtInt(edges.length) }))));
    graph = networkGraph({ nodes, edges }, ctx, { positions: netPositions, match: found, emptyText: needle ? t('net.map_no_match', { q: q.trim() }) : undefined });
    body.appendChild(graph);
    body.appendChild(h('div', { class: 'panel-body', style: { paddingTop: '0' } }, graphLegend([['host', t('infra.kind.agent')], ['network_device', t('infra.kind.network_device')], ['cloud_resource', t('infra.kind.cloud_resource')]])));
  }

  await load(body, () => get('/network/map', { from: range.from, to: range.to, tag: tagParam || undefined }, { signal }), (m) => {
    data = { nodes: asList(m && m.nodes), edges: asList(m && m.edges) };
    const ext = asList(m && m.external);
    clear(sideEl);
    sideEl.appendChild(panel(t('net.map_external'), dataTable({
      columns: [
        { key: 'addr', label: t('col.remote'), render: (x) => h('span', { class: 'mono small' }, x.addr || '-') },
        { key: 'port', label: t('col.port'), align: 'r' },
        { key: 'count', label: t('col.connections'), align: 'r', render: (x) => fmtInt(x.count) },
      ], rows: ext, sortKey: 'count', sortDir: 'desc', maxRows: 50, emptyText: t('net.map_no_external'),
    }), { flush: true, sub: t('net.map_external_sub') }));
    draw();
    return null;
  });
}
