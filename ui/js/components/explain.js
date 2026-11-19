// « Expliquer » (§ 10) : bouton, panneau latéral, appel POST /api/v1/explain, rendu Markdown sûr.
import { h, clear, icon } from '../dom.js';
import { t, tn, getLang } from '../i18n.js';
import { post, isAbort } from '../api.js';
import { fmtDuration, isNum } from '../format.js';
import { drawer, copyButton, errorMessage } from './ui.js';
import { confirmAndRun, actionLabel } from './remote-action.js';

const CLIENT_TIMEOUT_MS = 150000; // au-delà du délai serveur (90 s par défaut)

export function explainEnabled(me) {
  return !!(me && me.features && me.features.explain === true);
}

// Identifiant numérique quand c'est sans perte, sinon la chaîne telle quelle.
export function idValue(v) {
  if (typeof v === 'string' && /^\d+$/.test(v)) { const n = Number(v); return Number.isSafeInteger(n) ? n : v; }
  return v;
}

// Bouton « Expliquer ». payload : corps sans « lang » (kind, monitor_id, host_id…). Rien si la fonction est coupée.
export function explainButton(me, payload, opts = {}) {
  if (!explainEnabled(me)) return null;
  const btn = h('button', { class: ['btn', 'sm', 'explain-btn', opts.iconOnly ? 'icon' : null], type: 'button', title: t('explain.button_title') },
    icon('spark', 13), opts.iconOnly ? null : h('span', t('explain.button')));
  btn.addEventListener('click', (e) => {
    e.preventDefault();
    e.stopPropagation();
    openExplain(typeof payload === 'function' ? payload() : payload, { subject: opts.subject, me });
  });
  return btn;
}

function contextSummary(ctx) {
  if (!ctx || typeof ctx !== 'object') return t('explain.ctx.none');
  const parts = [];
  if (ctx.host) parts.push(t('explain.ctx.host', { host: ctx.host }));
  for (const k of ['logs', 'checks', 'events', 'processes', 'spans']) {
    if (isNum(ctx[k]) && ctx[k] > 0) parts.push(tn('explain.ctx.' + k, ctx[k]));
  }
  const metrics = Array.isArray(ctx.metrics) ? ctx.metrics.length : isNum(ctx.metrics) ? ctx.metrics : 0;
  if (metrics) parts.push(tn('explain.ctx.metrics', metrics));
  return parts.length ? parts.join(', ') : t('explain.ctx.none');
}

function errorText(e) {
  if (e && e.code === 'client_timeout') return t('explain.err.timeout');
  if (e && e.status === 429) return t('explain.err.429');
  if (e && e.status === 503) return t('explain.err.503');
  if (e && e.status === 404) return t('explain.err.404');
  if (e && e.status === 400) return t('explain.err.400') + (e.detail ? ' ' + e.detail : '');
  return errorMessage(e);
}

