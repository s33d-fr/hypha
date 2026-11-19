#!/bin/bash
# Lance (start) ou arrête (stop) headless_shell sur 127.0.0.1:9333, pour capture.mjs.
set -euo pipefail
SHELL_BIN=/root/.cache/ms-playwright/chromium_headless_shell-1234/chrome-linux/headless_shell
case "${1:-}" in
  start)
    PROFIL=$(mktemp -d /tmp/hypha-nav.XXXX)
    setsid nohup nice -n 10 "$SHELL_BIN" --remote-debugging-port=9333 --remote-debugging-address=127.0.0.1 --no-sandbox \
      --user-data-dir="$PROFIL" --hide-scrollbars --mute-audio --use-angle=swiftshader --enable-unsafe-swiftshader \
      about:blank > /tmp/hypha-nav.log 2>&1 < /dev/null &
    for i in $(seq 1 40); do curl -sf http://127.0.0.1:9333/json/version >/dev/null && break; sleep 0.25; done
    echo "navigateur prêt" ;;
  stop)
    pkill -f "remote-debugging-port=9333" || true
    rm -rf /tmp/hypha-nav.* ; echo "navigateur arrêté" ;;
  *) echo "start ou stop" >&2; exit 2 ;;
esac
