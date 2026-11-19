// Graphiques temporels sur uPlot (MIT, ui/vendor). Un seul axe Y par graphique.
import { h, clear, cssVar } from '../dom.js';
import { t } from '../i18n.js';
import { fmtUnit, fmtAxis, fmtHM, fmtTime, fmtDayMonth, fmtDateTime, isNum } from '../format.js';

const live = new Set();

export function destroyAllCharts() {
  for (const c of Array.from(live)) c.destroy();
  live.clear();
}

export function palette() {
  return [1, 2, 3, 4, 5, 6, 7, 8].map((i) => cssVar('--s' + i));
}

export function colorAt(i) {
  const p = palette();
  return i < p.length ? p[i] : cssVar('--s-other');
}

export function alpha(color, a) {
  const c = (color || '').trim();
  if (c.startsWith('#') && (c.length === 7 || c.length === 4)) {
    const full = c.length === 4 ? '#' + c[1] + c[1] + c[2] + c[2] + c[3] + c[3] : c;
    const r = parseInt(full.slice(1, 3), 16);
    const g = parseInt(full.slice(3, 5), 16);
    const b = parseInt(full.slice(5, 7), 16);
    return 'rgba(' + r + ',' + g + ',' + b + ',' + a + ')';
  }
  return c;
}

// Aligne des séries [[ts, v], …] sur un axe commun (millisecondes).
export function alignPoints(list) {
  const set = new Set();
  for (const pts of list) for (const p of pts || []) if (p && isNum(p[0])) set.add(p[0]);
  const xs = Array.from(set).sort((a, b) => a - b);
  const index = new Map(xs.map((x, i) => [x, i]));
  const ys = list.map((pts) => {
    const arr = new Array(xs.length).fill(null);
    for (const p of pts || []) {
      if (!p) continue;
      const i = index.get(p[0]);
      if (i !== undefined) arr[i] = isNum(p[1]) ? p[1] : null;
    }
    return arr;
  });
  return { xs, ys };
}

export function groupLabel(group) {
  if (!group || typeof group !== 'object') return '';
  const entries = Object.entries(group);
  if (!entries.length) return '';
  return entries.map(([k, v]) => k + ':' + v).join(', ');
}

// Réponse de POST /metrics/query vers des lignes de graphique.
export function linesFromMetrics(resp, opts = {}) {
  const series = (resp && Array.isArray(resp.series)) ? resp.series : [];
  const queryIds = new Set(series.map((s2) => s2.query));
  const multi = queryIds.size > 1 || opts.forceMetricName;
  const { xs, ys } = alignPoints(series.map((s2) => s2.points || []));
  const lines = series.map((s2, i) => {
    const g = groupLabel(s2.group);
    const name = (opts.names && opts.names[s2.query]) || s2.metric || s2.query || '';
    let label = g || name;
    if (multi && g) label = name + ' ' + g;
    return { label, values: ys[i], unit: s2.unit || null, query: s2.query, group: s2.group || {}, metric: s2.metric, display: opts.display && opts.display[s2.query] };
  });
  return { xs, lines, interval_s: resp && resp.interval_s };
}

let measureCtx = null;
function textWidth(str, font) {
  if (!measureCtx) measureCtx = document.createElement('canvas').getContext('2d');
  measureCtx.font = font;
  return measureCtx.measureText(str).width;
}

function timeAxisValues(u, splits, axisIdx, foundSpace, foundIncr) {
  return splits.map((sec) => {
    if (!isNum(sec)) return '';
    const ms = sec * 1000;
    if (foundIncr >= 86400) return fmtDayMonth(ms);
    const d = new Date(ms);
    if (foundIncr >= 3600 && d.getHours() === 0 && d.getMinutes() === 0) return fmtDayMonth(ms);
    if (foundIncr < 60) return fmtTime(ms);
    return fmtHM(ms);
  });
}

