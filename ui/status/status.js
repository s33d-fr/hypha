// Page de statut publique HYPHA (§ 21). Autonome : aucun module de l'application, aucune
// authentification. Lit GET /api/public/status/{slug} (?p=<slug>), se met à jour chaque minute.
// Tout texte passe par textContent : rien de ce que renvoie le serveur n'est interprété comme HTML.

const DICT = {
  fr: {
    title: 'Statut', loading: 'Chargement…',
    'err.missing': 'Adresse incomplète : le nom de la page manque (?p=…).',
    'err.404': 'Cette page de statut n’existe pas ou n’est pas publiée.',
    'err.network': 'Le serveur ne répond pas. Nouvelle tentative dans une minute.',
    'err.other': 'La page n’a pas pu être chargée.', retry: 'Réessayer',
    'overall.operational': 'Tous les systèmes fonctionnent',
    'overall.degraded': 'Performances dégradées',
    'overall.partial_outage': 'Panne partielle',
    'overall.major_outage': 'Panne majeure',
    'overall.maintenance': 'Maintenance en cours',
    'st.operational': 'Opérationnel', 'st.degraded': 'Dégradé', 'st.partial_outage': 'Panne partielle',
    'st.major_outage': 'Panne majeure', 'st.maintenance': 'Maintenance', 'st.nodata': 'Aucune donnée',
    updated: 'Mis à jour à {t}', uptime: '{v} de disponibilité', days90: '90 derniers jours', days30: '30 derniers jours', today: 'Aujourd’hui',
    'bars.label': '{name} : {v} de disponibilité sur {n} jours, {bad} jour(s) avec incident. Flèches gauche et droite pour parcourir les jours.',
    'day.uptime': '{v} de disponibilité',
    components: 'Composants', 'incidents.active': 'Incidents en cours', 'incidents.history': 'Historique des incidents (90 jours)',
    'incidents.none': 'Aucun incident ces 90 derniers jours.', maintenances: 'Maintenances',
    'mt.active': 'En cours', 'mt.upcoming': 'Prévue', 'mt.until': 'jusqu’au {d}', 'mt.from_to': 'du {a} au {b}',
    'inc.active': 'En cours', 'inc.stable': 'Sous surveillance', 'inc.resolved': 'Résolu',
    'inc.started': 'Début : {d}', 'inc.resolved_at': 'Résolu le {d}', 'inc.duration': 'Durée : {d}', 'inc.updates': 'Mises à jour',
    'inc.no_update': 'Aucune mise à jour publiée.', 'inc.show_updates': 'Voir les mises à jour ({n})',
    'sev.1': 'Impact critique', 'sev.2': 'Impact majeur', 'sev.3': 'Impact modéré', 'sev.4': 'Impact faible', 'sev.5': 'Information',
    powered: 'Propulsé par HYPHA, la supervision de S33D', generated: 'Données du {d}',
    'dur.min': 'min', 'dur.h': 'h', 'dur.d': 'j', 'dur.lt1': 'moins d’une minute', other: 'Autres',
  },
  en: {
    title: 'Status', loading: 'Loading…',
    'err.missing': 'Incomplete address: the page name is missing (?p=…).',
    'err.404': 'This status page does not exist or is not published.',
    'err.network': 'The server does not answer. Retrying in a minute.',
    'err.other': 'The page could not be loaded.', retry: 'Retry',
    'overall.operational': 'All systems operational',
    'overall.degraded': 'Degraded performance',
    'overall.partial_outage': 'Partial outage',
    'overall.major_outage': 'Major outage',
    'overall.maintenance': 'Maintenance in progress',
    'st.operational': 'Operational', 'st.degraded': 'Degraded', 'st.partial_outage': 'Partial outage',
    'st.major_outage': 'Major outage', 'st.maintenance': 'Maintenance', 'st.nodata': 'No data',
    updated: 'Updated at {t}', uptime: '{v} uptime', days90: 'Last 90 days', days30: 'Last 30 days', today: 'Today',
    'bars.label': '{name}: {v} uptime over {n} days, {bad} day(s) with an incident. Use the left and right arrows to browse days.',
    'day.uptime': '{v} uptime',
    components: 'Components', 'incidents.active': 'Ongoing incidents', 'incidents.history': 'Incident history (90 days)',
    'incidents.none': 'No incident in the last 90 days.', maintenances: 'Maintenance',
    'mt.active': 'In progress', 'mt.upcoming': 'Scheduled', 'mt.until': 'until {d}', 'mt.from_to': 'from {a} to {b}',
    'inc.active': 'Ongoing', 'inc.stable': 'Monitoring', 'inc.resolved': 'Resolved',
    'inc.started': 'Started: {d}', 'inc.resolved_at': 'Resolved on {d}', 'inc.duration': 'Duration: {d}', 'inc.updates': 'Updates',
    'inc.no_update': 'No update published.', 'inc.show_updates': 'Show updates ({n})',
    'sev.1': 'Critical impact', 'sev.2': 'Major impact', 'sev.3': 'Moderate impact', 'sev.4': 'Minor impact', 'sev.5': 'Information',
    powered: 'Powered by HYPHA, monitoring by S33D', generated: 'Data from {d}',
    'dur.min': 'min', 'dur.h': 'h', 'dur.d': 'd', 'dur.lt1': 'less than a minute', other: 'Other',
  },
};

