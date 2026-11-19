// HYPHA se supervise lui-même : sur hypha.s33d.fr seulement, l'interface charge le script RUM de
// HYPHA avec le jeton public de l'application « Interface HYPHA ». Un serveur auto-hébergé ne charge
// rien : ses visites ne partent jamais chez S33D.
(function () {
  if (location.hostname !== 'hypha.s33d.fr') return;
  var s = document.createElement('script');
  s.src = '/rum/hypha-rum.js';
  s.defer = true;
  s.setAttribute('data-token', 'pub55d7e919da017ec26f17fe9f1e431bde');  // jeton public RUM (lu par tout navigateur), gitleaks:allow
  s.setAttribute('data-service', 'hypha-ui');
  s.setAttribute('data-env', 'prod');
  s.setAttribute('data-version', '0.2.0');
  document.head.appendChild(s);
})();
