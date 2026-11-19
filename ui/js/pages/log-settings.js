// Réglages › Journaux (§§ 11, 23) : filtres d'exclusion, métriques tirées des journaux, masquage des données sensibles.
import { h, clear, icon, debounce } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, post, put, del, asList, isAbort } from '../api.js';
import { fmtInt, fmtCompact, fmtRel, fmtDateTime, isNum } from '../format.js';
import { empty, panel, modal, field, pills, toast, confirmDialog, errorMessage, select } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { histogramChart } from './logs-shared.js';
import { renderLogFilters } from './log-filters.js';
import { load, canWrite, isAdmin, isMissingRoute, routeErrorState } from './kit.js';

const SUBS = ['filters', 'metrics', 'sensitive'];

export async function renderLogSettings(root, ctx) {
  const sub = SUBS.includes(ctx.params.get('lt')) ? ctx.params.get('lt') : 'filters';
  root.appendChild(h('div', { class: 'row mb-3' }, pills(SUBS.map((k) => ({ key: k, label: t('lset.sub.' + k) })), sub, (k) => ctx.setParams({ lt: k === 'filters' ? null : k }))));
  const body = h('div');
  root.appendChild(body);
  if (sub === 'metrics') return renderLogMetrics(body, ctx);
  if (sub === 'sensitive') return renderSensitive(body, ctx);
  return renderLogFilters(body, ctx);
}

// ── Métriques tirées des journaux ─────────────────────────────────────────
const GROUP_KEYS = ['service', 'host', 'status', 'source'];
const NAME_RE = /^logs\.[a-z0-9_.]{1,120}$/;

function metricExplorerLink(ctx, m) {
  const by = asList(m.group_by).filter((g) => !String(g).startsWith('@')).concat(asList(m.group_by).filter((g) => String(g).startsWith('@')).map((g) => String(g).slice(1)));
  const metric = m.aggregation === 'distribution' ? m.name + '.p95' : m.name;
  return ctx.href('/metrics', { e: JSON.stringify([{ agg: m.aggregation === 'distribution' ? 'avg' : 'sum', metric, filters: [], by }]) });
}

