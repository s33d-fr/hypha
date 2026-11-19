---
id: kubernetes
path: /docs/kubernetes/
title: Superviser Kubernetes
nav: Kubernetes
section: Installer l'agent
order: 13
updated: 2026-11-19
description: Superviser une grappe Kubernetes avec HYPHA : agent sur chaque nœud, lecture de l'API avec un compte en lecture seule, contrôles et modèles de moniteurs.
lead: Deux choses à poser : l'agent sur chaque nœud pour le système, et un agent unique qui lit l'API de la grappe avec un compte en lecture seule.
---
## Les nœuds

Installez l'agent sur chaque nœud comme sur tout serveur [Linux](/docs/agent-linux/). Il voit le système, les processus, le réseau et le journal du nœud. HYPHA ne livre pas encore de DaemonSet : la pose par paquet ou par script, ou par votre outil de déploiement habituel (Ansible, cloud-init), est la voie prévue.

## La grappe

Un seul agent par grappe lit l'API : celui d'un nœud du plan de contrôle, ou d'une machine qui joint l'API. Il lit les nœuds, les pods, les déploiements, statefulsets et daemonsets et les événements d'avertissement. Il n'a besoin ni de metrics-server ni d'aucun droit d'écriture.

### 1. Créer le compte en lecture seule

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: hypha
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: hypha-lecture
  namespace: hypha
automountServiceAccountToken: false
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: hypha-lecture
rules:
  - apiGroups: [""]
    resources: [nodes, pods, events, namespaces]
    verbs: [get, list, watch]
  - apiGroups: [apps]
    resources: [deployments, statefulsets, daemonsets, replicasets]
    verbs: [get, list, watch]
  - nonResourceURLs: [/version, /healthz, /readyz, /livez]
    verbs: [get]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: hypha-lecture
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: hypha-lecture
subjects:
  - kind: ServiceAccount
    name: hypha-lecture
    namespace: hypha
---
apiVersion: v1
kind: Secret
metadata:
  name: hypha-lecture-jeton
  namespace: hypha
  annotations:
    kubernetes.io/service-account.name: hypha-lecture
type: kubernetes.io/service-account-token
```

Ce rôle ne donne accès ni aux secrets, ni aux configmaps, ni aux journaux des pods, ni à `exec`. Vérifiez-le :

```bash
kubectl auth can-i list pods -A --as=system:serviceaccount:hypha:hypha-lecture     # yes
kubectl auth can-i get secrets -A --as=system:serviceaccount:hypha:hypha-lecture   # no
```

### 2. Donner le jeton à l'agent

```bash
umask 077
kubectl -n hypha get secret hypha-lecture-jeton -o jsonpath='{.data.token}' | base64 -d | sudo tee /etc/hypha/kubernetes.token >/dev/null
```

### 3. Déclarer la grappe

`/etc/hypha/conf.d/20-kubernetes.yaml` :

```yaml
integrations:
  kubernetes:
    enabled: true
    cluster: production            # étiquette kube_cluster
    url: https://api.k8s.exemple:6443
    token_file: /etc/hypha/kubernetes.token   # relu à chaque passage
    ca_file: /etc/kubernetes/pki/ca.crt
```

Puis `sudo hypha-agent check-config && sudo systemctl restart hypha-agent`.

Dans un pod dont le compte de service porte ce rôle, l'agent trouve l'API et le jeton monté seul, sans `url` ni `token_file`.

Plusieurs grappes : une liste `instances`, une entrée nommée par grappe (voir [Configuration](/docs/configuration/#plusieurs-instances)).

## Ce que HYPHA relève

| Donnée | Nom |
|---|---|
| Nœuds, nœuds prêts | `kubernetes.nodes`, `kubernetes.nodes.ready` |
| Nœud prêt, pressions, cordon (contrôle) | `kubernetes.node` |
| Pods en marche, en attente, en échec, impossibles à placer | `kubernetes.pods.running`, `.pending`, `.failed`, `.unschedulable` |
| Redémarrages de conteneurs | `kubernetes.containers.restarts` |
| Charge de travail disponible (contrôle, pods en CrashLoopBackOff cités) | `kubernetes.workload` |
| Réplicas voulus et disponibles | `kubernetes.workload.desired`, `.available` |
| Réservations CPU et mémoire par nœud | `kubernetes.node.cpu.requests_pct`, `kubernetes.node.mem.requests_pct` |
| API joignable | `kubernetes.can_connect` |

Sur un nœud kubeadm, ajoutez `/etc/kubernetes/pki/*.crt` aux certificats suivis (`integrations.x509.paths`) : HYPHA vous prévient avant leur échéance d'un an.

## Modèles de moniteurs

Famille **Kubernetes** du catalogue : grappe injoignable, nœud indisponible, charge dégradée, conteneurs qui redémarrent, pods impossibles à placer.
