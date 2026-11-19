// Tableau compact, triable côté navigateur, en-tête collant.
import { h, clear } from '../dom.js';
import { t } from '../i18n.js';
import { EMPTY } from '../format.js';

// columns : [{key, label, sort (bool|fn valeur), align:'r', render(row) -> Node|string, value(row), width, cls, title}]
export function dataTable({ columns, rows, sortKey, sortDir = 'desc', onRowClick, rowClass, emptyText, maxRows, onSort, footer }) {
  const wrap = h('div', { class: 'table-wrap' });
  let key = sortKey || null;
  let dir = sortDir;
  let data = rows || [];

  function valueOf(col, row) {
    if (typeof col.sort === 'function') return col.sort(row);
    if (col.value) return col.value(row);
    return row[col.key];
  }

  function sorted() {
    const col = columns.find((c) => c.key === key);
    if (!col || col.sort === false) return data.slice();
    const mul = dir === 'asc' ? 1 : -1;
    return data.slice().sort((a, b) => {
      const va = valueOf(col, a);
      const vb = valueOf(col, b);
      const na = va === null || va === undefined || va === '';
      const nb = vb === null || vb === undefined || vb === '';
      if (na && nb) return 0;
      if (na) return 1;
      if (nb) return -1;
      if (typeof va === 'number' && typeof vb === 'number') return (va - vb) * mul;
      return String(va).localeCompare(String(vb), undefined, { numeric: true, sensitivity: 'base' }) * mul;
    });
  }

  function render() {
    clear(wrap);
    const table = h('table', { class: 'tbl' });
    const trh = h('tr');
    for (const col of columns) {
      const sortable = col.sort !== false;
      const th = h('th', {
        class: [col.align === 'r' ? 'r' : null, sortable ? 'sortable' : null, col.cls || null],
        style: col.width ? { width: col.width } : null, title: col.title || null,
        'aria-sort': key === col.key ? (dir === 'asc' ? 'ascending' : 'descending') : null,
      }, col.label, sortable ? h('span', { class: 'arrow' }, key === col.key ? (dir === 'asc' ? '▴' : '▾') : '') : null);
      if (sortable) th.addEventListener('click', () => {
        if (key === col.key) dir = dir === 'asc' ? 'desc' : 'asc';
        else { key = col.key; dir = col.defaultDir || (col.align === 'r' ? 'desc' : 'asc'); }
        if (onSort) onSort(key, dir);
        render();
      });
      trh.appendChild(th);
    }
    table.appendChild(h('thead', trh));
    const tbody = h('tbody');
    let list = sorted();
    const total = list.length;
    if (maxRows && list.length > maxRows) list = list.slice(0, maxRows);
    for (const row of list) {
      const tr = h('tr', { class: [onRowClick ? 'clickable' : null, rowClass ? rowClass(row) : null] });
      for (const col of columns) {
        let content;
        if (col.render) content = col.render(row);
        else {
          const v = row[col.key];
          content = v === null || v === undefined || v === '' ? EMPTY : String(v);
        }
        if (content === null || content === undefined) content = EMPTY;
        tr.appendChild(h('td', { class: [col.align === 'r' ? 'r num' : null, col.tdCls || null] }, content));
      }
      if (onRowClick) tr.addEventListener('click', (e) => {
        if (e.target.closest('a, button, input, select')) return;
        onRowClick(row, e);
      });
      tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    wrap.appendChild(table);
    if (!total) wrap.appendChild(h('div', { class: 'state sm' }, emptyText || t('empty.no_rows')));
    if (maxRows && total > maxRows) wrap.appendChild(h('div', { class: 'tbl-foot' }, t('table.truncated', { shown: maxRows, total })));
    if (footer) wrap.appendChild(footer);
  }

  render();
  wrap.update = (newRows) => { data = newRows || []; render(); };
  return wrap;
}

// Cellule « barre + valeur » pour les pourcentages.
export function barCell(pct, label, color) {
  const v = typeof pct === 'number' && Number.isFinite(pct) ? Math.max(0, Math.min(100, pct)) : null;
  return h('div', { class: 'bar-cell' },
    h('div', { class: 'bar' }, v === null ? null : h('i', { style: { width: v + '%', background: color || null } })),
    h('span', { class: 'v' }, label));
}

// Couleur d'une utilisation (0-100) sur l'échelle de la carte des hôtes.
export function usageColor(pct) {
  if (typeof pct !== 'number' || !Number.isFinite(pct)) return 'var(--hex-down)';
  if (pct < 25) return 'var(--hex-1)';
  if (pct < 50) return 'var(--hex-2)';
  if (pct < 75) return 'var(--hex-3)';
  if (pct < 90) return 'var(--hex-4)';
  return 'var(--hex-5)';
}
