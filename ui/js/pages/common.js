// Aides partagées par les pages.
import { h, clear } from '../dom.js';
import { t } from '../i18n.js';
import { get, asList, queryMetrics, isAbort } from '../api.js';
import { timeChart, linesFromMetrics } from '../components/chart.js';
import { errorState, select } from '../components/ui.js';
import { unitLabel } from '../format.js';

// Graphique alimenté par POST /metrics/query. Place le graphique (ou l'erreur) dans container.
export async function metricChart(container, o) {
  clear(container);
  const height = o.height || 180;
  container.appendChild(h('div', { class: 'skeleton', style: { height: height + 'px' } }));
  try {
    const resp = await queryMetrics(o.queries, o.range, o.maxPoints || 300, o.signal);
    const { xs, lines } = linesFromMetrics(resp, { names: o.names, display: o.displayById });
    if (o.colors) lines.forEach((l, i) => { if (o.colors[i]) l.color = o.colors[i]; });
    if (o.labels) lines.forEach((l) => { if (o.labels[l.query] && !Object.keys(l.group || {}).length) l.label = o.labels[l.query]; });
    const unit = o.unit || (lines.find((l) => l.unit) || {}).unit || null;
    const chart = timeChart({
      xs, lines, unit, height, display: o.display, stacked: o.stacked, xRange: [o.range.from, o.range.to],
      onRange: o.onRange, sync: o.sync, thresholds: o.thresholds, precision: o.precision, emptyText: o.emptyText, yMax: o.yMax,
    });
    clear(container);
    container.appendChild(chart);
    if (o.onData) o.onData(resp, lines, unit);
    return { resp, lines, unit, chart };
  } catch (e) {
    if (isAbort(e)) return null;
    clear(container);
    container.appendChild(errorState(e, () => metricChart(container, o)));
    return null;
  }
}

// Panneau titré contenant un graphique de métriques.
export function chartPanel(title, o) {
  const body = h('div', { class: 'panel-body' });
  const sub = h('span', { class: 'sub' }, o.unit ? unitLabel(o.unit) : '');
  const el = h('section', { class: 'panel' }, h('div', { class: 'panel-head' }, h('h3', title), sub, h('span', { class: 'spacer' }), o.actions || null), body);
  metricChart(body, Object.assign({}, o, {
    onData: (resp, lines, unit) => { if (!o.unit && unit) sub.textContent = unitLabel(unit); if (o.onData) o.onData(resp, lines, unit); },
  }));
  return el;
}

export async function loadHosts(signal, query) {
  return asList(await get('/hosts', query || null, { signal }), 'hosts');
}

// Sélecteur d'hôte : « Tous les hôtes » puis la liste triée.
export function hostSelect(hosts, value, onChange, allLabel) {
  const opts = [{ value: '', label: allLabel || t('host.all') }].concat(
    hosts.slice().sort((a, b) => String(a.hostname).localeCompare(String(b.hostname))).map((x) => ({ value: String(x.id), label: x.hostname || String(x.id) })));
  return select(opts, value || '', onChange, { style: { width: 'auto', minWidth: '180px' } });
}

export function hostLink(href, id, name) {
  if (id === null || id === undefined || id === '') return h('span', name || '-');
  return h('a', { href: href('/infrastructure/host/' + encodeURIComponent(id)) }, name || String(id));
}

// Échappe une valeur pour la syntaxe des requêtes (§ 7) : guillemets si espaces ou caractères spéciaux.
export function qv(v) {
  const str = String(v);
  return /^[A-Za-z0-9_.\-/:@*]+$/.test(str) ? str : '"' + str.replace(/\\/g, '\\\\').replace(/"/g, '\\"') + '"';
}

// Valeur d'étiquette pour une requête de métrique : {host:web-01}.
export function tagFilter(k, v) {
  const str = String(v);
  return k + ':' + (/^[^,{}\s]+$/.test(str) ? str : '"' + str.replace(/"/g, '\\"') + '"');
}

export function hostsDisplay(v) {
  if (Array.isArray(v)) return v.length <= 3 ? v.join(', ') : v.slice(0, 3).join(', ') + ' +' + (v.length - 3);
  if (v === null || v === undefined) return '-';
  return String(v);
}

export function hostsCount(v) {
  if (Array.isArray(v)) return v.length;
  if (typeof v === 'number') return v;
  return 0;
}
