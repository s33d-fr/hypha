// Routage par fragment : #/chemin/segments?param=valeur. Aucune réécriture côté nginx.

export function parseHash(hash = location.hash) {
  let raw = (hash || '').replace(/^#/, '');
  if (!raw.startsWith('/')) raw = '/' + raw;
  const qi = raw.indexOf('?');
  const pathPart = qi >= 0 ? raw.slice(0, qi) : raw;
  const query = qi >= 0 ? raw.slice(qi + 1) : '';
  const segs = pathPart.split('/').filter(Boolean).map((x) => {
    try { return decodeURIComponent(x); } catch (e) { return x; }
  });
  return { path: '/' + segs.map(encodeURIComponent).join('/'), segs, params: new URLSearchParams(query) };
}

export function buildHash(path, params) {
  let qs = '';
  if (params) {
    const p = params instanceof URLSearchParams ? params : new URLSearchParams();
    if (!(params instanceof URLSearchParams)) {
      for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== null && v !== '') p.set(k, v);
    }
    qs = p.toString();
  }
  return '#' + (path.startsWith('/') ? path : '/' + path) + (qs ? '?' + qs : '');
}

// Paramètres globaux conservés d'une page à l'autre (période).
const STICKY = ['t', 'from', 'to'];

export function stickyParams(current = parseHash().params) {
  const p = new URLSearchParams();
  for (const k of STICKY) if (current.has(k)) p.set(k, current.get(k));
  return p;
}

export function href(path, params) {
  const p = stickyParams();
  if (params) for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === '') p.delete(k); else p.set(k, v);
  }
  return buildHash(path, p);
}

export function navigate(path, params, opts = {}) {
  const h = href(path, params);
  if (opts.replace) {
    history.replaceState(null, '', h);
    window.dispatchEvent(new HashChangeEvent('hashchange'));
  } else if (location.hash === h) {
    window.dispatchEvent(new HashChangeEvent('hashchange'));
  } else {
    location.hash = h;
  }
}

// Met à jour des paramètres. silent : l'URL change sans nouveau rendu (état d'affichage).
export function setParams(updates, opts = {}) {
  const cur = parseHash();
  const p = new URLSearchParams(cur.params);
  for (const [k, v] of Object.entries(updates)) {
    if (v === undefined || v === null || v === '') p.delete(k); else p.set(k, String(v));
  }
  const h = buildHash(cur.path, p);
  if (h === location.hash) return;
  if (opts.silent) {
    history.replaceState(null, '', h);
  } else if (opts.replace) {
    history.replaceState(null, '', h);
    window.dispatchEvent(new HashChangeEvent('hashchange'));
  } else {
    location.hash = h;
  }
}

// Table des routes : motifs « /a/:b ». Le premier motif qui correspond gagne.
export function matchRoute(routes, segs) {
  for (const r of routes) {
    const pat = r.path.split('/').filter(Boolean);
    if (pat.length !== segs.length) continue;
    const args = {};
    let ok = true;
    for (let i = 0; i < pat.length; i++) {
      if (pat[i].startsWith(':')) args[pat[i].slice(1)] = segs[i];
      else if (pat[i] !== segs[i]) { ok = false; break; }
    }
    if (ok) return { route: r, args };
  }
  return null;
}
