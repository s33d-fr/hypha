// Tableaux de bord (§§ 7, 30) : liste par dossiers, clonage, export et import JSON, partage public en lecture,
// grille de 12 colonnes, édition (ajout, déplacement, taille, réglages), variables de modèle à valeurs
// multiples, rafraîchissement automatique, mode TV avec rotation, comparaison avec la période précédente.
import { h, clear, icon, on } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, post, put, del, asList, isAbort } from '../api.js';
import { fmtRel, fmtHM, toMs } from '../format.js';
import { section, empty, panel, modal, field, select, toast, confirmDialog, errorMessage, errorState, loading, searchInput, dropdown, menuItem, pills, copyButton } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { WIDGET_TYPES, WIDGET_DEFAULTS, METRIC_TYPES, renderWidget } from './widgets.js';
import { valueRefs, sameOriginUrl, imageUrl } from './widgets-extra.js';
import { MAP_METRICS } from '../components/hexmap.js';
import { parseHash } from '../router.js';
import { rangeFromParams } from '../time.js';
import { download, isAdmin, canWrite, isMissingRoute, stamp } from './kit.js';

const ROW_H = 72;
const GAP = 10;
const UNITS = ['', 'percent', 'byte', 'byte_per_second', 'operation_per_second', 'packet_per_second', 'second', 'millisecond', 'count'];
const REFRESH = [0, 30, 60, 300, 900];
const AGGS = ['avg', 'last', 'max', 'min', 'sum'];

// Familles du sélecteur de type.
const FAMILIES = [
  ['graphs', ['timeseries', 'heatmap', 'distribution', 'scatter', 'change']],
  ['values', ['query_value', 'toplist', 'table', 'pie', 'slo', 'slo_list', 'alert_value']],
  ['events', ['log_stream', 'event_timeline']],
  ['monitoring', ['monitor_summary', 'alert_graph', 'problems', 'check_status']],
  ['maps', ['host_map', 'service_map', 'network_map']],
  ['layout', ['note', 'image', 'iframe', 'group']],
];
const FORMULA_TYPES = ['timeseries', 'query_value', 'toplist', 'table'];
const THRESHOLD_TYPES = ['timeseries', 'query_value', 'toplist', 'table'];
const MAX_QUERIES = { timeseries: 6, table: 6, scatter: 2 };

export async function render(root, ctx) {
  if (ctx.route.view === 'board') return renderBoard(root, ctx);
  return renderList(root, ctx);
}

function unwrap(r) { return r && r.dashboard && typeof r.dashboard === 'object' ? r.dashboard : r; }

function exportBody(d) {
  return { title: d.title, description: d.description || '', folder: d.folder || '', refresh_s: d.refresh_s || null, template_vars: d.template_vars || [], widgets: d.widgets || [] };
}

function slug(s2) { return String(s2 || 'tableau').toLowerCase().normalize('NFD').replace(/[̀-ͯ]/g, '').replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 60) || 'tableau'; }

// Export JSON : route du serveur, sinon construit depuis le tableau (même contenu, sans identifiant).
async function exportDashboard(d) {
  let data;
  try {
    data = await get('/dashboards/' + encodeURIComponent(d.id) + '/export');
  } catch (e) {
    if (!isMissingRoute(e)) { toast(errorMessage(e), 'error'); return; }
    const full = d.widgets ? d : unwrap(await get('/dashboards/' + encodeURIComponent(d.id)));
    data = { hypha_dashboard: 1, exported_at: new Date().toISOString(), dashboard: exportBody(full) };
  }
  download('hypha-tableau-' + slug(d.title) + '-' + stamp() + '.json', JSON.stringify(data, null, 2), 'application/json');
  toast(t('dash.exported'), 'ok');
}

function importDialog(ctx) {
  const file = h('input', { type: 'file', accept: '.json,application/json', class: 'input' });
  const area = h('textarea', { class: 'input mono', rows: 10, placeholder: '{ "dashboard": { "title": "…", "widgets": [ … ] } }' });
  const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
  const info = h('div', { class: 'small text-2' });
  file.addEventListener('change', () => {
    const f = file.files && file.files[0];
    if (!f) return;
    if (f.size > 2 * 1024 * 1024) { err.textContent = t('dash.import.too_big'); err.hidden = false; return; }
    const reader = new FileReader();
    reader.onload = () => { area.value = String(reader.result || ''); check(); };
    reader.readAsText(f);
  });
  let parsed = null;
  const check = () => {
    err.hidden = true;
    parsed = null;
    info.textContent = '';
    if (!area.value.trim()) return;
    try {
      const j = JSON.parse(area.value);
      const d = j && j.dashboard && typeof j.dashboard === 'object' ? j.dashboard : j;
      if (!d || typeof d !== 'object' || !Array.isArray(d.widgets)) throw new Error(t('dash.import.no_widgets'));
      parsed = j;
      info.textContent = t('dash.import.found', { title: d.title || '-', n: d.widgets.length });
    } catch (e) { err.textContent = t('dash.import.bad_json') + ' ' + (e.message || ''); err.hidden = false; }
  };
  area.addEventListener('input', check);
  const btn = h('button', { class: 'btn primary', type: 'button' }, icon('upload', 13), t('dash.import.go'));
  const m = modal({
    title: t('dash.import.title'), wide: true,
    body: h('div', { class: 'col gap-3' }, h('div', { class: 'small muted' }, t('dash.import.hint')), field(t('dash.import.file'), file), field(t('dash.import.paste'), area), info, err),
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), btn],
  });
  btn.addEventListener('click', async () => {
    check();
    if (!parsed) { if (err.hidden) { err.textContent = t('dash.import.empty'); err.hidden = false; } return; }
    btn.disabled = true;
    try {
      let r;
      try { r = await post('/dashboards/import', parsed); } catch (e) {
        if (!isMissingRoute(e)) throw e;
        const d = parsed.dashboard && typeof parsed.dashboard === 'object' ? parsed.dashboard : parsed;
        r = await post('/dashboards', exportBody(d));
      }
      m.close();
      const id = r && (r.id || (r.dashboard && r.dashboard.id));
      toast(t('dash.import.done'), 'ok');
      if (id) ctx.navigate('/dashboards/' + id); else ctx.refresh();
    } catch (e) { btn.disabled = false; err.textContent = errorMessage(e); err.hidden = false; }
  });
}

function folderInput(value, folders) {
  const id = 'dash-folders-' + Math.random().toString(36).slice(2, 7);
  const inp = h('input', { class: 'input', value: value || '', list: id, maxlength: 120, placeholder: t('dash.folder_ph') });
  const dl = h('datalist', { id }, (folders || []).map((f) => h('option', { value: f })));
  const wrap = h('span', inp, dl);
  wrap.input = inp;
  return wrap;
}

function newDashboardDialog(ctx, base, folders) {
  const title = h('input', { class: 'input', value: base ? base.title + ' ' + t('dash.copy_suffix') : '', maxlength: 200 });
  const desc = h('textarea', { class: 'input', rows: 2 });
  desc.value = base ? base.description || '' : '';
  const folder = folderInput(base ? base.folder || '' : '', folders);
  const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
  const btn = h('button', { class: 'btn primary', type: 'button' }, base ? t('dash.duplicate') : t('dash.create'));
  const m = modal({
    title: base ? t('dash.duplicate_title') : t('dash.new_title'),
    body: h('div', { class: 'col gap-3' }, field(t('dash.f.title'), title), field(t('dash.f.description'), desc), field(t('dash.f.folder'), folder), err),
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), btn],
  });
  btn.addEventListener('click', async () => {
    if (!title.value.trim()) { err.textContent = t('form.required', { field: t('dash.f.title') }); err.hidden = false; return; }
    btn.disabled = true;
    try {
      const r = await post('/dashboards', {
        title: title.value.trim(), description: desc.value, folder: folder.input.value.trim(), refresh_s: base ? base.refresh_s || null : null,
        widgets: base ? base.widgets || [] : [], template_vars: base ? base.template_vars || [] : [{ name: 'host', tag: 'host', default: '*' }],
      });
      m.close();
      const id = r && (r.id || (r.dashboard && r.dashboard.id));
      if (id) ctx.navigate('/dashboards/' + id, { edit: base ? null : '1' }); else ctx.refresh();
    } catch (e) { btn.disabled = false; err.textContent = errorMessage(e); err.hidden = false; }
  });
}

