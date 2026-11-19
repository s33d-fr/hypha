// Client de l'API HYPHA (§ 7). Même domaine, cookie de session, aucune clé dans le navigateur.

export class ApiError extends Error {
  constructor(status, code, detail, body) {
    super(detail || code || ('HTTP ' + status));
    this.name = 'ApiError';
    this.status = status;
    this.code = code || '';
    this.detail = detail || '';
    this.body = body || null;
  }
}

const listeners = new Set();
export function onUnauthorized(fn) { listeners.add(fn); return () => listeners.delete(fn); }

function buildUrl(path, query) {
  const base = path.startsWith('/api/') || path.startsWith('/v1/') ? path : '/api/v1' + (path.startsWith('/') ? path : '/' + path);
  if (!query) return base;
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v === undefined || v === null || v === '') continue;
    if (Array.isArray(v)) v.forEach((x) => qs.append(k, x));
    else qs.append(k, String(v));
  }
  const str = qs.toString();
  return str ? base + '?' + str : base;
}

export async function request(method, path, opts = {}) {
  const { query, body, signal, quiet401 } = opts;
  const init = { method, credentials: 'same-origin', headers: { Accept: 'application/json' }, signal };
  if (body !== undefined) {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(body);
  }
  let res;
  try {
    res = await fetch(buildUrl(path, query), init);
  } catch (e) {
    if (e && e.name === 'AbortError') throw e;
    throw new ApiError(0, 'network', '', null);
  }
  let data = null;
  const ct = res.headers.get('Content-Type') || '';
  if (res.status !== 204) {
    if (ct.includes('application/json')) {
      try { data = await res.json(); } catch (e) { data = null; }
    } else {
      try { data = await res.text(); } catch (e) { data = null; }
    }
  }
  if (!res.ok) {
    const code = data && typeof data === 'object' ? data.error : '';
    const detail = data && typeof data === 'object' ? data.detail : '';
    const err = new ApiError(res.status, code, detail, data);
    if (res.status === 401 && !quiet401) listeners.forEach((fn) => { try { fn(err); } catch (e) { /* rien */ } });
    throw err;
  }
  return data;
}

export const get = (path, query, opts = {}) => request('GET', path, Object.assign({ query }, opts));
export const post = (path, body, opts = {}) => request('POST', path, Object.assign({ body: body === undefined ? {} : body }, opts));
export const put = (path, body, opts = {}) => request('PUT', path, Object.assign({ body }, opts));
export const del = (path, opts = {}) => request('DELETE', path, opts);

export function isAbort(e) { return e && e.name === 'AbortError'; }

// Les listes peuvent arriver nues ou enveloppées ({items:[…]}, {hosts:[…]}…) : on accepte les deux.
export function asList(data, ...keys) {
  if (Array.isArray(data)) return data;
  if (data && typeof data === 'object') {
    for (const k of keys.concat(['items', 'data', 'results'])) if (Array.isArray(data[k])) return data[k];
  }
  return [];
}

// Requête de métriques (§ 7) : une seule requête HTTP pour plusieurs séries.
// extra (§ 30) : {formulas:[{id, formula, alias}]} ; extra.path remplace la route (partage public).
export function queryMetrics(queries, range, maxPoints, signal, extra) {
  const body = {
    from: range.from,
    to: range.to,
    queries: queries.map((q) => ({ id: q.id, q: q.q })),
    max_points: maxPoints || 300,
  };
  if (extra && Array.isArray(extra.formulas) && extra.formulas.length) body.formulas = extra.formulas;
  return post((extra && extra.path) || '/metrics/query', body, { signal });
}

// Exécute des tâches avec une concurrence limitée (respect du serveur et du poste).
export async function pool(items, limit, fn) {
  const out = new Array(items.length);
  let i = 0;
  const workers = Array.from({ length: Math.min(limit, items.length) }, async () => {
    while (i < items.length) {
      const idx = i++;
      try { out[idx] = await fn(items[idx], idx); } catch (e) { out[idx] = e; }
    }
  });
  await Promise.all(workers);
  return out;
}
