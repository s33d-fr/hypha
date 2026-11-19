/*! HYPHA RUM (S33D) : vues, signaux web (LCP, CLS, INP), erreurs JS, ressources lentes. Sans dépendance. */
(function () {
  'use strict';
  if (window.__hyphaRum) return;
  var script = document.currentScript || document.querySelector('script[data-token][src*="hypha-rum"]');
  if (!script) return;
  var token = script.getAttribute('data-token');
  if (!token) return;
  window.__hyphaRum = true;

  var attr = function (n, d) { var v = script.getAttribute(n); return v === null || v === '' ? d : v; };
  var origin;
  try { origin = new URL(script.src, location.href).origin; } catch (e) { origin = location.origin; }
  var endpoint = attr('data-endpoint', origin + '/api/v1/intake/rum');
  endpoint += (endpoint.indexOf('?') >= 0 ? '&' : '?') + 'token=' + encodeURIComponent(token);
  var fullUrl = attr('data-full-url', 'false') === 'true';
  var base = { service: attr('data-service', null), env: attr('data-env', null), version: attr('data-version', null) };
  var sample = parseFloat(attr('data-sample', '1'));
  if (!(sample >= 0 && sample <= 1)) sample = 1;

  // Session : 30 min d'inactivité, conservée dans l'onglet. Décision d'échantillonnage par session.
  var SID = 'hypha_rum_sid';
  var now = function () { return Date.now(); };
  function randomId() {
    var a = new Uint8Array(16);
    (window.crypto || window.msCrypto).getRandomValues(a);
    var s = '';
    for (var i = 0; i < a.length; i++) s += (a[i] < 16 ? '0' : '') + a[i].toString(16);
    return s;
  }
  function session() {
    var raw = null;
    try { raw = JSON.parse(sessionStorage.getItem(SID) || 'null'); } catch (e) { raw = null; }
    if (!raw || now() - raw.last > 30 * 60 * 1000) raw = { id: randomId(), last: now(), keep: Math.random() < sample };
    raw.last = now();
    try { sessionStorage.setItem(SID, JSON.stringify(raw)); } catch (e) { /* stockage indisponible */ }
    return raw;
  }
  var sess = session();
  if (!sess.keep) return;

  // Une route en fragment (#/logs) fait partie de la page ; ses paramètres (#/logs?q=…) non, ils
  // peuvent porter des recherches de l'utilisateur.
  function pageUrl() {
    if (fullUrl) return location.href;
    var frag = location.hash ? location.hash.split('?')[0] : '';
    return location.origin + location.pathname + (frag.length > 1 ? frag : '');
  }
  function clip(s, n) { s = s === null || s === undefined ? '' : String(s); return s.length > n ? s.slice(0, n) : s; }
  function attrs(extra) {
    var o = {};
    for (var k in base) if (base[k] !== null) o[k] = base[k];
    if (extra) for (var j in extra) if (extra[j] !== undefined) o[j] = extra[j];
    return o;
  }

  var queue = [];
  var errorsSent = 0;
  function push(type, name, duration, extra, url) {
    queue.push({ ts: now(), type: type, url: url || pageUrl(), name: clip(name, 500), duration_ms: typeof duration === 'number' && isFinite(duration) ? Math.round(duration * 1000) / 1000 : null, attributes: attrs(extra) });
    if (queue.length >= 30) flush(false);
  }

  function flush(unloading) {
    if (!queue.length) return;
    sess = session();
    var body = JSON.stringify({ session_id: sess.id, events: queue.splice(0, queue.length) });
    // fetch keepalive (JSON, CORS) d'abord ; sendBeacon en text/plain quand fetch n'est pas utilisable.
    var sent = false;
    if (window.fetch && body.length < 60000) {
      try {
        fetch(endpoint, { method: 'POST', body: body, keepalive: true, credentials: 'omit', mode: 'cors', headers: { 'Content-Type': 'application/json' } })['catch'](function () {});
        sent = true;
      } catch (e) { sent = false; }
    }
    if (!sent && navigator.sendBeacon) {
      try { sent = navigator.sendBeacon(endpoint, new Blob([body], { type: 'text/plain;charset=UTF-8' })); } catch (e) { sent = false; }
    }
    if (!sent && !unloading && window.XMLHttpRequest) {
      try { var x = new XMLHttpRequest(); x.open('POST', endpoint, true); x.setRequestHeader('Content-Type', 'application/json'); x.send(body); } catch (e) { /* abandon */ }
    }
  }

  // Vue initiale : navigation timing.
  function initialView() {
    var nav = performance.getEntriesByType && performance.getEntriesByType('navigation')[0];
    var extra = { referrer: document.referrer ? clip(document.referrer.split('?')[0], 300) : undefined, viewport: window.innerWidth + 'x' + window.innerHeight, lang: navigator.language };
    var dur = null;
    if (nav) {
      dur = nav.loadEventEnd > 0 ? nav.loadEventEnd : nav.domContentLoadedEventEnd;
      extra.ttfb_ms = Math.round(nav.responseStart);
      extra.dom_interactive_ms = Math.round(nav.domInteractive);
      extra.dcl_ms = Math.round(nav.domContentLoadedEventEnd);
      extra.load_ms = Math.round(nav.loadEventEnd);
      extra.transfer_size = nav.transferSize;
      extra.nav_type = nav.type;
    }
    push('view', document.title, dur, extra);
  }
  if (document.readyState === 'complete') setTimeout(initialView, 0);
  else window.addEventListener('load', function () { setTimeout(initialView, 0); });

  // Navigations d'application à page unique.
  var lastUrl = pageUrl();
  function spaView() {
    var u = pageUrl();
    if (u === lastUrl) return;
    lastUrl = u;
    setTimeout(function () { push('view', document.title, null, { spa: true }); }, 0);
  }
  ['pushState', 'replaceState'].forEach(function (m) {
    var orig = history[m];
    if (typeof orig !== 'function') return;
    history[m] = function () { var r = orig.apply(this, arguments); spaView(); return r; };
  });
  window.addEventListener('popstate', spaView);
  window.addEventListener('hashchange', spaView);

  // Signaux web.
  var lcp = null; var cls = 0; var clsWin = 0; var clsFirst = 0; var clsLast = 0; var inp = null;
  var observe = function (type, cb, opts) {
    try {
      var po = new PerformanceObserver(function (list) { list.getEntries().forEach(cb); });
      po.observe(Object.assign({ type: type, buffered: true }, opts || {}));
    } catch (e) { /* type non pris en charge */ }
  };
  observe('largest-contentful-paint', function (e) { lcp = e.renderTime || e.startTime; });
  observe('layout-shift', function (e) {
    if (e.hadRecentInput) return;
    if (clsWin && e.startTime - clsLast < 1000 && e.startTime - clsFirst < 5000) { clsWin += e.value; clsLast = e.startTime; }
    else { clsWin = e.value; clsFirst = clsLast = e.startTime; }
    if (clsWin > cls) cls = clsWin;
  });
  observe('event', function (e) { if (e.interactionId && (inp === null || e.duration > inp)) inp = e.duration; }, { durationThreshold: 40 });
  observe('resource', function (e) {
    if (e.duration > 1000 && e.name.indexOf(endpoint.split('?')[0]) !== 0) {
      push('resource', clip(e.name.split('?')[0], 500), e.duration, { initiator: e.initiatorType, transfer_size: e.transferSize });
    }
  });

  var vitalsSent = false;
  function sendVitals() {
    if (vitalsSent) return;
    vitalsSent = true;
    if (lcp !== null) push('vital', 'LCP', lcp, { value: Math.round(lcp) });
    push('vital', 'CLS', cls, { value: Math.round(cls * 10000) / 10000 });
    if (inp !== null) push('vital', 'INP', inp, { value: Math.round(inp) });
  }

  // Erreurs JS (50 par page au plus).
  // Avertissements du navigateur sans conséquence pour l'utilisateur : jamais comptés comme erreurs.
  var BENIGN = /^(ResizeObserver loop (completed with undelivered notifications|limit exceeded)|Script error\.?)$/;
  window.addEventListener('error', function (ev) {
    if (BENIGN.test(ev.message || '')) return;
    if (errorsSent++ >= 50) return;
    var err = ev.error;
    push('error', ev.message || (err && err.message) || 'error', null, { source: clip(ev.filename, 300), line: ev.lineno, col: ev.colno, stack: err && err.stack ? clip(err.stack, 4000) : undefined, kind: 'error' });
  });
  window.addEventListener('unhandledrejection', function (ev) {
    if (errorsSent++ >= 50) return;
    var r = ev.reason;
    push('error', (r && r.message) || clip(r, 300) || 'unhandledrejection', null, { stack: r && r.stack ? clip(r.stack, 4000) : undefined, kind: 'unhandledrejection' });
  });

  // Envoi : toutes les 10 s, et à la sortie de la page.
  setInterval(function () { flush(false); }, 10000);
  document.addEventListener('visibilitychange', function () {
    if (document.visibilityState === 'hidden') { sendVitals(); flush(true); }
  });
  window.addEventListener('pagehide', function () { sendVitals(); flush(true); });
})();