function openMetricForm(ctx, existing, onSaved) {
  const editing = !!existing;
  const m = Object.assign({ name: 'logs.', query: '', group_by: [], aggregation: 'count', attribute: '', enabled: true }, existing || {});
  m.group_by = asList(m.group_by).slice();
  const name = h('input', { class: 'input mono', value: m.name, maxlength: 125, disabled: editing, placeholder: 'logs.nginx.errors' });
  const query = h('input', { class: 'input mono', value: m.query || '', spellcheck: 'false', placeholder: 'service:nginx status:error' });
  const attr = h('input', { class: 'input mono', value: m.attribute || '', placeholder: '@duration', spellcheck: 'false' });
  const aggSlot = h('div');
  const attrField = field(t('lset.m.attribute'), attr, t('lset.m.attribute_hint'));
  const groupBox = h('div', { class: 'tagbox' });
  const groupInput = h('input', { placeholder: t('lset.m.group_ph'), spellcheck: 'false' });
  const enabled = h('input', { type: 'checkbox', checked: m.enabled !== false });
  const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
  const preview = h('div', { class: 'col', style: { gap: '8px' } });
  const saveBtn = h('button', { class: 'btn primary', type: 'button' }, editing ? t('action.save') : t('lset.m.create'));

  const drawAgg = () => {
    clear(aggSlot);
    aggSlot.appendChild(pills([{ key: 'count', label: t('lset.m.agg.count') }, { key: 'distribution', label: t('lset.m.agg.distribution') }], m.aggregation, (k) => { m.aggregation = k; drawAgg(); drawNames(); }));
    attrField.hidden = m.aggregation !== 'distribution';
  };
  const namesEl = h('div', { class: 'xsmall muted mono' });
  const drawNames = () => {
    const n = name.value.trim();
    namesEl.textContent = m.aggregation === 'distribution' ? ['.avg', '.p50', '.p95', '.p99', '.max', '.count'].map((x) => n + x).join('  ') : n;
  };
  const drawGroups = () => {
    clear(groupBox);
    m.group_by.forEach((g, i) => groupBox.appendChild(h('span', { class: 'chip' }, h('span', { class: 'mono', style: { fontSize: '11px' } }, g),
      h('button', { type: 'button', title: t('action.remove'), onclick: () => { m.group_by.splice(i, 1); drawGroups(); loadPreview(); } }, icon('x', 10)))));
    if (m.group_by.length < 5) groupBox.appendChild(groupInput);
    groupBox.appendChild(h('span', { class: 'row', style: { gap: '2px' } }, GROUP_KEYS.filter((k) => !m.group_by.includes(k)).map((k) => h('button', { type: 'button', class: 'btn ghost sm', onclick: () => addGroup(k) }, '+' + k))));
  };
  function addGroup(v) {
    const g = String(v || '').trim();
    if (!g || m.group_by.includes(g) || m.group_by.length >= 5) return;
    if (!GROUP_KEYS.includes(g) && !/^@[\w.\-]+$/.test(g)) { toast(t('lset.m.bad_group'), 'error'); return; }
    m.group_by.push(g);
    groupInput.value = '';
    drawGroups();
    loadPreview();
  }
  groupInput.addEventListener('keydown', (e) => { if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); addGroup(groupInput.value); } });
  name.addEventListener('input', drawNames);
  query.addEventListener('input', () => { m.query = query.value; loadPreview(); });
  attr.addEventListener('input', () => { m.attribute = attr.value.trim(); });

  // Aperçu : nombre de lignes de la dernière heure qui correspondent, et répartition par le premier groupe.
  let pctrl = null;
  const loadPreview = debounce(async () => {
    if (pctrl) pctrl.abort();
    pctrl = new AbortController();
    clear(preview);
    const q = m.query.trim();
    const to = Date.now();
    const from = to - 3600e3;
    preview.appendChild(h('div', { class: 'row small muted' }, h('span', { class: 'spinner' }), t('lf.preview.loading')));
    try {
      const firstGroup = m.group_by[0] || '';
      const attrs = firstGroup.startsWith('@') ? firstGroup.slice(1) : undefined;
      const [hist, facets] = await Promise.all([
        get('/logs/histogram', { q, from, to, buckets: 30 }, { signal: pctrl.signal }),
        firstGroup ? get('/logs/facets', { q, from, to, attrs }, { signal: pctrl.signal }).catch(() => null) : Promise.resolve(null),
      ]);
      clear(preview);
      let total = 0;
      for (const b of asList(hist && hist.buckets)) for (const v of Object.values((b && b[1]) || {})) if (isNum(v)) total += v;
      const vals = facets && Array.isArray(facets[firstGroup]) ? facets[firstGroup] : [];
      preview.appendChild(h('div', { class: 'tiles' },
        h('div', { class: 'tile' }, h('div', { class: 't-label' }, t('lset.m.prev.per_hour')), h('div', { class: 't-value num', style: { fontSize: 'var(--fs-xl)' } }, fmtInt(total))),
        h('div', { class: 'tile' }, h('div', { class: 't-label' }, t('lset.m.prev.series')), h('div', { class: 't-value num', style: { fontSize: 'var(--fs-xl)' } }, fmtInt(firstGroup ? vals.length : 1)), h('div', { class: 't-sub' }, firstGroup ? t('lset.m.prev.series_sub', { g: firstGroup }) : t('lset.m.prev.series_one')))));
      preview.appendChild(histogramChart(hist, { from, to }, { height: 90, legend: false }));
      if (firstGroup) {
        preview.appendChild(h('div', { class: 'small text-2' }, t('lset.m.prev.by', { g: firstGroup })));
        if (!vals.length) preview.appendChild(h('div', { class: 'xsmall muted' }, t('lset.m.prev.no_group')));
        else {
          const max = Math.max(1, ...vals.map((v) => v.count || 0));
          preview.appendChild(h('div', { class: 'toplist' }, vals.slice(0, 8).map((v) => h('div', { class: 'tl-row' },
            h('div', { class: 'tl-bar' }, h('i', { style: { width: Math.max(2, (v.count || 0) / max * 100) + '%' } }), h('span', { class: 'mono' }, String(v.value))),
            h('span', { class: 'num', style: { textAlign: 'right' } }, fmtInt(v.count))))));
        }
      }
    } catch (e) {
      if (isAbort(e)) return;
      clear(preview);
      preview.appendChild(h('div', { class: 'callout crit' }, icon('warn', 16), h('span', { class: 'small' }, e.status === 400 ? t('lf.preview.bad_query') + (e.detail ? ' ' + e.detail : '') : errorMessage(e))));
    }
  }, 450);

  drawAgg();
  drawGroups();
  drawNames();
  const mdl = modal({
    title: editing ? t('lset.m.edit') : t('lset.m.new'), wide: true,
    onClose: () => { if (pctrl) pctrl.abort(); loadPreview.cancel(); },
    body: h('div', { class: 'grid c2' },
      h('div', { class: 'col gap-3' },
        field(t('lset.m.name'), name, t('lset.m.name_hint')),
        namesEl,
        field(t('lf.f.query'), query, t('lset.m.query_hint')),
        field(t('lset.m.aggregation'), aggSlot),
        attrField,
        field(t('lset.m.group_by'), groupBox, t('lset.m.group_hint')),
        h('label', { class: 'check' }, enabled, t('lset.m.enabled')),
        err),
      h('div', h('div', { class: 'chart-title' }, t('lf.preview.title'), h('span', { class: 'unit' }, t('lf.preview.last_hour'))), preview)),
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => mdl.close() }, t('action.cancel')), saveBtn],
  });
  loadPreview();

  saveBtn.addEventListener('click', async () => {
    err.hidden = true;
    const n = name.value.trim();
    if (!NAME_RE.test(n)) { err.textContent = t('lset.m.bad_name'); err.hidden = false; return; }
    if (m.aggregation === 'distribution' && !/^@?[\w.\-]+$/.test(m.attribute || '')) { err.textContent = t('lset.m.need_attr'); err.hidden = false; return; }
    if (groupInput.value.trim()) addGroup(groupInput.value);
    const body = { name: n, query: m.query.trim(), group_by: m.group_by, aggregation: m.aggregation, attribute: m.aggregation === 'distribution' ? (m.attribute.startsWith('@') ? m.attribute : '@' + m.attribute) : null, enabled: enabled.checked };
    saveBtn.disabled = true;
    try {
      if (editing) await put('/logs/metrics/' + encodeURIComponent(existing.id), body); else await post('/logs/metrics', body);
      mdl.close();
      toast(editing ? t('lset.m.saved') : t('lset.m.created'), 'ok');
      if (onSaved) onSaved();
    } catch (e) { saveBtn.disabled = false; err.textContent = errorMessage(e); err.hidden = false; }
  });
}

