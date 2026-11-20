---
id: windows
path: /docs/windows/
title: Windows : journaux d'événements, compteurs, WMI, Active Directory et DNS
nav: Windows
section: Intégrations
order: 28
updated: 2026-11-20
description: Superviser Windows et Windows Server avec HYPHA : journaux d'événements filtrés par canal, niveau, source et identifiant, compteurs de performance et requêtes WMI au choix, contrôleurs de domaine Active Directory et serveurs DNS Microsoft.
lead: Les journaux d'événements que vous choisissez, les compteurs et les classes WMI de vos logiciels, et deux rôles de Windows Server suivis d'office : Active Directory et DNS.
---
L'agent Windows ([installation](/docs/agent-windows/)) tourne en service sous le compte système : il lit tous les journaux, Sécurité compris, et les compteurs de performance de l'hôte. Tout ce qui suit est en lecture seule.

## Journaux d'événements

Par défaut, l'agent envoie les journaux **System** et **Application**. La liste `logs.windows_eventlog` accepte des noms de canaux, ou des objets avec des filtres :

```yaml
logs:
  windows_eventlog:
    - System
    - channel: Application
      exclude_providers: ["Microsoft-Windows-Security-SPP"]   # sources écartées (motifs glob)
    - channel: Security
      event_ids: [4625, 4740, "4720-4726", 4728, 4732, 4756]  # identifiants ou plages
      service: securite-windows                                # au lieu du nom de la source
      tags: {role: dc}
    - channel: Microsoft-Windows-TaskScheduler/Operational
      levels: [critical, error, warning]
```

| Réglage | Effet |
|---|---|
| `levels` | `critical`, `error`, `warning`, `information`, `verbose`, `audit_success`, `audit_failure` ; vide = tous |
| `event_ids`, `exclude_event_ids` | identifiants (`4625`) ou plages (`4624-4634`) retenus ou écartés |
| `providers`, `exclude_providers` | sources (fournisseurs) retenues ou écartées, sans casse, motifs `*` et `?` permis |
| `service` | service HYPHA des lignes du canal (par défaut le nom de la source) |
| `tags` | étiquettes ajoutées aux lignes du canal |

Les niveaux et identifiants retenus partent dans la requête XPath adressée à Windows : un journal Sécurité de contrôleur de domaine n'est pas relu en entier. Un événement écarté ne part pas, mais la position de lecture avance : rien n'est relu au redémarrage. Les événements Sécurité sont au niveau 0 ; ils se filtrent par `audit_success` et `audit_failure`, pas par `information`. Un canal nommé (`…/Operational`, `…/Admin`) se lit comme les autres ; `wevtutil el` les liste.

Chaque ligne porte la source `eventlog` et les attributs `@eventlog.channel`, `.event_id`, `.provider`, `.record_id`, `.level` (0 à 5), `.level_name` (les noms de `levels` ci-dessus), `.audit` (`success` ou `failure`), `.task`, `.opcode`, `.keywords`, les libellés rendus par Windows dans sa langue (`.level_text`, `.task_text`, `.opcode_text`, `.keywords_text`), `.user_sid`, `.pid`, `.computer` et les données de l'événement (`@eventlog.data.TargetUserName`…). Exemple de recherche : `source:eventlog @eventlog.event_id:4625`.

La métrique `windows.eventlog.events` compte les événements collectés par canal (`channel`) et par statut (`status`). Modèles de moniteurs : **Événement Windows critique**, **Arrêt inattendu de Windows** (6008), **Échecs d'ouverture de session** (4625, canal Security à ajouter), **Compte verrouillé** (4740), **Erreurs des journaux Windows**. Tableau prêt : **Journaux d'événements Windows**.

## Compteurs de performance

Les compteurs de vos logiciels, au choix, dans `integrations.perf_counters` : un jeu par logiciel, relevé ensemble.

