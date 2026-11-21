#!/usr/bin/env bash
# Installation de hypha-agent sous Linux (contrat HYPHA § 5).
#
#   curl -fsSL https://hypha.s33d.fr/install/install.sh | sudo HYPHA_API_KEY=hph_… bash
#   … | sudo bash -s -- --uninstall
#
# Strictement non interactif (lancé aussi par Ansible sur tout un parc) : aucune question,
# sortie non nulle sur échec, dernière ligne lisible (« hypha-agent 0.1.0 actif »).
# Rejouable : une réinstallation de la même version ne redémarre pas le service.
#
# Variables :
#   HYPHA_API_KEY     clé d'ingestion (obligatoire à la première installation)
#   HYPHA_URL         serveur (défaut https://hypha.s33d.fr ; auto-hébergé : son adresse)
#   HYPHA_TAGS        étiquettes, « env:prod,role:web » ou JSON {"env":"prod"}
#   HYPHA_HOSTNAME    nom de l'hôte dans HYPHA (défaut : nom système)
#   HYPHA_FORCE_CONF  1 = réécrire api_key et url d'une conf existante (rien d'autre)
#   HYPHA_TLS_PIN     serveur auto-hébergé à certificat autosigné ou d'autorité interne :
#                     empreinte « sha256//… » de sa clé publique (Réglages › Installer l'agent).
#                     Les téléchargements la vérifient (curl --pinnedpubkey), la conf la garde
#                     (tls_pin) : la clé du serveur est vérifiée à chaque connexion de l'agent.
# HYPHA_TAGS et HYPHA_HOSTNAME ne sont écrits qu'à la création de la conf : une conf
# existante n'est jamais réécrite, sauf api_key et url avec HYPHA_FORCE_CONF=1.
set -euo pipefail

URL="${HYPHA_URL:-https://hypha.s33d.fr}"
URL="${URL%/}"
PIN="${HYPHA_TLS_PIN:-}"
BIN=/usr/bin/hypha-agent
CONF_DIR=/etc/hypha
CONF="$CONF_DIR/hypha.yaml"
STATE_DIR=/var/lib/hypha
UNIT=/etc/systemd/system/hypha-agent.service
SERVICE=hypha-agent.service

die() { echo "hypha-agent : ERREUR : $*" >&2; echo "hypha-agent : échec de l'installation : $*"; exit 1; }
info() { echo "hypha-agent : $*"; }

[ "$(id -u)" -eq 0 ] || die "lancer en root (sudo)"
command -v systemctl >/dev/null 2>&1 || die "systemd introuvable (systemctl absent)"

# Un agent posé par paquet .deb/.rpm se met à jour par le gestionnaire de paquets :
# le script ne doit pas écraser des fichiers qui appartiennent au paquet.
packaged() {
    { command -v dpkg-query >/dev/null 2>&1 && dpkg-query -W -f='${Status}' hypha-agent 2>/dev/null | grep -q "install ok installed"; } ||
    { command -v rpm >/dev/null 2>&1 && rpm -q hypha-agent >/dev/null 2>&1; }
}

uninstall() {
    packaged && die "agent installé par paquet : le retirer par apt remove hypha-agent (ou dnf remove)"
    systemctl disable --now "$SERVICE" >/dev/null 2>&1 || true
    rm -f "$UNIT"
    systemctl daemon-reload
    rm -f "$BIN"
    info "conf ($CONF_DIR) et données ($STATE_DIR) gardées ; pour tout effacer : rm -rf $CONF_DIR $STATE_DIR"
    echo "hypha-agent désinstallé"
}

case "${1:-}" in
    --uninstall) uninstall; exit 0 ;;
    "") ;;
    *) die "option inconnue : $1 (seule --uninstall existe)" ;;
esac

packaged && die "agent installé par paquet : le mettre à jour par le gestionnaire de paquets"

case "$(uname -m)" in
    x86_64 | amd64) ARCH=amd64 ;;
    aarch64 | arm64) ARCH=arm64 ;;
    *) die "architecture non prise en charge : $(uname -m) (amd64 et arm64 seulement)" ;;
esac

