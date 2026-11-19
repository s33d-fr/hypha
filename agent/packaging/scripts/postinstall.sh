#!/bin/sh
# Après installation ou mise à jour du paquet (.deb : « configure », .rpm : 1 ou 2).
set -e
systemctl daemon-reload >/dev/null 2>&1 || true
systemctl enable hypha-agent.service >/dev/null 2>&1 || true
# L'agent ne démarre que si une clé est configurée ; sinon il échouerait en boucle.
if grep -Eq '^[[:space:]]*api_key:[[:space:]]*["'\'']?hph_' /etc/hypha/hypha.yaml 2>/dev/null; then
    systemctl restart hypha-agent.service || true
else
    echo "hypha-agent : renseigner api_key dans /etc/hypha/hypha.yaml, puis : systemctl start hypha-agent"
fi
