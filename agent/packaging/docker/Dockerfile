# Image Docker de hypha-agent : le binaire statique de dist/ sur alpine.
# Construction : packaging/docker/build-image.sh (après packaging/build.sh).
# Lancement (commande affichée par le serveur) :
#   docker run -d --name hypha-agent --restart unless-stopped --network host --pid host \
#     -v /proc:/host/proc:ro -v /sys:/host/sys:ro -v /:/host/root:ro \
#     -v /var/run/docker.sock:/var/run/docker.sock:ro -v hypha-agent:/var/lib/hypha \
#     -e HYPHA_API_KEY=… -e HYPHA_URL=… <image>
# Ajouter --cap-add SYS_PTRACE --security-opt apparmor=unconfined pour rattacher les
# connexions à leurs processus et lire les E/S par processus : sans ces deux options,
# Docker interdit de lire /proc/<pid>/fd et /proc/<pid>/io des processus de l'hôte.

# Les certificats sont préparés sur l'architecture de la machine de construction : l'image
# finale n'exécute rien pendant la construction, on peut donc la produire pour amd64
# depuis un hôte arm64 sans émulation.
FROM --platform=$BUILDPLATFORM alpine:3 AS certs
RUN apk add --no-cache ca-certificates

FROM alpine:3
ARG TARGETARCH
ARG VERSION=dev
LABEL org.opencontainers.image.title="hypha-agent" \
      org.opencontainers.image.description="Agent de collecte HYPHA (S33D)" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.vendor="S33D" \
      org.opencontainers.image.url="https://hypha.s33d.fr"
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY dist/hypha-agent-linux-${TARGETARCH} /usr/bin/hypha-agent
COPY packaging/docker/hypha.yaml /etc/hypha/hypha.yaml
# Fichiers de l'hôte, montés en lecture seule ; mêmes variables que gopsutil.
# HOST_ETC vise /etc dans la racine de l'hôte : la commande de lancement ne monte pas /etc à part.
ENV HOST_PROC=/host/proc \
    HOST_SYS=/host/sys \
    HOST_ETC=/host/root/etc \
    HOST_ROOT=/host/root
VOLUME /var/lib/hypha
ENTRYPOINT ["/usr/bin/hypha-agent"]
CMD ["run", "--config", "/etc/hypha/hypha.yaml"]