async function renderLogMetrics(root, ctx) {
  const { signal } = ctx;
  const writable = canWrite(ctx.me);
  const listEl = h('div');
  const countEl = h('span', { class: 'sub' });
  const newBtn = writable ? h('button', { class: 'btn primary sm', type: 'button', onclick: () => openMetricForm(ctx, null, reload) }, icon('plus', 13), t('lset.m.new')) : null;
  root.appendChild(h('div', { class: 'querybar' }, h('span', { class: 'muted small grow' }, t('lset.m.intro'))));
  root.appendChild(panel(t('lset.sub.metrics'), listEl, { flush: true, sub: writable ? null : t('lf.read_only'), actions: h('span', { class: 'row' }, countEl, newBtn) }));

  async function reload() {
    await load(listEl, async () => asList(await get('/logs/metrics', null, { signal }), 'metrics'), (list) => {
      countEl.textContent = tn('lset.m.count', list.length);
      if (!list.length) return empty(t('lset.m.none_title'), t('lset.m.none_text'), newBtn ? h('button', { class: 'btn primary', type: 'button', onclick: () => openMetricForm(ctx, null, reload) }, icon('plus', 14), t('lset.m.new')) : null);
      return dataTable({
        columns: [
          { key: 'enabled', label: t('col.enabled'), value: (m) => (m.enabled === false ? 0 : 1), render: (m) => {
            const cb = h('input', { type: 'checkbox', checked: m.enabled !== false, disabled: !writable });
            cb.addEventListener('change', async () => {
              try { await put('/logs/metrics/' + encodeURIComponent(m.id), Object.assign({}, m, { enabled: cb.checked })); toast(cb.checked ? t('lf.enabled_done') : t('lf.disabled_done'), 'ok', 2000); } catch (e) { cb.checked = !cb.checked; toast(errorMessage(e), 'error'); }
            });
            return h('label', { class: 'check' }, cb);
          } },
          { key: 'name', label: t('col.name'), render: (m) => h('a', { class: 'mono small', href: metricExplorerLink(ctx, m), title: t('lset.m.open_explorer') }, m.name) },
          { key: 'query', label: t('lf.f.query'), tdCls: 'trunc', render: (m) => h('span', { class: 'mono small', title: m.query || '' }, m.query || '*') },
          { key: 'aggregation', label: t('lset.m.aggregation'), render: (m) => h('span', { class: 'badge muted' }, t('lset.m.agg.' + (m.aggregation === 'distribution' ? 'distribution' : 'count')) + (m.attribute ? ' · ' + m.attribute : '')) },
          { key: 'group_by', label: t('lset.m.group_by'), sort: false, render: (m) => h('span', { class: 'mono small' }, asList(m.group_by).join(', ') || '-') },
          { key: 'updated_at', label: t('col.updated'), align: 'r', value: (m) => Date.parse(m.updated_at) || 0, render: (m) => h('span', { title: fmtDateTime(m.updated_at) }, fmtRel(m.updated_at)) },
          writable ? { key: 'act', label: '', sort: false, render: (m) => h('span', { class: 'row', style: { gap: '2px', justifyContent: 'flex-end' } },
            h('button', { class: 'btn ghost sm icon', type: 'button', title: t('action.edit'), onclick: () => openMetricForm(ctx, m, reload) }, icon('edit', 13)),
            h('button', { class: 'btn ghost sm icon danger', type: 'button', title: t('action.delete'), onclick: async () => {
              if (!(await confirmDialog({ title: t('lset.m.delete_title'), text: t('lset.m.delete_text', { name: m.name }), confirmLabel: t('action.delete'), danger: true }))) return;
              try { await del('/logs/metrics/' + encodeURIComponent(m.id)); toast(t('lset.m.deleted'), 'ok'); reload(); } catch (e) { toast(errorMessage(e), 'error'); }
            } }, icon('trash', 13))) } : null,
        ].filter(Boolean),
        rows: list, sortKey: 'name', sortDir: 'asc',
      });
    });
  }
  await reload();
}