// Crée un graphique. Retourne l'élément, avec .update(xs, lines) et .destroy().
// opts : {xs, lines:[{label, values, color, display}], unit, height, stacked, display, xRange:[from,to],
//         onRange(from,to), sync, legend, thresholds:[{value,color}], precision, emptyText, yMin, yMax,
//         bands:[{hi, lo, fill}] (indices de lignes), events:[{ts, title, color}] (repères verticaux),
//         yLog (axe logarithmique)} ; une ligne accepte aussi dash:[6,4] et width.
export function timeChart(opts) {
  const wrap = h('div', { class: 'chart' });
  const plotEl = h('div');
  const legendEl = h('div', { class: 'chart-legend' });
  wrap.appendChild(plotEl);
  if (opts.legend !== false) wrap.appendChild(legendEl);

  let state = { xs: opts.xs || [], lines: opts.lines || [] };
  const hidden = new Set();
  let u = null;
  let ro = null;
  let focused = null;
  const height = opts.height || 200;
  const unit = opts.unit || (state.lines[0] && state.lines[0].unit) || null;
  const stacked = !!opts.stacked;
  const font = '11px ' + (cssVar('--font') || 'system-ui');
  const handle = { destroy };

  function colors() {
    return state.lines.map((l, i) => l.color || colorAt(i));
  }

  function hasData() {
    return state.xs.length > 0 && state.lines.some((l) => l.values.some((v) => v !== null));
  }

  // Ordre logique (bas vers haut) ; en empilé, uPlot dessine du haut vers le bas.
  function uIndex(i) { return stacked ? state.lines.length - i : i + 1; }

  function buildData() {
    const xsS = state.xs.map((x) => x / 1000);
    if (!stacked) return [xsS].concat(state.lines.map((l) => l.values));
    const cum = new Array(state.xs.length).fill(0);
    const stacks = state.lines.map((l, i) => {
      if (hidden.has(i)) return l.values.map(() => null);
      return l.values.map((v, j) => (cum[j] += isNum(v) ? v : 0));
    });
    return [xsS].concat(stacks.slice().reverse());
  }

  function yRange(self, dmin, dmax) {
    if (!isNum(dmin) || !isNum(dmax)) return opts.yLog ? [1, 10] : [0, 1];
    if (opts.yLog) {
      const lo2 = dmin > 0 ? dmin : 0.1;
      const hi2 = Math.max(lo2 * 10, dmax);
      return [Math.pow(10, Math.floor(Math.log10(lo2))), Math.pow(10, Math.ceil(Math.log10(hi2)))];
    }
    let lo = opts.yMin !== undefined ? opts.yMin : Math.min(0, dmin);
    let hi = opts.yMax !== undefined ? opts.yMax : dmax;
    for (const th of opts.thresholds || []) if (isNum(th.value)) hi = Math.max(hi, th.value);
    if (hi === lo) hi = lo + (lo === 0 ? 1 : Math.abs(lo) * 0.1);
    const pad = (hi - lo) * 0.08;
    if (opts.yMax === undefined) hi += pad;
    if (lo < 0 && opts.yMin === undefined) lo -= pad;
    if (unit === 'percent' && dmax <= 100 && hi > 100 && opts.yMax === undefined) hi = Math.max(100, dmax);
    return [lo, hi];
  }

  function makeOpts(width) {
    const grid = cssVar('--chart-grid');
    const axisText = cssVar('--chart-text');
    const cols = colors();
    const series = [{}];
    const order = state.lines.map((l, i) => i);
    if (stacked) order.reverse();
    for (const i of order) {
      const l = state.lines[i];
      const col = cols[i];
      const disp = l.display || opts.display || 'line';
      const s2 = {
        label: l.label,
        stroke: col,
        width: disp === 'bars' ? 1 : 1.5,
        show: !hidden.has(i) || stacked,
        spanGaps: false,
        points: { size: 4, fill: col },
      };
      if (disp === 'bars') {
        s2.paths = window.uPlot.paths.bars({ size: [0.72, 64], gap: 1 });
        s2.fill = col;
        s2.points = { show: false };
      } else if (disp === 'area' || stacked) {
        s2.fill = stacked ? alpha(col, 0.9) : alpha(col, 0.16);
      }
      // Bornes d'anomalie et prévision (§ 19) : trait pointillé, épaisseur propre.
      if (Array.isArray(l.dash)) s2.dash = l.dash;
      if (isNum(l.width)) s2.width = l.width;
      series.push(s2);
    }
    const o = {
      width,
      height,
      padding: [10, 8, 0, 2],
      legend: { show: false },
      cursor: {
        drag: { x: !!opts.onRange, y: false, setScale: false },
        points: { size: 6 },
        focus: { prox: 24 },
      },
      focus: { alpha: 0.35 },
      scales: {
        x: { time: true },
        y: opts.yLog ? { range: yRange, distr: 3, log: 10 } : { range: yRange },
      },
      axes: [
        {
          stroke: axisText, font, size: 26, space: 70,
          grid: { show: false },
          ticks: { stroke: grid, width: 1, size: 4 },
          values: timeAxisValues,
        },
        {
          stroke: axisText, font, space: 28,
          grid: { stroke: grid, width: 1 },
          ticks: { show: false },
          values: (self, splits) => splits.map((v) => (opts.axisFmt ? opts.axisFmt(v) : fmtAxis(v, unit))),
          size: (self, values) => {
            if (!values || !values.length) return 44;
            let w = 0;
            for (const v of values) if (v) w = Math.max(w, textWidth(v, font));
            return Math.ceil(w) + 14;
          },
        },
      ],
      series,
      hooks: {
        setCursor: [onCursor],
        setSelect: [onSelect],
        setSeries: [(self, sidx, o2) => { if (o2 && o2.focus) focused = sidx; else if (sidx === null) focused = null; }],
        draw: [drawThresholds, drawEvents],
      },
    };
    if (opts.xRange) {
      const fr = opts.xRange[0] / 1000;
      const to = opts.xRange[1] / 1000;
      o.scales.x.range = () => [fr, to];
    }
    if (opts.sync) o.cursor.sync = { key: opts.sync, setSeries: false };
    // Bandes entre deux lignes (indices logiques) : bornes d'anomalie, intervalle de prévision.
    if (Array.isArray(opts.bands) && opts.bands.length && !stacked) {
      o.bands = opts.bands.filter((b) => b && isNum(b.hi) && isNum(b.lo) && state.lines[b.hi] && state.lines[b.lo])
        .map((b) => ({ series: [uIndex(b.hi), uIndex(b.lo)], fill: b.fill || alpha(cols[b.hi], 0.12) }));
    }
    return o;
  }

  function drawThresholds(self) {
    if (!opts.thresholds || !opts.thresholds.length) return;
    const ctx = self.ctx;
    const { left, top, width: w, height: hgt } = self.bbox;
    ctx.save();
    for (const th of opts.thresholds) {
      if (!isNum(th.value)) continue;
      const y = Math.round(self.valToPos(th.value, 'y', true)) + 0.5;
      if (y < top || y > top + hgt) continue;
      ctx.strokeStyle = th.color === 'warn' ? cssVar('--warn') : th.color === 'ok' ? cssVar('--ok') : th.color && th.color.startsWith('#') ? th.color : cssVar('--crit');
      ctx.lineWidth = 1 * (window.devicePixelRatio || 1);
      ctx.setLineDash([6 * (window.devicePixelRatio || 1), 4 * (window.devicePixelRatio || 1)]);
      ctx.beginPath();
      ctx.moveTo(left, y);
      ctx.lineTo(left + w, y);
      ctx.stroke();
    }
    ctx.restore();
  }

  // Repères d'événements (déploiements…) : trait vertical pointillé et petit triangle en haut.
  function drawEvents(self) {
    if (!Array.isArray(opts.events) || !opts.events.length) return;
    const ctx = self.ctx;
    const { left, top, width: w, height: hgt } = self.bbox;
    const dpr = window.devicePixelRatio || 1;
    ctx.save();
    for (const ev of opts.events) {
      if (!isNum(ev.ts)) continue;
      const x = Math.round(self.valToPos(ev.ts / 1000, 'x', true)) + 0.5;
      if (x < left || x > left + w) continue;
      const col = ev.color && ev.color.startsWith('#') ? ev.color : cssVar(ev.color || '--s7') || '#9085e9';
      ctx.strokeStyle = col;
      ctx.fillStyle = col;
      ctx.lineWidth = dpr;
      ctx.setLineDash([3 * dpr, 3 * dpr]);
      ctx.beginPath();
      ctx.moveTo(x, top);
      ctx.lineTo(x, top + hgt);
      ctx.stroke();
      ctx.setLineDash([]);
      ctx.beginPath();
      ctx.moveTo(x - 4 * dpr, top);
      ctx.lineTo(x + 4 * dpr, top);
      ctx.lineTo(x, top + 6 * dpr);
      ctx.closePath();
      ctx.fill();
    }
    ctx.restore();
  }

  function eventsNear(self, left) {
    if (!Array.isArray(opts.events) || !opts.events.length) return [];
    return opts.events.filter((ev) => isNum(ev.ts) && Math.abs(self.valToPos(ev.ts / 1000, 'x') - left) <= 6);
  }

  const tip = h('div', { class: 'chart-tip', hidden: true });

  function onCursor(self) {
    const idx = self.cursor.idx;
    const left = self.cursor.left;
    if (idx === null || idx === undefined || left === undefined || left < 0) { tip.hidden = true; return; }
    const cols = colors();
    const rows = [];
    state.lines.forEach((l, i) => {
      if (hidden.has(i)) return;
      const v = l.values[idx];
      rows.push({ i, label: l.label, v, color: cols[i] });
    });
    if (!rows.length) { tip.hidden = true; return; }
    if (stacked) rows.reverse(); else rows.sort((a, b) => (isNum(b.v) ? b.v : -Infinity) - (isNum(a.v) ? a.v : -Infinity));
    clear(tip);
    tip.appendChild(h('div', { class: 'tt' }, fmtDateTime(state.xs[idx])));
    const max = 12;
    const fIdx = focused !== null ? (stacked ? state.lines.length - focused : focused - 1) : null;
    rows.slice(0, max).forEach((r) => {
      tip.appendChild(h('div', { class: ['tr', r.i === fIdx ? 'hl' : null] },
        h('span', { class: 'sw', style: { background: r.color } }),
        h('span', { class: 'l' }, r.label || t('chart.value')),
        h('span', { class: 'v' }, opts.valueFmt ? opts.valueFmt(r.v) : fmtUnit(r.v, l0unit(r.i), opts.precision))));
    });
    if (rows.length > max) tip.appendChild(h('div', { class: 'tt' }, t('chart.more_series', { n: rows.length - max })));
    for (const ev of eventsNear(self, left).slice(0, 4)) {
      tip.appendChild(h('div', { class: 'tr' }, h('span', { class: 'sw', style: { background: ev.color && ev.color.startsWith('#') ? ev.color : 'var(' + (ev.color || '--s7') + ')', transform: 'rotate(45deg)' } }), h('span', { class: 'l' }, ev.title || t('chart.event')), h('span', { class: 'v' }, fmtTime(ev.ts))));
    }
    if (stacked && rows.length > 1) {
      const total = rows.reduce((acc, r) => acc + (isNum(r.v) ? r.v : 0), 0);
      tip.appendChild(h('div', { class: 'tr' }, h('span', { class: 'sw' }), h('span', { class: 'l' }, t('chart.total')), h('span', { class: 'v' }, fmtUnit(total, unit, opts.precision))));
    }
    tip.hidden = false;
    const ow = self.over.clientWidth;
    const tw = tip.offsetWidth;
    const th = tip.offsetHeight;
    let x = left + 14;
    if (x + tw > ow) x = Math.max(0, left - tw - 14);
    let y = (self.cursor.top || 0) - th / 2;
    y = Math.max(-8, Math.min(y, self.over.clientHeight - th + 8));
    tip.style.transform = 'translate(' + Math.round(x) + 'px,' + Math.round(y) + 'px)';
    tip.style.left = '0';
    tip.style.top = '0';
  }

  function l0unit(i) { return (state.lines[i] && state.lines[i].unit) || unit; }

  function onSelect(self) {
    const sel = self.select;
    if (opts.onRange && sel && sel.width > 4) {
      const a = self.posToVal(sel.left, 'x') * 1000;
      const b = self.posToVal(sel.left + sel.width, 'x') * 1000;
      if (isNum(a) && isNum(b) && b > a) opts.onRange(Math.round(a), Math.round(b));
    }
    if (sel && sel.width > 0) self.setSelect({ left: 0, top: 0, width: 0, height: 0 }, false);
  }

  function renderLegend() {
    clear(legendEl);
    if (opts.legend === false || state.lines.length < 2) { legendEl.hidden = true; return; }
    legendEl.hidden = false;
    const cols = colors();
    const max = opts.legendMax || 24;
    state.lines.slice(0, max).forEach((l, i) => {
      const item = h('span', { class: ['li', hidden.has(i) ? 'off' : null], title: l.label },
        h('span', { class: 'sw', style: { background: cols[i] } }), h('span', l.label || t('chart.value')));
      item.addEventListener('click', (e) => toggle(i, e.shiftKey || e.metaKey || e.ctrlKey));
      legendEl.appendChild(item);
    });
    if (state.lines.length > max) legendEl.appendChild(h('span', { class: 'more' }, t('chart.more_series', { n: state.lines.length - max })));
  }

  // Clic : masque/affiche ; Maj+clic : isole la série.
  function toggle(i, solo) {
    if (solo) {
      const onlyThis = state.lines.every((l, j) => (j === i ? !hidden.has(j) : hidden.has(j)));
      hidden.clear();
      if (!onlyThis) state.lines.forEach((l, j) => { if (j !== i) hidden.add(j); });
    } else if (hidden.has(i)) hidden.delete(i); else hidden.add(i);
    if (u) {
      if (stacked) u.setData(buildData());
      else state.lines.forEach((l, j) => u.setSeries(uIndex(j), { show: !hidden.has(j) }));
    }
    renderLegend();
  }

  function create() {
    const width = Math.max(120, Math.floor(plotEl.clientWidth || wrap.clientWidth));
    if (u) { u.destroy(); u = null; }
    clear(plotEl);
    if (!hasData()) {
      plotEl.appendChild(h('div', { class: 'chart-empty', style: { height: height + 'px' } }, opts.emptyText || t('chart.no_data')));
      legendEl.hidden = true;
      return;
    }
    u = new window.uPlot(makeOpts(width), buildData(), plotEl);
    u.over.appendChild(tip);
    renderLegend();
  }

  let lastW = 0;
  function start() {
    if (ro) return;
    ro = new ResizeObserver(() => {
      if (!wrap.isConnected) return;
      const w = Math.floor(wrap.clientWidth);
      if (w <= 0 || w === lastW) return;
      lastW = w;
      if (!u) create();
      else u.setSize({ width: Math.max(120, w), height });
    });
    ro.observe(wrap);
  }

  function destroy() {
    if (ro) { ro.disconnect(); ro = null; }
    if (u) { u.destroy(); u = null; }
    live.delete(handle);
  }

  wrap.update = (xs, lines) => {
    state = { xs: xs || [], lines: lines || [] };
    for (const i of Array.from(hidden)) if (i >= state.lines.length) hidden.delete(i);
    if (lastW > 0) create();
  };
  wrap.destroy = destroy;
  live.add(handle);
  start();
  return wrap;
}

// Graphique de métriques complet : titre, requête, rendu ; gère attente et erreur.
export function chartBox({ title, unitHint, height = 180 }) {
  const box = h('div', { class: 'panel' });
  const head = h('div', { class: 'panel-head' }, h('h3', title || ''), unitHint ? h('span', { class: 'sub' }, unitHint) : null);
  const body = h('div', { class: 'panel-body' }, h('div', { class: 'skeleton', style: { height: height + 'px' } }));
  box.appendChild(head);
  box.appendChild(body);
  box.body = body;
  box.head = head;
  return box;
}

// Résumé d'une série pour les tableaux (moyenne, min, max, dernière valeur).
export function summarize(values) {
  let n = 0; let sum = 0; let min = Infinity; let max = -Infinity; let last = null;
  for (const v of values || []) {
    if (!isNum(v)) continue;
    n++; sum += v; if (v < min) min = v; if (v > max) max = v; last = v;
  }
  return n ? { avg: sum / n, min, max, last, count: n } : { avg: null, min: null, max: null, last: null, count: 0 };
}
