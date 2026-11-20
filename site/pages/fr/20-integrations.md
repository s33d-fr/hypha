---
id: integrations
path: /docs/integrations/
title: Intégrations du système, du web et de la messagerie
nav: Système, web et messagerie
section: Intégrations
order: 20
updated: 2026-11-20
description: Intégrations de l'agent HYPHA : nginx, Apache, HAProxy, IIS, BIND, Postfix, Dovecot, rspamd, Exchange, OpenVPN, fail2ban, SSH, Wazuh, Suricata, certificats, SMART, capteurs, horloge, mises à jour, timers systemd, VirtualBox, Btrfs, NFS.
lead: L'agent reconnaît les logiciels installés et relève leurs mesures, aux noms compatibles Datadog. La plupart s'activent seules.
---
## Principe

- `enabled: auto` : l'intégration s'active après une première lecture réussie. Logiciel absent : état « absent », aucun contrôle, nouvel essai toutes les 10 minutes.
- `enabled: true` force l'intégration, `enabled: false` la coupe.
- Chaque intégration publie un contrôle `<nom>.can_connect` et ne casse jamais l'agent si l'outil manque.
- L'état de chacune se lit dans **Réglages › Agents**.

Les réglages vont dans `integrations.<nom>` de [la configuration](/docs/configuration/). Les bases de données ont leur page : [Bases de données](/docs/bases-de-donnees/).

## Serveurs web et répartiteurs

| Intégration | Source | Mesures principales | Activation |
|---|---|---|---|
| `nginx` | page `stub_status` | `nginx.requests`, `nginx.connections.active`, `.reading`, `.writing`, `.waiting` | `enabled: true` et `url` |
| `apache` | `mod_status` (`?auto`) | `apache.net.hits`, `.request_per_s`, `.bytes_per_s`, `apache.performance.busy_workers`, `.idle_workers`, `apache.conns_total` | auto |
| `haproxy` | socket d'administration (`show stat`) ou page CSV | `haproxy.frontend.*`, `haproxy.backend.*`, `haproxy.server.*`, `haproxy.backend_up`, `haproxy.server.up` | auto |
| `iis` (Windows) | compteurs de performance | `iis.requests_per_s`, `iis.current_connections` | auto |

Pour nginx, ouvrez la page d'état à la seule boucle locale :

```nginx
location /nginx_status { stub_status; allow 127.0.0.1; deny all; }
```

```yaml
integrations:
  nginx: {enabled: true, url: http://127.0.0.1/nginx_status}
  apache: {enabled: auto, url: "http://127.0.0.1/server-status?auto"}
  haproxy: {enabled: auto, socket: /run/haproxy/admin.sock}   # ou url: http://127.0.0.1:8404/stats;csv
```

Les journaux d'accès de nginx, d'Apache et de HAProxy sont découpés à l'arrivée : méthode, chemin, code HTTP, durée, adresse du client. Voir [Journaux](/docs/journaux/#decoupage-a-l-arrivee).

PHP-FPM, Varnish, Squid, Lighttpd, Gunicorn et Supervisor, ainsi que PowerDNS, OpenLDAP, Keycloak, Nexus, Harbor, ZooKeeper et Ceph, ont leurs sondes dédiées : voir [Applications et services](/docs/applications-et-services/).

## DNS

`bind` lit le canal de statistiques JSON (`statistics-channels { inet 127.0.0.1 port 8053; };`), sinon `rndc status`. Mesures : `bind.queries` par type, `bind.responses.nxdomain`, `bind.responses.servfail`, `bind.zone.serial` par zone, `bind.recursive_clients`.

## Messagerie

| Intégration | Source | Mesures et contrôles |
|---|---|---|
| `postfix` | `postqueue -j` et journaux de remise | `postfix.queue.size` par file (active, deferred, hold, incoming), `postfix.delivery.sent`, `.deferred`, `.bounced` |
| `dovecot` | `doveadm stats dump`, `doveadm who` | `dovecot.auth.success`, `.failures`, `dovecot.connections` |
| `rspamd` | page `/stat` locale | `rspamd.messages.scanned`, `.learned`, `.spam`, `.ham`, `rspamd.actions` par action, `rspamd.connections` |
| `exchange` (Windows) | compteurs de performance | `exchange.queue.<état>` par dépôt de transport, `exchange.database.mounted` (et contrôle), `exchange.imap.connections` ; contrôles des messages en quarantaine ou suspendus |