const STATUSES = ['operational', 'degraded', 'partial_outage', 'major_outage', 'maintenance'];
const REFRESH_MS = 60000;
const params = new URLSearchParams(location.search);
const lang = (() => {
  const forced = params.get('lang');
  if (forced === 'fr' || forced === 'en') return forced;
  const list = (navigator.languages && navigator.languages.length ? navigator.languages : [navigator.language || 'fr']).map((x) => String(x).toLowerCase());
  for (const l of list) { if (l.startsWith('fr')) return 'fr'; if (l.startsWith('en')) return 'en'; }
  return 'fr';
})();
const LOCALE = lang === 'fr' ? 'fr-FR' : 'en-GB';
document.documentElement.setAttribute('lang', lang);

function t(key, p) {
  const s = DICT[lang][key] !== undefined ? DICT[lang][key] : DICT.fr[key] !== undefined ? DICT.fr[key] : key;
  return p ? s.replace(/\{(\w+)\}/g, (m, k) => (p[k] === undefined || p[k] === null ? '' : String(p[k]))) : s;
}

function el(tag, attrs, ...children) {
  const n = document.createElement(tag);
  if (attrs && (typeof attrs !== 'object' || attrs instanceof Node || Array.isArray(attrs))) { children.unshift(attrs); attrs = null; }
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === null || v === undefined || v === false) continue;
    if (k === 'class') n.className = Array.isArray(v) ? v.filter(Boolean).join(' ') : v;
    else if (k.startsWith('on') && typeof v === 'function') n.addEventListener(k.slice(2), v);
    else n.setAttribute(k, v === true ? '' : String(v));
  }
  const add = (c) => {
    if (c === null || c === undefined || c === false || c === true) return;
    if (Array.isArray(c)) { c.forEach(add); return; }
    n.appendChild(c instanceof Node ? c : document.createTextNode(String(c)));
  };
  children.forEach(add);
  return n;
}

const SVG = 'http://www.w3.org/2000/svg';
const PATHS = {
  operational: 'M5 12l5 5 9-10',
  degraded: 'M12 4l9 16H3zM12 10v4M12 17h.01',
  partial_outage: 'M12 4l9 16H3zM12 10v4M12 17h.01',
  major_outage: 'M6 6l12 12M18 6L6 18',
  maintenance: 'M14.7 6.3a4 4 0 0 0 5 5l-9.4 9.4a2.1 2.1 0 0 1-3-3zM14.7 6.3L17 4a4 4 0 0 1 3 3l-2.3 2.3',
};
function svgIcon(name, size = 18) {
  const s = document.createElementNS(SVG, 'svg');
  s.setAttribute('width', size); s.setAttribute('height', size); s.setAttribute('viewBox', '0 0 24 24');
  s.setAttribute('fill', 'none'); s.setAttribute('stroke', 'currentColor'); s.setAttribute('stroke-width', '2');
  s.setAttribute('stroke-linecap', 'round'); s.setAttribute('stroke-linejoin', 'round'); s.setAttribute('aria-hidden', 'true');
  const p = document.createElementNS(SVG, 'path');
  p.setAttribute('d', PATHS[name] || PATHS.operational);
  s.appendChild(p);
  return s;
}

