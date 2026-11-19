// Menu « Vues » (§ 23) : enregistrer, ouvrir, supprimer l'état d'une page (Journaux, Traces, Métriques, Hôtes…).
import { h, clear, icon } from '../dom.js';
import { t } from '../i18n.js';
import { get, post, del, asList, isAbort } from '../api.js';
import { dropdown, menuItem, modal, field, toast, confirmDialog, errorMessage } from './ui.js';
import { fmtRel } from '../format.js';

const WRITERS = ['owner', 'admin', 'member'];

// opts : {page, me, getState() -> objet, apply(state)}
export function viewsMenu(opts) {
  const canWrite = !!opts.me && WRITERS.includes(opts.me.role);
  const btn = h('button', { class: 'btn', type: 'button', title: t('views.title') }, icon('bookmark', 13), h('span', t('views.button')), icon('chevronDown', 12));
  let cache = null;

  const dd = dropdown(btn, (close) => {
    const box = h('div', { class: 'views-menu' });
    const listEl = h('div', h('div', { class: 'menu-head row' }, h('span', { class: 'spinner' })));
    box.appendChild(h('div', { class: 'menu-head' }, t('views.saved')));
    box.appendChild(listEl);
    box.appendChild(h('div', { class: 'menu-sep' }));
    if (canWrite) box.appendChild(menuItem(t('views.save_current'), () => { close(); saveDialog(); }, { icon: 'plus' }));
    else box.appendChild(h('div', { class: 'menu-head' }, t('views.read_only')));
    fill(listEl, close);
    return box;
  }, { left: true });

  async function fill(listEl, close) {
    try {
      cache = asList(await get('/views', { page: opts.page }), 'views');
      clear(listEl);
      if (!cache.length) { listEl.appendChild(h('div', { class: 'menu-head' }, t('views.none'))); return; }
      for (const v of cache.slice().sort((a, b) => String(a.name).localeCompare(String(b.name)))) {
        const row = h('div', { class: 'views-row' },
          menuItem(v.name || '#' + v.id, () => { close(); opts.apply(v.state && typeof v.state === 'object' ? v.state : {}); toast(t('views.opened', { name: v.name || '' }), 'ok', 2000); }, { icon: 'bookmark' }),
          canWrite ? h('button', { class: 'btn ghost sm icon danger', type: 'button', title: t('action.delete'), onclick: (e) => { e.stopPropagation(); close(); remove(v); } }, icon('trash', 12)) : null);
        row.title = v.created_at ? t('views.created', { when: fmtRel(v.created_at) }) : '';
        listEl.appendChild(row);
      }
    } catch (e) {
      if (isAbort(e)) return;
      clear(listEl);
      const missing = e && (e.status === 405 || (e.status === 404 && !e.code));
      listEl.appendChild(h('div', { class: 'menu-head', style: { color: 'var(--crit)', whiteSpace: 'normal', maxWidth: '260px' } }, missing ? t('views.unavailable') : errorMessage(e)));
    }
  }

  function saveDialog() {
    const name = h('input', { class: 'input', maxlength: 120, placeholder: t('views.name_ph') });
    const shared = h('input', { type: 'checkbox', checked: true });
    const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
    const btnSave = h('button', { class: 'btn primary', type: 'button' }, t('action.save'));
    const m = modal({
      title: t('views.save_title'),
      body: h('div', { class: 'col gap-3' }, field(t('views.f.name'), name), h('label', { class: 'check' }, shared, t('views.f.shared')),
        h('div', { class: 'xsmall muted' }, t('views.what_saved')), err),
      actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), btnSave],
    });
    btnSave.addEventListener('click', async () => {
      if (!name.value.trim()) { err.textContent = t('form.required', { field: t('views.f.name') }); err.hidden = false; return; }
      btnSave.disabled = true;
      try {
        await post('/views', { page: opts.page, name: name.value.trim(), state: opts.getState(), shared: shared.checked });
        m.close();
        toast(t('views.saved_done'), 'ok');
      } catch (e) { btnSave.disabled = false; err.textContent = errorMessage(e); err.hidden = false; }
    });
  }

  async function remove(v) {
    if (!(await confirmDialog({ title: t('views.delete_title'), text: t('views.delete_text', { name: v.name || '' }), confirmLabel: t('action.delete'), danger: true }))) return;
    try { await del('/views/' + encodeURIComponent(v.id)); toast(t('views.deleted'), 'ok'); } catch (e) { toast(errorMessage(e), 'error'); }
  }

  return dd;
}
