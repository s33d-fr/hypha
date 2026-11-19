// Réglages › Formule et facturation (contrat § 52) : formule de l'espace, limites et consommation,
// estimation de la période, souscription (Stripe Checkout), portail de facturation, options.
import { h, icon } from '../dom.js';
import { t, getLang, locale } from '../i18n.js';
import { get, post, put, asList } from '../api.js';
import { fmtInt, fmtDate, fmtDateTime, fmtRel, isNum } from '../format.js';
import { panel, modal, field, select, toast, confirmDialog, errorMessage, kv } from '../components/ui.js';
import { dataTable } from '../components/table.js';
import { load } from './kit.js';

const EU = ['AT', 'BE', 'BG', 'CY', 'CZ', 'DE', 'DK', 'EE', 'ES', 'FI', 'FR', 'GR', 'HR', 'HU', 'IE', 'IT', 'LT', 'LU',
  'LV', 'MT', 'NL', 'PL', 'PT', 'RO', 'SE', 'SI', 'SK'];
const OTHER_COUNTRIES = ['CH', 'GB', 'NO', 'IS', 'LI', 'MC', 'AD', 'US', 'CA', 'MA', 'TN', 'SN', 'CI'];

function money(v, digits) {
  if (!isNum(v)) return '-';
  return new Intl.NumberFormat(locale(), { style: 'currency', currency: 'EUR', minimumFractionDigits: digits === undefined ? 2 : digits, maximumFractionDigits: digits === undefined ? 4 : digits }).format(v);
}

// Mo et Go décimaux, comme sur la facture (1 Go = 1 000 000 000 octets).
function sizeDec(v) {
  if (!isNum(v)) return '-';
  const fr = getLang() === 'fr';
  const units = fr ? ['o', 'ko', 'Mo', 'Go', 'To'] : ['B', 'kB', 'MB', 'GB', 'TB'];
  let x = v;
  let i = 0;
  while (x >= 1000 && i < units.length - 1) { x /= 1000; i++; }
  return new Intl.NumberFormat(locale(), { maximumFractionDigits: x >= 100 || i === 0 ? 0 : 1 }).format(x) + ' ' + units[i];
}

function qty(v) {
  return isNum(v) ? new Intl.NumberFormat(locale(), { maximumFractionDigits: 3 }).format(v) : '-';
}

function countryName(code) {
  try { return new Intl.DisplayNames([locale()], { type: 'region' }).of(code) || code; } catch (e) { return code; }
}

function bar(used, limit) {
  if (!isNum(limit) || limit <= 0) return h('span', { class: 'muted small' }, t('billing.unlimited'));
  const pct = Math.min(100, (used || 0) / limit * 100);
  const color = pct >= 100 ? 'var(--crit)' : pct >= 80 ? 'var(--warn)' : 'var(--accent)';
  return h('div', { class: 'bar-cell', style: { minWidth: '160px' } },
    h('div', { class: 'bar', style: { flex: '1 1 auto', height: '6px', background: 'var(--surface-3)', borderRadius: '3px' } },
      h('i', { style: { display: 'block', height: '100%', width: pct + '%', background: color, borderRadius: '3px' } })),
    h('span', { class: 'v small', style: { width: 'auto', marginLeft: '8px' } }, fmtInt(Math.round(pct)) + ' %'));
}

export function planLabel(code) {
  return t('billing.plan.' + (code || 'libre'));
}

export async function renderBilling(root, ctx) {
  const { signal, params, me } = ctx;
  const back = params.get('souscription');
  if (back === 'ok') root.appendChild(h('div', { class: 'callout mb-3' }, icon('check', 16), h('span', t('billing.checkout.done'))));
  if (back === 'annulee') root.appendChild(h('div', { class: 'callout warn mb-3' }, icon('warn', 16), h('span', t('billing.checkout.cancelled'))));
  const body = h('div');
  root.appendChild(body);
  await load(body, () => get('/billing', null, { signal }), (b) => draw(b || {}, ctx, me));
}

