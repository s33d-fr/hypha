---
id: mobile
path: /en/docs/mobile-app/
title: HYPHA on your phone: installable app and push notifications
nav: Mobile app
section: Alert and act
order: 47
updated: 2026-11-20
description: Install HYPHA as an app on Android, on iPhone (iOS 16.4 or later) or on a computer, enable push notifications, receive an alert, read the cause and acknowledge from the notification.
lead: An on-call app without an app store. HYPHA installs from the browser, rings on your phone, opens the problem with the explanation of its cause and is acknowledged in one tap.
---
## Install HYPHA

The HYPHA interface installs as an app: an icon on the home screen, its own window, opening straight on **Problems**, shortcuts to Problems and Incidents (long press on the icon, on Android and on computers). Nothing goes through an app store.

### Android

1. Open https://hypha.s33d.fr in Chrome (or Edge, Samsung Internet) and sign in.
2. Menu **⋮** › **Install app** (depending on the version: **Add to Home screen**). The **Install HYPHA** button in **Settings › Profile** does the same when the browser offers it.
3. Open HYPHA from its icon.

### iPhone and iPad (iOS 16.4 or later)

On iPhone and iPad, notifications only work in the app added to the home screen, not in a Safari tab.

1. Open https://hypha.s33d.fr in **Safari**.
2. **Share** button › **Add to Home Screen** › **Add**.
3. Open HYPHA from the new icon, sign in, then enable notifications (below).

### Computer

In Chrome or Edge, the install icon appears at the end of the address bar (or menu › **Install HYPHA**). Firefox and Safari on desktop also receive push notifications in a tab, without installing.

## Enable notifications on a device

In **Settings › Profile**, **Push notifications on this device** block:

1. **Enable notifications on this device**: the browser asks for permission; accept.
2. **Send a test**: a "HYPHA · test notification" arrives within seconds.

**My devices** lists the devices subscribed to your account (last successful send, last error); **Remove** unsubscribes one. Ten devices at most per person. A device belongs to a person, not to a workspace: it receives the channels of every workspace that names that person. **Signing out** of HYPHA unsubscribes the device, so that a shared device no longer receives your alerts.

If notifications were refused once, the browser does not ask again: allow them in the site settings (padlock in the address bar, or the phone's Settings › Notifications), then come back to your profile.

## The "Push notifications" channel

An administrator creates the channel in **Settings › Notification channels** › **New channel**, type **Push notifications**:

- **All members of the workspace**, or **Selected members**;
- each recipient receives on the devices they enabled; a member removed from the workspace receives nothing more;
- attach the channel to monitors (or escalations) like any other channel; **Send a test** tells you how many devices the test went to.

## The notification

- **Title**: state, priority and monitor, for example "Alert · P1 · Disk almost full"; a reminder, an escalation or a flapping monitor say so first.
- **Text**: the host and the monitor's message (400 characters at most).
- **Tapping the notification** opens HYPHA on **Problems**, in the problem's workspace, with the **explanation of the cause** by S33D's AI (when it is on for the workspace).
- **Acknowledge** (Android and computers) acknowledges the problem without opening the app, with your browser's session: no token is put in the notification. You need at least the **member** role in the workspace. If your session expired, the notification says so: open HYPHA, sign in again, then acknowledge. iPhone and iPad do not show buttons on these notifications: tap it, then acknowledge in **Problems**.
- The return to OK replaces the alert notification; a reminder rings the device again.

## Privacy and limits

- The message is encrypted for the device (RFC 8291) before it leaves: the browser's push service (Google for Chrome and Android, Apple, Mozilla, Microsoft) carries it without being able to read it.
- The push service keeps a message for 24 hours while the device is offline. If an alert and its recovery happen during that time, only the latest state is delivered.
- On Android, reliable on-call needs battery saving not to hold the browser back (Settings › Apps › Chrome › Battery › Unrestricted).
- Without network, the app shows an "HYPHA is offline" page; it keeps no data on the device, only the interface files, renewed at each HYPHA update.

## Self-hosted

Push notifications need a VAPID key specific to your server:

```bash
docker compose exec api python -m hypha vapid-key
```

Add the printed line (`HYPHA_VAPID_PRIVATE_KEY=…`) to the `.env` file, then `docker compose up -d`. Changing this key forces every user to enable notifications again on their devices. `HYPHA_VAPID_SUBJECT` (optional) gives the operator's contact to push services (`mailto:` or `https:`); by default, HYPHA's public address.

The interface must be served over HTTPS with a certificate the devices trust (a self-signed certificate prevents the service worker), and the server must reach the browsers' push services over HTTPS (`fcm.googleapis.com`, `web.push.apple.com`, `updates.push.services.mozilla.com`, `*.notify.windows.com`).
