// Service worker de HYPHA : application installable, page hors ligne, notifications push.
//
// Cache : SEULEMENT les fichiers statiques de l'interface (/js/, /assets/, /vendor/, le manifeste et la
// page hors ligne), jamais une réponse de l'API ni une page publique (documentation, pages de statut,
// tableaux partagés : le réseau seul, comme sans service worker). Réseau d'abord : en ligne, l'interface
// servie est toujours celle du serveur ; le cache ne sert que hors ligne. VERSION est posée à chaque
// déploiement (empreinte de ui/, deploy/estampiller-sw.sh) : le navigateur installe alors ce fichier
// neuf, qui efface les caches des versions précédentes.
'use strict';

const VERSION = '__HYPHA_UI_VERSION__';
const CACHE = 'hypha-ui-' + VERSION;
const PRECACHE = ['/offline.html', '/assets/img/icon-192.png', '/assets/img/badge-96.png'];
const STATIC = /^\/(js|assets|vendor)\//;
const SHELL = ['/', '/index.html'];
const OWN = ['/manifest.webmanifest', '/offline.html'];

const TEXT = {
  fr: { ack: 'Acquitter', open: 'Ouvrir', acked: 'Acquitté', ack_failed: 'Acquittement impossible',
        login: 'Connexion requise : ouvrez HYPHA, reconnectez-vous, puis acquittez.', ok_now: 'Déjà rétabli : rien à acquitter.',
        offline: 'Hors ligne : acquittement non envoyé.' },
  en: { ack: 'Acknowledge', open: 'Open', acked: 'Acknowledged', ack_failed: 'Could not acknowledge',
        login: 'Sign-in required: open HYPHA, sign in again, then acknowledge.', ok_now: 'Already recovered: nothing to acknowledge.',
        offline: 'Offline: acknowledgement not sent.' },
};

function tr(lang) { return TEXT[lang] || TEXT.fr; }

self.addEventListener('install', (event) => {
  event.waitUntil(caches.open(CACHE)
    .then((c) => c.addAll(PRECACHE.map((u) => new Request(u, { cache: 'reload' }))))
    .then(() => self.skipWaiting()));
});

self.addEventListener('activate', (event) => {
  event.waitUntil(caches.keys()
    .then((keys) => Promise.all(keys.filter((k) => k.startsWith('hypha-ui-') && k !== CACHE).map((k) => caches.delete(k))))
    .then(() => self.clients.claim()));
});

async function networkFirst(request) {
  const cache = await caches.open(CACHE);
  try {
    const res = await fetch(request);
    if (res.ok && res.type === 'basic') cache.put(request, res.clone()).catch(() => {});
    return res;
  } catch (err) {
    const hit = await cache.match(request);
    if (hit) return hit;
    throw err;
  }
}

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  const path = url.pathname;
  if (path.startsWith('/api/') || path.startsWith('/v1/')) return;          // jamais l'API
  if (req.mode === 'navigate') {
    if (!SHELL.includes(path)) return;                                       // pages publiques : réseau seul
    event.respondWith(fetch(req).catch(() => caches.match('/offline.html')));
    return;
  }
  if (url.search) return;                                                    // rien de paramétré
  if (STATIC.test(path) || OWN.includes(path)) event.respondWith(networkFirst(req));
});

// ── Notifications push ─────────────────────────────────────────────────────────
self.addEventListener('push', (event) => {
  let d = {};
  try { d = event.data ? event.data.json() : {}; } catch (e) { d = { body: event.data ? event.data.text() : '' }; }
  const T = tr(d.lang);
  const options = {
    body: d.body || '',
    icon: '/assets/img/icon-192.png',
    badge: '/assets/img/badge-96.png',
    timestamp: d.ts || Date.now(),
    data: { url: d.url || '/#/problems', org_id: d.org_id, monitor_id: d.monitor_id, group: d.group, lang: d.lang,
            title: d.title || 'HYPHA', tag: d.tag || '' },
  };
  if (d.tag) {
    options.tag = d.tag;
    options.renotify = d.state !== 'OK';                 // une relance refait sonner, un retour à OK non
  }
  if (d.state === 'ALERT' && d.priority && d.priority <= 2) options.requireInteraction = true;
  if (d.can_ack && d.monitor_id) options.actions = [{ action: 'ack', title: T.ack }, { action: 'open', title: T.open }];
  event.waitUntil(self.registration.showNotification(d.title || 'HYPHA', options));
});

function sameOrigin(raw) {
  try {
    const u = new URL(raw || '/#/problems', self.location.origin);
    if (u.origin === self.location.origin) return u.href;
  } catch (e) { /* lien illisible */ }
  return self.location.origin + '/#/problems';
}

async function openApp(raw) {
  const url = sameOrigin(raw);
  const wins = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
  for (const c of wins) {
    const p = new URL(c.url);
    // Une fenêtre de l'application (pas un onglet de la documentation) : on y suit le lien.
    if (p.origin === self.location.origin && SHELL.includes(p.pathname) && 'focus' in c) {
      c.postMessage({ type: 'hypha:open', url });
      return c.focus();
    }
  }
  return self.clients.openWindow(url);
}

async function note(d, body) {
  return self.registration.showNotification(d.title || 'HYPHA', {
    body, icon: '/assets/img/icon-192.png', badge: '/assets/img/badge-96.png', silent: true,
    tag: d.tag || undefined, data: { url: d.url, lang: d.lang, title: d.title, tag: d.tag },
  });
}

// « Acquitter » : l'API avec le cookie de session du navigateur ; aucun jeton dans la notification.
async function ack(d) {
  const T = tr(d.lang);
  let res;
  try {
    res = await fetch('/api/v1/push/ack', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ org_id: d.org_id, monitor_id: d.monitor_id, group: d.group }),
    });
  } catch (e) {
    return note(d, T.offline);
  }
  if (res.ok) return note(d, T.acked);
  if (res.status === 401) return note(d, T.login);
  if (res.status === 409) return note(d, T.ok_now);
  let detail = '';
  try { detail = (await res.json()).detail || ''; } catch (e) { /* corps illisible */ }
  return note(d, T.ack_failed + (detail ? ' : ' + detail : ''));
}

// Abonnement renouvelé par le navigateur (rare) : le nouveau est déclaré avec la session en cours ;
// l'ancien sera retiré par le serveur à son premier envoi refusé (410).
self.addEventListener('pushsubscriptionchange', (event) => {
  event.waitUntil((async () => {
    const old = event.oldSubscription;
    const key = old && old.options && old.options.applicationServerKey;
    const sub = event.newSubscription || (key ? await self.registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key }) : null);
    if (!sub) return;
    const j = sub.toJSON();
    const lang = String(self.navigator.language || '').startsWith('en') ? 'en' : 'fr';
    await fetch('/api/v1/push/subscriptions', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ endpoint: j.endpoint, keys: j.keys, label: lang === 'en' ? 'Renewed device' : 'Appareil renouvelé', lang }),
    }).catch(() => {});
  })());
});

self.addEventListener('notificationclick', (event) => {
  const d = event.notification.data || {};
  event.notification.close();
  if (event.action === 'ack') event.waitUntil(ack(d));
  else event.waitUntil(openApp(d.url));
});
