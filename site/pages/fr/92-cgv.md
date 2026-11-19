---
id: terms
kind: page
path: /cgv/
title: Conditions générales de vente du service HYPHA
updated: 2026-11-19
description: Conditions générales de vente du service hébergé HYPHA de S33D : formules Découverte, Pro et Entreprise, prix, facturation à l'usage, paiement, résiliation, données, responsabilité.
lead: Ces conditions régissent l'usage du service HYPHA hébergé par S33D sur hypha.s33d.fr. Version du 19 novembre 2026.
---
## 1. Objet

Les présentes conditions générales de vente (« CGV ») régissent la fourniture du service de supervision **HYPHA** hébergé par S33D et accessible sur hypha.s33d.fr (« le Service »), dans ses formules **Découverte**, **Pro** et **Entreprise**. Créer un espace ou souscrire une formule vaut acceptation des CGV en vigueur.

La formule **Libre** (le logiciel installé par le client sur ses propres serveurs) n'est pas un service fourni par S33D : elle relève des licences du logiciel, AGPL v3 pour le serveur et l'interface, Apache 2.0 pour l'agent, et n'est pas régie par les présentes CGV.

## 2. Prestataire

Le Service est fourni par **S33D**, SARL au capital de 1 000 €, immatriculée au RCS de Douai sous le numéro 131 908 329, TVA intracommunautaire FR16131908329, dont le siège est situé 6 Résidence Beauséjour, 59310 Faumont, France. Contact : [contact@s33d.fr](mailto:contact@s33d.fr). Voir les [mentions légales](/mentions-legales/).

## 3. Clients

Le Service s'adresse aux professionnels, associations, établissements d'enseignement et de recherche, collectivités et particuliers. Les dispositions propres aux consommateurs (articles 10 et 16) ne s'appliquent qu'au client qui agit en dehors de toute activité professionnelle.

## 4. Compte et espace

L'accès se fait par un compte S33D. La première connexion crée un **espace** en formule Découverte, dont l'utilisateur est le **propriétaire**. Seul le propriétaire choisit la formule et gère la facturation ; il répond des membres qu'il invite. Le client garde ses identifiants et ses clés confidentiels et révoque toute clé compromise.

## 5. Description du Service

Le Service collecte, conserve et présente les données de supervision que le client envoie par ses agents, ses connecteurs et ses applications : mesures, journaux, traces, contrôles, événements, inventaire. Il évalue des moniteurs, envoie des notifications, rédige des explications et des post-mortem par le modèle d'intelligence artificielle de S33D, et exécute, après validation par le client, des actions à distance que la configuration de ses agents autorise. Le contenu de chaque formule est décrit sur la page [Tarifs](/tarifs/) en vigueur à la date de souscription.

Les fonctions annoncées « en préparation » ou « à venir » sur la page Tarifs (connexion SSO SAML, notifications par SMS) ne sont dues qu'à partir de leur mise à disposition.

## 6. Prix

Les prix sont indiqués en euros **hors taxes**. La TVA française au taux en vigueur (20 %) s'y ajoute. Le client professionnel établi dans un autre État membre de l'Union européenne, dont le numéro de TVA est validé, est facturé hors taxes en autoliquidation.

