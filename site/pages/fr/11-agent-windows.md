---
id: agent-windows
path: /docs/agent-windows/
title: Installer l'agent sous Windows
nav: Windows
section: Installer l'agent
order: 11
updated: 2026-11-19
description: Installer hypha-agent sous Windows et Windows Server en une commande PowerShell ; service, journaux d'événements, mise à jour et désinstallation.
lead: Un service Windows, « Hypha Agent », qui lit les compteurs de performance, les journaux d'événements et les services.
---
## Ce qu'il faut

- Windows Server 2016 ou plus récent, ou Windows 10 et 11, en 64 bits (amd64).
- PowerShell lancé **en administrateur**.
- Un accès sortant en HTTPS vers `hypha.s33d.fr`. L'agent n'ouvre aucun port.
- Une clé d'ingestion (**Réglages › Clés API**).

## Installer en une commande

```powershell
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor 3072; $env:HYPHA_API_KEY='hph_votre_cle'; iwr -useb https://hypha.s33d.fr/install/install.ps1 | iex
```

Le début de la ligne active TLS 1.2, absent d'office de PowerShell 5.1 sur Windows Server 2016 et 2019. La page **Réglages › Installer l'agent** donne cette commande toute prête.

Le script télécharge `hypha-agent.exe` dans `C:\Program Files\Hypha`, vérifie sa somme SHA-256, écrit `C:\ProgramData\Hypha\hypha.yaml`, puis installe et démarre le service **Hypha Agent** (nom court `HyphaAgent`), en démarrage automatique, compte LocalSystem, relancé sur échec.

Les variables `HYPHA_URL`, `HYPHA_TAGS`, `HYPHA_HOSTNAME` et `HYPHA_FORCE_CONF` jouent le même rôle que sous [Linux](/docs/agent-linux/#installer-en-une-commande).

## Vérifier

```powershell
& "C:\Program Files\Hypha\hypha-agent.exe" status
& "C:\Program Files\Hypha\hypha-agent.exe" check-config
Get-Service HyphaAgent
```

## Ce que l'agent collecte d'office

- Processeur, mémoire (dont la mémoire engagée), fichier d'échange, disques, entrées-sorties, réseau, processus.
- Journaux d'événements **System** et **Application** (liste réglable : `logs.windows_eventlog`).
- Services : un service à démarrage automatique arrêté est signalé sur Windows Server. Les services de mise à jour connus pour s'arrêter d'eux-mêmes sont écartés ; ajoutez les vôtres dans `checks.windows_services_exclude`.
- Mises à jour Windows en attente, horloge (`w32tm`), certificats du magasin de l'ordinateur.
- IIS, SQL Server, Exchange et Hyper-V quand ils sont présents. Voir [Intégrations](/docs/integrations/) et [Virtualisation](/docs/virtualisation/).

Sur une édition de poste de travail, les services arrêtés ne sont pas signalés d'office : réglez `checks.windows_auto_services: always` pour les suivre.

## Antivirus

Certains antivirus retiennent ou refusent le premier lancement d'un exécutable qu'ils ne connaissent pas. Si l'installation ou une mise à jour échoue avec « Accès refusé », autorisez `C:\Program Files\Hypha\hypha-agent.exe`. Pendant une mise à jour automatique, l'agent attend jusqu'à 3 minutes que le nouveau binaire réponde ; sinon il garde l'ancien et réessaie une heure plus tard, trois fois au plus.

## Mettre à jour

Rejouez la commande d'installation, ou laissez la flotte se mettre à jour avec `auto_update: true` : voir [Flotte et mises à jour](/docs/flotte/).

## Désinstaller

```powershell
$env:HYPHA_UNINSTALL='1'; iwr -useb https://hypha.s33d.fr/install/install.ps1 | iex
```

Le service et le binaire sont retirés. `C:\ProgramData\Hypha` (configuration et état) reste en place.
