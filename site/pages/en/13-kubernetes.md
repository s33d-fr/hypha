---
id: kubernetes
path: /en/docs/kubernetes/
title: Monitor Kubernetes
nav: Kubernetes
section: Install the agent
order: 13
updated: 2026-11-19
description: Monitor a Kubernetes cluster with HYPHA: an agent on each node, API reads with a read-only account, checks and monitor templates.
lead: Two things to set up: the agent on each node for the system, and a single agent that reads the cluster API with a read-only account.
---
## The nodes

Install the agent on each node as on any [Linux](/en/docs/agent-linux/) server. It sees the node's system, processes, network and journal. HYPHA does not ship a DaemonSet yet: installing by package or script, or with your usual deployment tool (Ansible, cloud-init), is the intended way.

## The cluster

A single agent per cluster reads the API: one on a control-plane node, or on a machine that reaches the API. It reads nodes, pods, deployments, statefulsets, daemonsets and warning events. It needs neither metrics-server nor any write permission.

### 1. Create the read-only account

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

This role gives no access to secrets, configmaps, pod logs or `exec`. Check it:

```bash
kubectl auth can-i list pods -A --as=system:serviceaccount:hypha:hypha-lecture     # yes
kubectl auth can-i get secrets -A --as=system:serviceaccount:hypha:hypha-lecture   # no
```

### 2. Give the token to the agent

```bash
umask 077
kubectl -n hypha get secret hypha-lecture-jeton -o jsonpath='{.data.token}' | base64 -d | sudo tee /etc/hypha/kubernetes.token >/dev/null
```

### 3. Declare the cluster

`/etc/hypha/conf.d/20-kubernetes.yaml`:

```yaml
integrations:
  kubernetes:
    enabled: true
    cluster: production            # kube_cluster tag
    url: https://api.k8s.example:6443
    token_file: /etc/hypha/kubernetes.token   # read again on every pass
    ca_file: /etc/kubernetes/pki/ca.crt
```

Then `sudo hypha-agent check-config && sudo systemctl restart hypha-agent`.

In a pod whose service account holds this role, the agent finds the API and the mounted token on its own, without `url` or `token_file`.

Several clusters: an `instances` list, one named entry per cluster (see [Configuration](/en/docs/configuration/#several-instances)).

## What HYPHA collects

| Data | Name |
|---|---|
| Nodes, ready nodes | `kubernetes.nodes`, `kubernetes.nodes.ready` |
| Node ready, pressures, cordon (check) | `kubernetes.node` |
| Running, pending, failed, unschedulable pods | `kubernetes.pods.running`, `.pending`, `.failed`, `.unschedulable` |
| Container restarts | `kubernetes.containers.restarts` |
| Workload available (check, pods in CrashLoopBackOff named) | `kubernetes.workload` |
| Desired and available replicas | `kubernetes.workload.desired`, `.available` |
| CPU and memory requests per node | `kubernetes.node.cpu.requests_pct`, `kubernetes.node.mem.requests_pct` |
| API reachable | `kubernetes.can_connect` |

On a kubeadm node, add `/etc/kubernetes/pki/*.crt` to the watched certificates (`integrations.x509.paths`): HYPHA warns you before their one-year expiry.

## Monitor templates

The **Kubernetes** family of the catalogue: cluster unreachable, node unavailable, degraded workload, restarting containers, unschedulable pods.
