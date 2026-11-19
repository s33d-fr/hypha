// Journaux : barre de requête (§ 7), histogramme par statut, facettes, liste paginée par curseur, détail, direct.
import { h, clear, icon } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, asList, isAbort } from '../api.js';
import { fmtDateTime, fmtDateTimeFull, fmtInt, fmtDuration, isNum, toMs } from '../format.js';
import { section, empty, errorState, tagList, kv, copyButton, searchInput, toast, tabs } from '../components/ui.js';
import { jsonTree } from '../components/jsontree.js';
import { qv } from './common.js';
import { histogramChart, normStatus, statusLabel, statusColor } from './logs-shared.js';
import { explainButton, idValue } from '../components/explain.js';
import { openFilterForm, canWriteFilters } from './log-filters.js';
import { renderPatterns, openContext, SECURITY_FACETS, outcomeColor, securityShortcuts } from './logs-extra.js';
import { viewsMenu } from '../components/views.js';

const FACETS = [['status', 'logs.facet.status'], ['service', 'logs.facet.service'], ['host', 'logs.facet.host'], ['source', 'logs.facet.source']];
// Facettes d'attributs (§ 12) : affichées seulement si le serveur en rend des valeurs.
const ATTR_FACETS = [['@http.status_code', 'logs.facet.http_status'], ['@http.method', 'logs.facet.http_method']];
const ATTRS_PARAM = ATTR_FACETS.map(([k]) => k.slice(1)).join(',');
// Facettes de sécurité (§ 23) : demandées en plus ; un serveur qui les refuse (400) reçoit la liste de base.
const ATTRS_PARAM_ALL = ATTR_FACETS.concat(SECURITY_FACETS).map(([k]) => k.slice(1)).join(',');

// Attribut par chemin, que l'objet soit imbriqué ({http:{method}}) ou à plat ({"http.method"}).
export function attrAt(attrs, path) {
  if (!attrs || typeof attrs !== 'object') return undefined;
  if (Object.prototype.hasOwnProperty.call(attrs, path)) return attrs[path];
  let cur = attrs;
  for (const p of path.split('.')) { if (!cur || typeof cur !== 'object') return undefined; cur = cur[p]; }
  return cur;
}

// Les attributs http.* passent en tête de l'arbre.
function httpFirst(attrs) {
  const keys = Object.keys(attrs);
  const first = keys.filter((k) => k === 'http' || k.startsWith('http.'));
  const out = {};
  for (const k of first.concat(keys.filter((k) => !first.includes(k)))) out[k] = attrs[k];
  return out;
}

function httpCodeColor(v) {
  const n = Number(v);
  return n >= 500 ? 'var(--log-error)' : n >= 400 ? 'var(--log-warning)' : n >= 300 ? 'var(--log-notice)' : n >= 200 ? 'var(--ok)' : 'var(--nodata)';
}
const PAGE = 100;
const MAX_ROWS = 5000;
const LIVE_MAX = 2000;

// Découpe une requête en termes, guillemets respectés.
export function tokenize(q) {
  const out = [];
  const re = /(-?[@\w.\-/]+:"(?:[^"\\]|\\.)*"|-?"(?:[^"\\]|\\.)*"|\S+)/g;
  let m;
  while ((m = re.exec(q || ''))) out.push(m[1]);
  return out;
}

export function toggleTerm(q, term) {
  const toks = tokenize(q);
  const opposite = term.startsWith('-') ? term.slice(1) : '-' + term;
  const has = toks.includes(term);
  const kept = toks.filter((x) => x !== term && x !== opposite);
  if (!has) kept.push(term);
  return kept.join(' ');
}

