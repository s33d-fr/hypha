#!/bin/sh
# Avant retrait du paquet. Une mise à jour (.deb : « upgrade », .rpm : 1) n'arrête rien :
# le postinstall de la nouvelle version redémarre l'agent.
set -e
case "$1" in
    remove|purge|0)
        systemctl disable --now hypha-agent.service >/dev/null 2>&1 || true
        ;;
esac