function draw(b, ctx, me) {
  const out = h('div', { class: 'col gap-3' });
  const isOwner = me && me.role === 'owner';
  if (b.mode === 'selfhosted') {
    out.appendChild(panel(planLabel(null), h('div', { class: 'col gap-2' },
      h('p', t('billing.libre.text')), h('p', { class: 'small text-2' }, t('billing.libre.licences')))));
    return out;
  }
  if (b.plan === 'interne') {
    out.appendChild(panel(planLabel('interne'), h('p', t('billing.interne.text'))));
    out.appendChild(limitsPanel(b));
    return out;
  }
  for (const a of asList(b.alerts)) out.appendChild(alertCallout(a));
  out.appendChild(currentPanel(b, ctx, isOwner));
  out.appendChild(limitsPanel(b));
  if (asList(b.refused_hosts).length) out.appendChild(refusedPanel(b));
  if (b.estimate || b.pro_estimate) out.appendChild(estimatePanel(b));
  out.appendChild(plansPanel(b, ctx, isOwner));
  out.appendChild(h('div', { class: 'small text-2' }, t('billing.legal')));
  return out;
}

export function alertCallout(a) {
  const crit = a.level !== 'warn';
  let text;
  if (a.quota === 'hosts' || a.quota === 'devices') {
    text = t('billing.alert.' + a.quota, { limit: fmtInt(a.limit), n: fmtInt(a.count), names: asList(a.names).join(', ') });
  } else if (a.quota === 'ingest_bytes_per_day') {
    text = crit ? t('billing.alert.ingest', { limit: sizeDec(a.limit), lines: fmtInt(a.refused_log_lines || 0), spans: fmtInt(a.refused_spans || 0) })
      : t('billing.alert.ingest_warn', { used: sizeDec(a.used), limit: sizeDec(a.limit) });
  } else if (a.quota === 'explanations_per_month') {
    text = t('billing.alert.explanations', { limit: fmtInt(a.limit) });
  } else {
    text = t('billing.alert.generic');
  }
  return h('div', { class: ['callout', crit ? 'crit' : 'warn'] }, icon('warn', 16),
    h('span', text, ' ', h('a', { href: '#/settings/billing' }, t('billing.alert.see'))));
}

function currentPanel(b, ctx, isOwner) {
  const sub = b.subscription || {};
  const paid = b.plan === 'pro' || b.plan === 'entreprise';
  const rows = [[t('billing.f.plan'), h('strong', planLabel(b.plan))]];
  if (paid) {
    rows.push([t('billing.f.status'), h('span', { class: ['badge', sub.status === 'active' ? 'ok' : 'warn'] }, t('billing.status.' + (sub.status || 'none')))]);
    rows.push([t('billing.f.interval'), sub.has_commitment ? t('billing.interval.year_n', { n: fmtInt(sub.committed_hosts) }) : t('billing.interval.month')]);
    if (sub.period_start) rows.push([t('billing.f.period'), fmtDate(sub.period_start) + ' → ' + fmtDate(sub.period_end)]);
    if (sub.cancel_at) rows.push([t('billing.f.cancel_at'), h('span', { class: 'badge warn' }, t('billing.cancel_on', { date: fmtDate(sub.cancel_at) }))]);
    rows.push([t('billing.f.retention'), t('billing.retention.n', { n: sub.retention_days || 15 })]);
    if (sub.discount) rows.push([t('billing.f.discount'), t('billing.discount.' + sub.discount)]);
    rows.push([t('billing.f.tax'), t('billing.tax.' + (sub.tax_mode || 'fr20'))]);
    if (sub.last_error) rows.push([t('billing.f.error'), h('span', { style: { color: 'var(--crit)' } }, sub.last_error)]);
    if (sub.last_sync_at) rows.push([t('billing.f.sync'), fmtRel(sub.last_sync_at)]);
  }
  const actions = [];
  if (isOwner && paid) {
    actions.push(h('button', { class: 'btn sm', type: 'button', onclick: () => openPortal() }, icon('external', 12), t('billing.portal')));
  }
  const box = h('div', { class: 'col gap-3' }, kv(rows));
  if (isOwner && paid) box.appendChild(paidControls(b, ctx));
  if (!isOwner && b.plan !== 'interne') box.appendChild(h('div', { class: 'small text-2' }, t('billing.owner_only')));
  return panel(t('billing.current'), box, { actions: actions.length ? h('div', { class: 'row gap-2' }, ...actions) : null });
}

async function openPortal() {
  try {
    const r = await post('/billing/portal', { lang: getLang() });
    if (r && r.url) location.href = r.url;
  } catch (e) { toast(errorMessage(e), 'error', 8000); }
}

