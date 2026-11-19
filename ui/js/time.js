// Période globale : préréglages relatifs (t=1h) ou plage fixe (from=…&to=…), toujours dans l'URL.

export const PRESETS = [
  { key: '15m', ms: 15 * 60e3 },
  { key: '1h', ms: 3600e3 },
  { key: '4h', ms: 4 * 3600e3 },
  { key: '1d', ms: 86400e3 },
  { key: '7d', ms: 7 * 86400e3 },
  { key: '30d', ms: 30 * 86400e3 },
];

export const DEFAULT_PRESET = '1h';

export function presetMs(key) {
  const p = PRESETS.find((x) => x.key === key);
  return p ? p.ms : null;
}

// params : URLSearchParams de la route courante.
export function rangeFromParams(params) {
  const from = Number(params.get('from'));
  const to = Number(params.get('to'));
  if (Number.isFinite(from) && Number.isFinite(to) && from > 0 && to > from) {
    return { from: Math.round(from), to: Math.round(to), preset: null, live: false };
  }
  const key = params.get('t') || DEFAULT_PRESET;
  const ms = presetMs(key) || presetMs(DEFAULT_PRESET);
  const now = Date.now();
  return { from: now - ms, to: now, preset: presetMs(key) ? key : DEFAULT_PRESET, live: true };
}

export function rangeParams(range) {
  if (range.preset) return { t: range.preset, from: null, to: null };
  return { t: null, from: String(Math.round(range.from)), to: String(Math.round(range.to)) };
}

export function spanMs(range) { return range.to - range.from; }
