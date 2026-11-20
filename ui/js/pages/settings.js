// Réglages : clés API, installation de l'agent, membres, canaux de notification, journaux, pages de
// statut, rapports, audit, actions à distance, profil.
import { h, clear, icon } from '../dom.js';
import { t, tn, getLang, setLang, languages } from '../i18n.js';
import { get, post, put, del, asList, isAbort } from '../api.js';
import { fmtRel, fmtDateTime, toMs } from '../format.js';
import { section, empty, panel, modal, field, select, toast, confirmDialog, errorMessage, codeBlock, copyButton, tabs, kv, statusDot } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { getTheme, setTheme } from '../app.js';
import { renderLogFilters } from './log-filters.js';
import { renderChannels } from './settings-channels.js';

const VIEWS = ['keys', 'install', 'members', 'channels', 'logs', 'devices', 'fleet', 'billing', 'usage', 'status-pages', 'reports', 'audit', 'actions', 'profile'];
const ROLES = ['owner', 'admin', 'member', 'viewer'];

export async function render(root, ctx) {
  const view = VIEWS.includes(ctx.route.view) ? ctx.route.view : 'keys';
  ctx.setCrumbs([{ label: t('nav.settings'), href: ctx.href('/settings/keys') }, { label: t('nav.settings.' + view) }]);
  const shown = VIEWS.filter((k) => k !== 'billing' || (ctx.me && ctx.me.mode === 'saas'));
  root.appendChild(h('div', { class: 'mb-3' }, tabs(shown.map((k) => ({ key: k, label: t('nav.settings.' + k), href: ctx.href('/settings/' + k) })), view)));
  const body = h('div');
  root.appendChild(body);
  if (view === 'keys') return renderKeys(body, ctx);
  if (view === 'install') return renderInstall(body, ctx);
  if (view === 'members') return renderMembers(body, ctx);
  if (view === 'channels') return renderChannels(body, ctx);
  // Données 0.4.0 (§§ 14, 23, 27, 30) : journaux (filtres, métriques, masquage), équipements, flotte, consommation.
  if (view === 'logs') return (await import('./log-settings.js')).renderLogSettings(body, ctx);
  if (view === 'devices') return (await import('./settings-data.js')).renderDevicesSettings(body, ctx);
  if (view === 'fleet') return (await import('./fleet.js')).renderFleet(body, ctx);
  // Formule et facturation (§ 52).
  if (view === 'billing') return (await import('./settings-billing.js')).renderBilling(body, ctx);
  if (view === 'usage') return (await import('./settings-data.js')).renderUsage(body, ctx);
  // Exploitation 0.4.0 (§§ 21, 22, 24, 28) : modules chargés à la demande.
  if (view === 'status-pages') return (await import('./settings-status.js')).renderStatusPages(body, ctx);
  if (view === 'reports') return (await import('./settings-reports.js')).renderReports(body, ctx);
  if (view === 'audit') return (await import('./settings-audit.js')).renderAudit(body, ctx);
  if (view === 'actions') return (await import('./settings-actions.js')).renderActions(body, ctx);
  return renderProfile(body, ctx);
}

// ── Clés API ──────────────────────────────────────────────────────────────
// La clé n'est montrée qu'une fois : rien ne doit fermer cette fenêtre avant l'utilisateur.
export function showSecret(key, title, onClose) {
  const m = modal({
    title: title || t('keys.created_title'), dismissable: false, onClose,
    body: h('div', { class: 'col gap-3' },
      h('div', { class: 'callout warn' }, icon('warn', 16), h('span', t('keys.once'))),
      h('div', { class: 'secret' }, key),
      h('div', copyButton(key))),
    actions: [h('button', { class: 'btn primary', type: 'button', onclick: () => m.close() }, t('keys.saved_it'))],
  });
  return m;
}

export async function createKey(name, kind) {
  const r = await post('/keys', { name, kind });
  const key = r && (r.key || r.api_key || r.secret || (r.data && r.data.key));
  return { resp: r, key };
}

function createKeyDialog(onDone) {
  const name = h('input', { class: 'input', maxlength: 120, placeholder: t('keys.name_ph') });
  const kind = select([{ value: 'ingest', label: t('keys.kind.ingest') }, { value: 'app', label: t('keys.kind.app') }], 'ingest');
  const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
  const btn = h('button', { class: 'btn primary', type: 'button' }, t('keys.create'));
  const m = modal({
    title: t('keys.new'),
    body: h('div', { class: 'col gap-3' }, field(t('keys.f.name'), name), field(t('keys.f.kind'), kind, t('keys.kind_hint')), err),
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), btn],
  });
  btn.addEventListener('click', async () => {
    if (!name.value.trim()) { err.textContent = t('form.required', { field: t('keys.f.name') }); err.hidden = false; return; }
    btn.disabled = true;
    try {
      const { key } = await createKey(name.value.trim(), kind.value);
      m.close();
      if (key) showSecret(key, null, onDone);
      else { toast(t('keys.created_no_secret'), 'info'); onDone(); }
    } catch (e) { btn.disabled = false; err.textContent = errorMessage(e); err.hidden = false; }
  });
}

