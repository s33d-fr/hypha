// Mini-courbes et mini-histogrammes en SVG (tableaux, listes) : aucun axe, une échelle par graphique.
import { h, s } from '../dom.js';

function finite(v) { return typeof v === 'number' && Number.isFinite(v); }

// values : nombres (null = trou). opts : {width, height, color, fill, min, max, title}
export function sparkline(values, opts = {}) {
  const w = opts.width || 110;
  const hh = opts.height || 22;
  const vals = (values || []).map((v) => (finite(v) ? v : null));
  const nums = vals.filter((v) => v !== null);
  const svg = s('svg', { class: 'spark', width: w, height: hh, viewBox: '0 0 ' + w + ' ' + hh, preserveAspectRatio: 'none', 'aria-hidden': 'true' });
  if (opts.title) svg.appendChild(s('title', opts.title));
  if (nums.length < 2) {
    svg.appendChild(s('line', { x1: 0, y1: hh - 1, x2: w, y2: hh - 1, class: 'spark-base' }));
    return svg;
  }
  const lo = finite(opts.min) ? opts.min : Math.min(0, ...nums);
  let hi = finite(opts.max) ? opts.max : Math.max(...nums);
  if (hi <= lo) hi = lo + 1;
  const n = vals.length;
  const x = (i) => (n === 1 ? w / 2 : (i / (n - 1)) * (w - 2) + 1);
  const y = (v) => hh - 1 - ((v - lo) / (hi - lo)) * (hh - 3);
  let d = '';
  let area = '';
  let open = false;
  let startX = 0;
  vals.forEach((v, i) => {
    if (v === null) {
      if (open && opts.fill !== false) area += 'L' + x(i - 1).toFixed(1) + ',' + (hh - 1) + 'L' + startX.toFixed(1) + ',' + (hh - 1) + 'Z';
      open = false;
      return;
    }
    const px = x(i).toFixed(1);
    const py = y(v).toFixed(1);
    if (!open) { d += 'M' + px + ',' + py; area += 'M' + px + ',' + py; startX = x(i); open = true; } else { d += 'L' + px + ',' + py; area += 'L' + px + ',' + py; }
  });
  if (open && opts.fill !== false) area += 'L' + x(n - 1).toFixed(1) + ',' + (hh - 1) + 'L' + startX.toFixed(1) + ',' + (hh - 1) + 'Z';
  const color = opts.color || 'var(--accent)';
  if (opts.fill !== false) svg.appendChild(s('path', { d: area, class: 'spark-area', style: { fill: color } }));
  svg.appendChild(s('path', { d, class: 'spark-line', style: { stroke: color } }));
  return svg;
}

// Histogramme : une barre par valeur ; highlight(v, i) -> couleur facultative d'une barre.
export function sparkbars(values, opts = {}) {
  const w = opts.width || 110;
  const hh = opts.height || 22;
  const vals = (values || []).map((v) => (finite(v) ? v : 0));
  const svg = s('svg', { class: 'spark', width: w, height: hh, viewBox: '0 0 ' + w + ' ' + hh, preserveAspectRatio: 'none', 'aria-hidden': 'true' });
  if (opts.title) svg.appendChild(s('title', opts.title));
  const max = Math.max(0, ...vals);
  const n = vals.length;
  if (!n || max <= 0) {
    svg.appendChild(s('line', { x1: 0, y1: hh - 1, x2: w, y2: hh - 1, class: 'spark-base' }));
    return svg;
  }
  const bw = w / n;
  vals.forEach((v, i) => {
    if (v <= 0) return;
    const bh = Math.max(1, (v / max) * (hh - 1));
    const col = opts.highlight ? opts.highlight(v, i) : null;
    svg.appendChild(s('rect', { x: (i * bw + 0.5).toFixed(2), y: (hh - bh).toFixed(2), width: Math.max(0.8, bw - 1).toFixed(2), height: bh.toFixed(2), class: 'spark-bar', style: { fill: col || opts.color || 'var(--accent)' } }));
  });
  return svg;
}

// Barre empilée horizontale : parts [{value, color, label}].
export function stackBar(parts, opts = {}) {
  const total = (parts || []).reduce((a, p) => a + (finite(p.value) && p.value > 0 ? p.value : 0), 0);
  const el = h('div', { class: 'stackbar', style: { width: (opts.width || 120) + 'px' }, title: opts.title || null });
  if (!total) return el;
  for (const p of parts) {
    if (!finite(p.value) || p.value <= 0) continue;
    el.appendChild(h('i', { style: { width: (p.value / total * 100).toFixed(2) + '%', background: p.color || 'var(--accent)' }, title: p.label ? p.label + ' : ' + p.value : null }));
  }
  return el;
}