```yaml
integrations:
  perf_counters:
    - name: sql-locks                    # étiquette instance_name
      namespace: sqlserver               # préfixe des métriques (par défaut : le nom)
      interval: 30s                      # 10 s à 1 h
      timeout: 10s
      max_series: 500
      counters:
        - path: '\SQLServer:Locks(_Total)\Lock Waits/sec'
          metric: locks.waits            # → sqlserver.locks.waits
          unit: operation_per_second
        - path: '\PhysicalDisk(*)\Avg. Disk sec/Read'
          instance_tag: disk             # une série par instance, étiquette disk
          exclude_instances: [_Total]
```

Les chemins s'écrivent avec les **noms anglais** des objets et des compteurs, quelle que soit la langue de Windows (PowerShell les montre traduits). La liste anglaise : `(Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Perflib\009').Counter`. `(*)` relève toutes les instances (une série chacune, filtrées par `instances` et `exclude_instances`, motifs glob) ; une instance nommée (`(_Total)`) donne une seule série. Sans `metric`, le nom vient du compteur : `% Processor Time` → `processor_time_pct`, `Lock Waits/sec` → `lock_waits_per_s`. Les compteurs « /sec » sont déjà des débits.

Garde-fous : au-delà de `max_series`, les séries suivantes sont écartées ; un relevé qui dépasse `timeout` est abandonné, et aucun autre ne part tant qu'il n'a pas rendu la main. Le contrôle `perf_counters.can_collect` passe en avertissement si un compteur n'existe pas sur l'hôte ou si des séries sont écartées, en critique si rien ne se lit. Mesures du relevé : `perf_counters.series`, `.series.dropped`, `.duration`.

## Requêtes WMI

Pour ce que les compteurs ne disent pas (états, tailles, classes propres à un logiciel), `integrations.wmi` exécute des requêtes WQL en lecture seule :

```yaml
integrations:
  wmi:
    - name: os
      class: Win32_OperatingSystem
      properties: [FreePhysicalMemory, NumberOfProcesses]
    - name: disques-physiques
      wmi_namespace: root\Microsoft\Windows\Storage   # root\cimv2 par défaut
      class: MSFT_PhysicalDisk
      properties: [{property: HealthStatus, metric: sante}]
      tag_by: [FriendlyName]             # propriétés texte devenues étiquettes
    - name: services-arretes
      class: Win32_Service
      properties: [ExitCode]
      filter: "StartMode = 'Auto' AND State = 'Stopped'"
      tag_by: [Name]
      interval: 5m
