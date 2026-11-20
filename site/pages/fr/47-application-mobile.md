---
id: mobile
path: /docs/application-mobile/
title: HYPHA sur le téléphone : application installable et notifications push
nav: Application mobile
section: Alerter et agir
order: 47
updated: 2026-11-20
description: Installer HYPHA comme une application sur Android, sur iPhone (iOS 16.4 et plus) ou sur l'ordinateur, activer les notifications push, recevoir une alerte, lire la cause et acquitter depuis la notification.
lead: Une application d'astreinte sans magasin d'applications. HYPHA s'installe depuis le navigateur, sonne sur le téléphone, ouvre le problème avec l'explication de sa cause et s'acquitte d'un geste.
---
## Installer HYPHA

L'interface de HYPHA s'installe comme une application : icône sur l'écran d'accueil, fenêtre à elle, ouverture directe sur **Problèmes**, raccourcis vers Problèmes et Incidents (appui long sur l'icône, sur Android et sur ordinateur). Rien ne passe par un magasin d'applications.

### Android

1. Ouvrez https://hypha.s33d.fr dans Chrome (ou Edge, Samsung Internet) et connectez-vous.
2. Menu **⋮** › **Installer l'application** (selon la version : **Ajouter à l'écran d'accueil**). Le bouton **Installer HYPHA** de **Réglages › Profil** fait de même quand le navigateur le propose.
3. Ouvrez HYPHA depuis son icône.

### iPhone et iPad (iOS 16.4 et plus)

Sur iPhone et iPad, les notifications ne fonctionnent que dans l'application ajoutée à l'écran d'accueil, pas dans un onglet de Safari.

1. Ouvrez https://hypha.s33d.fr dans **Safari**.
2. Bouton **Partager** › **Sur l'écran d'accueil** › **Ajouter**.
3. Ouvrez HYPHA depuis la nouvelle icône, connectez-vous, puis activez les notifications (ci-dessous).

### Ordinateur

Dans Chrome ou Edge, l'icône d'installation apparaît au bout de la barre d'adresse (ou menu › **Installer HYPHA**). Firefox et Safari pour ordinateur reçoivent aussi les notifications push dans un onglet, sans installation.

## Activer les notifications sur un appareil

Dans **Réglages › Profil**, bloc **Notifications push sur cet appareil** :

1. **Activer les notifications sur cet appareil** : le navigateur demande l'autorisation ; acceptez.
2. **Envoyer un essai** : une notification « HYPHA · notification d'essai » arrive dans les secondes qui suivent.

**Mes appareils** liste les appareils abonnés à votre compte (dernier envoi réussi, dernière erreur) ; **Retirer** en désabonne un. Dix appareils au plus par personne. Un appareil appartient à une personne, pas à un espace : il reçoit les canaux de chaque espace qui la nomme. Se **déconnecter** de HYPHA désabonne l'appareil, pour qu'un appareil partagé ne reçoive plus vos alertes.

Si les notifications ont été refusées une fois, le navigateur ne redemande plus : autorisez-les dans les réglages du site (cadenas de la barre d'adresse, ou Réglages › Notifications du téléphone), puis revenez au profil.

## Le canal « Notifications push »

Un administrateur crée le canal dans **Réglages › Canaux de notification** › **Nouveau canal**, type **Notifications push** :

- **Tous les membres de l'espace**, ou **Membres choisis** ;
- chaque destinataire reçoit sur les appareils qu'il a activés ; un membre retiré de l'espace ne reçoit plus rien ;
- rattachez le canal aux moniteurs (ou aux escalades) comme les autres canaux ; **Envoyer un essai** dit sur combien d'appareils l'essai est parti.

## La notification

- **Titre** : état, priorité et moniteur, par exemple « Alerte · P1 · Disque presque plein » ; une relance, une escalade ou un moniteur instable le disent en tête.
- **Texte** : l'hôte et le message du moniteur (400 caractères au plus).
- **Toucher la notification** ouvre HYPHA sur **Problèmes**, dans l'espace du problème, avec l'**explication de la cause** par l'IA de S33D (si elle est active dans l'espace).
- **Acquitter** (Android et ordinateur) acquitte le problème sans ouvrir l'application, avec la session de votre navigateur : aucun jeton n'est mis dans la notification. Il faut le rôle **membre** (au moins) dans l'espace. Si votre session a expiré, la notification le dit : ouvrez HYPHA, reconnectez-vous, puis acquittez. iPhone et iPad n'affichent pas de boutons sur ces notifications : touchez-la, puis acquittez dans **Problèmes**.
- Le retour à OK remplace la notification de l'alerte ; une relance fait de nouveau sonner l'appareil.

## Confidentialité et limites

- Le message est chiffré pour l'appareil (RFC 8291) avant de partir : le service de push du navigateur (Google pour Chrome et Android, Apple, Mozilla, Microsoft) le transporte sans pouvoir le lire.
- Le service de push garde un message 24 heures quand l'appareil est hors réseau. Si une alerte puis son retour à OK arrivent pendant ce temps, seul le dernier état est livré.
- Sur Android, une astreinte fiable demande que les économies d'énergie ne retiennent pas le navigateur (Réglages › Applications › Chrome › Batterie › Sans restriction).
- Sans réseau, l'application montre une page « HYPHA est hors ligne » ; elle ne garde aucune donnée sur l'appareil, seulement les fichiers de l'interface, renouvelés à chaque mise à jour de HYPHA.

## Auto-hébergé

Les notifications push demandent une clé VAPID propre à votre serveur :

```bash
docker compose exec api python -m hypha vapid-key
```

Ajoutez la ligne rendue (`HYPHA_VAPID_PRIVATE_KEY=…`) au fichier `.env`, puis `docker compose up -d`. Changer cette clé oblige chaque utilisateur à réactiver les notifications sur ses appareils. `HYPHA_VAPID_SUBJECT` (facultatif) donne le contact de l'exploitant aux services de push (`mailto:` ou `https:`) ; par défaut, l'adresse publique de HYPHA.

L'interface doit être servie en HTTPS avec un certificat reconnu par les appareils (un certificat autosigné empêche le service worker), et le serveur doit joindre en HTTPS les services de push des navigateurs (`fcm.googleapis.com`, `web.push.apple.com`, `updates.push.services.mozilla.com`, `*.notify.windows.com`).
