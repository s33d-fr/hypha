// Filtres d'exclusion des journaux (§ 11) : volume, liste ordonnée, formulaire avec aperçu.
import { h, clear, icon, debounce } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, post, put, del, asList, isAbort } from '../api.js';
import { fmtInt, fmtCompact, fmtBytes, fmtRatioPct, fmtPct, fmtRel, fmtDateTime, isNum } from '../format.js';
import { section, empty, panel, modal, field, pills, toast, confirmDialog, errorMessage } from '../components/ui.js';
import { dataTable, barCell } from '../components/table.js';
import { compactLogRow } from './logs-shared.js';
import { qv } from './common.js';

export function canWriteFilters(me) {
  return !!me && ['owner', 'admin'].includes(me.role);
}

function actionLabel(f) {
  if (f.action === 'sample') return t('lf.action.sample_keep', { pct: fmtPct((isNum(f.sample_rate) ? f.sample_rate : 0) * 100, 1) });
  return t('lf.action.exclude');
}

function filterBody(f) {
  return {
    name: String(f.name || '').trim(),
    query: String(f.query || '').trim(),
    action: f.action === 'sample' ? 'sample' : 'exclude',
    sample_rate: f.action === 'sample' ? Math.max(0, Math.min(1, Number(f.sample_rate) || 0)) : 0,
    enabled: f.enabled !== false,
    position: isNum(f.position) ? f.position : 0,
  };
}

