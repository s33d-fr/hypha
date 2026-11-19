#!/usr/bin/env bash
# Construit l'image Docker de hypha-agent pour linux/amd64 et linux/arm64 à partir des
# binaires de dist/ (lancer packaging/build.sh avant), SANS la pousser :
#   - dist/oci/ : disposition OCI multi-architecture, à publier par exemple avec
#       oras cp --from-oci-layout dist/oci:$VERSION registre-vm.s33d.fr:8443/hypha/agent-docker:$VERSION
#   - image locale $IMAGE:$VERSION-<arch> de l'architecture de la machine, pour l'essayer.
# Le pilote « docker » de buildx ne produit pas plusieurs architectures avec le stockage
# d'images classique : un constructeur BuildKit temporaire est créé, puis supprimé.
# Usage, depuis agent/ : VERSION=0.1.1 packaging/docker/build-image.sh
set -euo pipefail

VERSION="${VERSION:?VERSION à fixer, par exemple VERSION=0.1.1}"
IMAGE="${IMAGE:-registre-vm.s33d.fr:8443/hypha/agent-docker}"
BUILDER=hypha-build-$$
cd "$(dirname "$0")/../.."
for a in amd64 arm64; do
    [ -x "dist/hypha-agent-linux-$a" ] || { echo "dist/hypha-agent-linux-$a absent : lancer packaging/build.sh" >&2; exit 1; }
done

docker buildx create --name "$BUILDER" --driver docker-container >/dev/null
trap 'docker buildx rm --force "$BUILDER" >/dev/null 2>&1 || true' EXIT
rm -rf dist/oci
nice -n 19 docker buildx build --builder "$BUILDER" -f packaging/docker/Dockerfile \
    --platform linux/amd64,linux/arm64 --build-arg VERSION="$VERSION" --provenance=false \
    -t "$IMAGE:$VERSION" --output "type=oci,dest=dist/oci,tar=false" .

native="$(docker version --format '{{.Server.Arch}}')"
nice -n 19 docker buildx build --builder default -f packaging/docker/Dockerfile \
    --platform "linux/$native" --build-arg VERSION="$VERSION" \
    -t "$IMAGE:$VERSION-$native" --load .
echo "disposition OCI : dist/oci ; image locale : $IMAGE:$VERSION-$native"
