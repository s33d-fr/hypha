#!/bin/bash
# Signe SHA256SUMS d'un dossier de publication avec la clé ed25519 de HYPHA (contrat § 27).
# La clé privée ne vit que sur la machine de build ; l'agent porte la clé publique
# (agent/internal/update/release_pubkey.txt) et refuse toute mise à jour mal signée.
# Usage : packaging/sign.sh <dossier contenant SHA256SUMS>
set -euo pipefail
D=${1:?dossier de publication}
K=${HYPHA_SIGNING_KEY:-$(cd "$(dirname "$0")/.." && pwd)/signing/release.key}
[ -f "$D/SHA256SUMS" ] || { echo "SHA256SUMS absent de $D"; exit 1; }
[ -r "$K" ] || { echo "clé de signature illisible : $K"; exit 1; }
openssl pkeyutl -sign -inkey "$K" -rawin -in "$D/SHA256SUMS" | base64 -w0 > "$D/SHA256SUMS.sig"
echo "signé : $D/SHA256SUMS.sig"