async function renderKeys(root, ctx) {
  const listEl = h('div');
  root.appendChild(h('div', { class: 'querybar' }, h('span', { class: 'muted small grow' }, t('keys.intro')),
    h('button', { class: 'btn primary', type: 'button', onclick: () => createKeyDialog(() => ctx.refresh()) }, icon('plus', 14), t('keys.new'))));
  root.appendChild(panel(t('nav.settings.keys'), listEl, { flush: true }));
  await section(listEl, async () => asList(await get('/keys', null, { signal: ctx.signal }), 'keys'), (list) => dataTable({
    columns: [
      { key: 'status', label: t('col.status'), value: (k) => (k.revoked_at ? 0 : 1), render: (k) => (k.revoked_at ? h('span', { class: 'badge muted' }, t('keys.revoked')) : h('span', { class: 'badge ok' }, t('keys.active'))) },
      { key: 'name', label: t('col.name') },
      { key: 'kind', label: t('col.type'), render: (k) => t('keys.kind.' + (k.kind || 'ingest')) },
      { key: 'prefix', label: t('keys.prefix'), render: (k) => h('span', { class: 'mono small' }, (k.prefix || '') + '\u2026') },
      { key: 'created_at', label: t('col.created'), align: 'r', value: (k) => toMs(k.created_at), render: (k) => h('span', { title: fmtDateTime(k.created_at) }, fmtRel(k.created_at)) },
      { key: 'last_used_at', label: t('keys.last_used'), align: 'r', value: (k) => toMs(k.last_used_at), render: (k) => (k.last_used_at ? h('span', { title: fmtDateTime(k.last_used_at) }, fmtRel(k.last_used_at)) : h('span', { class: 'muted' }, t('keys.never'))) },
      { key: 'actions', label: '', sort: false, render: (k) => (k.revoked_at ? null : h('button', { class: 'btn sm danger', type: 'button', onclick: async () => {
        if (!(await confirmDialog({ title: t('keys.revoke_title'), text: t('keys.revoke_text', { name: k.name || '' }), confirmLabel: t('keys.revoke'), danger: true }))) return;
        try { await del('/keys/' + k.id); toast(t('keys.revoked_done'), 'ok'); ctx.refresh(); } catch (e) { toast(errorMessage(e), 'error'); }
      } }, t('keys.revoke'))) },
    ],
    rows: list, sortKey: 'created_at', sortDir: 'desc', emptyText: t('keys.none'),
  }));
}

