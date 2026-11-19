---
id: explain
path: /docs/explication-et-reparation/
title: Explication des pannes et réparation d'un clic
nav: Explication et réparation
section: Alerter et agir
order: 46
updated: 2026-11-19
description: Comment HYPHA explique une panne avec ses preuves, dans votre langue, grâce au modèle d'IA de S33D, puis propose un geste nommé que vous validez d'un clic et que l'agent exécute.
lead: HYPHA dit ce qui se passe, la cause probable et ses preuves, le correctif. Quand le correctif est un geste connu, il le propose : un humain le valide, l'agent l'exécute.
---
{{video:4-alerte-expliquee}}

## Expliquer

Le bouton **Expliquer** figure sur une alerte (vue Problèmes, vue d'ensemble, moniteurs), sur la fiche d'un hôte, sur le détail d'une ligne de journal, sur une trace et sur une erreur applicative.

HYPHA rassemble alors, dans l'espace et pour l'objet concerné :

- l'hôte : système, cœurs, mémoire, étiquettes, dernier contact ;
- le moniteur : requête, seuils, état, valeur et ses dix derniers changements ;
- les contrôles hors OK de l'hôte ;
- ses journaux des 30 dernières minutes, erreurs d'abord, messages répétés regroupés ;
- ses événements des dernières 24 heures ;
- ses métriques des 30 dernières minutes, résumées (processeur, mémoire, charge, disques, échange, réseau) ;
- ses processus les plus gourmands ;
- pour une trace, les spans en erreur et le chemin critique.

Le modèle répond en quatre parties : **Ce qui se passe**, **Cause probable** (avec ses preuves : une ligne de journal, une valeur mesurée), **Correctif** (les commandes pour le système de l'hôte), **Éviter la récidive**. Il ne doit rien inventer ; si les éléments manquent, il le dit et indique quoi vérifier. Le panneau donne aussi le modèle utilisé, la durée et le contexte lu.

### Où tourne le modèle

- **Service hébergé** : sur les serveurs à GPU de S33D, en France. Les données envoyées au modèle ne partent chez aucun tiers.
- **Auto-hébergement** : sur votre propre serveur de modèle, de type Ollama (`HYPHA_LLM_URL`). Sans serveur de modèle, le bouton n'apparaît pas.

Limite : 60 explications par heure et par espace ; le nombre mensuel dépend de la formule (voir les [tarifs](/tarifs/)).

## Réparer d'un clic

HYPHA relit la partie **Correctif**. S'il y trouve un geste qu'il connaît (`systemctl restart|start|stop <unité>`, `Restart-Service <service>`, `service <nom> restart`), il le propose sous forme de bouton, par exemple « Redémarrer shop-worker.service sur web-01 », à deux conditions : l'agent de l'hôte accepte ce geste, et l'unité ou le service existe sur l'hôte.

1. Un clic ouvre une confirmation : geste, hôte, raison.
2. Si vous êtes administrateur, l'action est créée et approuvée d'un même geste. Sinon, elle attend l'approbation d'un administrateur dans **Réglages › Actions**.
3. L'agent la reçoit par son canal de contrôle, vérifie qu'elle est permise, l'exécute, puis rend le code de sortie et la sortie.
4. Le suivi est en direct : en attente, envoyée, en cours, réussie ou échouée. L'agent renvoie aussitôt l'état de l'unité.

Une action non approuvée expire après une heure ; une action approuvée que l'agent n'a pas prise expire après 10 minutes. Tout passe au [journal d'audit](/docs/comptes-et-roles/#journal-d-audit).

## Ce que l'agent accepte

Les actions sont **coupées par défaut**. Sur chaque hôte, vous décidez :

```yaml
actions:
  enabled: true
  allow: [restart_service, start_service]        # gestes permis
  services: ["nginx*", "shop-*"]                 # unités ou services visés
  deny_services: ["sshd*", "ssh*", "hypha-agent*", "systemd-*"]
  scripts:
    vider-cache: /etc/hypha/scripts/vider-cache.sh
  timeout: 120s
```

- Aucune commande libre : seulement des gestes nommés (`restart_service`, `start_service`, `stop_service`, `run_script`, `update_agent`).
- Un geste hors liste, un service interdit ou une action expirée sont refusés par l'agent, avec le motif.
- Le service de l'agent n'est jamais touché.
- Un script se désigne par son nom ; son chemin vient de la configuration de l'agent, jamais du serveur. Il est lancé sans argument, sa sortie est bornée à 64 Kio.
- En conteneur, les actions sur les services de l'hôte ne sont pas disponibles.

Une action se propose aussi à la main, par le formulaire de proposition (geste, unité ou script, raison) ou par l'[API](/docs/api/). L'historique et les actions en attente sont dans **Réglages › Actions**.
