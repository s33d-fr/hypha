// Arbre JSON dépliable. onKey(chemin, valeur) : clic sur une feuille (ajout d'un filtre, par exemple).
import { h } from '../dom.js';

function leaf(v) {
  if (v === null || v === undefined) return h('span', { class: 'z' }, 'null');
  if (typeof v === 'string') return h('span', { class: 's' }, JSON.stringify(v));
  if (typeof v === 'number') return h('span', { class: 'n' }, String(v));
  if (typeof v === 'boolean') return h('span', { class: 'b' }, String(v));
  return h('span', String(v));
}

function node(key, value, path, depth, onKey) {
  const isObj = value && typeof value === 'object';
  if (!isObj) {
    const k = key === null ? null : h('span', {
      class: ['k', onKey ? 'clickable' : null], title: onKey ? path : null,
      onclick: onKey ? () => onKey(path, value) : null,
    }, key + ': ');
    return h('li', h('span', { class: 'tog' }), k, leaf(value));
  }
  const entries = Array.isArray(value) ? value.map((v, i) => [String(i), v]) : Object.entries(value);
  const open = depth < 2;
  const child = h('ul', { hidden: !open });
  for (const [k, v] of entries) {
    const p = Array.isArray(value) ? path + '[' + k + ']' : (path ? path + '.' + k : k);
    child.appendChild(node(k, v, p, depth + 1, Array.isArray(value) ? null : onKey));
  }
  const tog = h('span', { class: 'tog' }, open ? '▾' : '▸');
  const summary = h('span', { class: 'z' }, Array.isArray(value) ? '[' + entries.length + ']' : '{' + entries.length + '}');
  const li = h('li', tog, key === null ? null : h('span', { class: 'k' }, key + ': '), summary, child);
  const flip = () => { child.hidden = !child.hidden; tog.textContent = child.hidden ? '▸' : '▾'; };
  tog.addEventListener('click', flip);
  summary.addEventListener('click', flip);
  summary.style.cursor = 'pointer';
  return li;
}

export function jsonTree(value, opts = {}) {
  const root = h('div', { class: 'jtree' });
  const ul = h('ul');
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    const entries = Object.entries(value);
    if (!entries.length) return h('div', { class: 'jtree muted' }, '{}');
    for (const [k, v] of entries) ul.appendChild(node(k, v, (opts.prefix || '') + k, 1, opts.onKey || null));
  } else {
    ul.appendChild(node(null, value, opts.prefix || '', 0, null));
  }
  root.appendChild(ul);
  return root;
}
