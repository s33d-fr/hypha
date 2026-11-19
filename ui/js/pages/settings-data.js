// Réglages › Équipements (§ 14) et Réglages › Consommation (§ 30).
import { h, clear, icon, cssVar } from '../dom.js';
import { t, tn } from '../i18n.js';
import { get, asList } from '../api.js';
import { fmtRel, fmtDateTime, fmtInt, fmtCompact, fmtBytes, toMs, isNum, fmtDate } from '../format.js';
import { empty, panel, select, field, codeBlock, pills, statusDot } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { timeChart } from '../components/chart.js';
import { load, tile } from './kit.js';
import { isDevice } from './device.js';

// ── Équipements ───────────────────────────────────────────────────────────
// La conf SNMP reste dans l'agent : le générateur ne demande aucun secret et ne transmet rien.
function yamlValue(v) {
  const str = String(v);
  return /^[\w.\-/:]+$/.test(str) ? str : JSON.stringify(str);
}

function snmpYaml(d) {
  const tags = (d.tags || '').split(',').map((x) => x.trim()).filter((x) => /^[^:]+:.+$/.test(x));
  const tagStr = tags.length ? ', tags: {' + tags.map((x) => { const i = x.indexOf(':'); return x.slice(0, i).trim() + ': ' + yamlValue(x.slice(i + 1).trim()); }).join(', ') + '}' : '';
  let dev;
  if (d.version === '3') {
    dev = '    - {name: ' + yamlValue(d.name || 'equipement') + ', address: ' + yamlValue(d.address || '192.0.2.1') + ', version: 3, user: ' + yamlValue(d.user || 'hypha') +
      ',\n       level: ' + d.level + (d.level !== 'noAuthNoPriv' ? ', auth_protocol: ' + d.auth + ', auth_key: "<clé d’authentification>"' : '') +
      (d.level === 'authPriv' ? ',\n       priv_protocol: ' + d.priv + ', priv_key: "<clé de chiffrement>"' : '') + ', profile: ' + d.profile + tagStr + '}';
  } else {
    dev = '    - {name: ' + yamlValue(d.name || 'equipement') + ', address: ' + yamlValue(d.address || '192.0.2.1') + ', version: ' + d.version + ', community: "<communauté>", profile: ' + d.profile + tagStr + '}';
  }
  return 'snmp:\n  enabled: true\n  interval: ' + (d.interval || '60s') + '\n  devices:\n' + dev + '\n' +
    (d.traps ? '  traps: {enabled: true, listen: "0.0.0.0:162", community: "<communauté des traps>"}\n' : '  traps: {enabled: false}\n');
}

