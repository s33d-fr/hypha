// Mise en forme des nombres, unités, durées et dates, selon la langue choisie.
import { t, locale, getLang } from './i18n.js';

export const EMPTY = '-';
const NBSP = ' ';
const cache = new Map();

function nf(min, max, extra) {
  const key = getLang() + '|' + min + '|' + max + '|' + (extra ? JSON.stringify(extra) : '');
  let f = cache.get(key);
  if (!f) {
    f = new Intl.NumberFormat(locale(), Object.assign({ minimumFractionDigits: min, maximumFractionDigits: max }, extra || {}));
    cache.set(key, f);
  }
  return f;
}

export function isNum(v) { return typeof v === 'number' && Number.isFinite(v); }

function autoDigits(abs) {
  if (abs === 0) return 0;
  if (abs >= 100) return 0;
  if (abs >= 10) return 1;
  if (abs >= 1) return 2;
  if (abs >= 0.01) return 3;
  return 4;
}

export function fmtNum(v, digits) {
  if (!isNum(v)) return EMPTY;
  const d = digits === undefined || digits === null ? autoDigits(Math.abs(v)) : digits;
  return nf(0, d).format(v);
}

export function fmtInt(v) {
  if (!isNum(v)) return EMPTY;
  return nf(0, 0).format(v);
}

export function fmtCompact(v, digits = 1) {
  if (!isNum(v)) return EMPTY;
  if (Math.abs(v) < 1000) return fmtNum(v, Math.abs(v) < 10 ? Math.min(2, digits + 1) : digits);
  return nf(0, digits, { notation: 'compact', compactDisplay: 'short' }).format(v);
}

export function fmtPct(v, digits) {
  if (!isNum(v)) return EMPTY;
  const d = digits === undefined || digits === null ? (Math.abs(v) >= 10 ? 0 : 1) : digits;
  return nf(0, d).format(v) + (getLang() === 'fr' ? NBSP + '%' : '%');
}

// Ratio 0..1 affiché en pourcentage.
export function fmtRatioPct(v, digits) {
  return isNum(v) ? fmtPct(v * 100, digits) : EMPTY;
}

const BYTE_KEYS = ['unit.b', 'unit.kib', 'unit.mib', 'unit.gib', 'unit.tib', 'unit.pib'];

export function fmtBytes(v, digits) {
  if (!isNum(v)) return EMPTY;
  const abs = Math.abs(v);
  let i = 0;
  let x = abs;
  while (x >= 1024 && i < BYTE_KEYS.length - 1) { x /= 1024; i++; }
  const d = digits === undefined || digits === null ? (i === 0 ? 0 : x >= 100 ? 0 : 1) : digits;
  return (v < 0 ? '-' : '') + nf(0, d).format(x) + NBSP + t(BYTE_KEYS[i]);
}

export function fmtBps(v, digits) {
  if (!isNum(v)) return EMPTY;
  return fmtBytes(v, digits) + t('unit.per_s');
}

// Durée en millisecondes, lisible : 0,42 ms · 128 ms · 1,25 s · 3 min 04 s · 2 h 05 min · 3 j 4 h.
export function fmtDuration(ms, short) {
  if (!isNum(ms)) return EMPTY;
  const a = Math.abs(ms);
  const sign = ms < 0 ? '-' : '';
  if (a < 1) return sign + nf(0, 2).format(a) + NBSP + t('dur.ms');
  if (a < 1000) return sign + nf(0, a < 10 ? 1 : 0).format(a) + NBSP + t('dur.ms');
  const sec = a / 1000;
  if (sec < 60) return sign + nf(0, sec < 10 ? 2 : 1).format(sec) + NBSP + t('dur.s');
  const totalS = Math.round(sec);
  if (totalS < 3600) {
    const m = Math.floor(totalS / 60);
    const s2 = totalS % 60;
    return sign + m + NBSP + t('dur.min') + (short || s2 === 0 ? '' : NBSP + String(s2).padStart(2, '0') + NBSP + t('dur.s'));
  }
  if (totalS < 86400) {
    const hh = Math.floor(totalS / 3600);
    const mm = Math.floor((totalS % 3600) / 60);
    return sign + hh + NBSP + t('dur.h') + (short || mm === 0 ? '' : NBSP + String(mm).padStart(2, '0') + NBSP + t('dur.min'));
  }
  const dd = Math.floor(totalS / 86400);
  const hh = Math.floor((totalS % 86400) / 3600);
  return sign + dd + NBSP + t('dur.d') + (short || hh === 0 ? '' : NBSP + hh + NBSP + t('dur.h'));
}