function paidControls(b, ctx) {
  const sub = b.subscription || {};
  const wrap = h('div', { class: 'row gap-2', style: { flexWrap: 'wrap', alignItems: 'flex-end' } });
  const ret = select([15, 30, 90].map((d) => ({ value: String(d), label: t('billing.retention.opt' + d) })), String(sub.retention_days || 15));
  wrap.appendChild(field(t('billing.f.retention'), ret, t('billing.retention.hint')));
  wrap.appendChild(h('button', { class: 'btn sm', type: 'button', onclick: async () => {
    try { await put('/billing/retention', { days: Number(ret.value) }); toast(t('billing.saved'), 'success'); ctx.refresh(); } catch (e) { toast(errorMessage(e), 'error', 8000); }
  } }, t('action.save')));
  const other = b.plan === 'pro' ? 'entreprise' : 'pro';
  wrap.appendChild(h('button', { class: 'btn sm', type: 'button', onclick: async () => {
    const ok = await confirmDialog({ title: t('billing.switch.' + other), text: t('billing.switch.' + other + '_text'), confirmLabel: t('billing.switch.confirm') });
    if (!ok) return;
    try { await put('/billing/plan', { plan: other }); toast(t('billing.saved'), 'success'); ctx.refresh(); } catch (e) { toast(errorMessage(e), 'error', 8000); }
  } }, t('billing.switch.' + other)));
  if (sub.cancel_at) {
    wrap.appendChild(h('button', { class: 'btn sm', type: 'button', onclick: async () => {
      try { await post('/billing/resume', {}); toast(t('billing.saved'), 'success'); ctx.refresh(); } catch (e) { toast(errorMessage(e), 'error', 8000); }
    } }, t('billing.resume')));
  } else {
    wrap.appendChild(h('button', { class: 'btn sm danger', type: 'button', onclick: async () => {
      const ok = await confirmDialog({ title: t('billing.cancel'), text: t('billing.cancel_text'), confirmLabel: t('billing.cancel'), danger: true });
      if (!ok) return;
      try { await post('/billing/cancel', {}); toast(t('billing.saved'), 'success'); ctx.refresh(); } catch (e) { toast(errorMessage(e), 'error', 8000); }
    } }, t('billing.cancel')));
  }
  return wrap;
}

function limitsPanel(b) {
  const L = b.limits || {};
  const N = b.now || {};
  const rows = [
    { key: 'hosts', label: t('billing.l.hosts'), used: fmtInt(N.hosts), limit: L.hosts, ratio: [N.hosts, L.hosts] },
    { key: 'devices', label: t('billing.l.devices'), used: fmtInt(N.devices), limit: L.devices, ratio: [N.devices, L.devices] },
    { key: 'ingest', label: t('billing.l.ingest'), used: sizeDec(N.ingest_bytes_today), limit: L.ingest_bytes_per_day, limitText: isNum(L.ingest_bytes_per_day) ? sizeDec(L.ingest_bytes_per_day) + t('billing.per_day') : null, ratio: [N.ingest_bytes_today, L.ingest_bytes_per_day] },
    { key: 'tests', label: t('billing.l.public_tests'), used: fmtInt(N.public_tests), limit: L.public_tests, ratio: [N.public_tests, L.public_tests],
      limitText: isNum(L.public_tests) ? null : (isNum(L.public_tests_included) ? t('billing.l.tests_included', { n: fmtInt(L.public_tests_included) }) : null) },
    { key: 'interval', label: t('billing.l.interval'), used: N.min_public_interval_s ? t('billing.every_s', { n: fmtInt(N.min_public_interval_s) }) : '-', limitText: t('billing.min_every', { n: fmtInt(Math.round((L.public_interval_min_s || 30) / 60 * 10) / 10) }) },
    { key: 'private', label: t('billing.l.private_tests'), used: fmtInt(N.private_tests), limitText: t('billing.unlimited') },
    { key: 'pages', label: t('billing.l.status_pages'), used: fmtInt(N.status_pages), limit: L.status_pages, ratio: [N.status_pages, L.status_pages] },
    { key: 'ai', label: t('billing.l.explanations'), used: fmtInt(N.explanations_month), limit: L.explanations_per_month, ratio: [N.explanations_month, L.explanations_per_month], limitText: isNum(L.explanations_per_month) ? fmtInt(L.explanations_per_month) + t('billing.per_month') : t('billing.reasonable') },
    { key: 'logs_days', label: t('billing.l.logs_days'), used: '', limitText: t('billing.days', { n: L.logs_days }) },
    { key: 'metrics', label: t('billing.l.metrics'), used: '', limitText: isNum(L.metrics_days) ? t('billing.metrics_short', { raw: L.metrics_raw_days, all: L.metrics_days }) : t('billing.metrics_full') },
  ];
  if (isNum(L.logs_gb_per_host)) rows.push({ key: 'incl', label: t('billing.l.included'), used: '', limitText: t('billing.included', { logs: L.logs_gb_per_host, traces: L.traces_gb_per_host }) });
  return panel(t('billing.limits'), dataTable({
    columns: [
      { key: 'label', label: t('billing.col.what') },
      { key: 'used', label: t('billing.col.now'), align: 'r', render: (r) => r.used },
      { key: 'limit', label: t('billing.col.limit'), align: 'r', render: (r) => r.limitText || (isNum(r.limit) ? fmtInt(r.limit) : t('billing.unlimited')) },
      { key: 'ratio', label: '', render: (r) => (r.ratio ? bar(r.ratio[0], r.ratio[1]) : '') },
    ], rows, maxRows: 50,
  }), { flush: true, sub: t('billing.limits_sub') });
}

