// Synthétiques multi-étapes et emplacements privés (§ 29) : éditeur d'étapes (ordre, assertions,
// extractions, variables), choix des emplacements, résultats par étape et par emplacement.
import { h, clear, icon } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, asList } from '../api.js';
import { fmtDuration, fmtPct, fmtRel, fmtDateTime, isNum, toMs } from '../format.js';
import { field, select } from '../components/ui.js';
import { dataTable } from '../components/table.js';

export const ASSERT_TYPES = ['status', 'header', 'body_contains', 'json_path', 'latency_ms'];
export const ASSERT_OPS = ['eq', 'ne', 'lt', 'gt', 'contains', 'matches'];
const EXTRACT_FROM = ['json_path', 'header', 'regex'];
const METHODS = ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS'];

function headersToText(hd) { return Object.entries(hd || {}).map(([k, v]) => k + ': ' + v).join('\n'); }
function textToHeaders(txt) {
  const out = {};
  for (const line of String(txt || '').split('\n')) { const i = line.indexOf(':'); if (i > 0) out[line.slice(0, i).trim()] = line.slice(i + 1).trim(); }
  return out;
}

function defaultOp(type) { return type === 'latency_ms' ? 'lt' : type === 'body_contains' ? 'contains' : 'eq'; }

// Assertions : liste modifiée en place. opts.compact : sans titre.
export function assertionsEditor(list, opts = {}) {
  const box = h('div', { class: 'col', style: { gap: '6px' } });
  function draw() {
    clear(box);
    list.forEach((a, i) => {
      const needsPath = a.type === 'header' || a.type === 'json_path';
      const path = h('input', { class: 'input mono sm', value: a.path || '', placeholder: a.type === 'header' ? 'content-type' : '$.data.id', style: { width: '150px' }, hidden: !needsPath });
      path.addEventListener('input', () => { a.path = path.value.trim(); });
      const value = h('input', { class: 'input mono sm', value: a.value === undefined || a.value === null ? '' : String(a.value), placeholder: a.type === 'status' ? '200' : a.type === 'latency_ms' ? '800' : '', style: { width: '160px' } });
      value.addEventListener('input', () => { a.value = ['status', 'latency_ms'].includes(a.type) && /^-?\d+(\.\d+)?$/.test(value.value.trim()) ? Number(value.value.trim()) : value.value; });
      box.appendChild(h('div', { class: 'row wrap assert-row', style: { gap: '6px' } },
        select(ASSERT_TYPES.map((ty) => ({ value: ty, label: t('synth.assert.type.' + ty) })), a.type, (v) => { a.type = v; a.op = defaultOp(v); if (v !== 'header' && v !== 'json_path') delete a.path; draw(); }, { class: 'input sm', style: { width: '150px' } }),
        path,
        select(ASSERT_OPS.map((op) => ({ value: op, label: t('synth.assert.op.' + op) })), a.op || defaultOp(a.type), (v) => { a.op = v; }, { class: 'input sm', style: { width: '130px' } }),
        value,
        h('button', { class: 'btn ghost sm icon', type: 'button', title: t('action.remove'), onclick: () => { list.splice(i, 1); draw(); } }, icon('trash', 12))));
    });
    box.appendChild(h('div', h('button', { class: 'btn sm', type: 'button', onclick: () => { list.push({ type: 'status', op: 'eq', value: 200 }); draw(); } }, icon('plus', 12), t('synth.assert.add'))));
    if (!list.length && opts.hint) box.insertBefore(h('div', { class: 'xsmall muted' }, opts.hint), box.firstChild);
  }
  draw();
  return box;
}

