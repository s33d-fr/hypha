// Contrôle de toutes les pages du plan du site dans headless_shell (déjà lancé sur :9333) :
// titre, langue, débordement horizontal à 390 px, erreurs JavaScript, images cassées.
//   node controle-pages.mjs https://hypha.s33d.fr/sitemap.xml
const [, , sitemap] = process.argv;
const xml = await (await fetch(sitemap)).text();
const urls = [...xml.matchAll(/<loc>([^<]+)<\/loc>/g)].map((m) => m[1]).filter((u) => !u.endsWith('.s33d.fr/'));
const ver = await (await fetch('http://127.0.0.1:9333/json/version')).json();
const ws = new WebSocket(ver.webSocketDebuggerUrl);
let seq = 0;
const pending = new Map();
const events = [];
ws.onmessage = (ev) => {
  const m = JSON.parse(ev.data);
  if (m.id && pending.has(m.id)) { const p = pending.get(m.id); pending.delete(m.id); m.error ? p.rej(new Error(m.error.message)) : p.res(m.result); }
  else events.push(m);
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
await S('Runtime.enable');
await S('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
let bad = 0;
for (const u of urls) {
  events.length = 0;
  await S('Page.navigate', { url: u });
  await new Promise((r) => setTimeout(r, 2500));
  const r = await S('Runtime.evaluate', { returnByValue: true, expression: `JSON.stringify({
    t: document.title, l: document.documentElement.lang, w: document.documentElement.scrollWidth,
    img: [...document.images].filter((i) => i.complete && !i.naturalWidth).length,
    h1: document.querySelectorAll('h1').length })` });
  const v = JSON.parse(r.result.value);
  const errs = events.filter((e) => e.method === 'Runtime.exceptionThrown').length;
  const wantLang = u.includes('/en/') ? 'en' : 'fr';
  const issues = [];
  if (v.w > 392) issues.push(`débordement ${v.w}px`);
  if (v.l !== wantLang) issues.push(`lang ${v.l}`);
  if (errs) issues.push(`${errs} erreur(s) JS`);
  if (v.img) issues.push(`${v.img} image(s) cassée(s)`);
  if (v.h1 !== 1) issues.push(`${v.h1} h1`);
  if (issues.length) { bad++; console.log(`${u} : ${issues.join(', ')}`); }
}
console.log(`${urls.length} pages contrôlées à 390 px, ${bad} en défaut`);
await send('Target.closeTarget', { targetId });
process.exit(0);