// ── Liste ─────────────────────────────────────────────────────────────────
async function renderList(root, ctx) {
  const { signal, href, params } = ctx;
  ctx.setCrumbs([{ label: t('nav.dashboards') }]);
  let q = params.get('q') || '';
  let folder = params.has('folder') ? params.get('folder') : null;
  const body = h('div');
  const countEl = h('span', { class: 'sub' });
  const folderEl = h('div', { class: 'row wrap mb-3' });
  const search = searchInput({ placeholder: t('dash.search_ph'), value: q, onInput: (v) => { q = v; ctx.setParams({ q: v || null }, { silent: true }); draw(); } });
  search.style.flex = '1 1 300px';
  const writable = canWrite(ctx.me);
  root.appendChild(h('div', { class: 'querybar' }, search,
    writable ? h('button', { class: 'btn', type: 'button', onclick: () => importDialog(ctx) }, icon('upload', 14), t('dash.import.button')) : null,
    writable ? h('button', { class: 'btn primary', type: 'button', onclick: () => newDashboardDialog(ctx, null, folders()) }, icon('plus', 14), t('dash.new')) : null));
  root.appendChild(folderEl);
  root.appendChild(panel(t('nav.dashboards'), body, { flush: true, actions: countEl }));
  let rows = [];
  const folders = () => Array.from(new Set(rows.map((d) => d.folder || '').filter(Boolean))).sort((a, b) => a.localeCompare(b));

  function drawFolders() {
    clear(folderEl);
    const list = folders();
    if (!list.length) return;
    folderEl.appendChild(icon('folder', 14));
    folderEl.appendChild(pills([{ key: '__all', label: t('dash.folder.all') + ' (' + rows.length + ')' }, { key: '', label: t('dash.folder.none') + ' (' + rows.filter((d) => !d.folder).length + ')' }]
      .concat(list.map((f) => ({ key: f, label: f + ' (' + rows.filter((d) => d.folder === f).length + ')' }))), folder === null ? '__all' : folder,
    (k) => { folder = k === '__all' ? null : k; ctx.setParams({ folder: folder === null ? null : (folder || '-') }, { silent: true }); drawFolders(); draw(); }));
  }

  function draw() {
    const needle = q.toLowerCase();
    const list = rows.filter((d) => (folder === null || (d.folder || '') === folder) && (!q || [d.title, d.description, d.folder].join(' ').toLowerCase().includes(needle)));
    countEl.textContent = tn('dash.count', list.length);
    clear(body);
    body.appendChild(dataTable({
      columns: [
        { key: 'folder', label: t('dash.f.folder'), value: (d) => (d.folder || '￿') + ' ' + (d.title || ''), render: (d) => (d.folder ? h('span', { class: 'tag' }, icon('folder', 11), ' ', d.folder) : h('span', { class: 'muted' }, '-')) },
        { key: 'title', label: t('col.title'), render: (d) => h('span', { class: 'row', style: { gap: '6px' } }, h('a', { href: href('/dashboards/' + d.id) }, d.title || '#' + d.id),
          d.share_token || d.shared ? h('span', { class: 'badge info', title: t('dash.share.on') }, icon('share', 11)) : null,
          d.refresh_s ? h('span', { class: 'badge muted', title: t('dash.refresh') }, icon('refresh', 11), ' ' + d.refresh_s + ' s') : null) },
        { key: 'description', label: t('col.description'), tdCls: 'trunc', render: (d) => h('span', { class: 'text-2', title: d.description || '' }, d.description || '-') },
        { key: 'widgets', label: t('col.widgets'), align: 'r', value: (d) => (Array.isArray(d.widgets) ? d.widgets.length : d.widget_count || 0), render: (d) => String(Array.isArray(d.widgets) ? d.widgets.length : d.widget_count ?? '-') },
        { key: 'updated_at', label: t('col.updated'), align: 'r', value: (d) => toMs(d.updated_at), render: (d) => fmtRel(d.updated_at) },
        { key: 'actions', label: '', sort: false, render: (d) => h('span', { class: 'row', style: { gap: '2px', justifyContent: 'flex-end' } },
          writable ? h('button', { class: 'btn ghost sm icon', type: 'button', title: t('dash.duplicate'), onclick: async () => {
            try { newDashboardDialog(ctx, unwrap(await get('/dashboards/' + d.id)), folders()); } catch (e) { toast(errorMessage(e), 'error'); }
          } }, icon('copy', 13)) : null,
          h('button', { class: 'btn ghost sm icon', type: 'button', title: t('dash.export'), onclick: () => exportDashboard(d) }, icon('download', 13)),
          writable ? h('button', { class: 'btn ghost sm icon danger', type: 'button', title: t('action.delete'), onclick: async () => {
            if (!(await confirmDialog({ title: t('dash.delete_title'), text: t('dash.delete_text', { name: d.title || '' }), confirmLabel: t('action.delete'), danger: true }))) return;
            try { await del('/dashboards/' + d.id); toast(t('dash.deleted'), 'ok'); ctx.refresh(); } catch (e) { toast(errorMessage(e), 'error'); }
          } }, icon('trash', 13)) : null) },
      ],
      rows: list, sortKey: 'folder', sortDir: 'asc',
      onRowClick: (d) => ctx.navigate('/dashboards/' + d.id),
      emptyText: rows.length ? t('dash.no_match') : t('dash.none'),
    }));
  }
  if (folder === '-') folder = '';
  await section(body, async () => asList(await get('/dashboards', null, { signal }), 'dashboards'), (list) => { rows = list; drawFolders(); draw(); return null; });
}

function overlaps(a, b) {
  return a !== b && a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
}

// Pousse vers le bas ce qui chevauche le widget déplacé, puis remonte tout ce qui peut l'être.
function resolve(widgets, moved) {
  for (let pass = 0; pass < 200; pass++) {
    let changed = false;
    for (const w of widgets) {
      if (w === moved) continue;
      for (const o of widgets) {
        if (o === w) continue;
        if (overlaps(w, o) && (o === moved || o.y <= w.y)) { w.y = o.y + o.h; changed = true; }
      }
    }
    if (!changed) break;
  }
  const sorted = widgets.slice().sort((a, b) => a.y - b.y || a.x - b.x);
  for (const w of sorted) {
    if (w === moved) continue;
    while (w.y > 0) {
      w.y--;
      if (widgets.some((o) => overlaps(w, o))) { w.y++; break; }
    }
  }
}

function normWidget(w, i) {
  const d = WIDGET_DEFAULTS[w.type] || WIDGET_DEFAULTS.note;
  const n = Object.assign({ id: 'w' + (i + 1), type: 'note', title: '', x: 0, y: 0, w: d.w, h: d.h, queries: [], options: {} }, w);
  n.x = Math.max(0, Math.min(11, Number(n.x) || 0));
  n.w = Math.max(1, Math.min(12 - n.x, Number(n.w) || d.w));
  n.y = Math.max(0, Number(n.y) || 0);
  n.h = Math.max(1, Number(n.h) || d.h);
  n.queries = Array.isArray(n.queries) ? n.queries : [];
  n.options = n.options && typeof n.options === 'object' ? n.options : {};
  if (n.type === 'group') n.options.widgets = asList(n.options.widgets).map((c, j) => normChild(c, j));
  return n;
}

function normChild(c, j) {
  const d = WIDGET_DEFAULTS[c.type] || WIDGET_DEFAULTS.note;
  const n = Object.assign({ id: 'c' + (j + 1), type: 'note', title: '', w: Math.min(d.w, 6), h: Math.min(d.h, 3), queries: [], options: {} }, c);
  n.w = Math.max(1, Math.min(12, Number(n.w) || 4));
  n.h = Math.max(1, Math.min(8, Number(n.h) || 2));
  n.queries = Array.isArray(n.queries) ? n.queries : [];
  n.options = n.options && typeof n.options === 'object' ? n.options : {};
  delete n.x; delete n.y;
  return n;
}

// Valeurs des variables depuis l'URL : tv_host=web-01 ou tv_host=web-01,web-02 (variable multiple).
function readValues(vars, params) {
  const values = {};
  for (const v of vars) {
    const raw = params.get('tv_' + v.name);
    if (v.multi) {
      const def = Array.isArray(v.defaults) && v.defaults.length ? v.defaults : [v.default || '*'];
      values[v.name] = raw !== null ? raw.split(',').map((x) => x.trim()).filter(Boolean) : def.slice();
      if (!values[v.name].length) values[v.name] = ['*'];
    } else values[v.name] = raw || v.default || '*';
  }
  return values;
}