// Valeur selon l'unité du contrat (§ 4).
export function fmtUnit(v, unit, precision) {
  if (!isNum(v)) return EMPTY;
  switch (unit) {
    case 'percent': return fmtPct(v, precision);
    case 'byte': return fmtBytes(v, precision);
    case 'byte_per_second': return fmtBps(v, precision);
    case 'operation_per_second': return fmtNum(v, precision) + NBSP + t('unit.ops');
    case 'packet_per_second': return fmtNum(v, precision) + NBSP + t('unit.pps');
    case 'second': return fmtDuration(v * 1000);
    case 'millisecond': return fmtDuration(v);
    case 'nanosecond': return fmtDuration(v / 1e6);
    case 'count': return Math.abs(v) >= 10000 ? fmtCompact(v) : fmtNum(v, precision);
    default:
      if (unit && typeof unit === 'string' && unit.length <= 12) return fmtNum(v, precision) + NBSP + unit;
      return Math.abs(v) >= 100000 ? fmtCompact(v) : fmtNum(v, precision);
  }
}

// Libellés d'axe : courts, sans décimales superflues.
export function fmtAxis(v, unit) {
  if (!isNum(v)) return '';
  switch (unit) {
    case 'percent': return fmtPct(v, Math.abs(v) < 10 && v % 1 !== 0 ? 1 : 0);
    case 'byte': return fmtBytes(v, null);
    case 'byte_per_second': return fmtBps(v, null);
    case 'second': return fmtDuration(v * 1000, true);
    case 'millisecond': return fmtDuration(v, true);
    default: return fmtCompact(v, 1);
  }
}

export function unitLabel(unit) {
  if (!unit) return '';
  const key = 'unitname.' + unit;
  const s2 = t(key);
  return s2 === key ? unit : s2;
}

// ── Dates ────────────────────────────────────────────────────────────────
const dfCache = new Map();
function df(opts) {
  const key = getLang() + JSON.stringify(opts);
  let f = dfCache.get(key);
  if (!f) { f = new Intl.DateTimeFormat(locale(), opts); dfCache.set(key, f); }
  return f;
}

export function toMs(ts) {
  if (isNum(ts)) return ts;
  if (typeof ts === 'string' && ts) {
    const n = Number(ts);
    if (Number.isFinite(n)) return n;
    const p = Date.parse(ts);
    return Number.isFinite(p) ? p : null;
  }
  return null;
}

export function fmtTime(ts) {
  const ms = toMs(ts);
  if (ms === null) return EMPTY;
  return df({ hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(ms);
}

export function fmtHM(ts) {
  const ms = toMs(ts);
  if (ms === null) return EMPTY;
  return df({ hour: '2-digit', minute: '2-digit', hour12: false }).format(ms);
}

export function fmtDate(ts) {
  const ms = toMs(ts);
  if (ms === null) return EMPTY;
  return df({ day: 'numeric', month: 'short', year: 'numeric' }).format(ms);
}

export function fmtDayMonth(ts) {
  const ms = toMs(ts);
  if (ms === null) return EMPTY;
  return df({ day: 'numeric', month: 'short' }).format(ms);
}

export function fmtDateTime(ts) {
  const ms = toMs(ts);
  if (ms === null) return EMPTY;
  return df({ day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(ms);
}

export function fmtDateTimeFull(ts) {
  const ms = toMs(ts);
  if (ms === null) return EMPTY;
  const base = df({ year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(ms);
  return base + '.' + String(Math.floor(ms % 1000)).padStart(3, '0');
}

export function fmtWeekday(ts) {
  const ms = toMs(ts);
  if (ms === null) return EMPTY;
  return df({ weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' }).format(ms);
}

export function fmtRel(ts, now = Date.now()) {
  const ms = toMs(ts);
  if (ms === null) return EMPTY;
  const diff = (ms - now) / 1000;
  const a = Math.abs(diff);
  const rtf = new Intl.RelativeTimeFormat(locale(), { numeric: 'auto', style: 'short' });
  if (a < 45) return t('time.just_now');
  if (a < 3600) return rtf.format(Math.round(diff / 60), 'minute');
  if (a < 86400) return rtf.format(Math.round(diff / 3600), 'hour');
  if (a < 86400 * 45) return rtf.format(Math.round(diff / 86400), 'day');
  return fmtDate(ms);
}

// Valeur pour une colonne datetime-local (heure locale).
export function toLocalInput(ms) {
  const d = new Date(ms);
  const p = (n) => String(n).padStart(2, '0');
  return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) + 'T' + p(d.getHours()) + ':' + p(d.getMinutes());
}

export function fromLocalInput(v) {
  if (!v) return null;
  const ms = new Date(v).getTime();
  return Number.isFinite(ms) ? ms : null;
}

export function shortId(id, n = 8) {
  if (id === null || id === undefined) return EMPTY;
  const str = String(id);
  return str.length > n ? str.slice(0, n) : str;
}

export function text(v) {
  if (v === null || v === undefined || v === '') return EMPTY;
  if (typeof v === 'object') return JSON.stringify(v);
  return String(v);
}