export async function renderDevicesSettings(root, ctx) {
  const { signal, href } = ctx;
  const listEl = h('div');
  const countEl = h('span', { class: 'sub' });
  root.appendChild(h('div', { class: 'querybar' }, h('span', { class: 'muted small grow' }, t('devset.intro'))));
  const genEl = h('div');
  root.appendChild(h('div', { class: 'split' },
    panel(t('devset.seen'), listEl, { flush: true, actions: countEl }),
    panel(t('devset.conf'), genEl)));

  await load(listEl, async () => {
    const hosts = asList(await get('/hosts', null, { signal }), 'hosts');
    return hosts;
  }, (hosts) => {
    const byId = new Map(hosts.map((x) => [String(x.id), x]));
    const devices = hosts.filter(isDevice);
    countEl.textContent = tn('devset.count', devices.length);
    if (!devices.length) return empty(t('devset.none_title'), t('devset.none_text'));
    return dataTable({
      columns: [
        { key: 'status', label: '', width: '24px', value: (x) => (x.status === 'down' ? 0 : 1), render: (x) => statusDot(x.status) },
        { key: 'hostname', label: t('col.name'), render: (x) => h('a', { href: href('/infrastructure/host/' + encodeURIComponent(x.id)) }, x.hostname || '#' + x.id) },
        { key: 'address', label: t('device.f.address'), value: (x) => (x.device && x.device.address) || (x.ip_addresses || [])[0] || '', render: (x) => h('span', { class: 'mono small' }, (x.device && x.device.address) || (x.ip_addresses || [])[0] || '-') },
        { key: 'model', label: t('device.f.model'), value: (x) => [x.device && x.device.vendor, x.device && x.device.model].filter(Boolean).join(' '), render: (x) => h('span', { class: 'small text-2' }, [x.device && x.device.vendor, x.device && x.device.model].filter(Boolean).join(' ') || '-') },
        { key: 'via', label: t('devset.via'), value: (x) => (byId.get(String(x.via_host_id)) || {}).hostname || '', render: (x) => {
          const via = byId.get(String(x.via_host_id));
          if (x.via_host_id === undefined || x.via_host_id === null) return h('span', { class: 'muted' }, '-');
          return h('a', { href: href('/infrastructure/host/' + encodeURIComponent(x.via_host_id)) }, (via && via.hostname) || x.via_hostname || '#' + x.via_host_id);
        } },
        { key: 'last_seen', label: t('devset.last_contact'), align: 'r', value: (x) => toMs(x.last_seen), render: (x) => h('span', { title: fmtDateTime(x.last_seen) }, fmtRel(x.last_seen)) },
      ],
      rows: devices, sortKey: 'hostname', sortDir: 'asc',
      onRowClick: (x) => ctx.navigate('/infrastructure/host/' + x.id),
    });
  });

  // Générateur de conf YAML
  const d = { name: '', address: '', version: '2c', user: 'hypha', level: 'authPriv', auth: 'sha', priv: 'aes', profile: 'auto', tags: '', interval: '60s', traps: false };
  const out = h('div');
  const formEl = h('div', { class: 'form-grid' });
  function draw() {
    clear(formEl);
    const inp = (key, ph, mono) => { const el = h('input', { class: ['input', mono ? 'mono' : null], value: d[key] || '', placeholder: ph || '' }); el.addEventListener('input', () => { d[key] = el.value.trim(); drawOut(); }); return el; };
    formEl.appendChild(field(t('col.name'), inp('name', 'switch-vlan1', true)));
    formEl.appendChild(field(t('device.f.address'), inp('address', '192.0.2.10', true)));
    formEl.appendChild(field(t('devset.version'), select([{ value: '1', label: 'v1' }, { value: '2c', label: 'v2c' }, { value: '3', label: 'v3' }], d.version, (v) => { d.version = v; draw(); })));
    formEl.appendChild(field(t('devset.profile'), select(['auto', 'generic', 'mikrotik', 'synology', 'printer', 'ups'].map((p) => ({ value: p, label: t('devset.profile.' + p) })), d.profile, (v) => { d.profile = v; drawOut(); })));
    if (d.version === '3') {
      formEl.appendChild(field(t('devset.user'), inp('user', 'hypha', true)));
      formEl.appendChild(field(t('devset.level'), select(['noAuthNoPriv', 'authNoPriv', 'authPriv'].map((l) => ({ value: l, label: l })), d.level, (v) => { d.level = v; draw(); })));
      if (d.level !== 'noAuthNoPriv') formEl.appendChild(field(t('devset.auth'), select(['md5', 'sha', 'sha256', 'sha512'].map((l) => ({ value: l, label: l.toUpperCase() })), d.auth, (v) => { d.auth = v; drawOut(); })));
      if (d.level === 'authPriv') formEl.appendChild(field(t('devset.priv'), select(['des', 'aes', 'aes192', 'aes256'].map((l) => ({ value: l, label: l.toUpperCase() })), d.priv, (v) => { d.priv = v; drawOut(); })));
    }
    formEl.appendChild(field(t('devset.interval'), select(['30s', '60s', '120s', '300s'].map((l) => ({ value: l, label: l })), d.interval, (v) => { d.interval = v; drawOut(); })));
    formEl.appendChild(h('div', { class: 'full' }, field(t('col.tags'), inp('tags', 'role:switch, site:atelier', true))));
    const traps = h('input', { type: 'checkbox', checked: d.traps });
    traps.addEventListener('change', () => { d.traps = traps.checked; drawOut(); });
    formEl.appendChild(h('label', { class: 'check full' }, traps, t('devset.traps')));
    drawOut();
  }
  function drawOut() {
    clear(out);
    out.appendChild(codeBlock(snmpYaml(d)));
  }
  genEl.appendChild(h('div', { class: 'col gap-3' },
    h('div', { class: 'callout' }, icon('lock', 16), h('div', { class: 'small' }, t('devset.no_secret'))),
    formEl, out,
    h('div', { class: 'small text-2' }, t('devset.where')),
    codeBlock('sudo nano /etc/hypha/hypha.yaml\nsudo hypha-agent check-config && sudo systemctl restart hypha-agent'),
    h('div', { class: 'xsmall muted' }, t('devset.windows'))));
  draw();
}

