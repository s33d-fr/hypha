// Démarrage : GET /api/v1/me décide entre l'accueil public (401) et l'application.
import { h, mount } from './dom.js';
import { t } from './i18n.js';
import { get, post, onUnauthorized } from './api.js';
import { errorState } from './components/ui.js';
import { startApp } from './app.js';
import { renderPublic } from './pages/public.js';
import { parseHash, buildHash } from './router.js';
import { registerServiceWorker } from './pwa.js';

// Lien venu d'un courriel ou d'une notification (`/monitors/6`) : la route passe dans le hash.
if (location.pathname !== '/' && location.pathname !== '/index.html' && !location.hash) {
  history.replaceState(null, '', '/#' + location.pathname + location.search);
}

const root = document.getElementById('root');
let app = null;

function showPublic(info) {
  if (app) { app.teardown(); app = null; }
  root.classList.remove('boot');
  renderPublic(root, Object.assign({ onLoggedIn: boot }, info || {}));
}

async function boot() {
  try {
    let me = await get('/me', null, { quiet401: true });
    if (!me || typeof me !== 'object' || !me.user) throw Object.assign(new Error('bad /me'), { status: 401, body: me });
    me = await followOrg(me);
    registerServiceWorker();
    if (app) app.teardown();
    app = startApp(root, me, { onLogout: () => showPublic({ mode: me.mode }) });
  } catch (e) {
    if (e && e.status === 401) {
      showPublic({ mode: e.body && typeof e.body === 'object' ? e.body.mode : undefined });
      return;
    }
    root.classList.remove('boot');
    mount(root, h('div', { class: 'public' },
      h('div', { class: 'public-top' }, h('span', { class: 'brand' }, h('img', { src: 'assets/img/hypha.svg', alt: '' }), h('span', { class: 'brand-name' }, 'HYPHA'))),
      h('div', { style: { maxWidth: '520px', margin: '10vh auto', width: '100%' } }, errorState(e, boot))));
  }
}

// Lien d'une notification (`…&org=<espace>`) : on passe dans l'espace du problème avant d'afficher la page,
// puis le paramètre quitte l'adresse. Un espace dont on n'est pas membre est ignoré.
async function followOrg(me) {
  const loc = parseHash();
  const want = Number(loc.params.get('org'));
  if (!loc.params.has('org')) return me;
  loc.params.delete('org');
  history.replaceState(null, '', buildHash(loc.path, loc.params));
  if (!want || !me.org || me.org.id === want || !(me.orgs || []).some((o) => o.id === want)) return me;
  try {
    const next = await post('/orgs/switch', { org_id: want });
    return next && next.user ? next : me;
  } catch (e) {
    return me;
  }
}

// Clic sur une notification alors que l'application est ouverte : le service worker passe le lien.
if ('serviceWorker' in navigator) {
  navigator.serviceWorker.addEventListener('message', (e) => {
    const d = e.data || {};
    if (d.type !== 'hypha:open' || typeof d.url !== 'string') return;
    let u;
    try { u = new URL(d.url, location.origin); } catch (err) { return; }
    if (u.origin !== location.origin) return;
    history.pushState(null, '', u.pathname + u.search + u.hash);
    boot();
  });
}

onUnauthorized(() => {
  if (app) showPublic({ expired: true });
});

document.title = 'HYPHA · ' + t('public.title_suffix');
boot();
