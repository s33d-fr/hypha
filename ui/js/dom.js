// Construction du DOM sans innerHTML sur des données : tout texte passe par textContent.

const SVG_NS = 'http://www.w3.org/2000/svg';

function append(el, child) {
  if (child === null || child === undefined || child === false || child === true) return;
  if (Array.isArray(child)) { for (const c of child) append(el, c); return; }
  if (child instanceof Node) { el.appendChild(child); return; }
  el.appendChild(document.createTextNode(String(child)));
}

function setAttrs(el, attrs, isSvg) {
  if (!attrs) return;
  for (const [k, v] of Object.entries(attrs)) {
    if (v === undefined || v === null || v === false) continue;
    if (k === 'class' || k === 'className') {
      const cls = Array.isArray(v) ? v.filter(Boolean).join(' ') : v;
      if (isSvg) el.setAttribute('class', cls); else el.className = cls;
    } else if (k === 'style' && typeof v === 'object') {
      for (const [sk, sv] of Object.entries(v)) {
        if (sv === undefined || sv === null) continue;
        if (sk.startsWith('--')) el.style.setProperty(sk, sv); else el.style[sk] = sv;
      }
    } else if (k === 'dataset') {
      for (const [dk, dv] of Object.entries(v)) if (dv !== undefined && dv !== null) el.dataset[dk] = dv;
    } else if (k.startsWith('on') && typeof v === 'function') {
      el.addEventListener(k.slice(2).toLowerCase(), v);
    } else if (k === 'value' && !isSvg) {
      el.value = v;
    } else if (k === 'checked' || k === 'selected' || k === 'disabled' || k === 'readOnly' || k === 'multiple') {
      el[k] = !!v;
    } else if (k === 'ref' && typeof v === 'function') {
      v(el);
    } else {
      el.setAttribute(k, v === true ? '' : String(v));
    }
  }
}

export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  if (attrs && (typeof attrs !== 'object' || attrs instanceof Node || Array.isArray(attrs))) {
    children.unshift(attrs);
    attrs = null;
  }
  setAttrs(el, attrs, false);
  append(el, children);
  return el;
}

export function s(tag, attrs, ...children) {
  const el = document.createElementNS(SVG_NS, tag);
  setAttrs(el, attrs, true);
  append(el, children);
  return el;
}

export function clear(el) {
  while (el && el.firstChild) el.removeChild(el.firstChild);
  return el;
}

export function mount(el, ...children) {
  clear(el);
  append(el, children);
  return el;
}

export function frag(...children) {
  const f = document.createDocumentFragment();
  append(f, children);
  return f;
}

