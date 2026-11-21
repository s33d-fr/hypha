#!/bin/sh
# HYPHA auto-hébergé : au démarrage de nginx, publie /install/tls.json pour la page « Installer
# l'agent » : empreinte de la clé publique du certificat (tls_pin des agents, même écriture que
# curl --pinnedpubkey), empreinte SHA-256 du certificat (premier téléchargement sous Windows) et
# reconnaissance par une autorité publique. Certificat public : les commandes restent ordinaires.
set -eu
CERT=/etc/nginx/certs/fullchain.pem
OUT=/srv/hypha-install/tls.json
if [ ! -r "$CERT" ]; then
    echo "hypha : $CERT illisible, /install/tls.json non publié" >&2
    exit 0
fi
pin=$(openssl x509 -in "$CERT" -pubkey -noout | openssl pkey -pubin -outform der | openssl dgst -sha256 -binary | openssl base64 -A)
cert=$(openssl x509 -in "$CERT" -outform der | openssl dgst -sha256 -r | cut -d' ' -f1 | tr 'a-f' 'A-F')
public=false
if openssl verify -CAfile /etc/ssl/certs/ca-certificates.crt -untrusted "$CERT" "$CERT" >/dev/null 2>&1; then
    public=true
fi
printf '{"pin":"sha256//%s","cert_sha256":"%s","public":%s}\n' "$pin" "$cert" "$public" >"$OUT"
echo "hypha : /install/tls.json publié (certificat public : $public, tls_pin sha256//$pin)"