// ── Installation de l'agent ───────────────────────────────────────────────
async function renderInstall(root, ctx) {
  const { signal } = ctx;
  const cmdEl = h('div');
  const agentsEl = h('div');
  root.appendChild(h('div', { class: 'split' },
    h('div', { class: 'split-v' },
      panel(t('install.title'), cmdEl),
      panel(t('install.agents'), agentsEl, { flush: true, sub: t('install.agents_sub') })),
    h('div', { class: 'split-v' },
      panel(t('install.after'), h('div', { class: 'col gap-3 small text-2' },
        h('div', h('b', t('install.check')), codeBlock('sudo hypha-agent status\nsudo hypha-agent check-config')),
        h('div', h('b', t('install.conf')), kv([['Linux', h('span', { class: 'mono' }, '/etc/hypha/hypha.yaml')], ['Windows', h('span', { class: 'mono' }, 'C:\\ProgramData\\Hypha\\hypha.yaml')]])),
        h('div', h('b', t('install.logs_conf')), codeBlock('logs:\n  enabled: true\n  journald: true\n  files:\n    - {path: /var/log/nginx/*.log, service: nginx, source: nginx}')),
        h('div', h('b', t('install.statsd')), h('div', t('install.statsd_text')), codeBlock('echo "app.orders:1|c|#env:prod" | nc -u -w0 127.0.0.1 8125')),
        h('div', h('b', t('install.otlp')), h('div', t('install.otlp_text'))),
        h('div', h('b', t('install.footprint')), h('div', t('install.footprint_text'))))))));

  let data = null;
  let generatedKey = null;
  let os = ctx.params.get('os') || 'linux';

  function withKey(cmd) {
    if (!cmd || !generatedKey) return cmd || '';
    return String(cmd).replace(/<HYPHA_API_KEY>|hph_(?:\u2026|\.\.\.|x+|X+|<[^>]*>|VOTRE_CLE|YOUR_KEY)/g, generatedKey);
  }

  function drawCommands() {
    clear(cmdEl);
    if (!data) return;
    cmdEl.appendChild(h('div', { class: 'row mb-3' }, h('span', { class: 'small muted' }, t('install.version', { v: data.version || '-' })), h('span', { class: 'spacer' }),
      data.url ? h('span', { class: 'small muted mono' }, data.url) : null));
    cmdEl.appendChild(tabs(['linux', 'windows', 'docker'].map((k) => ({ key: k, label: t('install.os.' + k) })), os, (k) => { os = k; ctx.setParams({ os: k }, { silent: true }); drawCommands(); }));
    const cmd = withKey(data[os]);
    const box = h('div', { class: 'col gap-3 mt-3' });
    box.appendChild(h('div', { class: 'small text-2' }, t('install.hint.' + os)));
    box.appendChild(cmd ? codeBlock(cmd) : h('div', { class: 'muted small' }, t('install.no_cmd')));
    const needsKey = /<HYPHA_API_KEY>|hph_(?:\u2026|\.\.\.|x+|X+|<[^>]*>|VOTRE_CLE|YOUR_KEY)/.test(String(data[os] || ''));
    if (needsKey && !generatedKey) {
      const btn = h('button', { class: 'btn primary sm', type: 'button' }, icon('key', 13), t('install.gen_key'));
      btn.addEventListener('click', async () => {
        btn.disabled = true;
        try {
          const { key } = await createKey(t('install.key_name', { d: new Date().toLocaleDateString() }), 'ingest');
          if (!key) { toast(t('keys.created_no_secret'), 'info'); btn.disabled = false; return; }
          generatedKey = key;
          showSecret(key, t('install.key_created'));
          drawCommands();
        } catch (e) { btn.disabled = false; toast(errorMessage(e), 'error'); }
      });
      box.appendChild(h('div', { class: 'callout' }, icon('key', 16), h('div', { class: 'small' }, h('div', { class: 'mb-2' }, t('install.key_needed')), btn)));
    } else if (generatedKey) {
      box.appendChild(h('div', { class: 'callout warn' }, icon('warn', 16), h('span', { class: 'small' }, t('install.key_inserted'))));
    } else {
      box.appendChild(h('div', { class: 'small muted' }, t('install.key_included')));
    }
    if (os === 'linux') box.appendChild(h('div', { class: 'small muted' }, t('install.linux_root')));
    cmdEl.appendChild(box);
  }

  await section(cmdEl, () => get('/install', null, { signal }), (d) => { data = d && typeof d === 'object' ? d : {}; drawCommands(); return null; });

  const drawAgents = (list) => dataTable({
    columns: [
      { key: 'status', label: '', width: '24px', value: (a) => agentStatus(a), render: (a) => statusDot(agentStatus(a)) },
      { key: 'hostname', label: t('col.host') },
      { key: 'version', label: t('col.agent') },
      { key: 'os', label: t('col.os'), render: (a) => [a.os, a.arch].filter(Boolean).join(' / ') || '-' },
      { key: 'last_seen', label: t('col.last_seen'), align: 'r', value: (a) => toMs(a.last_seen), render: (a) => h('span', { title: fmtDateTime(a.last_seen) }, fmtRel(a.last_seen)) },
      { key: 'collectors', label: t('install.collectors'), sort: false, render: (a) => collectorsCell(a.collectors && typeof a.collectors === 'object' ? a.collectors : a.status) },
    ],
    rows: list, sortKey: 'last_seen', sortDir: 'desc', emptyText: t('install.no_agents'),
  });
  await section(agentsEl, async () => asList(await get('/agents', null, { signal }), 'agents'), drawAgents);
  // Nouvel agent : la liste se met à jour toutes les 15 s tant que la page est ouverte.
  const timer = setInterval(async () => {
    if (document.visibilityState !== 'visible') return;
    try {
      const list = asList(await get('/agents', null, { signal }), 'agents');
      clear(agentsEl);
      agentsEl.appendChild(drawAgents(list));
    } catch (e) { if (!isAbort(e)) { /* nouvelle tentative au prochain tour */ } }
  }, 15000);
  ctx.onCleanup(() => clearInterval(timer));
}

function agentStatus(a) {
  if (typeof a.status === 'string') return a.status === 'up' || a.status === 'down' ? a.status : 'unknown';
  const ms = toMs(a.last_seen);
  if (ms === null) return 'unknown';
  return Date.now() - ms < 2 * 60 * 1000 ? 'up' : 'down';
}

