#!/usr/bin/env bash
# Construit les images de HYPHA auto-hébergé pour linux/amd64 et linux/arm64 et les publie sur le
# registre S33D :
#   registre-vm.s33d.fr:8443/hypha/server:$VERSION   (API, worker, migrations)
#   registre-vm.s33d.fr:8443/hypha/web:$VERSION      (nginx, interface, binaires de l'agent)
# À lancer sur la machine de construction, depuis la racine du dépôt, après que
# agent/dist contient les binaires de la version publiée.
# L'image serveur exécute pip à la construction : l'architecture étrangère passe par QEMU
# (binfmt), posé ici s'il manque. Un constructeur BuildKit temporaire est créé puis supprimé.
# Usage : VERSION=0.2.0 packaging/docker/build-images.sh
set -euo pipefail
VERSION="${VERSION:?VERSION à fixer, par exemple VERSION=0.2.0}"
REG="${REG:-registre-vm.s33d.fr:8443}"
BUILDER=hypha-images-$$
cd "$(dirname "$0")/../.."

[ -f agent/dist/hypha-agent-linux-amd64 ] || { echo "agent/dist incomplet" >&2; exit 1; }
if ! ls /proc/sys/fs/binfmt_misc/ | grep -q qemu-x86_64 && [ "$(uname -m)" != x86_64 ]; then
    docker run --privileged --rm tonistiigi/binfmt:latest --install amd64 >/dev/null
fi
if ! ls /proc/sys/fs/binfmt_misc/ | grep -q qemu-aarch64 && [ "$(uname -m)" != aarch64 ]; then
    docker run --privileged --rm tonistiigi/binfmt:latest --install arm64 >/dev/null
fi

docker buildx create --name "$BUILDER" --driver docker-container >/dev/null
trap 'docker buildx rm --force "$BUILDER" >/dev/null 2>&1 || true; rm -rf build/oci-server build/oci-web' EXIT
mkdir -p build
for nom in server web; do
    f=packaging/docker/Dockerfile; [ "$nom" = web ] && f=packaging/docker/Dockerfile.web
    rm -rf "build/oci-$nom"
    nice -n 19 docker buildx build --builder "$BUILDER" -f "$f" \
        --platform linux/amd64,linux/arm64 --provenance=false \
        -t "$REG/hypha/$nom:$VERSION" --output "type=oci,dest=build/oci-$nom,tar=false" .
    oras cp --from-oci-layout "build/oci-$nom:$VERSION" "$REG/hypha/$nom:$VERSION"
    oras tag "$REG/hypha/$nom:$VERSION" latest
    echo "publié : $REG/hypha/$nom:$VERSION (+ latest)"
done