// Formulaire de création ou d'édition. opts : {query, filter, position, onSaved}
export function openFilterForm(ctx, opts = {}) {
  const editing = !!(opts.filter && opts.filter.id !== undefined);
  const f = Object.assign({ name: '', query: opts.query || '', action: 'exclude', sample_rate: 0.1, enabled: true, position: 0 }, opts.filter || {});
  if (f.action === 'sample' && !(f.sample_rate > 0)) f.sample_rate = 0.1;
  const name = h('input', { class: 'input', value: f.name, maxlength: 200, placeholder: t('lf.f.name_ph') });
  const query = h('input', { class: 'input mono', value: f.query, spellcheck: 'false', placeholder: 'service:uvicorn "GET /health"' });
  const pct = h('input', { class: 'input', type: 'number', min: 0, max: 100, step: 'any', value: String(Math.round((f.sample_rate || 0) * 1000) / 10), style: { width: '110px' } });
  const pctField = field(t('lf.f.keep_pct'), h('div', { class: 'row' }, pct, h('span', { class: 'muted' }, '%')), t('lf.f.keep_hint'));
  const actionSlot = h('div');
  const preview = h('div', { class: 'col', style: { gap: '8px' } });
  const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
  const saveBtn = h('button', { class: 'btn primary', type: 'button' }, editing ? t('action.save') : t('lf.create'));
  let last = null;

  const drawAction = () => {
    clear(actionSlot);
    actionSlot.appendChild(pills([{ key: 'exclude', label: t('lf.action.exclude') }, { key: 'sample', label: t('lf.action.sample') }], f.action, (k) => { f.action = k; drawAction(); drawGain(); }));
    pctField.hidden = f.action !== 'sample';
  };
  name.addEventListener('input', () => { f.name = name.value; });
  query.addEventListener('input', () => { f.query = query.value; loadPreview(); });
  pct.addEventListener('input', () => { const v = Number(pct.value); f.sample_rate = Number.isFinite(v) ? Math.max(0, Math.min(100, v)) / 100 : 0; drawGain(); });

  const gainEl = h('div', { class: 'small' });
  function drawGain() {
    clear(gainEl);
    if (!last || !isNum(last.per_hour)) return;
    const dropped = f.action === 'sample' ? last.per_hour * (1 - (f.sample_rate || 0)) : last.per_hour;
    gainEl.appendChild(h('span', { class: 'text-2' }, t('lf.preview.gain', { n: fmtInt(Math.round(dropped)) })));
    if (f.action === 'sample') gainEl.appendChild(h('div', { class: 'xsmall muted mt-2' }, t('lf.preview.errors_kept')));
  }

  let pctrl = null;
  const loadPreview = debounce(async () => {
    const q = f.query.trim();
    if (pctrl) pctrl.abort();
    clear(preview);
    last = null;
    drawGain();
    if (!q) { preview.appendChild(h('div', { class: 'muted small' }, t('lf.preview.type_query'))); return; }
    pctrl = new AbortController();
    preview.appendChild(h('div', { class: 'row small muted' }, h('span', { class: 'spinner' }), t('lf.preview.loading')));
    try {
      const r = await get('/logs/filters/preview', { q }, { signal: pctrl.signal });
      last = r || {};
      clear(preview);
      const sample = asList(last.sample, 'logs');
      preview.appendChild(h('div', { class: 'tiles' },
        h('div', { class: 'tile' }, h('div', { class: 't-label' }, t('lf.preview.per_hour')), h('div', { class: 't-value num', style: { fontSize: 'var(--fs-xl)' } }, fmtInt(last.per_hour))),
        h('div', { class: 'tile' }, h('div', { class: 't-label' }, t('lf.preview.share')), h('div', { class: 't-value num', style: { fontSize: 'var(--fs-xl)' } }, fmtRatioPct(last.share, 1)))));
      preview.appendChild(gainEl);
      drawGain();
      preview.appendChild(h('div', { class: 'small text-2' }, sample.length ? t('lf.preview.examples') : t('lf.preview.no_match')));
      if (sample.length) preview.appendChild(h('div', { class: 'log-list panel' }, sample.slice(0, 5).map((l) => compactLogRow(l))));
    } catch (e) {
      if (isAbort(e)) return;
      clear(preview);
      preview.appendChild(h('div', { class: 'callout crit' }, icon('warn', 16), h('span', { class: 'small' }, e.status === 400 ? t('lf.preview.bad_query') + (e.detail ? ' ' + e.detail : '') : errorMessage(e))));
    }
  }, 400);

  drawAction();
  const m = modal({
    title: editing ? t('lf.edit') : t('lf.new'), wide: true,
    onClose: () => { if (pctrl) pctrl.abort(); loadPreview.cancel(); },
    body: h('div', { class: 'grid c2' },
      h('div', { class: 'col gap-3' },
        field(t('lf.f.name'), name),
        field(t('lf.f.query'), query, t('lf.f.query_hint')),
        field(t('lf.f.action'), actionSlot, t('lf.f.action_hint')),
        pctField,
        err),
      h('div', h('div', { class: 'chart-title' }, t('lf.preview.title'), h('span', { class: 'unit' }, t('lf.preview.last_hour'))), preview)),
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), saveBtn],
  });
  loadPreview();

  saveBtn.addEventListener('click', async () => {
    err.hidden = true;
    const body = filterBody(f);
    if (!body.query) { err.textContent = t('form.required', { field: t('lf.f.query') }); err.hidden = false; return; }
    if (!body.name) body.name = body.query.slice(0, 80);
    if (body.action === 'sample' && !(body.sample_rate > 0 && body.sample_rate < 1)) { err.textContent = t('lf.err.rate'); err.hidden = false; return; }
    saveBtn.disabled = true;
    try {
      // Nouveau filtre : placé après les existants (le premier qui correspond décide).
      if (!editing) {
        const list = asList(await get('/logs/filters'), 'filters');
        body.position = list.reduce((mx, x) => Math.max(mx, (isNum(x.position) ? x.position : 0) + 1), 0);
      }
      if (editing) await put('/logs/filters/' + f.id, body); else await post('/logs/filters', body);
      m.close();
      toast(editing ? t('lf.saved') : t('lf.created'), 'ok');
      if (opts.onSaved) opts.onSaved();
    } catch (e) { saveBtn.disabled = false; err.textContent = errorMessage(e); err.hidden = false; }
  });
  return m;
}

