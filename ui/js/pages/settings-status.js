// Réglages › Pages de statut (§ 21) : liste et éditeur (titre, description, composants choisis parmi
// moniteurs, synthétiques et SLO, groupes, publication, lien public).
import { h, clear, icon } from '../dom.js';
import { t } from '../i18n.js';
import { get, post, put, del, asList, isAbort } from '../api.js';
import { fmtRel, toMs } from '../format.js';
import { panel, empty, field, select, modal, toast, confirmDialog, errorMessage, copyButton, loading } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { isAdmin, listErrorState } from './ops-shared.js';

const KINDS = ['monitor', 'synthetic', 'slo'];

export function publicUrl(slug) { return location.origin + '/status/?p=' + encodeURIComponent(slug); }

function slugify(s2) {
  return String(s2 || '').normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 63);
}
const SLUG_RE = /^[a-z0-9][a-z0-9-]{1,62}$/;

export async function renderStatusPages(root, ctx) {
  const admin = isAdmin(ctx.me);
  const listEl = h('div');
  root.appendChild(h('div', { class: 'querybar' }, h('span', { class: 'muted small grow' }, t('sp.intro')),
    admin ? h('button', { class: 'btn primary', type: 'button', onclick: () => editor(ctx, null) }, icon('plus', 14), t('sp.new')) : null));
  if (!admin) { root.appendChild(h('div', { class: 'callout' }, icon('lock', 16), h('span', { class: 'small' }, t('sp.admin_only')))); return; }
  root.appendChild(panel(t('nav.settings.status-pages'), listEl, { flush: true }));
  listEl.appendChild(loading());
  let list;
  try {
    list = asList(await get('/status-pages', null, { signal: ctx.signal }), 'status_pages', 'pages');
  } catch (e) {
    if (isAbort(e)) return;
    clear(listEl); listEl.appendChild(listErrorState(e, () => ctx.refresh())); return;
  }
  clear(listEl);
  listEl.appendChild((() => {
    if (!list.length) return empty(t('sp.none_title'), t('sp.none_text'), h('button', { class: 'btn primary', type: 'button', onclick: () => editor(ctx, null) }, icon('plus', 14), t('sp.new')));
    return dataTable({
      columns: [
        { key: 'public', label: t('col.status'), value: (p) => (p.public ? 1 : 0), render: (p) => (p.public ? h('span', { class: 'badge ok' }, icon('globe', 12), t('sp.public')) : h('span', { class: 'badge muted' }, icon('lock', 12), t('sp.private'))) },
        { key: 'title', label: t('col.title'), render: (p) => h('b', p.title || p.slug) },
        { key: 'slug', label: t('sp.slug'), render: (p) => h('span', { class: 'mono small' }, p.slug) },
        { key: 'components', label: t('sp.components'), align: 'r', value: (p) => (Array.isArray(p.components) ? p.components.length : 0), render: (p) => String(Array.isArray(p.components) ? p.components.length : 0) },
        { key: 'link', label: t('sp.link'), sort: false, render: (p) => (p.public ? h('span', { class: 'row', style: { gap: '4px' } },
          h('a', { class: 'mono small', href: publicUrl(p.slug), target: '_blank', rel: 'noopener' }, '/status/?p=' + p.slug, ' ', icon('external', 11)), copyButton(publicUrl(p.slug), { label: false }))
          : h('span', { class: 'muted small' }, t('sp.not_published'))) },
        { key: 'updated_at', label: t('col.updated'), align: 'r', value: (p) => toMs(p.updated_at), render: (p) => (p.updated_at ? fmtRel(p.updated_at) : '-') },
        { key: 'act', label: '', sort: false, render: (p) => h('span', { class: 'row', style: { gap: '2px', justifyContent: 'flex-end' } },
          h('button', { class: 'btn ghost sm icon', type: 'button', title: t('action.edit'), onclick: () => editor(ctx, p) }, icon('edit', 13)),
          h('button', { class: 'btn ghost sm icon danger', type: 'button', title: t('action.delete'), onclick: async () => {
            if (!(await confirmDialog({ title: t('sp.delete_title'), text: t('sp.delete_text', { name: p.title || p.slug }), confirmLabel: t('action.delete'), danger: true }))) return;
            try { await del('/status-pages/' + p.id); toast(t('sp.deleted'), 'ok'); ctx.refresh(); } catch (e) { toast(errorMessage(e), 'error'); }
          } }, icon('trash', 13))) },
      ],
      rows: list, sortKey: 'title', sortDir: 'asc', onRowClick: (p) => editor(ctx, p),
    });
  })());
}

