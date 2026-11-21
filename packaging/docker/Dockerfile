# hypha-server (API et worker) pour l'auto-hébergé.
# Contexte de construction : la racine du dépôt (voir docker-compose.yml).
FROM python:3.14-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

RUN groupadd --system --gid 10001 hypha \
 && useradd --system --uid 10001 --gid hypha --home-dir /opt/hypha --no-create-home hypha

WORKDIR /opt/hypha/server
COPY server/requirements.txt ./requirements.txt
RUN pip install -r requirements.txt

COPY server/hypha ./hypha
COPY schema /opt/hypha/schema
# Version de l'agent publiée avec ce kit (servie par l'image web) : la page d'installation, la
# commande Docker et la mise à jour de la flotte la lisent ici, jamais une valeur par défaut figée.
COPY agent/dist/version.txt /opt/hypha/install/version.txt

ENV HYPHA_SCHEMA_DIR=/opt/hypha/schema \
    HYPHA_INSTALL_DIR=/opt/hypha/install \
    HYPHA_LISTEN=0.0.0.0:8231 \
    HYPHA_MODE=selfhosted

USER hypha
EXPOSE 8231
ENTRYPOINT ["python", "-m", "hypha"]
CMD ["api"]
