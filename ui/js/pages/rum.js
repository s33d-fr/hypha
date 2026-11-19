// RUM : applications, résumé (vues, sessions, erreurs, LCP/INP/CLS p75, pages lentes), extrait de code.
import { h, clear, icon } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, post, asList, isAbort } from '../api.js';
import { fmtDuration, fmtNum, fmtCompact, fmtRel, fmtDateTime, toMs, isNum } from '../format.js';
import { section, empty, panel, modal, field, toast, errorMessage, errorState, loading, codeBlock, tabs } from '../components/ui.js';
import { dataTable } from '../components/table.js';

// Seuils publics des signaux web (bon / à améliorer / mauvais).
const VITALS = {
  lcp: { good: 2500, poor: 4000, fmt: (v) => fmtDuration(v) },
  inp: { good: 200, poor: 500, fmt: (v) => fmtDuration(v) },
  cls: { good: 0.1, poor: 0.25, fmt: (v) => fmtNum(v, 3) },
};

function pick(obj, names) {
  if (!obj || typeof obj !== 'object') return null;
  for (const n of names) {
    const parts = n.split('.');
    let cur = obj;
    for (const p of parts) cur = cur && typeof cur === 'object' ? cur[p] : undefined;
    if (cur !== undefined && cur !== null) return cur;
  }
  return null;
}

function vitalRating(key, v) {
  const th = VITALS[key];
  if (!isNum(v)) return null;
  return v <= th.good ? 'ok' : v <= th.poor ? 'warn' : 'alert';
}

export function snippet(app) {
  const src = location.origin + '/rum/hypha-rum.js';
  return '<script src="' + src + '" data-token="' + (app.client_token || '') + '" data-service="' + String(app.name || '').replace(/"/g, '') + '" defer></script>';
}

export async function render(root, ctx) {
  if (ctx.route.view === 'app') return renderApp(root, ctx);
  return renderList(root, ctx);
}

function createDialog(ctx) {
  const name = h('input', { class: 'input', maxlength: 120, placeholder: 'www.example.fr' });
  const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
  const btn = h('button', { class: 'btn primary', type: 'button' }, t('rum.create'));
  const m = modal({
    title: t('rum.new'),
    body: h('div', { class: 'col gap-3' }, field(t('rum.f.name'), name, t('rum.f.name_hint')), err),
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), btn],
  });
  btn.addEventListener('click', async () => {
    if (!name.value.trim()) { err.textContent = t('form.required', { field: t('rum.f.name') }); err.hidden = false; return; }
    btn.disabled = true;
    try {
      const r = await post('/rum/apps', { name: name.value.trim() });
      m.close();
      const id = r && (r.id || (r.app && r.app.id));
      toast(t('rum.created'), 'ok');
      if (id) ctx.navigate('/rum/' + id, { tab: 'setup' }); else ctx.refresh();
    } catch (e) { btn.disabled = false; err.textContent = errorMessage(e); err.hidden = false; }
  });
}

async function renderList(root, ctx) {
  const { signal, href } = ctx;
  ctx.setCrumbs([{ label: t('nav.rum') }]);
  const body = h('div');
  root.appendChild(h('div', { class: 'querybar' }, h('span', { class: 'muted small grow' }, t('rum.intro')), h('button', { class: 'btn primary', type: 'button', onclick: () => createDialog(ctx) }, icon('plus', 14), t('rum.new'))));
  root.appendChild(panel(t('rum.apps'), body, { flush: true }));
  await section(body, async () => asList(await get('/rum/apps', null, { signal }), 'apps'), (list) => {
    if (!list.length) return empty(t('rum.none_title'), t('rum.none_text'), h('button', { class: 'btn primary', type: 'button', onclick: () => createDialog(ctx) }, icon('plus', 14), t('rum.new')));
    return dataTable({
      columns: [
        { key: 'name', label: t('col.name'), render: (a) => h('a', { href: href('/rum/' + a.id) }, a.name || '#' + a.id) },
        { key: 'client_token', label: t('rum.token'), render: (a) => h('span', { class: 'mono small' }, a.client_token || '-') },
        { key: 'created_at', label: t('col.created'), align: 'r', value: (a) => toMs(a.created_at), render: (a) => h('span', { title: fmtDateTime(a.created_at) }, fmtRel(a.created_at)) },
      ],
      rows: list, sortKey: 'name', sortDir: 'asc', onRowClick: (a) => ctx.navigate('/rum/' + a.id),
    });
  });
}