function editor(ctx, existing) {
  const p = existing ? JSON.parse(JSON.stringify(existing)) : { title: '', slug: '', description: '', components: [], public: false };
  p.components = Array.isArray(p.components) ? p.components : [];
  let slugTouched = !!existing;
  const sources = { monitor: [], synthetic: [], slo: [] };
  const srcP = Promise.all([
    get('/monitors', null, { signal: ctx.signal }).then((r) => { sources.monitor = asList(r, 'monitors').map((x) => ({ id: x.id, name: x.name })); }).catch(() => {}),
    get('/synthetics', null, { signal: ctx.signal }).then((r) => { sources.synthetic = asList(r, 'synthetics', 'tests').map((x) => ({ id: x.id, name: x.name || x.target })); }).catch(() => {}),
    get('/slos', null, { signal: ctx.signal }).then((r) => { sources.slo = asList(r, 'slos').map((x) => ({ id: x.id, name: x.name })); }).catch(() => {}),
  ]);

  const title = h('input', { class: 'input', maxlength: 120, value: p.title, placeholder: t('sp.f.title_ph') });
  const slug = h('input', { class: 'input mono', maxlength: 63, value: p.slug, placeholder: 'mon-service' });
  const linkInfo = h('div', { class: 'xsmall muted' });
  const desc = h('textarea', { class: 'input', rows: 2, maxlength: 1000, placeholder: t('sp.f.description_ph') });
  desc.value = p.description || '';
  const pub = h('input', { type: 'checkbox', checked: !!p.public });
  const pubWarn = h('div', { class: 'callout warn', hidden: !p.public }, icon('globe', 16), h('span', { class: 'small' }, t('sp.public_warn')));
  const compEl = h('div', { class: 'col', style: { gap: '6px' } });
  const addEl = h('div', { class: 'row wrap' });
  const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
  const groupList = h('datalist', { id: 'sp-groups' });

  const showLink = () => { linkInfo.textContent = SLUG_RE.test(p.slug) ? t('sp.f.link', { url: publicUrl(p.slug) }) : t('sp.f.slug_hint'); };
  title.addEventListener('input', () => { p.title = title.value; if (!slugTouched) { p.slug = slugify(title.value); slug.value = p.slug; showLink(); } });
  slug.addEventListener('input', () => { slugTouched = true; p.slug = slug.value.trim().toLowerCase(); showLink(); });
  desc.addEventListener('input', () => { p.description = desc.value; });
  pub.addEventListener('change', () => { p.public = pub.checked; pubWarn.hidden = !pub.checked; });
  showLink();

  function refName(c) {
    const list = sources[c.kind] || [];
    const x = list.find((s2) => String(s2.id) === String(c.ref_id));
    return x ? x.name : '#' + c.ref_id;
  }

  function drawComponents() {
    clear(compEl);
    clear(groupList);
    Array.from(new Set(p.components.map((c) => c.group).filter(Boolean))).forEach((g) => groupList.appendChild(h('option', { value: g })));
    if (!p.components.length) { compEl.appendChild(h('div', { class: 'small muted' }, t('sp.no_components'))); return; }
    p.components.forEach((c, i) => {
      const name = h('input', { class: 'input sm', value: c.name || '', maxlength: 120, placeholder: t('sp.f.comp_name'), 'aria-label': t('sp.f.comp_name') });
      name.addEventListener('input', () => { c.name = name.value; });
      const group = h('input', { class: 'input sm', value: c.group || '', maxlength: 80, list: 'sp-groups', placeholder: t('sp.f.comp_group'), 'aria-label': t('sp.f.comp_group') });
      group.addEventListener('change', () => { c.group = group.value.trim() || null; drawComponents(); });
      compEl.appendChild(h('div', { class: 'sp-comp' },
        h('span', { class: 'badge muted' }, t('sp.kind.' + (KINDS.includes(c.kind) ? c.kind : 'other'))),
        h('span', { class: 'small ellipsis sp-ref', title: refName(c) }, refName(c)),
        name, group,
        h('span', { class: 'row', style: { gap: '2px' } },
          h('button', { class: 'btn ghost sm icon', type: 'button', title: t('sp.up'), disabled: i === 0, onclick: () => { p.components.splice(i - 1, 0, p.components.splice(i, 1)[0]); drawComponents(); } }, icon('up', 12)),
          h('button', { class: 'btn ghost sm icon', type: 'button', title: t('sp.down'), disabled: i === p.components.length - 1, onclick: () => { p.components.splice(i + 1, 0, p.components.splice(i, 1)[0]); drawComponents(); } }, icon('down', 12)),
          h('button', { class: 'btn ghost sm icon danger', type: 'button', title: t('action.remove'), onclick: () => { p.components.splice(i, 1); drawComponents(); } }, icon('x', 12)))));
    });
  }

  function drawAdd() {
    clear(addEl);
    let kind = 'monitor';
    const objSel = h('select', { class: 'input', style: { minWidth: '220px' } });
    const fill = () => {
      while (objSel.options.length) objSel.remove(0);
      const used = new Set(p.components.filter((c) => c.kind === kind).map((c) => String(c.ref_id)));
      const list = (sources[kind] || []).filter((x) => !used.has(String(x.id))).sort((a, b) => String(a.name).localeCompare(String(b.name)));
      if (!list.length) objSel.appendChild(h('option', { value: '' }, t('sp.nothing_to_add')));
      list.forEach((x) => objSel.appendChild(h('option', { value: x.id }, x.name || '#' + x.id)));
    };
    const kindSel = select(KINDS.map((k) => ({ value: k, label: t('sp.kind.' + k) })), kind, (v) => { kind = v; fill(); });
    const add = h('button', { class: 'btn sm', type: 'button' }, icon('plus', 13), t('sp.add_component'));
    add.addEventListener('click', () => {
      if (!objSel.value) return;
      const x = (sources[kind] || []).find((s2) => String(s2.id) === objSel.value);
      const lastGroup = p.components.length ? p.components[p.components.length - 1].group || null : null;
      p.components.push({ kind, ref_id: /^\d+$/.test(objSel.value) ? Number(objSel.value) : objSel.value, name: x ? x.name : '', group: lastGroup });
      drawComponents(); fill();
    });
    addEl.appendChild(kindSel);
    addEl.appendChild(objSel);
    addEl.appendChild(add);
    fill();
    srcP.then(() => { fill(); drawComponents(); });
  }

  drawComponents();
  drawAdd();
  const saveBtn = h('button', { class: 'btn primary', type: 'button' }, existing ? t('action.save') : t('sp.create'));
  const m = modal({
    title: existing ? t('sp.edit') : t('sp.new'), wide: true,
    body: h('div', { class: 'col gap-3' },
      h('div', { class: 'form-grid' }, field(t('sp.f.title'), title), field(t('sp.slug'), slug)),
      linkInfo,
      field(t('sp.f.description'), desc),
      h('div', h('h4', { class: 'mb-2' }, t('sp.components')), h('p', { class: 'xsmall muted' }, t('sp.components_hint')), compEl, h('div', { class: 'mt-2' }, addEl), groupList),
      h('label', { class: 'check' }, pub, t('sp.f.public')), pubWarn,
      h('div', { class: 'callout' }, icon('shield', 16), h('span', { class: 'small' }, t('sp.privacy'))),
      err),
    actions: [
      existing && existing.public ? h('a', { class: 'btn', href: publicUrl(existing.slug), target: '_blank', rel: 'noopener' }, icon('external', 13), t('sp.open_public')) : null,
      h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), saveBtn,
    ].filter(Boolean),
  });
  saveBtn.addEventListener('click', async () => {
    err.hidden = true;
    if (!p.title.trim()) { err.textContent = t('form.required', { field: t('sp.f.title') }); err.hidden = false; return; }
    if (!SLUG_RE.test(p.slug)) { err.textContent = t('sp.err.slug'); err.hidden = false; return; }
    const body = {
      title: p.title.trim(), slug: p.slug, description: (p.description || '').trim(), public: !!p.public,
      components: p.components.map((c) => ({ kind: c.kind, ref_id: c.ref_id, name: (c.name || '').trim() || refName(c), group: c.group || null })),
    };
    saveBtn.disabled = true;
    try {
      if (existing) await put('/status-pages/' + existing.id, body); else await post('/status-pages', body);
      m.close();
      toast(existing ? t('sp.saved') : t('sp.created'), 'ok');
      ctx.refresh();
    } catch (e) {
      saveBtn.disabled = false;
      err.textContent = e && e.status === 409 ? t('sp.err.slug_taken') : errorMessage(e);
      err.hidden = false;
    }
  });
  return m;
}