- **Découverte** : gratuite, sans moyen de paiement. Elle est limitée (nombre d'hôtes et d'équipements, volume quotidien de journaux et de traces, tests synthétiques, explications par l'IA, pages de statut, durées de conservation) selon la page Tarifs. Ce qui dépasse une limite est refusé et signalé, jamais facturé.
- **Pro** : 2 € HT par hôte et par mois, sans engagement ni minimum ; 1,50 € HT par hôte et par mois en annuel (18 € HT par hôte engagé et par an). Les équipements réseau et ressources cloud, le volume de journaux et de traces au-delà de l'inclus, la conservation longue, les exécutions synthétiques au-delà des tests inclus et les SMS sont facturés selon la page Tarifs.
- **Entreprise** : le prix de Pro, plus un forfait de 250 € HT par mois.
- **Remises** : associations, établissements d'enseignement et de recherche : −50 % sur les hôtes de Pro, sur justificatif, remise posée par S33D. Collectivités : prix Pro.

## 7. Calcul de la consommation

- Un hôte, un équipement ou une ressource cloud est compté à l'heure pendant laquelle il envoie des données, sur la période de facturation, et non au pic.
- Le volume de journaux et de traces est celui reçu et enregistré par le Service, en gigaoctets décimaux. Les volumes inclus (10 Go de journaux et 5 Go de traces par hôte et par mois) sont mis en commun sur l'espace.
- La consommation de la période est visible à tout moment dans **Réglages › Formule et facturation**, avec une estimation de la facture. Les relevés du Service font foi.

## 8. Commande et paiement

La souscription se fait depuis **Réglages › Formule et facturation**. Le paiement est traité par le prestataire **Stripe**, par carte bancaire ou par les moyens que Stripe propose sur la page de paiement ; S33D n'a jamais accès aux coordonnées bancaires du client. La formule Entreprise peut être réglée sur bon de commande ou par mandat administratif.

- **Mensuel** : la période va du 1er au dernier jour du mois (UTC). La consommation de la période est facturée à son terme et prélevée sur le moyen de paiement enregistré.
- **Annuel** : le nombre d'hôtes engagé est payé d'avance pour douze mois. Les hôtes au-delà de l'engagement sont facturés chaque mois au tarif mensuel.
- Les factures sont émises par S33D et disponibles dans le portail de paiement (**Paiement et factures**).

En cas d'échec de paiement, Stripe présente de nouveau le paiement. Si toutes les tentatives échouent, l'espace est ramené aux limites de Découverte jusqu'au paiement ; les sommes dues restent exigibles. Tout retard de paiement d'un client professionnel entraîne des pénalités au taux d'intérêt légal multiplié par trois et l'indemnité forfaitaire de 40 € pour frais de recouvrement (article L.441-10 du Code de commerce).

## 9. Durée, changement de formule et résiliation

- La formule mensuelle est **sans engagement** et se reconduit chaque mois.
- La formule annuelle engage le nombre d'hôtes choisi pour douze mois.
- Le passage de Pro à Entreprise prend effet immédiatement, le forfait étant facturé au prorata du mois en cours ; le retour d'Entreprise à Pro donne lieu à un avoir au prorata.
- Le client résilie à tout moment par **Revenir à Découverte**. L'abonnement s'arrête à la fin de la période en cours, ou à la fin de l'engagement annuel. La consommation de la période reste due ; il n'y a pas de remboursement d'une période payée d'avance, sauf article 10.
- À la résiliation, l'espace continue en Découverte, avec ses limites et ses durées de conservation.

## 10. Droit de rétractation du consommateur

Le client consommateur dispose d'un délai de quatorze jours à compter de la souscription d'une formule payante pour se rétracter, sans motif, par écrit à [contact@s33d.fr](mailto:contact@s33d.fr) (articles L.221-18 et suivants du Code de la consommation). Le Service commençant dès la souscription à la demande du client, celui-ci paie, s'il se rétracte, le montant correspondant au Service fourni jusqu'à la communication de sa décision (article L.221-25). Les sommes payées d'avance au-delà de ce montant lui sont remboursées sous quatorze jours.

## 11. Obligations de S33D

S33D met en œuvre les moyens raisonnables pour assurer la disponibilité, la sécurité et la confidentialité du Service, qu'elle héberge sur son infrastructure en propre en France. Le Service peut être interrompu pour maintenance, si possible en dehors des heures ouvrées. Sauf stipulation contraire de la formule Entreprise (support en français avec délais garantis, dans les conditions convenues avec le client), S33D est tenue d'une obligation de moyens.

Les explications, post-mortem et actions proposés par l'intelligence artificielle sont des aides à la décision : ils peuvent être incomplets ou inexacts et doivent être vérifiés. Aucune action n'est exécutée sans la validation d'un utilisateur et l'autorisation de la configuration de l'agent concerné.

## 12. Obligations du client

Le client :

- n'installe les agents et ne branche les connecteurs que sur les systèmes et comptes qu'il est en droit de superviser ;
- choisit les données qu'il envoie, et dispose des droits nécessaires à leur traitement ; il utilise les outils de filtrage et de masquage pour écarter celles qu'il ne souhaite pas confier au Service ;
- n'utilise pas le Service pour sonder ou attaquer des systèmes tiers, ni pour un usage contraire à la loi ;
- respecte un usage raisonnable des fonctions annoncées sans limite ; un usage manifestement anormal (par exemple automatisé à des fins sans rapport avec la supervision) peut être limité après information du client ;
- tient exactes ses informations de facturation.

S33D peut suspendre un espace dont l'usage met en péril le Service ou des tiers, après information du propriétaire sauf urgence.

## 13. Données du client

Le client reste propriétaire de ses données. S33D ne les utilise que pour fournir le Service et ne les vend ni ne les exploite à d'autres fins. Pour les données à caractère personnel que contiennent les données de supervision, S33D agit en **sous-traitant** du client, au sens de l'article 28 du RGPD :

- elle ne traite ces données que sur instruction du client, matérialisée par sa configuration et son usage du Service ;
- les personnes qui y ont accès sont tenues à la confidentialité ;
- elle met en œuvre les mesures de sécurité décrites dans la [documentation](/docs/securite/) ;
- elle ne fait appel à un autre sous-traitant qu'aux conditions de la [politique de confidentialité](/confidentialite/), qui en tient la liste ;
- elle aide le client à répondre aux demandes des personnes concernées et à ses obligations de sécurité ;
- elle notifie au client toute violation de données le concernant dans les meilleurs délais après en avoir eu connaissance ;
- au terme du Service, les données sont effacées à l'échéance des durées de conservation ; le client peut en demander l'effacement anticipé ;
- elle met à disposition les informations nécessaires pour démontrer le respect de ces obligations.

La formule Entreprise comprend un contrat de sous-traitance signé (DPA) qui précise ces engagements. Le traitement des données personnelles par S33D en tant que responsable est décrit dans la [politique de confidentialité](/confidentialite/).

## 14. Responsabilité

S33D n'est pas responsable des dommages indirects (perte de chiffre d'affaires, de clientèle, de données non sauvegardées par ailleurs), ni des conséquences d'une alerte non émise ou d'une action validée par le client. Envers un client professionnel, la responsabilité totale de S33D au titre du Service est limitée aux sommes payées par le client pour le Service au cours des douze mois précédant le fait générateur. Ces limites ne s'appliquent pas en cas de faute lourde ou intentionnelle, ni aux dommages corporels, ni envers le consommateur au-delà de ce que permet la loi.

