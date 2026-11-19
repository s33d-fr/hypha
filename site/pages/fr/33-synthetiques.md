---
id: synthetics
path: /docs/synthetiques/
title: Tests synthétiques
nav: Tests synthétiques
section: Observer
order: 33
updated: 2026-11-19
description: Tests synthétiques HYPHA : HTTP, TCP, TLS, DNS, ICMP, parcours en plusieurs étapes, SMTP, IMAP, aller-retour de messagerie et DNS de messagerie, depuis S33D ou depuis vos agents.
lead: HYPHA joue vos sites, vos API et votre messagerie à intervalle régulier, depuis S33D ou depuis l'intérieur de votre réseau.
---
## Types de tests

| Type | Ce qu'il vérifie |
|---|---|
| `http` | statut attendu, texte attendu, durée, redirections, échéance du certificat |
| `multistep` | un parcours de plusieurs requêtes, avec assertions et variables extraites d'une étape à l'autre |
| `tcp` | ouverture d'un port |
| `ssl` | certificat présenté (chaîne, nom, échéance), y compris derrière STARTTLS (SMTP, IMAP, POP3) |
| `dns` | réponse d'un enregistrement (type, serveur interrogé, valeur attendue) |
| `icmp` | réponse au ping |
| `smtp`, `imap` | bannière, capacités, STARTTLS ou TLS selon le port, certificat |
| `mail_roundtrip` | envoi authentifié, puis relève IMAP du message ; ou passage par un écho extérieur dont le rapport SPF et DKIM est relevé |
| `mail_dns` | MX, SPF (unique, qualificatif final, 10 recherches au plus), DMARC, DKIM des sélecteurs donnés, listes noires |

Intervalle : de 30 secondes à 24 heures ; 5 minutes au moins pour `mail_roundtrip` et `mail_dns`.

## Assertions

Les tests `http`, `ssl` et chaque étape d'un `multistep` acceptent des assertions :

| Assertion | Exemple |
|---|---|
| `status` | égal à 200 |
| `header` | `content-type` contient `json` |
| `body_contains` | le corps contient `Commande validée` |
| `json_path` | `$.stock` supérieur à 0 |
| `latency_ms` | inférieur à 800 |

Opérateurs : `eq`, `ne`, `lt`, `gt`, `contains`, `matches`.

### Parcours en plusieurs étapes

Chaque étape peut extraire une valeur (`json_path`, en-tête ou expression régulière) et la réutiliser plus loin sous la forme `{{nom}}` dans l'adresse, les en-têtes ou le corps. Exemple : obtenir un jeton, puis appeler l'API avec ce jeton. Le résultat détaille la durée, le statut et les assertions de chaque étape.

## Emplacements

- **S33D** : le test part des serveurs de S33D, vers des adresses publiques seulement.
- **Vos agents** (emplacement privé) : le test part du réseau d'un hôte, adresses internes comprises. Activez `synthetics.enabled: true` sur l'agent ; il reçoit ses tests par le canal de contrôle et renvoie les résultats. `max_concurrent` borne les tests simultanés (4 par défaut).

Un test peut partir de plusieurs emplacements ; la page du test montre les résultats par emplacement, et le moniteur peut n'alerter qu'à partir d'un nombre d'emplacements en échec. Les tests de messagerie `mail_roundtrip` et `mail_dns` partent de S33D seulement.

## Messagerie

Pour une messagerie surveillée de bout en bout :

1. `smtp` sur chaque MX (port 25) et sur la soumission (587) ;
2. `imap` sur 993 ;
3. `ssl` avec STARTTLS pour l'échéance des certificats ;
4. `mail_roundtrip` : une boîte dédiée s'envoie un message et le relève ; chaque passage n'efface que son propre message ;
5. `mail_dns` sur chacun de vos domaines : SPF, DMARC, DKIM et listes noires.

Le mot de passe d'une boîte de test est rendu masqué par l'API et n'est jamais affiché.

## Alerter

Le modèle **Synthétique en échec** alerte après N échecs consécutifs (2 par défaut). Les certificats relevés par les tests figurent aussi dans la page **Certificats**.

## Ce que les formules comptent

Les tests lancés depuis S33D sont comptés par formule ; ceux qui partent de vos agents ne sont jamais limités. Voir les [tarifs](/tarifs/).
