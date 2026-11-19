// Tournage d'une scène dans la vraie interface HYPHA, par CDP, avec headless_shell sur la machine de tournage.
//   DEMO_SESSION=… node tournage.mjs scenes/vue-ensemble.json fr /tmp/hypha-video/vue-ensemble-fr
// Le navigateur doit écouter sur 127.0.0.1:9333 (voir tourner.sh). Rien n'est capturé du poste.
// Sortie : images JPEG du screencast + frames.json (horodatages, légendes) pour assembler.py.
import fs from 'node:fs';
import path from 'node:path';

const [, , sceneFile, lang, outDir] = process.argv;
const BASE = process.env.DEMO_BASE || 'https://hypha.s33d.fr';
const PORT = process.env.CDP_PORT || '9333';
const W = 1280, H = 720;
const scene = JSON.parse(fs.readFileSync(sceneFile, 'utf8'));
fs.mkdirSync(outDir, { recursive: true });

const ver = await (await fetch(`http://127.0.0.1:${PORT}/json/version`)).json();
const ws = new WebSocket(ver.webSocketDebuggerUrl);
let seq = 0;
const pending = new Map();
const listeners = [];
ws.onmessage = (ev) => {
  const m = JSON.parse(ev.data);
  if (m.id && pending.has(m.id)) {
    const { res, rej } = pending.get(m.id);
    pending.delete(m.id);
    if (m.error) rej(new Error(m.error.message)); else res(m.result);
  } else for (const f of listeners) f(m);
};
await new Promise((r) => { ws.onopen = r; });
const send = (method, params = {}, sessionId) => {
  const id = ++seq;
  ws.send(JSON.stringify({ id, method, params, sessionId }));
  return new Promise((res, rej) => pending.set(id, { res, rej }));
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
const S = (m, p) => send(m, p, sessionId);
await S('Page.enable');
await S('Runtime.enable');
await S('Network.enable');
await S('Emulation.setDeviceMetricsOverride', { width: W, height: H, deviceScaleFactor: 1, mobile: false });
await S('Network.setCookie', {
  name: 'hypha_session', value: process.env.DEMO_SESSION || '', domain: new URL(BASE).hostname,
  path: '/', secure: true, httpOnly: true, sameSite: 'Lax',
});
// Langue, thème sombre, rafraîchissement coupé ; curseur dessiné dans la page (headless n'en a pas).
const CURSOR = '<svg width="22" height="22" viewBox="0 0 22 22"><path d="M3 2 L3 18 L7.5 13.8 L10.6 20.5 L13.4 19.2 L10.4 12.7 L16.5 12.4 Z" fill="#fff" stroke="#111" stroke-width="1.3" stroke-linejoin="round"/></svg>';
await S('Page.addScriptToEvaluateOnNewDocument', {
  source: `try { localStorage.setItem('hypha.lang', ${JSON.stringify(lang)}); localStorage.setItem('hypha.theme', 'dark');
    localStorage.setItem('hypha.refresh', '0'); } catch (e) {}
  addEventListener('DOMContentLoaded', () => {
    const c = document.createElement('div'); c.id = 'demo-cursor';
    c.style.cssText = 'position:fixed;left:' + (window.__cx || 900) + 'px;top:' + (window.__cy || 520) + 'px;width:22px;height:22px;z-index:2147483647;pointer-events:none;transition:left .7s cubic-bezier(.3,.7,.3,1),top .7s cubic-bezier(.3,.7,.3,1);filter:drop-shadow(0 1px 2px rgba(0,0,0,.6))';
    c.innerHTML = ${JSON.stringify(CURSOR)}; document.documentElement.appendChild(c);
  });`,
});

const frames = [];
const captions = [];
let t0 = null;
let recording = false;
let paused = 0;        // secondes retirées de la vidéo (étapes « pause » … « resume »)
let pauseStart = null;
listeners.push(async (m) => {
  if (m.method !== 'Page.screencastFrame' || m.sessionId !== sessionId) return;
  // Heure de réception, horloge murale ; les pauses (attente d'une réponse lente) sont retirées.
  const now = Date.now() / 1000;
  if (recording) {
    if (t0 === null) t0 = now;
    const f = `f${String(frames.length).padStart(5, '0')}.jpg`;
    fs.writeFileSync(path.join(outDir, f), Buffer.from(m.params.data, 'base64'));
    frames.push({ f, t: now - t0 - paused });
  }
  send('Page.screencastFrameAck', { sessionId: m.params.sessionId }, sessionId).catch(() => {});
});

async function evaluate(expr) {
  const r = await S('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true });
  return r.result ? r.result.value : undefined;
}

// Élément visé : sélecteur, éventuellement dans un bloc (within) dont le texte contient `contains`
// (texte propre à chaque langue : {"fr": …, "en": …}), sinon filtré lui-même sur ce texte.
function finder(sel, step) {
  const c = step.contains === undefined ? null : (typeof step.contains === 'object' ? step.contains[lang] : step.contains);
  return `(() => {
    const sel = ${JSON.stringify(sel)}, within = ${JSON.stringify(step.within || null)}, c = ${JSON.stringify(c)};
    let all = [];
    if (within) {
      for (const b of document.querySelectorAll(within)) if (!c || b.textContent.includes(c)) all.push(...b.querySelectorAll(sel));
    } else {
      all = [...document.querySelectorAll(sel)];
      if (c) all = all.filter((e) => e.textContent.includes(c));
    }
    all = all.filter((e) => e.getClientRects().length);
    return all[${step.nth || 0}] || null;
  })()`;
}

async function waitFor(expr, timeout = 20000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) {
    if (await evaluate(`!!${expr}`)) return true;
    await sleep(250);
  }
  throw new Error('introuvable : ' + expr.slice(0, 200));
}

async function rectOf(expr) {
  return evaluate(`(() => { const e = ${expr}; if (!e) return null;
    e.scrollIntoView({ block: 'nearest' }); const r = e.getBoundingClientRect();
    return { x: r.left + Math.min(r.width / 2, 60), y: r.top + r.height / 2 }; })()`);
}

let cx = 900, cy = 520;
async function moveTo(x, y) {
  cx = Math.round(x); cy = Math.round(y);
  await evaluate(`(() => { window.__cx = ${cx}; window.__cy = ${cy}; const c = document.getElementById('demo-cursor');
    if (c) { c.style.left = '${cx}px'; c.style.top = '${cy}px'; } })()`);
  await S('Input.dispatchMouseEvent', { type: 'mouseMoved', x: cx, y: cy });
  await sleep(800);
}

async function click(expr) {
  await waitFor(expr);
  const r = await rectOf(expr);
  if (!r) throw new Error('introuvable : ' + expr.slice(0, 200));
  await moveTo(r.x, r.y);
  await S('Input.dispatchMouseEvent', { type: 'mousePressed', x: cx, y: cy, button: 'left', clickCount: 1 });
  await sleep(90);
  await S('Input.dispatchMouseEvent', { type: 'mouseReleased', x: cx, y: cy, button: 'left', clickCount: 1 });
}

async function typeText(expr, text, enter) {
  await click(expr);
  await evaluate(`(() => { const e = ${expr}; e.focus(); e.select && e.select(); })()`);
  for (const ch of text) {
    await S('Input.insertText', { text: ch });
    await sleep(70);
  }
  if (enter) {
    await sleep(300);
    for (const type of ['rawKeyDown', 'char', 'keyUp']) {
      await S('Input.dispatchKeyEvent', { type, key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, text: type === 'char' ? '\r' : undefined });
    }
  }
}

async function goto(url) {
  const loaded = new Promise((r) => {
    const f = (m) => { if (m.method === 'Page.loadEventFired' && m.sessionId === sessionId) { listeners.splice(listeners.indexOf(f), 1); r(); } };
    listeners.push(f);
  });
  await S('Page.navigate', { url: BASE + url });
  await Promise.race([loaded, sleep(20000)]);
}

const now = () => (t0 === null ? 0 : Date.now() / 1000 - t0 - paused);
const speeds = [];
for (const step of scene.steps) {
  const label = step[lang] !== undefined ? step[lang] : step.text;
  if (step.pause) { recording = false; pauseStart = Date.now() / 1000; }
  if (step.goto) await goto(step.goto.replace('{lang}', lang));
  if (step.hash) await evaluate(`location.hash = ${JSON.stringify(step.hash)}`);
  if (step.waitFor) await waitFor(finder(step.waitFor, step), step.timeout || 20000);
  if (step.record) {
    await S('Page.startScreencast', { format: 'jpeg', quality: 88, maxWidth: W, maxHeight: H, everyNthFrame: 1 });
    recording = true;
    await moveTo(cx + 1, cy);
  }
  if (step.resume && pauseStart !== null) {
    paused += Date.now() / 1000 - pauseStart - 0.6;   // 0,6 s gardées pour la transition
    pauseStart = null;
    recording = true;
    await moveTo(cx + 1, cy);
  }
  if (step.speed) speeds.push({ t: now(), factor: step.speed });
  if (step.caption) captions.push({ start: now(), end: now() + (step.dur || 4000) / 1000, text: label });
  if (step.move) { const r = await rectOf(finder(step.move, step)); if (r) await moveTo(r.x, r.y); }
  if (step.click) await click(finder(step.click, step));
  if (step.type) await typeText(finder(step.type, step), label, step.enter);
  if (step.scroll) {
    await S('Input.dispatchMouseEvent', { type: 'mouseWheel', x: step.x || 760, y: step.y || 420, deltaX: 0, deltaY: step.scroll });
  }
  if (step.eval) await evaluate(step.eval);
  if (step.wait) await sleep(step.wait);
}
await S('Page.stopScreencast');
recording = false;
const end = now();
fs.writeFileSync(path.join(outDir, 'frames.json'), JSON.stringify({ frames, captions, speeds, end, title: scene.title[lang] }, null, 1));
console.log(`${frames.length} images, ${end.toFixed(1)} s, ${captions.length} légendes`);
await send('Target.closeTarget', { targetId });
ws.close();
process.exit(0);
