// Tests synthétiques : liste (disponibilité, latence), formulaire, résultats, exécution immédiate.
import { h, clear, icon, cssVar } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, post, put, del, asList, pool, isAbort } from '../api.js';
import { fmtDuration, fmtPct, fmtDateTime, fmtRel, toMs, isNum, fmtInt } from '../format.js';
import { section, empty, panel, field, select, toast, confirmDialog, errorMessage, errorState, loading, kv, searchInput } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { timeChart, alignPoints } from '../components/chart.js';
import { jsonTree } from '../components/jsontree.js';
import { assertionsEditor, variablesEditor, stepsEditor, newStep, locationsPicker, locationLabel, stepResults, locationTable } from './synth-steps.js';

const KINDS = ['http', 'multistep', 'tcp', 'ssl', 'dns', 'icmp', 'smtp', 'imap', 'mail_roundtrip', 'mail_dns'];
// Types que seul le serveur exécute (§ 32) : pas d'emplacement d'agent.
const SERVER_ONLY = ['mail_roundtrip', 'mail_dns'];
const TLS_KINDS = ['http', 'ssl', 'smtp', 'imap'];
const METHODS = ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS'];
const INTERVALS = [30, 60, 120, 300, 600, 900, 1800, 3600, 21600, 43200, 86400];

function results(resp) { return asList(resp, 'results').slice().sort((a, b) => (toMs(a.ts) || 0) - (toMs(b.ts) || 0)); }

function stats(list) {
  const n = list.length;
  if (!n) return { uptime: null, latency: null, last: null, n: 0 };
  const ok = list.filter((r) => r.ok).length;
  const lat = list.map((r) => r.latency_ms).filter(isNum);
  return { uptime: ok / n * 100, latency: lat.length ? lat.reduce((a, b) => a + b, 0) / lat.length : null, last: list[n - 1], n };
}

function strip(list, cells = 40) {
  const el = h('div', { class: 'uptime-strip', title: t('synth.strip_title') });
  if (!list.length) { for (let i = 0; i < cells; i++) el.appendChild(h('i')); return el; }
  const per = Math.max(1, Math.ceil(list.length / cells));
  for (let i = 0; i < list.length; i += per) {
    const chunk = list.slice(i, i + per);
    const bad = chunk.some((r) => !r.ok);
    el.appendChild(h('i', { class: bad ? 'ko' : 'ok', title: fmtDateTime(chunk[0].ts) + (bad ? ' · ' + t('synth.failure') : '') }));
  }
  return el;
}

function uptimeColor(u) { return !isNum(u) ? null : u >= 99.9 ? 'var(--ok)' : u >= 99 ? 'var(--warn)' : 'var(--crit)'; }

export async function render(root, ctx) {
  if (ctx.route.view === 'form') return renderForm(root, ctx);
  if (ctx.route.view === 'detail') return renderDetail(root, ctx);
  return renderList(root, ctx);
}