// ── Masquage des données sensibles ────────────────────────────────────────
export const BUILTIN_RULES = ['secrets', 'credit_card', 'iban', 'email', 'phone_fr', 'ipv4_public', 'fr_nir'];

function ruleForm(ctx, existing, onSaved) {
  const editing = !!existing;
  const r = Object.assign({ name: '', pattern: '', replacement: '', scope_query: '', targets: ['message', 'attributes'], enabled: true }, existing || {});
  const name = h('input', { class: 'input', value: r.name || '', maxlength: 120 });
  const pattern = h('input', { class: 'input mono', value: r.pattern || '', spellcheck: 'false', placeholder: '(?i)token=[A-Za-z0-9]{20,}' });
  const repl = h('input', { class: 'input mono', value: r.replacement || '', placeholder: '[masqué:…]' });
  const scope = h('input', { class: 'input mono', value: r.scope_query || '', placeholder: 'service:api' });
  const tMsg = h('input', { type: 'checkbox', checked: asList(r.targets).includes('message') });
  const tAttr = h('input', { type: 'checkbox', checked: asList(r.targets).includes('attributes') });
  const enabled = h('input', { type: 'checkbox', checked: r.enabled !== false });
  const sample = h('textarea', { class: 'input mono', rows: 3, placeholder: t('lset.s.sample_ph') });
  const sampleOut = h('div', { class: 'mono small masked-out' });
  const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
  const saveBtn = h('button', { class: 'btn primary', type: 'button' }, editing ? t('action.save') : t('lset.s.create'));
  const check = () => {
    clear(sampleOut);
    const p = pattern.value;
    if (!p) return;
    let re;
    try { re = new RegExp(p.replace(/^\(\?i\)/, ''), /^\(\?i\)/.test(p) ? 'gi' : 'g'); } catch (e) { sampleOut.appendChild(h('span', { style: { color: 'var(--warn)' } }, t('lset.s.regex_js_warn', { err: e.message }))); return; }
    if (!sample.value) return;
    const replacement = repl.value || '[masqué:' + (name.value.trim() || 'regle') + ']';
    sampleOut.appendChild(maskedText(sample.value.replace(re, replacement)));
  };
  pattern.addEventListener('input', check);
  sample.addEventListener('input', check);
  repl.addEventListener('input', check);
  const mdl = modal({
    title: editing ? t('lset.s.edit') : t('lset.s.new'), wide: true,
    body: h('div', { class: 'grid c2' },
      h('div', { class: 'col gap-3' },
        field(t('lset.s.name'), name),
        field(t('lset.s.pattern'), pattern, t('lset.s.pattern_hint')),
        field(t('lset.s.replacement'), repl, t('lset.s.replacement_hint')),
        field(t('lset.s.scope'), scope, t('lset.s.scope_hint')),
        h('div', { class: 'row' }, h('label', { class: 'check' }, tMsg, t('lset.s.target.message')), h('label', { class: 'check' }, tAttr, t('lset.s.target.attributes'))),
        h('label', { class: 'check' }, enabled, t('lset.s.enabled')),
        err),
      h('div', { class: 'col gap-3' }, field(t('lset.s.try_local'), sample, t('lset.s.try_local_hint')), sampleOut)),
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => mdl.close() }, t('action.cancel')), saveBtn],
  });
  saveBtn.addEventListener('click', async () => {
    err.hidden = true;
    if (!name.value.trim()) { err.textContent = t('form.required', { field: t('lset.s.name') }); err.hidden = false; return; }
    if (!r.builtin && !pattern.value.trim()) { err.textContent = t('form.required', { field: t('lset.s.pattern') }); err.hidden = false; return; }
    const targets = [tMsg.checked ? 'message' : null, tAttr.checked ? 'attributes' : null].filter(Boolean);
    if (!targets.length) { err.textContent = t('lset.s.need_target'); err.hidden = false; return; }
    const body = { name: name.value.trim(), builtin: r.builtin || null, pattern: r.builtin ? null : pattern.value, replacement: repl.value.trim() || '[masqué:' + name.value.trim() + ']', scope_query: scope.value.trim(), targets, enabled: enabled.checked };
    if (isNum(r.position)) body.position = r.position;
    saveBtn.disabled = true;
    try {
      if (editing) await put('/logs/sensitive/' + encodeURIComponent(existing.id), body); else await post('/logs/sensitive', body);
      mdl.close();
      toast(editing ? t('lset.s.saved') : t('lset.s.created'), 'ok');
      if (onSaved) onSaved();
    } catch (e) { saveBtn.disabled = false; err.textContent = errorMessage(e); err.hidden = false; }
  });
  if (r.builtin) { pattern.disabled = true; pattern.placeholder = t('lset.s.builtin_pattern'); }
}