async function renderApp(root, ctx) {
  const { args, signal, href, range, params } = ctx;
  ctx.setCrumbs([{ label: t('nav.rum'), href: href('/rum') }, { label: '#' + args.id }]);
  root.appendChild(loading());
  let app;
  try {
    const list = asList(await get('/rum/apps', null, { signal }), 'apps');
    app = list.find((a) => String(a.id) === String(args.id));
  } catch (e) {
    if (isAbort(e)) return;
    clear(root); root.appendChild(errorState(e, () => ctx.refresh())); return;
  }
  clear(root);
  if (!app) { root.appendChild(empty(t('err.404'), t('rum.app_missing'))); return; }
  ctx.setCrumbs([{ label: t('nav.rum'), href: href('/rum') }, { label: app.name || '#' + app.id }]);
  let tab = params.get('tab') === 'setup' ? 'setup' : 'summary';
  root.appendChild(h('div', { class: 'page-head' }, h('h1', app.name || '#' + app.id), h('span', { class: 'mono xsmall muted' }, app.client_token || '')));
  const tabBar = h('div', { class: 'mb-3' });
  const body = h('div');
  root.appendChild(tabBar);
  root.appendChild(body);
  const drawTabs = () => { clear(tabBar); tabBar.appendChild(tabs([{ key: 'summary', label: t('rum.tab.summary') }, { key: 'setup', label: t('rum.tab.setup') }], tab, (k) => { tab = k; ctx.setParams({ tab: k === 'summary' ? null : k }, { silent: true }); drawTabs(); drawBody(); })); };

  function drawBody() {
    clear(body);
    if (tab === 'setup') { body.appendChild(setupView(app)); return; }
    section(body, () => get('/rum/apps/' + encodeURIComponent(app.id) + '/summary', { from: range.from, to: range.to }, { signal }), (s2) => summaryView(s2, app));
  }
  drawTabs();
  drawBody();
}

