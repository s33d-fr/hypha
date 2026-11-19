---
id: snmp
path: /docs/snmp/
title: Équipements réseau en SNMP
nav: SNMP
section: Intégrations
order: 22
updated: 2026-11-19
description: Superviser switchs, routeurs, NAS, imprimantes et onduleurs en SNMP v1, v2c et v3 depuis un agent HYPHA, sans rien installer sur l'équipement.
lead: Un agent interroge les équipements de son réseau et les publie comme des hôtes à part entière. Les secrets SNMP ne quittent jamais l'agent.
---
## Déclarer un équipement

Dans la configuration d'un agent qui joint l'équipement, de préférence un fichier `conf.d/20-snmp.yaml` (droits 600) :

```yaml
snmp:
  enabled: true
  interval: 60s
  devices:
    # SNMP v2c
    - {name: switch-baie-1, address: 192.0.2.10, version: 2c, community: lecture, tags: {role: switch}}
    # SNMP v3, authentification et chiffrement
    - name: nas-sauvegardes
      address: 192.0.2.20
      version: 3
      user: hypha
      level: authPriv
      auth_protocol: sha        # md5, sha, sha224, sha256, sha384, sha512
      auth_key: "…"             # 8 caractères au moins
      priv_protocol: aes        # des, aes, aes192, aes256, aes192c, aes256c
      priv_key: "…"
      profile: auto
```

Autres réglages par équipement : `port` (161), `timeout` (5 s), `retries`, `context`, `interfaces_exclude` (motifs), `interface_down`.

Conseils :

- préférez SNMPv3 en `authPriv`, avec un compte en lecture seule limité à l'adresse de l'agent ;
- désactivez la communauté `public` de l'équipement avant d'activer SNMP ;
- vérifiez avec `sudo hypha-agent check-config`, puis redémarrez l'agent.

## Profils

`profile: auto` lit `sysObjectID` et choisit le profil :

| Profil | Ce qu'il ajoute |
|---|---|
| `generic` | MIB-II, IF-MIB, HOST-RESOURCES-MIB : interfaces, processeur, mémoire, stockage |
| `mikrotik` | processeur, températures, ventilateurs, alimentations de RouterOS |
| `synology` | état des disques, des volumes et du RAID |
| ASUSTOR (détecté) | températures, ventilateurs, état et température des disques, état et occupation des volumes ; contrôles `snmp.disk.health` et `snmp.volume.health` |
| `printer` | état de l'imprimante et consommables |
| `ups` | UPS-MIB : charge de la batterie, autonomie, charge de sortie |

## Mesures et contrôles

| Mesure | Unité |
|---|---|
| `snmp.ifBandwidthIn`, `.ifBandwidthOut` | octets par seconde |
| `snmp.ifBandwidthInUsage`, `.OutUsage` | pourcentage du débit de l'interface |
| `snmp.ifInErrors`, `.ifOutErrors`, `.ifInDiscards`, `.ifOutDiscards` | paquets par seconde |
| `snmp.ifOperStatus`, `snmp.ifAdminStatus` | état |
| `snmp.cpu.usage`, `snmp.memory.usage`, `snmp.storage.usage` | pourcentage |
| `snmp.temperature`, `snmp.fan.speed`, `snmp.psu.status` | degrés, tours par minute, état |
| `snmp.ups.battery.charge`, `.runtime`, `.load` | pourcentage, secondes |
| `snmp.sysUpTimeInstance`, `snmp.poll.duration` | secondes, millisecondes |

Contrôles :

- `snmp.can_poll` : critique si l'équipement ne répond plus ;
- `snmp.interface.status` : critique pour une interface active mais hors service. Avec `interface_down: seen` (par défaut), seule une interface déjà vue en service compte : un port libre d'un switch reste OK. `always` applique la règle à toutes les interfaces actives.

## Traps

```yaml
snmp:
  traps: {enabled: true, listen: "0.0.0.0:162", community: lecture}
```

Chaque trap v1 ou v2c reçu devient un événement (`source:snmp type:snmp_trap`), 60 par minute au plus. Un moniteur de type « événement » peut alerter dessus.

## Dans l'interface

- **Infrastructure** filtre par type (serveurs ou équipements réseau). La fiche d'un équipement montre son identité et ses interfaces en tableau : nom, alias, état, vitesse, débit, erreurs, avec de petites courbes.
- **Réglages › Équipements** liste les équipements vus, l'agent qui les interroge et leur dernier contact.
- La carte du réseau place les équipements comme des nœuds.

Modèles du catalogue (famille **Réseau**) : interface réseau en panne, équipement injoignable.
