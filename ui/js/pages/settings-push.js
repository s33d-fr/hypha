// Profil › Application et notifications : installer HYPHA, abonner cet appareil aux notifications push
// (sur un geste explicite), essayer, retirer ses appareils.
import { h, clear, icon } from '../dom.js';
import { t } from '../i18n.js';
import { get, post, del, asList } from '../api.js';
import { fmtRel, toMs } from '../format.js';
import { panel, toast, confirmDialog, errorMessage } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import {
  pushSupported, isStandalone, isIOS, canPromptInstall, promptInstall, onInstallChange,
  currentSubscription, subscribeDevice, unsubscribeDevice,
} from '../pwa.js';

function callout(kind, iconName, ...body) {
  return h('div', { class: ['callout', kind] }, icon(iconName, 16), h('div', { class: 'small' }, ...body));
}

function installBox(ctx) {
  const box = h('div', { class: 'col gap-2' });
  const draw = () => {
    clear(box);
    if (isStandalone()) { box.appendChild(callout('ok', 'check', t('push.installed'))); return; }
    if (canPromptInstall()) {
      box.appendChild(h('div', { class: 'row' }, h('button', {
        class: 'btn primary', type: 'button',
        onclick: async () => { if (await promptInstall()) toast(t('push.install_done'), 'ok'); draw(); },
      }, icon('download', 14), t('push.install'))));
    }
    box.appendChild(h('ul', { class: 'small text-2', style: { margin: '0', paddingLeft: '18px' } },
      h('li', t('push.how.android')), h('li', t('push.how.ios')), h('li', t('push.how.desktop'))));
    box.appendChild(h('div', { class: 'xsmall muted' }, h('a', { href: t('push.doc_url'), target: '_blank', rel: 'noopener' }, t('push.doc'))));
  };
  draw();
  ctx.onCleanup(onInstallChange(draw));
  return box;
}