export function openExplain(payload, opts = {}) {
  const body = h('div', { class: 'explain' });
  const content = h('div', { class: 'explain-content' });
  const foot = h('div', { class: 'explain-foot' });
  body.appendChild(content);
  body.appendChild(foot);
  let ctrl = null;
  let timer = null;
  let deadline = null;
  const stop = () => { if (timer) clearInterval(timer); timer = null; if (deadline) clearTimeout(deadline); deadline = null; };
  const d = drawer({
    title: t('explain.title') + (opts.subject ? ' · ' + opts.subject : ''),
    body,
    onClose: () => { stop(); if (ctrl) ctrl.abort(); },
  });

  const closeBtn = () => h('button', { class: 'btn sm', type: 'button', onclick: () => d.close() }, t('action.close'));

  async function run() {
    stop();
    if (ctrl) ctrl.abort();
    ctrl = new AbortController();
    const myCtrl = ctrl;
    const started = Date.now();
    const elapsed = h('div', { class: 'xsmall muted mt-2 num' }, t('explain.elapsed', { s: 0 }));
    clear(content);
    clear(foot);
    content.appendChild(h('div', { class: 'explain-wait', role: 'status', 'aria-live': 'polite' },
      h('span', { class: 'spinner' }),
      h('div', h('b', t('explain.loading')), h('div', { class: 'small text-2 mt-2' }, t('explain.loading_hint')), elapsed)));
    foot.appendChild(h('div', { class: 'row', style: { justifyContent: 'flex-end' } }, closeBtn()));
    // Le panneau peut disparaître sans fermeture explicite (changement de page) : on arrête tout.
    timer = setInterval(() => {
      if (!body.isConnected) { stop(); myCtrl.abort(); return; }
      elapsed.textContent = t('explain.elapsed', { s: Math.round((Date.now() - started) / 1000) });
    }, 1000);
    let timedOut = false;
    deadline = setTimeout(() => { timedOut = true; myCtrl.abort(); }, CLIENT_TIMEOUT_MS);
    try {
      const r = await post('/explain', Object.assign({}, payload, { lang: getLang() }), { signal: myCtrl.signal });
      if (myCtrl !== ctrl || !body.isConnected) return;
      stop();
      showResult(r || {}, Date.now() - started);
    } catch (e) {
      if (myCtrl !== ctrl || !body.isConnected) return;
      stop();
      if (isAbort(e) && !timedOut) return;
      showError(timedOut ? { code: 'client_timeout' } : e);
    }
  }

  function showResult(r, clientMs) {
    const text = typeof r.explanation === 'string' ? r.explanation.trim() : '';
    clear(content);
    clear(foot);
    content.appendChild(text ? renderMarkdown(text) : h('div', { class: 'muted' }, t('explain.empty')));
    const proposed = proposedActions(r, payload, opts.me);
    if (proposed) content.appendChild(proposed);
    content.appendChild(h('p', { class: 'xsmall muted mt-3' }, t('explain.ai_note')));
    content.appendChild(h('p', { class: 'xsmall muted' }, t(opts.me && opts.me.mode === 'selfhosted' ? 'explain.where.selfhosted' : 'explain.where.saas')));
    const meta = h('dl', { class: 'kv explain-meta' },
      h('dt', t('explain.model')), h('dd', h('span', { class: 'mono small' }, r.model || '-')),
      h('dt', t('explain.duration')), h('dd', fmtDuration(isNum(r.duration_ms) ? r.duration_ms : clientMs)),
      h('dt', t('explain.context')), h('dd', contextSummary(r.context)));
    foot.appendChild(meta);
    foot.appendChild(h('div', { class: 'row', style: { justifyContent: 'flex-end', marginTop: '8px' } },
      text ? copyButton(() => text) : null, closeBtn()));
  }

  function showError(e) {
    clear(content);
    clear(foot);
    content.appendChild(h('div', { class: 'callout crit' }, icon('warn', 16), h('div', h('div', errorText(e)),
      e && e.code && e.code !== 'client_timeout' && ![404, 429, 503].includes(e.status) ? h('div', { class: 'xsmall muted mt-2 mono' }, e.code) : null)));
    // Rien à relancer sur 400, 404 ou 429 (limite horaire de l'organisation).
    const canRetry = !(e && [400, 404, 429].includes(e.status));
    foot.appendChild(h('div', { class: 'row', style: { justifyContent: 'flex-end' } },
      canRetry ? h('button', { class: 'btn sm primary', type: 'button', onclick: run }, t('action.retry')) : null, closeBtn()));
  }

  run();
  return d;
}

// Gestes proposés par l'IA (§ 28) : un bouton par geste reconnu, confirmation puis suivi en direct.
function proposedActions(r, payload, me) {
  const list = Array.isArray(r && r.proposed_actions) ? r.proposed_actions.filter((a) => a && a.kind) : [];
  if (!list.length) return null;
  const ctx = (r && r.context) || {};
  const canPropose = ['owner', 'admin', 'member'].includes(me && me.role);
  const box = h('div', { class: 'explain-actions' },
    h('h4', icon('play', 13), t('act.proposed')),
    h('p', { class: 'xsmall muted' }, t('act.proposed_hint')));
  for (const pa of list) {
    const hostname = pa.hostname || pa.host || ctx.host || '';
    const hostId = pa.host_id !== undefined && pa.host_id !== null ? pa.host_id : payload.host_id !== undefined ? payload.host_id : ctx.host_id;
    const label = pa.label || actionLabel(pa, hostname);
    const btn = h('button', { class: 'btn sm act-btn', type: 'button', disabled: !canPropose, title: canPropose ? (pa.reason || label) : t('err.403') },
      icon(pa.kind === 'run_script' ? 'play' : 'refresh', 13), h('span', label));
    btn.addEventListener('click', () => confirmAndRun(me, {
      host_id: hostId, hostname, kind: pa.kind, params: pa.params || {}, reason: pa.reason || '', label, proposed_by_ai: true,
      origin: Object.assign({}, payload.monitor_id !== undefined ? { monitor_id: payload.monitor_id, group: payload.group } : {}, payload.kind ? { explain_kind: payload.kind } : {}),
    }));
    box.appendChild(h('div', { class: 'act-row' }, btn, pa.reason ? h('div', { class: 'xsmall text-2' }, pa.reason) : null));
  }
  return box;
}

// ── Markdown sûr ──────────────────────────────────────────────────────────
// Aucun HTML n'est interprété : chaque morceau devient un nœud texte. Pris en charge : titres,
// paragraphes, gras, italique (*x*), `code`, blocs ```, listes (puces, numérotées, un niveau
// d'imbrication), citations, filets, liens http(s), tableaux.