```

Chaque propriété numérique de chaque ligne devient une série `<namespace>.<metric>` (par défaut le nom de la propriété : `FreePhysicalMemory` → `free_physical_memory`), étiquetée par `tag_by` (`FriendlyName` → `friendly_name`). Les entiers 64 bits, rendus en texte par WMI, sont convertis ; une propriété non numérique est signalée (à mettre dans `tag_by`). Intervalle de 15 s à 1 h (1 min par défaut), mêmes garde-fous `timeout` et `max_series` que les compteurs. Le contrôle `wmi.can_query` passe en avertissement si la requête ne rend aucune ligne, en critique si elle échoue. Mesures : `wmi.rows`, `wmi.series`, `wmi.series.dropped`, `wmi.duration`. Pour un débit, préférez le compteur de performance : une classe `Win32_PerfFormattedData_*` relue de loin en loin rend des moyennes peu fiables.

Les listes `perf_counters` et `wmi` de plusieurs fichiers `conf.d` s'additionnent. Modèles de moniteurs : **Compteurs de performance illisibles**, **Requête WMI en échec**. Tableau prêt : **Compteurs de performance et WMI**.

## Active Directory

Sur un contrôleur de domaine (service NTDS présent), l'agent s'active seul : `integrations.active_directory: {enabled: auto}` (`false` pour le couper).

- **Toutes les 30 s**, compteurs : sessions, recherches, écritures et liaisons LDAP et leur durée (`active_directory.ldap.*`), lectures, écritures et recherches de l'annuaire (`.ds.*`), réplication entrante et sortante et opérations en attente (`.dra.*`), file ATQ, authentifications Kerberos et NTLM (`.kerberos.*`, `.ntlm.authentications`), sémaphores Netlogon, goulot des authentifications NTLM (`.netlogon.*`).
- **Toutes les 30 s**, contrôle `active_directory.service` des services du rôle présents : NTDS, Netlogon, Kdc, ADWS, DFSR, IsmServ, W32Time, LanmanServer, DNS (un service désactivé est ignoré) ; `active_directory.can_connect` en critique si NTDS est arrêté ou ses compteurs illisibles.
- **Toutes les 5 min**, par PowerShell (module ActiveDirectory, servi par ADWS) : partenaires de réplication entrante, contrôle `active_directory.replication` par partenaire et partition (avertissement à l'échec, critique sans réussite depuis 24 h ou jamais), âge de la dernière réussite (`.replication.age`), rôles FSMO tenus (`.fsmo_roles`, et dans le message de `can_connect`), contrôle `active_directory.sysvol` (partages SYSVOL et NETLOGON).
- Journaux **Directory Service** et **DFS Replication** ajoutés à la collecte (`logs: false` pour ne pas les prendre ; une entrée du même canal dans `logs.windows_eventlog` l'emporte, avec ses filtres).

Modèles de moniteurs : **Contrôleur de domaine en panne**, **Service Active Directory arrêté**, **Réplication Active Directory en échec**, **SYSVOL ou NETLOGON non partagé**, **Liaisons LDAP lentes**, **Goulot Netlogon (NTLM)**. Tableau prêt : **Active Directory**. Pour les échecs d'ouverture de session et les comptes verrouillés, ajoutez le canal Security (voir plus haut).

## DNS Microsoft

Sur un serveur DNS de Windows (service DNS présent), même principe : `integrations.windows_dns`.

```yaml
integrations:
  windows_dns:
    exclude_zones: ["*.in-addr.arpa"]   # zones ni contrôlées ni mesurées (motifs glob)
    test_zone: exemple.fr               # zone de la question SOA (par défaut : la première zone principale)
    address: 127.0.0.1:53               # l'adresse du serveur s'il n'écoute pas sur la boucle locale
```

- **Toutes les 30 s** : requêtes reçues (UDP, TCP, rejetées), réponses, requêtes récursives et leurs échecs et délais, mises à jour dynamiques (`windows_dns.*`) ; mises à jour refusées, mises à jour sécurisées en échec, transferts de zone réussis et en échec, comptés entre deux relevés ; mémoire du cache et de la base.
- **Toutes les 30 s**, l'agent pose une vraie question SOA au serveur : `windows_dns.can_connect` en critique si le service est arrêté, s'il ne répond pas, répond en erreur (REFUSED, SERVFAIL…) ou sans autorité ; temps de réponse `windows_dns.response_time`.
- **Toutes les 5 min**, par PowerShell (module DnsServer) : contrôle `windows_dns.zone` par zone (avertissement si suspendue ou si le dernier transfert d'une zone secondaire a échoué, critique si arrêtée ou expirée), numéro de série des zones principales (`windows_dns.zone.serial`), âge du dernier transfert réussi des zones secondaires.
- Journal **DNS Server** ajouté à la collecte. Les changements d'enregistrements sont dans le canal `Microsoft-Windows-DNSServer/Audit`, à ajouter à `logs.windows_eventlog` si vous voulez les garder (volumineux avec beaucoup de mises à jour dynamiques).

Modèles de moniteurs : **Serveur DNS Microsoft muet**, **Zone DNS en défaut**, **Échecs de récursion DNS**. Tableau prêt : **DNS Microsoft**.

## Compatibilité

Windows Server 2016 et suivants, et Windows 10 et 11 pour les journaux, les compteurs et WMI. Éprouvé sur Windows Server 2025 en français, contrôleur unique d'un domaine avec DNS intégré à l'annuaire : la réplication entre plusieurs contrôleurs et les zones secondaires sont lues mais n'ont pas encore été éprouvées sur un vrai serveur.
