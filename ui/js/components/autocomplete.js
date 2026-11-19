// Champ avec suggestions asynchrones (noms de métriques, valeurs d'étiquettes).
import { h, clear, debounce } from '../dom.js';
import { isAbort } from '../api.js';

// fetcher(texte, signal) -> Promise<[string | {value, label}]>
export function autocomplete({ value = '', placeholder = '', fetcher, onSelect, onInput, cls, mono = true, minChars = 0, attrs = {} }) {
  const input = h('input', Object.assign({
    class: ['input', mono ? 'mono' : null, cls || null], value, placeholder, spellcheck: 'false', autocomplete: 'off',
  }, attrs));
  const wrap = h('div', { class: 'ac' }, input);
  let menu = null;
  let items = [];
  let hl = -1;
  let ctrl = null;

  function close() { if (menu) { menu.remove(); menu = null; } hl = -1; }

  function render() {
    if (!items.length) { close(); return; }
    if (!menu) { menu = h('div', { class: 'menu', role: 'listbox' }); wrap.appendChild(menu); }
    clear(menu);
    items.forEach((it, i) => {
      const label = typeof it === 'string' ? it : (it.label || it.value);
      const el = h('button', { type: 'button', class: ['menu-item', i === hl ? 'hl' : null], role: 'option' }, label);
      el.addEventListener('mousedown', (e) => { e.preventDefault(); choose(i); });
      menu.appendChild(el);
    });
  }

  function choose(i) {
    const it = items[i];
    if (it === undefined) return;
    const v = typeof it === 'string' ? it : it.value;
    input.value = v;
    close();
    if (onSelect) onSelect(v);
  }

  const load = debounce(async () => {
    const q = input.value;
    if (q.length < minChars) { items = []; close(); return; }
    if (ctrl) ctrl.abort();
    ctrl = new AbortController();
    try {
      const res = await fetcher(q, ctrl.signal);
      if (document.activeElement !== input) return;
      items = (res || []).slice(0, 50);
      hl = -1;
      render();
    } catch (e) {
      if (!isAbort(e)) { items = []; close(); }
    }
  }, 160);

  input.addEventListener('input', () => { if (onInput) onInput(input.value); load(); });
  input.addEventListener('focus', () => load());
  input.addEventListener('blur', () => setTimeout(close, 120));
  input.addEventListener('keydown', (e) => {
    if (!menu) { if (e.key === 'Enter' && onSelect) { e.preventDefault(); onSelect(input.value); } return; }
    if (e.key === 'ArrowDown') { e.preventDefault(); hl = Math.min(items.length - 1, hl + 1); render(); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); hl = Math.max(0, hl - 1); render(); }
    else if (e.key === 'Enter') { e.preventDefault(); if (hl >= 0) choose(hl); else { close(); if (onSelect) onSelect(input.value); } }
    else if (e.key === 'Escape') { e.stopPropagation(); close(); }
  });
  wrap.input = input;
  return wrap;
}
