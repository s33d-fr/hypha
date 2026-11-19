#!/usr/bin/env bash
# Construit les binaires, les paquets .deb/.rpm, SHA256SUMS et sa signature dans dist/.
# À lancer depuis agent/ sur une machine de construction (jamais sur un poste de travail) :
#   VERSION=0.1.0 packaging/build.sh
# Prérequis : Go (version de go.mod), nfpm (go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest),
# openssl (signature). Concurrence bornée et priorité basse : la machine de construction peut
# être un hôte de production.
#
# Signature (contrat § 27) : si la clé privée de publication existe (HYPHA_SIGNING_KEY, par
# défaut signing/release.key à la racine de l'arbre de construction), SHA256SUMS est signé par packaging/sign.sh
# du dépôt, puis la signature est vérifiée avec la clé publique embarquée dans l'agent
# (internal/update/release_pubkey.txt) : une publication que l'agent refuserait s'arrête ici.
# Sans clé, la publication part non signée et les agents refuseront de s'y mettre à jour.
set -euo pipefail

VERSION="${VERSION:?VERSION à fixer, par exemple VERSION=0.1.0}"
NFPM="${NFPM:-nfpm}"
export CGO_ENABLED=0 GOMAXPROCS="${GOMAXPROCS:-4}" GOFLAGS="${GOFLAGS:--p=4}"

cd "$(dirname "$0")/.."
AGENT_DIR="$(pwd)"
SIGN="${SIGN:-$AGENT_DIR/../packaging/sign.sh}"
KEY="${HYPHA_SIGNING_KEY:-$AGENT_DIR/../signing/release.key}"
mkdir -p dist/pkg
nice -n 19 go vet ./...
GOOS=windows nice -n 19 go vet ./...

for target in linux/amd64 linux/arm64 windows/amd64; do
    os="${target%/*}" arch="${target#*/}" ext=""
    [ "$os" = windows ] && ext=.exe
    GOOS="$os" GOARCH="$arch" nice -n 19 go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$VERSION" \
        -o "dist/hypha-agent-$os-$arch$ext" ./cmd/hypha-agent
done

for arch in amd64 arm64; do
    cp "dist/hypha-agent-linux-$arch" dist/pkg/hypha-agent
    for packager in deb rpm; do
        VERSION="$VERSION" ARCH="$arch" "$NFPM" pkg --packager "$packager" --config packaging/nfpm.yaml --target dist/
    done
done
rm -rf dist/pkg

cd dist
# version.txt est couvert par la signature : l'agent refuse une publication d'une autre
# version que sa cible avant même d'en télécharger le binaire.
echo "$VERSION" >version.txt
rm -f SHA256SUMS.sig
sha256sum hypha-agent-linux-amd64 hypha-agent-linux-arm64 hypha-agent-windows-amd64.exe ./*.deb ./*.rpm version.txt |
    sed 's| \./| |' >SHA256SUMS

if [ -r "$KEY" ]; then
    [ -f "$SIGN" ] || { echo "clé de signature présente mais $SIGN introuvable (SIGN=…)"; exit 1; }
    HYPHA_SIGNING_KEY="$KEY" bash "$SIGN" "$(pwd)"
    # Vérification avec la clé publique embarquée : en-tête DER d'une clé Ed25519 + 32 octets.
    pubder="$(mktemp)" sigbin="$(mktemp)"
    trap 'rm -f "$pubder" "$sigbin"' EXIT
    { printf '\x30\x2a\x30\x05\x06\x03\x2b\x65\x70\x03\x21\x00'; base64 -d "$AGENT_DIR/internal/update/release_pubkey.txt"; } >"$pubder"
    base64 -d SHA256SUMS.sig >"$sigbin"
    openssl pkeyutl -verify -pubin -keyform DER -inkey "$pubder" -rawin -in SHA256SUMS -sigfile "$sigbin" >/dev/null ||
        { echo "SHA256SUMS.sig ne correspond pas à la clé publique embarquée : publication arrêtée"; rm -f SHA256SUMS.sig; exit 1; }
    echo "signature vérifiée avec la clé embarquée"
else
    echo "AVERTISSEMENT : clé de signature $KEY absente : SHA256SUMS non signé, mise à jour automatique impossible vers $VERSION"
fi
ls -l