async function renderList(root, ctx) {
  const { signal, href, range, params } = ctx;
  ctx.setCrumbs([{ label: t('nav.synthetics') }]);
  let q = params.get('q') || '';
  const body = h('div');
  const countEl = h('span', { class: 'sub' });
  const search = searchInput({ placeholder: t('synth.search_ph'), value: q, onInput: (v) => { q = v; ctx.setParams({ q: v || null }, { silent: true }); draw(); } });
  search.style.flex = '1 1 300px';
  root.appendChild(h('div', { class: 'querybar' }, search, h('a', { class: 'btn primary', href: href('/synthetics/new') }, icon('plus', 14), t('synth.new'))));
  root.appendChild(panel(t('nav.synthetics'), body, { flush: true, actions: countEl }));
  let tests = [];
  const res = new Map();

  function draw() {
    const list = tests.filter((x) => !q || [x.name, x.target, x.kind].join(' ').toLowerCase().includes(q.toLowerCase()));
    countEl.textContent = tn('synth.count', list.length);
    clear(body);
    if (!tests.length) {
      body.appendChild(empty(t('synth.none_title'), t('synth.none_text'), h('a', { class: 'btn primary', href: href('/synthetics/new') }, icon('plus', 14), t('synth.new'))));
      return;
    }
    body.appendChild(dataTable({
      columns: [
        { key: 'last', label: t('col.status'), value: (x) => { const s2 = res.get(x.id); return s2 && s2.last ? (s2.last.ok ? 1 : 0) : 2; },
          render: (x) => { const s2 = res.get(x.id); if (!s2) return h('span', { class: 'spinner' }); if (s2 instanceof Error) return h('span', { class: 'badge muted' }, '?'); return s2.last ? h('span', { class: ['badge', s2.last.ok ? 'ok' : 'alert'] }, s2.last.ok ? t('synth.up') : t('synth.down')) : h('span', { class: 'badge muted' }, t('synth.no_result')); } },
        { key: 'name', label: t('col.name'), render: (x) => h('a', { href: href('/synthetics/' + x.id) }, x.name || '#' + x.id) },
        { key: 'kind', label: t('col.type'), render: (x) => h('span', { class: 'badge muted' }, String(x.kind || '').toUpperCase()) },
        { key: 'target', label: t('col.target'), tdCls: 'trunc', render: (x) => h('span', { class: 'mono small', title: x.target || '' }, x.target || '-') },
        { key: 'strip', label: t('synth.availability'), sort: false, render: (x) => { const s2 = res.get(x.id); return s2 && !(s2 instanceof Error) ? strip(s2.list, 30) : strip([], 30); } },
        { key: 'uptime', label: t('col.uptime'), align: 'r', value: (x) => { const s2 = res.get(x.id); return s2 && !(s2 instanceof Error) ? s2.uptime : null; },
          render: (x) => { const s2 = res.get(x.id); const u = s2 && !(s2 instanceof Error) ? s2.uptime : (isNum(x.uptime_pct) ? x.uptime_pct : null); return h('span', { style: { color: uptimeColor(u) } }, fmtPct(u, 2)); } },
        { key: 'latency', label: t('col.latency'), align: 'r', value: (x) => { const s2 = res.get(x.id); return s2 && !(s2 instanceof Error) ? s2.latency : null; },
          render: (x) => { const s2 = res.get(x.id); return fmtDuration(s2 && !(s2 instanceof Error) ? s2.latency : x.avg_latency_ms); } },
        { key: 'interval_s', label: t('col.interval'), align: 'r', render: (x) => fmtDuration((x.interval_s || 60) * 1000, true) },
        { key: 'enabled', label: t('col.enabled'), value: (x) => (x.enabled === false ? 0 : 1), render: (x) => (x.enabled === false ? h('span', { class: 'badge muted' }, t('synth.paused')) : h('span', { class: 'muted small' }, t('synth.active'))) },
        { key: 'last_run', label: t('col.last_run'), align: 'r', value: (x) => toMs(x.last_run), render: (x) => fmtRel(x.last_run) },
      ],
      rows: list, sortKey: 'name', sortDir: 'asc',
      onRowClick: (x) => ctx.navigate('/synthetics/' + x.id),
    }));
  }

  await section(body, async () => asList(await get('/synthetics', null, { signal }), 'synthetics', 'tests'), (list) => { tests = list; draw(); return null; });
  // Disponibilité et latence calculées depuis les résultats de la période, 4 requêtes à la fois.
  await pool(tests, 4, async (x) => {
    try {
      const list = results(await get('/synthetics/' + x.id + '/results', { from: range.from, to: range.to }, { signal }));
      res.set(x.id, Object.assign(stats(list), { list }));
    } catch (e) {
      if (isAbort(e)) return;
      res.set(x.id, e instanceof Error ? e : new Error('x'));
    }
  });
  if (ctx.isCurrent()) draw();
}

function headersToText(hd) { return Object.entries(hd || {}).map(([k, v]) => k + ': ' + v).join('\n'); }
function textToHeaders(txt) {
  const out = {};
  for (const line of String(txt || '').split('\n')) {
    const i = line.indexOf(':');
    if (i > 0) out[line.slice(0, i).trim()] = line.slice(i + 1).trim();
  }
  return out;
}

