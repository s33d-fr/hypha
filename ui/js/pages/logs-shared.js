// Journaux : statuts, couleurs, histogramme empilé, ligne compacte. Partagé par plusieurs pages.
import { h, cssVar } from '../dom.js';
import { t } from '../i18n.js';
import { timeChart } from '../components/chart.js';
import { fmtDateTime, toMs } from '../format.js';

// Du bas vers le haut de la pile.
export const STATUSES = ['debug', 'info', 'notice', 'warning', 'error', 'critical', 'alert', 'emergency'];

export function normStatus(st) {
  const s2 = String(st || 'info').toLowerCase();
  if (s2 === 'warn') return 'warning';
  if (s2 === 'err') return 'error';
  if (s2 === 'crit' || s2 === 'fatal') return 'critical';
  return STATUSES.includes(s2) ? s2 : 'info';
}

export function statusColor(st) { return cssVar('--log-' + normStatus(st)); }

export function statusLabel(st) { return t('logstatus.' + normStatus(st)); }

// Réponse de GET /logs/histogram : {"interval_s", "buckets":[[ts, {"error":3}], …]}.
export function histogramChart(resp, range, opts = {}) {
  const buckets = (resp && Array.isArray(resp.buckets)) ? resp.buckets : [];
  const xs = [];
  const present = new Set();
  for (const b of buckets) {
    if (!Array.isArray(b)) continue;
    xs.push(toMs(b[0]));
    for (const k of Object.keys(b[1] || {})) present.add(normStatus(k));
  }
  const order = STATUSES.filter((s2) => present.has(s2));
  const lines = order.map((st) => ({
    label: statusLabel(st),
    color: statusColor(st),
    values: buckets.map((b) => {
      const counts = (Array.isArray(b) && b[1]) || {};
      let sum = 0;
      let any = false;
      for (const [k, v] of Object.entries(counts)) if (normStatus(k) === st && typeof v === 'number') { sum += v; any = true; }
      return any ? sum : 0;
    }),
    unit: 'count',
  }));
  return timeChart({
    xs, lines, unit: 'count', height: opts.height || 110, display: 'bars', stacked: true,
    xRange: [range.from, range.to], onRange: opts.onRange, legend: opts.legend !== false, emptyText: t('logs.no_logs_range'), precision: 0,
  });
}

// Ligne compacte (widget, vue d'ensemble, fiche hôte).
export function compactLogRow(log, onClick) {
  const st = normStatus(log.status);
  return h('div', { class: 'log-row', style: { gridTemplateColumns: '3px 130px 70px minmax(0,1fr)' }, onclick: onClick ? () => onClick(log) : null },
    h('span', { class: ['sbar', 'status-bar', 'st-' + st] }),
    h('span', { class: 'meta num' }, fmtDateTime(log.ts)),
    h('span', { class: ['meta', 'st-text-' + st] }, statusLabel(st)),
    h('span', { class: 'msg', title: log.message || '' }, (log.service ? '[' + log.service + '] ' : '') + (log.message || '')));
}
