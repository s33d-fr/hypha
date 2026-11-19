// Carte des hôtes : un hexagone par hôte, couleur selon l'utilisation (0-100 %), regroupement par étiquette.
import { h, s, clear } from '../dom.js';
import { t } from '../i18n.js';
import { fmtPct, isNum } from '../format.js';
import { usageColor } from './table.js';

export const MAP_METRICS = [
  { key: 'cpu', field: 'cpu', label: 'map.metric.cpu' },
  { key: 'mem', field: 'mem_pct', label: 'map.metric.mem' },
  { key: 'disk', field: 'disk_max_pct', label: 'map.metric.disk' },
  { key: 'load', field: 'load_norm', label: 'map.metric.load', scale: 100 },
];

export function metricValue(host, metricKey) {
  const m = MAP_METRICS.find((x) => x.key === metricKey) || MAP_METRICS[0];
  const v = host[m.field];
  if (!isNum(v)) return null;
  return m.scale ? v * m.scale : v;
}

function hexPath(cx, cy, r) {
  const pts = [];
  for (let i = 0; i < 6; i++) {
    const a = Math.PI / 180 * (60 * i - 30);
    pts.push((cx + r * Math.cos(a)).toFixed(1) + ',' + (cy + r * Math.sin(a)).toFixed(1));
  }
  return 'M' + pts.join('L') + 'Z';
}

export function legend() {
  const steps = [
    ['var(--hex-1)', '0-25'], ['var(--hex-2)', '25-50'], ['var(--hex-3)', '50-75'], ['var(--hex-4)', '75-90'], ['var(--hex-5)', '90-100'],
  ];
  return h('div', { class: 'hexmap-legend' },
    steps.map(([c, l]) => h('span', h('span', { class: 'sw', style: { background: c } }), l + (l.endsWith('100') ? ' %' : ''))),
    h('span', h('span', { class: 'sw', style: { background: 'var(--hex-down)' } }), t('map.no_value')));
}

// hosts : liste de GET /hosts. opts : {metric, groupBy, onClick(host), size}
export function hexMap(hosts, opts = {}) {
  const wrap = h('div', { class: 'hexmap' });
  const tip = h('div', { class: 'chart-tip map-tip', hidden: true });
  document.body.appendChild(tip);
  const metric = opts.metric || 'cpu';
  const groupBy = opts.groupBy || '';
  const groups = new Map();
  for (const host of hosts) {
    const g = groupBy ? ((host.tags && host.tags[groupBy]) || t('map.no_tag', { tag: groupBy })) : '';
    if (!groups.has(g)) groups.set(g, []);
    groups.get(g).push(host);
  }
  const entries = Array.from(groups.entries()).sort((a, b) => String(a[0]).localeCompare(String(b[0])));
  for (const [, list] of entries) list.sort((a, b) => (metricValue(b, metric) ?? -1) - (metricValue(a, metric) ?? -1));

  function draw() {
    clear(wrap);
    const width = Math.max(240, wrap.clientWidth || 800);
    const total = hosts.length;
    const r = opts.size || (total <= 12 ? 34 : total <= 40 ? 24 : total <= 150 ? 17 : 11);
    const hw = Math.sqrt(3) * r;
    const vh = 1.5 * r;
    const perRow = Math.max(1, Math.floor((width - hw / 2 - 8) / hw));
    let y = 0;
    const svg = s('svg', { role: 'img', 'aria-label': t('map.aria') });
    for (const [name, list] of entries) {
      if (groupBy) {
        y += 18;
        svg.appendChild(s('text', { x: 2, y: y - 4, class: 'group-title' }, name + ' (' + list.length + ')'));
      }
      const rows = Math.ceil(list.length / perRow);
      list.forEach((host, i) => {
        const row = Math.floor(i / perRow);
        const col = i % perRow;
        const cx = hw / 2 + 2 + col * hw + (row % 2 ? hw / 2 : 0);
        const cy = y + r + row * vh;
        const v = metricValue(host, metric);
        const down = host.status === 'down';
        const fill = down ? 'var(--hex-down)' : usageColor(v);
        const path = s('path', { d: hexPath(cx, cy, r - 1), class: 'hex', fill, 'stroke-dasharray': down ? '3 2' : null, tabindex: 0, role: 'link', 'aria-label': host.hostname });
        path.addEventListener('mouseenter', (e) => showTip(e, host, v));
        path.addEventListener('mousemove', moveTip);
        path.addEventListener('mouseleave', () => { tip.hidden = true; });
        path.addEventListener('click', () => { tip.hidden = true; if (opts.onClick) opts.onClick(host); });
        path.addEventListener('keydown', (e) => { if (e.key === 'Enter' && opts.onClick) opts.onClick(host); });
        svg.appendChild(path);
        if (r >= 24) {
          const label = String(host.hostname || '').slice(0, r >= 30 ? 12 : 8);
          const ink = down || !isNum(v) ? 'var(--text-2)' : v >= 50 && v < 75 ? '#0b0b0b' : '#ffffff';
          svg.appendChild(s('text', { x: cx, y: cy + 3, class: 'hex-label', style: { fill: ink } }, label));
        }
      });
      y += r + Math.max(0, rows - 1) * vh + r + 6;
    }
    svg.setAttribute('viewBox', '0 0 ' + width + ' ' + Math.max(40, y));
    svg.setAttribute('height', String(Math.max(40, y)));
    wrap.appendChild(svg);
  }

  function showTip(e, host, v) {
    clear(tip);
    tip.appendChild(h('div', { class: 'tt' }, host.hostname || ''));
    const rows = [
      [t('map.metric.cpu'), fmtPct(host.cpu)],
      [t('map.metric.mem'), fmtPct(host.mem_pct)],
      [t('map.metric.disk'), fmtPct(host.disk_max_pct)],
      [t('map.metric.load'), isNum(host.load_norm) ? host.load_norm.toFixed(2) : '-'],
      [t('host.status'), t('host.status.' + (host.status || 'unknown'))],
    ];
    for (const [k, val] of rows) tip.appendChild(h('div', { class: 'tr' }, h('span', { class: 'l' }, k), h('span', { class: 'v' }, val)));
    tip.hidden = false;
    moveTip(e);
  }
  function moveTip(e) {
    const x = Math.min(window.innerWidth - tip.offsetWidth - 8, e.clientX + 14);
    const y = Math.min(window.innerHeight - tip.offsetHeight - 8, e.clientY + 14);
    tip.style.left = x + 'px';
    tip.style.top = y + 'px';
  }

  let lastW = 0;
  const ro = new ResizeObserver(() => {
    if (!wrap.isConnected) { ro.disconnect(); tip.remove(); return; }
    const w = Math.floor(wrap.clientWidth);
    if (w > 0 && Math.abs(w - lastW) > 4) { lastW = w; draw(); }
  });
  ro.observe(wrap);
  wrap.destroy = () => { ro.disconnect(); tip.remove(); };
  return wrap;
}
