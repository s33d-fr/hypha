---
id: faq
kind: page
path: /faq/
title: Questions fréquentes sur HYPHA
updated: 2026-11-19
description: Les réponses aux questions courantes sur HYPHA : prix, formules, hébergement des données, agent, sécurité, IA, auto-hébergement, résiliation.
lead: Les réponses courtes aux questions qu'on nous pose le plus. La documentation donne le détail.
---
## Le produit

### Qu'est-ce que HYPHA ?

HYPHA est la supervision de S33D : mesures des serveurs, journaux, traces, réseau, clouds, alertes et pages de statut, au même endroit. Il dit la cause probable d'une panne avec ses preuves, dans votre langue, puis propose la réparation, que vous validez d'un clic.

### Que peut-on superviser ?

Des serveurs Linux et Windows, des conteneurs Docker, des grappes Kubernetes, des bases de données (PostgreSQL, MySQL, MariaDB, Redis, MongoDB, SQL Server…), des serveurs web et de messagerie, des switchs, NAS, imprimantes et onduleurs en SNMP, des comptes AWS, Azure, Google Cloud, OVHcloud et Scaleway, des hyperviseurs vSphere, Proxmox, Hyper-V et KVM, des sites et des API par tests synthétiques, et vos applications par OpenTelemetry.

### HYPHA remplace-t-il nos outils actuels ?

HYPHA couvre la supervision d'infrastructure, les journaux, les traces, les tests synthétiques, les alertes, les incidents et les pages de statut. Les noms de ses métriques suivent les conventions de Datadog, et il reçoit OpenTelemetry et StatsD : vos tableaux et vos applications instrumentées s'y retrouvent vite.

### En quelles langues ?

L'interface, la documentation et les explications de pannes existent en français et en anglais.

## Prix et formules

### HYPHA est-il gratuit ?

Oui de deux façons : la formule **Libre** (le logiciel sur votre serveur, sans limite) et la formule **Découverte** (hébergée par S33D, 5 hôtes, sans carte bancaire). La formule **Pro** coûte 2 € HT par hôte et par mois, ou 1,50 € en annuel. Voir les [tarifs](/tarifs/).

### Comment les hôtes sont-ils comptés ?

À l'usage réel, à l'heure, sur le mois, pas au pic. Un serveur allumé dix jours sur trente compte pour un tiers d'hôte-mois. Un équipement SNMP ou une ressource cloud lue par un connecteur coûte 0,50 € HT par mois en Pro.

### Que se passe-t-il quand j'atteins une limite de Découverte ?

Ce qui dépasse est refusé et l'interface vous le dit : un sixième hôte n'est pas enregistré, les journaux au-delà de 500 Mo sont refusés jusqu'à minuit UTC, les explications reviennent le 1er du mois. Rien n'est jamais facturé sans passage à une formule payante.

### Y a-t-il un engagement ?

Non en mensuel : vous arrêtez quand vous voulez, l'abonnement court jusqu'à la fin du mois en cours. L'annuel engage un nombre d'hôtes pour un an, payé d'avance, en échange de 25 % de remise.

### Les prix sont-ils HT ou TTC ?

Hors taxes. La TVA française de 20 % s'ajoute ; un professionnel d'un autre pays de l'Union européenne au numéro de TVA valide est en autoliquidation.

### Y a-t-il des remises ?

Oui : −50 % sur Pro pour les associations, l'enseignement et la recherche, sur justificatif. Les collectivités paient le prix Pro et peuvent régler par mandat administratif.

### Comment payer ?

Par carte bancaire, sur la page sécurisée de Stripe. S33D ne voit jamais vos coordonnées bancaires. La formule Entreprise accepte le bon de commande et le mandat administratif. La souscription en ligne des formules payantes ouvre prochainement.

## Données et sécurité

### Où sont hébergées mes données ?

Sur l'infrastructure en propre de S33D, en France. Les explications par l'IA sont rédigées sur les serveurs de S33D : rien ne part chez un fournisseur d'IA. En auto-hébergement, rien ne quitte votre serveur.

### Combien de temps mes données sont-elles gardées ?

Selon la formule : en Découverte, journaux et traces 3 jours, mesures 30 jours ; en Pro, journaux et traces 15 jours (30 ou 90 jours en option), mesures jusqu'à 15 mois au pas d'une heure. Voir la [politique de confidentialité](/confidentialite/).

### L'agent est-il sûr ?

Il n'ouvre aucun port : il envoie ses données en HTTPS et interroge lui-même le serveur. Ses mises à jour sont signées et vérifiées. Les connecteurs utilisent des comptes en lecture seule. Les réparations sont coupées par défaut et, activées, ne passent que des gestes nommés que vous autorisez. Voir [Sécurité](/docs/securite/).

### Peut-on éviter d'envoyer des données sensibles ?

Oui. La règle de masquage `secrets` retire d'office jetons, mots de passe et clés des journaux, avant leur écriture. D'autres règles (cartes, IBAN, e-mails, téléphones, adresses IP, numéros de sécurité sociale) s'activent d'un clic. Les filtres d'exclusion écartent des lignes entières, et l'agent peut ne jamais envoyer certaines lignes.

### Faut-il donner un accès administrateur à nos clouds ?

Non. Chaque connecteur demande le plus petit droit de lecture : rôle Lecteur chez Azure, `compute.viewer` chez Google, quelques actions `Describe` chez AWS, des `GET` chez OVHcloud.

## L'IA et la réparation

### Comment fonctionne l'explication des pannes ?

Sur une alerte, un hôte, une ligne de journal ou une trace, HYPHA rassemble journaux, mesures, contrôles, événements et processus, puis le modèle de S33D décrit ce qui se passe, la cause probable avec ses preuves, le correctif et comment éviter la récidive. Comptez de 5 à 30 secondes.

### L'IA agit-elle seule sur mes serveurs ?

Non. Elle propose un geste nommé (par exemple, redémarrer un service). Un humain le valide d'un clic, et l'agent ne l'exécute que si la configuration de l'hôte le permet.

## Installation

### Comment installer l'agent ?

Une commande sous Linux ou Windows, donnée par **Réglages › Installer l'agent**. Il existe aussi des paquets `.deb` et `.rpm` et une image Docker. Voir [Démarrer](/docs/demarrage/).

### L'agent ralentit-il mes serveurs ?

Il vise moins d'un pour cent d'un cœur et moins de 60 Mo de mémoire, tourne en priorité basse, et systemd le plafonne en cas d'emballement.

### Peut-on héberger HYPHA soi-même ?

Oui, avec Docker Compose : c'est la formule Libre, gratuite et sans limite. Voir [Auto-hébergement](/docs/auto-hebergement/).

## Compte

### Comment arrêter ?

Revenez à Découverte depuis **Réglages › Formule et facturation** : l'abonnement s'arrête à la fin de la période. Pour supprimer votre compte et vos données, écrivez à [contact@s33d.fr](mailto:contact@s33d.fr).

### Qui contacter ?

S33D, à [contact@s33d.fr](mailto:contact@s33d.fr).
