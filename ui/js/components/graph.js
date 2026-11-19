// Graphe à forces en SVG, code en propre (aucune bibliothèque) : carte des services, carte du réseau.
// Simulation : répulsion entre nœuds (grille de voisinage au-delà de 400 nœuds), ressorts sur les
// arêtes, rappel vers le centre, collision ; refroidissement progressif puis arrêt. Zoom à la molette,
// déplacement du fond, nœuds déplaçables, survol = voisins en avant, clic = fiche.
import { h, s, clear, on } from '../dom.js';
import { t } from '../i18n.js';

const ALPHA_MIN = 0.002;
const ALPHA_DECAY = 1 - Math.pow(0.002, 1 / 300);
const VELOCITY_DECAY = 0.58;

function clamp(v, lo, hi) { return Math.max(lo, Math.min(hi, v)); }

// Forme d'un nœud selon sa nature (service, base, externe, hôte, équipement réseau).
function shapeFor(kind, r) {
  if (kind === 'database') return s('rect', { x: -r, y: -r * 0.8, width: 2 * r, height: 1.6 * r, rx: r * 0.35, class: 'gshape' });
  if (kind === 'network_device') {
    const d = r * 1.15;
    return s('path', { d: 'M0,' + (-d) + 'L' + d + ',0L0,' + d + 'L' + (-d) + ',0Z', class: 'gshape' });
  }
  if (kind === 'cloud_resource') return s('rect', { x: -r, y: -r, width: 2 * r, height: 2 * r, rx: r * 0.3, class: 'gshape' });
  return s('circle', { r, class: ['gshape', kind === 'external' ? 'ext' : null] });
}