function summaryView(s2, app) {
  const views = pick(s2, ['views', 'view_count', 'totals.views']);
  const sessions = pick(s2, ['sessions', 'session_count', 'totals.sessions']);
  const errors = pick(s2, ['errors', 'error_count', 'totals.errors']);
  const vit = {
    lcp: pick(s2, ['lcp_p75_ms', 'lcp_p75', 'vitals.lcp_p75', 'vitals.lcp.p75', 'p75.lcp']),
    inp: pick(s2, ['inp_p75_ms', 'inp_p75', 'vitals.inp_p75', 'vitals.inp.p75', 'p75.inp']),
    cls: pick(s2, ['cls_p75', 'vitals.cls_p75', 'vitals.cls.p75', 'p75.cls']),
  };
  const pages = asList(pick(s2, ['slowest_pages', 'slow_pages', 'pages']) || []);
  const box = h('div');
  const tile = (label, value, sub, rating) => h('div', { class: 'tile' }, h('div', { class: 't-label' }, rating ? h('span', { class: ['dot', rating] }) : null, label),
    h('div', { class: 't-value num', style: { fontSize: 'var(--fs-xl)' } }, value), sub ? h('div', { class: 't-sub' }, sub) : null);
  box.appendChild(h('div', { class: 'tiles mb-3' },
    tile(t('rum.views'), fmtCompact(views)),
    tile(t('rum.sessions'), fmtCompact(sessions)),
    tile(t('rum.errors'), fmtCompact(errors)),
    ['lcp', 'inp', 'cls'].map((k) => {
      const r = vitalRating(k, vit[k]);
      return tile(t('rum.vital.' + k) + ' p75', isNum(vit[k]) ? VITALS[k].fmt(vit[k]) : '-', r ? t('rum.rating.' + r) : t('rum.vital_hint.' + k), r);
    })));
  if (!isNum(views) || !views) {
    box.appendChild(h('div', { class: 'callout mb-3' }, icon('info', 16), h('div', { class: 'small' }, t('rum.no_data'), h('div', { class: 'mt-2' }, codeBlock(snippet(app))))));
  }
  box.appendChild(panel(t('rum.slow_pages'), dataTable({
    columns: [
      { key: 'url', label: t('col.page'), tdCls: 'trunc', value: (p) => p.url || p.view_url || p.name, render: (p) => h('span', { class: 'mono small', title: p.url || p.view_url || '' }, p.url || p.view_url || p.name || '-') },
      { key: 'views', label: t('rum.views'), align: 'r', value: (p) => pick(p, ['views', 'count']), render: (p) => fmtCompact(pick(p, ['views', 'count'])) },
      { key: 'p75', label: t('rum.load_p75'), align: 'r', value: (p) => pick(p, ['p75_ms', 'load_p75_ms', 'duration_p75_ms', 'p75']), render: (p) => fmtDuration(pick(p, ['p75_ms', 'load_p75_ms', 'duration_p75_ms', 'p75'])) },
      { key: 'lcp', label: 'LCP p75', align: 'r', value: (p) => pick(p, ['lcp_p75_ms', 'lcp_p75']), render: (p) => { const v = pick(p, ['lcp_p75_ms', 'lcp_p75']); return h('span', { class: vitalRating('lcp', v) ? 'st-text-' + (vitalRating('lcp', v) === 'alert' ? 'error' : vitalRating('lcp', v) === 'warn' ? 'warning' : 'info') : null }, fmtDuration(v)); } },
      { key: 'errors', label: t('rum.errors'), align: 'r', value: (p) => pick(p, ['errors', 'error_count']), render: (p) => fmtCompact(pick(p, ['errors', 'error_count'])) },
    ],
    rows: pages, sortKey: 'p75', sortDir: 'desc', maxRows: 50, emptyText: t('rum.no_pages'),
  }), { flush: true }));
  return box;
}

function setupView(app) {
  return h('div', { class: 'grid c2' },
    panel(t('rum.setup.title'), h('div', { class: 'col gap-3' },
      h('p', { class: 'small text-2' }, t('rum.setup.step1')),
      codeBlock(snippet(app)),
      h('p', { class: 'small text-2' }, t('rum.setup.step2')),
      h('ul', { class: 'small text-2', style: { margin: '0', paddingLeft: '18px' } },
        h('li', t('rum.setup.collect.views')), h('li', t('rum.setup.collect.vitals')), h('li', t('rum.setup.collect.errors')), h('li', t('rum.setup.collect.resources')), h('li', t('rum.setup.collect.spa'))))),
    panel(t('rum.setup.options'), h('div', { class: 'col gap-3' },
      h('table', { class: 'tbl' }, h('thead', h('tr', h('th', t('rum.setup.attr')), h('th', t('rum.setup.effect')))),
        h('tbody',
          [['data-token', 'rum.setup.opt.token'], ['data-service', 'rum.setup.opt.service'], ['data-env', 'rum.setup.opt.env'], ['data-version', 'rum.setup.opt.version'],
            ['data-sample', 'rum.setup.opt.sample'], ['data-full-url', 'rum.setup.opt.full_url'], ['data-endpoint', 'rum.setup.opt.endpoint']]
            .map(([a, k]) => h('tr', h('td', h('span', { class: 'mono small' }, a)), h('td', { class: 'wrap small' }, t(k)))))),
      h('p', { class: 'small muted' }, t('rum.setup.privacy')))));
}
