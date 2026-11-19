// Tableau partagé : thème du système (ou choix mémorisé), langue mémorisée, avant le premier rendu.
(function () {
  var th = null;
  try { th = localStorage.getItem('hypha.theme'); } catch (e) { th = null; }
  if (th !== 'light' && th !== 'dark') th = window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
  document.documentElement.setAttribute('data-theme', th);
  try {
    var lg = localStorage.getItem('hypha.lang');
    if (lg === 'en' || lg === 'fr') document.documentElement.setAttribute('lang', lg);
  } catch (e) { /* langue par défaut */ }
})();