// nodes : [{id, label, kind, state:'ok|warn|alert|nodata', size, data}]
// edges : [{from, to, weight, errors, data}]
// opts : {height, onNodeClick(node), nodeTip(node) -> [[libellé, valeur]], edgeTip(edge) -> [[…]],
//         positions (Map partagée : garde la disposition d'un rendu à l'autre), directed, compact, emptyText}
export function forceGraph(nodesIn, edgesIn, opts = {}) {
  const height = opts.height || 520;
  const wrap = h('div', { class: ['fgraph', opts.compact ? 'compact' : null], style: { height: height + 'px' } });
  const tip = h('div', { class: 'chart-tip map-tip', hidden: true });
  document.body.appendChild(tip);
  const positions = opts.positions instanceof Map ? opts.positions : new Map();
  const gid = nextGraphId();

  // ── Données ─────────────────────────────────────────────────────────────
  const byId = new Map();
  const nodes = [];
  for (const n of nodesIn || []) {
    if (n === null || n === undefined || n.id === undefined || n.id === null || byId.has(String(n.id))) continue;
    const node = Object.assign({}, n, { id: String(n.id), x: 0, y: 0, vx: 0, vy: 0, fx: null, fy: null, deg: 0, r: 8 });
    byId.set(node.id, node);
    nodes.push(node);
  }
  const edges = [];
  for (const e of edgesIn || []) {
    const a = byId.get(String(e.from));
    const b = byId.get(String(e.to));
    if (!a || !b || a === b) continue;
    edges.push(Object.assign({}, e, { s: a, t: b }));
    a.deg++; b.deg++;
  }
  if (!nodes.length) {
    wrap.appendChild(h('div', { class: 'state' }, opts.emptyText || t('graph.empty')));
    wrap.destroy = () => tip.remove();
    wrap.fit = () => {};
    return wrap;
  }
  // Nœuds trouvés par la recherche de la page : anneau et nom appuyé, voisins atténués, liaisons
  // des nœuds trouvés seules en avant.
  const matched = new Set(asIds(opts.match).filter((id) => byId.has(id)));
  const maxSize = Math.max(1, ...nodes.map((n) => (Number.isFinite(n.size) ? n.size : 0)));
  const maxW = Math.max(1, ...edges.map((e) => (Number.isFinite(e.weight) ? e.weight : 0)));
  const many = nodes.length > 60;
  for (const n of nodes) {
    const sz = Number.isFinite(n.size) && n.size > 0 ? n.size : 0;
    n.r = opts.compact ? 4 + 8 * Math.sqrt(sz / maxSize) : (many ? 5 : 7) + (many ? 10 : 14) * Math.sqrt(sz / maxSize);
  }
  // Positions : celles du rendu précédent, sinon une spirale (aucun recouvrement au départ).
  let fresh = 0;
  nodes.forEach((n, i) => {
    const p = positions.get(n.id);
    if (p) { n.x = p.x; n.y = p.y; return; }
    fresh++;
    const rad = 18 * Math.sqrt(i + 0.5);
    const ang = i * Math.PI * (3 - Math.sqrt(5));
    n.x = rad * Math.cos(ang);
    n.y = rad * Math.sin(ang);
  });

  // ── Simulation ──────────────────────────────────────────────────────────
  let alpha = fresh ? 1 : 0.25;
  let alphaTarget = 0;
  const charge = opts.compact ? -140 : -420;
  const linkDist = opts.compact ? 36 : 70;

  function repulse() {
    const n = nodes.length;
    if (n <= 400) {
      for (let i = 0; i < n; i++) {
        const a = nodes[i];
        for (let j = i + 1; j < n; j++) pair(a, nodes[j]);
      }
      return;
    }
    // Grille : seules les cellules voisines comptent (coupure à la taille d'une cellule).
    const cell = 160;
    const grid = new Map();
    for (const nd of nodes) {
      const k = Math.floor(nd.x / cell) + ',' + Math.floor(nd.y / cell);
      if (!grid.has(k)) grid.set(k, []);
      grid.get(k).push(nd);
    }
    for (const [k, list] of grid) {
      const [cx, cy] = k.split(',').map(Number);
      for (let dx = -1; dx <= 1; dx++) {
        for (let dy = -1; dy <= 1; dy++) {
          const other = grid.get((cx + dx) + ',' + (cy + dy));
          if (!other) continue;
          for (const a of list) for (const b of other) if (a.id < b.id) pair(a, b);
        }
      }
    }
  }

  function pair(a, b) {
    let dx = b.x - a.x;
    let dy = b.y - a.y;
    let d2 = dx * dx + dy * dy;
    if (d2 < 0.01) { dx = (Math.random() - 0.5) * 0.5; dy = (Math.random() - 0.5) * 0.5; d2 = dx * dx + dy * dy; }
    const f = charge * alpha / Math.max(d2, 25);
    a.vx += dx * f; a.vy += dy * f;
    b.vx -= dx * f; b.vy -= dy * f;
    const minD = a.r + b.r + 6;
    if (d2 < minD * minD) {
      const d = Math.sqrt(d2);
      const push = (minD - d) / d * 0.5;
      a.vx -= dx * push; a.vy -= dy * push;
      b.vx += dx * push; b.vy += dy * push;
    }
  }

  function links() {
    for (const e of edges) {
      const a = e.s; const b = e.t;
      let dx = b.x + b.vx - a.x - a.vx;
      let dy = b.y + b.vy - a.y - a.vy;
      const l = Math.sqrt(dx * dx + dy * dy) || 1;
      const target = linkDist + a.r + b.r;
      const k = (l - target) / l * alpha * (0.6 / Math.max(1, Math.min(a.deg, b.deg)));
      dx *= k; dy *= k;
      const bias = a.deg / (a.deg + b.deg);
      b.vx -= dx * bias; b.vy -= dy * bias;
      a.vx += dx * (1 - bias); a.vy += dy * (1 - bias);
    }
  }

  function tick() {
    alpha += (alphaTarget - alpha) * ALPHA_DECAY;
    repulse();
    links();
    const g = 0.035 * alpha;
    for (const n of nodes) {
      if (n.fx !== null) { n.x = n.fx; n.y = n.fy; n.vx = 0; n.vy = 0; continue; }
      n.vx -= n.x * g;
      n.vy -= n.y * g;
      n.vx *= VELOCITY_DECAY;
      n.vy *= VELOCITY_DECAY;
      n.x += clamp(n.vx, -60, 60);
      n.y += clamp(n.vy, -60, 60);
    }
  }

  // Première disposition calculée sans dessiner (60 ms au plus), le reste s'anime.
  const t0 = performance.now();
  let pre = 0;
  while (alpha > 0.08 && pre < 260 && performance.now() - t0 < 60) { tick(); pre++; }

  // ── Dessin ──────────────────────────────────────────────────────────────
  const svg = s('svg', { class: 'fgraph-svg', role: 'img', 'aria-label': opts.ariaLabel || t('graph.aria') });
  const defs = s('defs');
  for (const kind of ['n', 'warn', 'err']) {
    defs.appendChild(s('marker', { id: 'gm-' + kind + '-' + gid, viewBox: '0 0 10 10', refX: 9, refY: 5, markerWidth: 5, markerHeight: 5, orient: 'auto-start-reverse', class: 'gmarker ' + kind },
      s('path', { d: 'M0,0L10,5L0,10Z' })));
  }
  svg.appendChild(defs);
  const viewport = s('g', { class: 'gview' });
  const edgeLayer = s('g', { class: 'gedges' });
  const nodeLayer = s('g', { class: 'gnodes' });
  viewport.appendChild(edgeLayer);
  viewport.appendChild(nodeLayer);
  svg.appendChild(viewport);
  wrap.appendChild(svg);

  const neighbors = new Map(nodes.map((n) => [n.id, new Set()]));
  for (const e of edges) {
    const w = Number.isFinite(e.weight) ? e.weight : 0;
    const errRatio = Number.isFinite(e.errors) && w > 0 ? e.errors / w : 0;
    const level = errRatio >= 0.05 ? 'err' : errRatio >= 0.01 ? 'warn' : 'n';
    e.el = s('line', {
      class: ['gedge', level !== 'n' ? level : null],
      'stroke-width': (1 + 6 * Math.sqrt(Math.max(0, w) / maxW)).toFixed(2),
      'marker-end': opts.directed ? 'url(#gm-' + level + '-' + gid + ')' : null,
    });
    e.el.addEventListener('mouseenter', (ev) => { if (opts.edgeTip) showTip(ev, edgeTitle(e), opts.edgeTip(e)); });
    e.el.addEventListener('mousemove', moveTip);
    e.el.addEventListener('mouseleave', hideTip);
    edgeLayer.appendChild(e.el);
    neighbors.get(e.s.id).add(e.t.id);
    neighbors.get(e.t.id).add(e.s.id);
  }
  for (const n of nodes) {
    const st = ['ok', 'warn', 'alert', 'nodata'].includes(n.state) ? n.state : 'nodata';
    n.el = s('g', { class: ['gnode', st, 'k-' + (n.kind || 'node'), matched.has(n.id) ? 'match' : null], tabindex: 0, role: 'button', 'aria-label': n.label || n.id });
    if (matched.has(n.id)) n.el.appendChild(s('circle', { class: 'gring', r: n.r + 6 }));
    n.el.appendChild(shapeFor(n.kind, n.r));
    n.labelEl = s('text', { class: 'glabel', y: n.r + 12 }, String(n.label || n.id).slice(0, 40));
    n.el.appendChild(n.labelEl);
    n.el.addEventListener('mouseenter', (ev) => { highlight(n); showTip(ev, n.label || n.id, opts.nodeTip ? opts.nodeTip(n) : []); });
    n.el.addEventListener('mousemove', moveTip);
    n.el.addEventListener('mouseleave', () => { highlight(null); hideTip(); });
    n.el.addEventListener('keydown', (ev) => { if (ev.key === 'Enter' && opts.onNodeClick) opts.onNodeClick(n); });
    n.el.addEventListener('pointerdown', (ev) => startNodeDrag(ev, n));
    nodeLayer.appendChild(n.el);
  }

  function edgeTitle(e) { return (e.s.label || e.s.id) + ' → ' + (e.t.label || e.t.id); }

  function draw() {
    for (const e of edges) {
      const a = e.s; const b = e.t;
      let x2 = b.x; let y2 = b.y;
      if (opts.directed) {
        const dx = b.x - a.x; const dy = b.y - a.y;
        const l = Math.sqrt(dx * dx + dy * dy) || 1;
        x2 = b.x - dx / l * (b.r + 3);
        y2 = b.y - dy / l * (b.r + 3);
      }
      e.el.setAttribute('x1', a.x.toFixed(1));
      e.el.setAttribute('y1', a.y.toFixed(1));
      e.el.setAttribute('x2', x2.toFixed(1));
      e.el.setAttribute('y2', y2.toFixed(1));
    }
    for (const n of nodes) n.el.setAttribute('transform', 'translate(' + n.x.toFixed(1) + ',' + n.y.toFixed(1) + ')');
  }

  // ── Vue : zoom et déplacement ───────────────────────────────────────────
  let k = 1; let tx = 0; let ty = 0;
  let touched = false;
  function applyView() {
    viewport.setAttribute('transform', 'translate(' + tx.toFixed(1) + ',' + ty.toFixed(1) + ') scale(' + k.toFixed(3) + ')');
    svg.classList.toggle('nolabels', many && k < 0.7);
  }
  function size() { return { w: Math.max(200, wrap.clientWidth || 800), h: Math.max(120, wrap.clientHeight || height) }; }
  function fit() {
    const { w, h: hh } = size();
    let x0 = Infinity; let y0 = Infinity; let x1 = -Infinity; let y1 = -Infinity;
    for (const n of nodes) { x0 = Math.min(x0, n.x - n.r); y0 = Math.min(y0, n.y - n.r); x1 = Math.max(x1, n.x + n.r); y1 = Math.max(y1, n.y + n.r + 14); }
    const bw = Math.max(40, x1 - x0); const bh = Math.max(40, y1 - y0);
    k = clamp(Math.min((w - 40) / bw, (hh - 40) / bh), 0.08, 2.2);
    tx = w / 2 - (x0 + bw / 2) * k;
    ty = hh / 2 - (y0 + bh / 2) * k;
    applyView();
  }
  function zoomAt(mx, my, factor) {
    const nk = clamp(k * factor, 0.08, 5);
    tx = mx - (mx - tx) * nk / k;
    ty = my - (my - ty) * nk / k;
    k = nk;
    touched = true;
    applyView();
  }
  function local(ev) { const r = svg.getBoundingClientRect(); return { x: ev.clientX - r.left, y: ev.clientY - r.top }; }

  const offs = [];
  offs.push(on(svg, 'wheel', (ev) => {
    ev.preventDefault();
    const p = local(ev);
    zoomAt(p.x, p.y, Math.exp(-ev.deltaY * (ev.deltaMode === 1 ? 0.05 : 0.0016)));
  }, { passive: false }));

  let pan = null;
  offs.push(on(svg, 'pointerdown', (ev) => {
    if (ev.button !== 0 || ev.target.closest('.gnode')) return;
    pan = { x: ev.clientX, y: ev.clientY, tx, ty };
    svg.setPointerCapture(ev.pointerId);
    svg.classList.add('panning');
  }));
  offs.push(on(svg, 'pointermove', (ev) => {
    if (!pan) return;
    tx = pan.tx + ev.clientX - pan.x;
    ty = pan.ty + ev.clientY - pan.y;
    touched = true;
    applyView();
  }));
  const endPan = () => { pan = null; svg.classList.remove('panning'); };
  offs.push(on(svg, 'pointerup', endPan));
  offs.push(on(svg, 'pointercancel', endPan));

  function startNodeDrag(ev, n) {
    if (ev.button !== 0) return;
    ev.stopPropagation();
    const start = { x: ev.clientX, y: ev.clientY };
    let moved = false;
    n.el.setPointerCapture(ev.pointerId);
    const move = (e2) => {
      if (!moved && Math.abs(e2.clientX - start.x) + Math.abs(e2.clientY - start.y) < 4) return;
      if (!moved) { moved = true; alphaTarget = 0.25; wake(); hideTip(); }
      const p = local(e2);
      n.fx = (p.x - tx) / k;
      n.fy = (p.y - ty) / k;
      touched = true;
    };
    const up = () => {
      n.el.removeEventListener('pointermove', move);
      n.el.removeEventListener('pointerup', up);
      n.el.removeEventListener('pointercancel', up);
      if (moved) { alphaTarget = 0; n.fx = null; n.fy = null; } else if (opts.onNodeClick) { hideTip(); opts.onNodeClick(n); }
    };
    n.el.addEventListener('pointermove', move);
    n.el.addEventListener('pointerup', up);
    n.el.addEventListener('pointercancel', up);
  }

  // ── Survol ──────────────────────────────────────────────────────────────
  function highlight(n) {
    nodes.forEach((x) => x.el.classList.remove('near'));
    if (!n) { svg.classList.remove('focus'); nodes.forEach((x) => x.el.classList.remove('hl')); edges.forEach((e) => e.el.classList.remove('hl')); pin(); return; }
    const near = neighbors.get(n.id);
    svg.classList.add('focus');
    for (const x of nodes) x.el.classList.toggle('hl', x === n || near.has(x.id));
    for (const e of edges) e.el.classList.toggle('hl', e.s === n || e.t === n);
  }
  // Hors survol : les nœuds trouvés restent en avant, leurs voisins à demi.
  function pin() {
    if (!matched.size) return;
    svg.classList.add('focus');
    for (const x of nodes) {
      x.el.classList.toggle('hl', matched.has(x.id));
      x.el.classList.toggle('near', !matched.has(x.id) && Array.from(matched).some((id) => neighbors.get(id).has(x.id)));
    }
    for (const e of edges) e.el.classList.toggle('hl', matched.has(e.s.id) || matched.has(e.t.id));
  }
  function showTip(ev, title, rows) {
    clear(tip);
    tip.appendChild(h('div', { class: 'tt' }, title));
    for (const [label, val] of rows || []) {
      if (val === undefined || val === null || val === '') continue;
      tip.appendChild(h('div', { class: 'tr' }, h('span', { class: 'l' }, label), h('span', { class: 'v' }, val)));
    }
    tip.hidden = false;
    moveTip(ev);
  }
  function moveTip(ev) {
    if (tip.hidden) return;
    tip.style.left = Math.min(window.innerWidth - tip.offsetWidth - 8, ev.clientX + 14) + 'px';
    tip.style.top = Math.min(window.innerHeight - tip.offsetHeight - 8, ev.clientY + 14) + 'px';
  }
  function hideTip() { tip.hidden = true; }

  // ── Boucle d'animation ──────────────────────────────────────────────────
  let raf = 0;
  let fitted = false;
  function frame() {
    raf = 0;
    if (!wrap.isConnected) { destroy(); return; }
    const t1 = performance.now();
    let n = 0;
    while (n < 3 && performance.now() - t1 < 8) { tick(); n++; }
    draw();
    if (!touched && (!fitted || alpha > 0.05)) { fit(); fitted = true; }
    for (const nd of nodes) positions.set(nd.id, { x: nd.x, y: nd.y });
    if (alpha > ALPHA_MIN || alphaTarget > 0) raf = requestAnimationFrame(frame);
  }
  function wake() { if (!raf) raf = requestAnimationFrame(frame); }

  // Graphe retiré de la page (widget redessiné, changement de page) : tout s'arrête.
  const ro = new ResizeObserver(() => { if (!wrap.isConnected) { destroy(); return; } if (!touched) fit(); });
  ro.observe(wrap);

  // Boutons de vue
  const tools = h('div', { class: 'fgraph-tools' },
    h('button', { class: 'btn sm icon', type: 'button', title: t('graph.zoom_in'), onclick: () => { const z = size(); zoomAt(z.w / 2, z.h / 2, 1.3); } }, '+'),
    h('button', { class: 'btn sm icon', type: 'button', title: t('graph.zoom_out'), onclick: () => { const z = size(); zoomAt(z.w / 2, z.h / 2, 1 / 1.3); } }, '−'),
    h('button', { class: 'btn sm', type: 'button', title: t('graph.fit_title'), onclick: () => { touched = false; fit(); } }, t('graph.fit')),
    h('button', { class: 'btn sm', type: 'button', title: t('graph.relayout_title'), onclick: () => { alpha = 0.8; touched = false; wake(); } }, t('graph.relayout')));
  wrap.appendChild(tools);
  if (!opts.compact) wrap.appendChild(h('div', { class: 'fgraph-hint xsmall muted' }, t('graph.hint')));

  draw();
  applyView();
  pin();
  requestAnimationFrame(() => { fit(); wake(); });

  function destroy() {
    if (raf) cancelAnimationFrame(raf);
    raf = 0;
    ro.disconnect();
    offs.forEach((f) => f());
    tip.remove();
  }
  wrap.destroy = destroy;
  wrap.fit = () => { touched = false; fit(); };
  wrap.nodeCount = nodes.length;
  return wrap;
}

function asIds(v) {
  if (!v) return [];
  return Array.from(v instanceof Set ? v : (Array.isArray(v) ? v : [v])).map(String);
}

// Identifiant propre à chaque graphe pour les marqueurs de flèches (plusieurs graphes par page).
let uidCounter = 0;
const uidSuffix = 'g' + Date.now().toString(36);
export function nextGraphId() { uidCounter++; return uidSuffix + uidCounter; }

// Légende des états et des formes.
export function graphLegend(kinds) {
  const st = [['ok', 'state.ok'], ['warn', 'state.warn'], ['alert', 'state.alert'], ['nodata', 'state.nodata']];
  return h('div', { class: 'fgraph-legend' },
    st.map(([k, label]) => h('span', h('i', { class: 'sw st-' + k }), t(label))),
    (kinds || []).map(([k, label]) => h('span', h('i', { class: 'shape k-' + k }), label)),
    h('span', h('i', { class: 'line' }), t('graph.legend.width')));
}
