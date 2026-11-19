---
id: logs
path: /docs/journaux/
title: Journaux
nav: Journaux
section: Observer
order: 31
updated: 2026-11-19
description: Collecter, chercher et maîtriser les journaux dans HYPHA : syntaxe de recherche, facettes, motifs, suivi en direct, filtres d'exclusion, découpage à l'arrivée, masquage, métriques tirées des journaux.
lead: Tous les journaux du parc au même endroit, cherchés en une ligne, découpés et masqués à l'arrivée, avec la maîtrise de leur volume.
---
## D'où viennent les journaux

- **Linux** : le journal systemd et les fichiers déclarés dans `logs.files`.
- **Windows** : les journaux d'événements (System et Application d'office).
- **Applications** : OpenTelemetry (`/v1/logs`), directement ou par le relais de l'agent.
- Une ligne JSON est décodée par l'agent : `message` ou `msg`, `level` ou `severity`, le reste en attributs.

Voir [la configuration des journaux](/docs/configuration/#journaux).

{{video:2-journaux}}

## Chercher

| Syntaxe | Effet |
|---|---|
| `timeout connexion` | lignes qui contiennent les deux mots (ET implicite, sans casse) |
| `"phrase exacte"` | la phrase telle quelle |
| `service:nginx`, `host:web-01`, `status:error`, `source:journald` | égalité sur un champ ; `service:shop*` pour un préfixe |
| `@http.status_code:502` | valeur d'un attribut |
| `-service:sshd` | exclusion |
| `status:error OR status:critical` | l'un ou l'autre |

Statuts : `emergency`, `alert`, `critical`, `error`, `warning`, `notice`, `info`, `debug`.

La page montre l'histogramme par statut et les facettes : statut, service, hôte, source, et, quand elles existent, code HTTP et méthode. Un clic sur une valeur l'ajoute à la requête.

- **Direct** : les nouvelles lignes arrivent toutes les 2 secondes.
- **Contexte** : les lignes du même hôte et du même service autour d'une ligne.
- **Motifs** : les messages semblables regroupés en gabarits (nombres, identifiants, adresses, chemins remplacés), comptés, avec leur part et leurs statuts. Un clic filtre sur le motif.
- **Vues** : une recherche s'enregistre et se retrouve dans le menu Vues.
- **Expliquer** : depuis le détail d'une ligne, l'IA de S33D décrit ce qui se passe (voir [Explication et réparation](/docs/explication-et-reparation/)).

## Découpage à l'arrivée

HYPHA découpe les formats courants en attributs, sans écraser un attribut déjà présent :

- accès d'uvicorn, de nginx et d'Apache (format combined), HAProxy (httplog) : `http.method`, `http.url_path`, `http.status_code`, `http.useragent`, `network.client.ip`, `network.bytes_written`, `duration`, `haproxy.frontend`, `haproxy.backend`, `haproxy.server` ;
- `sshd`, `sudo`, `su`, `fail2ban` : `evt.name`, `evt.outcome`, `usr.name`, `network.client.ip`, `sudo.command`, `fail2ban.jail`.

Une ligne d'information dont le code HTTP vaut 500 ou plus devient une erreur, 400 ou plus un avertissement. Un statut plus grave déjà posé n'est jamais abaissé.

## Filtres d'exclusion et échantillonnage

Les journaux d'accès et les sondes de santé font souvent l'essentiel du volume. **Réglages › Journaux** montre le volume par service et par hôte, et pose des filtres appliqués à l'arrivée, avant l'écriture :

- **Exclure** : les lignes qui correspondent à la requête sont jetées.
- **Échantillonner** : on garde une part des lignes (par exemple 10 %), tirée de façon stable.
- Le premier filtre qui correspond décide ; l'ordre se change en glissant.
- L'aperçu donne, avant d'enregistrer, le nombre de lignes par heure concernées, leur part du total et cinq exemples.
- Les erreurs et plus graves ne sont jamais jetées par un échantillonnage, seulement par une exclusion explicite sur le statut.

Depuis la page Journaux, **Exclure ces lignes** préremplit un filtre avec la requête en cours. Les lignes jetées ne comptent pas dans votre consommation.

## Masquage des données sensibles

Appliqué à l'arrivée, avant l'écriture, sur le message et les attributs texte. Une donnée masquée devient `[masqué:<règle>]`.

| Règle | Ce qu'elle masque | État d'un espace neuf |
|---|---|---|
| `secrets` | jetons Bearer, `password=…`, clés AWS, GitHub, Slack, clés HYPHA, JWT, clés privées PEM | active |
| `credit_card` | numéros de carte (contrôle de Luhn) | proposée |
| `iban` | IBAN | proposée |
| `email` | adresses e-mail | proposée |
| `phone_fr` | numéros de téléphone français | proposée |
| `ipv4_public` | adresses IPv4 publiques | proposée |
| `fr_nir` | numéros de sécurité sociale | proposée |

Un administrateur active les règles et vérifie un texte d'essai dans **Réglages › Journaux**.

## Métriques tirées des journaux

Une requête de journaux peut alimenter une métrique : un compte de lignes, ou la distribution d'un attribut (`.avg`, `.p50`, `.p95`, `.p99`, `.max`), par groupe. Ces métriques se tracent et s'alertent comme les autres et restent après la fin de la conservation des journaux.

## Erreurs applicatives

Une ligne d'erreur qui porte une pile (Python, Java, Go, Node) ou des attributs `error.kind` et `error.stack` alimente la page **Erreurs**, avec les erreurs des traces et du navigateur. Voir [Traces](/docs/traces/#suivi-des-erreurs).

## Conservation

La durée de conservation dépend de la formule : voir les [tarifs](/tarifs/). En auto-hébergement, elle est réglée par vous.