function refusedPanel(b) {
  return panel(t('billing.refused'), dataTable({
    columns: [
      { key: 'hostname', label: t('col.name'), render: (r) => h('span', { class: 'mono small' }, r.hostname) },
      { key: 'kind', label: t('billing.col.kind'), render: (r) => t('billing.kind.' + (r.kind === 'agent' ? 'agent' : 'device')) },
      { key: 'last_at', label: t('billing.col.last'), render: (r) => fmtDateTime(r.last_at) },
      { key: 'attempts', label: t('billing.col.attempts'), align: 'r', render: (r) => fmtInt(r.attempts) },
    ], rows: asList(b.refused_hosts), maxRows: 50,
  }), { flush: true, sub: t('billing.refused_sub') });
}

function estimatePanel(b) {
  const est = b.estimate || b.pro_estimate;
  const p = b.period || {};
  const lines = asList(est.lines).filter((l) => l.key === 'entreprise' || l.quantity > 0 || l.key === 'host');
  const table = dataTable({
    columns: [
      { key: 'key', label: t('billing.col.item'), render: (l) => t('billing.item.' + l.key) },
      { key: 'quantity', label: t('billing.col.qty'), align: 'r', render: (l) => qty(l.quantity) + ' ' + t('billing.unit.' + l.key) },
      { key: 'unit_price', label: t('billing.col.unit'), align: 'r', render: (l) => money(l.unit_price) },
      { key: 'amount', label: t('billing.col.amount'), align: 'r', render: (l) => money(l.amount, 2) },
    ], rows: lines, maxRows: 20,
  });
  const totals = kv([
    [t('billing.total_ht'), h('strong', money(est.total_ht, 2))],
    [est.tax_mode === 'reverse' ? t('billing.tax.reverse') : t('billing.vat20'), money(est.vat, 2)],
    [t('billing.total_ttc'), money(est.total_ttc, 2)],
    est.annual_commitment ? [t('billing.annual_commitment'), t('billing.annual_commitment_v', { n: fmtInt(est.annual_commitment.hosts), amount: money(est.annual_commitment.amount, 2) })] : [t('billing.annual_commitment'), undefined],
  ]);
  const facts = h('div', { class: 'small text-2' }, t('billing.period_facts', {
    from: fmtDate(p.period_start), to: fmtDate(p.period_end), hours: fmtInt(p.host_hours), months: qty(p.host_months),
    logs: sizeDec(p.log_bytes), traces: sizeDec(p.span_bytes), incl_logs: qty(p.logs_included_gb), incl_traces: qty(p.traces_included_gb),
  }));
  const title = b.estimate ? t('billing.estimate') : t('billing.pro_estimate');
  const sub = b.estimate ? t('billing.estimate_sub') : t('billing.pro_estimate_sub');
  return panel(title, h('div', { class: 'col gap-3' }, table, h('div', { style: { padding: '0 var(--sp-3) var(--sp-3)' } }, totals, facts)), { flush: true, sub });
}

