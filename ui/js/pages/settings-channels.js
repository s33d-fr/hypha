// Réglages › Canaux de notification (§§ 7, 18) : un formulaire par type, « Envoyer un essai ».
import { h, clear, icon } from '../dom.js';
import { t } from '../i18n.js';
import { get, post, put, del, asList } from '../api.js';
import { fmtRel, toMs } from '../format.js';
import { section, empty, panel, modal, field, select, toast, confirmDialog, errorMessage } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { isAdmin } from './ops-shared.js';

export const KINDS = ['email', 'webhook', 'slack', 'teams', 'mattermost', 'discord', 'googlechat', 'telegram', 'ntfy', 'pagerduty', 'opsgenie'];
const URL_KINDS = ['webhook', 'slack', 'teams', 'mattermost', 'discord', 'googlechat'];

// Champs propres à chaque type (§ 18). secret : saisi masqué, jamais affiché dans la liste.
const FIELDS = {
  telegram: [
    { key: 'bot_token', secret: true, required: true, placeholder: '123456789:AA…', check: (v) => /^\d{5,}:[\w-]{20,}$/.test(v) },
    { key: 'chat_id', required: true, placeholder: '-1001234567890', check: (v) => /^(-?\d{3,}|@[\w]{4,})$/.test(v) },
  ],
  ntfy: [
    { key: 'url', required: true, placeholder: 'https://ntfy.sh', def: 'https://ntfy.sh', check: (v) => /^https:\/\//i.test(v) },
    { key: 'topic', required: true, placeholder: 'hypha-alertes', check: (v) => /^[\w-]{1,64}$/.test(v) },
    { key: 'token', secret: true, required: false, placeholder: 'tk_…' },
  ],
  pagerduty: [
    { key: 'routing_key', secret: true, required: true, placeholder: 'R0123456789ABCDEF0123456789ABCDE', check: (v) => /^[A-Za-z0-9]{32}$/.test(v) },
  ],
  opsgenie: [
    { key: 'api_key', secret: true, required: true, placeholder: 'xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx', check: (v) => v.length >= 20 },
    { key: 'region', required: true, options: ['eu', 'us'], def: 'eu' },
  ],
};

function emptyConfig(kind) {
  if (kind === 'email') return { to: [] };
  if (URL_KINDS.includes(kind)) return kind === 'webhook' ? { url: '', headers: {} } : { url: '' };
  const cfg = {};
  for (const f of FIELDS[kind] || []) cfg[f.key] = f.def !== undefined ? f.def : '';
  return cfg;
}

export function channelTarget(c) {
  const cfg = c.config || {};
  const tail = (v) => (v ? '…' + String(v).slice(-4) : '');
  switch (c.kind) {
    case 'email': return (Array.isArray(cfg.to) ? cfg.to : [cfg.to]).filter(Boolean).join(', ');
    case 'telegram': return cfg.chat_id ? t('channels.target.telegram', { chat: cfg.chat_id }) : '-';
    case 'ntfy': { let host = ''; try { host = new URL(cfg.url || 'https://ntfy.sh').host; } catch (e) { host = cfg.url || ''; } return host + (cfg.topic ? '/' + cfg.topic : ''); }
    case 'pagerduty': return t('channels.target.pagerduty', { k: tail(cfg.routing_key) });
    case 'opsgenie': return t('channels.target.opsgenie', { r: String(cfg.region || 'eu').toUpperCase() });
    default: {
      const url = cfg.url || '';
      try { const u = new URL(url); return u.host + (u.pathname.length > 1 ? '/…' : ''); } catch (e) { return url ? url.slice(0, 40) : '-'; }
    }
  }
}

function secretInput(value, placeholder, onInput) {
  const inp = h('input', { class: 'input mono', type: 'password', value: value || '', placeholder: placeholder || '', autocomplete: 'off', spellcheck: 'false' });
  inp.addEventListener('input', () => onInput(inp.value.trim()));
  const eye = h('button', { class: 'btn icon', type: 'button', title: t('channels.show_secret'), 'aria-label': t('channels.show_secret'), onclick: () => { inp.type = inp.type === 'password' ? 'text' : 'password'; } }, icon('eye', 14));
  return h('div', { class: 'row' }, inp, eye);
}

async function sendTest(id) {
  const r = await post('/channels/' + encodeURIComponent(id) + '/test', {});
  return { ok: !!(r && r.ok), detail: (r && r.detail) || '' };
}

function testResultBox(res) {
  return h('div', { class: ['callout', res.ok ? null : 'crit'] }, icon(res.ok ? 'check' : 'warn', 16),
    h('div', { class: 'small' }, h('b', t(res.ok ? 'channels.test_ok' : 'channels.test_ko')), res.detail ? h('div', { class: 'mono xsmall mt-2' }, res.detail) : null));
}

function channelDialog(ctx, existing) {
  let saved = existing ? JSON.parse(JSON.stringify(existing)) : null;
  let changed = false;
  const c = existing ? JSON.parse(JSON.stringify(existing)) : { kind: 'email', name: '', config: emptyConfig('email'), enabled: true };
  c.config = c.config && typeof c.config === 'object' ? c.config : emptyConfig(c.kind);
  const box = h('div', { class: 'col gap-3' });
  const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
  const testOut = h('div');
  const dirty = () => !saved || JSON.stringify({ k: c.kind, n: c.name, c: c.config, e: c.enabled }) !== JSON.stringify({ k: saved.kind, n: saved.name, c: saved.config, e: saved.enabled });

  function draw() {
    clear(box);
    const name = h('input', { class: 'input', value: c.name || '' });
    name.addEventListener('input', () => { c.name = name.value; drawButtons(); });
    box.appendChild(field(t('channels.f.kind'), select(KINDS.map((k) => ({ value: k, label: t('channels.kind.' + k) })), c.kind, (v) => { c.kind = v; c.config = emptyConfig(v); draw(); drawButtons(); }, { disabled: !!saved })));
    box.appendChild(h('div', { class: 'xsmall muted', style: { marginTop: '-6px' } }, t('channels.kind_desc.' + c.kind)));
    box.appendChild(field(t('channels.f.name'), name));
    if (c.kind === 'email') {
      const to = h('textarea', { class: 'input mono', rows: 3, placeholder: 'ops@example.fr' });
      to.value = (c.config.to || []).join('\n');
      to.addEventListener('input', () => { c.config.to = to.value.split(/[\n,;]+/).map((x) => x.trim()).filter(Boolean); drawButtons(); });
      box.appendChild(field(t('channels.f.to'), to, t('channels.f.to_hint')));
    } else if (URL_KINDS.includes(c.kind)) {
      box.appendChild(field(t('channels.f.url'), secretInput(c.config.url, 'https://', (v) => { c.config.url = v; drawButtons(); }), t('channels.f.url_hint.' + c.kind)));
      if (c.kind === 'webhook') {
        const hd = h('textarea', { class: 'input mono', rows: 3, placeholder: 'Authorization: Bearer …' });
        hd.value = Object.entries(c.config.headers || {}).map(([k, v]) => k + ': ' + v).join('\n');
        hd.addEventListener('input', () => {
          const o = {};
          hd.value.split('\n').forEach((l) => { const i = l.indexOf(':'); if (i > 0) o[l.slice(0, i).trim()] = l.slice(i + 1).trim(); });
          c.config.headers = o;
          drawButtons();
        });
        box.appendChild(field(t('channels.f.headers'), hd, t('channels.f.payload')));
      }
    } else {
      for (const f of FIELDS[c.kind] || []) {
        let ctl;
        if (f.options) ctl = select(f.options.map((o) => ({ value: o, label: t('channels.opt.' + f.key + '.' + o) })), c.config[f.key] || f.def, (v) => { c.config[f.key] = v; drawButtons(); });
        else if (f.secret) ctl = secretInput(c.config[f.key], f.placeholder, (v) => { c.config[f.key] = v; drawButtons(); });
        else {
          ctl = h('input', { class: 'input mono', value: c.config[f.key] || '', placeholder: f.placeholder || '', spellcheck: 'false' });
          ctl.addEventListener('input', () => { c.config[f.key] = ctl.value.trim(); drawButtons(); });
        }
        box.appendChild(field(t('channels.f.' + c.kind + '.' + f.key) + (f.required ? '' : ' (' + t('channels.optional') + ')'), ctl, t('channels.h.' + c.kind + '.' + f.key)));
      }
    }
    const en = h('input', { type: 'checkbox', checked: c.enabled !== false });
    en.addEventListener('change', () => { c.enabled = en.checked; drawButtons(); });
    box.appendChild(h('label', { class: 'check' }, en, t('channels.f.enabled')));
    box.appendChild(h('div', { class: 'xsmall muted' }, t('channels.retry_note')));
    box.appendChild(err);
    box.appendChild(testOut);
  }

  function validate() {
    if (!String(c.name || '').trim()) return t('form.required', { field: t('channels.f.name') });
    if (c.kind === 'email') {
      if (!(c.config.to || []).length) return t('form.required', { field: t('channels.f.to') });
      const bad = c.config.to.find((x) => !/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(x));
      if (bad) return t('channels.err.email', { v: bad });
      return null;
    }
    if (URL_KINDS.includes(c.kind)) return /^https:\/\//i.test(c.config.url || '') ? null : t('channels.err.https');
    for (const f of FIELDS[c.kind] || []) {
      const v = String(c.config[f.key] || '');
      if (f.required && !v) return t('form.required', { field: t('channels.f.' + c.kind + '.' + f.key) });
      if (v && f.check && !f.check(v)) return t('channels.err.format', { field: t('channels.f.' + c.kind + '.' + f.key) });
    }
    return null;
  }

  const saveBtn = h('button', { class: 'btn primary', type: 'button' });
  const testBtn = h('button', { class: 'btn', type: 'button' }, icon('send', 13));
  const testLabel = h('span');
  testBtn.appendChild(testLabel);
  function drawButtons() {
    saveBtn.textContent = saved ? t('action.save') : t('channels.create');
    saveBtn.disabled = !!saved && !dirty();
    testLabel.textContent = !saved || dirty() ? t('channels.save_and_test') : t('channels.test');
  }

  async function save() {
    err.hidden = true;
    const msg = validate();
    if (msg) { err.textContent = msg; err.hidden = false; return false; }
    const body = { kind: c.kind, name: c.name.trim(), config: c.config, enabled: c.enabled !== false };
    const r = saved ? await put('/channels/' + saved.id, body) : await post('/channels', body);
    const obj = r && r.channel ? r.channel : r;
    saved = Object.assign({}, saved || {}, body, obj && typeof obj === 'object' ? obj : {});
    if (!saved.id) throw new Error(t('err.unknown'));
    saved.config = JSON.parse(JSON.stringify(c.config));
    changed = true;
    return true;
  }

  saveBtn.addEventListener('click', async () => {
    saveBtn.disabled = true;
    try {
      const was = !!existing;
      if (await save()) { toast(was ? t('channels.saved') : t('channels.created'), 'ok'); m.close(); }
    } catch (e) { err.textContent = errorMessage(e); err.hidden = false; }
    drawButtons();
  });
  testBtn.addEventListener('click', async () => {
    testBtn.disabled = true;
    clear(testOut);
    testOut.appendChild(h('div', { class: 'row small text-2' }, h('span', { class: 'spinner' }), t('channels.testing')));
    try {
      if (!saved || dirty()) { if (!(await save())) { clear(testOut); testBtn.disabled = false; return; } }
      const res = await sendTest(saved.id);
      clear(testOut);
      testOut.appendChild(testResultBox(res));
    } catch (e) {
      clear(testOut);
      testOut.appendChild(testResultBox({ ok: false, detail: errorMessage(e) + (e && e.detail && e.detail !== errorMessage(e) ? ' : ' + e.detail : '') }));
    }
    testBtn.disabled = false;
    drawButtons();
  });

  draw();
  drawButtons();
  const m = modal({
    title: existing ? t('channels.edit') : t('channels.new'), body: box,
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.close')), testBtn, saveBtn],
    onClose: () => { if (changed) ctx.refresh(); },
  });
}

export async function renderChannels(root, ctx) {
  const listEl = h('div');
  const admin = isAdmin(ctx.me);
  root.appendChild(h('div', { class: 'querybar' }, h('span', { class: 'muted small grow' }, t('channels.intro')),
    admin ? h('button', { class: 'btn primary', type: 'button', onclick: () => channelDialog(ctx, null) }, icon('plus', 14), t('channels.new')) : null));
  if (!admin) root.appendChild(h('div', { class: 'callout mb-3' }, icon('lock', 16), h('span', { class: 'small' }, t('channels.admin_only'))));
  root.appendChild(panel(t('nav.settings.channels'), listEl, { flush: true }));
  await section(listEl, async () => asList(await get('/channels', null, { signal: ctx.signal }), 'channels'), (list) => {
    if (!list.length) return empty(t('channels.none_title'), t('channels.none_text'), admin ? h('button', { class: 'btn primary', type: 'button', onclick: () => channelDialog(ctx, null) }, icon('plus', 14), t('channels.new')) : null);
    return dataTable({
      columns: [
        { key: 'enabled', label: t('col.status'), value: (c) => (c.enabled === false ? 0 : 1), render: (c) => (c.enabled === false ? h('span', { class: 'badge muted' }, t('channels.disabled')) : h('span', { class: 'badge ok' }, t('channels.enabled'))) },
        { key: 'name', label: t('col.name') },
        { key: 'id', label: t('channels.mention'), render: (c) => h('span', { class: 'mono xsmall muted', title: t('channels.mention_title') }, '@canal-' + c.id) },
        { key: 'kind', label: t('col.type'), render: (c) => t('channels.kind.' + c.kind) },
        { key: 'target', label: t('channels.target'), value: channelTarget, render: (c) => h('span', { class: 'mono small' }, channelTarget(c)) },
        { key: 'created_at', label: t('col.created'), align: 'r', value: (c) => toMs(c.created_at), render: (c) => fmtRel(c.created_at) },
        { key: 'actions', label: '', sort: false, render: (c) => (admin ? h('span', { class: 'row', style: { gap: '2px', justifyContent: 'flex-end' } },
          h('button', { class: 'btn ghost sm', type: 'button', title: t('channels.test'), onclick: async (e) => {
            const b = e.currentTarget;
            b.disabled = true;
            try { const res = await sendTest(c.id); toast((res.ok ? t('channels.test_ok') : t('channels.test_ko')) + (res.detail ? ' · ' + res.detail : ''), res.ok ? 'ok' : 'error', 6000); } catch (err2) { toast(errorMessage(err2), 'error'); }
            b.disabled = false;
          } }, icon('send', 13), t('channels.test')),
          h('button', { class: 'btn ghost sm icon', type: 'button', title: t('action.edit'), onclick: () => channelDialog(ctx, c) }, icon('edit', 13)),
          h('button', { class: 'btn ghost sm icon danger', type: 'button', title: t('action.delete'), onclick: async () => {
            if (!(await confirmDialog({ title: t('channels.delete_title'), text: t('channels.delete_text', { name: c.name || '' }), confirmLabel: t('action.delete'), danger: true }))) return;
            try { await del('/channels/' + c.id); toast(t('channels.deleted'), 'ok'); ctx.refresh(); } catch (e) { toast(errorMessage(e), 'error'); }
          } }, icon('trash', 13))) : null) },
      ],
      rows: list, sortKey: 'name', sortDir: 'asc', onRowClick: admin ? (c) => channelDialog(ctx, c) : null,
    });
  });
}
