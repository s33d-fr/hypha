// Capture d'une page (contrôle du tournage et des pages publiques), headless_shell déjà lancé sur :9333.
//   [DEMO_SESSION=…] node capture.mjs <url complète> <langue> <fichier.png> [largeur] [hauteur] [pleine]
import fs from 'node:fs';

const [, , url, lang, out, w = '1280', hgt = '720', full = ''] = process.argv;
const ver = await (await fetch('http://127.0.0.1:9333/json/version')).json();
const ws = new WebSocket(ver.webSocketDebuggerUrl);
let seq = 0;
const pending = new Map();
ws.onmessage = (ev) => {
  const m = JSON.parse(ev.data);
  if (m.id && pending.has(m.id)) { const p = pending.get(m.id); pending.delete(m.id); m.error ? p.rej(new Error(m.error.message)) : p.res(m.result); }
};
await new Promise((r) => { ws.onopen = r; });
const send = (method, params = {}, sessionId) => {
  const id = ++seq;
  ws.send(JSON.stringify({ id, method, params, sessionId }));
  return new Promise((res, rej) => pending.set(id, { res, rej }));
};
const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
const S = (m, p) => send(m, p, sessionId);
await S('Page.enable');
await S('Network.enable');
await S('Emulation.setDeviceMetricsOverride', { width: +w, height: +hgt, deviceScaleFactor: 1, mobile: +w < 700 });
if (process.env.DEMO_SESSION) {
  await S('Network.setCookie', { name: 'hypha_session', value: process.env.DEMO_SESSION, domain: new URL(url).hostname, path: '/', secure: true, httpOnly: true });
}
await S('Page.addScriptToEvaluateOnNewDocument', { source: `try { localStorage.setItem('hypha.lang', ${JSON.stringify(lang)}); } catch (e) {}` });
await S('Page.navigate', { url });
await new Promise((r) => setTimeout(r, 6000));
// CAPTURE_EVAL : script joué avant la capture (lancer une vidéo, ouvrir un menu), puis 4 s d'attente.
if (process.env.CAPTURE_EVAL) {
  await S('Runtime.evaluate', { expression: process.env.CAPTURE_EVAL, awaitPromise: true, userGesture: true });
  await new Promise((r) => setTimeout(r, 4000));
}
const info = await S('Runtime.evaluate', { expression: 'JSON.stringify({title: document.title, w: document.documentElement.scrollWidth, h: document.documentElement.scrollHeight, lang: document.documentElement.lang})', returnByValue: true });
const shot = await S('Page.captureScreenshot', { format: 'png', captureBeyondViewport: !!full });
fs.writeFileSync(out, Buffer.from(shot.data, 'base64'));
console.log(info.result.value);
await send('Target.closeTarget', { targetId });
process.exit(0);