export function extractEditor(list, onChange) {
  const box = h('div', { class: 'col', style: { gap: '6px' } });
  function draw() {
    clear(box);
    list.forEach((x, i) => {
      const name = h('input', { class: 'input mono sm', value: x.name || '', placeholder: 'jeton', style: { width: '120px' } });
      name.addEventListener('input', () => { x.name = name.value.replace(/[^\w.\-]/g, ''); if (onChange) onChange(); });
      const path = h('input', { class: 'input mono sm', value: x.path || '', placeholder: x.from === 'header' ? 'x-request-id' : x.from === 'regex' ? 'id=(\\d+)' : '$.token', style: { width: '200px' } });
      path.addEventListener('input', () => { x.path = path.value; });
      box.appendChild(h('div', { class: 'row wrap', style: { gap: '6px' } },
        h('span', { class: 'xsmall muted' }, '{{'), name, h('span', { class: 'xsmall muted' }, '}} ←'),
        select(EXTRACT_FROM.map((f) => ({ value: f, label: t('synth.extract.from.' + f) })), x.from || 'json_path', (v) => { x.from = v; draw(); }, { class: 'input sm', style: { width: '140px' } }),
        path,
        h('button', { class: 'btn ghost sm icon', type: 'button', title: t('action.remove'), onclick: () => { list.splice(i, 1); draw(); if (onChange) onChange(); } }, icon('trash', 12))));
    });
    box.appendChild(h('div', h('button', { class: 'btn sm', type: 'button', onclick: () => { list.push({ name: '', from: 'json_path', path: '' }); draw(); } }, icon('plus', 12), t('synth.extract.add'))));
  }
  draw();
  return box;
}

// Variables de config (clé → valeur), utilisables partout en {{nom}}.
export function variablesEditor(obj, onChange) {
  const box = h('div', { class: 'col', style: { gap: '6px' } });
  let rows = Object.entries(obj || {}).map(([k, v]) => ({ k, v: String(v) }));
  const sync = () => { for (const k of Object.keys(obj)) delete obj[k]; rows.forEach((r) => { if (r.k) obj[r.k] = r.v; }); if (onChange) onChange(); };
  function draw() {
    clear(box);
    rows.forEach((r, i) => {
      const k = h('input', { class: 'input mono sm', value: r.k, placeholder: 'base_url', style: { width: '140px' } });
      const v = h('input', { class: 'input mono sm', value: r.v, placeholder: 'https://api.example.fr', style: { flex: '1 1 220px' } });
      k.addEventListener('input', () => { r.k = k.value.replace(/[^\w.\-]/g, ''); sync(); });
      v.addEventListener('input', () => { r.v = v.value; sync(); });
      box.appendChild(h('div', { class: 'row', style: { gap: '6px' } }, k, h('span', { class: 'muted' }, '='), v,
        h('button', { class: 'btn ghost sm icon', type: 'button', title: t('action.remove'), onclick: () => { rows.splice(i, 1); sync(); draw(); } }, icon('trash', 12))));
    });
    box.appendChild(h('div', h('button', { class: 'btn sm', type: 'button', onclick: () => { rows.push({ k: '', v: '' }); draw(); } }, icon('plus', 12), t('synth.vars.add'))));
  }
  draw();
  return box;
}

const VAR_RE = /\{\{\s*([\w.\-]+)\s*\}\}/g;
function usedVars(step) {
  const out = new Set();
  const scan = (s2) => { let m; VAR_RE.lastIndex = 0; while ((m = VAR_RE.exec(String(s2 || '')))) out.add(m[1]); };
  scan(step.url); scan(step.body); Object.entries(step.headers || {}).forEach(([k, v]) => { scan(k); scan(v); });
  return out;
}

export function newStep(i) {
  return { name: t('synth.step.default_name', { n: i + 1 }), method: 'GET', url: 'https://', headers: {}, body: '', timeout_s: 10, follow_redirects: true, assertions: [{ type: 'status', op: 'eq', value: 200 }], extract: [] };
}

