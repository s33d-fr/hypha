// Application connectée : coquille, routes, période globale, organisation, thème.
import { h, icon, clear, mount, on } from './dom.js';
import { t, getLang } from './i18n.js';
import { get, post, isAbort } from './api.js';
import { parseHash, matchRoute, navigate, setParams, href } from './router.js';
import { PRESETS, rangeFromParams, rangeParams } from './time.js';
import { fmtDateTime, toLocalInput, fromLocalInput } from './format.js';
import { dropdown, menuItem, errorState, loading, toast } from './components/ui.js';
import { destroyAllCharts } from './components/chart.js';
import { problemBadge, startProblemCounter } from './components/problem-count.js';

const page = (loader) => loader;

export const ROUTES = [
  { path: '/problems', nav: 'problems', title: 'nav.problems', load: page(() => import('./pages/problems.js')), time: false },
  { path: '/overview', nav: 'overview', title: 'nav.overview', load: page(() => import('./pages/overview.js')) },
  { path: '/infrastructure', nav: 'infra', sub: 'infra.list', title: 'nav.infra', load: page(() => import('./pages/infra.js')) },
  { path: '/infrastructure/map', nav: 'infra', sub: 'infra.map', title: 'nav.infra', load: page(() => import('./pages/infra.js')), view: 'map' },
  { path: '/inventory', nav: 'infra', sub: 'infra.inventory', title: 'nav.infra.inventory', load: page(() => import('./pages/inventory.js')), time: false },
  { path: '/certificates', nav: 'certificates', title: 'nav.certificates', load: page(() => import('./pages/certificates.js')), time: false },
  { path: '/infrastructure/host/:id', nav: 'infra', title: 'nav.infra', load: page(() => import('./pages/host.js')) },
  { path: '/metrics', nav: 'metrics', title: 'nav.metrics', load: page(() => import('./pages/metrics.js')) },
  { path: '/logs', nav: 'logs', sub: 'logs.search', title: 'nav.logs', load: page(() => import('./pages/logs.js')), flush: false },
  { path: '/apm', nav: 'apm', sub: 'apm.services', title: 'nav.apm', load: page(() => import('./pages/apm.js')), view: 'services' },
  { path: '/apm/service/:name', nav: 'apm', sub: 'apm.services', title: 'nav.apm', load: page(() => import('./pages/apm.js')), view: 'service' },
  { path: '/apm/traces', nav: 'apm', sub: 'apm.traces', title: 'nav.apm', load: page(() => import('./pages/apm.js')), view: 'traces' },
  { path: '/apm/trace/:id', nav: 'apm', sub: 'apm.traces', title: 'nav.apm', load: page(() => import('./pages/trace.js')) },
  { path: '/services', nav: 'services', sub: 'services.catalog', title: 'nav.services', load: page(() => import('./pages/services.js')), view: 'catalog' },
  { path: '/services/map', nav: 'services', sub: 'services.map', title: 'nav.services.map', load: page(() => import('./pages/services.js')), view: 'service_map' },
  { path: '/service/:name', nav: 'services', sub: 'services.catalog', title: 'nav.services', load: page(() => import('./pages/services.js')), view: 'service' },
  { path: '/errors', nav: 'errors', title: 'nav.errors', load: page(() => import('./pages/errors.js')), view: 'list' },
  { path: '/errors/:id', nav: 'errors', title: 'nav.errors', load: page(() => import('./pages/errors.js')), view: 'detail' },
  { path: '/databases', nav: 'databases', title: 'nav.databases', load: page(() => import('./pages/databases.js')), view: 'list' },
  { path: '/databases/query/:id', nav: 'databases', title: 'nav.databases', load: page(() => import('./pages/databases.js')), view: 'query' },
  { path: '/processes', nav: 'processes', title: 'nav.processes', load: page(() => import('./pages/processes.js')), time: false },
  { path: '/network', nav: 'network', sub: 'network.connections', title: 'nav.network', load: page(() => import('./pages/network.js')) },
  { path: '/network/map', nav: 'network', sub: 'network.map', title: 'nav.network.map', load: page(() => import('./pages/services.js')), view: 'network_map' },
  { path: '/containers', nav: 'containers', title: 'nav.containers', load: page(() => import('./pages/containers.js')), time: false },
  { path: '/events', nav: 'events', title: 'nav.events', load: page(() => import('./pages/events.js')) },
  { path: '/monitors', nav: 'monitors', sub: 'monitors.list', title: 'nav.monitors', load: page(() => import('./pages/monitors.js')), view: 'list', time: false },
  { path: '/monitors/new', nav: 'monitors', sub: 'monitors.new', title: 'nav.monitors', load: page(() => import('./pages/monitor-form.js')) },
  { path: '/monitors/:id', nav: 'monitors', sub: 'monitors.list', title: 'nav.monitors', load: page(() => import('./pages/monitors.js')), view: 'detail' },
  { path: '/monitors/:id/edit', nav: 'monitors', sub: 'monitors.list', title: 'nav.monitors', load: page(() => import('./pages/monitor-form.js')) },
  { path: '/downtimes', nav: 'downtimes', title: 'nav.downtimes', load: page(() => import('./pages/downtimes.js')), view: 'list', time: false },
  { path: '/downtimes/new', nav: 'downtimes', title: 'nav.downtimes', load: page(() => import('./pages/downtimes.js')), view: 'form', time: false },
  { path: '/downtimes/:id/edit', nav: 'downtimes', title: 'nav.downtimes', load: page(() => import('./pages/downtimes.js')), view: 'form', time: false },
  { path: '/incidents', nav: 'incidents', title: 'nav.incidents', load: page(() => import('./pages/incidents.js')), view: 'list', time: false },
  { path: '/incidents/:id', nav: 'incidents', title: 'nav.incidents', load: page(() => import('./pages/incidents.js')), view: 'detail', time: false },
  { path: '/slos', nav: 'slos', sub: 'slos.list', title: 'nav.slos', load: page(() => import('./pages/slo.js')), view: 'list', time: false },
  { path: '/slos/new', nav: 'slos', sub: 'slos.new', title: 'nav.slos', load: page(() => import('./pages/slo.js')), view: 'form', time: false },
  { path: '/slos/:id', nav: 'slos', sub: 'slos.list', title: 'nav.slos', load: page(() => import('./pages/slo.js')), view: 'detail', time: false },
  { path: '/slos/:id/edit', nav: 'slos', sub: 'slos.list', title: 'nav.slos', load: page(() => import('./pages/slo.js')), view: 'form', time: false },
  { path: '/dashboards', nav: 'dashboards', title: 'nav.dashboards', load: page(() => import('./pages/dashboards.js')), view: 'list', time: false },
  { path: '/dashboards/:id', nav: 'dashboards', title: 'nav.dashboards', load: page(() => import('./pages/dashboards.js')), view: 'board' },
  { path: '/synthetics', nav: 'synthetics', title: 'nav.synthetics', load: page(() => import('./pages/synthetics.js')), view: 'list' },
  { path: '/synthetics/new', nav: 'synthetics', title: 'nav.synthetics', load: page(() => import('./pages/synthetics.js')), view: 'form', time: false },
  { path: '/synthetics/:id', nav: 'synthetics', title: 'nav.synthetics', load: page(() => import('./pages/synthetics.js')), view: 'detail' },
  { path: '/synthetics/:id/edit', nav: 'synthetics', title: 'nav.synthetics', load: page(() => import('./pages/synthetics.js')), view: 'form', time: false },
  { path: '/rum', nav: 'rum', title: 'nav.rum', load: page(() => import('./pages/rum.js')), view: 'list' },
  { path: '/rum/:id', nav: 'rum', title: 'nav.rum', load: page(() => import('./pages/rum.js')), view: 'app' },
  { path: '/settings/keys', nav: 'settings', sub: 'settings.keys', title: 'nav.settings', load: page(() => import('./pages/settings.js')), view: 'keys', time: false },
  { path: '/settings/install', nav: 'settings', sub: 'settings.install', title: 'nav.settings', load: page(() => import('./pages/settings.js')), view: 'install', time: false },
  { path: '/settings/members', nav: 'settings', sub: 'settings.members', title: 'nav.settings', load: page(() => import('./pages/settings.js')), view: 'members', time: false },
  { path: '/settings/channels', nav: 'settings', sub: 'settings.channels', title: 'nav.settings', load: page(() => import('./pages/settings.js')), view: 'channels', time: false },
  { path: '/settings/logs', nav: 'settings', sub: 'settings.logs', title: 'nav.settings', load: page(() => import('./pages/settings.js')), view: 'logs', time: false },
  { path: '/settings/devices', nav: 'settings', sub: 'settings.devices', title: 'nav.settings', load: page(() => import('./pages/settings.js')), view: 'devices', time: false },
  { path: '/settings/fleet', nav: 'settings', sub: 'settings.fleet', title: 'nav.settings', load: page(() => import('./pages/settings.js')), view: 'fleet', time: false },
  { path: '/settings/billing', nav: 'settings', sub: 'settings.billing', title: 'nav.settings', load: page(() => import('./pages/settings.js')), view: 'billing', time: false },
  { path: '/settings/usage', nav: 'settings', sub: 'settings.usage', title: 'nav.settings', load: page(() => import('./pages/settings.js')), view: 'usage', time: false },
  { path: '/settings/status-pages', nav: 'settings', sub: 'settings.status-pages', title: 'nav.settings', load: page(() => import('./pages/settings.js')), view: 'status-pages', time: false },
  { path: '/settings/reports', nav: 'settings', sub: 'settings.reports', title: 'nav.settings', load: page(() => import('./pages/settings.js')), view: 'reports', time: false },
  { path: '/settings/audit', nav: 'settings', sub: 'settings.audit', title: 'nav.settings', load: page(() => import('./pages/settings.js')), view: 'audit' },
  { path: '/settings/actions', nav: 'settings', sub: 'settings.actions', title: 'nav.settings', load: page(() => import('./pages/settings.js')), view: 'actions', time: false },
  { path: '/settings/profile', nav: 'settings', sub: 'settings.profile', title: 'nav.settings', load: page(() => import('./pages/settings.js')), view: 'profile', time: false },
];

