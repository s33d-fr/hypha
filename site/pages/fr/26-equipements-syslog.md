---
id: syslog-devices
path: /docs/equipements-syslog/
title: Équipements réseau et sécurité (syslog)
nav: Équipements (syslog)
section: Intégrations
order: 26
updated: 2026-11-20
description: Envoyer le syslog d'un pare-feu ou d'un routeur (Fortinet, Stormshield, pfSense, OPNsense, Palo Alto, Cisco ASA, SonicWall, WatchGuard, Check Point, MikroTik, Ubiquiti) vers un agent HYPHA, et lire refus, menaces, connexions d'administration et VPN.
lead: L'équipement envoie ses journaux à un agent de votre réseau ; le serveur les découpe avec le modèle de l'éditeur et en tire des attributs communs à tous les équipements.
---
## Principe

1. L'équipement envoie son syslog (UDP ou TCP) à un agent HYPHA de son réseau.
2. L'agent reçoit, décode l'en-tête syslog et note, d'après sa règle `parsers`, le modèle d'analyse à appliquer (attribut `syslog.parser`).
3. Le serveur découpe le message avec ce modèle : action du pare-feu, adresses et ports, protocole, règle, utilisateur, interface, gravité propre à l'équipement, type d'événement.

> **Éprouvé sur des exemples, pas sur les équipements.** Chaque modèle a été vérifié sur des lignes tirées de la documentation publique de l'éditeur (ou, quand elle n'en publie pas, d'un guide d'intégration public ou du format documenté), envoyées en UDP et en TCP à un agent d'essai. Aucun de ces équipements n'a été branché pour de vrai. Une ligne que le modèle ne reconnaît pas reste entière dans le message, sans attribut ajouté.

## Activer la réception sur un agent

La réception syslog est coupée par défaut. Ajoutez un fichier `conf.d/40-syslog.yaml` à l'agent qui recevra les journaux :

```yaml
syslog:
  enabled: true
  udp: "0.0.0.0:5514"          # adresse:port ; vide = pas d'UDP
  tcp: "0.0.0.0:5514"          # trames terminées par une fin de ligne ou préfixées de leur longueur
  allowed_sources: ["192.0.2.0/24"]   # seuls émetteurs admis (vide = tous)
  parsers:
    - {match: 192.0.2.1, parser: fortinet}        # adresse de l'émetteur
    - {match: 192.0.2.16/29, parser: cisco-asa}   # réseau CIDR
    - {match: "pfsense*", parser: pfsense}         # motif sur le nom d'hôte de l'en-tête
  tags: {site: siege}
```

