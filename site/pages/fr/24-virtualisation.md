---
id: virtualization
path: /docs/virtualisation/
title: Hyperviseurs : vSphere, Proxmox VE, Hyper-V, KVM et VirtualBox
nav: Virtualisation
section: Intégrations
order: 24
updated: 2026-11-19
description: Superviser ESXi et vCenter, Proxmox VE, Hyper-V, KVM avec libvirt et VirtualBox avec HYPHA, en lecture seule : hôtes, VM, datastores, sauvegardes, instantanés.
lead: L'état des hyperviseurs et de leurs VM, lu en lecture seule, à côté des agents installés dans les VM.
---
## VMware vSphere (ESXi et vCenter)

On n'installe pas d'agent sur un ESXi : un agent de votre parc interroge l'API vSphere avec un compte au rôle **ReadOnly**.

```yaml
integrations:
  vsphere:
    interval: 60s
    targets:
      - name: vcenter
        url: https://vcenter.exemple/sdk
        username: hypha@vsphere.local
        password: "…"
        thumbprint: "AA:BB:…"     # empreinte SHA-1 d'un certificat autosigné
        # insecure: true          # à éviter : accepte n'importe quel certificat
        tags: {site: paris}
```

Ce que HYPHA relève :

- hôtes ESXi : état, alarmes, mode maintenance, CPU, mémoire, VM en marche, durée de fonctionnement (`vsphere.host.*`) ;
- datastores : accessibilité et occupation (`vsphere.datastore.*`) ;
- VM : alimentation, état, signal du système invité, CPU, mémoire, ancienneté du plus vieil instantané (`vsphere.vm.*`) ;
- événements de vSphere.

Les hôtes ESXi et les VM deviennent des ressources dans **Infrastructure**. Une VM qui porte aussi un agent HYPHA est rapprochée de son hôte par son numéro de série (« VMware-… ») : une seule ligne, et sa fiche montre ce que vSphere en dit.

## Proxmox VE

Deux façons de lire la grappe :

- **Sur un nœud Proxmox** : l'agent utilise `pvesh` en root, sans compte, dans une unité systemd à part et bornée. Seul le premier nœud en ligne lit la grappe : les autres agents se taisent, sans doublon.
- **À distance** : l'API avec un jeton au rôle **PVEAuditor**.

```yaml
integrations:
  proxmox:
    url: https://pve.exemple:8006
    token_id: "hypha@pve!lecture"
    token_secret: "…"
    ca_file: /etc/hypha/pve-ca.pem
```

Contrôles : quorum de la grappe, nœud hors ligne, stockage indisponible, invité HA hors de l'état demandé, dernière sauvegarde (vzdump) de chaque nœud en échec, levé dès que la suivante réussit. Mesures des nœuds, des stockages et des invités (VM et conteneurs). Les tâches en échec deviennent des événements.

## Hyper-V

Sous Windows, l'agent lit `Get-VM` quand le service Hyper-V est présent ; aucun réglage.

- Contrôle `hyperv.vm` par VM : état critique (stockage injoignable), pause, arrêt d'une VM dont le démarrage automatique est « toujours », signal de vie du système invité perdu.
- Contrôle `hyperv.vm.replication` : santé de la réplication.
- Mesures : CPU, mémoire attribuée et demandée, vCPU, durée de fonctionnement, nombre de points de contrôle et âge du plus ancien (un fichier AVHDX oublié grossit sans fin).

## KVM et libvirt

Sur l'hôte, `virsh` en lecture seule (`qemu:///system` par défaut, réglable par `uri`).

- Domaines : état et raison, CPU, vCPU, mémoire, disques, réseau (`libvirt.domain.*`).
- Contrôles : domaine planté, mis en pause sur erreur d'entrée-sortie (pool plein, le cas classique), arrêté alors que son démarrage automatique est actif ; pool de stockage inactif ou inaccessible.
- Pools : occupation et espace disponible (`libvirt.pool.*`).

## VirtualBox

L'agent de l'hôte relève les VM déclarées et en marche, et signale une VM à démarrage automatique arrêtée (`virtualbox.vm.running`). Réglage `user` pour ne suivre qu'un compte.

## Modèles de moniteurs

Famille **Virtualisation** du catalogue : connecteur vSphere en échec, hôte ESXi en défaut, datastore inaccessible ou presque plein, VM vSphere en défaut, instantané ancien ; Proxmox injoignable, quorum perdu, nœud hors ligne, stockage indisponible ou presque plein, invité HA hors de son état, sauvegarde en échec ; VM Hyper-V en défaut, point de contrôle ancien, réplication dégradée ; VM KVM en défaut, pool inactif ou presque plein.