async function renderForm(root, ctx) {
  const { args, signal, href } = ctx;
  const editing = !!args.id;
  ctx.setCrumbs([{ label: t('nav.synthetics'), href: href('/synthetics') }, { label: editing ? t('synth.edit') : t('synth.new') }]);
  let test = { name: '', kind: 'http', target: 'https://', interval_s: 60, enabled: true, locations: ['server'], steps: [], config: { method: 'GET', headers: {}, body: '', expect_status: 200, expect_text: '', timeout_s: 10, ssl_days_warn: 21, follow_redirects: true, assertions: [], variables: {} } };
  if (editing) {
    root.appendChild(loading());
    try {
      const r = await get('/synthetics/' + encodeURIComponent(args.id), null, { signal });
      const x = r && r.test ? r.test : r;
      test = Object.assign(test, x, { config: Object.assign({}, test.config, (x && x.config) || {}) });
      test.locations = Array.isArray(test.locations) && test.locations.length ? test.locations.slice() : ['server'];
      test.steps = Array.isArray(test.steps) ? test.steps : [];
      test.config.assertions = Array.isArray(test.config.assertions) ? test.config.assertions : [];
      test.config.variables = test.config.variables && typeof test.config.variables === 'object' ? test.config.variables : {};
    } catch (e) {
      if (isAbort(e)) return;
      clear(root); root.appendChild(errorState(e, () => ctx.refresh())); return;
    }
    clear(root);
  }
  const c = test.config;
  const err = h('div', { class: 'callout crit mt-3', hidden: true });
  const fields = h('div', { class: 'form-grid' });
  const extraEl = h('div');
  const saveBtn = h('button', { class: 'btn primary', type: 'button' }, editing ? t('action.save') : t('synth.create'));
  root.appendChild(h('div', { class: 'page-head' }, h('h1', editing ? t('synth.edit') : t('synth.new'))));
  root.appendChild(h('section', { class: 'panel', 'data-no-autorefresh': '1' }, h('div', { class: 'panel-body' }, fields, extraEl, err,
    h('div', { class: 'row mt-4' }, h('span', { class: 'spacer' }), h('a', { class: 'btn', href: editing ? href('/synthetics/' + args.id) : href('/synthetics') }, t('action.cancel')), saveBtn))));

  function draw() {
    clear(fields);
    const name = h('input', { class: 'input', value: test.name || '' });
    name.addEventListener('input', () => { test.name = name.value; });
    const target = h('input', { class: 'input mono', value: test.target || '', placeholder: t('synth.target_ph.' + test.kind) });
    target.addEventListener('input', () => { test.target = target.value.trim(); });
    const enabled = h('input', { type: 'checkbox', checked: test.enabled !== false });
    enabled.addEventListener('change', () => { test.enabled = enabled.checked; });
    const numInput = (key, min, max) => {
      const inp = h('input', { class: 'input', type: 'number', min, max, value: c[key] ?? '' });
      inp.addEventListener('input', () => { c[key] = inp.value === '' ? null : Number(inp.value); });
      return inp;
    };
    const textInput = (key, ph, type) => {
      const inp = h('input', { class: 'input mono', type: type || 'text', value: c[key] ?? '', placeholder: ph || '', autocomplete: type === 'password' ? 'new-password' : 'off' });
      inp.addEventListener('input', () => { c[key] = type === 'password' ? inp.value : inp.value.trim(); });
      return inp;
    };
    fields.appendChild(h('div', { class: 'full' }, field(t('synth.f.name'), name)));
    fields.appendChild(field(t('synth.f.kind'), select(KINDS.map((k) => ({ value: k, label: t('synth.kind.' + k) })), test.kind, (v) => { test.kind = v; draw(); })));
    if (test.kind !== 'multistep') fields.appendChild(h('div', { style: { gridColumn: 'span 2' } }, field(t('synth.f.target'), target, t('synth.target_hint.' + test.kind))));
    fields.appendChild(field(t('synth.f.interval'), select(INTERVALS.map((s2) => ({ value: s2, label: t('monitors.every', { d: fmtDuration(s2 * 1000, true) }) })), test.interval_s || 60, (v) => { test.interval_s = Number(v); })));
    fields.appendChild(field(t('synth.f.timeout'), numInput('timeout_s', 1, 120)));
    if (test.kind === 'http') {
      fields.appendChild(field(t('synth.f.method'), select(METHODS.map((mm) => ({ value: mm, label: mm })), c.method || 'GET', (v) => { c.method = v; draw(); })));
      fields.appendChild(field(t('synth.f.expect_status'), numInput('expect_status', 100, 599)));
      const et = h('input', { class: 'input', value: c.expect_text || '' });
      et.addEventListener('input', () => { c.expect_text = et.value; });
      fields.appendChild(field(t('synth.f.expect_text'), et, t('synth.f.expect_text_hint')));
      const fr = h('input', { type: 'checkbox', checked: c.follow_redirects !== false });
      fr.addEventListener('change', () => { c.follow_redirects = fr.checked; });
      fields.appendChild(h('div', { class: 'col', style: { justifyContent: 'flex-end' } }, h('label', { class: 'check' }, fr, t('synth.f.follow'))));
      const hd = h('textarea', { class: 'input mono', rows: 3, placeholder: 'Authorization: Bearer …' });
      hd.value = headersToText(c.headers);
      hd.addEventListener('input', () => { c.headers = textToHeaders(hd.value); });
      fields.appendChild(h('div', { style: { gridColumn: 'span 2' } }, field(t('synth.f.headers'), hd, t('synth.f.headers_hint'))));
      if (!['GET', 'HEAD', 'OPTIONS'].includes(c.method)) {
        const bd = h('textarea', { class: 'input mono', rows: 3 });
        bd.value = c.body || '';
        bd.addEventListener('input', () => { c.body = bd.value; });
        fields.appendChild(h('div', { style: { gridColumn: 'span 2' } }, field(t('synth.f.body'), bd)));
      }
    }
    if (test.kind === 'ssl') {
      fields.appendChild(field(t('synth.f.starttls'), select([{ value: '', label: t('synth.starttls.none') }, { value: 'smtp', label: 'SMTP' }, { value: 'imap', label: 'IMAP' }, { value: 'pop3', label: 'POP3' }],
        c.starttls || '', (v) => { c.starttls = v || null; }), t('synth.f.starttls_hint')));
    }
    if (test.kind === 'smtp' || test.kind === 'imap') {
      fields.appendChild(field(t('synth.f.tls'), select(['auto', 'starttls', 'implicit', 'off'].map((m) => ({ value: m, label: t('synth.tls.' + m) })), c.tls || 'auto', (v) => { c.tls = v; })));
      fields.appendChild(field(t('synth.f.tls_server_name'), textInput('tls_server_name', 'mx.example.com'), t('synth.f.tls_server_name_hint')));
      fields.appendChild(field(t('synth.f.expect_banner'), textInput('expect_banner', 'ESMTP')));
    }
    if (test.kind === 'mail_roundtrip') {
      fields.appendChild(field(t('synth.f.smtp'), textInput('smtp', 'smtp.example.com:587'), t('synth.f.smtp_hint')));
      fields.appendChild(field(t('synth.f.imap'), textInput('imap', 'imap.example.com:993')));
      fields.appendChild(field(t('synth.f.username'), textInput('username', ''), t('synth.f.username_hint')));
      fields.appendChild(field(t('synth.f.password'), textInput('password', '', 'password'), t('synth.f.password_hint')));
      fields.appendChild(field(t('synth.f.to'), textInput('to', 'check-auth@verifier.port25.com'), t('synth.f.to_hint')));
      fields.appendChild(field(t('synth.f.reply_from'), textInput('reply_from', 'verifier.port25.com'), t('synth.f.reply_from_hint')));
      fields.appendChild(field(t('synth.f.wait_s'), numInput('wait_s', 10, 900)));
    }
    if (test.kind === 'mail_dns') {
      const sel = h('input', { class: 'input mono', value: (c.dkim_selectors || []).join(', '), placeholder: 'selector1, s1' });
      sel.addEventListener('input', () => { c.dkim_selectors = sel.value.split(/[\s,;]+/).map((x) => x.trim()).filter(Boolean); });
      fields.appendChild(field(t('synth.f.dkim'), sel, t('synth.f.dkim_hint')));
      fields.appendChild(field(t('synth.f.dmarc_policy'), select([{ value: '', label: t('synth.any') }, ...['reject', 'quarantine', 'none'].map((v) => ({ value: v, label: 'p=' + v }))], c.dmarc_policy || '', (v) => { c.dmarc_policy = v || null; })));
      fields.appendChild(field(t('synth.f.spf_all'), select([{ value: '', label: t('synth.any') }, { value: '-all', label: '-all' }, { value: '~all', label: '~all' }], c.spf_all || '', (v) => { c.spf_all = v || null; })));
      const bl = h('input', { type: 'checkbox', checked: !!c.dnsbl });
      bl.addEventListener('change', () => { c.dnsbl = bl.checked; });
      fields.appendChild(h('div', { class: 'col', style: { justifyContent: 'flex-end' } }, h('label', { class: 'check', title: t('synth.f.dnsbl_hint') }, bl, t('synth.f.dnsbl'))));
    }
    if (TLS_KINDS.includes(test.kind)) fields.appendChild(field(t('synth.f.ssl_days'), numInput('ssl_days_warn', 1, 365), t('synth.f.ssl_days_hint')));
    fields.appendChild(h('div', { class: 'col', style: { justifyContent: 'flex-end' } }, h('label', { class: 'check' }, enabled, t('synth.f.enabled'))));
    // § 29 : étapes, variables, assertions et emplacements.
    clear(extraEl);
    if (test.kind === 'multistep') {
      if (!test.steps.length) test.steps.push(newStep(0));
      const stepsEl = stepsEditor(test.steps, c.variables);
      extraEl.appendChild(h('div', { class: 'form-section' }, h('h3', icon('key', 15), t('synth.vars.title')),
        h('div', { class: 'small muted mb-2' }, t('synth.vars.hint')), variablesEditor(c.variables, () => { if (stepsEl.redraw) stepsEl.redraw(); })));
      extraEl.appendChild(h('div', { class: 'form-section' }, h('h3', icon('steps', 15), t('synth.step.title')), h('div', { class: 'small muted mb-2' }, t('synth.step.hint')), stepsEl));
    } else if (test.kind === 'http' || test.kind === 'ssl') {
      extraEl.appendChild(h('div', { class: 'form-section' }, h('h3', t('synth.assert.title')), h('div', { class: 'small muted mb-2' }, t('synth.assert.hint_' + test.kind)), assertionsEditor(c.assertions, { hint: t('synth.assert.none_hint') })));
    }
    if (SERVER_ONLY.includes(test.kind)) {
      test.locations = ['server'];
      extraEl.appendChild(h('div', { class: 'form-section' }, h('h3', icon('globe', 15), t('synth.loc.title')), h('div', { class: 'small muted' }, t('synth.loc.server_only'))));
    } else {
      extraEl.appendChild(h('div', { class: 'form-section' }, h('h3', icon('globe', 15), t('synth.loc.title')), h('div', { class: 'small muted mb-2' }, t('synth.loc.hint')), locationsPicker(test.locations, ctx)));
    }
  }
  draw();

  saveBtn.addEventListener('click', async () => {
    err.hidden = true;
    const errs = [];
    if (!String(test.name || '').trim()) errs.push(t('form.required', { field: t('synth.f.name') }));
    if (test.kind === 'multistep') {
      test.steps.forEach((st, i) => { if (!/^https?:\/\/./i.test(String(st.url || '').replace(/\{\{[^}]*\}\}/g, 'x'))) errs.push(t('synth.step.bad_url', { n: i + 1 })); });
      if (!test.steps.length) errs.push(t('synth.step.none'));
      test.target = (test.steps[0] && test.steps[0].url) || test.target;
    } else if (!String(test.target || '').trim() || test.target === 'https://') errs.push(t('form.required', { field: t('synth.f.target') }));
    if (test.kind === 'http' && test.target && !/^https?:\/\//i.test(test.target)) errs.push(t('synth.err.url'));
    if ((test.kind === 'tcp') && test.target && !/:\d+$/.test(test.target)) errs.push(t('synth.err.port'));
    if (errs.length) { clear(err); err.appendChild(icon('warn', 16)); err.appendChild(h('div', errs.map((e) => h('div', e)))); err.hidden = false; return; }
    const cfg = { timeout_s: c.timeout_s || 10 };
    if (test.kind === 'http') Object.assign(cfg, { method: c.method || 'GET', headers: c.headers || {}, body: c.body || '', expect_status: c.expect_status || 200, expect_text: c.expect_text || '', follow_redirects: c.follow_redirects !== false });
    if (TLS_KINDS.includes(test.kind)) cfg.ssl_days_warn = c.ssl_days_warn || 21;
    if (test.kind === 'http' || test.kind === 'ssl') cfg.assertions = (c.assertions || []).filter((a) => a && a.type);
    if (test.kind === 'ssl' && c.starttls) cfg.starttls = c.starttls;
    const opt = (o) => Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined && v !== null && v !== ''));
    if (test.kind === 'smtp' || test.kind === 'imap') Object.assign(cfg, opt({ tls: c.tls || 'auto', tls_server_name: c.tls_server_name, expect_banner: c.expect_banner }));
    if (test.kind === 'mail_roundtrip') {
      Object.assign(cfg, opt({ timeout_s: c.timeout_s || 20, smtp: c.smtp, imap: c.imap, smtp_tls: c.smtp_tls || 'auto', imap_tls: c.imap_tls || 'auto', username: c.username,
        password: c.password, to: c.to, reply_from: c.reply_from, wait_s: c.wait_s || 180 }));
      cfg.expect_auth_pass = c.expect_auth_pass !== false;
    }
    if (test.kind === 'mail_dns') Object.assign(cfg, opt({ dkim_selectors: c.dkim_selectors || [], dmarc_policy: c.dmarc_policy, spf_all: c.spf_all }), { dnsbl: !!c.dnsbl });
    if (test.kind === 'multistep') cfg.variables = c.variables || {};
    const body = { name: test.name.trim(), kind: test.kind, target: String(test.target || '').trim(), interval_s: Math.max(30, test.interval_s || 60), enabled: test.enabled !== false, config: cfg, locations: !SERVER_ONLY.includes(test.kind) && test.locations && test.locations.length ? test.locations : ['server'] };
    if (test.kind === 'multistep') {
      body.steps = test.steps.map((st, i) => ({
        name: st.name || t('synth.step.default_name', { n: i + 1 }), method: st.method || 'GET', url: String(st.url || '').trim(), headers: st.headers || {}, body: st.body || '',
        timeout_s: st.timeout_s || 10, follow_redirects: st.follow_redirects !== false,
        assertions: (st.assertions || []).filter((a) => a && a.type), extract: (st.extract || []).filter((x) => x && x.name && x.path),
      }));
    }
    saveBtn.disabled = true;
    try {
      const r = editing ? await put('/synthetics/' + args.id, body) : await post('/synthetics', body);
      toast(editing ? t('synth.saved') : t('synth.created'), 'ok');
      const id = editing ? args.id : r && (r.id || (r.test && r.test.id));
      ctx.navigate(id ? '/synthetics/' + id : '/synthetics');
    } catch (e) {
      saveBtn.disabled = false;
      clear(err); err.appendChild(icon('warn', 16)); err.appendChild(h('div', errorMessage(e))); err.hidden = false;
    }
  });
}