export async function renderPushPanels(root, ctx) {
  root.appendChild(panel(t('push.install_title'), installBox(ctx)));
  const body = h('div', { class: 'col gap-3' });
  root.appendChild(panel(t('push.title'), body));
  const devicesEl = h('div');
  root.appendChild(panel(t('push.devices'), devicesEl, { flush: true }));

  let config = null;
  let devices = [];
  let sub = null;

  async function load() {
    try { config = await get('/push/config', null, { signal: ctx.signal }); } catch (e) { config = { enabled: false, error: e }; }
    try { devices = asList(await get('/push/subscriptions', null, { signal: ctx.signal })); } catch (e) { devices = []; }
    sub = await currentSubscription();
  }

  function mine(d) { return !!(sub && d.endpoint === sub.endpoint); }

  function drawState() {
    clear(body);
    body.appendChild(h('div', { class: 'small text-2' }, t('push.intro')));
    if (!pushSupported()) {
      body.appendChild(callout('warn', 'warn', t(isIOS() && !isStandalone() ? 'push.ios_first' : 'push.unsupported')));
      return;
    }
    if (!config || !config.enabled) {
      body.appendChild(callout('warn', 'warn', t('push.server_off')));
      return;
    }
    const registered = devices.some(mine);
    const perm = typeof Notification !== 'undefined' ? Notification.permission : 'default';
    if (perm === 'denied') body.appendChild(callout('crit', 'lock', t('push.denied')));
    const row = h('div', { class: 'row', style: { flexWrap: 'wrap', gap: '8px' } });
    if (registered && perm === 'granted') {
      body.appendChild(callout('ok', 'check', t('push.on_here')));
      const here = devices.find(mine);
      row.appendChild(h('button', { class: 'btn', type: 'button', onclick: (e) => testDevice(here, e.currentTarget) }, icon('send', 13), t('push.test')));
      row.appendChild(h('button', {
        class: 'btn ghost danger', type: 'button',
        onclick: async (e) => {
          e.currentTarget.disabled = true;
          try { await unsubscribeDevice(devices); toast(t('push.off_done'), 'ok'); } catch (err) { toast(errorMessage(err), 'error'); }
          await refresh();
        },
      }, icon('trash', 13), t('push.disable')));
    } else if (perm !== 'denied') {
      body.appendChild(h('div', { class: 'small' }, t('push.off_here')));
      row.appendChild(h('button', {
        class: 'btn primary', type: 'button',
        // Le clic appelle directement la demande d'autorisation du navigateur (geste explicite).
        onclick: async (e) => {
          const b = e.currentTarget;
          b.disabled = true;
          try {
            await subscribeDevice(config.public_key);
            toast(t('push.on_done'), 'ok');
          } catch (err) {
            const code = (err && err.code) || '';
            toast(code.startsWith('permission_') ? t('push.not_allowed') : errorMessage(err), 'error', 7000);
          }
          await refresh();
        },
      }, icon('bell', 14), t('push.enable')));
    }
    body.appendChild(row);
  }

  async function testDevice(d, btn) {
    if (!d) return;
    if (btn) btn.disabled = true;
    try {
      const r = await post('/push/subscriptions/' + d.id + '/test', {});
      toast((r && r.ok ? t('push.test_ok') : t('push.test_ko')) + (r && r.detail ? ' · ' + r.detail : ''), r && r.ok ? 'ok' : 'error', 7000);
    } catch (e) { toast(errorMessage(e), 'error', 7000); }
    if (btn) btn.disabled = false;
    await refresh();
  }

  function drawDevices() {
    clear(devicesEl);
    if (!devices.length) { devicesEl.appendChild(h('div', { class: 'state' }, h('div', { class: 'small' }, t('push.no_devices')))); return; }
    devicesEl.appendChild(dataTable({
      columns: [
        { key: 'label', label: t('push.col.device'), render: (d) => h('span', null, d.label || t('push.unnamed'), mine(d) ? h('span', { class: 'badge ok', style: { marginLeft: '6px' } }, t('push.this_device')) : null) },
        { key: 'created_at', label: t('col.created'), value: (d) => toMs(d.created_at), render: (d) => fmtRel(d.created_at) },
        { key: 'last_success_at', label: t('push.col.last_ok'), value: (d) => toMs(d.last_success_at) || 0, render: (d) => (d.last_success_at ? fmtRel(d.last_success_at) : '-') },
        { key: 'last_error', label: t('push.col.last_error'), render: (d) => (d.last_error ? h('span', { class: 'xsmall mono', title: d.last_error }, (d.last_error_at ? fmtRel(d.last_error_at) + ' · ' : '') + d.last_error) : '-') },
        { key: 'actions', label: '', sort: false, render: (d) => h('span', { class: 'row', style: { gap: '2px', justifyContent: 'flex-end' } },
          h('button', { class: 'btn ghost sm', type: 'button', title: t('push.test'), onclick: (e) => testDevice(d, e.currentTarget) }, icon('send', 13), t('push.test')),
          h('button', { class: 'btn ghost sm icon danger', type: 'button', title: t('push.remove'), onclick: async () => {
            if (!(await confirmDialog({ title: t('push.remove_title'), text: t('push.remove_text', { name: d.label || t('push.unnamed') }), confirmLabel: t('push.remove'), danger: true }))) return;
            try {
              if (mine(d)) await unsubscribeDevice(devices); else await del('/push/subscriptions/' + d.id);
              toast(t('push.removed'), 'ok');
            } catch (e) { toast(errorMessage(e), 'error'); }
            await refresh();
          } }, icon('trash', 13))) },
      ],
      rows: devices, sortKey: 'created_at', sortDir: 'desc',
    }));
  }

  async function refresh() {
    await load();
    if (!ctx.isCurrent()) return;
    drawState();
    drawDevices();
  }

  body.appendChild(h('div', { class: 'row small text-2' }, h('span', { class: 'spinner' })));
  await refresh();
}