// ── Tableau ───────────────────────────────────────────────────────────────
async function renderBoard(root, ctx) {
  const { args, signal, href, params } = ctx;
  let range = ctx.range;
  ctx.setCrumbs([{ label: t('nav.dashboards'), href: href('/dashboards') }, { label: '#' + args.id }]);
  root.appendChild(loading());
  let dash;
  try {
    dash = unwrap(await get('/dashboards/' + encodeURIComponent(args.id), null, { signal }));
  } catch (e) {
    if (isAbort(e)) return;
    clear(root); root.appendChild(errorState(e, () => ctx.refresh())); return;
  }
  clear(root);
  const writable = canWrite(ctx.me);
  let editing = writable && params.get('edit') === '1';
  let dirty = false;
  let compare = params.get('cmp') === '1';
  const tv = params.get('tv') === '1';
  let widgets = (Array.isArray(dash.widgets) ? dash.widgets : []).map(normWidget);
  let vars = Array.isArray(dash.template_vars) ? dash.template_vars.filter((v) => v && v.name) : [];
  // Brouillon d'édition gardé dans l'onglet : un changement de période ou une actualisation ne perd rien.
  const draftKey = 'hypha.dash.draft.' + dash.id;
  const readDraft = () => { try { return JSON.parse(sessionStorage.getItem(draftKey) || 'null'); } catch (e) { return null; } };
  const clearDraft = () => { try { sessionStorage.removeItem(draftKey); } catch (e) { /* rien à effacer */ } };
  function markDirty() {
    dirty = true;
    try { sessionStorage.setItem(draftKey, JSON.stringify({ widgets, vars, title: dash.title, description: dash.description || '', folder: dash.folder || '', refresh_s: dash.refresh_s || null })); } catch (e) { /* brouillon non gardé */ }
  }
  const draft = writable ? readDraft() : null;
  if (draft && Array.isArray(draft.widgets)) {
    widgets = draft.widgets.map(normWidget);
    vars = Array.isArray(draft.vars) ? draft.vars : vars;
    Object.assign(dash, { title: draft.title || dash.title, description: draft.description, folder: draft.folder || '', refresh_s: draft.refresh_s || null });
    editing = true;
    dirty = true;
    toast(t('dash.draft_restored'), 'info', 5000);
  }
  const values = readValues(vars, params);
  ctx.setCrumbs([{ label: t('nav.dashboards'), href: href('/dashboards') }, { label: dash.title || '#' + dash.id }]);

  const head = h('div', { class: 'page-head' });
  const varBar = h('div', { class: 'tv-bar' });
  const grid = h('div', { class: 'dash-grid' });
  const tvBar = h('div', { class: 'tv-top', hidden: !tv });
  root.appendChild(tvBar);
  root.appendChild(head);
  root.appendChild(varBar);
  root.appendChild(grid);

  const beforeUnload = (e) => { if (dirty) { e.preventDefault(); e.returnValue = ''; } };
  ctx.onCleanup(on(window, 'beforeunload', beforeUnload));

  // Rafraîchissement automatique : réglage du tableau, remplaçable pour la session (?refresh=).
  let refreshS = params.has('refresh') ? Number(params.get('refresh')) || 0 : Number(dash.refresh_s) || 0;
  let refreshTimer = null;
  function armRefresh() {
    if (refreshTimer) { clearInterval(refreshTimer); refreshTimer = null; }
    const sec = tv && !refreshS ? 60 : refreshS;
    if (sec > 0) refreshTimer = setInterval(() => {
      if (editing || document.visibilityState !== 'visible' || document.querySelector('.modal-back, .drawer')) return;
      const r = rangeFromParams(parseHash().params);
      if (r.live) range = r;
      drawWidgets();
    }, Math.max(10, sec) * 1000);
  }
  ctx.onCleanup(() => { if (refreshTimer) clearInterval(refreshTimer); });

  // Mode TV : coquille masquée, plein écran si possible, rotation entre tableaux.
  if (tv) {
    const app = document.querySelector('.app');
    if (app) app.classList.add('tv-mode');
    ctx.onCleanup(() => { const a = document.querySelector('.app'); if (a) a.classList.remove('tv-mode'); });
    const rot = (params.get('rot') || '').split(',').filter(Boolean);
    const every = Math.max(15, Number(params.get('every')) || 60);
    const clock = h('span', { class: 'num' }, fmtHM(Date.now()));
    const left = h('span', { class: 'xsmall muted num' });
    tvBar.appendChild(h('div', { class: 'row', style: { gap: '12px' } }, h('b', dash.title || ''), h('span', { class: 'spacer' }), left, clock,
      h('button', { class: 'btn sm', type: 'button', onclick: () => exitTv() }, icon('x', 12), t('dash.tv.exit'))));
    let remain = every;
    const tick = setInterval(() => {
      clock.textContent = fmtHM(Date.now());
      if (rot.length > 1) {
        remain -= 1;
        left.textContent = t('dash.tv.next_in', { s: remain });
        if (remain <= 0) {
          const i = rot.indexOf(String(dash.id));
          const next = rot[(i + 1) % rot.length];
          clearInterval(tick);
          ctx.navigate('/dashboards/' + next, { tv: '1', rot: rot.join(','), every: String(every), cmp: compare ? '1' : null });
        }
      }
    }, 1000);
    ctx.onCleanup(() => clearInterval(tick));
    const esc = on(document, 'keydown', (e) => { if (e.key === 'Escape' && !document.querySelector('.modal-back')) exitTv(); });
    ctx.onCleanup(esc);
  }
  function exitTv() {
    if (document.fullscreenElement && document.exitFullscreen) document.exitFullscreen().catch(() => {});
    ctx.setParams({ tv: null, rot: null, every: null });
  }

  function drawHead() {
    clear(head);
    if (tv) { head.hidden = true; return; }
    head.appendChild(h('div', { style: { minWidth: '0' } },
      h('div', { class: 'row wrap', style: { gap: '8px' } }, h('h1', valueRefs(dash.title || '#' + dash.id, vars, values)),
        dash.folder ? h('span', { class: 'tag' }, icon('folder', 11), ' ', dash.folder) : null,
        dash.share_token ? h('span', { class: 'badge info', title: t('dash.share.on') }, icon('share', 11), t('dash.share.public')) : null),
      dash.description ? h('div', { class: 'desc' }, dash.description) : null));
    head.appendChild(h('span', { class: 'spacer' }));
    if (editing) {
      head.appendChild(h('span', { class: 'badge info' }, t('dash.editing')));
      head.appendChild(h('button', { class: 'btn sm', type: 'button', onclick: () => editWidget(null) }, icon('plus', 13), t('dash.add_widget')));
      head.appendChild(h('button', { class: 'btn sm', type: 'button', onclick: () => editVars() }, icon('filter', 13), t('dash.vars')));
      head.appendChild(h('button', { class: 'btn sm', type: 'button', onclick: () => editMeta() }, icon('settings', 13), t('dash.settings')));
      head.appendChild(h('button', { class: 'btn sm', type: 'button', onclick: async () => {
        if (dirty && !(await confirmDialog({ title: t('dash.discard_title'), text: t('dash.discard'), confirmLabel: t('dash.discard_ok'), danger: true }))) return;
        dirty = false;
        clearDraft();
        ctx.setParams({ edit: null });
      } }, t('action.cancel')));
      head.appendChild(h('button', { class: 'btn sm primary', type: 'button', onclick: () => save() }, t('action.save')));
      return;
    }
    head.appendChild(h('button', { class: ['btn', 'sm', compare ? 'on' : null], type: 'button', title: t('dash.compare_title'), onclick: () => { compare = !compare; ctx.setParams({ cmp: compare ? '1' : null }, { silent: true }); drawHead(); drawWidgets(); } }, icon('history', 13), t('dash.compare')));
    const rsel = h('select', { class: 'input sm', style: { width: 'auto' }, title: t('dash.refresh') },
      REFRESH.map((s2) => h('option', { value: s2, selected: s2 === refreshS }, s2 ? t('dash.refresh_every', { s: s2 >= 60 ? (s2 / 60) + ' min' : s2 + ' s' }) : t('dash.refresh_off'))));
    rsel.addEventListener('change', () => { refreshS = Number(rsel.value) || 0; ctx.setParams({ refresh: String(refreshS) }, { silent: true }); armRefresh(); });
    head.appendChild(rsel);
    head.appendChild(h('button', { class: 'btn sm', type: 'button', title: t('dash.tv.title'), onclick: () => tvDialog() }, icon('tv', 13), t('dash.tv.button')));
    if (writable) head.appendChild(h('button', { class: 'btn sm', type: 'button', onclick: () => { editing = true; ctx.setParams({ edit: '1' }, { silent: true }); drawAll(); } }, icon('edit', 13), t('action.edit')));
    const more = h('button', { class: 'btn sm icon', type: 'button', title: t('dash.more') }, icon('menu', 13));
    head.appendChild(dropdown(more, (close) => h('div',
      writable ? menuItem(t('dash.duplicate'), () => { close(); newDashboardDialog(ctx, Object.assign({}, dash, { widgets, template_vars: vars }), dash.folder ? [dash.folder] : []); }, { icon: 'copy' }) : null,
      menuItem(t('dash.export'), () => { close(); exportDashboard(Object.assign({}, dash, { widgets, template_vars: vars })); }, { icon: 'download' }),
      isAdmin(ctx.me) ? menuItem(t('dash.share.menu'), () => { close(); shareDialog(); }, { icon: 'share' }) : null,
      writable ? h('div', { class: 'menu-sep' }) : null,
      writable ? menuItem(t('action.delete'), async () => {
        close();
        if (!(await confirmDialog({ title: t('dash.delete_title'), text: t('dash.delete_text', { name: dash.title || '' }), confirmLabel: t('action.delete'), danger: true }))) return;
        try { await del('/dashboards/' + dash.id); toast(t('dash.deleted'), 'ok'); ctx.navigate('/dashboards'); } catch (e) { toast(errorMessage(e), 'error'); }
      }, { icon: 'trash' }) : null)));
  }

  async function save() {
    try {
      const body = { title: dash.title, description: dash.description || '', folder: dash.folder || '', refresh_s: dash.refresh_s || null, widgets, template_vars: vars };
      await put('/dashboards/' + dash.id, body);
      dirty = false;
      clearDraft();
      toast(t('dash.saved'), 'ok');
      editing = false;
      ctx.setParams({ edit: null }, { silent: true });
      drawAll();
    } catch (e) { toast(errorMessage(e), 'error'); }
  }

  function shareDialog() {
    const box = h('div', { class: 'col gap-3' });
    const m = modal({ title: t('dash.share.title'), body: box, actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.close'))] });
    const draw = () => {
      clear(box);
      box.appendChild(h('div', { class: 'callout warn' }, icon('warn', 16), h('div', { class: 'small' }, t('dash.share.warning'))));
      if (dash.share_token) {
        const url = location.origin + '/share/?t=' + encodeURIComponent(dash.share_token);
        box.appendChild(field(t('dash.share.link'), h('div', { class: 'row' }, h('input', { class: 'input mono grow', value: url, readOnly: true }), copyButton(url))));
        box.appendChild(h('div', { class: 'row' }, h('a', { class: 'btn sm', href: url, target: '_blank', rel: 'noopener' }, icon('external', 12), t('dash.share.open')),
          h('span', { class: 'spacer' }),
          h('button', { class: 'btn sm danger', type: 'button', onclick: async () => {
            try { await del('/dashboards/' + dash.id + '/share'); dash.share_token = null; toast(t('dash.share.removed'), 'ok'); draw(); drawHead(); } catch (e) { toast(errorMessage(e), 'error'); }
          } }, t('dash.share.remove'))));
        box.appendChild(h('div', { class: 'xsmall muted' }, t('dash.share.scope')));
      } else {
        box.appendChild(h('div', { class: 'small text-2' }, t('dash.share.intro')));
        box.appendChild(h('div', h('button', { class: 'btn primary', type: 'button', onclick: async () => {
          try { const r = await post('/dashboards/' + dash.id + '/share', {}); dash.share_token = r && (r.share_token || r.token); toast(t('dash.share.created'), 'ok'); draw(); drawHead(); } catch (e) { toast(errorMessage(e), 'error'); }
        } }, icon('share', 13), t('dash.share.create'))));
      }
    };
    draw();
  }

  async function tvDialog() {
    const box = h('div', { class: 'col gap-3' }, h('div', h('span', { class: 'spinner' })));
    const go = h('button', { class: 'btn primary', type: 'button' }, icon('tv', 13), t('dash.tv.start'));
    const m = modal({ title: t('dash.tv.title'), body: box, actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), go] });
    let all = [];
    try { all = asList(await get('/dashboards', null, { signal }), 'dashboards'); } catch (e) { all = [dash]; }
    const chosen = new Set([String(dash.id)]);
    for (const d of all) if (dash.folder && d.folder === dash.folder) chosen.add(String(d.id));
    const every = select([30, 60, 120, 300].map((s2) => ({ value: s2, label: s2 >= 60 ? (s2 / 60) + ' min' : s2 + ' s' })), 60);
    const full = h('input', { type: 'checkbox', checked: true });
    clear(box);
    box.appendChild(h('div', { class: 'small text-2' }, t('dash.tv.intro')));
    box.appendChild(field(t('dash.tv.rotation'), h('div', { class: 'col', style: { gap: '4px', maxHeight: '260px', overflow: 'auto' } }, all.map((d) => {
      const cb = h('input', { type: 'checkbox', checked: chosen.has(String(d.id)) });
      cb.addEventListener('change', () => { if (cb.checked) chosen.add(String(d.id)); else chosen.delete(String(d.id)); });
      return h('label', { class: 'check' }, cb, d.title || '#' + d.id, d.folder ? h('span', { class: 'xsmall muted' }, ' · ' + d.folder) : null);
    }))));
    box.appendChild(field(t('dash.tv.every'), every));
    box.appendChild(h('label', { class: 'check' }, full, t('dash.tv.fullscreen')));
    go.addEventListener('click', () => {
      m.close();
      if (full.checked && document.documentElement.requestFullscreen) document.documentElement.requestFullscreen().catch(() => {});
      const ids = Array.from(chosen);
      if (!ids.includes(String(dash.id))) ids.unshift(String(dash.id));
      ctx.setParams({ tv: '1', rot: ids.length > 1 ? ids.join(',') : null, every: every.value, edit: null });
    });
  }

  // ── Variables ───────────────────────────────────────────────────────────
  function setValue(v, val) {
    values[v.name] = val;
    const def = v.multi ? (Array.isArray(v.defaults) && v.defaults.length ? v.defaults : [v.default || '*']).join(',') : (v.default || '*');
    const cur = Array.isArray(val) ? val.join(',') : val;
    ctx.setParams({ ['tv_' + v.name]: cur === def ? null : cur }, { silent: true });
    drawHead();
    drawWidgets();
  }

  function drawVars() {
    clear(varBar);
    if (!vars.length) { varBar.hidden = true; return; }
    varBar.hidden = false;
    for (const v of vars) {
      if (v.multi) { varBar.appendChild(h('label', h('b', '$' + v.name), multiPicker(v))); continue; }
      const sel = h('select', { class: 'input sm', style: { width: 'auto', minWidth: '140px' } }, h('option', { value: '*' }, '*'));
      const cur = values[v.name] || '*';
      if (cur !== '*') sel.appendChild(h('option', { value: cur, selected: true }, cur));
      sel.addEventListener('change', () => setValue(v, sel.value));
      varBar.appendChild(h('label', h('b', '$' + v.name), sel));
      loadVarValues(v).then((vals) => {
        for (const val of vals) if (val !== cur) sel.appendChild(h('option', { value: val }, val));
        sel.value = cur;
      }).catch(() => {});
    }
  }

  function multiPicker(v) {
    const cur = () => (Array.isArray(values[v.name]) ? values[v.name] : [values[v.name] || '*']);
    const label = h('span', { class: 'ellipsis', style: { maxWidth: '220px' } });
    const setLabel = () => { const c = cur(); label.textContent = c.includes('*') || !c.length ? '*' : c.length <= 2 ? c.join(', ') : c.slice(0, 2).join(', ') + ' +' + (c.length - 2); };
    setLabel();
    const btn = h('button', { class: 'btn sm', type: 'button', style: { minWidth: '140px', justifyContent: 'space-between' } }, label, icon('chevronDown', 11));
    return dropdown(btn, (close) => {
      const box = h('div', { class: 'multi-pick' });
      const filter = h('input', { class: 'input sm', placeholder: t('action.search'), style: { width: '100%' } });
      const list = h('div', { class: 'multi-list' }, h('span', { class: 'spinner' }));
      const sel = new Set(cur().filter((x) => x !== '*'));
      box.appendChild(filter);
      box.appendChild(h('div', { class: 'row', style: { gap: '4px', margin: '6px 0' } },
        h('button', { class: 'btn ghost sm', type: 'button', onclick: () => { sel.clear(); apply(); } }, t('dash.var.all')),
        h('span', { class: 'spacer' }),
        h('button', { class: 'btn primary sm', type: 'button', onclick: () => { apply(); close(); } }, t('action.apply'))));
      box.appendChild(list);
      let all = [];
      const drawList = () => {
        clear(list);
        const needle = filter.value.trim().toLowerCase();
        const shown = all.filter((x) => !needle || x.toLowerCase().includes(needle)).slice(0, 300);
        if (!shown.length) list.appendChild(h('div', { class: 'menu-head' }, t('dash.var.no_values')));
        for (const val of shown) {
          const cb = h('input', { type: 'checkbox', checked: sel.has(val) });
          cb.addEventListener('change', () => { if (cb.checked) sel.add(val); else sel.delete(val); });
          list.appendChild(h('label', { class: 'check menu-item' }, cb, h('span', { class: 'mono small' }, val)));
        }
      };
      const apply = () => { setValue(v, sel.size ? Array.from(sel) : ['*']); setLabel(); };
      filter.addEventListener('input', drawList);
      loadVarValues(v).then((vals) => { all = Array.from(new Set(vals.concat(Array.from(sel)))).sort(); drawList(); }).catch(() => { all = Array.from(sel); drawList(); });
      setTimeout(() => filter.focus(), 0);
      return box;
    }, { left: true });
  }

  async function loadVarValues(v) {
    const tag = v.tag || v.name;
    if (tag === 'host') {
      const hosts = asList(await get('/hosts', null, { signal }), 'hosts');
      return hosts.map((x) => x.hostname).filter(Boolean).sort();
    }
    const metric = (allWidgets().flatMap((w) => w.queries || []).map((q) => /\w+:([^{\s(),]+)\{/.exec(q.q || '')).find(Boolean) || [])[1] || 'system.cpu.usage';
    const tags = await get('/metrics/tags', { metric }, { signal });
    return (tags && Array.isArray(tags[tag]) ? tags[tag] : []).map(String).sort();
  }

  function allWidgets() { return widgets.flatMap((w) => (w.type === 'group' ? [w].concat(asList(w.options.widgets)) : [w])); }

  // ── Grille ──────────────────────────────────────────────────────────────
  function widgetHeightPx(w) { return w.h * ROW_H + (w.h - 1) * GAP - 30 - 8; }

  function place(el, w) {
    el.style.gridColumn = (w.x + 1) + ' / span ' + w.w;
    el.style.gridRow = (w.y + 1) + ' / span ' + w.h;
  }

  let wctrl = null;
  function envFor(w, height) {
    return { range, signal: wctrl.signal, vars, values, ctx, height, compare, eventsCache: sharedEvents };
  }
  let sharedEvents = new Map();
  function drawWidgets() {
    if (wctrl) wctrl.abort();
    wctrl = new AbortController();
    sharedEvents = new Map();
    signal.addEventListener('abort', () => wctrl && wctrl.abort(), { once: true });
    clear(grid);
    grid.classList.toggle('editing', editing);
    if (editing) grid.setAttribute('data-no-autorefresh', '1'); else grid.removeAttribute('data-no-autorefresh');
    if (!widgets.length) {
      grid.appendChild(h('div', { style: { gridColumn: '1 / -1', gridRow: '1 / span 3' } }, empty(t('dash.empty_title'), t('dash.empty_text'),
        writable ? h('button', { class: 'btn primary', type: 'button', onclick: () => { if (!editing) { editing = true; ctx.setParams({ edit: '1' }, { silent: true }); drawHead(); } editWidget(null); } }, icon('plus', 14), t('dash.add_widget')) : null)));
      return;
    }
    for (const w of widgets) grid.appendChild(widgetEl(w));
  }

  function widgetEl(w) {
    const body = h('div', { class: 'widget-body' });
    const el = h('div', { class: ['widget', w.type === 'group' ? 'w-group' : null], dataset: { wid: w.id }, style: w.type === 'group' && w.options.color ? { borderTopColor: 'var(--' + w.options.color + ')' } : null },
      h('div', { class: 'widget-head' }, h('h3', { title: w.title || '' }, valueRefs(w.title || '', vars, values) || t('widget.type.' + w.type)),
        h('div', { class: 'widget-tools' },
          h('button', { class: 'btn ghost sm icon', type: 'button', title: t('action.edit'), onclick: (e) => { e.stopPropagation(); editWidget(w); } }, icon('edit', 12)),
          h('button', { class: 'btn ghost sm icon', type: 'button', title: t('action.duplicate'), onclick: (e) => { e.stopPropagation(); duplicate(w); } }, icon('copy', 12)),
          h('button', { class: 'btn ghost sm icon', type: 'button', title: t('action.remove'), onclick: (e) => { e.stopPropagation(); widgets = widgets.filter((x) => x !== w); markDirty(); drawWidgets(); } }, icon('trash', 12)))),
      body,
      h('div', { class: 'resize', title: t('dash.resize') }));
    place(el, w);
    if (editing) attachDrag(el, w);
    renderWidget(body, w, envFor(w, widgetHeightPx(w) - 8));
    return el;
  }

  function duplicate(w) {
    const copy = JSON.parse(JSON.stringify(w));
    copy.id = nextId();
    copy.y = w.y + w.h;
    widgets.push(copy);
    resolve(widgets, copy);
    markDirty();
    drawWidgets();
  }

  function nextId() {
    let i = widgets.length + 1;
    while (widgets.some((w) => w.id === 'w' + i)) i++;
    return 'w' + i;
  }

  function attachDrag(el, w) {
    const headEl = el.querySelector('.widget-head');
    const handle = el.querySelector('.resize');
    const start = (e, mode) => {
      if (e.button !== 0 || e.target.closest('button')) return;
      e.preventDefault();
      const rect = grid.getBoundingClientRect();
      const colW = (rect.width + GAP) / 12;
      const rowW = ROW_H + GAP;
      const sx = e.clientX; const sy = e.clientY;
      const orig = { x: w.x, y: w.y, w: w.w, h: w.h };
      el.classList.add('dragging');
      const move = (ev) => {
        const dx = Math.round((ev.clientX - sx) / colW);
        const dy = Math.round((ev.clientY - sy) / rowW);
        if (mode === 'move') {
          w.x = Math.max(0, Math.min(12 - w.w, orig.x + dx));
          w.y = Math.max(0, orig.y + dy);
        } else {
          w.w = Math.max(2, Math.min(12 - w.x, orig.w + dx));
          w.h = Math.max(1, orig.h + dy);
        }
        place(el, w);
      };
      const up = () => {
        document.removeEventListener('pointermove', move);
        document.removeEventListener('pointerup', up);
        el.classList.remove('dragging');
        if (w.x !== orig.x || w.y !== orig.y || w.w !== orig.w || w.h !== orig.h) {
          resolve(widgets, w);
          markDirty();
          drawWidgets();
        }
      };
      document.addEventListener('pointermove', move);
      document.addEventListener('pointerup', up);
    };
    headEl.addEventListener('pointerdown', (e) => start(e, 'move'));
    handle.addEventListener('pointerdown', (e) => start(e, 'resize'));
  }

  function editMeta() {
    const title = h('input', { class: 'input', value: dash.title || '' });
    const desc = h('textarea', { class: 'input', rows: 2 });
    desc.value = dash.description || '';
    const folder = folderInput(dash.folder || '', []);
    get('/dashboards', null, { signal }).then((r) => {
      const dl = folder.querySelector('datalist');
      Array.from(new Set(asList(r, 'dashboards').map((d) => d.folder).filter(Boolean))).sort().forEach((f) => dl.appendChild(h('option', { value: f })));
    }).catch(() => {});
    const refresh = select(REFRESH.map((s2) => ({ value: s2, label: s2 ? t('dash.refresh_every', { s: s2 >= 60 ? (s2 / 60) + ' min' : s2 + ' s' }) : t('dash.refresh_off') })), Number(dash.refresh_s) || 0);
    const m = modal({
      title: t('dash.settings'),
      body: h('div', { class: 'col gap-3' }, field(t('dash.f.title'), title), field(t('dash.f.description'), desc), field(t('dash.f.folder'), folder), field(t('dash.f.refresh'), refresh, t('dash.f.refresh_hint'))),
      actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')),
        h('button', { class: 'btn primary', type: 'button', onclick: () => {
          if (!title.value.trim()) return;
          Object.assign(dash, { title: title.value.trim(), description: desc.value, folder: folder.input.value.trim(), refresh_s: Number(refresh.value) || null });
          refreshS = Number(dash.refresh_s) || 0;
          markDirty(); m.close(); drawHead();
          ctx.setCrumbs([{ label: t('nav.dashboards'), href: href('/dashboards') }, { label: dash.title }]);
        } }, t('action.apply'))],
    });
  }

  function editVars() {
    let list = vars.map((v) => Object.assign({}, v));
    const box = h('div', { class: 'col gap-3' });
    const redraw = () => {
      clear(box);
      box.appendChild(h('p', { class: 'small muted' }, t('dash.vars_help')));
      box.appendChild(h('p', { class: 'small muted' }, t('dash.vars_help_multi')));
      list.forEach((v, i) => {
        const name = h('input', { class: 'input mono', value: v.name || '', placeholder: 'host' });
        const tag = h('input', { class: 'input mono', value: v.tag || '', placeholder: 'host' });
        const def = h('input', { class: 'input mono', value: v.multi ? (Array.isArray(v.defaults) && v.defaults.length ? v.defaults.join(',') : v.default || '*') : v.default || '*', placeholder: '*' });
        const multi = h('input', { type: 'checkbox', checked: !!v.multi });
        name.addEventListener('input', () => { v.name = name.value.trim().replace(/[^\w.-]/g, ''); });
        tag.addEventListener('input', () => { v.tag = tag.value.trim(); });
        def.addEventListener('input', () => { v.default = def.value.trim() || '*'; });
        multi.addEventListener('change', () => { v.multi = multi.checked; });
        box.appendChild(h('div', { class: 'row top' }, field(t('dash.var.name'), name), field(t('dash.var.tag'), tag), field(t('dash.var.default'), def),
          h('label', { class: 'check', style: { marginTop: '22px' } }, multi, t('dash.var.multi')),
          h('button', { class: 'btn ghost icon', type: 'button', style: { marginTop: '20px' }, title: t('action.remove'), onclick: () => { list.splice(i, 1); redraw(); } }, icon('trash', 13))));
      });
      box.appendChild(h('div', h('button', { class: 'btn sm', type: 'button', onclick: () => { list.push({ name: '', tag: '', default: '*' }); redraw(); } }, icon('plus', 12), t('dash.var.add'))));
    };
    redraw();
    const m = modal({
      title: t('dash.vars'), body: box, wide: true,
      actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')),
        h('button', { class: 'btn primary', type: 'button', onclick: () => {
          vars = list.filter((v) => v.name).map((v) => {
            const out = { name: v.name, tag: v.tag || v.name, default: '*' };
            const def = String(v.default || '*');
            if (v.multi) { out.multi = true; out.defaults = def.split(',').map((x) => x.trim()).filter(Boolean); out.default = out.defaults[0] || '*'; } else out.default = def.split(',')[0].trim() || '*';
            return out;
          });
          const fresh = readValues(vars, new URLSearchParams());
          for (const v of vars) if (values[v.name] === undefined || Array.isArray(values[v.name]) !== !!v.multi) values[v.name] = fresh[v.name];
          markDirty(); m.close(); drawVars(); drawWidgets();
        } }, t('action.apply'))],
    });
  }

  function editWidget(existing) {
    openWidgetEditor({
      existing, nested: false, ctx, signal, vars, values, getRange: () => range, href,
      onApply: (w) => {
        if (!existing) {
          w.id = nextId();
          w.y = widgets.reduce((acc, x) => Math.max(acc, x.y + x.h), 0);
          widgets.push(normWidget(w, widgets.length));
        } else {
          const i = widgets.indexOf(existing);
          if (i >= 0) widgets[i] = normWidget(Object.assign(w, { id: existing.id, x: existing.x, y: existing.y, w: existing.w, h: existing.h }), i);
        }
        markDirty();
        drawWidgets();
      },
    });
  }

  function drawAll() { drawHead(); drawVars(); drawWidgets(); armRefresh(); }
  drawAll();
}

