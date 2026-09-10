# read-k8s — Kubernetes, both ways

```sh
go run ./read-k8s
```

**Runs outside a cluster.** The API path talks to an `httptest` server; the mount path reads a directory laid out exactly as kubelet lays one out — `..data` symlink and all.

## Two paths, and they are not interchangeable

| | Needs | Sees a change |
|---|---|---|
| `ConfigMap` / `Secret` | RBAC to read the resource | immediately |
| `Mount` | **nothing** — kubelet already wrote the file | on kubelet's refresh |

`Mount` is the one to reach for when the pod should not have permission to read the API at all. That is a meaningful hardening step: a compromised process with no API credential cannot enumerate other secrets.

## Real usage

```go
k8s.ConfigMap("app-config")   // in-cluster: namespace, address and token all come
k8s.Secret("app-secrets")     // from the service account kubelet mounted
k8s.Mount("/etc/config")      // no credential at all
```

## What it prints

```
from the API:
FIELD     KEY       VALUE         SOURCE
Host      HOST      api.internal  k8s:configmaps/app-config
Password  PASSWORD  ••••••        k8s:secrets/app-secrets
Port      PORT      8443          k8s:configmaps/app-config

from a projected volume:
FIELD     KEY       VALUE             SOURCE
Host      HOST      mounted.internal  k8smount:projected-volume
...
```

Provenance distinguishes the ConfigMap from the Secret. Merging them under one name would make an RBAC problem impossible to localise.

## The `..data` symlink is the whole trick

A projected volume is not a plain directory. kubelet writes values into a **timestamped** directory and points a `..data` symlink at it; each key is a symlink through `..data`. On update kubelet swaps the *link*.

So a reader that follows `..data` sees either the whole old version or the whole new one — **never a half-updated mix**. A reader that walked the directory naively could read `USERNAME` from the old version and `PASSWORD` from the new one, which is exactly the failure that produces a five-minute outage nobody can reproduce.

The adapter therefore follows `..data`, skips dot-prefixed bookkeeping entries, skips dangling symlinks rather than failing, and does not descend into subdirectories.

## No `client-go`

Reading one named resource is a single HTTP GET. `client-go` brings API machinery, several codec layers, and a version treadmill tied to the cluster's release cadence. This module is `net/http` plus the files every pod already has.

If you need kubeconfig, exec/OIDC plugins, or watching, your program already has `client-go` — and `cfgkit.SourceFunc` wraps it in five lines.

## Next

- [`read-etcd`](../read-etcd) — the store Kubernetes itself uses
- [`provenance`](../provenance) — auditing which resource supplied what
