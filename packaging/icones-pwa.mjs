// Icônes de l'application installable (ui/assets/img/) depuis l'icône D « boîte de Petri » retenue pour HYPHA
// (géométrie de docs/communication/icone/fabriquer.mjs, pistes « d » et « dp » petit format).
// Rendu PNG par headless_shell (Chromium) : à lancer sur la machine de construction, jamais sur un poste de travail.
//   node packaging/icones-pwa.mjs <dossier de sortie> [<chemin de headless_shell>]
// Écrit icon-192/512 (tuile arrondie, « any »), icon-maskable-192/512 (fond plein, dessin dans la zone
// sûre de 80 %), apple-touch-icon (180, fond plein : iOS arrondit lui-même) et badge-96 (silhouette
// blanche sur fond transparent, barre d'état d'Android).
import { spawn } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

const SORTIE = process.argv[2] || "icones";
const SHELL = process.argv[3] ||
  "/root/.cache/ms-playwright/chromium_headless_shell-1234/chrome-linux/headless_shell";
const PORT = 9341;
const MARINE = "#14213a";
const DEGRADE = ["#4f8fe0", "#a3c8f5"];

const D = {
  anneau: [196, 18],
  traits: [
    [22, "M256 262 Q238 190 262 116"], [22, "M256 262 Q320 250 374 212"], [22, "M256 262 Q302 318 332 374"],
    [22, "M256 262 Q214 316 174 360"], [22, "M256 262 Q196 244 138 238"], [14, "M250 192 Q226 166 208 148"],
    [14, "M196 248 Q184 220 178 190"], [14, "M300 318 Q338 330 372 330"],
  ],
  noeuds: [[256, 262, 34], [262, 116, 18], [374, 212, 18], [332, 374, 18], [174, 360, 18], [138, 238, 18],
    [208, 148, 12], [178, 190, 12], [372, 330, 12]],
};
const DP = {
  anneau: [200, 36],
  traits: [[36, "M256 258 Q240 198 262 134"], [36, "M256 258 Q316 250 364 220"], [36, "M256 258 Q296 310 322 352"],
    [36, "M256 258 Q216 308 184 346"], [36, "M256 258 Q200 246 148 242"]],
  noeuds: [[256, 258, 50], [262, 134, 28], [364, 220, 28], [322, 352, 28], [184, 346, 28], [148, 242, 28]],
};

// fond : "tuile" (carré arrondi), "plein" (carré bord à bord) ou null ; echelle : dessin autour du centre.
function svg(p, { fond, echelle = 1, encre = null }) {
  const peinture = encre || "url(#g)";
  const g = encre ? "" : `<defs><linearGradient id="g" gradientUnits="userSpaceOnUse" x1="96" y1="416" x2="416" y2="96">` +
    `<stop offset="0" stop-color="${DEGRADE[0]}"/><stop offset="1" stop-color="${DEGRADE[1]}"/></linearGradient></defs>`;
  const f = fond === "tuile" ? `<rect width="512" height="512" rx="112" fill="${MARINE}"/>`
    : fond === "plein" ? `<rect width="512" height="512" fill="${MARINE}"/>` : "";
  const t = echelle === 1 ? "" : ` transform="translate(256 256) scale(${echelle}) translate(-256 -256)"`;
  const anneau = `<circle cx="256" cy="256" r="${p.anneau[0]}" fill="none" stroke="${peinture}" stroke-width="${p.anneau[1]}"/>`;
  const traits = p.traits.map(([e, d]) => `<path stroke-width="${e}" d="${d}"/>`).join("");
  const noeuds = p.noeuds.map(([x, y, r]) => `<circle cx="${x}" cy="${y}" r="${r}"/>`).join("");
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512">${g}${f}<g${t}>${anneau}` +
    `<g fill="none" stroke="${peinture}" stroke-linecap="round" stroke-linejoin="round">${traits}</g>` +
    `<g fill="${peinture}">${noeuds}</g></g></svg>`;
}

const ICONES = [
  ["icon-192.png", 192, svg(D, { fond: "tuile" })],
  ["icon-512.png", 512, svg(D, { fond: "tuile" })],
  // Zone sûre d'une icône « maskable » : cercle de 40 % de la taille ; anneau extérieur 205 × 0,78 = 160 < 204.
  ["icon-maskable-192.png", 192, svg(D, { fond: "plein", echelle: 0.78 })],
  ["icon-maskable-512.png", 512, svg(D, { fond: "plein", echelle: 0.78 })],
  ["apple-touch-icon.png", 180, svg(D, { fond: "plein", echelle: 0.84 })],
  ["badge-96.png", 96, svg(DP, { fond: null, echelle: 1.06, encre: "#ffffff" })],
];

const attendre = (ms) => new Promise((r) => setTimeout(r, ms));

async function cdp(url) {
  const ws = new WebSocket(url);
  await new Promise((ok, ko) => { ws.onopen = ok; ws.onerror = ko; });
  let n = 0;
  const attente = new Map();
  ws.onmessage = (e) => {
    const m = JSON.parse(e.data);
    if (m.id && attente.has(m.id)) { attente.get(m.id)(m); attente.delete(m.id); }
  };
  const envoyer = (method, params = {}) => new Promise((ok, ko) => {
    const id = ++n;
    attente.set(id, (m) => (m.error ? ko(new Error(method + " : " + m.error.message)) : ok(m.result)));
    ws.send(JSON.stringify({ id, method, params }));
  });
  return { envoyer, fermer: () => ws.close() };
}

fs.mkdirSync(SORTIE, { recursive: true });
const chrome = spawn(SHELL, [`--remote-debugging-port=${PORT}`, "--no-sandbox", "--disable-gpu", "--hide-scrollbars",
  "--force-color-profile=srgb", "about:blank"], { stdio: "ignore" });
try {
  let cible = null;
  for (let i = 0; i < 50 && !cible; i++) {
    await attendre(200);
    try {
      const liste = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json();
      cible = liste.find((t) => t.type === "page");
    } catch (e) { /* pas encore prêt */ }
  }
  if (!cible) throw new Error("headless_shell ne répond pas");
  const c = await cdp(cible.webSocketDebuggerUrl);
  await c.envoyer("Page.enable");
  await c.envoyer("Emulation.setDefaultBackgroundColorOverride", { color: { r: 0, g: 0, b: 0, a: 0 } });
  for (const [nom, taille, source] of ICONES) {
    await c.envoyer("Emulation.setDeviceMetricsOverride", { width: taille, height: taille, deviceScaleFactor: 1, mobile: false });
    const html = `<!doctype html><html><head><style>html,body{margin:0;background:transparent}svg{display:block;width:${taille}px;height:${taille}px}</style></head><body>${source}</body></html>`;
    await c.envoyer("Page.navigate", { url: "data:text/html;base64," + Buffer.from(html).toString("base64") });
    await attendre(400);
    const { data } = await c.envoyer("Page.captureScreenshot", { format: "png", clip: { x: 0, y: 0, width: taille, height: taille, scale: 1 } });
    fs.writeFileSync(path.join(SORTIE, nom), Buffer.from(data, "base64"));
    console.log(nom, taille);
  }
  c.fermer();
} finally {
  chrome.kill("SIGKILL");
}