- Un port au-dessus de 1024 (ici 5514) évite de donner des droits à l'agent ; le port 80 est refusé.
- L'agent ne touche à aucun pare-feu : ouvrez le port sur l'hôte pour les seuls équipements émetteurs.
- `match` vaut une adresse, un réseau CIDR, ou un motif (`*`, `?`) sur le nom d'hôte de l'en-tête syslog (à défaut, l'adresse). La première règle qui correspond l'emporte.
- `rate_limit` (2 000 lignes/s par défaut) et `max_connections` (64) bornent la réception ; les lignes jetées sont comptées dans `syslog.dropped`.
- Vérifiez avec `sudo hypha-agent check-config`, puis redémarrez l'agent.

## Modèles

| Équipement | `parser` | Format reçu | Exemples des essais |
|---|---|---|---|
| Fortinet FortiGate (FortiOS) | `fortinet` | clé=valeur (`date=… logid=…`) | éditeur : « Sample logs by log type » (FortiOS 7.0) et Fortinet Community |
| Stormshield SNS | `stormshield` | clé=valeur WELF (`id=firewall …`) | éditeur (alarme), FortiSIEM (filtrage) |
| pfSense | `pfsense` | `filterlog` CSV | éditeur (« Viewing the Firewall Log ») ; grammaire de Netgate |
| OPNsense | `opnsense` | `filterlog` CSV (même format) | construites sur la grammaire de Netgate |
| Palo Alto Networks (PAN-OS) | `paloalto` | CSV TRAFFIC, THREAT, SYSTEM, CONFIG, GLOBALPROTECT | construites sur l'ordre des champs de PAN-OS 10.2 |
| Cisco ASA et FTD | `cisco-asa` | `%ASA-n-nnnnnn: …` | éditeur (Cisco Security Center) ; gabarits du guide des messages |
| SonicWall (SonicOS) | `sonicwall` | clé=valeur (`id=firewall … m=… msg=…`) | FortiSIEM, IBM/Juniper JSA |
| WatchGuard Firebox | `watchguard` | `msg_id="…"` puis `Allow` ou `Deny …` | éditeur (catalogue des journaux Fireware 12.12) ; Graylog |
| Check Point | `checkpoint` | Log Exporter : syslog `[clé:"valeur"; …]`, CEF, LEEF | construites sur le format du guide Log Exporter |
| MikroTik RouterOS | `mikrotik` | `sujets message` (`firewall,info input: …`) | éditeur (page « Log » de RouterOS) |
| Ubiquiti UniFi et EdgeOS | `ubiquiti` | journaux du noyau (`[RÈGLE] IN=… SRC=…`), CEF de UniFi Network | Graylog (le site de l'éditeur refuse les lectures automatiques) |

Le modèle `kv` (vague 1) reste disponible pour tout autre équipement qui écrit des paires `clé=valeur` : chaque clé devient un attribut, sans normalisation.

## Attributs produits

Les mêmes pour tous les modèles, quand l'équipement les donne :

| Attribut | Valeurs |
|---|---|
| `evt.category` | `traffic`, `threat`, `vpn`, `admin`, `system` |
| `evt.name` | nom de l'événement chez l'éditeur (sous-type, numéro de message, `admin_login`…) |
| `evt.outcome` | `success`, `failure` (connexions, VPN) |
| `fw.action` | `allow`, `deny` |
| `fw.rule` | nom ou numéro de la règle, de la politique ou de la liste d'accès |
| `network.source.ip`, `network.source.port` | source du flux |
| `network.destination.ip`, `network.destination.port` | destination du flux |
| `network.protocol`, `network.interface.in`, `network.interface.out` | protocole et interfaces |
| `usr.name` | utilisateur ou administrateur |
| `threat.name`, `threat.type` | signature, virus, type de menace |
| `vpn.tunnel`, `vpn.peer`, `vpn.status` | tunnel, pair distant, `up` ou `down` |
| `device.vendor`, `device.product`, `device.name`, `device.severity` | équipement et gravité donnée par l'équipement |

Les champs bruts de l'éditeur restent sous son préfixe (`fortinet.*`, `stormshield.*`, `filterlog.*`, `paloalto.*`, `cisco.asa.*`, `sonicwall.*`, `watchguard.*`, `checkpoint.*`, `mikrotik.*`, `ubiquiti.*`). `network.client.ip` reste l'adresse de l'équipement émetteur.

Le statut de la ligne est aggravé, jamais abaissé : menace → `warning` (`error` si l'équipement la dit haute ou critique) ; échec de connexion d'administration, échec VPN, tunnel tombé → `warning`.

Exemples de recherche : `source:syslog @fw.action:deny`, `@evt.category:threat`, `@evt.category:admin @evt.outcome:failure`, `@syslog.parser:cisco-asa @vpn.status:down`.

## Régler l'envoi sur l'équipement

Remplacez `192.0.2.50` par l'adresse de l'agent et `5514` par son port. Les menus changent d'une version à l'autre : vérifiez dans la documentation de la vôtre.

- **Fortinet** : `config log syslogd setting`, `set status enable`, `set server "192.0.2.50"`, `set port 5514`, `set mode udp` (ou `reliable` pour TCP), `set format default` ; puis activez la journalisation sur les politiques voulues.
- **Stormshield** : module *Traces - Syslog - IPFIX*, onglet *Syslog* : un profil vers l'agent (UDP ou TCP, port, format RFC 5424), familles de journaux cochées (filtrage, alarmes, VPN, administration).
- **pfSense** : *Status > System Logs > Settings*, *Enable Remote Logging*, serveur `192.0.2.50:5514`, contenus *Firewall Events* et *System Events*. pfSense n'envoie qu'en UDP.
- **OPNsense** : *System > Settings > Logging*, onglet des destinations distantes : transport UDP ou TCP, applications `filter` (filterlog) et `audit`, port.
- **Palo Alto** : *Device > Server Profiles > Syslog* (serveur, UDP ou TCP, port, format BSD ou IETF), puis un profil *Log Forwarding* sur les règles (trafic, menaces) et *Device > Log Settings* (système, configuration, GlobalProtect). Le format CSV par défaut est celui qu'attend le modèle.
- **Cisco ASA** : `logging enable`, `logging timestamp`, `logging trap informational`, `logging host inside 192.0.2.50 udp/5514` (ou `tcp/5514` ; en TCP, ajoutez `logging permit-hostdown`, sinon l'ASA bloque le trafic quand le serveur syslog ne répond plus).
- **SonicWall** : *Device > Log > Syslog*, ajoutez le serveur (adresse, port), format *Default*.
- **WatchGuard** : *System > Logging*, onglet *Syslog Server* : adresse, port, format *Syslog*.
- **Check Point** : sur le serveur de journaux, `cp_log_export add name hypha target-server 192.0.2.50 target-port 5514 protocol udp format syslog`, puis `cp_log_export restart name hypha`.
- **MikroTik** : `/system logging action add name=hypha target=remote remote=192.0.2.50 remote-port=5514`, puis `/system logging add topics=firewall action=hypha`, `/system logging add topics=account action=hypha`, `/system logging add topics=critical action=hypha`. RouterOS ne dit pas ce que fait une règle qui journalise : mettez `log=yes log-prefix="drop"` sur les règles de rejet pour que `fw.action` vaille `deny`.
- **Ubiquiti** : UniFi Network 9 et suivants, *Settings > Control Plane > Integrations > Activity Logging*, serveur SIEM (CEF) ; journalisation activée sur les règles de pare-feu voulues. EdgeOS : `set system syslog host 192.0.2.50:5514 facility all level info` et `log enable` sur les règles.

## Moniteurs et tableaux prêts

Famille *Équipements réseau et sécurité* du [catalogue des moniteurs](/docs/moniteurs/), chacun groupé par équipement émetteur et restreint à un modèle si vous le voulez (variable `parser`) :

- **Pic de refus du pare-feu** (`fw.action:deny`, 5 minutes) ;
- **Menaces détectées par un équipement** (`evt.category:threat`) ;
- **Échecs de connexion d'administration d'un équipement** (10 minutes) ;
- **Tunnel VPN tombé** (`vpn.status:down`) et **Échecs VPN répétés** (15 minutes).

S'y ajoutent ceux de la réception elle-même : *Réception syslog en échec*, *Équipement syslog muet*, *Lignes syslog jetées*.

Le catalogue des tableaux propose un tableau par modèle (*Fortinet FortiGate (syslog)*, *Cisco ASA et FTD (syslog)*…) : lignes reçues par équipement, lignes jetées, puis le trafic refusé, les menaces, l'administration, le VPN et les erreurs, filtrables par agent et par équipement.

## Ce qui a été éprouvé

Dans une machine d'essai, un agent a reçu sur un port non privilégié, en UDP puis en TCP, chaque ligne d'exemple des onze modèles, envoyée depuis une adresse propre à chaque modèle ; les attributs analysés ont été relus dans HYPHA. Les lignes d'exemple et leur source sont dans `server/tests/test_equipements.py`. Ce qui ne l'est pas : le comportement des équipements réels (formats propres à une version, champs absents, volumes), les messages d'administration et de VPN que les éditeurs ne publient pas en exemple (Stormshield, SonicWall, Check Point, UniFi). Si un journal de votre équipement n'est pas reconnu, la ligne garde tout son texte : envoyez-nous un exemple anonymisé.
