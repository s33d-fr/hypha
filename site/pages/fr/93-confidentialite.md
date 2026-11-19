---
id: privacy
kind: page
path: /confidentialite/
title: Politique de confidentialité de HYPHA
updated: 2026-11-19
description: Comment S33D traite les données personnelles dans le service HYPHA : données de compte et de facturation, données de supervision des clients, durées de conservation, sous-traitants, hébergement en France, droits des personnes.
lead: HYPHA est hébergé par S33D sur son infrastructure en propre, en France. Cette page dit quelles données nous traitons, pourquoi, combien de temps, avec qui, et comment exercer vos droits. Version du 19 novembre 2026.
---
## 1. Qui traite vos données

**S33D**, SARL au capital de 1 000 €, RCS Douai 131 908 329, 6 Résidence Beauséjour, 59310 Faumont, France. Contact pour toute question sur vos données : [contact@s33d.fr](mailto:contact@s33d.fr).

S33D intervient à deux titres :

| Données | Rôle de S33D |
|---|---|
| Comptes, membres, facturation, support, sécurité et mesure du site et de l'interface | **responsable de traitement** |
| Données de supervision envoyées par les clients (mesures, journaux, traces, inventaire, événements) | **sous-traitant** du client, qui en est responsable |

En auto-hébergement (formule Libre), S33D ne reçoit aucune donnée : tout reste sur le serveur du client.

## 2. Les données dont S33D est responsable

| Données | Finalité | Base légale | Conservation |
|---|---|---|---|
| Compte : identifiant, adresse e-mail, nom, date de dernière connexion | créer et sécuriser l'accès, rattacher aux espaces | exécution du contrat | tant que le compte existe ; effacement sur demande |
| Espaces : appartenance, rôle, invitations | gérer les droits dans un espace | exécution du contrat | tant que l'espace existe |
| Session : cookie `hypha_session`, adresse IP et navigateur de la session | maintenir la connexion | exécution du contrat | 7 jours |
| Journal d'audit : auteur, action, objet, date, adresse IP | sécurité, preuve des modifications | intérêt légitime | 13 mois |
| Facturation : raison sociale, e-mail de facturation, pays, numéro de TVA, abonnement, consommation, factures | facturer les formules payantes | exécution du contrat, obligation légale | 10 ans pour les pièces comptables |
| Échanges avec le support | répondre aux demandes | exécution du contrat, intérêt légitime | 3 ans après le dernier échange |
| Journaux techniques des serveurs : adresse IP, date, page demandée, navigateur | sécurité et bon fonctionnement du service | intérêt légitime, obligation légale | 1 an au plus |
| Mesure de l'interface : pages vues, temps de chargement, erreurs JavaScript, sans paramètres d'adresse | corriger les lenteurs et les erreurs de l'interface | intérêt légitime | 30 jours |

Le compte S33D sert à se connecter à plusieurs services de S33D. Si vous choisissez de vous connecter avec Google, Microsoft, Apple, Facebook ou GitHub, S33D ne reçoit de ce fournisseur que votre identifiant, votre adresse e-mail vérifiée et votre nom ; elle n'a jamais accès à votre mot de passe.

S33D ne vend aucune donnée et n'en fait aucun usage publicitaire.

## 3. Les données de supervision

Les données que le client envoie au Service peuvent contenir des données personnelles : noms d'utilisateurs ou adresses IP dans les journaux, adresses e-mail, noms de machines, attributs de traces, adresses des pages vues par les visiteurs d'un site suivi par le RUM. Pour ces données :

- le **client est responsable de traitement** : il choisit ce qu'il envoie, informe les personnes concernées et répond à leurs demandes ;
- **S33D est sous-traitant** : elle ne traite ces données que pour fournir le Service, selon la configuration du client, dans les conditions de l'article 13 des [conditions de vente](/cgv/) ; la formule Entreprise comprend un contrat de sous-traitance signé ;
- les explications et post-mortem sont rédigés par le modèle d'IA de S33D, sur ses propres serveurs en France. Les données envoyées au modèle ne partent chez aucun fournisseur d'IA et ne servent pas à entraîner un modèle ;
- le client dispose d'outils pour limiter ce qu'il envoie : masquage à l'arrivée (règle `secrets` active d'office ; cartes, IBAN, e-mails, téléphones, adresses IP publiques, numéros de sécurité sociale au choix), filtres d'exclusion, lignes écartées par l'agent avant tout envoi.

### Durées de conservation

La durée de consultation dépend de la formule ; les données sont ensuite effacées à l'échéance de la purge régulière de la base, indiquée dans la dernière colonne.

