---
id: billing
path: /docs/formules-et-facturation/
title: Formules, limites et facturation
nav: Formules et facturation
section: Administrer
order: 51
updated: 2026-11-19
description: Choisir et changer de formule HYPHA, comprendre les limites de Découverte, la facturation à l'usage de Pro, l'engagement annuel, la TVA, les factures et le retour à Découverte.
lead: La page Réglages › Formule et facturation montre votre formule, vos limites, votre consommation et l'estimation de la période. Les prix sont sur la page Tarifs.
---
{{video:7-formule-et-consommation}}

## Les formules en bref

| Formule | Pour qui | Prix HT |
|---|---|---|
| Libre | auto-hébergement, sans limite | 0 € |
| Découverte | service hébergé, pour essayer ou pour un petit parc | 0 €, sans carte bancaire |
| Pro | service hébergé, à l'usage | 2 € par hôte et par mois, 1,50 € en annuel |
| Entreprise | Pro avec support garanti et contrat RGPD | Pro + 250 € par mois |

Le détail de chaque formule est sur la page [Tarifs](/tarifs/), les règles contractuelles dans les [conditions de vente](/cgv/).

## La page Formule et facturation

**Réglages › Formule et facturation** montre :

- votre formule, l'état de l'abonnement, la période en cours, la TVA appliquée, la conservation choisie ;
- **Limites et consommation** : hôtes et équipements actifs dans la dernière heure, journaux et traces reçus aujourd'hui, tests lancés depuis S33D, pages de statut, explications du mois ;
- les **hôtes refusés** par une limite depuis 24 heures ;
- l'**estimation de la période** en cours, calculée comme la facture, et pour Découverte, ce que coûterait Pro ce mois-ci avec votre consommation.

Seul le **propriétaire** de l'espace choisit la formule et gère la facturation.

## Quand une limite de Découverte est atteinte

HYPHA ne fait jamais payer en silence : une limite atteinte refuse ce qui dépasse, le signale et ne facture rien.

| Limite | Ce qui se passe |
|---|---|
| 5 hôtes actifs | les hôtes déjà présents gardent leur place ; un hôte en plus est refusé et ses données ne sont pas enregistrées. Un hôte silencieux depuis une heure libère sa place. |
| 10 équipements ou ressources cloud | même règle |
| 500 Mo de journaux et traces par jour | les envois sont refusés jusqu'à minuit UTC ; les lignes refusées sont comptées |
| 10 tests lancés depuis S33D, toutes les 5 minutes au plus | la création d'un test de plus est refusée ; un test lancé par vos agents n'est pas limité |
| 30 explications par l'IA par mois | de nouveau disponibles le 1er du mois |
| 1 page de statut | la création d'une page de plus est refusée |

Un bandeau dans l'interface et le message renvoyé à l'agent disent quelle limite est atteinte. L'agent abandonne alors l'envoi refusé sans le stocker, pour ne pas saturer son tampon.

Conservation en Découverte : journaux et traces visibles 3 jours, mesures 1 jour en pleine résolution et 30 jours au pas de 5 minutes.

## Passer à Pro

1. **Réglages › Formule et facturation › Choisir Pro**.
2. Choisissez la facturation mensuelle (sans engagement) ou annuelle (un nombre d'hôtes engagés, payé d'avance).
3. Indiquez le courriel de facturation, le pays, et pour un professionnel la raison sociale et le numéro de TVA intracommunautaire.
4. Le paiement se fait sur la page sécurisée de Stripe. La formule s'active dès que Stripe confirme le paiement.

> La souscription en ligne des formules payantes ouvre prochainement. Découverte et Libre sont disponibles dès maintenant.

## Comment Pro est facturé

- **Hôtes à l'usage réel**, comptés à l'heure, pas au pic : un hôte présent la moitié du mois compte pour un demi-hôte-mois.
- **Équipements réseau et ressources cloud** : 0,50 € HT pièce et par mois, à l'heure de même.
- **Journaux et traces** : 10 Go de journaux et 5 Go de traces inclus par hôte et par mois, mis en commun sur l'espace ; au-delà, 0,10 € HT par Go reçu.
- **Conservation longue** (option) : 30 jours (+0,05 € HT par Go reçu) ou 90 jours (+0,15 € HT par Go reçu), pour les journaux et traces reçus à partir du choix.
- **Tests lancés depuis S33D** : 50 tests inclus ; au-delà, 1 € HT les 10 000 exécutions.
- La période mensuelle va du 1er au dernier jour du mois. La consommation est transmise à Stripe chaque heure ; la facture du mois reprend le cumul.
- **Annuel** : les hôtes engagés sont payés d'avance (18 € HT par hôte et par an) ; les heures au-delà de l'engagement suivent le tarif mensuel.

## TVA et factures

- Prix hors taxes. TVA française de 20 %.
- Professionnel d'un autre pays de l'Union européenne dont le numéro de TVA est valide (vérifié par Stripe auprès du VIES) : autoliquidation, mention en pied de facture.
- Factures et moyen de paiement : bouton **Paiement et factures**, qui ouvre le portail de Stripe.
- HYPHA ne voit jamais vos coordonnées bancaires.

## Remises

- Associations, enseignement, recherche : −50 % sur les hôtes de Pro, sur demande auprès de S33D avec un justificatif ; S33D pose la remise sur votre espace.
- Collectivités : prix Pro, paiement par mandat administratif possible.

## Changer ou arrêter

- **Passer en Entreprise** : le forfait de 250 € HT par mois s'ajoute au prorata du mois en cours. **Revenir à Pro** le retire avec un avoir au prorata.
- **Revenir à Découverte** : l'abonnement s'arrête à la fin de la période en cours (ou de l'engagement annuel). La consommation de la période reste facturée, puis les limites de Découverte s'appliquent.
