// Accueil public : présentation de HYPHA et connexion (SSO S33D, ou compte local en auto-hébergé).
import { h, icon, mount } from '../dom.js';
import { t, getLang, setLang, languages } from '../i18n.js';
import { post } from '../api.js';

const POINTS = [
  ['infra', 'public.p.infra.t', 'public.p.infra.d'],
  ['logs', 'public.p.logs.t', 'public.p.logs.d'],
  ['apm', 'public.p.apm.t', 'public.p.apm.d'],
  ['monitor', 'public.p.monitors.t', 'public.p.monitors.d'],
  ['dashboard', 'public.p.dash.t', 'public.p.dash.d'],
  ['synthetic', 'public.p.synth.t', 'public.p.synth.d'],
];

// Pages publiques (documentation, tarifs, FAQ, pages légales) : générées par site/build.py, une
// adresse par langue.
const SITE_PAGES = {
  fr: { docs: '/docs/', pricing: '/tarifs/', faq: '/faq/', legal: '/mentions-legales/', terms: '/cgv/', privacy: '/confidentialite/', self: '/docs/auto-hebergement/' },
  en: { docs: '/en/docs/', pricing: '/en/pricing/', faq: '/en/faq/', legal: '/en/legal-notice/', terms: '/en/terms/', privacy: '/en/privacy/', self: '/en/docs/self-hosting/' },
};
export function sitePage(key) { return (SITE_PAGES[getLang()] || SITE_PAGES.fr)[key]; }

function connectUrl() {
  const next = '/' + (location.hash || '#/overview');
  return '/api/auth/connexion?next=' + encodeURIComponent(next);
}

// Mode inconnu : on essaie POST /api/auth/local sans identifiants. La route n'existe qu'en auto-hébergé.
async function probeMode() {
  try {
    await post('/api/auth/local', {}, { quiet401: true });
    return 'selfhosted';
  } catch (e) {
    const code = String((e && e.code) || '').toLowerCase();
    if (/saas|not_selfhosted|disabled|not_available|unsupported/.test(code)) return 'saas';
    if (e && (e.status === 400 || e.status === 401 || e.status === 422)) return 'selfhosted';
    return 'saas';
  }
}

export function renderPublic(root, info) {
  let mode = info.mode || null;
  const card = h('div', { class: 'login-card' });

  function ssoButton(primary) {
    return h('a', { class: ['btn', primary ? 'primary' : null], href: connectUrl() }, icon('lock', 15), t('auth.login'));
  }

  function localForm() {
    const login = h('input', { class: 'input', name: 'username', autocomplete: 'username', required: true, placeholder: t('auth.login_id') });
    const pwd = h('input', { class: 'input', type: 'password', name: 'password', autocomplete: 'current-password', required: true, placeholder: t('auth.password') });
    const err = h('div', { class: 'small', style: { color: 'var(--crit)' }, hidden: true, role: 'alert' });
    const submit = h('button', { class: 'btn primary', type: 'submit' }, t('auth.login'));
    const form = h('form', { novalidate: true },
      h('label', { class: 'field' }, h('span', { class: 'lbl' }, t('auth.login_id')), login),
      h('label', { class: 'field' }, h('span', { class: 'lbl' }, t('auth.password')), pwd),
      err, submit);
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      err.hidden = true;
      if (!login.value.trim() || !pwd.value) { err.textContent = t('auth.fill_both'); err.hidden = false; return; }
      submit.disabled = true;
      try {
        await post('/api/auth/local', { login: login.value.trim(), password: pwd.value }, { quiet401: true });
        info.onLoggedIn();
      } catch (ex) {
        submit.disabled = false;
        err.textContent = ex.status === 401 || ex.status === 403 ? t('auth.bad_credentials') : ex.status === 429 ? t('err.429') : ex.status === 0 ? t('err.network') : (ex.detail || t('err.unknown'));
        err.hidden = false;
        pwd.value = '';
        pwd.focus();
      }
    });
    return form;
  }

  function renderCard() {
    const kids = [];
    if (info.expired) kids.push(h('div', { class: 'callout mb-3' }, icon('info', 16), h('span', t('auth.expired'))));
    if (mode === 'selfhosted') {
      kids.push(h('h2', t('auth.local_title')), h('p', { class: 'muted' }, t('auth.local_text')), localForm());
      if (info.oidc) kids.push(h('p', { class: 'or' }, t('auth.or')), ssoButton(false));
    } else {
      kids.push(h('h2', t('auth.title')), h('p', { class: 'muted' }, t('auth.sso_text')), ssoButton(true));
      if (mode === null) kids.push(h('p', { class: 'or' }, h('span', { class: 'spinner' })));
      // Données chez soi : le kit d'auto-hébergement (docker-compose et notice).
      kids.push(h('p', { class: 'or' }, t('public.self_q'), ' ', h('a', { href: sitePage('self') }, t('public.self_link'))));
    }
    mount(card, kids);
  }

  const langSwitch = h('div', { class: 'pill-group' }, languages().map((l) => h('button', {
    type: 'button', class: l.code === getLang() ? 'on' : null,
    onclick: () => { setLang(l.code); renderPublic(root, Object.assign({}, info, { mode })); },
  }, l.code.toUpperCase())));

  const page = h('div', { class: 'public' },
    h('header', { class: 'public-top' },
      h('span', { class: 'brand' }, h('img', { src: 'assets/img/hypha.svg', alt: '' }), h('span', { class: 'brand-name' }, 'HYPHA', h('small', t('brand.by')))),
      h('span', { class: 'spacer' }),
      h('a', { class: 'btn ghost sm', href: sitePage('docs') }, t('public.foot.docs')),
      h('a', { class: 'btn ghost sm', href: sitePage('pricing') }, t('public.foot.pricing')),
      langSwitch),
    h('main', { class: 'public-main' },
      h('section', { class: 'hero' },
        h('h1', t('public.headline')),
        h('p', { class: 'lead' }, t('public.lead')),
        h('div', { class: 'public-cover' },
          ['public.c.hosted', 'public.c.self', 'public.c.free', 'public.c.lang'].map((k) => h('span', { class: 'badge info' }, t(k)))),
        h('div', { class: 'points' }, POINTS.map(([ic, ti, de]) => h('div', { class: 'point' }, icon(ic, 18), h('div', h('b', t(ti)), h('span', t(de))))))),
      h('aside', card)),
    h('footer', { class: 'public-foot' },
      h('span', t('public.foot.product')),
      h('span', t('public.foot.where')),
      ['docs', 'pricing', 'faq', 'legal', 'terms', 'privacy'].map((k) => h('a', { href: sitePage(k) }, t('public.foot.' + k))),
      h('a', { href: 'https://s33d.fr', rel: 'noopener' }, 's33d.fr')));

  mount(root, page);
  document.title = 'HYPHA · ' + t('public.title_suffix');
  renderCard();
  if (mode === null) {
    probeMode().then((m) => { mode = m; if (card.isConnected) renderCard(); });
  }
}
