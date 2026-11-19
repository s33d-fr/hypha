---
id: cloud
path: /docs/cloud/
title: Clouds AWS, Azure, Google Cloud, OVHcloud et Scaleway
nav: Clouds
section: Intégrations
order: 23
updated: 2026-11-19
description: Connecteurs cloud de HYPHA en lecture seule : instances AWS EC2, volumes EBS et alarmes CloudWatch, VM Azure et Resource Health, Compute Engine, serveurs OVHcloud, Scaleway Instances et Elastic Metal.
lead: Un agent lit l'API du fournisseur avec un compte en lecture seule. Les instances deviennent des ressources dans HYPHA, à côté de vos hôtes.
---
## Principe

- Le connecteur tourne dans un agent de votre choix : il lui faut seulement un accès sortant vers l'API du fournisseur.
- Il s'active dès que ses identifiants sont posés. Rangez-les dans un fichier `conf.d` réservé à root (600).
- Chaque instance ou serveur devient une **ressource cloud** : un hôte sans agent, rattaché à l'agent qui l'interroge, avec son état chez le fournisseur et ses contrôles.
- Si un agent HYPHA tourne aussi dans l'instance, HYPHA rapproche les deux (même numéro de série, ou même adresse publique) et n'affiche qu'une ligne. La fiche de l'hôte montre alors « Vu par les connecteurs ».
- Plusieurs comptes ou projets : une liste `instances` (voir [Configuration](/docs/configuration/#plusieurs-instances)).
- Chaque connecteur publie `<nom>.can_connect`. Le catalogue de moniteurs porte un modèle « connecteur en échec » par fournisseur.

## AWS

Lecture d'EC2, d'EBS, des alarmes CloudWatch et de l'identité du compte, par une politique IAM en lecture seule :

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": ["ec2:DescribeInstances", "ec2:DescribeInstanceStatus", "ec2:DescribeVolumes",
               "cloudwatch:DescribeAlarms", "sts:GetCallerIdentity"],
    "Resource": "*"
  }]
}
```

```yaml
integrations:
  aws:
    access_key_id: AKIA…
    secret_access_key: "…"
    regions: [eu-west-3, eu-central-1]
    interval: 5m
    metrics: false
```

- Instances : état, type, adresses, contrôles d'état d'EC2 et événements planifiés (`aws.ec2.status`).
- Volumes EBS en erreur (`aws.ebs.volume`), alarmes CloudWatch déclenchées (`aws.cloudwatch.alarm`).
- `metrics: true` ajoute le CPU des instances par `cloudwatch:GetMetricData` (à ajouter à la politique). Cet appel est facturé par AWS à votre compte : il reste coupé par défaut.

## Azure

Un principal de service au rôle **Lecteur** sur chaque abonnement suivi :

```bash
az ad sp create-for-rbac --name hypha-lecture --role Reader --scopes /subscriptions/<id-abonnement>
```

```yaml
integrations:
  azure:
    tenant_id: "…"
    client_id: "…"
    client_secret: "…"
    subscriptions: ["<id-abonnement>"]
```

- VM : taille, système, état d'alimentation, adresses (interfaces et IP publiques).
- Santé vue par Azure (Resource Health) : contrôle `azure.vm.health`. Une VM désallouée par son propriétaire n'est pas une panne.
- Alertes d'Azure Monitor déclenchées : un contrôle par abonnement, qui revient au vert dès qu'aucune n'est active.

## Google Cloud

Un compte de service au rôle **Lecteur de Compute** (`roles/compute.viewer`) et sa clé JSON, dans un fichier à part :

```yaml
integrations:
  gcp:
    credentials_file: /etc/hypha/gcp-lecture.json   # 600, root
    projects: [mon-projet]
```

Les instances de toutes les zones du projet : état, type, adresses (`gcp.instance.status`). Une instance que Google répare (`REPAIRING`) passe en critique.

## OVHcloud

Une application de l'API OVHcloud et une clé « consumer » limitée aux lectures : droits `GET` sur `/dedicated/server*`, `/ip*` et `/domain*`.

```yaml
integrations:
  ovhcloud:
    endpoint: https://eu.api.ovh.com/1.0
    application_key: "…"
    application_secret: "…"
    consumer_key: "…"
    interval: 15m
```

Ce que l'agent d'une machine ne voit pas :

- l'état de chaque serveur dédié vu par OVHcloud (`ovh.dedicated_server.state`) ;
- les adresses IP bloquées pour spam (`ovh.ip.spam`) ou pour piratage (`ovh.ip.antihack`) ;
- la mitigation anti-DDoS en cours (`ovh.ip.mitigation`) ;
- l'échéance des services et des domaines (`ovh.service.renewal`, `ovh.service.days_left`), relue toutes les 6 heures ;
- les interventions et tâches d'OVHcloud sur vos serveurs, en événements.

Seuls des `GET` partent, espacés de 200 ms ; 5 minutes au moins entre deux passages.

## Scaleway

La clé d'API d'un membre IAM aux droits `InstancesReadOnly` et `ElasticMetalReadOnly` :

```yaml
integrations:
  scaleway:
    secret_key: "…"
    zones: [fr-par-1, fr-par-2, nl-ams-1]
```

Par zone : les instances (état, verrouillage, maintenances planifiées) et les serveurs Elastic Metal (état, réponse au ping de Scaleway).

## Ce que cela coûte

- Côté fournisseur : les lectures d'EC2, de STS, d'Azure Resource Manager et de Compute Engine ne sont pas facturées ; chez AWS, `DescribeAlarms` entre dans le volume d'appels CloudWatch offert chaque mois, et `metrics: true` est facturé. Vérifiez les conditions de votre fournisseur.
- Côté HYPHA : chaque ressource lue par un connecteur compte comme un équipement. Voir les [tarifs](/tarifs/).
