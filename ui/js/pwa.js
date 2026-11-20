// Application installable et notifications push : service worker, installation, abonnement de cet appareil.
// Le service worker n'est enregistré qu'une fois connecté (les visiteurs de l'accueil n'en ont pas).
import { get, post, del } from './api.js';
import { getLang } from './i18n.js';

let registration = null;
let installEvent = null;
const installListeners = new Set();

// Chrome, Edge, Samsung Internet : l'invite d'installation se garde pour un bouton « Installer ».
window.addEventListener('beforeinstallprompt', (e) => {
  e.preventDefault();
  installEvent = e;
  installListeners.forEach((fn) => { try { fn(); } catch (err) { /* rien */ } });
});
window.addEventListener('appinstalled', () => { installEvent = null; installListeners.forEach((fn) => { try { fn(); } catch (err) { /* rien */ } }); });

export function swSupported() { return 'serviceWorker' in navigator && window.isSecureContext; }
export function pushSupported() { return swSupported() && 'PushManager' in window && 'Notification' in window; }
export function isStandalone() {
  return (window.matchMedia && window.matchMedia('(display-mode: standalone)').matches) || navigator.standalone === true;
}
export function isIOS() {
  return /iPad|iPhone|iPod/.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
}
export function canPromptInstall() { return !!installEvent; }
export function onInstallChange(fn) { installListeners.add(fn); return () => installListeners.delete(fn); }

export async function promptInstall() {
  if (!installEvent) return false;
  const ev = installEvent;
  installEvent = null;
  ev.prompt();
  const choice = await ev.userChoice.catch(() => null);
  return !!(choice && choice.outcome === 'accepted');
}

export async function registerServiceWorker() {
  if (!swSupported()) return null;
  try {
    registration = await navigator.serviceWorker.register('/sw.js', { scope: '/', updateViaCache: 'none' });
  } catch (e) {
    console.warn('service worker non enregistré', e);
    registration = null;
  }
  return registration;
}

async function reg() {
  if (registration) return registration;
  if (!swSupported()) return null;
  return registerServiceWorker();
}

function keyBytes(b64) {
  const s = String(b64 || '').replace(/-/g, '+').replace(/_/g, '/');
  const raw = atob(s + '='.repeat((4 - (s.length % 4)) % 4));
  return Uint8Array.from(raw, (c) => c.charCodeAt(0));
}

function sameKey(sub, publicKey) {
  const cur = sub && sub.options && sub.options.applicationServerKey;
  if (!cur) return true;
  const a = new Uint8Array(cur);
  const b = keyBytes(publicKey);
  return a.length === b.length && a.every((x, i) => x === b[i]);
}

export async function currentSubscription() {
  if (!pushSupported()) return null;
  const r = await reg();
  if (!r) return null;
  try { return await r.pushManager.getSubscription(); } catch (e) { return null; }
}

// Nom lisible de l'appareil, proposé à l'abonnement (« Chrome · Android »).
export function deviceLabel() {
  const ua = navigator.userAgent || '';
  const browser = /SamsungBrowser/.test(ua) ? 'Samsung Internet' : /Edg\//.test(ua) ? 'Edge' : /OPR\//.test(ua) ? 'Opera'
    : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'Navigateur';
  const os = /Android/.test(ua) ? 'Android' : isIOS() ? (/iPad/.test(ua) ? 'iPad' : 'iPhone') : /Windows/.test(ua) ? 'Windows'
    : /Mac OS X/.test(ua) ? 'macOS' : /Linux/.test(ua) ? 'Linux' : '';
  return [browser, os].filter(Boolean).join(' · ') + (isStandalone() ? ' (app)' : '');
}

// À appeler directement depuis le clic (geste explicite) : Safari refuse la demande d'autorisation
// si un autre appel réseau l'a précédée dans le même geste.
export async function subscribeDevice(publicKey) {
  const perm = await Notification.requestPermission();
  if (perm !== 'granted') throw Object.assign(new Error('permission ' + perm), { code: 'permission_' + perm });
  const r = await reg();
  if (!r) throw Object.assign(new Error('service worker'), { code: 'no_sw' });
  // Service worker actif avant l'abonnement ; jamais d'attente sans fin s'il ne s'installe pas.
  await Promise.race([navigator.serviceWorker.ready,
    new Promise((resolve, reject) => setTimeout(() => reject(Object.assign(new Error('service worker inactif'), { code: 'no_sw' })), 15000))]);
  let sub = await r.pushManager.getSubscription();
  if (sub && !sameKey(sub, publicKey)) { await sub.unsubscribe().catch(() => {}); sub = null; }
  if (!sub) sub = await r.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(publicKey) });
  const j = sub.toJSON();
  return post('/push/subscriptions', { endpoint: j.endpoint, keys: j.keys, label: deviceLabel(), lang: getLang() === 'en' ? 'en' : 'fr' });
}

// Désabonne cet appareil (serveur puis navigateur). `devices` : liste déjà lue, sinon relue.
export async function unsubscribeDevice(devices) {
  const sub = await currentSubscription();
  if (!sub) return false;
  const list = devices || (await get('/push/subscriptions').catch(() => []));
  const mine = (Array.isArray(list) ? list : []).find((d) => d.endpoint === sub.endpoint);
  if (mine) await del('/push/subscriptions/' + mine.id).catch(() => {});
  await sub.unsubscribe().catch(() => {});
  return true;
}