function plansPanel(b, ctx, isOwner) {
  const P = b.prices || {};
  const open = b.billing && b.billing.open;
  const cards = h('div', { class: 'grid c3' });
  const card = (code, price, points, action) => h('div', { class: 'panel', style: { padding: 'var(--sp-4)', border: b.plan === code ? '1px solid var(--accent)' : null } },
    h('h3', { style: { margin: '0 0 4px' } }, planLabel(code), b.plan === code ? h('span', { class: 'badge ok', style: { marginLeft: '8px' } }, t('billing.yours')) : null),
    h('div', { class: 'num', style: { fontSize: 'var(--fs-xl)', margin: '4px 0 8px' } }, price),
    h('ul', { class: 'list-plain small', style: { margin: '0 0 12px', paddingLeft: '18px', listStyle: 'disc' } }, ...points.map((x) => h('li', x))),
    action || null);
  const choose = (plan) => {
    if (!isOwner) return h('div', { class: 'xsmall muted' }, t('billing.owner_only'));
    if (b.plan === plan) return null;
    if ((b.plan === 'pro' || b.plan === 'entreprise') && plan !== 'decouverte') return null;
    if (plan === 'decouverte') return null;
    if (!open) return h('div', { class: 'col gap-2' }, h('button', { class: 'btn primary', type: 'button', disabled: true }, t('billing.choose', { plan: planLabel(plan) })), h('div', { class: 'xsmall muted' }, t('billing.closed')));
    return h('button', { class: 'btn primary', type: 'button', onclick: () => checkoutDialog(plan, b, ctx) }, t('billing.choose', { plan: planLabel(plan) }));
  };
  cards.appendChild(card('decouverte', t('billing.free'), [t('billing.p.d1'), t('billing.p.d2'), t('billing.p.d3'), t('billing.p.d4'), t('billing.p.d5')], choose('decouverte')));
  cards.appendChild(card('pro', t('billing.pro_price', { m: money(P.host_month, 2), y: money(P.host_year / 12, 2) }),
    [t('billing.p.p1'), t('billing.p.p2', { d: money(P.device_month, 2) }), t('billing.p.p3', { gb: money(P.gb_over, 2) }), t('billing.p.p4', { r30: money(P.retention_30, 2), r90: money(P.retention_90, 2) }), t('billing.p.p5'), t('billing.p.p6', { sms: money(P.sms, 2) })], choose('pro')));
  cards.appendChild(card('entreprise', t('billing.ent_price', { e: money(P.entreprise_month, 0) }),
    [t('billing.p.e1'), t('billing.p.e2'), t('billing.p.e3'), t('billing.p.e4'), t('billing.p.e5')], choose('entreprise')));
  return panel(t('billing.plans'), h('div', { class: 'col gap-3' }, cards, h('div', { class: 'small text-2' }, t('billing.discounts'))), { sub: t('billing.plans_sub') });
}

function checkoutDialog(plan, b, ctx) {
  const interval = select([{ value: 'month', label: t('billing.interval.month_opt') }, { value: 'year', label: t('billing.interval.year_opt') }], 'month');
  const committed = h('input', { class: 'input', type: 'number', min: 1, max: 100000, value: Math.max(1, (b.now && b.now.hosts) || 1) });
  const committedField = field(t('billing.f.committed'), committed, t('billing.committed_hint'));
  committedField.hidden = true;
  interval.addEventListener('change', () => { committedField.hidden = interval.value !== 'year'; });
  const company = h('input', { class: 'input', maxlength: 200, placeholder: t('billing.company_ph') });
  const email = h('input', { class: 'input', type: 'email', maxlength: 320, value: (ctx.me && ctx.me.user && ctx.me.user.email) || '' });
  const countries = EU.concat(OTHER_COUNTRIES).map((c) => ({ value: c, label: countryName(c) })).sort((a, b2) => a.label.localeCompare(b2.label, locale()));
  const country = select(countries, 'FR');
  const vat = h('input', { class: 'input mono', maxlength: 20, placeholder: 'FR16131908329' });
  const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true });
  const btn = h('button', { class: 'btn primary', type: 'button' }, t('billing.to_payment'));
  const m = modal({
    title: t('billing.choose', { plan: planLabel(plan) }),
    body: h('div', { class: 'col gap-3' },
      h('div', { class: 'small text-2' }, t('billing.checkout_intro')),
      field(t('billing.f.interval'), interval), committedField,
      field(t('billing.f.company'), company), field(t('billing.f.email'), email),
      field(t('billing.f.country'), country), field(t('billing.f.vat'), vat, t('billing.vat_hint')), err),
    actions: [h('button', { class: 'btn', type: 'button', onclick: () => m.close() }, t('action.cancel')), btn],
  });
  btn.addEventListener('click', async () => {
    err.hidden = true;
    btn.disabled = true;
    try {
      const r = await post('/billing/checkout', {
        plan, interval: interval.value, committed_hosts: Number(committed.value) || 0, company: company.value.trim(),
        email: email.value.trim(), country: country.value, vat_id: vat.value.trim(), lang: getLang(),
      });
      if (r && r.url) location.href = r.url;
      else { btn.disabled = false; err.textContent = t('err.unknown'); err.hidden = false; }
    } catch (e) { btn.disabled = false; err.textContent = errorMessage(e); err.hidden = false; }
  });
}
