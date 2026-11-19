// Briques d'interface partagées : états, fenêtres, tiroir, menus, onglets, copie, notifications.
import { h, icon, clear, on } from '../dom.js';
import { t } from '../i18n.js';
import { isAbort } from '../api.js';

export function loading(label) {
  return h('div', { class: 'state sm' }, h('span', { class: 'spinner' }), label ? h('div', { class: 'mt-2 small' }, label) : null);
}

export function empty(title, text, actions) {
  return h('div', { class: 'state' },
    title ? h('h3', title) : null,
    text ? h('div', { class: 'small' }, text) : null,
    actions ? h('div', { class: 'actions' }, actions) : null);
}

export function errorMessage(err) {
  if (!err) return t('err.unknown');
  if (err.status === 0) return t('err.network');
  if (err.status === 401) return t('err.401');
  if (err.status === 403) return t('err.403');
  if (err.status === 404) return t('err.404');
  if (err.status === 413) return t('err.413');
  // Limite de formule (§ 52) : message traduit, la limite en paramètre.
  if (err.status === 402 && err.code === 'plan_limit' && err.body && err.body.quota) {
    const key = 'billing.limit.' + err.body.quota;
    const msg = t(key, { limit: err.body.limit });
    return msg === key ? (err.detail || t('err.unknown')) : msg;
  }
  if (err.status === 429) return t('err.429');
  if (err.status >= 500) return t('err.5xx');
  return err.detail || err.message || t('err.unknown');
}

export function errorState(err, retry) {
  const detail = err && (err.detail || err.code) ? [err.code, err.detail].filter(Boolean).join(' : ') : '';
  return h('div', { class: 'state error' },
    h('div', errorMessage(err)),
    detail && detail !== errorMessage(err) ? h('div', { class: 'detail' }, detail) : null,
    retry ? h('div', { class: 'actions' }, h('button', { class: 'btn sm', onclick: retry }, t('action.retry'))) : null);
}

// Remplit un conteneur avec le résultat d'un chargement, en gérant attente, erreur et vide.
export async function section(container, loader, renderFn, opts = {}) {
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
    container.appendChild(errorState(e, opts.retry === false ? null : () => section(container, loader, renderFn, opts)));
    if (opts.onError) opts.onError(e);
    return undefined;
  }
}

export function panel(title, body, opts = {}) {
  return h('section', { class: ['panel', opts.class] },
    title || opts.actions ? h('div', { class: 'panel-head' },
      opts.icon ? icon(opts.icon, 14) : null,
      h('h2', title || ''),
      opts.sub ? h('span', { class: 'sub' }, opts.sub) : null,
      h('span', { class: 'spacer' }),
      opts.actions || null) : null,
    h('div', { class: ['panel-body', opts.flush ? 'flush' : null] }, body));
}

export function badgeState(state) {
  const st = String(state || '').toUpperCase();
  const map = { OK: 'ok', WARN: 'warn', ALERT: 'alert', NODATA: 'nodata', MUTED: 'muted', UP: 'ok', DOWN: 'alert' };
  const cls = map[st] || 'muted';
  const label = t('state.' + (st || 'UNKNOWN').toLowerCase());
  return h('span', { class: ['badge', cls] }, h('span', { class: ['dot', cls === 'alert' ? 'alert' : cls === 'muted' ? 'nodata' : cls] }), label);
}

export function checkStatusBadge(status) {
  const map = { 0: ['ok', 'check.ok'], 1: ['warn', 'check.warn'], 2: ['alert', 'check.crit'], 3: ['nodata', 'check.unknown'] };
  const [cls, key] = map[status] || map[3];
  return h('span', { class: ['badge', cls] }, h('span', { class: ['dot', cls] }), t(key));
}

export function tagList(tags, onClick) {
  if (!tags || typeof tags !== 'object') return h('span', { class: 'muted' }, '-');
  const entries = Object.entries(tags);
  if (!entries.length) return h('span', { class: 'muted' }, '-');
  return h('div', { class: 'tags' }, entries.map(([k, v]) => {
    const label = [h('b', k + ':'), String(v)];
    return onClick
      ? h('button', { class: 'tag', type: 'button', title: k + ':' + v, onclick: (e) => { e.stopPropagation(); onClick(k, v, e); } }, label)
      : h('span', { class: 'tag', title: k + ':' + v }, label);
  }));
}

// ── Notifications ─────────────────────────────────────────────────────────
let toastBox = null;
export function toast(message, kind = 'info', ms = 4000) {
  if (!toastBox || !document.body.contains(toastBox)) {
    toastBox = h('div', { class: 'toasts', role: 'status', 'aria-live': 'polite' });
    document.body.appendChild(toastBox);
  }
  const el = h('div', { class: ['toast', kind] }, message);
  toastBox.appendChild(el);
  setTimeout(() => el.remove(), ms);
}

