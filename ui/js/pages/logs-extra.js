// Journaux, ajouts de la 0.4.0 (§ 23) : motifs, contexte d'une ligne, facettes de sécurité.
import { h, clear, icon } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, asList } from '../api.js';
import { fmtInt, fmtRatioPct, fmtRel, fmtDateTime, fmtDateTimeFull, isNum, toMs } from '../format.js';
import { empty, drawer, copyButton } from '../components/ui.js';
import { dataTable, barCell } from '../components/table.js';
import { stackBar } from '../components/spark.js';
import { normStatus, statusLabel, statusColor, STATUSES } from './logs-shared.js';
import { qv } from './common.js';
import { load } from './kit.js';

// Facettes de sécurité (analyse sshd, sudo, su, fail2ban à l'arrivée).
export const SECURITY_FACETS = [
  ['@evt.name', 'logs.facet.evt_name'],
  ['@evt.outcome', 'logs.facet.evt_outcome'],
  ['@usr.name', 'logs.facet.usr_name'],
  ['@network.client.ip', 'logs.facet.client_ip'],
  ['@fail2ban.jail', 'logs.facet.jail'],
];

export function outcomeColor(v) {
  const s2 = String(v || '').toLowerCase();
  return s2 === 'failure' ? 'var(--crit)' : s2 === 'success' ? 'var(--ok)' : 'var(--nodata)';
}

// Raccourcis de recherche « sécurité ».
export const SECURITY_SHORTCUTS = [
  ['logs.sec.auth_fail', '@evt.name:authentication @evt.outcome:failure'],
  ['logs.sec.auth_ok', '@evt.name:authentication @evt.outcome:success'],
  ['logs.sec.sudo', '@evt.name:sudo'],
  ['logs.sec.ban', '@evt.name:ban'],
];

const PLACEHOLDER = /(<(?:num|uuid|hex|ip|path|ts|str|\*)>)/g;

function patternText(p) {
  const el = h('span', { class: 'pattern mono' });
  String(p || '').split(PLACEHOLDER).forEach((part, i) => {
    if (!part) return;
    el.appendChild(i % 2 ? h('span', { class: 'ph', title: t('logs.patterns.ph.' + part.slice(1, -1)) }, part) : document.createTextNode(part));
  });
  return el;
}

function statusParts(st) {
  if (!st || typeof st !== 'object') return [];
  const order = STATUSES.slice().reverse();
  const agg = {};
  for (const [k, v] of Object.entries(st)) { const n = normStatus(k); agg[n] = (agg[n] || 0) + (isNum(v) ? v : 0); }
  return order.filter((k) => agg[k] > 0).map((k) => ({ value: agg[k], color: statusColor(k), label: statusLabel(k) }));
}

// Onglet « Motifs » : regroupement des messages en gabarits ; clic = filtrer sur le motif.
export function renderPatterns(container, ctx, q, opts = {}) {
  const { range, signal } = ctx;
  clear(container);
  return load(container, async () => asList(await get('/logs/patterns', { q, from: range.from, to: range.to, limit: 50 }, { signal }), 'patterns'), (list) => {
    if (!list.length) return empty(t('logs.patterns.none_title'), t('logs.patterns.none_text'));
    const total = list.reduce((a, p) => a + (isNum(p.count) ? p.count : 0), 0);
    const maxShare = Math.max(0.0001, ...list.map((p) => (isNum(p.share) ? p.share : 0)));
    const box = h('div');
    box.appendChild(h('div', { class: 'tbl-foot', style: { borderTop: '0', borderBottom: '1px solid var(--border)' } }, tn('logs.patterns.count', list.length, { lines: fmtInt(total) }), h('span', { class: 'spacer' }), t('logs.patterns.sample_note')));
    box.appendChild(dataTable({
      columns: [
        { key: 'count', label: t('logs.patterns.lines'), align: 'r', render: (p) => fmtInt(p.count) },
        { key: 'share', label: t('lf.vol.share'), align: 'r', render: (p) => barCell(isNum(p.share) ? p.share / maxShare * 100 : null, fmtRatioPct(p.share, 1)) },
        { key: 'status', label: t('col.status'), sort: false, render: (p) => stackBar(statusParts(p.status), { width: 70, title: Object.entries(p.status || {}).map(([k, v]) => statusLabel(k) + ' ' + v).join(' · ') }) },
        { key: 'pattern', label: t('logs.patterns.pattern'), tdCls: 'wrap', render: (p) => h('div', { style: { minWidth: '320px' } }, patternText(p.pattern),
          p.sample ? h('div', { class: 'xsmall muted mono ellipsis', style: { maxWidth: '720px' }, title: p.sample }, t('logs.patterns.example') + ' ' + p.sample) : null) },
        { key: 'services', label: t('col.service'), value: (p) => (Array.isArray(p.services) ? p.services.join(',') : ''), render: (p) => h('span', { class: 'small text-2' }, (Array.isArray(p.services) ? p.services.slice(0, 3).join(', ') + (p.services.length > 3 ? ' +' + (p.services.length - 3) : '') : '') || '-') },
        { key: 'last_ts', label: t('logs.patterns.last'), align: 'r', value: (p) => toMs(p.last_ts), render: (p) => h('span', { title: t('logs.patterns.first') + ' ' + fmtDateTime(p.first_ts) }, fmtRel(p.last_ts)) },
        opts.onExclude ? { key: 'act', label: '', sort: false, render: (p) => (p.query ? h('button', { class: 'btn ghost sm', type: 'button', title: t('lf.exclude_this'), onclick: (e) => { e.stopPropagation(); opts.onExclude(p.query); } }, icon('filter', 12)) : null) } : null,
      ].filter(Boolean),
      rows: list, sortKey: 'count', sortDir: 'desc',
      onRowClick: (p) => { if (p.query && opts.onPick) opts.onPick(p.query); },
      emptyText: t('logs.patterns.none_title'),
    }));
    return box;
  });
}

