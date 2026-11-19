// Tableau de bord partagé en lecture (§ 30) : /share/?t=<jeton>. Aucune session : seules les routes
// publiques GET /api/public/dashboards/{jeton} et POST …/query sont appelées.
import { h, clear, icon } from '../js/dom.js';
import { t } from '../js/i18n.js';
import { get, isAbort } from '../js/api.js';
import { fmtDateTime, fmtHM } from '../js/format.js';
import { destroyAllCharts } from '../js/components/chart.js';
import { renderWidget } from '../js/pages/widgets.js';
import { valueRefs } from '../js/pages/widgets-extra.js';

const ROW_H = 72;
const GAP = 10;
const PRESETS = [['1h', 3600e3], ['4h', 4 * 3600e3], ['1d', 86400e3], ['7d', 7 * 86400e3]];
const root = document.getElementById('root');
const token = new URLSearchParams(location.search).get('t') || '';
const cleanups = [];

function presetMs(key) { const p = PRESETS.find(([k]) => k === key); return p ? p[1] : 3600e3; }
function readPreset() { const r = new URLSearchParams(location.search).get('r'); return PRESETS.some(([k]) => k === r) ? r : null; }

function shell(content) {
  clear(root);
  root.appendChild(h('div', { class: 'share' },
    h('header', { class: 'share-top' },
      h('span', { class: 'brand' }, h('img', { src: '../assets/img/hypha.svg', alt: '' }), h('span', { class: 'brand-name' }, 'HYPHA', h('small', t('brand.by')))),
      content.head || null),
    h('main', { class: 'share-main' }, content.body),
    h('footer', { class: 'share-foot' }, t('share.footer'))));
}

function message(title, text) {
  shell({ body: h('div', { class: 'state' }, h('h3', title), text ? h('div', { class: 'small' }, text) : null) });
}

async function boot() {
  document.title = 'HYPHA · ' + t('share.title');
  if (!/^[A-Za-z0-9_\-]{8,200}$/.test(token)) { message(t('share.bad_link'), t('share.bad_link_text')); return; }
  let dash;
  try {
    const r = await get('/api/public/dashboards/' + encodeURIComponent(token), null, { quiet401: true });
    dash = r && r.dashboard && typeof r.dashboard === 'object' ? r.dashboard : r;
  } catch (e) {
    if (e && (e.status === 404 || e.status === 403 || e.status === 401)) message(t('share.gone'), t('share.gone_text'));
    else message(t('err.unknown'), e && e.status === 0 ? t('err.network') : (e && e.detail) || t('err.5xx'));
    return;
  }
  if (!dash || !Array.isArray(dash.widgets)) { message(t('share.gone'), t('share.gone_text')); return; }
  draw(dash);
}

function draw(dash) {
  while (cleanups.length) { try { cleanups.pop()(); } catch (e) { /* nettoyage au mieux */ } }
  destroyAllCharts();
  let preset = readPreset() || '1h';
  const vars = Array.isArray(dash.template_vars) ? dash.template_vars.filter((v) => v && v.name) : [];
  const values = {};
  for (const v of vars) values[v.name] = v.multi && Array.isArray(v.defaults) && v.defaults.length ? v.defaults.slice() : (v.default || '*');
  document.title = (dash.title || t('share.title')) + ' · HYPHA';

  const updated = h('span', { class: 'xsmall muted num' });
  const presetBar = h('div', { class: 'pill-group' }, PRESETS.map(([k]) => h('button', {
    type: 'button', class: k === preset ? 'on' : null,
    onclick: () => { const u = new URL(location.href); u.searchParams.set('r', k); history.replaceState(null, '', u.toString()); draw(dash); },
  }, t('time.preset.' + k))));
  const head = h('div', { class: 'row grow', style: { gap: '10px', minWidth: '0' } },
    h('div', { class: 'grow', style: { minWidth: '0' } }, h('h1', { class: 'ellipsis' }, valueRefs(dash.title || '', vars, values)), dash.description ? h('div', { class: 'small muted ellipsis' }, dash.description) : null),
    vars.length ? h('div', { class: 'tags' }, vars.map((v) => h('span', { class: 'tag', title: t('share.vars_fixed') }, h('b', '$' + v.name + ':'), Array.isArray(values[v.name]) ? values[v.name].join(', ') : values[v.name]))) : null,
    presetBar, updated,
    h('span', { class: 'badge muted', title: t('share.read_only_title') }, icon('lock', 11), t('share.read_only')));
  const grid = h('div', { class: 'dash-grid' });
  shell({ head, body: grid });

  let ctrl = null;
  const ctx = {
    href: () => '#', navigate: () => {}, onRange: () => {},
    onCleanup: (fn) => cleanups.push(fn), isCurrent: () => true, me: null,
  };
  function drawWidgets() {
    if (ctrl) ctrl.abort();
    ctrl = new AbortController();
    destroyAllCharts();
    clear(grid);
    const to = Date.now();
    const range = { from: to - presetMs(preset), to, preset, live: true };
    updated.textContent = t('share.updated', { at: fmtHM(to) });
    updated.title = fmtDateTime(to);
    const widgets = dash.widgets.slice().sort((a, b) => (a.y || 0) - (b.y || 0) || (a.x || 0) - (b.x || 0));
    if (!widgets.length) { grid.appendChild(h('div', { style: { gridColumn: '1 / -1' } }, h('div', { class: 'state' }, t('dash.empty_title')))); return; }
    for (const w of widgets) {
      const x = Math.max(0, Math.min(11, Number(w.x) || 0));
      const ww = Math.max(1, Math.min(12 - x, Number(w.w) || 4));
      const hh = Math.max(1, Number(w.h) || 2);
      const body = h('div', { class: 'widget-body' });
      const el = h('div', { class: 'widget' }, h('div', { class: 'widget-head' }, h('h3', { title: w.title || '' }, valueRefs(w.title || '', vars, values) || t('widget.type.' + w.type))), body);
      el.style.gridColumn = (x + 1) + ' / span ' + ww;
      el.style.gridRow = ((Number(w.y) || 0) + 1) + ' / span ' + hh;
      grid.appendChild(el);
      renderWidget(body, w, {
        range, signal: ctrl.signal, vars, values, ctx, height: hh * ROW_H + (hh - 1) * GAP - 46,
        publicPath: '/api/public/dashboards/' + encodeURIComponent(token) + '/query',
      }).catch((e) => { if (!isAbort(e)) console.error(e); });
    }
  }
  drawWidgets();
  const every = Math.max(30, Number(dash.refresh_s) || 60) * 1000;
  const timer = setInterval(() => { if (document.visibilityState === 'visible') drawWidgets(); }, every);
  cleanups.push(() => clearInterval(timer));
  cleanups.push(() => { if (ctrl) ctrl.abort(); });
}

boot();
