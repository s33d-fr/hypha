// Traduction : français par défaut, anglais complet. Choix mémorisé dans le navigateur.
import fr from '../assets/i18n/fr.js';
import en from '../assets/i18n/en.js';
// Exploitation 0.4.0 (problèmes, maintenances, incidents, pages de statut, rapports, audit, actions) :
// dictionnaire à part pour que deux chantiers puissent l'enrichir sans se gêner, fusionné ici.
import opsFr from '../assets/i18n/ops.fr.js';
import opsEn from '../assets/i18n/ops.en.js';

Object.assign(fr, opsFr);
Object.assign(en, opsEn);
// Données et exploration 0.4.0 (équipements, inventaire, journaux, erreurs, services, flotte, synthétiques,
// métriques, tableaux de bord, bases de données, consommation) : même principe.
import dataFr from '../assets/i18n/data.fr.js';
import dataEn from '../assets/i18n/data.en.js';

Object.assign(fr, dataFr);
Object.assign(en, dataEn);
// Formules et facturation (§ 52) : même principe.
import billingFr from '../assets/i18n/billing.fr.js';
import billingEn from '../assets/i18n/billing.en.js';

Object.assign(fr, billingFr);
Object.assign(en, billingEn);

const DICTS = { fr, en };
const LOCALES = { fr: 'fr-FR', en: 'en-GB' };

function readStored() {
  try {
    const v = localStorage.getItem('hypha.lang');
    return v === 'en' || v === 'fr' ? v : null;
  } catch (e) {
    return null;
  }
}

let lang = readStored() || 'fr';

export function getLang() { return lang; }
export function locale() { return LOCALES[lang] || 'fr-FR'; }
export function languages() { return [{ code: 'fr', label: 'Français' }, { code: 'en', label: 'English' }]; }

export function setLang(code) {
  if (!DICTS[code]) return;
  lang = code;
  try { localStorage.setItem('hypha.lang', code); } catch (e) { /* préférence non mémorisée */ }
  document.documentElement.setAttribute('lang', code);
}

function interpolate(str, params) {
  if (!params) return str;
  return str.replace(/\{(\w+)\}/g, (m, k) => (params[k] === undefined || params[k] === null ? '' : String(params[k])));
}

export function has(key) {
  return Object.prototype.hasOwnProperty.call(DICTS[lang], key) || Object.prototype.hasOwnProperty.call(fr, key);
}

export function t(key, params) {
  const d = DICTS[lang];
  let str = Object.prototype.hasOwnProperty.call(d, key) ? d[key] : fr[key];
  if (str === undefined) str = key;
  return interpolate(str, params);
}

// Pluriel : clé + "_one" pour 0 et 1 en français, 1 seul en anglais ; sinon clé + "_other".
export function tn(key, n, params) {
  const one = lang === 'fr' ? Math.abs(n) < 2 : Math.abs(n) === 1;
  return t(key + (one ? '_one' : '_other'), Object.assign({ n: fmtInt(n) }, params || {}));
}

let intFmt = null;
let intFmtLang = null;
function fmtInt(n) {
  if (intFmtLang !== lang) { intFmt = new Intl.NumberFormat(locale(), { maximumFractionDigits: 0 }); intFmtLang = lang; }
  return typeof n === 'number' && Number.isFinite(n) ? intFmt.format(n) : String(n);
}