async function renderDetail(root, ctx) {
  const { args, signal, href, range } = ctx;
  ctx.setCrumbs([{ label: t('nav.synthetics'), href: href('/synthetics') }, { label: '#' + args.id }]);
  root.appendChild(loading());
  let test;
  try {
    const r = await get('/synthetics/' + encodeURIComponent(args.id), null, { signal });
    test = r && r.test ? r.test : r;
  } catch (e) {
    if (isAbort(e)) return;
    clear(root); root.appendChild(errorState(e, () => ctx.refresh())); return;
  }
  clear(root);
  ctx.setCrumbs([{ label: t('nav.synthetics'), href: href('/synthetics') }, { label: test.name || '#' + test.id }]);
  const runBtn = h('button', { class: 'btn sm primary', type: 'button' }, icon('play', 12), t('synth.run_now'));
  root.appendChild(h('div', { class: 'page-head' },
    h('h1', test.name || '#' + test.id), h('span', { class: 'badge muted' }, String(test.kind || '').toUpperCase()),
    test.enabled === false ? h('span', { class: 'badge muted' }, t('synth.paused')) : null,
    h('span', { class: 'desc mono' }, test.kind === 'multistep' ? tn('synth.step.count', (test.steps || []).length) : test.target || ''),
    (test.locations || []).some((l) => l !== 'server') ? h('span', { class: 'tags' }, (test.locations || []).map((l) => h('span', { class: 'tag', title: l }, locationLabel(l)))) : null,
    h('span', { class: 'spacer' }),
    runBtn,
    h('a', { class: 'btn sm', href: href('/monitors/new', { type: 'synthetic', test_id: test.id }) }, icon('monitor', 13), t('synth.create_monitor')),
    h('a', { class: 'btn sm', href: href('/synthetics/' + test.id + '/edit') }, icon('edit', 13), t('action.edit')),
    h('button', { class: 'btn sm danger', type: 'button', onclick: async () => {
      if (!(await confirmDialog({ title: t('synth.delete_title'), text: t('synth.delete_text', { name: test.name || '' }), confirmLabel: t('action.delete'), danger: true }))) return;
      try { await del('/synthetics/' + test.id); toast(t('synth.deleted'), 'ok'); ctx.navigate('/synthetics'); } catch (e) { toast(errorMessage(e), 'error'); }
    } }, icon('trash', 13), t('action.delete'))));

  runBtn.addEventListener('click', async () => {
    runBtn.disabled = true;
    try {
      const r = await post('/synthetics/' + test.id + '/run', {});
      const ok = r && typeof r === 'object' && 'ok' in r ? r.ok : null;
      toast(ok === null ? t('synth.run_sent') : ok ? t('synth.run_ok', { ms: fmtDuration(r.latency_ms) }) : t('synth.run_ko', { err: r.error || '' }), ok === false ? 'error' : 'ok', 6000);
      setTimeout(() => ctx.refresh(), 1500);
    } catch (e) { toast(errorMessage(e), 'error'); } finally { runBtn.disabled = false; }
  });

  const tilesEl = h('div', { class: 'tiles mb-3' });
  const chartEl = h('div', { class: 'panel-body' });
  const stripEl = h('div', { class: 'panel-body' });
  const tableEl = h('div');
  const locEl = h('div');
  const stepsEl = h('div');
  root.appendChild(tilesEl);
  root.appendChild(h('div', { class: 'grid c2 mb-3' }, panel(t('synth.by_location'), locEl, { flush: true }), panel(t('synth.last_steps'), stepsEl)));
  root.appendChild(h('section', { class: 'panel mb-3' }, h('div', { class: 'panel-head' }, h('h3', t('synth.availability'))), stripEl));
  root.appendChild(h('section', { class: 'panel mb-3' }, h('div', { class: 'panel-head' }, h('h3', t('synth.latency_chart'))), chartEl));
  root.appendChild(panel(t('synth.results'), tableEl, { flush: true }));

  // Noms des hôtes des emplacements privés (agent:<host_id>).
  const hostNames = new Map();
  if ((test.locations || []).some((l) => String(l).startsWith('agent:'))) {
    try { asList(await get('/hosts', null, { signal }), 'hosts').forEach((x) => hostNames.set(String(x.id), x.hostname)); } catch (e) { if (isAbort(e)) return; }
  }
  await section(tableEl, async () => results(await get('/synthetics/' + test.id + '/results', { from: range.from, to: range.to }, { signal })), (list) => {
    const st = stats(list);
    const last = st.last;
    const tile = (label, value, sub, color) => h('div', { class: 'tile' }, h('div', { class: 't-label' }, label), h('div', { class: 't-value num', style: { fontSize: 'var(--fs-xl)', color } }, value), sub ? h('div', { class: 't-sub' }, sub) : null);
    clear(tilesEl);
    tilesEl.appendChild(tile(t('col.status'), last ? (last.ok ? t('synth.up') : t('synth.down')) : t('synth.no_result'), last ? fmtRel(last.ts) : null, last ? (last.ok ? 'var(--ok)' : 'var(--crit)') : null));
    tilesEl.appendChild(tile(t('col.uptime'), fmtPct(st.uptime, 2), tn('synth.runs', st.n), uptimeColor(st.uptime)));
    tilesEl.appendChild(tile(t('synth.avg_latency'), fmtDuration(st.latency)));
    tilesEl.appendChild(tile(t('synth.failures'), fmtInt(list.filter((r) => !r.ok).length)));
    const certDays = last && last.details ? (last.details.cert_days ?? last.details.ssl_days ?? last.details.days_left) : null;
    if (isNum(certDays)) tilesEl.appendChild(tile(t('synth.cert_days'), fmtInt(certDays), null, certDays < ((test.config && test.config.ssl_days_warn) || 21) ? 'var(--warn)' : null));
    clear(stripEl);
    stripEl.appendChild(list.length ? strip(list, 120) : h('div', { class: 'muted small' }, t('synth.no_result_range')));
    clear(locEl);
    locEl.appendChild(locationTable(list, hostNames));
    clear(stepsEl);
    const lastSteps = last ? stepResults(last.details) : null;
    stepsEl.appendChild(lastSteps || h('div', { class: 'muted small' }, test.kind === 'multistep' ? t('synth.no_steps_yet') : t('synth.single_step')));
    clear(chartEl);
    const ok = list.filter((r) => r.ok && isNum(r.latency_ms)).map((r) => [toMs(r.ts), r.latency_ms]);
    const ko = list.filter((r) => !r.ok).map((r) => [toMs(r.ts), isNum(r.latency_ms) ? r.latency_ms : 0]);
    const { xs, ys } = alignPoints([ok, ko]);
    chartEl.appendChild(timeChart({
      xs, unit: 'millisecond', height: 180, xRange: [range.from, range.to], onRange: ctx.onRange,
      lines: [{ label: t('synth.latency'), values: ys[0], color: cssVar('--s1') }, { label: t('synth.failure'), values: ys[1], color: cssVar('--crit'), display: 'bars' }],
    }));
    const sorted = list.slice().reverse();
    return dataTable({
      columns: [
        { key: 'ts', label: t('col.date'), value: (r) => toMs(r.ts), render: (r) => h('span', { class: 'num' }, fmtDateTime(r.ts)) },
        { key: 'ok', label: t('col.status'), value: (r) => (r.ok ? 1 : 0), render: (r) => h('span', { class: ['badge', r.ok ? 'ok' : 'alert'] }, r.ok ? t('synth.up') : t('synth.down')) },
        { key: 'status_code', label: t('col.http_code'), align: 'r' },
        { key: 'latency_ms', label: t('col.latency'), align: 'r', render: (r) => fmtDuration(r.latency_ms) },
        { key: 'location', label: t('col.location'), value: (r) => r.location || '', render: (r) => (r.location ? locationLabel(r.location, hostNames) : '-') },
        { key: 'error', label: t('col.error'), tdCls: 'trunc', render: (r) => h('span', { title: r.error || '', style: { color: r.error ? 'var(--crit)' : null } }, r.error || '-') },
      ],
      rows: sorted, sortKey: 'ts', sortDir: 'desc', maxRows: 500,
      onRowClick: (r) => showDetails(r),
      emptyText: t('synth.no_result_range'),
    });
  });

  const detailsEl = h('div', { class: 'mt-3' });
  root.appendChild(detailsEl);
  function showDetails(r) {
    clear(detailsEl);
    detailsEl.appendChild(panel(t('synth.result_of', { d: fmtDateTime(r.ts) }), h('div', { class: 'grid c2' },
      kv([[t('col.status'), r.ok ? t('synth.up') : t('synth.down')], [t('col.http_code'), r.status_code ?? null], [t('col.latency'), fmtDuration(r.latency_ms)], [t('col.location'), r.location || null], [t('col.error'), r.error || null]]),
      h('div', { class: 'col gap-3' }, stepResults(r.details), r.details && Object.keys(r.details).length ? jsonTree(r.details) : h('div', { class: 'muted small' }, t('synth.no_details'))))));
    detailsEl.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  }
}
