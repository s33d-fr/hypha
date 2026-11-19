// Certificats (§ 33) : ceux que les agents lisent sur les hôtes (fichiers, configurations des serveurs web
// et de messagerie, magasin Windows) et ceux que les tests synthétiques voient sur le réseau, triés par
// échéance ; filtre texte et par état, export CSV.
import { h, clear, icon } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, asList } from '../api.js';
import { fmtDate, fmtDateTime, fmtRel, fmtInt, isNum } from '../format.js';
import { empty, panel, searchInput, pills, toast } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { hostLink } from './common.js';
import { load, tile, download, toCsv, stamp } from './kit.js';

const BADGE = { 0: 'ok', 1: 'warn', 2: 'alert', 3: 'muted' };

function daysCell(r) {
  if (!isNum(r.days_left)) return h('span', { class: 'muted' }, '-');
  const d = r.days_left;
  const label = d < 0 ? t('cert.expired_since', { n: fmtInt(Math.ceil(-d)) }) : tn('cert.days', Math.floor(d));
  return h('span', { class: 'badge ' + (BADGE[r.status] || 'muted') }, label);
}

function whereCell(r, href) {
  if (r.source === 'agent') {
    return h('span', { class: 'col', style: { gap: '2px' } }, hostLink(href, r.host_id, r.hostname),
      h('span', { class: 'mono xsmall muted ellipsis', title: r.where || '' }, r.where || '-'));
  }
  return h('span', { class: 'col', style: { gap: '2px' } },
    h('a', { href: href('/synthetics/' + r.test_id) }, r.name || r.where),
    h('span', { class: 'mono xsmall muted' }, (r.where || '') + (r.location && r.location !== 'server' ? ' · ' + r.location : '')));
}

export async function render(root, ctx) {
  const { params, signal, href } = ctx;
  ctx.setCrumbs([{ label: t('nav.certificates') }]);
  let q = params.get('q') || '';
  let only = params.get('only') || 'all';

  const tilesEl = h('div', { class: 'mb-3' });
  const listEl = h('div');
  const countEl = h('span', { class: 'sub' });
  const search = searchInput({ placeholder: t('cert.search_ph'), value: q, onInput: (v) => { q = v; ctx.setParams({ q: v || null }, { silent: true }); draw(); } });
  search.style.flex = '1 1 280px';
  const csvBtn = h('button', { class: 'btn', type: 'button', onclick: () => exportCsv() }, icon('download', 13), t('inv.export_csv'));
  root.appendChild(h('div', { class: 'querybar' }, search,
    pills([{ key: 'all', label: t('cert.only.all') }, { key: 'problems', label: t('cert.only.problems') }, { key: 'agent', label: t('cert.only.agent') }, { key: 'synthetic', label: t('cert.only.synthetic') }], only,
      (k) => { only = k; ctx.setParams({ only: k === 'all' ? null : k }, { silent: true }); draw(); }),
    csvBtn));
  root.appendChild(tilesEl);
  root.appendChild(h('div', { class: 'mb-3' }, panel(t('cert.list'), listEl, { flush: true, actions: countEl })));
  root.appendChild(h('div', { class: 'small muted' }, t('cert.footer')));

  let rows = [];
  function filtered() {
    const needle = q.trim().toLowerCase();
    return rows.filter((r) => {
      if (only === 'problems' && !(r.status === 1 || r.status === 2)) return false;
      if ((only === 'agent' || only === 'synthetic') && r.source !== only) return false;
      if (!needle) return true;
      const hay = [r.subject, r.issuer, r.hostname, r.name, r.where, r.origin, r.sans].join(' ').toLowerCase();
      return needle.split(/\s+/).every((w) => hay.includes(w));
    });
  }

  const columns = [
    { key: 'days_left', label: t('cert.col.days'), value: (r) => (isNum(r.days_left) ? r.days_left : 1e9), render: daysCell },
    { key: 'subject', label: t('cert.col.subject'), tdCls: 'trunc', render: (r) => h('span', { class: 'col', style: { gap: '2px' } },
      h('span', { class: 'mono', title: r.sans || r.subject || '' }, r.subject || '-'),
      r.sans ? h('span', { class: 'xsmall muted ellipsis', title: r.sans }, r.sans) : null) },
    { key: 'issuer', label: t('cert.col.issuer'), render: (r) => h('span', { class: 'small text-2' }, r.issuer || '-') },
    { key: 'where', label: t('cert.col.where'), value: (r) => r.hostname || r.name || r.where, render: (r) => whereCell(r, href) },
    { key: 'origin', label: t('cert.col.origin'), render: (r) => h('span', { class: 'badge muted' }, t('cert.origin.' + (r.origin || 'file'))) },
    { key: 'not_after', label: t('cert.col.not_after'), align: 'r', value: (r) => r.not_after || 0, render: (r) => h('span', { title: r.not_after ? fmtDateTime(r.not_after) : '' }, r.not_after ? fmtDate(r.not_after) : '-') },
    { key: 'ts', label: t('cert.col.seen'), align: 'r', value: (r) => Number(new Date(r.ts)) || 0, render: (r) => h('span', { class: 'small muted', title: fmtDateTime(r.ts) }, fmtRel(r.ts)) },
  ];

  function draw() {
    const list = filtered();
    countEl.textContent = tn('cert.count', list.length, { total: fmtInt(rows.length) });
    clear(listEl);
    if (!rows.length) { listEl.appendChild(empty(t('cert.none_title'), t('cert.none_text'))); return; }
    listEl.appendChild(dataTable({
      columns, rows: list, sortKey: params.get('sort') || 'days_left', sortDir: params.get('dir') || 'asc',
      onSort: (k, d) => ctx.setParams({ sort: k, dir: d }, { silent: true }),
      emptyText: t('cert.no_match'),
    }));
  }

  function exportCsv() {
    const list = filtered();
    if (!list.length) { toast(t('inv.export_empty'), 'info'); return; }
    const csv = toCsv([
      { label: 'subject', value: (r) => r.subject },
      { label: 'sans', value: (r) => r.sans },
      { label: 'issuer', value: (r) => r.issuer },
      { label: 'source', value: (r) => r.source },
      { label: 'origin', value: (r) => r.origin },
      { label: 'host_or_test', value: (r) => r.hostname || r.name },
      { label: 'where', value: (r) => r.where },
      { label: 'not_after', value: (r) => (r.not_after ? new Date(r.not_after).toISOString() : '') },
      { label: 'days_left', value: (r) => r.days_left },
      { label: 'status', value: (r) => r.status },
    ], list);
    download('hypha-certificats-' + stamp() + '.csv', csv, 'text/csv;charset=utf-8');
  }

  await load(tilesEl, () => get('/certificates', null, { signal }), (data) => {
    data = data || {};
    rows = asList(data.items);
    const s = data.summary || {};
    draw();
    return h('div', { class: 'tiles' },
      tile(t('cert.tile.total'), fmtInt(s.total || rows.length)),
      tile(t('cert.tile.expired'), fmtInt(s.expired || 0), null, s.expired > 0 ? 'var(--crit)' : null),
      tile(t('cert.tile.critical'), fmtInt(s.critical || 0), t('cert.tile.critical_sub', { n: data.crit_days || 7 }), s.critical > 0 ? 'var(--crit)' : null),
      tile(t('cert.tile.warning'), fmtInt(s.warning || 0), t('cert.tile.warning_sub', { n: data.warn_days || 21 }), s.warning > 0 ? 'var(--warn)' : null),
      tile(t('cert.tile.ok'), fmtInt(s.ok || 0)));
  });
}