// Texte masqué : les remplacements « [masqué:…] » ressortent.
function maskedText(str) {
  const box = h('div', { class: 'masked' });
  String(str || '').split(/(\[masqu[ée][^\]]*\]|\[masked[^\]]*\])/i).forEach((part, i) => {
    if (!part) return;
    box.appendChild(i % 2 ? h('mark', part) : document.createTextNode(part));
  });
  return box;
}

async function renderSensitive(root, ctx) {
  const { signal } = ctx;
  if (!isAdmin(ctx.me)) {
    root.appendChild(panel(t('lset.sub.sensitive'), empty(t('lset.s.admin_only_title'), t('lset.s.admin_only_text'))));
    return;
  }
  const builtinEl = h('div');
  const customEl = h('div');
  const testerEl = h('div');
  root.appendChild(h('div', { class: 'callout mb-3' }, icon('mask', 16), h('div', { class: 'small' }, t('lset.s.intro'))));
  root.appendChild(h('div', { class: 'split' },
    h('div', { class: 'split-v' },
      panel(t('lset.s.builtins'), builtinEl, { flush: true }),
      panel(t('lset.s.customs'), customEl, { flush: true, actions: h('button', { class: 'btn primary sm', type: 'button', onclick: () => ruleForm(ctx, null, reload) }, icon('plus', 13), t('lset.s.new')) })),
    panel(t('lset.s.tester'), testerEl)));

  let rules = [];
  async function reload() {
    try {
      rules = asList(await get('/logs/sensitive', null, { signal }), 'rules').slice().sort((a, b) => (a.position || 0) - (b.position || 0));
      drawBuiltins();
      drawCustoms();
    } catch (e) {
      if (isAbort(e)) return;
      clear(builtinEl); builtinEl.appendChild(routeErrorState(e, reload));
      clear(customEl);
    }
  }

  async function setEnabled(rule, on) {
    try {
      await put('/logs/sensitive/' + encodeURIComponent(rule.id), Object.assign({}, rule, { enabled: on }));
      toast(on ? t('lset.s.on_done') : t('lset.s.off_done'), 'ok', 2000);
    } catch (e) { toast(errorMessage(e), 'error'); }
    reload();
  }

  function drawBuiltins() {
    clear(builtinEl);
    const rows = BUILTIN_RULES.map((key) => ({ key, rule: rules.find((r) => r.builtin === key) || null }));
    for (const r of rules) if (r.builtin && !BUILTIN_RULES.includes(r.builtin)) rows.push({ key: r.builtin, rule: r });
    builtinEl.appendChild(dataTable({
      columns: [
        { key: 'on', label: t('col.enabled'), value: (x) => (x.rule && x.rule.enabled ? 1 : 0), render: (x) => {
          const cb = h('input', { type: 'checkbox', checked: !!(x.rule && x.rule.enabled) });
          cb.addEventListener('change', async () => {
            cb.disabled = true;
            if (x.rule) return setEnabled(x.rule, cb.checked);
            try { await post('/logs/sensitive', { name: t('lset.s.b.' + x.key), builtin: x.key, enabled: true, replacement: '[masqué:' + x.key + ']', targets: ['message', 'attributes'], scope_query: '' }); toast(t('lset.s.on_done'), 'ok', 2000); } catch (e) { toast(errorMessage(e), 'error'); }
            reload();
          });
          return h('label', { class: 'check' }, cb);
        } },
        { key: 'name', label: t('col.name'), value: (x) => t('lset.s.b.' + x.key), render: (x) => h('div', h('b', { style: { fontWeight: '500' } }, t('lset.s.b.' + x.key)), h('div', { class: 'xsmall muted', style: { whiteSpace: 'normal', maxWidth: '420px' } }, t('lset.s.bd.' + x.key))) },
        { key: 'matched', label: t('lset.s.matched'), align: 'r', value: (x) => (x.rule ? x.rule.matched_total : null), render: (x) => (x.rule ? fmtCompact(x.rule.matched_total) : '-') },
        { key: 'act', label: '', sort: false, render: (x) => (x.rule ? h('button', { class: 'btn ghost sm icon', type: 'button', title: t('lset.s.scope_edit'), onclick: () => ruleForm(ctx, x.rule, reload) }, icon('edit', 13)) : null) },
      ],
      rows, sortKey: 'name', sortDir: 'asc',
    }));
  }

  function drawCustoms() {
    clear(customEl);
    const list = rules.filter((r) => !r.builtin);
    if (!list.length) { customEl.appendChild(empty(null, t('lset.s.no_custom'))); return; }
    customEl.appendChild(dataTable({
      columns: [
        { key: 'enabled', label: t('col.enabled'), value: (r) => (r.enabled ? 1 : 0), render: (r) => { const cb = h('input', { type: 'checkbox', checked: r.enabled !== false }); cb.addEventListener('change', () => setEnabled(r, cb.checked)); return h('label', { class: 'check' }, cb); } },
        { key: 'name', label: t('col.name') },
        { key: 'pattern', label: t('lset.s.pattern'), tdCls: 'trunc', render: (r) => h('span', { class: 'mono small', title: r.pattern || '' }, r.pattern || '-') },
        { key: 'scope_query', label: t('lset.s.scope'), render: (r) => h('span', { class: 'mono small' }, r.scope_query || '*') },
        { key: 'matched_total', label: t('lset.s.matched'), align: 'r', render: (r) => fmtCompact(r.matched_total) },
        { key: 'act', label: '', sort: false, render: (r) => h('span', { class: 'row', style: { gap: '2px', justifyContent: 'flex-end' } },
          h('button', { class: 'btn ghost sm icon', type: 'button', title: t('action.edit'), onclick: () => ruleForm(ctx, r, reload) }, icon('edit', 13)),
          h('button', { class: 'btn ghost sm icon danger', type: 'button', title: t('action.delete'), onclick: async () => {
            if (!(await confirmDialog({ title: t('lset.s.delete_title'), text: t('lset.s.delete_text', { name: r.name || '' }), confirmLabel: t('action.delete'), danger: true }))) return;
            try { await del('/logs/sensitive/' + encodeURIComponent(r.id)); toast(t('lset.s.deleted'), 'ok'); reload(); } catch (e) { toast(errorMessage(e), 'error'); }
          } }, icon('trash', 13))) },
      ],
      rows: list, sortKey: 'name', sortDir: 'asc',
    }));
  }

  // Testeur : le serveur applique les règles actives et rend le texte masqué.
  const input = h('textarea', { class: 'input mono', rows: 6, placeholder: t('lset.s.tester_ph') });
  const out = h('div', { class: 'col', style: { gap: '8px' } });
  const runBtn = h('button', { class: 'btn primary', type: 'button' }, icon('play', 12), t('lset.s.test'));
  runBtn.addEventListener('click', async () => {
    if (!input.value) return;
    runBtn.disabled = true;
    clear(out);
    out.appendChild(h('span', { class: 'spinner' }));
    try {
      const r = await post('/logs/sensitive/test', { text: input.value });
      clear(out);
      const masked = r && (typeof r.text === 'string' ? r.text : typeof r.masked === 'string' ? r.masked : typeof r.result === 'string' ? r.result : '');
      const touched = asList(r && (r.rules || r.matched || r.hits));
      out.appendChild(h('div', { class: 'small text-2' }, t('lset.s.result')));
      out.appendChild(h('div', { class: 'code', style: { paddingRight: '12px' } }, maskedText(masked)));
      out.appendChild(h('div', { class: 'small' }, touched.length ? t('lset.s.touched', { list: touched.map((x) => (typeof x === 'string' ? x : x.name || x.builtin || x.rule || '')).filter(Boolean).join(', ') }) : t('lset.s.untouched')));
    } catch (e) {
      clear(out);
      out.appendChild(isMissingRoute(e) ? routeErrorState(e) : h('div', { class: 'callout crit' }, icon('warn', 16), h('span', { class: 'small' }, errorMessage(e))));
    } finally { runBtn.disabled = false; }
  });
  testerEl.appendChild(h('div', { class: 'col gap-3' }, h('div', { class: 'small muted' }, t('lset.s.tester_hint')), input, h('div', runBtn), out,
    h('div', { class: 'xsmall muted' }, t('lset.s.examples')),
    select([{ value: '', label: t('lset.s.example_pick') }].concat(['jwt', 'card', 'mail', 'phone', 'iban'].map((k) => ({ value: k, label: t('lset.s.ex.' + k) }))), '', (v) => { if (v) { input.value = EXAMPLES[v]; } })));

  await reload();
}

// Exemples factices (aucune donnée réelle) pour essayer le masquage.
const EXAMPLES = {
  jwt: 'Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dGVzdC1zaWduYXR1cmU password=Tr0ub4dor',  // exemple fictif, gitleaks:allow
  card: 'paiement refusé carte 4111 1111 1111 1111 exp 12/29',
  mail: 'envoi du rapport à jeanne.martin@example.fr',
  phone: 'rappel demandé au 06 12 34 56 78 ou +33 6 12 34 56 78',
  iban: 'virement vers FR76 3000 6000 0112 3456 7890 189',
};