// Éditeur d'étapes : ajout, ordre, duplication, assertions, extractions ; variables disponibles affichées.
export function stepsEditor(steps, variables) {
  const box = h('div', { class: 'col gap-3' });
  const open = new Set([0]);
  function available(i) {
    const vars = new Set(Object.keys(variables || {}));
    for (let j = 0; j < i; j++) for (const x of steps[j].extract || []) if (x.name) vars.add(x.name);
    return vars;
  }
  function draw() {
    clear(box);
    steps.forEach((st, i) => {
      st.assertions = Array.isArray(st.assertions) ? st.assertions : [];
      st.extract = Array.isArray(st.extract) ? st.extract : [];
      st.headers = st.headers && typeof st.headers === 'object' ? st.headers : {};
      const avail = available(i);
      const missing = Array.from(usedVars(st)).filter((v) => !avail.has(v));
      const isOpen = open.has(i);
      const head = h('div', { class: 'step-head' },
        h('span', { class: 'step-num' }, String(i + 1)),
        h('b', { class: 'ellipsis grow' }, st.name || t('synth.step.default_name', { n: i + 1 })),
        h('span', { class: 'mono xsmall muted ellipsis', style: { maxWidth: '320px' } }, (st.method || 'GET') + ' ' + (st.url || '')),
        missing.length ? h('span', { class: 'badge warn', title: t('synth.vars.missing', { list: missing.join(', ') }) }, icon('warn', 11), tn('synth.vars.missing_n', missing.length)) : null,
        h('span', { class: 'badge muted' }, tn('synth.assert.count', st.assertions.length)),
        h('button', { class: 'btn ghost sm icon', type: 'button', title: t('synth.step.up'), disabled: i === 0, onclick: (e) => { e.stopPropagation(); [steps[i - 1], steps[i]] = [steps[i], steps[i - 1]]; open.clear(); open.add(i - 1); draw(); } }, icon('up', 12)),
        h('button', { class: 'btn ghost sm icon', type: 'button', title: t('synth.step.down'), disabled: i === steps.length - 1, onclick: (e) => { e.stopPropagation(); [steps[i + 1], steps[i]] = [steps[i], steps[i + 1]]; open.clear(); open.add(i + 1); draw(); } }, icon('down', 12)),
        h('button', { class: 'btn ghost sm icon', type: 'button', title: t('action.duplicate'), onclick: (e) => { e.stopPropagation(); const c = JSON.parse(JSON.stringify(st)); c.name = (c.name || '') + ' ' + t('dash.copy_suffix'); steps.splice(i + 1, 0, c); open.add(i + 1); draw(); } }, icon('copy', 12)),
        h('button', { class: 'btn ghost sm icon danger', type: 'button', title: t('action.remove'), disabled: steps.length <= 1, onclick: (e) => { e.stopPropagation(); steps.splice(i, 1); open.clear(); draw(); } }, icon('trash', 12)),
        h('span', { class: 'muted' }, icon(isOpen ? 'chevronDown' : 'chevronRight', 14)));
      head.addEventListener('click', () => { if (open.has(i)) open.delete(i); else open.add(i); draw(); });
      const card = h('div', { class: ['step-card', isOpen ? 'open' : null] }, head);
      if (isOpen) {
        const name = h('input', { class: 'input', value: st.name || '' });
        name.addEventListener('input', () => { st.name = name.value; });
        const url = h('input', { class: 'input mono', value: st.url || '', placeholder: 'https://{{base_url}}/api/connexion', spellcheck: 'false' });
        url.addEventListener('change', () => { st.url = url.value.trim(); draw(); });
        const hd = h('textarea', { class: 'input mono', rows: 2, placeholder: 'Authorization: Bearer {{jeton}}' });
        hd.value = headersToText(st.headers);
        hd.addEventListener('change', () => { st.headers = textToHeaders(hd.value); draw(); });
        const bd = h('textarea', { class: 'input mono', rows: 3, placeholder: '{"identifiant":"{{utilisateur}}"}' });
        bd.value = st.body || '';
        bd.addEventListener('change', () => { st.body = bd.value; draw(); });
        const to = h('input', { class: 'input', type: 'number', min: 1, max: 120, value: st.timeout_s ?? 10, style: { width: '90px' } });
        to.addEventListener('input', () => { st.timeout_s = Number(to.value) || 10; });
        const fr = h('input', { type: 'checkbox', checked: st.follow_redirects !== false });
        fr.addEventListener('change', () => { st.follow_redirects = fr.checked; });
        card.appendChild(h('div', { class: 'step-body' },
          h('div', { class: 'form-grid' },
            field(t('synth.step.name'), name),
            field(t('synth.f.method'), select(METHODS.map((m) => ({ value: m, label: m })), st.method || 'GET', (v) => { st.method = v; draw(); })),
            h('div', { style: { gridColumn: 'span 2' } }, field(t('synth.step.url'), url)),
            field(t('synth.f.timeout'), to),
            h('div', { class: 'col', style: { justifyContent: 'flex-end' } }, h('label', { class: 'check' }, fr, t('synth.f.follow'))),
            h('div', { style: { gridColumn: 'span 2' } }, field(t('synth.f.headers'), hd, t('synth.f.headers_hint'))),
            !['GET', 'HEAD', 'OPTIONS'].includes(st.method) ? h('div', { style: { gridColumn: 'span 2' } }, field(t('synth.f.body'), bd)) : null),
          avail.size ? h('div', { class: 'row wrap xsmall mt-2', style: { gap: '4px' } }, h('span', { class: 'muted' }, t('synth.vars.available')), Array.from(avail).sort().map((v) => h('button', {
            type: 'button', class: 'tag', title: t('synth.vars.insert'),
            onclick: () => { const ins = '{{' + v + '}}'; url.value += ins; st.url = url.value; url.focus(); },
          }, '{{' + v + '}}'))) : null,
          missing.length ? h('div', { class: 'callout warn mt-2', style: { padding: '6px 10px' } }, icon('warn', 14), h('span', { class: 'xsmall' }, t('synth.vars.missing', { list: missing.join(', ') }))) : null,
          h('div', { class: 'grid c2 mt-3' },
            h('div', h('h4', { class: 'mb-2' }, t('synth.assert.title')), assertionsEditor(st.assertions, { hint: t('synth.assert.none_hint') })),
            h('div', h('h4', { class: 'mb-2' }, t('synth.extract.title')), h('div', { class: 'xsmall muted mb-2' }, t('synth.extract.hint')), extractEditor(st.extract, () => {})))));
      }
      box.appendChild(card);
    });
    box.appendChild(h('div', h('button', { class: 'btn sm', type: 'button', onclick: () => { steps.push(newStep(steps.length)); open.clear(); open.add(steps.length - 1); draw(); } }, icon('plus', 12), t('synth.step.add'))));
  }
  draw();
  box.redraw = draw;
  return box;
}

