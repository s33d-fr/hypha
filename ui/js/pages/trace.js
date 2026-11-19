// Trace : cascade des spans (barres imbriquées, couleur par service), détail du span, journaux liés.
import { h, clear, icon } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, asList, isAbort } from '../api.js';
import { fmtDuration, fmtDateTimeFull, fmtPct, toMs, isNum } from '../format.js';
import { empty, errorState, panel, kv, copyButton, loading } from '../components/ui.js';
import { jsonTree } from '../components/jsontree.js';
import { colorAt } from '../components/chart.js';
import { compactLogRow } from './logs-shared.js';
import { explainButton } from '../components/explain.js';

const MAX_ROWS = 3000;

function buildTree(spans) {
  const byId = new Map();
  for (const sp of spans) byId.set(sp.span_id, Object.assign({ children: [] }, sp, { _ts: toMs(sp.ts) || 0, _dur: isNum(sp.duration_ms) ? sp.duration_ms : 0 }));
  const roots = [];
  for (const n of byId.values()) {
    const p = n.parent_span_id ? byId.get(n.parent_span_id) : null;
    if (p && p !== n) p.children.push(n); else roots.push(n);
  }
  const sortRec = (list) => { list.sort((a, b) => a._ts - b._ts); list.forEach((x) => sortRec(x.children)); };
  sortRec(roots);
  return { roots, byId };
}

// Temps propre d'un span : sa durée moins l'union des intervalles de ses enfants.
function selfTime(n) {
  if (!n.children.length) return n._dur;
  const iv = n.children.map((c) => [Math.max(c._ts, n._ts), Math.min(c._ts + c._dur, n._ts + n._dur)]).filter(([a, b]) => b > a).sort((a, b) => a[0] - b[0]);
  let covered = 0; let cs = null; let ce = null;
  for (const [a, b] of iv) {
    if (cs === null) { cs = a; ce = b; } else if (a <= ce) ce = Math.max(ce, b); else { covered += ce - cs; cs = a; ce = b; }
  }
  if (cs !== null) covered += ce - cs;
  return Math.max(0, n._dur - covered);
}

function niceTicks(total) {
  const n = 5;
  return Array.from({ length: n + 1 }, (_, i) => (total * i) / n);
}