// Réglages › Journaux
export async function renderLogFilters(root, ctx) {
  const { signal, params } = ctx;
  const writable = canWriteFilters(ctx.me);
  let span = params.get('span') === '24h' ? '24h' : '1h';
  let filters = [];
  let excluded = new Map();

  const volumeEl = h('div');
  const filtersEl = h('div');
  const countEl = h('span', { class: 'sub' });
  const newBtn = writable ? h('button', { class: 'btn primary sm', type: 'button', onclick: () => openFilterForm(ctx, { onSaved: reloadFilters }) }, icon('plus', 13), t('lf.new')) : null;

  root.appendChild(h('div', { class: 'querybar' }, h('span', { class: 'muted small grow' }, t('lf.intro')),
    pills([{ key: '1h', label: t('time.preset.1h') }, { key: '24h', label: t('time.preset.1d') }], span, (k) => { span = k; ctx.setParams({ span: k === '1h' ? null : k }, { silent: true }); loadVolume(); })));
  root.appendChild(volumeEl);
  root.appendChild(h('div', { class: 'mt-3' }, panel(t('lf.filters'), filtersEl, {
    flush: true, sub: writable ? t('lf.order_hint') : t('lf.read_only'),
    actions: h('span', { class: 'row' }, countEl, newBtn),
  })));

  async function loadVolume() {
    const to = Date.now();
    const from = to - (span === '24h' ? 86400e3 : 3600e3);
    await section(volumeEl, () => get('/logs/volume', { from, to }, { signal }), (v) => {
      v = v || {};
      const total = isNum(v.total) ? v.total : 0;
      excluded = new Map(asList(v.excluded).map((x) => [String(x.filter_id), x.count]));
      const exclSum = asList(v.excluded).reduce((a, x) => a + (isNum(x.count) ? x.count : 0), 0);
      const hours = span === '24h' ? 24 : 1;
      const byService = asList(v.by_service);
      const byHost = asList(v.by_host);
      const bytesSum = byService.reduce((a, x) => a + (isNum(x.bytes) ? x.bytes : 0), 0);
      const tile = (label, value, sub) => h('div', { class: 'tile' }, h('div', { class: 't-label' }, label), h('div', { class: 't-value num', style: { fontSize: 'var(--fs-xl)' } }, value), sub ? h('div', { class: 't-sub' }, sub) : null);
      const volTable = (rows, keyLabel, keyOf) => dataTable({
        columns: [
          { key: 'k', label: keyLabel, value: keyOf, render: (r) => h('span', { class: 'mono small' }, keyOf(r) || t('lf.vol.none')) },
          { key: 'count', label: t('lf.vol.lines'), align: 'r', render: (r) => fmtCompact(r.count) },
          { key: 'share', label: t('lf.vol.share'), align: 'r', value: (r) => r.count, render: (r) => { const p = total ? (r.count || 0) / total * 100 : null; return barCell(p, fmtPct(p, 1)); } },
          { key: 'bytes', label: t('lf.vol.bytes'), align: 'r', render: (r) => fmtBytes(r.bytes) },
          writable ? { key: 'act', label: '', sort: false, render: (r) => (keyOf(r) ? h('button', { class: 'btn ghost sm', type: 'button', title: t('lf.exclude_this'), onclick: (e) => { e.stopPropagation(); openFilterForm(ctx, { query: r._q, onSaved: reloadFilters }); } }, icon('filter', 12), t('lf.filter_btn')) : null) } : null,
        ].filter(Boolean),
        rows, sortKey: 'count', sortDir: 'desc', maxRows: 25, emptyText: t('lf.vol.empty'),
      });
      const svcKey = (r) => r.service || '';
      const hostKey = (r) => r.host || r.hostname || (r.host_id !== undefined && r.host_id !== null ? String(r.host_id) : '');
      byService.forEach((r) => { r._q = r.service ? 'service:' + qv(r.service) : ''; });
      byHost.forEach((r) => { const k = hostKey(r); r._q = k ? 'host:' + qv(k) : ''; });
      return h('div',
        h('div', { class: 'tiles mb-3' },
          tile(t('lf.vol.total'), fmtCompact(total), t('lf.vol.per_hour', { n: fmtInt(Math.round(total / hours)) })),
          tile(t('lf.vol.size'), fmtBytes(bytesSum), t('lf.vol.size_hint')),
          tile(t('lf.vol.excluded'), fmtCompact(exclSum), total + exclSum ? t('lf.vol.excluded_share', { pct: fmtPct(exclSum / (total + exclSum) * 100, 1) }) : null)),
        h('div', { class: 'grid c2' },
          panel(t('lf.vol.by_service'), volTable(byService, t('col.service'), svcKey), { flush: true }),
          panel(t('lf.vol.by_host'), volTable(byHost, t('col.host'), hostKey), { flush: true })));
    });
    drawFilters();
  }

  function drawFilters() {
    clear(filtersEl);
    countEl.textContent = tn('lf.count', filters.length);
    if (!filters.length) {
      filtersEl.appendChild(empty(t('lf.none_title'), t('lf.none_text'), newBtn ? h('button', { class: 'btn primary', type: 'button', onclick: () => openFilterForm(ctx, { onSaved: reloadFilters }) }, icon('plus', 14), t('lf.new')) : null));
      return;
    }
    const table = h('table', { class: 'tbl' });
    table.appendChild(h('thead', h('tr',
      h('th', { style: { width: '54px' } }, '#'), h('th', t('col.enabled')), h('th', t('col.name')), h('th', t('lf.f.query')), h('th', t('lf.f.action')),
      h('th', { class: 'r' }, t('lf.excluded_total')), h('th', { class: 'r' }, t('lf.excluded_span')), h('th', { class: 'r' }, t('col.updated')), h('th', ''))));
    const tbody = h('tbody');
    filters.forEach((f, i) => {
      const toggle = h('input', { type: 'checkbox', checked: f.enabled !== false, disabled: !writable, title: t(f.enabled !== false ? 'lf.disable' : 'lf.enable') });
      toggle.addEventListener('change', () => update(f, { enabled: toggle.checked }));
      const tr = h('tr', { class: ['lf-row', f.enabled === false ? 'off' : null], draggable: writable ? 'true' : null, dataset: { id: f.id } },
        h('td', h('span', { class: 'row', style: { gap: '2px' } }, writable ? h('span', { class: 'lf-grip', title: t('lf.drag') }, icon('grip', 14)) : null, h('span', { class: 'num muted' }, String(i + 1)))),
        h('td', h('label', { class: 'check' }, toggle)),
        h('td', h('b', { style: { fontWeight: '500' } }, f.name || '-')),
        h('td', { class: 'trunc' }, h('span', { class: 'mono small', title: f.query || '' }, f.query || '-')),
        h('td', h('span', { class: ['badge', f.action === 'sample' ? 'info' : 'muted'] }, actionLabel(f))),
        h('td', { class: 'r num' }, fmtCompact(f.excluded_total)),
        h('td', { class: 'r num' }, excluded.has(String(f.id)) ? fmtCompact(excluded.get(String(f.id))) : '-'),
        h('td', { class: 'r' }, h('span', { title: fmtDateTime(f.updated_at) }, fmtRel(f.updated_at))),
        h('td', writable ? h('span', { class: 'row', style: { gap: '2px', justifyContent: 'flex-end' } },
          h('button', { class: 'btn ghost sm icon', type: 'button', title: t('lf.up'), disabled: i === 0, onclick: () => move(i, i - 1) }, '↑'),
          h('button', { class: 'btn ghost sm icon', type: 'button', title: t('lf.down'), disabled: i === filters.length - 1, onclick: () => move(i, i + 1) }, '↓'),
          h('button', { class: 'btn ghost sm icon', type: 'button', title: t('action.edit'), onclick: () => openFilterForm(ctx, { filter: f, onSaved: reloadFilters }) }, icon('edit', 13)),
          h('button', { class: 'btn ghost sm icon danger', type: 'button', title: t('action.delete'), onclick: () => remove(f) }, icon('trash', 13))) : null));
      if (writable) attachDrag(tr, i);
      tbody.appendChild(tr);
    });
    table.appendChild(tbody);
    filtersEl.appendChild(h('div', { class: 'table-wrap' }, table));
    filtersEl.appendChild(h('div', { class: 'tbl-foot' }, t('lf.first_wins')));
  }

  // Glisser-déposer (souris) ; les flèches font la même chose au clavier et au toucher.
  let dragFrom = null;
  function attachDrag(tr, i) {
    tr.addEventListener('dragstart', (e) => { dragFrom = i; tr.classList.add('dragging'); try { e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', String(i)); } catch (x) { /* navigateur ancien */ } });
    tr.addEventListener('dragend', () => { dragFrom = null; tr.classList.remove('dragging'); filtersEl.querySelectorAll('.drop-before, .drop-after').forEach((x) => x.classList.remove('drop-before', 'drop-after')); });
    tr.addEventListener('dragover', (e) => {
      if (dragFrom === null) return;
      e.preventDefault();
      const r = tr.getBoundingClientRect();
      const after = e.clientY > r.top + r.height / 2;
      tr.classList.toggle('drop-after', after);
      tr.classList.toggle('drop-before', !after);
    });
    tr.addEventListener('dragleave', () => tr.classList.remove('drop-before', 'drop-after'));
    tr.addEventListener('drop', (e) => {
      e.preventDefault();
      if (dragFrom === null) return;
      const r = tr.getBoundingClientRect();
      let to = e.clientY > r.top + r.height / 2 ? i + 1 : i;
      if (dragFrom < to) to -= 1;
      move(dragFrom, to);
    });
  }

  async function move(from, to) {
    if (to < 0 || to >= filters.length || from === to) return;
    const before = filters.slice();
    const [it] = filters.splice(from, 1);
    filters.splice(to, 0, it);
    filters.forEach((f, i) => { f.position = i; });
    drawFilters();
    try {
      await post('/logs/filters/reorder', { ids: filters.map((f) => f.id) });
    } catch (e) {
      filters = before;
      drawFilters();
      toast(errorMessage(e), 'error');
    }
  }

  async function update(f, changes) {
    const prev = Object.assign({}, f);
    Object.assign(f, changes);
    drawFilters();
    try {
      await put('/logs/filters/' + f.id, filterBody(f));
      toast(changes.enabled === false ? t('lf.disabled_done') : changes.enabled === true ? t('lf.enabled_done') : t('lf.saved'), 'ok', 2500);
    } catch (e) {
      Object.assign(f, prev);
      drawFilters();
      toast(errorMessage(e), 'error');
    }
  }

  async function remove(f) {
    if (!(await confirmDialog({ title: t('lf.delete_title'), text: t('lf.delete_text', { name: f.name || f.query || '' }), confirmLabel: t('action.delete'), danger: true }))) return;
    try { await del('/logs/filters/' + f.id); toast(t('lf.deleted'), 'ok'); reloadFilters(); } catch (e) { toast(errorMessage(e), 'error'); }
  }

  async function reloadFilters() {
    try {
      filters = asList(await get('/logs/filters', null, { signal }), 'filters').slice().sort((a, b) => (a.position || 0) - (b.position || 0));
      drawFilters();
    } catch (e) {
      if (isAbort(e)) return;
      clear(filtersEl);
      filtersEl.appendChild(h('div', { class: 'state error' }, errorMessage(e)));
    }
  }

  await Promise.all([reloadFilters(), loadVolume()]);
  // Lien profond : #/settings/logs?new=<requête> ouvre le formulaire prérempli.
  if (writable && params.get('new') !== null) openFilterForm(ctx, { query: params.get('new'), onSaved: reloadFilters });
}