// Emplacements : le serveur HYPHA et les agents qui annoncent la capacité « synthetics ».
export function locationsPicker(selected, ctx) {
  const box = h('div', { class: 'col', style: { gap: '6px' } });
  const set = new Set(selected.length ? selected : ['server']);
  const sync = () => { selected.splice(0, selected.length, ...Array.from(set)); };
  sync();
  const row = (value, label, sub, disabled) => {
    const cb = h('input', { type: 'checkbox', checked: set.has(value), disabled: !!disabled });
    cb.addEventListener('change', () => { if (cb.checked) set.add(value); else set.delete(value); if (!set.size) { set.add('server'); draw(); } sync(); });
    return h('label', { class: 'check loc-row' }, cb, h('span', label), sub ? h('span', { class: 'xsmall muted' }, sub) : null);
  };
  let agents = null;
  let failed = false;
  function draw() {
    clear(box);
    box.appendChild(row('server', t('synth.loc.server'), t('synth.loc.server_sub')));
    if (agents === null && !failed) { box.appendChild(h('div', { class: 'row xsmall muted' }, h('span', { class: 'spinner' }), t('synth.loc.loading'))); return; }
    if (failed) { box.appendChild(h('div', { class: 'xsmall muted' }, t('synth.loc.fleet_unavailable'))); }
    const capable = (agents || []).filter((a) => a.capabilities && a.capabilities.synthetics && a.host_id !== undefined && a.host_id !== null);
    for (const a of capable.sort((x, y) => String(x.hostname).localeCompare(String(y.hostname)))) box.appendChild(row('agent:' + a.host_id, t('synth.loc.agent', { host: a.hostname || '#' + a.host_id }), [a.os, a.version].filter(Boolean).join(' · ')));
    // Emplacements déjà choisis dont l'agent n'a plus la capacité : gardés, signalés.
    for (const v of set) if (v.startsWith('agent:') && !capable.some((a) => 'agent:' + a.host_id === v)) box.appendChild(h('div', { class: 'row' }, row(v, v), h('span', { class: 'badge warn' }, t('synth.loc.no_cap'))));
    if (!capable.length && !failed) box.appendChild(h('div', { class: 'xsmall muted' }, t('synth.loc.none_capable')));
  }
  draw();
  get('/fleet', null, { signal: ctx.signal }).then((r) => { agents = asList(r, 'agents', 'fleet'); draw(); }).catch(() => { failed = true; agents = []; draw(); });
  return box;
}

export function locationLabel(loc, hostNames) {
  if (!loc || loc === 'server') return t('synth.loc.server');
  const m = /^agent:(.+)$/.exec(String(loc));
  if (m) return t('synth.loc.agent', { host: (hostNames && hostNames.get(m[1])) || m[1] });
  return String(loc);
}