const NAV = [
  { key: 'problems', icon: 'problem', path: '/problems' },
  { key: 'overview', icon: 'overview', path: '/overview' },
  { key: 'infra', icon: 'infra', path: '/infrastructure', sub: [['infra.list', '/infrastructure'], ['infra.map', '/infrastructure/map'], ['infra.inventory', '/inventory']] },
  { key: 'metrics', icon: 'metrics', path: '/metrics' },
  { key: 'logs', icon: 'logs', path: '/logs', sub: [['logs.search', '/logs'], ['logs.live', '/logs', { live: '1' }]] },
  { key: 'apm', icon: 'apm', path: '/apm', sub: [['apm.services', '/apm'], ['apm.traces', '/apm/traces']] },
  { key: 'services', icon: 'graph', path: '/services', sub: [['services.catalog', '/services'], ['services.map', '/services/map']] },
  { key: 'errors', icon: 'bug', path: '/errors' },
  { key: 'databases', icon: 'database', path: '/databases' },
  { key: 'processes', icon: 'process', path: '/processes' },
  { key: 'network', icon: 'network', path: '/network', sub: [['network.connections', '/network'], ['network.map', '/network/map']] },
  { key: 'containers', icon: 'container', path: '/containers' },
  { key: 'events', icon: 'events', path: '/events' },
  { key: 'monitors', icon: 'monitor', path: '/monitors', sub: [['monitors.list', '/monitors'], ['monitors.new', '/monitors/new']] },
  { key: 'downtimes', icon: 'wrench', path: '/downtimes' },
  { key: 'incidents', icon: 'siren', path: '/incidents' },
  { key: 'slos', icon: 'flag', path: '/slos', sub: [['slos.list', '/slos'], ['slos.new', '/slos/new']] },
  { key: 'dashboards', icon: 'dashboard', path: '/dashboards' },
  { key: 'synthetics', icon: 'synthetic', path: '/synthetics' },
  { key: 'certificates', icon: 'shield', path: '/certificates' },
  { key: 'rum', icon: 'rum', path: '/rum' },
  { key: 'settings', icon: 'settings', path: '/settings/keys', sub: [['settings.keys', '/settings/keys'], ['settings.install', '/settings/install'], ['settings.members', '/settings/members'], ['settings.channels', '/settings/channels'], ['settings.logs', '/settings/logs'], ['settings.devices', '/settings/devices'], ['settings.fleet', '/settings/fleet'], ['settings.billing', '/settings/billing'], ['settings.usage', '/settings/usage'], ['settings.status-pages', '/settings/status-pages'], ['settings.reports', '/settings/reports'], ['settings.audit', '/settings/audit'], ['settings.actions', '/settings/actions'], ['settings.profile', '/settings/profile']] },
];