// Icônes : tracés statiques (jamais de données dedans), 16 px, trait 1.6.
const ICONS = {
  cloud: 'M7 18h10a4 4 0 0 0 .6-7.96A6 6 0 0 0 6.2 9.4 4.4 4.4 0 0 0 7 18z',
  overview: 'M3 3h7v7H3zM14 3h7v4h-7zM14 11h7v10h-7zM3 14h7v7H3z',
  infra: 'M4 4h16v6H4zM4 14h16v6H4zM8 7h.01M8 17h.01',
  metrics: 'M3 20h18M5 16l4-6 4 3 6-8',
  logs: 'M4 5h16M4 10h16M4 15h10M4 20h7',
  apm: 'M4 6h8M8 12h10M6 18h6M4 6v0M12 6l-4 6M18 12l-6 6',
  process: 'M5 4h14v16H5zM9 8h6M9 12h6M9 16h3',
  network: 'M12 3v6M12 15v6M3 12h6M15 12h6M9 9l-4-4M15 9l4-4M9 15l-4 4M15 15l4 4',
  container: 'M3 7l9-4 9 4v10l-9 4-9-4zM3 7l9 4 9-4M12 11v10',
  events: 'M12 3v4M12 17v4M3 12h4M17 12h4M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8z',
  monitor: 'M6 8a6 6 0 0 1 12 0c0 7 3 8 3 8H3s3-1 3-8M10 20a2 2 0 0 0 4 0',
  dashboard: 'M3 3h8v10H3zM13 3h8v6h-8zM13 11h8v10h-8zM3 15h8v6H3z',
  synthetic: 'M12 3a9 9 0 1 0 9 9M12 7v5l3 3M17 3h4v4',
  rum: 'M3 5h18v12H3zM8 21h8M12 17v4M7 13l3-3 2 2 4-4',
  settings: 'M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6zM19.4 15a1.6 1.6 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.6 1.6 0 0 0-1.8-.3 1.6 1.6 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.6 1.6 0 0 0-1-1.5 1.6 1.6 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.6 1.6 0 0 0 .3-1.8 1.6 1.6 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.6 1.6 0 0 0 1.5-1 1.6 1.6 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.6 1.6 0 0 0 1.8.3H9a1.6 1.6 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.6 1.6 0 0 0 1 1.5 1.6 1.6 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.6 1.6 0 0 0-.3 1.8V9a1.6 1.6 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.6 1.6 0 0 0-1.5 1z',
  search: 'M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14zM21 21l-5-5',
  clock: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM12 7v5l3 2',
  refresh: 'M20 11a8 8 0 0 0-14.9-3M4 4v4h4M4 13a8 8 0 0 0 14.9 3M20 20v-4h-4',
  chevronDown: 'M6 9l6 6 6-6',
  chevronRight: 'M9 6l6 6-6 6',
  chevronLeft: 'M15 6l-6 6 6 6',
  plus: 'M12 5v14M5 12h14',
  x: 'M6 6l12 12M18 6L6 18',
  check: 'M5 12l5 5 9-10',
  copy: 'M9 9h11v11H9zM5 15H4V4h11v1',
  trash: 'M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13',
  edit: 'M4 20h4L19 9l-4-4L4 16zM14 6l4 4',
  mute: 'M6 8a6 6 0 0 1 12 0c0 7 3 8 3 8H3s3-1 3-8M3 3l18 18',
  sun: 'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8zM12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.4 1.4M17.6 17.6L19 19M5 19l1.4-1.4M17.6 6.4L19 5',
  moon: 'M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z',
  user: 'M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM4 21a8 8 0 0 1 16 0',
  logout: 'M15 4h4v16h-4M10 8l-4 4 4 4M6 12h10',
  key: 'M14 4a6 6 0 0 0-5.7 7.9L3 17.2V21h3.8l.9-.9V18h2.1v-2.1h2.1l1.3-1.3A6 6 0 1 0 14 4zM16 7h.01',
  download: 'M12 4v11M7 10l5 5 5-5M5 20h14',
  users: 'M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM2 21a7 7 0 0 1 14 0M16 3.5a4 4 0 0 1 0 7M18 14a7 7 0 0 1 4 7',
  bell: 'M6 8a6 6 0 0 1 12 0c0 7 3 8 3 8H3s3-1 3-8M10 20a2 2 0 0 0 4 0',
  globe: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM3 12h18M12 3c2.5 2.5 3.5 6 3.5 9s-1 6.5-3.5 9c-2.5-2.5-3.5-6-3.5-9s1-6.5 3.5-9z',
  play: 'M7 5l12 7-12 7z',
  pause: 'M7 5h3v14H7zM14 5h3v14h-3z',
  info: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM12 11v5M12 8h.01',
  warn: 'M12 4l9 16H3zM12 10v4M12 17h.01',
  external: 'M14 4h6v6M20 4l-9 9M18 14v6H4V6h6',
  menu: 'M4 6h16M4 12h16M4 18h16',
  collapse: 'M4 4v16M9 12h11M13 8l-4 4 4 4',
  expand: 'M20 4v16M4 12h11M11 8l4 4-4 4',
  map: 'M12 3l7.8 4.5v9L12 21l-7.8-4.5v-9z',
  list: 'M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01',
  lock: 'M6 11h12v10H6zM8 11V8a4 4 0 0 1 8 0v3',
  shield: 'M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z',
  layers: 'M12 3l9 5-9 5-9-5zM3 13l9 5 9-5M3 17l9 5 9-5',
  server: 'M4 4h16v7H4zM4 13h16v7H4zM8 7.5h.01M8 16.5h.01',
  trace: 'M4 5h10M7 10h10M10 15h10M6 20h6',
  flag: 'M5 21V4M5 4h11l-2 4 2 4H5',
  filter: 'M4 5h16l-6 8v6l-4-2v-4z',
  grip: 'M9 6h.01M15 6h.01M9 12h.01M15 12h.01M9 18h.01M15 18h.01',
  // Données et exploration 0.4.0 : équipements, inventaire, erreurs, services, bases, vues, flotte, tableaux.
  device: 'M3 8h18v8H3zM7 12h.01M10.5 12h.01M14 12h3M7 16v3M17 16v3M12 5v3',
  package: 'M12 3l8 4.5v9L12 21l-8-4.5v-9zM4 7.5l8 4.5 8-4.5M12 12v9M8 5.3l8 4.5',
  bug: 'M9 7V5a3 3 0 0 1 6 0v2M7 9h10v5a5 5 0 0 1-10 0zM12 11v8M3 13h4M17 13h4M4 7l3 2M20 7l-3 2M4 19l3-2M20 19l-3-2',
  database: 'M4 6c0-1.7 3.6-3 8-3s8 1.3 8 3-3.6 3-8 3-8-1.3-8-3zM4 6v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3',
  graph: 'M5 4a2 2 0 1 1 0 4 2 2 0 0 1 0-4zM19 6a2 2 0 1 1 0 4 2 2 0 0 1 0-4zM12 16a2 2 0 1 1 0 4 2 2 0 0 1 0-4zM7 6.3l10 1.4M6 7.8l5 8.4M18 9.8l-5 6.4',
  bookmark: 'M6 3h12v18l-6-4-6 4z',
  tv: 'M3 5h18v12H3zM9 21h6M8 9l3 2-3 2M13 13h3',
  upload: 'M12 20V9M7 14l5-5 5 5M5 4h14',
  share: 'M18 3a3 3 0 1 1 0 6 3 3 0 0 1 0-6zM6 9a3 3 0 1 1 0 6 3 3 0 0 1 0-6zM18 15a3 3 0 1 1 0 6 3 3 0 0 1 0-6zM8.6 13.5l6.8 4M15.4 6.5l-6.8 4',
  folder: 'M3 6h6l2 2h10v11H3z',
  mask: 'M3 7h18v5a9 4 0 0 1-18 0zM8 11h2M14 11h2',
  fx: 'M14 4h-2a3 3 0 0 0-3 3v13M6 10h7M14 12l6 7M20 12l-6 7',
  agent: 'M7 4h10v6H7zM5 10h14v10H5zM9 14h.01M15 14h.01M10 17h4M12 2v2',
  usage: 'M4 20V10M10 20V4M16 20v-7M2 20h20',
  steps: 'M4 6h4v4H4zM10 8h10M4 14h4v4H4zM10 16h10',
  spark: 'M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9zM19 15l.8 2.2L22 18l-2.2.8L19 21l-.8-2.2L16 18l2.2-.8z',
  link: 'M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1',
  // Exploitation 0.4.0 : problèmes, maintenances, incidents, audit, actions, pages de statut.
  problem: 'M12 3l9.5 17h-19zM12 10v4.5M12 17.5h.01',
  wrench: 'M14.7 6.3a4 4 0 0 0 5 5l-9.4 9.4a2.1 2.1 0 0 1-3-3zM14.7 6.3L17 4a4 4 0 0 1 3 3l-2.3 2.3',
  siren: 'M7 18v-6a5 5 0 0 1 10 0v6M5 21h14M12 3v2M4.2 6.2l1.4 1.4M19.8 6.2l-1.4 1.4M10 13a2 2 0 0 1 2-2',
  history: 'M3 12a9 9 0 1 0 3-6.7M3 4v5h5M12 7v5l3 2',
  ack: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM8 12l3 3 5-6',
  status: 'M4 5h16v14H4zM8 15v-3M12 15V9M16 15v-5',
  mail: 'M3 6h18v12H3zM3 7l9 6 9-6',
  send: 'M4 12l16-8-6 16-2-7zM12 13l8-9',
  eye: 'M2 12s4-7 10-7 10 7 10 7-4 7-10 7S2 12 2 12zM12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6z',
  up: 'M12 19V5M6 11l6-6 6 6',
  down: 'M12 5v14M6 13l6 6 6-6',
};

export function icon(name, size = 16) {
  const d = ICONS[name] || ICONS.info;
  return s('svg', {
    width: size, height: size, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor',
    'stroke-width': 1.7, 'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'aria-hidden': 'true',
  }, s('path', { d }));
}

export function debounce(fn, ms) {
  let t = null;
  const f = (...args) => { clearTimeout(t); t = setTimeout(() => fn(...args), ms); };
  f.cancel = () => clearTimeout(t);
  return f;
}

export function on(target, type, fn, opts) {
  target.addEventListener(type, fn, opts);
  return () => target.removeEventListener(type, fn, opts);
}

export function cssVar(name, el = document.documentElement) {
  return getComputedStyle(el).getPropertyValue(name).trim();
}

export function uid(prefix = 'id') {
  return prefix + Math.random().toString(36).slice(2, 9);
}
