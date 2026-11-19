#!/bin/sh
# Après retrait du paquet. Conf et données (/etc/hypha, /var/lib/hypha) restent, sauf purge .deb.
set -e
systemctl daemon-reload >/dev/null 2>&1 || true
if [ "$1" = "purge" ]; then
    rm -rf /var/lib/hypha /etc/hypha
fi