export async function render(root, ctx) {
  const { params, range, signal, href } = ctx;
  const q = params.get('q') || '';
  const live = params.get('live') === '1';
  const focusId = params.get('focus');
  const tab = params.get('tab') === 'patterns' ? 'patterns' : 'list';
  ctx.setCrumbs([{ label: t('nav.logs'), href: href('/logs') }, { label: live ? t('nav.logs.live') : t('nav.logs.search') }]);

  const search = searchInput({ placeholder: t('logs.query_ph'), value: q, onEnter: (v) => ctx.setParams({ q: v.trim() || null, focus: null }) });
  const liveBtn = h('button', { class: ['btn', live ? 'on' : null], type: 'button', title: t('logs.live_title'), onclick: () => ctx.setParams({ live: live ? null : '1' }) },
    h('span', { class: 'dot-live' }), live ? t('logs.live_on') : t('logs.live'));
  const helpBtn = h('button', { class: 'btn ghost sm', type: 'button', onclick: () => { help.hidden = !help.hidden; } }, icon('info', 13), t('logs.syntax'));
  const help = h('div', { class: 'callout mb-3', hidden: true }, icon('info', 16), h('div', { class: 'small' },
    h('div', t('logs.help.1')), h('div', { class: 'mono' }, t('logs.help.example'))));
  const excludeBtn = canWriteFilters(ctx.me) ? h('button', {
    class: 'btn', type: 'button', title: t('lf.from_logs_title'),
    onclick: () => openFilterForm(ctx, { query: search.input.value.trim() || q, onSaved: () => toast(t('lf.from_logs_done'), 'ok', 5000) }),
  }, icon('filter', 13), t('lf.from_logs')) : null;
  const views = viewsMenu({
    page: 'logs', me: ctx.me,
    getState: () => ({ q: search.input.value.trim() || q, tab, t: range.preset || null }),
    apply: (st) => { const up = { q: st.q || null, tab: st.tab === 'patterns' ? 'patterns' : null, focus: null }; if (st.t) Object.assign(up, { t: st.t, from: null, to: null }); ctx.setParams(up); },
  });
  root.appendChild(h('div', { class: 'querybar' }, search, h('button', { class: 'btn primary', type: 'button', onclick: () => ctx.setParams({ q: search.input.value.trim() || null, focus: null }) }, t('action.search')), liveBtn, excludeBtn, views, helpBtn));
  root.appendChild(help);

  const facetsEl = h('div', { class: 'panel facets' });
  const histEl = h('div', { class: 'panel-body' });
  const listHead = h('div', { class: 'panel-head' });
  const listEl = h('div', { class: 'log-list', 'data-no-autorefresh': live ? '1' : null });
  const moreEl = h('div');
  const patternsEl = h('div');
  function logTabs() {
    return tabs([{ key: 'list', label: t('logs.tab.list') }, { key: 'patterns', label: t('logs.tab.patterns') }], tab, (k) => ctx.setParams({ tab: k === 'list' ? null : k, focus: null }));
  }
  root.appendChild(h('div', { class: 'logs-layout' },
    facetsEl,
    h('div', { class: 'col gap-3', style: { minWidth: '0' } },
      h('section', { class: 'panel' }, histEl),
      tab === 'patterns'
        ? h('section', { class: 'panel' }, logTabs(), patternsEl)
        : h('section', { class: 'panel' }, logTabs(), listHead, h('div', { class: 'log-head' }, h('span'), h('span', t('col.date')), h('span', t('col.status')), h('span', { class: 'c-host' }, t('col.host')), h('span', { class: 'c-svc' }, t('col.service')), h('span', t('col.message'))), listEl, moreEl))));

  const setQuery = (nq) => ctx.setParams({ q: nq || null, focus: null });

  // Facettes
  section(facetsEl, async () => {
    try { return await get('/logs/facets', { q, from: range.from, to: range.to, attrs: ATTRS_PARAM_ALL }, { signal }); } catch (e) {
      if (e && e.status === 400) return get('/logs/facets', { q, from: range.from, to: range.to, attrs: ATTRS_PARAM }, { signal });
      throw e;
    }
  }, (resp) => {
    const box = h('div');
    const toks = tokenize(q);
    const attrFacets = ATTR_FACETS.concat(SECURITY_FACETS).filter(([k]) => Array.isArray(resp && resp[k]) && resp[k].length);
    for (const [key, label] of FACETS.concat(attrFacets)) {
      const vals = Array.isArray(resp && resp[key]) ? resp[key] : [];
      const f = h('div', { class: 'facet' }, h('h4', t(label)));
      if (!vals.length) f.appendChild(h('div', { class: 'xsmall muted' }, t('logs.facet_empty')));
      vals.slice(0, 25).forEach((v) => {
        const value = v && typeof v === 'object' ? v.value : v;
        const count = v && typeof v === 'object' ? v.count : null;
        if (value === null || value === undefined || value === '') return;
        const term = key + ':' + qv(value);
        const on = toks.includes(term);
        const ex = h('span', { class: 'ex', title: t('logs.exclude'), role: 'button' }, '−');
        ex.addEventListener('click', (e) => { e.stopPropagation(); setQuery(toggleTerm(q, '-' + term)); });
        f.appendChild(h('button', { type: 'button', class: ['facet-row', on ? 'on' : null], title: term, onclick: () => setQuery(toggleTerm(q, term)) },
          key === 'status' ? h('span', { class: 'dot', style: { background: statusColor(value) } }) : key === '@http.status_code' ? h('span', { class: 'dot', style: { background: httpCodeColor(value) } }) : key === '@evt.outcome' ? h('span', { class: 'dot', style: { background: outcomeColor(value) } }) : null,
          h('span', { class: 'ellipsis' }, key === 'status' ? statusLabel(value) : String(value)),
          ex,
          h('span', { class: 'cnt' }, isNum(count) ? fmtInt(count) : '')));
      });
      box.appendChild(f);
    }
    box.appendChild(securityShortcuts((sq) => setQuery(sq)));
    return box;
  });

  // Histogramme
  section(histEl, () => get('/logs/histogram', { q, from: range.from, to: range.to, buckets: 60 }, { signal }), (resp) => histogramChart(resp, range, { onRange: ctx.onRange, height: 120 }));

  // Liste
  const rows = [];
  const seen = new Set();
  let cursor = null;
  let loadingMore = false;
  let maxId = null;
  let openId = null;
  // Identifiants bigint : nombre ou chaîne de chiffres selon le sérialiseur du serveur.
  function bumpMax(id) {
    const n = typeof id === 'string' && /^\d+$/.test(id) ? Number(id) : id;
    if (isNum(n) && (maxId === null || n > maxId)) maxId = n;
  }
  const countEl = h('span', { class: 'sub' });
  clear(listHead);
  listHead.appendChild(h('h3', t('logs.list')));
  listHead.appendChild(countEl);
  listHead.appendChild(h('span', { class: 'spacer' }));
  if (live) listHead.appendChild(h('span', { class: 'badge ok' }, h('span', { class: 'dot ok' }), t('logs.live_running')));

  function updateCount() {
    countEl.textContent = tn('logs.count', rows.length) + (cursor && !live ? ' · ' + t('logs.more_available') : '');
  }

  function rowEl(log, isNew) {
    const st = normStatus(log.status);
    const el = h('div', { class: ['log-row', isNew ? 'new' : null, String(log.id) === String(openId) ? 'open' : null], dataset: { id: log.id } },
      h('span', { class: ['sbar', 'status-bar', 'st-' + st] }),
      h('span', { class: 'meta num' }, fmtDateTime(log.ts)),
      h('span', { class: ['meta', 'st-text-' + st] }, statusLabel(st)),
      h('span', { class: 'meta c-host', title: log.hostname || '' }, log.hostname || '-'),
      h('span', { class: 'meta c-svc', title: log.service || '' }, log.service || '-'),
      h('span', { class: 'msg', title: '' }, log.message || ''));
    el.addEventListener('click', () => toggleDetail(log, el));
    return el;
  }

  function toggleDetail(log, el) {
    const next = el.nextElementSibling;
    if (next && next.classList.contains('log-detail')) {
      next.remove(); el.classList.remove('open'); openId = null; return;
    }
    listEl.querySelectorAll('.log-detail').forEach((d) => { if (d.previousElementSibling) d.previousElementSibling.classList.remove('open'); d.remove(); });
    openId = log.id;
    el.classList.add('open');
    el.after(detail(log));
  }

  function detail(log) {
    const st = normStatus(log.status);
    const actions = h('div', { class: 'row wrap mb-2' },
      explainButton(ctx.me, { kind: 'log', log_id: idValue(log.id), ts: toMs(log.ts) }, { subject: (log.service ? log.service + ' · ' : '') + (log.hostname || '') }),
      copyButton(() => log.message || ''),
      h('button', { class: 'btn sm', type: 'button', title: t('logs.context.title'), onclick: () => openContext(ctx, log) }, icon('list', 12), t('logs.context.button')),
      log.service ? h('button', { class: 'btn sm', type: 'button', onclick: () => setQuery(toggleTerm(q, 'service:' + qv(log.service))) }, icon('filter', 12), t('logs.filter_service', { v: log.service })) : null,
      log.hostname ? h('button', { class: 'btn sm', type: 'button', onclick: () => setQuery(toggleTerm(q, 'host:' + qv(log.hostname))) }, icon('filter', 12), t('logs.filter_host', { v: log.hostname })) : null,
      log.host_id ? h('a', { class: 'btn sm', href: href('/infrastructure/host/' + log.host_id) }, icon('server', 12), t('logs.view_host')) : null,
      log.trace_id ? h('a', { class: 'btn sm primary', href: href('/apm/trace/' + encodeURIComponent(log.trace_id)) }, icon('trace', 12), t('logs.view_trace')) : null);
    const method = attrAt(log.attributes, 'http.method');
    const code = attrAt(log.attributes, 'http.status_code');
    const urlPath = attrAt(log.attributes, 'http.url_path');
    const dur = attrAt(log.attributes, 'duration');
    const client = attrAt(log.attributes, 'network.client.ip');
    const httpLine = method || code !== undefined || urlPath ? h('div', { class: 'row wrap mb-2 small' },
      method ? h('button', { class: 'tag', type: 'button', onclick: () => setQuery(toggleTerm(q, '@http.method:' + qv(method))) }, String(method)) : null,
      urlPath ? h('span', { class: 'mono' }, String(urlPath)) : null,
      code !== undefined && code !== null ? h('button', { class: 'tag', type: 'button', style: { color: httpCodeColor(code) }, onclick: () => setQuery(toggleTerm(q, '@http.status_code:' + qv(code))) }, String(code)) : null,
      isNum(dur) ? h('span', { class: 'muted' }, fmtDuration(dur)) : null,
      client ? h('span', { class: 'muted mono' }, String(client)) : null) : null;
    return h('div', { class: 'log-detail' },
      actions,
      httpLine,
      h('pre', log.message || ''),
      h('div', { class: 'grid c2' },
        h('div', kv([
          [t('col.date'), fmtDateTimeFull(log.ts)],
          [t('col.status'), h('span', { class: 'st-text-' + st }, statusLabel(st))],
          [t('col.host'), log.hostname || null],
          [t('col.service'), log.service || null],
          [t('col.source'), log.source || null],
          [t('logs.trace_id'), log.trace_id ? h('a', { class: 'mono small', href: href('/apm/trace/' + encodeURIComponent(log.trace_id)) }, log.trace_id) : null],
          [t('logs.span_id'), log.span_id ? h('span', { class: 'mono small' }, log.span_id) : undefined],
          ['ID', h('span', { class: 'mono small' }, String(log.id))],
        ]), log.tags && Object.keys(log.tags).length ? h('div', { class: 'mt-3' }, h('h4', { class: 'mb-2' }, t('col.tags')), tagList(log.tags)) : null),
        h('div', h('h4', { class: 'mb-2' }, t('logs.attributes')),
          log.attributes && typeof log.attributes === 'object' && Object.keys(log.attributes).length
            ? jsonTree(httpFirst(log.attributes), { onKey: (path, value) => setQuery(toggleTerm(q, '@' + path + ':' + qv(value === null ? 'null' : value))) })
            : h('div', { class: 'muted small' }, t('logs.no_attributes')),
          log.attributes && Object.keys(log.attributes || {}).length ? h('div', { class: 'xsmall muted mt-2' }, t('logs.attr_hint')) : null)));
  }

  function append(list) {
    const frag = document.createDocumentFragment();
    for (const log of list) {
      if (seen.has(String(log.id))) continue;
      seen.add(String(log.id));
      rows.push(log);
      bumpMax(log.id);
      frag.appendChild(rowEl(log, false));
    }
    listEl.appendChild(frag);
  }

  async function loadPage() {
    if (loadingMore) return;
    loadingMore = true;
    clear(moreEl);
    moreEl.appendChild(h('div', { class: 'state sm' }, h('span', { class: 'spinner' })));
    try {
      const resp = await get('/logs/search', { q, from: range.from, to: range.to, limit: PAGE, cursor: cursor || undefined }, { signal });
      const list = resp && Array.isArray(resp.logs) ? resp.logs : asList(resp, 'logs');
      cursor = resp && resp.next_cursor ? resp.next_cursor : null;
      append(list);
      if (!rows.length) listEl.appendChild(empty(t('logs.none_title'), t('logs.none_text')));
      updateCount();
      clear(moreEl);
      if (cursor && !live && rows.length < MAX_ROWS) {
        const btn = h('button', { class: 'btn sm', type: 'button', onclick: () => loadPage() }, t('logs.load_more'));
        const sentinel = h('div', { class: 'tbl-foot', style: { justifyContent: 'center' } }, btn);
        moreEl.appendChild(sentinel);
        observe(sentinel);
      } else if (rows.length >= MAX_ROWS) {
        moreEl.appendChild(h('div', { class: 'tbl-foot' }, t('logs.max_rows', { n: fmtInt(MAX_ROWS) })));
      }
      if (focusId) {
        const el = listEl.querySelector('[data-id="' + CSS.escape(focusId) + '"]');
        const log = rows.find((x) => String(x.id) === focusId);
        if (el && log && !el.classList.contains('open')) { toggleDetail(log, el); el.scrollIntoView({ block: 'center' }); }
      }
    } catch (e) {
      if (isAbort(e)) return;
      clear(moreEl);
      moreEl.appendChild(errorState(e, () => loadPage()));
    } finally {
      loadingMore = false;
    }
  }

  let io = null;
  function observe(el) {
    if (io) io.disconnect();
    io = new IntersectionObserver((entries) => { if (entries.some((e) => e.isIntersecting)) loadPage(); }, { rootMargin: '400px' });
    io.observe(el);
  }
  ctx.onCleanup(() => { if (io) io.disconnect(); });

  if (tab === 'patterns') {
    await renderPatterns(patternsEl, ctx, q, {
      onPick: (pq) => ctx.setParams({ q: pq, tab: null, focus: null }),
      onExclude: canWriteFilters(ctx.me) ? (pq) => openFilterForm(ctx, { query: pq, onSaved: () => toast(t('lf.from_logs_done'), 'ok', 5000) }) : null,
    });
    return;
  }

  await loadPage();

  // Direct : GET /logs/tail toutes les 2 s, nouvelles lignes en tête.
  if (live) {
    let timer = null;
    let busy = false;
    let failures = 0;
    const tick = async () => {
      if (busy || document.visibilityState !== 'visible') return;
      busy = true;
      try {
        const resp = await get('/logs/tail', { q, after_id: maxId === null ? undefined : maxId }, { signal });
        const list = resp && Array.isArray(resp.logs) ? resp.logs : asList(resp, 'logs');
        failures = 0;
        const fresh = list.filter((l) => !seen.has(String(l.id))).sort((a, b) => Number(b.id || 0) - Number(a.id || 0));
        if (fresh.length) {
          const emptyEl = listEl.querySelector('.state');
          if (emptyEl) emptyEl.remove();
          const frag = document.createDocumentFragment();
          for (const log of fresh) {
            seen.add(String(log.id));
            rows.unshift(log);
            bumpMax(log.id);
            frag.appendChild(rowEl(log, true));
          }
          listEl.insertBefore(frag, listEl.firstChild);
          while (rows.length > LIVE_MAX) {
            const old = rows.pop();
            seen.delete(String(old.id));
            const el = listEl.querySelector('[data-id="' + CSS.escape(String(old.id)) + '"]');
            if (el) { const nx = el.nextElementSibling; if (nx && nx.classList.contains('log-detail')) nx.remove(); el.remove(); }
          }
          updateCount();
        }
      } catch (e) {
        if (isAbort(e)) return;
        failures++;
        if (failures === 3) toast(t('logs.live_error'), 'error');
      } finally {
        busy = false;
      }
    };
    timer = setInterval(tick, 2000);
    ctx.onCleanup(() => clearInterval(timer));
  }
}
