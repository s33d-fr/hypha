---
id: agent-docker
path: /docs/agent-docker/
title: Lancer l'agent en conteneur Docker
nav: Docker
section: Installer l'agent
order: 12
updated: 2026-11-19
description: Lancer hypha-agent dans un conteneur Docker qui observe l'hôte et ses conteneurs ; volumes, droits et fichiers journaux.
lead: L'image de l'agent observe l'hôte qui la porte, ses processus, son réseau et ses conteneurs.
---
## Lancer

```bash
docker run -d --name hypha-agent --restart unless-stopped --network host --pid host \
  --cap-add SYS_PTRACE --security-opt apparmor=unconfined \
  -v /proc:/host/proc:ro -v /sys:/host/sys:ro -v /:/host/root:ro \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v hypha-agent:/var/lib/hypha \
  -e HYPHA_API_KEY=hph_votre_cle -e HYPHA_URL=https://hypha.s33d.fr \
  registre-vm.s33d.fr/hypha/agent-docker:0.4.21
```

La page **Réglages › Installer l'agent**, onglet **Docker**, donne la commande avec la version publiée du moment.

| Option | Pourquoi |
|---|---|
| `--network host`, `--pid host` | voir le réseau et les processus de l'hôte, pas ceux du conteneur |
| `--cap-add SYS_PTRACE`, `apparmor=unconfined` | rattacher chaque connexion à son processus |
| `/proc`, `/sys`, `/` en lecture seule | mesures du système et des disques de l'hôte |
| `docker.sock` en lecture seule | liste et mesures des conteneurs |
| volume `hypha-agent` | identité de l'agent et tampon d'envoi, gardés d'un redémarrage à l'autre |

## Suivre des fichiers journaux

L'image ne lit pas le journal systemd de l'hôte : pour lui, installez l'agent sur l'hôte ([Linux](/docs/agent-linux/)). Pour suivre des fichiers, montez-les puis montez une configuration qui les déclare :

```bash
-v /var/log/nginx:/host/log/nginx:ro \
-v /etc/hypha/hypha.yaml:/etc/hypha/hypha.yaml:ro
```

```yaml
logs:
  enabled: true
  journald: false
  files:
    - {path: /host/log/nginx/*.log, service: nginx, source: nginx}
```

`HYPHA_API_KEY` et `HYPHA_URL` passés par `-e` l'emportent sur le fichier.

## Ce qui change en conteneur

- La mise à jour se fait par l'image (`docker pull` d'une nouvelle version), jamais par l'agent.
- Les actions à distance sur les services de l'hôte ne sont pas disponibles : l'image n'a pas `systemctl`.
- Les tests synthétiques privés, le SNMP, l'inventaire et les intégrations fonctionnent comme sur un hôte ; leurs réglages se montent dans `/etc/hypha/conf.d/`.

## Relais OpenTelemetry et StatsD

Avec `--network host`, les applications de l'hôte joignent le relais OTLP de l'agent sur `127.0.0.1:4318` et StatsD sur `127.0.0.1:8125/udp`. Activez `otlp.enabled: true` dans la configuration montée. Voir [Traces](/docs/traces/).