// ── Éditeur de widget (aussi pour les enfants d'un groupe) ────────────────
function openWidgetEditor(o) {
  const { existing, nested, ctx, signal, vars, values, href } = o;
  const isNew = !existing;
  let w = existing ? JSON.parse(JSON.stringify(existing)) : null;
  const box = h('div', { class: 'col gap-3' });
  const preview = h('div', { class: 'widget', style: { height: '260px' } });
  const types = nested ? WIDGET_TYPES.filter((ty) => ty !== 'group') : WIDGET_TYPES;

  const pickType = (type) => {
    const d = JSON.parse(JSON.stringify(WIDGET_DEFAULTS[type]));
    const keep = w ? { title: w.title } : {};
    w = Object.assign({ type, title: '' }, d, keep);
    if (nested) { w.w = Math.min(d.w, 6); w.h = Math.min(d.h, 3); }
    drawForm();
  };

  function typePicker() {
    const wrap = h('div', { class: 'col', style: { gap: '8px' } });
    for (const [fam, list] of FAMILIES) {
      const items = list.filter((ty) => types.includes(ty));
      if (!items.length) continue;
      wrap.appendChild(h('div', { class: 'xsmall muted', style: { textTransform: 'uppercase', letterSpacing: '.05em' } }, t('dash.family.' + fam)));
      wrap.appendChild(h('div', { class: 'type-pick' }, items.map((ty) => h('button', { type: 'button', class: w && w.type === ty ? 'sel' : null, onclick: () => pickType(ty) },
        h('b', t('widget.type.' + ty)), h('span', t('widget.desc.' + ty))))));
    }
    return wrap;
  }

  function numInput(obj, key, attrs) {
    const el = h('input', Object.assign({ class: 'input', type: 'number', value: obj[key] ?? '' }, attrs || {}));
    el.addEventListener('change', () => { if (el.value === '') delete obj[key]; else obj[key] = Number(el.value); refresh(); });
    return el;
  }
  function textInput(obj, key, ph, mono) {
    const el = h('input', { class: ['input', mono ? 'mono' : null], value: obj[key] || '', placeholder: ph || '', spellcheck: 'false' });
    el.addEventListener('input', () => { obj[key] = el.value; refresh(); });
    return el;
  }
  function checkInput(obj, key, label, def) {
    const cb = h('input', { type: 'checkbox', checked: obj[key] === undefined ? !!def : !!obj[key] });
    cb.addEventListener('change', () => { obj[key] = cb.checked; refresh(); });
    return h('label', { class: 'check' }, cb, label);
  }

  function drawForm() {
    clear(box);
    box.appendChild(field(t('dash.w.type'), typePicker()));
    if (!w) return;
    const title = h('input', { class: 'input', value: w.title || '', placeholder: t('widget.type.' + w.type) });
    title.addEventListener('input', () => { w.title = title.value; });
    box.appendChild(field(t('dash.w.title'), title, t('dash.w.title_hint')));
    if (nested) {
      box.appendChild(h('div', { class: 'form-grid' },
        field(t('dash.w.cols'), select(Array.from({ length: 12 }, (_, i) => ({ value: i + 1, label: String(i + 1) })), w.w || 4, (v) => { w.w = Number(v); })),
        field(t('dash.w.rows'), select(Array.from({ length: 8 }, (_, i) => ({ value: i + 1, label: String(i + 1) })), w.h || 2, (v) => { w.h = Number(v); }))));
    }
    w.options = w.options || {};
    const op = w.options;
    if (METRIC_TYPES.includes(w.type)) metricForm(op);
    else if (w.type === 'log_stream') {
      box.appendChild(field(t('dash.w.log_query'), textInput(op, 'log_query', 'status:error $host', true), t('dash.w.log_query_hint')));
    } else if (w.type === 'note') {
      const txt = h('textarea', { class: 'input', rows: 6 });
      txt.value = op.text || '';
      txt.addEventListener('input', () => { op.text = txt.value; refresh(); });
      box.appendChild(field(t('dash.w.text'), txt, t('dash.w.text_hint')));
    } else if (w.type === 'host_map') {
      box.appendChild(field(t('map.color_by'), select(MAP_METRICS.map((mm) => ({ value: mm.key, label: t(mm.label) })), op.metric || 'cpu', (v) => { op.metric = v; refresh(); })));
    } else if (w.type === 'slo') {
      const slot = h('div', h('span', { class: 'spinner' }));
      box.appendChild(field(t('dash.w.slo'), slot, t('dash.w.slo_hint')));
      get('/slos', null, { signal }).then((r) => {
        const list = asList(r, 'slos');
        clear(slot);
        if (!list.length) { slot.appendChild(h('div', { class: 'small muted' }, t('slo.none_title'), ' ', h('a', { href: href('/slos/new') }, t('slo.new')))); return; }
        if (op.slo_id === null || op.slo_id === undefined) { op.slo_id = list[0].id; refresh(); }
        slot.appendChild(select(list.map((x) => ({ value: x.id, label: x.name || '#' + x.id })), op.slo_id, (v) => { op.slo_id = /^\d+$/.test(v) ? Number(v) : v; refresh(); }));
      }).catch((e) => { clear(slot); slot.appendChild(h('div', { class: 'small', style: { color: 'var(--crit)' } }, errorMessage(e))); });
    } else if (w.type === 'check_status') {
      const c = h('input', { class: 'input mono', value: op.check || '', placeholder: 'systemd.unit' });
      c.addEventListener('change', () => { op.check = c.value.trim(); refresh(); });
      box.appendChild(field(t('dash.w.check'), c, t('dash.w.check_hint')));
    } else if (w.type === 'event_timeline') {
      box.appendChild(field(t('dash.w.events_q'), textInput(op, 'events_q', 'source:deploy', true), t('dash.w.events_q_hint')));
    } else if (w.type === 'alert_graph' || w.type === 'alert_value') {
      const slot = h('div', h('span', { class: 'spinner' }));
      box.appendChild(field(t('dash.w.monitor'), slot));
      get('/monitors', null, { signal }).then((r) => {
        const list = asList(r, 'monitors');
        clear(slot);
        if (!list.length) { slot.appendChild(h('div', { class: 'small muted' }, t('dash.w.no_monitor'))); return; }
        if (op.monitor_id === null || op.monitor_id === undefined) { op.monitor_id = list[0].id; refresh(); }
        slot.appendChild(select(list.map((x) => ({ value: x.id, label: (x.name || '#' + x.id) + (x.type ? ' · ' + x.type : '') })), op.monitor_id, (v) => { op.monitor_id = /^\d+$/.test(v) ? Number(v) : v; refresh(); }));
      }).catch((e) => { clear(slot); slot.appendChild(h('div', { class: 'small', style: { color: 'var(--crit)' } }, errorMessage(e))); });
      if (w.type === 'alert_graph') box.appendChild(field(t('dash.w.events_q'), textInput(op, 'events_q', 'source:deploy', true), t('dash.w.events_q_hint')));
      else box.appendChild(h('div', { class: 'form-grid' }, field(t('dash.w.unit'), select(UNITS.map((u) => ({ value: u, label: u ? t('unitname.' + u) : t('dash.w.unit_auto') })), op.unit || '', (v) => { if (v) op.unit = v; else delete op.unit; refresh(); })), field(t('dash.w.precision'), numInput(op, 'precision', { min: 0, max: 6 }))));
    } else if (w.type === 'network_map') {
      box.appendChild(field(t('net.map_tag'), textInput(op, 'tag', 'role:web', true), t('dash.w.net_tag_hint')));
    } else if (w.type === 'problems') {
      box.appendChild(h('div', { class: 'form-grid' },
        field(t('dash.w.priority'), select([{ value: '', label: t('dash.w.priority_all') }].concat([1, 2, 3, 4, 5].map((p) => ({ value: p, label: 'P' + p }))), op.priority || '', (v) => { op.priority = v; refresh(); })),
        field(t('dash.w.acked'), select([{ value: 'false', label: t('dash.w.acked_no') }, { value: 'true', label: t('dash.w.acked_yes') }, { value: 'all', label: t('dash.w.acked_all') }], op.acked || 'false', (v) => { op.acked = v; refresh(); })),
        field(t('dash.w.limit'), numInput(op, 'limit', { min: 1, max: 100 }))));
    } else if (w.type === 'slo_list') {
      box.appendChild(field(t('dash.w.slo_filter'), textInput(op, 'q', 'api'), t('dash.w.slo_filter_hint')));
    } else if (w.type === 'iframe') {
      const inp = textInput(op, 'url', '/status/?p=s33d', true);
      const warn = h('div', { class: 'xsmall', style: { color: 'var(--crit)' }, hidden: true }, t('widget.iframe.bad_url'));
      inp.addEventListener('input', () => { warn.hidden = !op.url || !!sameOriginUrl(op.url); });
      box.appendChild(field(t('dash.w.url'), inp, t('dash.w.iframe_hint')));
      box.appendChild(warn);
    } else if (w.type === 'image') {
      const inp = textInput(op, 'url', 'https://…/schema.png', true);
      const warn = h('div', { class: 'xsmall', style: { color: 'var(--crit)' }, hidden: true }, t('widget.image.bad_url'));
      inp.addEventListener('input', () => { warn.hidden = !op.url || !!imageUrl(op.url); });
      box.appendChild(field(t('dash.w.url'), inp, t('dash.w.image_hint')));
      box.appendChild(warn);
      box.appendChild(h('div', { class: 'form-grid' },
        field(t('dash.w.fit'), select([{ value: 'contain', label: t('dash.w.fit.contain') }, { value: 'cover', label: t('dash.w.fit.cover') }], op.fit || 'contain', (v) => { op.fit = v; refresh(); })),
        field(t('dash.w.alt'), textInput(op, 'alt', ''))));
    } else if (w.type === 'group') {
      groupForm(op);
    }
    box.appendChild(field(t('dash.w.preview'), preview));
    refresh();
  }

  function metricForm(op) {
    const max = MAX_QUERIES[w.type] || 1;
    const qBox = h('div', { class: 'col', style: { gap: '6px' } });
    const drawQueries = () => {
      clear(qBox);
      if (!w.queries.length) w.queries.push({ q: '', display: 'line' });
      while (w.type === 'scatter' && w.queries.length < 2) w.queries.push({ q: '' });
      op.aggs = Array.isArray(op.aggs) ? op.aggs : [];
      op.aliases = Array.isArray(op.aliases) ? op.aliases : [];
      w.queries.slice(0, max).forEach((q, i) => {
        const inp = h('input', { class: 'input mono', value: q.q || '', spellcheck: 'false', placeholder: 'avg:system.cpu.usage{$host} by {host}' });
        inp.addEventListener('input', () => { q.q = inp.value; refresh(); });
        const row = h('div', { class: 'row' }, h('span', { class: 'qid', style: { background: 'var(--s' + ((i % 8) + 1) + ')' } }, w.type === 'scatter' ? (i === 0 ? 'X' : 'Y') : 'abcdefgh'[i]), h('span', { class: 'grow' }, inp));
        if (w.type === 'timeseries') {
          row.appendChild(select(['line', 'area', 'bars'].map((d) => ({ value: d, label: t('display.' + d) })), q.display || 'line', (v) => { q.display = v; refresh(); }, { style: { width: '110px' } }));
        }
        if (w.type === 'table') {
          const al = h('input', { class: 'input', value: op.aliases[i] || '', placeholder: t('dash.w.alias'), style: { width: '120px' } });
          al.addEventListener('input', () => { op.aliases[i] = al.value; refresh(); });
          row.appendChild(al);
          row.appendChild(select(AGGS.map((a) => ({ value: a, label: t('agg.' + a) })), op.aggs[i] || 'avg', (v) => { op.aggs[i] = v; refresh(); }, { style: { width: '130px' } }));
        }
        if (max > 1 && w.queries.length > 1 && w.type !== 'scatter') row.appendChild(h('button', { class: 'btn ghost icon', type: 'button', title: t('action.remove'), onclick: () => { w.queries.splice(i, 1); op.aggs.splice(i, 1); op.aliases.splice(i, 1); drawQueries(); refresh(); } }, icon('trash', 13)));
        qBox.appendChild(row);
      });
      if (max > 1 && w.type !== 'scatter' && w.queries.length < max) qBox.appendChild(h('div', h('button', { class: 'btn sm', type: 'button', onclick: () => { w.queries.push({ q: '', display: 'line' }); drawQueries(); } }, icon('plus', 12), t('metrics.add_query'))));
    };
    drawQueries();
    box.appendChild(field(t('dash.w.queries'), qBox, w.type === 'scatter' ? t('dash.w.scatter_hint') : t('dash.w.queries_hint')));
    if (FORMULA_TYPES.includes(w.type)) {
      const fBox = h('div', { class: 'col', style: { gap: '6px' } });
      const drawF = () => {
        clear(fBox);
        op.formulas = Array.isArray(op.formulas) ? op.formulas : [];
        op.formulas.forEach((f, i) => {
          const inp = h('input', { class: 'input mono', value: f.formula || '', placeholder: 'a / b * 100', spellcheck: 'false' });
          inp.addEventListener('input', () => { f.formula = inp.value; refresh(); });
          const al = h('input', { class: 'input', value: f.alias || '', placeholder: t('metrics.formula_alias_ph'), style: { width: '160px' } });
          al.addEventListener('input', () => { f.alias = al.value; refresh(); });
          fBox.appendChild(h('div', { class: 'row' }, h('span', { class: 'qid fid' }, 'f' + (i + 1)), h('span', { class: 'grow' }, inp), al,
            h('button', { class: 'btn ghost icon', type: 'button', title: t('action.remove'), onclick: () => { op.formulas.splice(i, 1); drawF(); refresh(); } }, icon('trash', 13))));
        });
        fBox.appendChild(h('div', { class: 'row' }, h('button', { class: 'btn sm', type: 'button', onclick: () => { op.formulas.push({ formula: '', alias: '' }); drawF(); } }, icon('fx', 12), t('metrics.add_formula')),
          op.formulas.length ? checkInput(op, 'show_queries', t('dash.w.show_queries'), false) : null));
      };
      drawF();
      box.appendChild(field(t('metrics.formulas'), fBox, t('dash.w.formulas_hint')));
    }
    const unit = select(UNITS.map((u) => ({ value: u, label: u ? t('unitname.' + u) : t('dash.w.unit_auto') })), op.unit || '', (v) => { if (v) op.unit = v; else delete op.unit; refresh(); });
    box.appendChild(h('div', { class: 'form-grid' }, field(t('dash.w.unit'), unit), field(t('dash.w.precision'), numInput(op, 'precision', { min: 0, max: 6 }))));
    if (w.type === 'timeseries') {
      box.appendChild(field(t('dash.w.events_q'), textInput(op, 'events_q', 'source:deploy', true), t('dash.w.events_q_hint')));
      box.appendChild(h('div', { class: 'row wrap' }, checkInput(op, 'y_log', t('dash.w.y_log'), false), checkInput(op, 'legend', t('dash.w.legend'), true)));
    }
    if (w.type === 'change') {
      box.appendChild(h('div', { class: 'form-grid' },
        field(t('dash.w.shift'), select(['period', 'hour', 'day', 'week'].map((k) => ({ value: k, label: t('widget.change.shift.' + k) })), op.shift || 'period', (v) => { op.shift = v; refresh(); })),
        field(t('dash.w.mode'), select([{ value: 'pct', label: t('dash.w.mode.pct') }, { value: 'abs', label: t('dash.w.mode.abs') }], op.mode || 'pct', (v) => { op.mode = v; refresh(); })),
        field(t('dash.w.agg'), select(AGGS.map((a) => ({ value: a, label: t('agg.' + a) })), op.agg || 'avg', (v) => { op.agg = v; refresh(); })),
        h('div', { class: 'col', style: { justifyContent: 'flex-end' } }, checkInput(op, 'increase_good', t('dash.w.increase_good'), true))));
    }
    if (w.type === 'pie') box.appendChild(field(t('dash.w.agg'), select(AGGS.map((a) => ({ value: a, label: t('agg.' + a) })), op.agg || 'last', (v) => { op.agg = v; refresh(); })));
    if (w.type === 'heatmap') box.appendChild(field(t('dash.w.buckets'), numInput(op, 'buckets', { min: 4, max: 40 })));
    if (w.type === 'distribution') box.appendChild(field(t('dash.w.bins'), numInput(op, 'bins', { min: 5, max: 60 })));
    if (w.type === 'scatter') {
      box.appendChild(h('div', { class: 'form-grid' },
        field(t('dash.w.x_label'), textInput(op, 'x_label', 'CPU')), field(t('dash.w.y_label'), textInput(op, 'y_label', 'Mémoire')),
        field(t('dash.w.x_unit'), select(UNITS.map((u) => ({ value: u, label: u ? t('unitname.' + u) : t('dash.w.unit_auto') })), op.x_unit || '', (v) => { op.x_unit = v || undefined; refresh(); })),
        field(t('dash.w.y_unit'), select(UNITS.map((u) => ({ value: u, label: u ? t('unitname.' + u) : t('dash.w.unit_auto') })), op.y_unit || '', (v) => { op.y_unit = v || undefined; refresh(); })),
        field(t('dash.w.agg'), select(AGGS.map((a) => ({ value: a, label: t('agg.' + a) })), op.agg || 'avg', (v) => { op.agg = v; refresh(); }))));
    }
    if (THRESHOLD_TYPES.includes(w.type)) {
      const thBox = h('div', { class: 'col', style: { gap: '6px' } });
      const drawTh = () => {
        clear(thBox);
        op.thresholds = Array.isArray(op.thresholds) ? op.thresholds : [];
        op.thresholds.forEach((th, i) => {
          const v = h('input', { class: 'input', type: 'number', step: 'any', value: th.value ?? '', style: { width: '120px' } });
          v.addEventListener('input', () => { th.value = Number(v.value); refresh(); });
          thBox.appendChild(h('div', { class: 'row' }, h('span', { class: 'small muted' }, t('dash.w.from_value')), v,
            select(['ok', 'warn', 'alert', 'info'].map((c) => ({ value: c, label: t('dash.color.' + c) })), th.color || 'warn', (c) => { th.color = c; refresh(); }, { style: { width: '130px' } }),
            h('button', { class: 'btn ghost icon', type: 'button', onclick: () => { op.thresholds.splice(i, 1); drawTh(); refresh(); } }, icon('trash', 13))));
        });
        thBox.appendChild(h('div', h('button', { class: 'btn sm', type: 'button', onclick: () => { op.thresholds.push({ value: 0, color: 'warn' }); drawTh(); } }, icon('plus', 12), t('dash.w.add_threshold'))));
      };
      drawTh();
      box.appendChild(field(t('dash.w.thresholds'), thBox));
    }
  }

  function groupForm(op) {
    op.widgets = asList(op.widgets);
    const list = h('div', { class: 'col', style: { gap: '6px' } });
    const drawKids = () => {
      clear(list);
      if (!op.widgets.length) list.appendChild(h('div', { class: 'small muted' }, t('widget.group.empty')));
      op.widgets.forEach((c, i) => {
        list.appendChild(h('div', { class: 'row group-kid' },
          h('span', { class: 'badge muted' }, t('widget.type.' + c.type)),
          h('span', { class: 'grow ellipsis' }, c.title || '-'),
          h('span', { class: 'xsmall muted' }, (c.w || 4) + ' × ' + (c.h || 2)),
          h('button', { class: 'btn ghost sm icon', type: 'button', disabled: i === 0, title: t('synth.step.up'), onclick: () => { [op.widgets[i - 1], op.widgets[i]] = [op.widgets[i], op.widgets[i - 1]]; drawKids(); refresh(); } }, icon('up', 12)),
          h('button', { class: 'btn ghost sm icon', type: 'button', disabled: i === op.widgets.length - 1, title: t('synth.step.down'), onclick: () => { [op.widgets[i + 1], op.widgets[i]] = [op.widgets[i], op.widgets[i + 1]]; drawKids(); refresh(); } }, icon('down', 12)),
          h('button', { class: 'btn ghost sm icon', type: 'button', title: t('action.edit'), onclick: () => openWidgetEditor(Object.assign({}, o, { existing: c, nested: true, onApply: (nw) => { op.widgets[i] = normChild(Object.assign(nw, { id: c.id }), i); drawKids(); refresh(); } })) }, icon('edit', 12)),
          h('button', { class: 'btn ghost sm icon danger', type: 'button', title: t('action.remove'), onclick: () => { op.widgets.splice(i, 1); drawKids(); refresh(); } }, icon('trash', 12))));
      });
      list.appendChild(h('div', h('button', { class: 'btn sm', type: 'button', onclick: () => openWidgetEditor(Object.assign({}, o, { existing: null, nested: true, onApply: (nw) => { op.widgets.push(normChild(Object.assign(nw, { id: 'c' + (op.widgets.length + 1) }), op.widgets.length)); drawKids(); refresh(); } })) }, icon('plus', 12), t('dash.w.add_child'))));
    };
    drawKids();
    box.appendChild(field(t('dash.w.children'), list, t('dash.w.children_hint')));
    box.appendChild(field(t('dash.w.group_color'), select([{ value: '', label: t('dash.w.color_none') }, { value: 'accent', label: t('dash.color.info') }, { value: 'ok', label: t('dash.color.ok') }, { value: 'warn', label: t('dash.color.warn') }, { value: 'crit', label: t('dash.color.alert') }], op.color || '', (v) => { op.color = v; })));
  }

  let pctrl = null;
  const refreshNow = () => {
    if (!w) return;
    if (pctrl) pctrl.abort();
    pctrl = new AbortController();
    clear(preview);
    const body = h('div', { class: 'widget-body', style: { paddingTop: '8px' } });
    preview.appendChild(body);
    renderWidget(body, w, { range: o.getRange(), signal: pctrl.signal, vars, values, ctx, height: 230 });
  };
  let timer = null;
  const refresh = () => { clearTimeout(timer); timer = setTimeout(refreshNow, 450); };

  drawForm();
  const m = modal({
    title: isNew ? (nested ? t('dash.w.add_child') : t('dash.add_widget')) : t('dash.edit_widget'), body: box, wide: true,
    onClose: () => { if (pctrl) pctrl.abort(); clearTimeout(timer); },
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')),
      h('button', { class: 'btn primary', type: 'button', onclick: () => {
        if (!w) return;
        w.queries = (w.queries || []).filter((q) => q.q && q.q.trim());
        if (w.options && Array.isArray(w.options.formulas)) w.options.formulas = w.options.formulas.filter((f) => f.formula && f.formula.trim());
        if (w.type === 'iframe' && !sameOriginUrl(w.options.url)) { toast(t('widget.iframe.bad_url'), 'error'); return; }
        if (w.type === 'image' && !imageUrl(w.options.url)) { toast(t('widget.image.bad_url'), 'error'); return; }
        o.onApply(w);
        m.close();
      } }, isNew ? t('dash.add') : t('action.apply'))],
  });
}