// ── Consommation ──────────────────────────────────────────────────────────
const USAGE_SERIES = [
  ['log_lines', 'count', '--s1'], ['log_bytes', 'byte', '--s2'], ['metric_points', 'count', '--s3'],
  ['spans', 'count', '--s7'], ['rum_events', 'count', '--s5'], ['checks', 'count', '--s4'],
];

function dayMs(d) {
  if (isNum(d)) return d;
  const ms = Date.parse(String(d) + (String(d).length === 10 ? 'T00:00:00' : ''));
  return Number.isFinite(ms) ? ms : null;
}

export async function renderUsage(root, ctx) {
  const { params, signal } = ctx;
  let span = ['7', '30', '90'].includes(params.get('days')) ? params.get('days') : '30';
  root.appendChild(h('div', { class: 'querybar' }, h('span', { class: 'muted small grow' }, t('usage.intro')),
    pills([{ key: '7', label: t('time.preset.7d') }, { key: '30', label: t('time.preset.30d') }, { key: '90', label: t('usage.90d') }], span, (k) => ctx.setParams({ days: k === '30' ? null : k }))));
  // Formule (§ 52) : limites, consommation facturable et estimation sur la page dédiée.
  if (ctx.me && ctx.me.mode === 'saas' && ctx.me.plan && ctx.me.plan !== 'interne') {
    root.appendChild(h('div', { class: 'callout mb-3' }, icon('info', 16),
      h('span', t('usage.to_billing'), ' ', h('a', { href: ctx.href('/settings/billing') }, t('nav.settings.billing')))));
  }
  const body = h('div');
  root.appendChild(body);
  const to = Date.now();
  const from = to - Number(span) * 86400e3;
  await load(body, () => get('/usage', { from, to }, { signal }), (u) => {
    u = u || {};
    const days = asList(u.days).map((d) => Object.assign({}, d, { ms: dayMs(d.day) })).filter((d) => d.ms !== null).sort((a, b) => a.ms - b.ms);
    const sum = (k) => days.reduce((a, d) => a + (isNum(d[k]) ? d[k] : 0), 0);
    const n = Math.max(1, days.length);
    const out = h('div', { class: 'col gap-3' });
    out.appendChild(h('div', { class: 'tiles' },
      tile(t('usage.hosts'), fmtInt(u.hosts)),
      tile(t('usage.devices'), fmtInt(u.devices)),
      tile(t('usage.series'), fmtCompact(u.series), t('usage.series_sub')),
      tile(t('usage.log_lines'), fmtCompact(sum('log_lines')), t('usage.per_day', { v: fmtCompact(sum('log_lines') / n) })),
      tile(t('usage.log_bytes'), fmtBytes(sum('log_bytes')), t('usage.per_day', { v: fmtBytes(sum('log_bytes') / n) })),
      tile(t('usage.metric_points'), fmtCompact(sum('metric_points')), t('usage.per_day', { v: fmtCompact(sum('metric_points') / n) })),
      tile(t('usage.spans'), fmtCompact(sum('spans'))),
      tile(t('usage.span_bytes'), fmtBytes(sum('span_bytes')), t('usage.per_day', { v: fmtBytes(sum('span_bytes') / n) })),
      tile(t('usage.rum_events'), fmtCompact(sum('rum_events'))),
      tile(t('usage.synthetic_runs'), fmtCompact(sum('synthetic_runs')), sum('synthetic_billable_runs') ? t('usage.billable', { v: fmtCompact(sum('synthetic_billable_runs')) }) : null),
      tile(t('usage.explanations'), fmtInt(sum('explanations'))),
      sum('refused_log_lines') || sum('refused_spans') ? tile(t('usage.refused'), fmtCompact(sum('refused_log_lines') + sum('refused_spans')), t('usage.refused_sub'), 'var(--crit)') : null));
    if (!days.length) { out.appendChild(empty(t('usage.none_title'), t('usage.none_text'))); return out; }
    const xs = days.map((d) => d.ms);
    const grid = h('div', { class: 'charts-grid' });
    for (const [key, unit, color] of USAGE_SERIES) {
      grid.appendChild(panel(t('usage.' + key), timeChart({
        xs, lines: [{ label: t('usage.' + key), values: days.map((d) => (isNum(d[key]) ? d[key] : 0)), color: cssVar(color) }],
        unit, height: 150, display: 'bars', legend: false, xRange: [from, to],
      }), { sub: t('usage.per_day_sub') }));
    }
    out.appendChild(grid);
    const top = asList(u.top_services_by_logs);
    const maxL = Math.max(1, ...top.map((x) => x.lines || x.log_lines || x.count || 0));
    out.appendChild(h('div', { class: 'grid c2' },
      panel(t('usage.top_services'), top.length ? dataTable({
        columns: [
          { key: 'service', label: t('col.service'), render: (x) => h('span', { class: 'mono small' }, x.service || t('lf.vol.none')) },
          { key: 'lines', label: t('lf.vol.lines'), align: 'r', value: (x) => x.lines || x.log_lines || x.count, render: (x) => h('div', { class: 'bar-cell' }, h('div', { class: 'bar' }, h('i', { style: { width: ((x.lines || x.log_lines || x.count || 0) / maxL * 100) + '%' } })), h('span', { class: 'v' }, fmtCompact(x.lines || x.log_lines || x.count))) },
          { key: 'bytes', label: t('lf.vol.bytes'), align: 'r', value: (x) => x.bytes || x.log_bytes, render: (x) => fmtBytes(x.bytes || x.log_bytes) },
        ], rows: top, sortKey: 'lines', sortDir: 'desc', maxRows: 25,
      }) : empty(null, t('usage.no_services')), { flush: true, actions: h('a', { class: 'btn sm', href: ctx.href('/settings/logs') }, icon('filter', 12), t('usage.to_filters')) }),
      panel(t('usage.by_day'), dataTable({
        columns: [
          { key: 'ms', label: t('col.date'), render: (d) => fmtDate(d.ms) },
          { key: 'log_lines', label: t('usage.log_lines'), align: 'r', render: (d) => fmtCompact(d.log_lines) },
          { key: 'log_bytes', label: t('usage.log_bytes'), align: 'r', render: (d) => fmtBytes(d.log_bytes) },
          { key: 'metric_points', label: t('usage.metric_points'), align: 'r', render: (d) => fmtCompact(d.metric_points) },
          { key: 'spans', label: t('usage.spans'), align: 'r', render: (d) => fmtCompact(d.spans) },
        ], rows: days, sortKey: 'ms', sortDir: 'desc', maxRows: 90,
      }), { flush: true })));
    return out;
  });
}