export async function render(root, ctx) {
  const { args, signal, href } = ctx;
  const traceId = args.id;
  ctx.setCrumbs([{ label: t('nav.apm'), href: href('/apm') }, { label: t('nav.apm.traces'), href: href('/apm/traces') }, { label: traceId.slice(0, 16) }]);
  root.appendChild(loading());
  let resp;
  try {
    resp = await get('/apm/traces/' + encodeURIComponent(traceId), null, { signal });
  } catch (e) {
    if (isAbort(e)) return;
    clear(root);
    root.appendChild(errorState(e, () => ctx.refresh()));
    return;
  }
  clear(root);
  const spans = asList(resp && resp.spans, 'spans');
  const logs = asList(resp && resp.logs, 'logs');
  if (!spans.length) { root.appendChild(empty(t('trace.none_title'), t('trace.none_text'))); return; }

  const { roots, byId } = buildTree(spans);
  const all = Array.from(byId.values());
  const start = Math.min(...all.map((n) => n._ts));
  const end = Math.max(...all.map((n) => n._ts + n._dur));
  const total = Math.max(0.001, end - start);
  const services = [];
  for (const n of all.slice().sort((a, b) => a._ts - b._ts)) if (!services.includes(n.service)) services.push(n.service);
  const svcColor = new Map(services.map((s2, i) => [s2, colorAt(i)]));
  const errors = all.filter((n) => n.status === 'error').length;
  const root0 = roots[0];

  const selfBySvc = new Map();
  for (const n of all) selfBySvc.set(n.service, (selfBySvc.get(n.service) || 0) + selfTime(n));
  const selfTotal = Array.from(selfBySvc.values()).reduce((a, b) => a + b, 0) || 1;

  root.appendChild(h('div', { class: 'page-head' },
    h('h1', (root0.service ? root0.service + ' · ' : '') + (root0.name || '')),
    errors ? h('span', { class: 'badge alert' }, tn('trace.errors', errors)) : h('span', { class: 'badge ok' }, t('apm.ok')),
    h('span', { class: 'desc' }, [fmtDuration(total), tn('trace.spans', all.length), tn('trace.services', services.length), fmtDateTimeFull(start)].join(' · ')),
    h('span', { class: 'spacer' }),
    explainButton(ctx.me, { kind: 'trace', trace_id: traceId }, { subject: (root0.service ? root0.service + ' · ' : '') + (root0.name || '') }),
    h('span', { class: 'mono xsmall muted' }, traceId), copyButton(traceId, { label: false })));
  if (root0.resource) root.appendChild(h('div', { class: 'mono small text-2 mb-3' }, root0.resource));

  root.appendChild(h('div', { class: 'svc-legend mb-3' }, services.map((s2) => h('span', { title: t('trace.self_time') },
    h('span', { class: 'sw', style: { background: svcColor.get(s2) } }), s2, ' ', h('span', { class: 'muted' }, fmtPct((selfBySvc.get(s2) || 0) / selfTotal * 100))))));

  const collapsed = new Set();
  const wf = h('div', { class: 'wf' });
  const detailEl = h('div');
  let selected = root0.span_id;
  root.appendChild(h('div', { class: 'split' }, h('section', { class: 'panel', style: { overflow: 'hidden' } }, wf), h('div', { class: 'split-v' }, detailEl, logsPanel())));

  function drawWaterfall() {
    clear(wf);
    const ticks = niceTicks(total);
    wf.appendChild(h('div', { class: 'wf-head' }, h('div', { class: 'small muted', style: { paddingLeft: '10px' } }, t('trace.span')),
      h('div', { class: 'wf-ticks' }, ticks.map((v, i) => h('span', { style: { left: (v / total * 100) + '%', transform: i === ticks.length - 1 ? 'translateX(-100%)' : i === 0 ? 'none' : null } }, fmtDuration(v))))));
    let count = 0;
    const walk = (n, depth) => {
      if (count >= MAX_ROWS) return;
      count++;
      const left = (n._ts - start) / total * 100;
      const width = Math.max(0.15, n._dur / total * 100);
      const hasKids = n.children.length > 0;
      const isCollapsed = collapsed.has(n.span_id);
      const tw = h('span', { class: 'tw' }, hasKids ? (isCollapsed ? '▸' : '▾') : '');
      if (hasKids) tw.addEventListener('click', (e) => { e.stopPropagation(); if (isCollapsed) collapsed.delete(n.span_id); else collapsed.add(n.span_id); drawWaterfall(); });
      const durLabel = h('span', { class: 'wf-dur' }, fmtDuration(n._dur));
      if (left + width > 82) { durLabel.style.right = (100 - left + 0.5) + '%'; } else { durLabel.style.left = 'calc(' + (left + width) + '% + 4px)'; }
      const row = h('div', { class: ['wf-row', n.span_id === selected ? 'sel' : null], title: (n.service || '') + ' · ' + (n.name || '') + (n.resource ? '\n' + n.resource : '') },
        h('div', { class: 'wf-name', style: { paddingLeft: (6 + depth * 14) + 'px' } }, tw,
          h('span', { class: 'dot', style: { background: svcColor.get(n.service) } }),
          h('span', { class: 'svc' }, n.service || ''), h('span', { class: 'op' }, n.name || ''),
          hasKids && isCollapsed ? h('span', { class: 'badge muted' }, String(n.children.length)) : null),
        h('div', { class: 'wf-track' },
          ticks.slice(1, -1).map((v) => h('span', { class: 'grid-l', style: { left: (v / total * 100) + '%' } })),
          h('span', { class: ['wf-bar', n.status === 'error' ? 'err' : null], style: { left: left + '%', width: width + '%', background: svcColor.get(n.service) } }),
          durLabel));
      row.addEventListener('click', () => { selected = n.span_id; drawWaterfall(); drawDetail(); });
      wf.appendChild(row);
      if (!isCollapsed) n.children.forEach((c) => walk(c, depth + 1));
    };
    roots.forEach((r) => walk(r, 0));
    if (count >= MAX_ROWS) wf.appendChild(h('div', { class: 'tbl-foot' }, t('trace.truncated', { n: MAX_ROWS })));
  }

  function drawDetail() {
    clear(detailEl);
    const n = byId.get(selected);
    if (!n) return;
    const events = Array.isArray(n.events) ? n.events : [];
    detailEl.appendChild(panel(t('trace.span_detail'), h('div', { class: 'col gap-3' },
      h('div', { class: 'row' }, h('span', { class: 'dot', style: { background: svcColor.get(n.service) } }), h('b', n.service || ''), h('span', { class: 'mono small' }, n.name || ''),
        n.status === 'error' ? h('span', { class: 'badge alert' }, t('apm.error')) : null),
      kv([
        [t('col.resource'), n.resource ? h('span', { class: 'mono small' }, n.resource) : null],
        [t('col.duration'), fmtDuration(n._dur) + ' (' + fmtPct(n._dur / total * 100) + ')'],
        [t('trace.self_time'), fmtDuration(selfTime(n))],
        [t('trace.offset'), fmtDuration(n._ts - start)],
        [t('trace.kind'), n.kind || null],
        [t('col.status'), n.status || null],
        [t('trace.status_message'), n.status_message || undefined],
        [t('col.host'), n.hostname || null],
        [t('trace.span_id'), h('span', { class: 'mono small' }, n.span_id || '-')],
        [t('trace.parent_id'), n.parent_span_id ? h('span', { class: 'mono small' }, n.parent_span_id) : null],
      ]),
      h('div', h('h4', { class: 'mb-2' }, t('trace.attributes')), n.attributes && Object.keys(n.attributes).length ? jsonTree(n.attributes) : h('div', { class: 'muted small' }, t('logs.no_attributes'))),
      events.length ? h('div', h('h4', { class: 'mb-2' }, tn('trace.events', events.length)),
        h('ul', { class: 'list-plain' }, events.slice(0, 50).map((ev) => {
          const ets = toMs(ev.ts !== undefined ? ev.ts : ev.time);
          return h('li', { style: { display: 'block' } },
            h('div', { class: 'row' }, h('b', { class: 'small' }, ev.name || t('trace.event')), ets !== null ? h('span', { class: 'muted xsmall' }, '+' + fmtDuration(ets - start)) : null),
            ev.attributes && Object.keys(ev.attributes).length ? jsonTree(ev.attributes) : null);
        }))) : null)));
  }

  function logsPanel() {
    const body = h('div', { class: 'log-list' });
    if (!logs.length) body.appendChild(empty(null, t('trace.no_logs')));
    for (const l of logs.slice().sort((a, b) => (toMs(a.ts) || 0) - (toMs(b.ts) || 0))) {
      const row = compactLogRow(l, () => {
        const nx = row.nextElementSibling;
        if (nx && nx.classList.contains('log-detail')) { nx.remove(); return; }
        row.after(h('div', { class: 'log-detail' }, h('pre', l.message || ''), l.attributes && Object.keys(l.attributes).length ? jsonTree(l.attributes) : null,
          l.span_id && byId.has(l.span_id) ? h('button', { class: 'btn sm mt-2', type: 'button', onclick: () => { selected = l.span_id; drawWaterfall(); drawDetail(); } }, icon('trace', 12), t('trace.go_span')) : null));
      });
      body.appendChild(row);
    }
    return panel(tn('trace.logs', logs.length), body, { flush: true });
  }

  drawWaterfall();
  drawDetail();
}