// ── Fenêtre modale ────────────────────────────────────────────────────────
const openLayers = [];
on(document, 'keydown', (e) => {
  if (e.key === 'Escape' && openLayers.length) {
    const top = openLayers[openLayers.length - 1];
    if (top && top.dismissable !== false) { e.preventDefault(); top.close(); }
  }
});

export function modal({ title, body, actions, wide, dismissable = true, onClose }) {
  const back = h('div', { class: 'modal-back' });
  const box = h('div', { class: ['modal', wide ? 'wide' : null], role: 'dialog', 'aria-modal': 'true', 'aria-label': title || '' });
  const layer = { dismissable, close };
  function close() {
    const i = openLayers.indexOf(layer);
    if (i >= 0) openLayers.splice(i, 1);
    back.remove();
    if (onClose) onClose();
  }
  box.appendChild(h('div', { class: 'modal-head' },
    h('h2', title || ''), h('span', { class: 'spacer' }),
    h('button', { class: 'btn ghost icon sm', type: 'button', 'aria-label': t('action.close'), onclick: close }, icon('x', 14))));
  const bodyEl = h('div', { class: 'modal-body' }, body);
  box.appendChild(bodyEl);
  const foot = h('div', { class: 'modal-foot' });
  if (actions && actions.length) { actions.forEach((a) => foot.appendChild(a)); box.appendChild(foot); }
  back.appendChild(box);
  back.addEventListener('mousedown', (e) => { if (e.target === back && dismissable) close(); });
  document.body.appendChild(back);
  openLayers.push(layer);
  const first = box.querySelector('input, select, textarea');
  if (first) setTimeout(() => first.focus(), 0);
  return { close, body: bodyEl, foot, box };
}

export function confirmDialog({ title, text, confirmLabel, danger }) {
  return new Promise((resolve) => {
    let done = false;
    const finish = (v) => { if (!done) { done = true; m.close(); resolve(v); } };
    const m = modal({
      title,
      body: h('p', text || ''),
      onClose: () => { if (!done) { done = true; resolve(false); } },
      actions: [
        h('button', { class: 'btn', type: 'button', onclick: () => finish(false) }, t('action.cancel')),
        h('button', { class: ['btn', danger ? 'danger solid' : 'primary'], type: 'button', onclick: () => finish(true) }, confirmLabel || t('action.confirm')),
      ],
    });
  });
}

// ── Tiroir latéral (détail) ───────────────────────────────────────────────
export function drawer({ title, body, onClose, actions }) {
  const back = h('div', { class: 'drawer-back' });
  const el = h('aside', { class: 'drawer', role: 'dialog', 'aria-label': title || '' });
  const layer = { close };
  function close() {
    const i = openLayers.indexOf(layer);
    if (i >= 0) openLayers.splice(i, 1);
    back.remove(); el.remove();
    if (onClose) onClose();
  }
  const titleEl = h('h2', { class: 'ellipsis grow', style: { fontSize: 'var(--fs-md)' } }, title || '');
  el.appendChild(h('div', { class: 'drawer-head' }, titleEl, actions || null,
    h('button', { class: 'btn ghost icon sm', type: 'button', 'aria-label': t('action.close'), onclick: close }, icon('x', 14))));
  const bodyEl = h('div', { class: 'drawer-body' }, body);
  el.appendChild(bodyEl);
  back.addEventListener('mousedown', close);
  document.body.appendChild(back);
  document.body.appendChild(el);
  openLayers.push(layer);
  return { close, body: bodyEl, setTitle: (s2) => { titleEl.textContent = s2; } };
}

// ── Menu déroulant ────────────────────────────────────────────────────────
export function dropdown(trigger, buildMenu, opts = {}) {
  const wrap = h('div', { class: 'dropdown' }, trigger);
  let menu = null;
  let off = null;
  function close() {
    if (menu) { menu.remove(); menu = null; }
    if (off) { off(); off = null; }
    trigger.setAttribute('aria-expanded', 'false');
  }
  function open() {
    close();
    menu = h('div', { class: ['menu', opts.left ? 'left' : null], role: 'menu' });
    const content = buildMenu(close);
    if (content) menu.appendChild(content);
    wrap.appendChild(menu);
    trigger.setAttribute('aria-expanded', 'true');
    setTimeout(() => {
      off = on(document, 'mousedown', (e) => { if (menu && !wrap.contains(e.target)) close(); });
    }, 0);
  }
  trigger.setAttribute('aria-haspopup', 'true');
  trigger.addEventListener('click', (e) => { e.preventDefault(); if (menu) close(); else open(); });
  wrap.close = close;
  return wrap;
}

