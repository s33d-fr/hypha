---
id: agent-linux
path: /docs/agent-linux/
title: Installer l'agent sous Linux
nav: Linux
section: Installer l'agent
order: 10
updated: 2026-11-19
description: Installer hypha-agent sous Linux en une commande, ou par paquet .deb et .rpm ; vérifier, mettre à jour et désinstaller.
lead: Un seul binaire, sans dépendance, pour amd64 et arm64. Il tourne en service systemd et pèse moins d'un pour cent d'un cœur.
---
## Ce qu'il faut

- Une distribution avec systemd, en amd64 ou arm64 (Debian, Ubuntu, Red Hat, Rocky, Alma, Fedora, Raspberry Pi OS 64 bits…).
- Un accès sortant en HTTPS vers `hypha.s33d.fr`, directement ou par un mandataire. L'agent n'ouvre aucun port sur l'hôte.
- Une clé d'ingestion : **Réglages › Clés API**, ou le bouton **Créer une clé et l'insérer** de la page **Installer l'agent**.

## Installer en une commande

```bash
curl -fsSL https://hypha.s33d.fr/install/install.sh | sudo HYPHA_API_KEY=hph_votre_cle bash
```

Le script télécharge le binaire de votre architecture, vérifie sa somme SHA-256, écrit `/etc/hypha/hypha.yaml` (droits 600), pose l'unité `hypha-agent.service`, puis la démarre. Il ne pose aucune question et peut être rejoué : une même version ne redémarre pas le service.

Variables acceptées :

| Variable | Rôle |
|---|---|
| `HYPHA_API_KEY` | clé d'ingestion, obligatoire à la première installation |
| `HYPHA_URL` | serveur visé, `https://hypha.s33d.fr` par défaut ; votre adresse en auto-hébergement |
| `HYPHA_TAGS` | étiquettes ajoutées à tout ce que l'agent envoie : `env:prod,role:web` ou `{"env":"prod"}` |
| `HYPHA_HOSTNAME` | nom de l'hôte dans HYPHA (par défaut, le nom système) |
| `HYPHA_FORCE_CONF` | `1` réécrit `api_key` et `url` d'une configuration existante |
| `HYPHA_TLS_PIN` | serveur auto-hébergé à certificat autosigné ou d'autorité interne : empreinte `sha256//…` de sa clé publique, affichée dans **Réglages › Installer l'agent** ; les téléchargements la vérifient et la configuration la garde (`tls_pin`) |

`HYPHA_TAGS` et `HYPHA_HOSTNAME` ne servent qu'à la création du fichier : une configuration existante n'est jamais réécrite, sauf `api_key` et `url` avec `HYPHA_FORCE_CONF=1`.

## Installer par paquet

Les paquets `.deb` et `.rpm` sont publiés à côté du binaire, sous [hypha.s33d.fr/install/](https://hypha.s33d.fr/install/SHA256SUMS) :

```bash
# Debian, Ubuntu (remplacer 0.4.21 par la version de version.txt)
curl -fsSLO https://hypha.s33d.fr/install/hypha-agent_0.4.21_amd64.deb
sudo apt install ./hypha-agent_0.4.21_amd64.deb

# Red Hat, Rocky, Alma, Fedora
sudo dnf install https://hypha.s33d.fr/install/hypha-agent-0.4.21-1.x86_64.rpm
```

Le paquet pose la configuration d'exemple. Renseignez `api_key` dans `/etc/hypha/hypha.yaml`, puis :

```bash
sudo systemctl start hypha-agent
```

Un agent installé par paquet se met à jour par le gestionnaire de paquets ; le script d'installation refuse alors de le remplacer.

## Vérifier

```bash
sudo hypha-agent status          # état des collecteurs et du dernier envoi
sudo hypha-agent check-config    # configuration fusionnée, secrets masqués, erreurs avec leur fichier
systemctl status hypha-agent
journalctl -u hypha-agent -n 50
```

L'hôte apparaît dans **Infrastructure** dans la minute. La page **Réglages › Agents** montre sa version, l'état de chaque collecteur et la dernière erreur.

## Ce que l'agent collecte d'office

- Mesures du système toutes les 15 secondes : processeur, mémoire, échange, disques, entrées-sorties, réseau, charge, processus.
- Journal systemd (`journalctl`), repris après le dernier envoi réussi.
- Instantané des processus et des connexions réseau chaque minute.
- Conteneurs Docker si `/var/run/docker.sock` existe.
- Inventaire matériel et logiciel au démarrage puis toutes les 6 heures.
- Unités systemd en échec et timers en retard, mises à jour en attente, horloge, certificats, capteurs et disques SMART quand les outils sont présents.
- Les logiciels détectés : PostgreSQL, MySQL, Redis, nginx, HAProxy, Postfix et les autres [intégrations](/docs/integrations/).

Tout se règle dans `/etc/hypha/hypha.yaml` et `/etc/hypha/conf.d/*.yaml` : voir la [référence de configuration](/docs/configuration/).

## Droits et empreinte

- L'agent tourne en `root` pour lire tous les journaux et le `/proc` de chaque processus.
- L'unité systemd le durcit : `/usr`, `/boot` et `/etc` en lecture seule pour lui, `/home` en lecture seule, `/tmp` privé, aucune élévation de droits ; il n'écrit que sous `/var/lib/hypha`.
- Priorité basse (`Nice=10`, entrées-sorties en tâche de fond) et plafonds en cas d'emballement : 25 % d'un cœur, 256 Mo de mémoire.
- Si le serveur est injoignable, les envois attendent dans un tampon disque (256 Mo par défaut), puis repartent.

## Mettre à jour

- Script : rejouez la commande d'installation ; seule une nouvelle version redémarre le service.
- Automatique : avec `auto_update: true`, l'agent suit la version cible fixée dans **Réglages › Agents** et vérifie la signature de chaque binaire. Voir [Flotte et mises à jour](/docs/flotte/).
- Paquet : `apt upgrade` ou `dnf upgrade` après avoir téléchargé le nouveau paquet.

## Désinstaller

```bash
curl -fsSL https://hypha.s33d.fr/install/install.sh | sudo bash -s -- --uninstall
```

La configuration (`/etc/hypha`) et l'état (`/var/lib/hypha`) sont gardés. Pour tout effacer : `sudo rm -rf /etc/hypha /var/lib/hypha`. Un agent installé par paquet se retire par `apt remove hypha-agent` ou `dnf remove hypha-agent`.
