---
id: docs
path: /docs/
title: Documentation de HYPHA
nav: Vue d'ensemble
section: Démarrer
order: 1
updated: 2026-11-19
description: Documentation de HYPHA, la supervision de S33D : installer l'agent, brancher ses intégrations, suivre métriques, journaux et traces, alerter et réparer.
lead: HYPHA supervise serveurs, applications, réseau et clouds. Il dit la cause d'une panne dans votre langue, puis propose la réparation, que vous validez d'un clic.
---
## Par où commencer

<div class="cards">
<a class="card" href="/docs/demarrage/"><b>Démarrer</b><span>Créer un compte, installer le premier agent, recevoir la première alerte.</span></a>
<a class="card" href="/docs/videos/"><b>Vidéos</b><span>Sept démonstrations courtes tournées dans la vraie interface.</span></a>
<a class="card" href="/docs/agent-linux/"><b>Installer l'agent</b><span>Linux, Windows, Docker et Kubernetes.</span></a>
<a class="card" href="/docs/integrations/"><b>Intégrations</b><span>Bases, messagerie, serveurs web, SNMP, clouds et hyperviseurs.</span></a>
<a class="card" href="/docs/moniteurs/"><b>Moniteurs</b><span>Seuils, anomalies, prévisions et plus de 90 modèles prêts à l'emploi.</span></a>
<a class="card" href="/docs/explication-et-reparation/"><b>Explication et réparation</b><span>La cause d'une panne, ses preuves, puis le geste validé d'un clic.</span></a>
<a class="card" href="/docs/api/"><b>API</b><span>Clés, envoi de données, OpenTelemetry, automatisation.</span></a>
<a class="card" href="/docs/auto-hebergement/"><b>Auto-hébergement</b><span>Le même logiciel sur votre serveur, vos données chez vous.</span></a>
</div>

## Ce que fait HYPHA

- **Infrastructure** : hôtes Linux et Windows, processus, réseau, conteneurs, inventaire matériel et logiciel, certificats.
- **Journaux** : collecte, recherche plein texte, facettes, motifs, filtres d'exclusion, masquage des données sensibles.
- **Traces** : OpenTelemetry, carte des services, suivi des erreurs, mesure de l'expérience réelle des visiteurs (RUM).
- **Réseau et clouds** : équipements SNMP, AWS, Azure, Google Cloud, OVHcloud, Scaleway, vSphere, Proxmox, Hyper-V, KVM.
- **Alertes** : moniteurs, vue Problèmes, onze types de canaux, maintenances, escalades, incidents, SLO, pages de statut.
- **Explication et réparation** : l'IA de S33D décrit la cause probable avec ses preuves ; un geste nommé se valide d'un clic et l'agent l'exécute.

## Deux façons de l'utiliser

| | Service hébergé | Auto-hébergement |
|---|---|---|
| Où vivent les données | sur l'infrastructure en propre de S33D, en France | sur votre serveur |
| Formules | Découverte (gratuite), Pro, Entreprise | Libre (gratuite) |
| Mise en route | un compte S33D, puis l'agent | `docker compose up -d`, puis l'agent |

Les formules et leurs prix sont sur la page [Tarifs](/tarifs/). Le serveur est publié sous licence AGPL v3, l'agent sous licence Apache 2.0.

## Les mots de HYPHA

- **Espace** : votre organisation dans HYPHA. Membres, clés, moniteurs et données appartiennent à un espace.
- **Agent** : le programme `hypha-agent`, installé sur chaque machine. Il envoie métriques, journaux et contrôles, et interroge les équipements et les API qu'on lui confie.
- **Hôte** : une machine suivie. Un équipement SNMP ou une ressource cloud apparaît aussi comme un hôte.
- **Moniteur** : une règle qui évalue des données et change d'état (OK, avertissement, alerte, sans données).
- **Groupe** : une ligne d'un moniteur, par exemple `host:web-02` pour un moniteur groupé par hôte.
