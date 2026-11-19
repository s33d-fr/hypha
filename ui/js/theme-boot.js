// Thème et langue appliqués avant le premier rendu (script classique, sans module, compatible CSP).
(function () {
  try {
    var th = localStorage.getItem('hypha.theme');
    if (th === 'light' || th === 'dark') document.documentElement.setAttribute('data-theme', th);
    var lg = localStorage.getItem('hypha.lang');
    if (lg === 'en' || lg === 'fr') document.documentElement.setAttribute('lang', lg);
  } catch (e) { /* stockage indisponible : thème sombre par défaut */ }
})();
