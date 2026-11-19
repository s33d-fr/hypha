// Moniteurs : lecture tolérante des champs d'état renvoyés par le serveur.

export const MONITOR_TYPES = ['metric', 'host', 'service_check', 'log', 'synthetic', 'apm', 'slo',
  'anomaly', 'forecast', 'outlier', 'composite', 'change', 'event', 'process', 'error_tracking'];
export const STATES = ['ALERT', 'WARN', 'NODATA', 'OK'];

export function alertName(a) {
  return a.monitor_name || a.name || (a.monitor && a.monitor.name) || ('#' + (a.monitor_id !== undefined ? a.monitor_id : ''));
}

export function alertGroup(a) {
  const g = a.group_key !== undefined ? a.group_key : a.group;
  if (g && typeof g === 'object') return Object.entries(g).map(([k, v]) => k + ':' + v).join(',') || '*';
  return g || '*';
}

export function alertState(a) {
  return String(a.state || a.to_state || 'OK').toUpperCase();
}

// État global d'un moniteur : champ calculé par le serveur (§ 7 « avec état global calculé »).
export function monitorState(m) {
  const st = m.overall_state || m.state || m.status;
  if (st) return String(st).toUpperCase();
  if (Array.isArray(m.states) && m.states.length) {
    const list = m.states.map((x) => String(x.state || 'OK').toUpperCase());
    for (const s2 of STATES) if (list.includes(s2)) return s2;
  }
  return 'OK';
}

export function isMuted(m, now = Date.now()) {
  if (m.muted === true) return true;
  if (m.muted_until === null || m.muted_until === undefined) return false;
  const ms = typeof m.muted_until === 'number' ? m.muted_until : Date.parse(m.muted_until);
  return Number.isFinite(ms) && ms > now;
}

export function mutedUntilMs(m) {
  if (m.muted_until === null || m.muted_until === undefined) return null;
  const ms = typeof m.muted_until === 'number' ? m.muted_until : Date.parse(m.muted_until);
  return Number.isFinite(ms) ? ms : null;
}

export function stateRank(st) {
  const i = STATES.indexOf(st);
  return i < 0 ? STATES.length : i;
}
