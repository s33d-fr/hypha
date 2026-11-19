// Compteur de problèmes non acquittés dans la barre latérale (§ 17), rafraîchi toutes les 30 s.
// Les groupes supprimés (maintenance, dépendance) ne comptent pas : ils sont attendus.
import { h } from '../dom.js';
import { tn } from '../i18n.js';
import { get, asList } from '../api.js';

const PERIOD_MS = 30000;
let badge = null;
let timer = null;
let state = { n: 0, worst: null, known: false };

function paint() {
  if (!badge) return;
  badge.hidden = !state.known || state.n === 0;
  badge.textContent = state.n > 99 ? '99+' : String(state.n);
  badge.className = 'nav-count' + (state.worst ? ' p' + state.worst : '');
  badge.title = tn('problems.unacked_count', state.n);
}

export function problemBadge() {
  if (!badge) badge = h('span', { class: 'nav-count', hidden: true, 'aria-live': 'polite' });
  paint();
  return badge;
}

export async function refreshProblemCount() {
  try {
    const list = asList(await get('/problems', { acked: 'false' }, { quiet401: true }), 'problems');
    let n = 0;
    let worst = null;
    for (const p of list) {
      if (p.acked || p.suppressed) continue;
      if (String(p.state || '').toUpperCase() === 'OK') continue;
      n++;
      const pr = Number(p.priority);
      if (Number.isInteger(pr) && pr >= 1 && pr <= 5 && (worst === null || pr < worst)) worst = pr;
    }
    state = { n, worst, known: true };
  } catch (e) {
    // Route absente (serveur 0.3) ou session close : le compteur se tait.
    if (e && (e.status === 404 || e.status === 401 || e.status === 405)) stopProblemCounter();
    state = Object.assign({}, state, { known: state.known && !(e && e.status === 401) });
  }
  paint();
  return state.n;
}

export function stopProblemCounter() {
  if (timer) clearInterval(timer);
  timer = null;
}

export function startProblemCounter() {
  stopProblemCounter();
  refreshProblemCount();
  timer = setInterval(() => { if (document.visibilityState === 'visible') refreshProblemCount(); }, PERIOD_MS);
  return stopProblemCounter;
}