function readPref(key, fallback) {
  try { const v = localStorage.getItem(key); return v === null ? fallback : v; } catch (e) { return fallback; }
}
function writePref(key, v) {
  try { localStorage.setItem(key, v); } catch (e) { /* préférence non mémorisée */ }
}

export function getTheme() { return document.documentElement.getAttribute('data-theme') === 'light' ? 'light' : 'dark'; }
export function setTheme(th) {
  document.documentElement.setAttribute('data-theme', th === 'light' ? 'light' : 'dark');
  writePref('hypha.theme', th === 'light' ? 'light' : 'dark');
}

function initials(user) {
  const src = (user && (user.name || user.email)) || '?';
  const parts = String(src).replace(/@.*/, '').split(/[\s._-]+/).filter(Boolean);
  return ((parts[0] || '?')[0] + (parts[1] ? parts[1][0] : '')).toUpperCase();
}

export function presetLabel(key) { return t('time.preset.' + key); }

export function rangeLabel(range) {
  if (range.preset) return presetLabel(range.preset);
  return fmtDateTime(range.from) + ' → ' + fmtDateTime(range.to);
}

export function startApp(root, me, hooks) {
  let current = null; // {cleanups, ctrl}
  let refreshTimer = null;
  const app = h('div', { class: ['app', readPref('hypha.nav', '') === 'collapsed' ? 'nav-collapsed' : null] });
  const sidebar = h('aside', { class: 'sidebar' });
  const topbar = h('header', { class: 'topbar' });
  const content = h('main', { class: 'content', id: 'page', tabindex: '-1' });
  app.appendChild(sidebar);
  app.appendChild(h('div', { class: 'main' }, topbar, content));
  mount(root, app);
  root.classList.remove('boot');

  function renderSidebar(match) {
    clear(sidebar);
    sidebar.appendChild(h('a', { class: 'brand', href: href('/overview') },
      h('img', { src: 'assets/img/hypha.svg', alt: '' }),
      h('span', { class: 'brand-name' }, 'HYPHA', h('small', t('brand.by')))));
    const nav = h('nav', { class: 'nav', 'aria-label': t('nav.aria') });
    const cur = parseHash();
    for (const item of NAV) {
      const active = match && match.route.nav === item.key;
      nav.appendChild(h('a', { class: ['nav-item', active ? 'active' : null], href: href(item.path), title: t('nav.' + item.key) },
        icon(item.icon, 16), h('span', { class: 'nav-label' }, t('nav.' + item.key)), item.key === 'problems' ? problemBadge() : null));
      if (item.sub) {
        const sub = h('div', { class: ['nav-sub', active ? 'open' : null] });
        for (const [key, p, extra] of item.sub) {
          if (key === 'settings.billing' && me.mode !== 'saas') continue;
          let on;
          if (key === 'logs.live') on = active && cur.params.get('live') === '1';
          else if (key === 'logs.search') on = active && cur.params.get('live') !== '1';
          else on = active && match.route.sub === key;
          sub.appendChild(h('a', { class: on ? 'active' : null, href: href(p, extra || null) }, t('nav.' + key)));
        }
        if (active) nav.appendChild(sub);
      }
    }
    sidebar.appendChild(nav);
    const collapsed = app.classList.contains('nav-collapsed');
    sidebar.appendChild(h('div', { class: 'nav-foot' },
      h('button', {
        class: 'btn ghost sm', type: 'button', title: t(collapsed ? 'nav.expand' : 'nav.collapse'),
        onclick: () => {
          app.classList.toggle('nav-collapsed');
          writePref('hypha.nav', app.classList.contains('nav-collapsed') ? 'collapsed' : '');
          renderSidebar(match);
        },
      }, icon(collapsed ? 'expand' : 'collapse', 14), h('span', { class: 'nav-label' }, t(collapsed ? 'nav.expand' : 'nav.collapse')))));
  }

  const crumbsEl = h('div', { class: 'crumbs grow' });
  const toolsEl = h('div', { class: 'row' });

  function setCrumbs(items) {
    clear(crumbsEl);
    items.forEach((it, i) => {
      if (i > 0) crumbsEl.appendChild(h('span', { class: 'sep' }, '/'));
      if (it.href && i < items.length - 1) crumbsEl.appendChild(h('a', { href: it.href }, it.label));
      else crumbsEl.appendChild(h('span', { class: 'cur', title: it.label }, it.label));
    });
    document.title = (items.length ? items[items.length - 1].label + ' · ' : '') + 'HYPHA';
  }

  function timePicker(range, enabled) {
    const btn = h('button', { class: 'btn', type: 'button', disabled: !enabled, title: enabled ? t('time.pick') : t('time.not_used') },
      icon('clock', 14), h('span', rangeLabel(range)), icon('chevronDown', 12));
    const dd = dropdown(btn, (close) => {
      const box = h('div');
      box.appendChild(h('div', { class: 'menu-head' }, t('time.relative')));
      for (const p of PRESETS) {
        box.appendChild(menuItem(presetLabel(p.key), () => { close(); setParams(rangeParams({ preset: p.key })); }, { selected: range.preset === p.key }));
      }
      const fromIn = h('input', { class: 'input sm', type: 'datetime-local', value: toLocalInput(range.from) });
      const toIn = h('input', { class: 'input sm', type: 'datetime-local', value: toLocalInput(range.to) });
      const err = h('div', { class: 'xsmall', style: { color: 'var(--crit)' }, hidden: true });
      box.appendChild(h('div', { class: 'custom' },
        h('div', { class: 'menu-head', style: { padding: '0' } }, t('time.custom')),
        h('label', { class: 'field' }, h('span', { class: 'lbl' }, t('time.from')), fromIn),
        h('label', { class: 'field' }, h('span', { class: 'lbl' }, t('time.to')), toIn),
        err,
        h('button', {
          class: 'btn primary sm', type: 'button',
          onclick: () => {
            const a = fromLocalInput(fromIn.value);
            const b = fromLocalInput(toIn.value);
            if (!a || !b || b <= a) { err.textContent = t('time.invalid'); err.hidden = false; return; }
            close();
            setParams(rangeParams({ preset: null, from: a, to: b }));
          },
        }, t('action.apply'))));
      return box;
    });
    return h('div', { class: 'timepicker' }, dd);
  }

  function refreshSelect() {
    const val = readPref('hypha.refresh', '0');
    const sel = h('select', { class: 'input sm', title: t('refresh.auto'), style: { width: 'auto' } },
      [['0', t('refresh.off')], ['30', '30 s'], ['60', '1 min'], ['300', '5 min']].map(([v, l]) => h('option', { value: v, selected: v === val }, l)));
    sel.addEventListener('change', () => { writePref('hypha.refresh', sel.value); armRefresh(); });
    return sel;
  }

  function armRefresh() {
    if (refreshTimer) { clearInterval(refreshTimer); refreshTimer = null; }
    const sec = Number(readPref('hypha.refresh', '0'));
    if (sec > 0) refreshTimer = setInterval(() => {
      const r = rangeFromParams(parseHash().params);
      // Jamais pendant une saisie, une édition ou le direct : la page porte data-no-autorefresh.
      if (r.live && document.visibilityState === 'visible' && !document.querySelector('.modal-back, .drawer, [data-no-autorefresh]')) route({ keepScroll: true });
    }, sec * 1000);
  }

  function orgControl() {
    const orgs = Array.isArray(me.orgs) ? me.orgs : [];
    const curOrg = me.org || {};
    if (orgs.length < 2) return h('span', { class: 'tag', title: t('org.current') }, curOrg.name || curOrg.slug || '');
    const btn = h('button', { class: 'btn ghost', type: 'button', title: t('org.switch') }, icon('layers', 14), h('span', curOrg.name || curOrg.slug || ''), icon('chevronDown', 12));
    return dropdown(btn, (close) => h('div',
      h('div', { class: 'menu-head' }, t('org.switch')),
      orgs.map((o) => menuItem(o.name || o.slug, async () => {
        close();
        if (o.id === curOrg.id) return;
        try {
          await post('/orgs/switch', { org_id: o.id });
          location.hash = '#/overview';
          location.reload();
        } catch (e) { toast(t('org.switch_failed'), 'error'); }
      }, { selected: o.id === curOrg.id }))));
  }

  function userControl() {
    const user = me.user || {};
    const btn = h('button', { class: 'btn ghost', type: 'button', title: user.email || '' }, h('span', { class: 'user-avatar' }, initials(user)));
    return dropdown(btn, (close) => h('div',
      h('div', { class: 'menu-head' }, h('div', { style: { color: 'var(--text)', fontWeight: '600' } }, user.name || user.email || ''), user.name && user.email ? h('div', user.email) : null,
        me.role ? h('div', t('role.' + me.role)) : null),
      h('div', { class: 'menu-sep' }),
      menuItem(t('nav.settings.profile'), () => { close(); navigate('/settings/profile'); }, { icon: 'user' }),
      menuItem(getTheme() === 'dark' ? t('theme.to_light') : t('theme.to_dark'), () => { close(); setTheme(getTheme() === 'dark' ? 'light' : 'dark'); route({ keepScroll: true }); }, { icon: getTheme() === 'dark' ? 'sun' : 'moon' }),
      menuItem(t('help.docs'), () => { close(); window.open(docsUrl(), '_blank', 'noopener'); }, { icon: 'external' }),
      h('div', { class: 'menu-sep' }),
      menuItem(t('auth.logout'), async () => { close(); await logout(); }, { icon: 'logout' })));
  }

  // Documentation publique (site/build.py), dans la langue de l'interface.
  function docsUrl() { return getLang() === 'en' ? '/en/docs/' : '/docs/'; }

  async function logout() {
    try { await post('/api/auth/deconnexion', {}, { quiet401: true }); } catch (e) { /* session déjà close */ }
    teardown();
    if (hooks && hooks.onLogout) hooks.onLogout();
  }

  function renderTopbar(match, range) {
    clear(topbar);
    clear(toolsEl);
    const usesTime = !match || match.route.time !== false;
    topbar.appendChild(crumbsEl);
    toolsEl.appendChild(timePicker(range, usesTime));
    toolsEl.appendChild(h('button', { class: 'btn icon', type: 'button', title: t('action.refresh'), onclick: () => route({ keepScroll: true }) }, icon('refresh', 14)));
    toolsEl.appendChild(refreshSelect());
    topbar.appendChild(toolsEl);
    topbar.appendChild(orgControl());
    topbar.appendChild(h('button', {
      class: 'btn ghost icon', type: 'button', title: getTheme() === 'dark' ? t('theme.to_light') : t('theme.to_dark'),
      onclick: () => { setTheme(getTheme() === 'dark' ? 'light' : 'dark'); route({ keepScroll: true }); },
    }, icon(getTheme() === 'dark' ? 'sun' : 'moon', 15)));
    topbar.appendChild(h('a', { class: 'btn ghost icon', href: docsUrl(), target: '_blank', rel: 'noopener', title: t('help.docs_title'), 'aria-label': t('help.docs') }, icon('info', 15)));
    topbar.appendChild(userControl());
  }

  function cleanupPage() {
    if (!current) return;
    current.ctrl.abort();
    for (const fn of current.cleanups) { try { fn(); } catch (e) { /* nettoyage au mieux */ } }
    destroyAllCharts();
    document.querySelectorAll('.map-tip, .drawer, .drawer-back, .modal-back').forEach((el) => el.remove());
    current = null;
  }

  let seq = 0;
  async function route(opts = {}) {
    const loc = parseHash();
    if (loc.segs.length === 0) { navigate('/overview', null, { replace: true }); return; }
    const match = matchRoute(ROUTES, loc.segs);
    const scrollTop = opts.keepScroll ? content.scrollTop : 0;
    cleanupPage();
    const my = ++seq;
    const range = rangeFromParams(loc.params);
    renderSidebar(match);
    renderTopbar(match, range);
    clear(content);
    if (!match) {
      setCrumbs([{ label: t('err.404') }]);
      content.appendChild(h('div', { class: 'state' }, h('h3', t('err.page_missing')), h('div', { class: 'actions' }, h('a', { class: 'btn', href: href('/overview') }, t('nav.overview')))));
      return;
    }
    const ctrl = new AbortController();
    current = { ctrl, cleanups: [] };
    const ctx = {
      me,
      args: match.args,
      route: match.route,
      view: match.route.view || null,
      params: loc.params,
      range,
      signal: ctrl.signal,
      lang: getLang(),
      setCrumbs,
      setParams,
      navigate,
      href,
      refresh: () => route({ keepScroll: true }),
      onCleanup: (fn) => { if (current && current.ctrl === ctrl) current.cleanups.push(fn); },
      onRange: (from, to) => setParams(rangeParams({ preset: null, from, to })),
      isCurrent: () => my === seq,
    };
    setCrumbs([{ label: t(match.route.title) }]);
    content.appendChild(loading());
    try {
      const mod = await match.route.load();
      if (my !== seq) return;
      clear(content);
      const view = h('div', { class: 'view' });
      planBanner(content, match);
      content.appendChild(view);
      const ret = await mod.render(view, ctx);
      if (typeof ret === 'function') ctx.onCleanup(ret);
      if (opts.keepScroll && my === seq) content.scrollTop = scrollTop;
    } catch (e) {
      if (isAbort(e) || my !== seq) return;
      clear(content);
      console.error(e);
      content.appendChild(errorState(e, () => route()));
    }
  }

  // Limite de formule atteinte (§ 52) : bandeau sur toutes les pages, rien n'est refusé en silence.
  // /billing relu au plus toutes les 5 minutes ; jamais pour l'espace interne ni en auto-hébergé.
  let alertsAt = 0;
  let alerts = [];
  function planBanner(container, match) {
    if (me.mode !== 'saas' || !me.plan || me.plan === 'interne') return;
    if (match && match.route.view === 'billing') return;
    const slot = h('div', { class: 'col gap-2 mb-3' });
    container.appendChild(slot);
    const paint = () => {
      if (!alerts.length) { slot.remove(); return; }
      import('./pages/settings-billing.js').then((m) => { alerts.forEach((a) => slot.appendChild(m.alertCallout(a))); });
    };
    if (Date.now() - alertsAt < 300000) { paint(); return; }
    get('/billing').then((b) => { alerts = (b && b.alerts) || []; alertsAt = Date.now(); paint(); }).catch(() => slot.remove());
  }

  function teardown() {
    cleanupPage();
    stopCounter();
    offs.forEach((fn) => fn());
    if (refreshTimer) clearInterval(refreshTimer);
  }

  const offs = [
    on(window, 'hashchange', () => route()),
    on(document, 'keydown', (e) => {
      if (e.key !== '/' || e.ctrlKey || e.metaKey || e.altKey) return;
      const tag = (e.target && e.target.tagName) || '';
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || (e.target && e.target.isContentEditable)) return;
      const target = content.querySelector('[data-search]');
      if (target) { e.preventDefault(); target.focus(); if (target.select) target.select(); }
    }),
  ];
  armRefresh();
  const stopCounter = startProblemCounter();
  route();
  return { teardown, refresh: () => route({ keepScroll: true }) };
}