Pour vérifier la messagerie de bout en bout (bannière SMTP, IMAP, envoi puis relève, SPF, DKIM, DMARC, listes noires), voir les [tests synthétiques de messagerie](/docs/synthetiques/#messagerie).

## Sécurité et accès

- `openvpn` : fichiers de statut (serveur : clients connectés ; client : débit du tunnel) : `openvpn.clients.connected`, `openvpn.client.bytes_in`, `.bytes_out`. Un fichier figé depuis 10 minutes passe `openvpn.can_connect` en critique.
- `fail2ban` : `fail2ban-client status`, relu toutes les 5 minutes : `fail2ban.banned`, `fail2ban.failed` par prison.
- Journaux d'authentification : les lignes de `sshd`, `sudo`, `su` et `fail2ban` sont analysées à l'arrivée (`evt.name`, `evt.outcome`, `usr.name`, `network.client.ip`). Le modèle « Attaque SSH par force brute » s'appuie dessus.
- `ssh` : disponibilité et bannière de serveurs SSH, sans authentification (l'agent lit la bannière, donne la sienne et ferme ; le serveur journalise une connexion close avant authentification). Contrôle `ssh.can_connect` par cible : critique si le port ne répond pas ou ne parle pas SSH, avertissement si la bannière ne vérifie pas `expect_banner` ou annonce le protocole 1 ; mesure `ssh.response_time`. Inactif tant qu'aucune cible n'est donnée :

```yaml
integrations:
  ssh:
    targets:
      - {name: bastion, address: "bastion.exemple:22", expect_banner: "^SSH-2.0-OpenSSH_9"}
      - {address: 192.0.2.10}          # port 22 par défaut
```

- `wazuh` : API REST du manager Wazuh (port 55000), avec un compte de l'API au rôle `readonly` (`url`, `username`, `password`, `insecure: true` pour son certificat autosigné). Démons du manager (`wazuh.manager.daemons` : critique si analysisd, remoted, wazuh-db, modulesd… est arrêté), agents par état de connexion (`wazuh.agents`, étiquette `status`), événements reçus, traités et perdus, alertes écrites, occupation des files d'analysisd et de remoted, sessions et octets de remoted.
- `suricata` : fichier `eve.json` (par défaut `/var/log/suricata/eve.json`), lu à la suite : dernier relevé `stats` (paquets capturés et perdus, part perdue `suricata.capture.drop_rate`, décodage, flux, TCP, détection, flux par protocole applicatif, mémoire) et alertes comptées par gravité et action (`suricata.alerts`). Contrôle `suricata.can_read` : critique si le dernier relevé `stats` a plus de `stale_after` (5 min ; Suricata arrêté, ou `stats` absent des types de `eve-log`). Au premier passage, seuls les 4 derniers Mio du fichier sont lus.

## Certificats

`x509` suit les certificats posés sur l'hôte : `tls.cert.days_left` et le contrôle `tls.cert.expiry` (avertissement sous 21 jours, critique sous 7 jours), relus toutes les heures.

- Chemins lus par défaut : `/etc/letsencrypt/live/*/cert.pem`, `/etc/ssl/*/fullchain.pem`.
- Découverte (`discover`, active par défaut) : les certificats déclarés par Apache, nginx, Postfix, Dovecot et HAProxy.
- Windows : le magasin de l'ordinateur (`stores: [My]`), sans les certificats remplacés par un plus récent ni ceux expirés depuis plus de 30 jours.

La page **Certificats** de l'interface rassemble tous les certificats relevés par les agents et par les tests synthétiques, triés par échéance.

## Matériel et système

| Intégration | Source | Mesures et contrôles |
|---|---|---|
| `smart` | `smartctl`, toutes les 30 min, sans réveiller un disque en veille | `smart.health`, `.temperature`, `.reallocated_sectors`, `.power_on_hours`, `.wear_level` |
| `hwmon` | `/sys/class/hwmon` | `system.temperature`, `system.fan.rpm` |
| `ntp` | `chronyc`, `timedatectl` ou `ntpq` ; `w32tm` sous Windows | `ntp.offset`, contrôle `ntp.in_sync` |
| `updates` | cache du gestionnaire de paquets (sans `apt update`), relu toutes les heures | `system.updates.pending`, `.security`, `system.reboot_required` ; Windows : contrôle `windows.updates.pending` |
| `systemd_timers` | `systemctl` | contrôle `systemd.timer` : critique si le dernier passage a échoué, avertissement si un timer actif n'a pas tourné depuis deux périodes |
| `virtualbox` | `VirtualBox.xml` des comptes et processus | `virtualbox.vms.running`, `.total`, contrôle `virtualbox.vm.running` des VM en démarrage automatique |
| `btrfs` | `/sys/fs/btrfs` (compteurs d'erreurs : noyau 5.14 et plus, sinon `btrfs device stats`) | par système de fichiers monté : `btrfs.allocated`, `.used`, `.usage` par type (`usage_type`) et profil (`replication_type`), `btrfs.unallocated` et `.unallocated.pct`, `btrfs.device.errors` ; contrôle `btrfs.health` : critique si un disque manque, avertissement si des compteurs d'erreurs ne sont pas nuls |
| `nfs` | client : `/proc/1/mountstats` ; serveur : `/proc/net/rpc/nfsd` | client, par montage : opérations, octets lus et écrits, temps aller-retour et d'exécution moyens, retransmissions, délais dépassés, occupation ; contrôle `nfs.mount` (critique si `statfs` ne répond pas dans `timeout`, 5 s : montage figé). Serveur : fils nfsd, appels RPC et mauvais appels, octets, opérations par version, exports ; contrôle `nfs.server` |

Un serveur NFS en conteneur garde ses statistiques dans son propre espace réseau : `integrations.nfs.server_stats: /proc/<pid du conteneur>/net/rpc/nfsd`. `nfs.mounts` restreint les montages suivis (motifs).

## Processus, ports et pages

Sans intégration particulière, l'agent sait aussi contrôler :

- la présence d'un processus (`checks.processes`) : contrôle `process.up` et mesure `process.count` ;
- une page HTTP (`checks.http`) ou un port TCP (`checks.tcp`), depuis le réseau de l'hôte.

## Vos propres mesures

L'agent écoute StatsD au format DogStatsD sur `127.0.0.1:8125/udp`, agrège sur 15 secondes et envoie le tout comme des métriques ordinaires :

```bash
echo "boutique.commandes:1|c|#env:prod" | nc -u -w0 127.0.0.1 8125
```

Pour une application qui publie déjà ses mesures au format Prometheus, des plugins Nagios ou Centreon, ou des journaux envoyés par syslog, voir [Collecte générique](/docs/collecte-generique/).

Les compteurs (`c`) deviennent des sommes, les jauges (`g`) des valeurs, les durées (`ms`, `h`) des distributions (`.avg`, `.max`, `.p50`, `.p95`, `.p99`, `.count`). Pour les métriques OpenTelemetry, voir [Traces](/docs/traces/).