// Résultats d'une exécution, étape par étape (details.steps).
export function stepResults(details) {
  const steps = asList(details && details.steps);
  if (!steps.length) return null;
  const rows = [];
  steps.forEach((st, i) => {
    const asserts = asList(st.assertions);
    const ok = st.ok !== undefined ? !!st.ok : asserts.every((a) => a.ok !== false);
    rows.push(h('div', { class: ['step-res', ok ? 'ok' : 'ko'] },
      h('div', { class: 'row', style: { gap: '8px' } },
        h('span', { class: 'step-num' }, String(i + 1)),
        h('b', { class: 'grow ellipsis' }, st.name || t('synth.step.default_name', { n: i + 1 })),
        isNum(st.status_code) ? h('span', { class: 'badge muted mono' }, String(st.status_code)) : null,
        h('span', { class: 'num small' }, fmtDuration(st.duration_ms ?? st.latency_ms)),
        h('span', { class: ['badge', ok ? 'ok' : 'alert'] }, ok ? t('synth.up') : t('synth.down'))),
      st.error ? h('div', { class: 'small mt-2', style: { color: 'var(--crit)' } }, String(st.error)) : null,
      asserts.length ? h('ul', { class: 'assert-list' }, asserts.map((a) => h('li', { class: a.ok === false ? 'ko' : 'ok' },
        icon(a.ok === false ? 'x' : 'check', 12),
        h('span', { class: 'mono small' }, t('synth.assert.type.' + (a.type || 'status')) + (a.path ? ' ' + a.path : '') + ' ' + t('synth.assert.op.' + (a.op || 'eq')) + ' ' + (a.value !== undefined ? JSON.stringify(a.value) : '')),
        a.actual !== undefined ? h('span', { class: 'xsmall muted mono' }, t('synth.assert.actual', { v: typeof a.actual === 'string' ? a.actual.slice(0, 120) : JSON.stringify(a.actual) })) : null))) : null,
      st.extracted && typeof st.extracted === 'object' && Object.keys(st.extracted).length ? h('div', { class: 'xsmall muted mono mt-2' }, t('synth.extract.got', { list: Object.keys(st.extracted).join(', ') })) : null));
  });
  return h('div', { class: 'col', style: { gap: '6px' } }, rows);
}

// Synthèse par emplacement : disponibilité, latence moyenne, dernier état.
export function locationTable(list, hostNames) {
  const by = new Map();
  for (const r of list) {
    const k = r.location || 'server';
    if (!by.has(k)) by.set(k, []);
    by.get(k).push(r);
  }
  const rows = Array.from(by.entries()).map(([loc, rs]) => {
    rs.sort((a, b) => (toMs(a.ts) || 0) - (toMs(b.ts) || 0));
    const ok = rs.filter((r) => r.ok).length;
    const lat = rs.map((r) => r.latency_ms).filter(isNum);
    const last = rs[rs.length - 1];
    return { loc, n: rs.length, uptime: ok / rs.length * 100, latency: lat.length ? lat.reduce((a, b) => a + b, 0) / lat.length : null, last };
  });
  return dataTable({
    columns: [
      { key: 'loc', label: t('col.location'), render: (r) => locationLabel(r.loc, hostNames) },
      { key: 'last', label: t('col.status'), value: (r) => (r.last && r.last.ok ? 1 : 0), render: (r) => (r.last ? h('span', { class: ['badge', r.last.ok ? 'ok' : 'alert'], title: fmtDateTime(r.last.ts) }, r.last.ok ? t('synth.up') : t('synth.down')) : '-') },
      { key: 'uptime', label: t('col.uptime'), align: 'r', render: (r) => h('span', { style: { color: r.uptime >= 99.9 ? 'var(--ok)' : r.uptime >= 99 ? 'var(--warn)' : 'var(--crit)' } }, fmtPct(r.uptime, 2)) },
      { key: 'latency', label: t('synth.avg_latency'), align: 'r', render: (r) => fmtDuration(r.latency) },
      { key: 'n', label: t('synth.runs_col'), align: 'r' },
      { key: 'when', label: t('col.last_run'), align: 'r', value: (r) => toMs(r.last && r.last.ts), render: (r) => fmtRel(r.last && r.last.ts) },
    ],
    rows, sortKey: 'loc', sortDir: 'asc', emptyText: t('synth.no_result_range'),
  });
}
