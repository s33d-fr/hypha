// Aides des pages de la 0.4.0 : rôles, route absente, tuiles, export CSV, versions.
import { h, clear } from '../dom.js';
import { t } from '../i18n.js';
import { isAbort } from '../api.js';
import { errorState, loading } from '../components/ui.js';
import { isNum } from '../format.js';

export const canWrite = (me) => !!me && ['owner', 'admin', 'member'].includes(me.role);
export const isAdmin = (me) => !!me && ['owner', 'admin'].includes(me.role);

// Route inconnue du serveur (serveur pas encore à jour) : 405, ou 404 sans code d'erreur HYPHA.
export function isMissingRoute(e) {
  return !!e && (e.status === 405 || e.status === 501 || (e.status === 404 && !e.code));
}

export function routeErrorState(e, retry) {
  if (isMissingRoute(e)) {
    return h('div', { class: 'state' }, h('h3', t('kit.route_missing_title')), h('div', { class: 'small' }, t('kit.route_missing_text')));
  }
  return errorState(e, retry);
}

// Comme section() : attente, erreur lisible (route absente comprise), rendu.
export async function load(container, loader, renderFn, opts = {}) {
  clear(container);
  container.appendChild(opts.loadingNode || loading());
  try {
    const data = await loader();
    clear(container);
    const out = renderFn(data);
    if (out) container.appendChild(out);
    return data;
  } catch (e) {
    if (isAbort(e)) return undefined;
    clear(container);
    container.appendChild(routeErrorState(e, opts.retry === false ? null : () => load(container, loader, renderFn, opts)));
    if (opts.onError) opts.onError(e);
    return undefined;
  }
}

export function tile(label, value, sub, color) {
  return h('div', { class: 'tile' }, h('div', { class: 't-label' }, label),
    h('div', { class: 't-value num', style: { fontSize: 'var(--fs-xl)', color: color || null } }, value),
    sub ? h('div', { class: 't-sub' }, sub) : null);
}

// Fichier téléchargé depuis le navigateur (rien ne passe par le serveur).
export function download(filename, content, mime) {
  const blob = content instanceof Blob ? content : new Blob([content], { type: mime || 'text/plain;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = h('a', { href: url, download: filename, style: { display: 'none' } });
  document.body.appendChild(a);
  a.click();
  setTimeout(() => { URL.revokeObjectURL(url); a.remove(); }, 1000);
}

// CSV (séparateur « ; » en français pour les tableurs, « , » sinon) ; cellules protégées contre les formules.
export function toCsv(columns, rows, sep) {
  const delim = sep || ';';
  const cell = (v) => {
    let str = v === null || v === undefined ? '' : typeof v === 'object' ? JSON.stringify(v) : String(v);
    if (/^[=+\-@\t\r]/.test(str)) str = "'" + str;
    return /["\n\r;,]/.test(str) ? '"' + str.replace(/"/g, '""') + '"' : str;
  };
  const lines = [columns.map((c) => cell(c.label)).join(delim)];
  for (const r of rows) lines.push(columns.map((c) => cell(c.value(r))).join(delim));
  return '﻿' + lines.join('\r\n') + '\r\n';
}

export function stamp() {
  const d = new Date();
  const p = (n) => String(n).padStart(2, '0');
  return d.getFullYear() + p(d.getMonth() + 1) + p(d.getDate()) + '-' + p(d.getHours()) + p(d.getMinutes());
}

// Compare deux versions « 0.4.0 », « v0.3.12-rc1 » : négatif si a < b.
export function cmpVersion(a, b) {
  const pa = String(a || '').replace(/^v/i, '').split(/[.+-]/);
  const pb = String(b || '').replace(/^v/i, '').split(/[.+-]/);
  for (let i = 0; i < Math.max(pa.length, pb.length, 3); i++) {
    const x = pa[i] === undefined ? 0 : pa[i];
    const y = pb[i] === undefined ? 0 : pb[i];
    const nx = /^\d+$/.test(x) ? Number(x) : NaN;
    const ny = /^\d+$/.test(y) ? Number(y) : NaN;
    if (Number.isFinite(nx) && Number.isFinite(ny)) { if (nx !== ny) return nx - ny; } else if (String(x) !== String(y)) return String(x) < String(y) ? -1 : 1;
  }
  return 0;
}

// Écart d'une version à la version publiée : « ok », « patch », « minor », « major », ou null.
export function versionLag(v, latest) {
  if (!v || !latest) return null;
  if (cmpVersion(v, latest) >= 0) return 'ok';
  const a = String(v).replace(/^v/i, '').split('.').map((x) => parseInt(x, 10) || 0);
  const b = String(latest).replace(/^v/i, '').split('.').map((x) => parseInt(x, 10) || 0);
  if (a[0] !== b[0]) return 'major';
  if (a[1] !== b[1]) return 'minor';
  return 'patch';
}

export function lagColor(lag) {
  return lag === 'ok' ? 'var(--ok)' : lag === 'patch' ? 'var(--text-2)' : lag === 'minor' ? 'var(--warn)' : lag === 'major' ? 'var(--crit)' : null;
}

// Points [[ts, v], …] ou [{ts|hour|t, v|count|value}] vers [[ms, v]].
export function toPoints(list) {
  if (!Array.isArray(list)) return [];
  return list.map((p) => {
    if (Array.isArray(p)) return [Number(p[0]) || Date.parse(p[0]) || 0, isNum(p[1]) ? p[1] : Number(p[1])];
    if (p && typeof p === 'object') {
      const ts = p.ts !== undefined ? p.ts : p.hour !== undefined ? p.hour : p.t !== undefined ? p.t : p.time;
      const v = p.v !== undefined ? p.v : p.count !== undefined ? p.count : p.value;
      const ms = typeof ts === 'number' ? ts : Date.parse(ts);
      return [Number.isFinite(ms) ? ms : 0, isNum(v) ? v : Number(v)];
    }
    return null;
  }).filter((p) => p && p[0]).sort((a, b) => a[0] - b[0]);
}

// Ligne « k:v » vers [k, v].
export function splitTag(str) {
  const i = String(str || '').indexOf(':');
  return i > 0 ? [str.slice(0, i), str.slice(i + 1)] : null;
}

export function matchesTagFilters(tags, filters) {
  return (filters || []).every(([k, v]) => tags && String(tags[k]) === String(v));
}

// Valeur numérique si l'identifiant n'est fait que de chiffres.
export function numId(v) {
  return typeof v === 'string' && /^\d+$/.test(v) && Number.isSafeInteger(Number(v)) ? Number(v) : v;
}