if [ -n "$PIN" ]; then
    [[ "$PIN" =~ ^sha256//[A-Za-z0-9+/]{43}=(\;sha256//[A-Za-z0-9+/]{43}=)*$ ]] ||
        die "HYPHA_TLS_PIN invalide : « $PIN » (attendu sha256//<empreinte base64>)"
    command -v curl >/dev/null 2>&1 || die "HYPHA_TLS_PIN demande curl (wget ne vérifie pas une empreinte)"
fi

fetch() { # url destination
    if [ -n "$PIN" ]; then
        # Chaîne non reconnue acceptée (-k), clé du serveur exigée : curl coupe avant tout échange
        # si elle diffère de l'empreinte.
        curl -fsSL -k --pinnedpubkey "$PIN" --retry 3 --connect-timeout 15 -o "$2" "$1"
    elif command -v curl >/dev/null 2>&1; then
        curl -fsSL --retry 3 --connect-timeout 15 -o "$2" "$1"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -T 15 -t 3 -O "$2" "$1"
    else
        die "ni curl ni wget pour télécharger"
    fi
}

sha() { sha256sum "$1" | awk '{print $1}'; }

install -d -m 0755 "$CONF_DIR"
# Compléments de conf (conf.d/*.yaml, fusionnés après hypha.yaml) : ils peuvent porter des
# secrets (community SNMP, clés v3), réservés à root.
install -d -m 0700 "$CONF_DIR/conf.d"
install -d -m 0700 "$STATE_DIR"
# Répertoire de travail sous /var/lib (et non /tmp, souvent monté noexec) : le binaire
# téléchargé y est exécuté pour lire sa version et valider la conf.
TMP="$(mktemp -d "$STATE_DIR/.install.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

# --- Binaire, vérifié par SHA256SUMS -----------------------------------------------------
NAME="hypha-agent-linux-$ARCH"
fetch "$URL/install/$NAME" "$TMP/$NAME" || die "téléchargement impossible : $URL/install/$NAME"
fetch "$URL/install/SHA256SUMS" "$TMP/SHA256SUMS" || die "téléchargement impossible : $URL/install/SHA256SUMS"
expected="$(awk -v f="$NAME" '$2 == f || $2 == "*" f {print $1; exit}' "$TMP/SHA256SUMS")"
[ -n "$expected" ] || die "$NAME absent de SHA256SUMS"
actual="$(sha "$TMP/$NAME")"
[ "$expected" = "$actual" ] || die "somme SHA-256 incorrecte pour $NAME (attendue $expected, obtenue $actual)"
chmod 0755 "$TMP/$NAME"
VERSION="$("$TMP/$NAME" version | awk '{print $2}')"
[ -n "$VERSION" ] || die "le binaire téléchargé ne s'exécute pas"

bin_changed=0
if [ -f "$BIN" ] && [ "$(sha "$BIN")" = "$actual" ]; then
    info "binaire $VERSION déjà en place"
else
    install -m 0755 "$TMP/$NAME" "$BIN.new"
    mv -f "$BIN.new" "$BIN"
    bin_changed=1
    info "binaire $VERSION posé : $BIN"
fi

# --- Configuration ----------------------------------------------------------------------
yaml_quote() { # chaîne YAML entre guillemets doubles
    local s="${1//\\/\\\\}"
    s="${s//\"/\\\"}"
    printf '"%s"' "$s"
}

# Étiquettes : JSON tel quel (un objet JSON est du YAML valide), ou « clé:valeur,… ».
tags_yaml() {
    local raw="${HYPHA_TAGS:-}" out="" pair k v
    raw="$(printf '%s' "$raw" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')"
    if [ -z "$raw" ]; then
        printf '{}'
        return
    fi
    if [ "${raw:0:1}" = "{" ]; then
        [ "${raw: -1}" = "}" ] || { echo "HYPHA_TAGS : objet JSON mal fermé" >&2; return 1; }
        printf '%s' "$raw"
        return
    fi
    local IFS=','
    for pair in $raw; do
        pair="$(printf '%s' "$pair" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')"
        [ -n "$pair" ] || continue
        k="${pair%%:*}"
        v="${pair#*:}"
        [ "$k" != "$pair" ] || v=""
        [[ "$k" =~ ^[a-z0-9_./-]+$ ]] || { echo "HYPHA_TAGS : clé « $k » hors de [a-z0-9_.-/]" >&2; return 1; }
        out="${out:+$out, }$k: $(yaml_quote "$v")"
    done
    printf '{%s}' "$out"
}

write_new_conf() { # destination
    umask 077
    cat >"$1" <<EOF
# Configuration de hypha-agent, écrite par install.sh. Modèle commenté :
# https://hypha.s33d.fr (Réglages → Installation). Vérifier : hypha-agent check-config
api_key: $(yaml_quote "$HYPHA_API_KEY")
url: $(yaml_quote "$URL")
$( [ -z "$PIN" ] || printf 'tls_pin: %s' "$(yaml_quote "$PIN")" )
hostname: $(yaml_quote "${HYPHA_HOSTNAME:-}")
tags: $TAGS_YAML
interval: 15s
buffer: {dir: $STATE_DIR/buffer, max_mb: 256}
logs:
  enabled: true
  journald: true
  files: []
  exclude_patterns: []
processes: {enabled: true, include: []}
network: {enabled: true}
containers: {enabled: auto}
checks:
  systemd_units: []
  http: []
  tcp: []
statsd: {enabled: true, listen: "127.0.0.1:8125"}
otlp: {enabled: false, listen: "127.0.0.1:4318"}
EOF
}

# Réécrit une clé de premier niveau (api_key, url) d'une conf existante ; l'ajoute si absente.
set_key() { # fichier clé valeur
    local file="$1" key="$2" val
    val="$(yaml_quote "$3")"
    if grep -q "^$key:" "$file"; then
        awk -v k="$key" -v v="$val" 'index($0, k ":") == 1 { print k ": " v; next } { print }' "$file" >"$file.tmp"
        mv -f "$file.tmp" "$file"
    else
        printf '%s: %s\n' "$key" "$val" >>"$file"
    fi
}

# La conf est vérifiée telle que le service la lira : sans HYPHA_API_KEY ni HYPHA_URL,
# qui ne sont pas transmises à systemd.
check_conf() { # binaire fichier
    env -u HYPHA_API_KEY -u HYPHA_URL -u HYPHA_TLS_PIN "$1" check-config --config "$2" >/dev/null ||
        die "configuration refusée par check-config (détail ci-dessus) : $2"
}

conf_changed=0
if [ ! -f "$CONF" ]; then
    [ -n "${HYPHA_API_KEY:-}" ] || die "HYPHA_API_KEY manquante (première installation)"
    TAGS_YAML="$(tags_yaml)" || die "HYPHA_TAGS invalide : « ${HYPHA_TAGS:-} »"
    write_new_conf "$TMP/hypha.yaml"
    check_conf "$TMP/$NAME" "$TMP/hypha.yaml"
    install -m 0600 "$TMP/hypha.yaml" "$CONF"
    conf_changed=1
    info "configuration créée : $CONF"
elif [ "${HYPHA_FORCE_CONF:-0}" = "1" ]; then
    cp -p "$CONF" "$TMP/hypha.yaml"
    [ -z "${HYPHA_API_KEY:-}" ] || set_key "$TMP/hypha.yaml" api_key "$HYPHA_API_KEY"
    [ -z "${HYPHA_URL:-}" ] || set_key "$TMP/hypha.yaml" url "$URL"
    [ -z "$PIN" ] || set_key "$TMP/hypha.yaml" tls_pin "$PIN"
    if cmp -s "$TMP/hypha.yaml" "$CONF"; then
        info "configuration inchangée : $CONF"
    else
        check_conf "$TMP/$NAME" "$TMP/hypha.yaml"
        install -m 0600 "$TMP/hypha.yaml" "$CONF"
        conf_changed=1
        info "api_key et url réécrites (HYPHA_FORCE_CONF=1) : $CONF"
    fi
elif [ -n "$PIN" ] && ! grep -q '^tls_pin:' "$CONF"; then
    # Sans l'empreinte, l'agent refuserait le certificat que l'installation vient d'accepter.
    cp -p "$CONF" "$TMP/hypha.yaml"
    set_key "$TMP/hypha.yaml" tls_pin "$PIN"
    check_conf "$TMP/$NAME" "$TMP/hypha.yaml"
    install -m 0600 "$TMP/hypha.yaml" "$CONF"
    conf_changed=1
    info "configuration existante gardée, tls_pin ajouté : $CONF"
else
    chmod 0600 "$CONF"
    info "configuration existante gardée telle quelle : $CONF"
fi

# --- Unité systemd (même contenu que packaging/hypha-agent.service) ----------------------
read -r -d '' UNIT_CONTENT <<'EOF' || true
[Unit]
Description=HYPHA : agent de collecte (métriques, journaux, contrôles)
Documentation=https://hypha.s33d.fr
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
# root : lecture de tous les journaux et du /proc de tous les processus (contrat § 5).
# La réduction des droits est prévue plus tard.
User=root
# Mise à jour signée (contrat § 27) : /usr est en lecture seule pour l'agent (ProtectSystem) ;
# il prépare le binaire vérifié sous /var/lib/hypha/update puis sort, et cette commande, lancée
# avec tous les droits (+) avant l'agent, revérifie la signature et le pose. Sans préparation,
# elle ne fait rien ; en échec, l'ancien binaire reste et l'agent démarre quand même (-).
ExecStartPre=-+/usr/bin/hypha-agent apply-update
ExecStart=/usr/bin/hypha-agent run --config /etc/hypha/hypha.yaml
Restart=always
RestartSec=10
KillMode=mixed
TimeoutStopSec=30
StateDirectory=hypha
StateDirectoryMode=0700

# L'agent ne doit jamais gêner l'hôte qu'il surveille : priorité basse et plafonds.
# Les plafonds ne servent qu'en cas d'emballement (l'agent vise < 1 % d'un cœur, < 60 Mo).
Nice=10
IOSchedulingClass=idle
CPUQuota=25%
MemoryHigh=128M
MemoryMax=256M
TasksMax=64
LimitNOFILE=8192

# Durcissement : /usr, /boot et /etc en lecture seule pour l'agent, qui n'écrit que sous /var/lib/hypha.
NoNewPrivileges=yes
ProtectSystem=full
ProtectHome=read-only
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF
unit_changed=0
if [ ! -f "$UNIT" ] || ! printf '%s\n' "$UNIT_CONTENT" | cmp -s - "$UNIT"; then
    printf '%s\n' "$UNIT_CONTENT" >"$UNIT"
    chmod 0644 "$UNIT"
    systemctl daemon-reload
    unit_changed=1
    info "unité posée : $UNIT"
fi
systemctl enable "$SERVICE" >/dev/null 2>&1 || die "systemctl enable $SERVICE a échoué"

# La conf en place (gardée ou nouvelle) doit être valide avant tout (re)démarrage.
check_conf "$BIN" "$CONF"

# --- Démarrage : seulement ce qui est nécessaire ----------------------------------------
if ! systemctl is-active --quiet "$SERVICE"; then
    systemctl start "$SERVICE" || die "démarrage refusé (journalctl -u $SERVICE)"
    info "service démarré"
elif [ "$bin_changed" -eq 1 ] || [ "$conf_changed" -eq 1 ] || [ "$unit_changed" -eq 1 ]; then
    systemctl restart "$SERVICE" || die "redémarrage refusé (journalctl -u $SERVICE)"
    info "service redémarré"
else
    info "service déjà actif, rien à redémarrer"
fi

# Un agent qui s'arrête juste après le démarrage (clé refusée, conf) doit se voir ici.
sleep 3
if ! systemctl is-active --quiet "$SERVICE"; then
    journalctl -u "$SERVICE" -n 20 --no-pager 2>/dev/null || true
    die "le service ne reste pas actif (journal ci-dessus)"
fi
systemctl --no-pager --lines=0 status "$SERVICE" 2>/dev/null | sed -n '1,4p' || true
echo "hypha-agent $VERSION actif"