const toMs = (v) => {
  if (typeof v === 'number' && Number.isFinite(v)) return v;
  if (typeof v === 'string' && v) { const n = Number(v); if (Number.isFinite(n)) return n; const d = Date.parse(v); return Number.isFinite(d) ? d : null; }
  return null;
};
const dtf = (o) => new Intl.DateTimeFormat(LOCALE, o);
const fmtDateTime = (ms) => (ms === null ? '' : dtf({ day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' }).format(ms));
const fmtTime = (ms) => dtf({ hour: '2-digit', minute: '2-digit' }).format(ms);
const fmtDay = (ms) => dtf({ weekday: 'short', day: 'numeric', month: 'short', year: 'numeric', timeZone: 'UTC' }).format(ms);
const fmtPct = (v) => (typeof v === 'number' && Number.isFinite(v)
  ? new Intl.NumberFormat(LOCALE, { maximumFractionDigits: v >= 99.99 && v < 100 ? 3 : 2, minimumFractionDigits: v === 100 ? 0 : 2 }).format(v) + (lang === 'fr' ? ' %' : '%')
  : '-');
function fmtDuration(ms) {
  if (!(ms > 0)) return t('dur.lt1');
  const m = Math.round(ms / 60000);
  if (m < 1) return t('dur.lt1');
  if (m < 60) return m + ' ' + t('dur.min');
  const hTot = Math.floor(m / 60);
  if (hTot < 24) return hTot + ' ' + t('dur.h') + (m % 60 ? ' ' + String(m % 60).padStart(2, '0') + ' ' + t('dur.min') : '');
  return Math.floor(hTot / 24) + ' ' + t('dur.d') + (hTot % 24 ? ' ' + (hTot % 24) + ' ' + t('dur.h') : '');
}
const statusOf = (s) => (STATUSES.includes(s) ? s : 'operational');

// ── Infobulle des jours ──────────────────────────────────────────────────
const tip = el('div', { class: 'sp-tip', role: 'tooltip', hidden: true });
document.body.appendChild(tip);
function showTip(target, day) {
  tip.textContent = '';
  const ms = Date.parse(day.date + 'T00:00:00Z');
  tip.appendChild(el('b', Number.isFinite(ms) ? fmtDay(ms) : day.date));
  const has = typeof day.uptime === 'number' && Number.isFinite(day.uptime);
  tip.appendChild(el('div', has ? t('day.uptime', { v: fmtPct(day.uptime) }) : t('st.nodata')));
  if (has) tip.appendChild(el('div', { class: 'sp-tip-st s-' + statusOf(day.worst) }, el('i'), t('st.' + statusOf(day.worst))));
  tip.hidden = false;
  const r = target.getBoundingClientRect();
  const tw = tip.offsetWidth;
  const th = tip.offsetHeight;
  let x = r.left + r.width / 2 - tw / 2 + window.scrollX;
  x = Math.max(8 + window.scrollX, Math.min(x, window.scrollX + document.documentElement.clientWidth - tw - 8));
  let y = r.top + window.scrollY - th - 8;
  if (y < window.scrollY + 4) y = r.bottom + window.scrollY + 8;
  tip.style.transform = 'translate(' + Math.round(x) + 'px,' + Math.round(y) + 'px)';
}
function hideTip() { tip.hidden = true; }
document.addEventListener('scroll', hideTip, { passive: true });

function normDays(days) {
  const list = Array.isArray(days) ? days.filter((d) => d && d.date).slice().sort((a, b) => String(a.date).localeCompare(String(b.date))) : [];
  const out = list.slice(-90);
  // Moins de 90 jours connus : on complète à gauche par des jours sans donnée.
  if (out.length < 90) {
    const first = out.length ? Date.parse(out[0].date + 'T00:00:00Z') : Date.UTC(new Date().getUTCFullYear(), new Date().getUTCMonth(), new Date().getUTCDate());
    const pad = [];
    for (let i = 90 - out.length; i > 0; i--) pad.push({ date: new Date(first - i * 86400e3 + (out.length ? 0 : 86400e3)).toISOString().slice(0, 10), uptime: null, worst: null });
    return pad.concat(out);
  }
  return out;
}

function dayClass(d) {
  if (!(typeof d.uptime === 'number' && Number.isFinite(d.uptime))) return 'nodata';
  return statusOf(d.worst);
}

function bars(comp) {
  const days = normDays(comp.days);
  const bad = days.filter((d) => ['partial_outage', 'major_outage', 'degraded'].includes(dayClass(d))).length;
  const box = el('div', {
    class: 'sp-bars', tabindex: '0', role: 'img',
    'aria-label': t('bars.label', { name: comp.name || '', v: fmtPct(comp.uptime_90d), n: 90, bad }),
  });
  const spans = days.map((d, i) => {
    const s = el('span', { class: ['sp-day', 'd-' + dayClass(d), i < 60 ? 'old' : null] });
    s.addEventListener('mouseenter', () => showTip(s, d));
    s.addEventListener('mouseleave', hideTip);
    s.addEventListener('click', (e) => { e.stopPropagation(); showTip(s, d); });
    box.appendChild(s);
    return s;
  });
  let cur = days.length - 1;
  const focusDay = () => { spans.forEach((s, i) => s.classList.toggle('kb', i === cur)); showTip(spans[cur], days[cur]); };
  box.addEventListener('focus', () => { cur = days.length - 1; focusDay(); });
  box.addEventListener('blur', () => { spans.forEach((s) => s.classList.remove('kb')); hideTip(); });
  box.addEventListener('keydown', (e) => {
    const narrow = window.matchMedia('(max-width: 640px)').matches;
    const min = narrow ? 60 : 0;
    if (e.key === 'ArrowLeft') { e.preventDefault(); cur = Math.max(min, cur - 1); focusDay(); }
    else if (e.key === 'ArrowRight') { e.preventDefault(); cur = Math.min(days.length - 1, cur + 1); focusDay(); }
    else if (e.key === 'Home') { e.preventDefault(); cur = min; focusDay(); }
    else if (e.key === 'End') { e.preventDefault(); cur = days.length - 1; focusDay(); }
    else if (e.key === 'Escape') { hideTip(); }
  });
  return el('div', { class: 'sp-bars-wrap' }, box,
    el('div', { class: 'sp-bars-foot' }, el('span', { class: 'wide' }, t('days90')), el('span', { class: 'narrow' }, t('days30')), el('span', { class: 'line' }),
      el('span', t('uptime', { v: fmtPct(comp.uptime_90d) })), el('span', { class: 'line' }), el('span', t('today'))));
}

function componentRow(c) {
  const st = statusOf(c.status);
  return el('div', { class: 'sp-comp' },
    el('div', { class: 'sp-comp-head' }, el('span', { class: 'sp-comp-name' }, c.name || ''),
      el('span', { class: ['sp-cstatus', 's-' + st] }, svgIcon(st, 15), t('st.' + st))),
    bars(c));
}

function incidentCard(inc, open) {
  const sev = Math.min(5, Math.max(1, Number(inc.severity) || 3));
  const started = toMs(inc.started);
  const resolved = toMs(inc.resolved);
  const status = ['active', 'stable', 'resolved'].includes(inc.status) ? inc.status : (resolved ? 'resolved' : 'active');
  const updates = (Array.isArray(inc.updates) ? inc.updates : []).slice().sort((a, b) => (toMs(b.ts) || 0) - (toMs(a.ts) || 0));
  const list = el('ol', { class: 'sp-updates' }, updates.map((u) => el('li', el('time', { datetime: new Date(toMs(u.ts) || 0).toISOString() }, fmtDateTime(toMs(u.ts))), el('p', String(u.body || '')))));
  const meta = [t('inc.started', { d: fmtDateTime(started) })];
  if (resolved) { meta.push(t('inc.resolved_at', { d: fmtDateTime(resolved) })); if (started) meta.push(t('inc.duration', { d: fmtDuration(resolved - started) })); }
  return el('article', { class: ['sp-inc', 'sev' + sev, status === 'resolved' ? 'done' : null] },
    el('div', { class: 'sp-inc-head' }, el('h3', String(inc.title || '')), el('span', { class: ['sp-inc-st', 'i-' + status] }, t('inc.' + status))),
    el('div', { class: 'sp-inc-meta' }, el('span', t('sev.' + sev)), meta.map((m) => el('span', m))),
    open ? (updates.length ? list : el('p', { class: 'sp-muted' }, t('inc.no_update')))
      : (updates.length ? el('details', el('summary', t('inc.show_updates', { n: updates.length })), list) : null));
}

const app = document.getElementById('app');
const slug = (params.get('p') || '').trim();
let timer = null;

function showError(kind, retry) {
  app.textContent = '';
  app.removeAttribute('aria-busy');
  app.appendChild(el('div', { class: 'sp-error', role: 'alert' }, el('p', t('err.' + kind)),
    retry ? el('button', { class: 'sp-btn', type: 'button', onclick: () => load(true) }, t('retry')) : null));
}

function render(d) {
  const overall = statusOf(d.overall);
  document.title = (d.title || t('title')) + ' · ' + t('title');
  app.textContent = '';
  app.removeAttribute('aria-busy');
  const gen = toMs(d.generated_at) || Date.now();
  app.appendChild(el('header', { class: 'sp-head' }, el('h1', String(d.title || t('title'))), d.description ? el('p', { class: 'sp-desc' }, String(d.description)) : null));
  app.appendChild(el('section', { class: ['sp-overall', 's-' + overall], role: 'status', 'aria-live': 'polite' },
    el('span', { class: 'sp-overall-ico' }, svgIcon(overall, 22)), el('span', { class: 'sp-overall-label' }, t('overall.' + overall)),
    el('span', { class: 'sp-overall-time' }, t('updated', { t: fmtTime(gen) }))));

  const incidents = Array.isArray(d.incidents) ? d.incidents : [];
  const active = incidents.filter((i) => i.status !== 'resolved' && !toMs(i.resolved));
  const past = incidents.filter((i) => !active.includes(i)).sort((a, b) => (toMs(b.started) || 0) - (toMs(a.started) || 0));
  if (active.length) app.appendChild(el('section', { class: 'sp-section', 'aria-labelledby': 'sp-h-active' }, el('h2', { id: 'sp-h-active' }, t('incidents.active')), active.map((i) => incidentCard(i, true))));

  const now = Date.now();
  const mts = (Array.isArray(d.maintenances) ? d.maintenances : []).slice().sort((a, b) => (toMs(a.start) || 0) - (toMs(b.start) || 0));
  if (mts.length) {
    app.appendChild(el('section', { class: 'sp-section', 'aria-labelledby': 'sp-h-mt' }, el('h2', { id: 'sp-h-mt' }, t('maintenances')),
      el('ul', { class: 'sp-mts' }, mts.map((m) => {
        const a = toMs(m.start);
        const b = toMs(m.end);
        const on = a !== null && a <= now && (b === null || b > now);
        return el('li', { class: on ? 'on' : null }, el('span', { class: 'sp-mt-ico' }, svgIcon('maintenance', 16)),
          el('div', el('b', String(m.name || '')), el('div', { class: 'sp-muted' }, on ? t('mt.active') + (b ? ' · ' + t('mt.until', { d: fmtDateTime(b) }) : '')
            : t('mt.upcoming') + ' · ' + (b ? t('mt.from_to', { a: fmtDateTime(a), b: fmtDateTime(b) }) : fmtDateTime(a)))));
      }))));
  }

  const comps = Array.isArray(d.components) ? d.components : [];
  if (comps.length) {
    const groups = [];
    for (const c of comps) {
      const g = c.group || '';
      let e = groups.find((x) => x.name === g);
      if (!e) { e = { name: g, items: [] }; groups.push(e); }
      e.items.push(c);
    }
    const sec = el('section', { class: 'sp-section', 'aria-labelledby': 'sp-h-comp' }, el('h2', { id: 'sp-h-comp' }, t('components')));
    const named = groups.some((g) => g.name);
    for (const g of groups) {
      const worst = g.items.reduce((acc, c) => (STATUSES.indexOf(statusOf(c.status)) > STATUSES.indexOf(acc) && statusOf(c.status) !== 'maintenance' ? statusOf(c.status) : acc), 'operational');
      sec.appendChild(el('div', { class: 'sp-group' },
        named ? el('div', { class: 'sp-group-head' }, el('h3', g.name || t('other')), el('span', { class: ['sp-cstatus', 's-' + worst] }, svgIcon(worst, 14), t('st.' + worst))) : null,
        g.items.map(componentRow)));
    }
    app.appendChild(sec);
  }

  app.appendChild(el('section', { class: 'sp-section', 'aria-labelledby': 'sp-h-hist' }, el('h2', { id: 'sp-h-hist' }, t('incidents.history')),
    past.length ? past.map((i) => incidentCard(i, false)) : el('p', { class: 'sp-muted' }, t('incidents.none'))));

  app.appendChild(el('footer', { class: 'sp-foot' }, el('span', t('powered')), el('span', t('generated', { d: fmtDateTime(gen) }))));
}

async function load(manual) {
  if (!slug) { showError('missing', false); return; }
  try {
    const res = await fetch('/api/public/status/' + encodeURIComponent(slug), { credentials: 'omit', headers: { Accept: 'application/json' }, cache: 'no-store' });
    if (res.status === 404) { showError('404', false); stop(); return; }
    if (!res.ok) throw new Error('HTTP ' + res.status);
    const data = await res.json();
    if (!data || typeof data !== 'object') throw new Error('json');
    render(data);
  } catch (e) {
    // Panne passagère : on garde la page déjà affichée ; sinon message avec nouvelle tentative.
    if (!app.querySelector('.sp-overall') || manual) showError(e instanceof TypeError ? 'network' : 'other', true);
  }
}

function stop() { if (timer) clearInterval(timer); timer = null; }

document.addEventListener('click', hideTip);
load();
timer = setInterval(() => { if (document.visibilityState === 'visible') load(); }, REFRESH_MS);
document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'visible' && timer) load(); });