## 15. Propriété intellectuelle

Le logiciel HYPHA est distribué sous licence AGPL v3 (serveur et interface) et Apache 2.0 (agent). Les marques HYPHA et S33D, les textes et visuels du site restent la propriété de S33D.

## 16. Réclamations et médiation

Toute réclamation s'adresse d'abord à [contact@s33d.fr](mailto:contact@s33d.fr). Conformément à l'article L.612-1 du Code de la consommation, le client consommateur peut ensuite saisir gratuitement le médiateur de la consommation désigné par S33D, dont les coordonnées figurent dans les conditions générales de vente de [www.s33d.fr](https://www.s33d.fr/cgv), dans un délai d'un an à compter de sa réclamation écrite.

## 17. Modification des conditions et des prix

S33D peut modifier les CGV et les prix. Toute modification défavorable est annoncée par e-mail au propriétaire de l'espace au moins trente jours avant son entrée en vigueur ; le client peut résilier avant cette date sans autre frais que la consommation déjà faite. Les engagements annuels en cours gardent leur prix jusqu'à leur terme.

## 18. Force majeure

Aucune des parties n'est responsable d'un manquement causé par un événement de force majeure au sens de l'article 1218 du Code civil.

## 19. Droit applicable et juridiction

Les CGV sont soumises au droit français. À défaut d'accord amiable, et le cas échéant après recours au médiateur, tout litige avec un client professionnel relève de la compétence exclusive du **Tribunal de commerce de Douai**. Le client consommateur peut saisir la juridiction de son domicile ou celle prévue par la loi.