function inline(text) {
  const out = [];
  const re = /(`+)([^`]+?)\1|\*\*([^*]+?)\*\*|__([^_]+?)__|\*([^*\s][^*]*?)\*|\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/g;
  let last = 0;
  let m;
  while ((m = re.exec(text))) {
    if (m.index > last) out.push(text.slice(last, m.index));
    if (m[2] !== undefined) out.push(h('code', m[2]));
    else if (m[3] !== undefined || m[4] !== undefined) out.push(h('strong', inline(m[3] !== undefined ? m[3] : m[4])));
    else if (m[5] !== undefined) out.push(h('em', inline(m[5])));
    else if (m[6] !== undefined) out.push(h('a', { href: m[7], target: '_blank', rel: 'noopener noreferrer nofollow' }, m[6]));
    last = re.lastIndex;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

export function renderMarkdown(src) {
  const root = h('div', { class: 'md' });
  // Raisonnement éventuel du modèle : retiré, jamais affiché.
  const text = String(src || '').replace(/<think>[\s\S]*?<\/think>/gi, '').replace(/\r\n?/g, '\n');
  const lines = text.split('\n');
  let i = 0;
  let para = [];
  const flushPara = () => {
    if (!para.length) return;
    root.appendChild(h('p', inline(para.join(' '))));
    para = [];
  };
  const listRe = /^(\s*)([-*+]|\d+[.)])\s+(.*)$/;
  while (i < lines.length) {
    const line = lines[i];
    const fence = /^\s*(```|~~~)\s*([\w+-]*)\s*$/.exec(line);
    if (fence) {
      flushPara();
      const code = [];
      i++;
      while (i < lines.length && !new RegExp('^\\s*' + fence[1] + '\\s*$').test(lines[i])) { code.push(lines[i]); i++; }
      i++;
      const str = code.join('\n');
      root.appendChild(h('div', { class: 'md-pre' }, h('pre', h('code', { 'data-lang': fence[2] || null }, str)), copyButton(() => str, { label: false })));
      continue;
    }
    if (!line.trim()) { flushPara(); i++; continue; }
    // Tableau (post-mortem : actions, responsable, échéance) : en-tête, filet |---|, lignes.
    if (line.includes('|') && i + 1 < lines.length && /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)+\|?\s*$/.test(lines[i + 1])) {
      flushPara();
      const cells = (l) => {
        let s2 = l.trim();
        if (s2.startsWith('|')) s2 = s2.slice(1);
        if (s2.endsWith('|')) s2 = s2.slice(0, -1);
        return s2.split('|').map((c) => c.trim());
      };
      const hdr = cells(line);
      const aligns = cells(lines[i + 1]).map((c) => (/^:-+:$/.test(c) ? 'center' : /-+:$/.test(c) ? 'right' : null));
      i += 2;
      const rows = [];
      while (i < lines.length && lines[i].includes('|') && lines[i].trim()) { rows.push(cells(lines[i])); i++; }
      const cellStyle = (k) => (aligns[k] ? { textAlign: aligns[k] } : null);
      root.appendChild(h('div', { class: 'md-table-wrap' }, h('table', { class: 'md-table' },
        h('thead', h('tr', hdr.map((c, k) => h('th', { style: cellStyle(k) }, inline(c))))),
        h('tbody', rows.map((row) => h('tr', hdr.map((_, k) => h('td', { style: cellStyle(k) }, inline(row[k] || '')))))))));
      continue;
    }
    const head =/^\s*(#{1,6})\s+(.*?)\s*#*\s*$/.exec(line);
    if (head) {
      flushPara();
      root.appendChild(h(head[1].length <= 2 ? 'h3' : 'h4', inline(head[2])));
      i++;
      continue;
    }
    if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(line)) { flushPara(); root.appendChild(h('hr')); i++; continue; }
    if (/^\s*>/.test(line)) {
      flushPara();
      const quote = [];
      while (i < lines.length && /^\s*>/.test(lines[i])) { quote.push(lines[i].replace(/^\s*>\s?/, '')); i++; }
      root.appendChild(h('blockquote', inline(quote.join(' '))));
      continue;
    }
    if (listRe.test(line)) {
      flushPara();
      const first = listRe.exec(line);
      const baseIndent = first[1].length;
      const ordered = /\d/.test(first[2]);
      const list = h(ordered ? 'ol' : 'ul');
      if (ordered && parseInt(first[2], 10) > 1) list.setAttribute('start', String(parseInt(first[2], 10)));
      let lastLi = null;
      let sub = null;
      while (i < lines.length) {
        const l = lines[i];
        const m = listRe.exec(l);
        if (m && m[1].length <= baseIndent + 1) {
          lastLi = h('li', inline(m[3]));
          list.appendChild(lastLi);
          sub = null;
          i++;
        } else if (m && lastLi) {
          if (!sub) { sub = h(/\d/.test(m[2]) ? 'ol' : 'ul'); lastLi.appendChild(sub); }
          sub.appendChild(h('li', inline(m[3])));
          i++;
        } else if (l.trim() && /^\s{2,}\S/.test(l) && lastLi && !/^\s*(```|~~~)/.test(l)) {
          lastLi.appendChild(document.createTextNode(' '));
          inline(l.trim()).forEach((n) => lastLi.appendChild(typeof n === 'string' ? document.createTextNode(n) : n));
          i++;
        } else break;
      }
      root.appendChild(list);
      continue;
    }
    para.push(line.trim());
    i++;
  }
  flushPara();
  return root;
}