function collectorsCell(status) {
  const c = status && typeof status === 'object' && status.collectors && typeof status.collectors === 'object' ? status.collectors : null;
  if (!c) return h('span', { class: 'muted' }, '-');
  const errs = Array.isArray(status.errors) ? status.errors.length : 0;
  return h('div', { class: 'tags' }, Object.entries(c).slice(0, 8).map(([k, v]) => h('span', { class: ['badge', v === 'ok' ? 'ok' : v === 'absent' || v === 'disabled' ? 'muted' : 'warn'], title: k + ' : ' + v }, k)),
    errs ? h('span', { class: 'badge alert', title: status.errors.slice(0, 5).join('\n') }, tn('install.errors', errs)) : null);
}

// ── Membres ───────────────────────────────────────────────────────────────
async function renderMembers(root, ctx) {
  const listEl = h('div');
  const canInvite = ['owner', 'admin'].includes(ctx.me.role);
  root.appendChild(h('div', { class: 'querybar' }, h('span', { class: 'muted small grow' }, t('members.intro')),
    canInvite ? h('button', { class: 'btn primary', type: 'button', onclick: () => inviteDialog(ctx) }, icon('plus', 14), t('members.invite')) : null));
  root.appendChild(panel(t('nav.settings.members'), listEl, { flush: true }));
  await section(listEl, async () => asList(await get('/org/members', null, { signal: ctx.signal }), 'members'), (list) => dataTable({
    columns: [
      { key: 'name', label: t('col.name'), value: (u) => u.name || u.email, render: (u) => h('span', { class: 'row' }, h('span', { class: 'user-avatar' }, String(u.name || u.email || '?').slice(0, 1).toUpperCase()), u.name || '-') },
      { key: 'email', label: t('members.email') },
      { key: 'role', label: t('members.role'), value: (u) => ROLES.indexOf(u.role), render: (u) => h('span', { class: ['badge', u.role === 'owner' ? 'info' : 'muted'] }, t('role.' + (u.role || 'member'))) },
      { key: 'created_at', label: t('members.since'), align: 'r', value: (u) => toMs(u.created_at), render: (u) => fmtRel(u.created_at) },
    ],
    rows: list, sortKey: 'role', sortDir: 'asc', emptyText: t('members.none'),
  }));
}

function inviteDialog(ctx) {
  const email = h('input', { class: 'input', type: 'email', placeholder: 'prenom.nom@example.fr' });
  const role = select(ROLES.filter((r) => r !== 'owner' || ctx.me.role === 'owner').map((r) => ({ value: r, label: t('role.' + r) })), 'member');
  const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
  const btn = h('button', { class: 'btn primary', type: 'button' }, t('members.invite'));
  const m = modal({
    title: t('members.invite'),
    body: h('div', { class: 'col gap-3' }, field(t('members.email'), email), field(t('members.role'), role, t('members.role_hint')), err),
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), btn],
  });
  btn.addEventListener('click', async () => {
    if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email.value.trim())) { err.textContent = t('members.bad_email'); err.hidden = false; return; }
    btn.disabled = true;
    try { await post('/org/members', { email: email.value.trim(), role: role.value }); m.close(); toast(t('members.invited'), 'ok'); ctx.refresh(); } catch (e) { btn.disabled = false; err.textContent = errorMessage(e); err.hidden = false; }
  });
}

// ── Canaux de notification : settings-channels.js (§ 18, un formulaire par type) ──

// ── Profil ────────────────────────────────────────────────────────────────
async function renderProfile(root, ctx) {
  const me = ctx.me || {};
  const user = me.user || {};
  root.appendChild(h('div', { class: 'grid c2' },
    panel(t('profile.account'), kv([
      [t('col.name'), user.name || null],
      [t('members.email'), user.email || null],
      [t('members.role'), me.role ? t('role.' + me.role) : null],
      [t('profile.org'), me.org ? (me.org.name || me.org.slug) : null],
      [t('profile.mode'), me.mode ? t('profile.mode.' + me.mode) : null],
    ])),
    panel(t('profile.prefs'), h('div', { class: 'col gap-3' },
      field(t('profile.lang'), select(languages().map((l) => ({ value: l.code, label: l.label })), getLang(), (v) => { setLang(v); ctx.refresh(); }), t('profile.lang_hint')),
      field(t('profile.theme'), select([{ value: 'dark', label: t('theme.dark') }, { value: 'light', label: t('theme.light') }], getTheme(), (v) => { setTheme(v); ctx.refresh(); }))))));
  // Application installable et notifications push de cet appareil.
  const push = h('div', { class: 'col gap-3 mt-3', id: 'notifications' });
  root.appendChild(push);
  await (await import('./settings-push.js')).renderPushPanels(push, ctx);
}