export function menuItem(label, onClick, opts = {}) {
  return h(opts.href ? 'a' : 'button', {
    class: ['menu-item', opts.selected ? 'sel' : null], type: opts.href ? null : 'button', href: opts.href || null, role: 'menuitem',
    onclick: (e) => { if (onClick) onClick(e); },
  }, h('span', { class: 'check-mark' }, opts.selected ? icon('check', 14) : opts.icon ? icon(opts.icon, 14) : null), label);
}

// ── Onglets ───────────────────────────────────────────────────────────────
export function tabs(items, active, onChange) {
  const bar = h('div', { class: 'tabs', role: 'tablist' });
  for (const it of items) {
    const attrs = {
      class: it.key === active ? 'active' : null, role: 'tab', 'aria-selected': it.key === active ? 'true' : 'false',
    };
    if (it.href) {
      bar.appendChild(h('a', Object.assign(attrs, { href: it.href }), it.label, it.count !== undefined && it.count !== null ? h('span', { class: 'count' }, String(it.count)) : null));
    } else {
      bar.appendChild(h('button', Object.assign(attrs, { type: 'button', onclick: () => onChange && onChange(it.key) }), it.label,
        it.count !== undefined && it.count !== null ? h('span', { class: 'count' }, String(it.count)) : null));
    }
  }
  return bar;
}

export function pills(items, active, onChange) {
  return h('div', { class: 'pill-group' }, items.map((it) => h('button', {
    type: 'button', class: it.key === active ? 'on' : null, onclick: () => onChange(it.key), title: it.title || null,
  }, it.label)));
}

// ── Copie dans le presse-papiers ──────────────────────────────────────────
export async function copyText(str) {
  try {
    if (navigator.clipboard && window.isSecureContext) { await navigator.clipboard.writeText(str); return true; }
  } catch (e) { /* repli ci-dessous */ }
  const ta = h('textarea', { style: { position: 'fixed', left: '-9999px', top: '0' } });
  ta.value = str;
  document.body.appendChild(ta);
  ta.select();
  let ok = false;
  try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
  ta.remove();
  return ok;
}

export function copyButton(getText, opts = {}) {
  const btn = h('button', { class: ['btn', 'sm', opts.class || null, 'copy'], type: 'button', title: t('action.copy') },
    icon('copy', 13), opts.label === false ? null : h('span', t('action.copy')));
  btn.addEventListener('click', async (e) => {
    e.stopPropagation();
    const str = typeof getText === 'function' ? getText() : getText;
    const ok = await copyText(String(str || ''));
    toast(ok ? t('msg.copied') : t('msg.copy_failed'), ok ? 'ok' : 'error', 2000);
  });
  return btn;
}

export function codeBlock(str) {
  return h('div', { class: 'code' }, String(str || ''), copyButton(() => str, { label: false }));
}

export function field(label, control, hint) {
  return h('label', { class: 'field' }, h('span', { class: 'lbl' }, label), control, hint ? h('span', { class: 'hint' }, hint) : null);
}

export function select(options, value, onChange, attrs = {}) {
  const el = h('select', Object.assign({ class: 'input' }, attrs));
  for (const o of options) {
    const opt = h('option', { value: o.value }, o.label);
    if (String(o.value) === String(value)) opt.selected = true;
    el.appendChild(opt);
  }
  if (onChange) el.addEventListener('change', () => onChange(el.value));
  return el;
}

export function searchInput({ placeholder, value, onInput, onEnter, slash = true, cls }) {
  const input = h('input', { class: ['input', cls || null], type: 'search', placeholder: placeholder || '', value: value || '', 'data-search': slash ? '1' : null, spellcheck: 'false', autocomplete: 'off' });
  if (onInput) input.addEventListener('input', () => onInput(input.value));
  if (onEnter) input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); onEnter(input.value); } });
  const wrap = h('div', { class: 'search' }, icon('search', 14), input, slash ? h('span', { class: 'kbd', title: t('hint.slash') }, '/') : null);
  wrap.input = input;
  return wrap;
}

export function kv(pairs) {
  const dl = h('dl', { class: 'kv' });
  for (const [k, v] of pairs) {
    if (v === undefined) continue;
    dl.appendChild(h('dt', k));
    dl.appendChild(h('dd', v === null || v === '' ? '-' : v));
  }
  return dl;
}

export function statusDot(status) {
  return h('span', { class: ['dot', status === 'up' ? 'up' : status === 'down' ? 'down' : 'nodata'], title: t('host.status.' + (status || 'unknown')) });
}