| Données | Découverte | Pro et Entreprise | Effacement au plus tard |
|---|---|---|---|
| Journaux et traces | 3 jours | 15 jours ; 30 ou 90 jours avec l'option de conservation longue | 16 jours après réception ; avec l'option, 31 ou 91 jours |
| Mesures en pleine résolution | 1 jour | 15 jours | 16 jours |
| Mesures au pas de 5 minutes | 30 jours | 92 jours | 93 jours |
| Mesures au pas d'une heure | non affichées | 15 mois | 16 mois |
| Requêtes de bases de données (DBM) | 15 jours | 15 jours | 16 jours |
| Événements RUM des sites suivis | 30 jours | 30 jours | 31 jours |
| Processus, connexions réseau, conteneurs | 3 jours | 3 jours | 4 jours |
| Résultats des contrôles | 30 jours | 30 jours | 31 jours |
| Résultats des tests synthétiques | 92 jours | 92 jours | 93 jours |
| Événements et changements d'état des alertes | 13 mois | 13 mois | 14 mois |
| Inventaire matériel et logiciel | dernier relevé de chaque hôte | dernier relevé de chaque hôte | à la suppression de l'hôte ou de l'espace |

Les réglages de l'espace (moniteurs, tableaux, canaux, incidents) sont gardés tant que l'espace existe. À la demande du propriétaire, l'espace et ses données sont effacés sans attendre ces échéances, sous réserve des pièces de facturation.

## 4. Destinataires et sous-traitants

Les données sont accessibles aux seules personnes de S33D qui en ont besoin pour le Service, et aux membres de l'espace selon leur rôle. S33D fait appel aux sous-traitants suivants :

| Sous-traitant | Rôle | Données | Lieu |
|---|---|---|---|
| Stripe Payments Europe, Ltd. | paiement et facturation des formules payantes | identité de facturation, e-mail, pays, numéro de TVA, moyen de paiement (saisi chez Stripe), montants | Irlande ; transferts possibles vers les États-Unis, encadrés par les clauses contractuelles types de la Commission européenne |
| OVH SAS | hébergement des deux serveurs relais de messagerie de S33D | e-mails de notification et de rapport (adresse du destinataire, contenu de l'alerte) | France |

Aucun autre sous-traitant ne reçoit de données du Service : ni outil de mesure d'audience, ni régie publicitaire, ni fournisseur d'IA. Les notifications par SMS, annoncées à venir, feront l'objet d'une mise à jour de cette liste avant leur ouverture.

Les canaux de notification que le client configure (Slack, Microsoft Teams, Telegram, Discord, Google Chat, Mattermost, ntfy, PagerDuty, Opsgenie, webhooks) reçoivent les alertes sur instruction du client ; ce sont ses propres prestataires. De même, les connecteurs cloud lisent les comptes que le client désigne, depuis ses propres agents.

S33D peut communiquer des données aux autorités qui les demandent dans un cadre légal.

## 5. Hébergement et transferts

Le Service et ses données sont hébergés sur l'infrastructure en propre de S33D, en France. Les seuls transferts possibles hors de l'Union européenne sont ceux de Stripe, décrits ci-dessus, et ceux que le client déclenche en choisissant un canal de notification situé hors de l'Union.

## 6. Sécurité

S33D chiffre les échanges (HTTPS), ne garde que l'empreinte des clés d'API, trace chaque modification dans le journal d'audit, cloisonne les données par espace et applique les mesures décrites sur la page [Sécurité](/docs/securite/). Aucune mesure ne supprime tout risque : S33D informe le client de toute violation de données le concernant dans les meilleurs délais.

## 7. Cookies et stockage dans le navigateur

| Nom | Type | Rôle | Durée |
|---|---|---|---|
| `hypha_session` | cookie, nécessaire | connexion à l'interface | 7 jours |
| `hypha_oidc` | cookie, nécessaire | protéger la connexion par le compte S33D | 10 minutes |
| `hypha.lang`, `hypha.theme`, `hypha.nav`, `hypha.refresh` | stockage local | langue, thème, menu et rafraîchissement choisis | jusqu'à effacement par vous |
| `hypha_rum_sid` | stockage de session | mesure des performances de l'interface | 30 minutes d'inactivité, et la fermeture de l'onglet |

Aucun cookie publicitaire ni de réseau social. La page de paiement de Stripe pose ses propres cookies, régis par la politique de Stripe.

Le script RUM que le client place sur ses propres sites ne pose aucun cookie ; il garde un identifiant de session dans l'onglet du visiteur (`sessionStorage`) et retire par défaut les paramètres des adresses. Le client informe ses visiteurs de cette mesure.

## 8. Vos droits

Vous disposez d'un droit d'accès, de rectification, d'effacement, de limitation, de portabilité et d'opposition, et du droit de définir des directives sur le sort de vos données après votre décès. Écrivez à [contact@s33d.fr](mailto:contact@s33d.fr) ; nous répondons sous un mois. Une pièce d'identité peut être demandée en cas de doute sur votre identité.

Pour des données de supervision, adressez-vous d'abord au client qui utilise HYPHA : il en est responsable. S33D lui transmet toute demande reçue.

Vous pouvez introduire une réclamation auprès de la CNIL (3 place de Fontenoy, TSA 80715, 75334 Paris Cedex 07, [cnil.fr](https://www.cnil.fr)).

## 9. Modifications

Cette politique peut évoluer, notamment avec de nouvelles fonctions. La date de version figure en tête de page ; les changements importants sont annoncés par e-mail aux propriétaires d'espace.