// « Voir le contexte » : lignes du même hôte et service autour de la ligne choisie.
export function openContext(ctx, log) {
  const body = h('div');
  const tsMs = toMs(log.ts);
  const q = [log.hostname ? 'host:' + qv(log.hostname) : null, log.service ? 'service:' + qv(log.service) : null].filter(Boolean).join(' ');
  const openSearch = h('a', {
    class: 'btn sm', href: ctx.href('/logs', { q: q || null, t: null, from: tsMs ? String(tsMs - 5 * 60e3) : null, to: tsMs ? String(tsMs + 5 * 60e3) : null, focus: log.id, tab: null }),
  }, icon('search', 12), t('logs.context.open_search'));
  const d = drawer({ title: t('logs.context.title') + (log.service ? ' · ' + log.service : '') + (log.hostname ? ' · ' + log.hostname : ''), body, actions: openSearch });
  openSearch.addEventListener('click', () => d.close());
  load(body, () => get('/logs/' + encodeURIComponent(log.id) + '/context', { ts: tsMs || undefined, before: 20, after: 20 }), (r) => {
    let lines = [];
    if (r && (Array.isArray(r.before) || Array.isArray(r.after))) lines = asList(r.before).concat(r.line ? [r.line] : [], asList(r.after));
    else lines = asList(r, 'logs', 'lines');
    if (!lines.some((l) => String(l.id) === String(log.id))) lines.push(log);
    lines.sort((a, b) => (toMs(a.ts) || 0) - (toMs(b.ts) || 0) || Number(a.id || 0) - Number(b.id || 0));
    if (lines.length <= 1) return h('div', empty(null, t('logs.context.none')), ctxLine(log, true));
    const box = h('div', { class: 'log-list ctx-list' });
    box.appendChild(h('div', { class: 'xsmall muted mb-2' }, t('logs.context.hint')));
    let focusEl = null;
    for (const l of lines) {
      const el = ctxLine(l, String(l.id) === String(log.id));
      if (String(l.id) === String(log.id)) focusEl = el;
      box.appendChild(el);
    }
    if (focusEl) setTimeout(() => focusEl.scrollIntoView({ block: 'center' }), 0);
    return box;
  });
  return d;
}

function ctxLine(l, current) {
  const st = normStatus(l.status);
  return h('div', { class: ['ctx-row', current ? 'cur' : null] },
    h('span', { class: ['status-bar', 'st-' + st] }),
    h('span', { class: 'meta num', title: fmtDateTimeFull(l.ts) }, fmtDateTimeFull(l.ts).slice(-12)),
    h('span', { class: ['meta', 'st-text-' + st] }, statusLabel(st)),
    h('span', { class: 'msg' }, l.message || ''),
    current ? copyButton(() => l.message || '', { label: false }) : null);
}

// Panneau « Sécurité » des facettes : raccourcis de recherche prêts à l'emploi.
export function securityShortcuts(onPick) {
  return h('div', { class: 'facet' }, h('h4', t('logs.sec.title')),
    SECURITY_SHORTCUTS.map(([key, query]) => h('button', { type: 'button', class: 'facet-row', title: query, onclick: () => onPick(query) }, icon('shield', 11), h('span', { class: 'ellipsis' }, t(key)))));
}

