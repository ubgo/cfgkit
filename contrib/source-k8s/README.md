# cfgkit/contrib/source-k8s

**Support level: supported** — 100% of its behaviour is exercised against a fake API server, including the in-cluster path. See [the catalogue](../../docs/catalogue.md#support-levels).

Read a Kubernetes **ConfigMap** or **Secret** as a cfgkit source.

```go
import k8s "github.com/ubgo/cfgkit/contrib/source-k8s"

cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
	cfgkit.FromFiles(".env"),
	k8s.ConfigMap("app-config"),
	k8s.Secret("app-secrets"),
	cfgkit.FromEnviron(),        // still wins over everything
))
```

Inside a pod that is all the configuration needed: the namespace, the API address and the credential all come from the service account kubelet already mounted.

## It carries no dependencies

That is the design decision worth explaining, because a Kubernetes client without `client-go` is unusual.

Reading one named resource is a single HTTP GET. `client-go` is a very large dependency to take for that — it brings the API machinery, several codec layers, and a version treadmill tied to the cluster's release cadence. This module is `net/http`, `encoding/json`, and the files kubelet mounts into every pod.

```
require github.com/ubgo/cfgkit v0.0.0
```

That is the whole `go.mod`. Your `go.sum` gains nothing else.

## What this does not do

Stated in full, because the omissions are the price of the paragraph above:

| Not supported | What to do instead |
|---|---|
| kubeconfig files | `kubectl proxy` for development (below), or wrap `client-go` yourself |
| `exec` / OIDC / cloud credential plugins | wrap `client-go` — five lines through `cfgkit.SourceFunc` |
| Watching for changes | pair `cfgkit.Watcher` with your own trigger — see [Reload](../../docs/reload.md) |
| Listing or discovering resources | name the resource you want |
| `binaryData` | use `data`; binary values are not configuration |

None of these is hard to add in *your* program if you need it, and `Source` is the seam:

```go
// You already have client-go? Then this module is not the only way.
cm, err := clientset.CoreV1().ConfigMaps(ns).Get(ctx, "app-config", metav1.GetOptions{})
if err != nil {
	return err
}
src := cfgkit.SourceFunc("k8s", func(key string) (string, bool, error) {
	v, ok := cm.Data[key]
	return v, ok, nil
})
```

## Two ways to read the same object

```go
k8s.ConfigMap("app-config")   // through the API
k8s.Mount("/etc/app-config")  // from the volume kubelet already mounted
```

| | `Mount()` | `ConfigMap()` |
|---|---|---|
| RBAC rule needed | **no** | yes |
| API round trip | **none** | one |
| Works when the API server is unreachable | **yes** | no |
| Refreshed in place by kubelet | **yes** | re-read to see changes |
| Requires the volume in the pod spec | yes | **no** |
| Can read another namespace | no | **yes** |

**`Mount` is usually the better default in a pod** — it is what the platform already does for you, and it is the only one of the two that keeps working when the API server is down. Reach for `ConfigMap()` when the object is not mounted, or lives elsewhere.

```yaml
volumeMounts:
  - name: config
    mountPath: /etc/app-config
```

Each **file is one key**: the filename is the key, the contents are the value — exactly how kubelet projects `data`.

### The layout it handles

kubelet does not write a flat directory. It writes a timestamped directory, points a `..data` symlink at it, and gives each key a symlink through that. This provider **follows `..data`**, which is what makes a read atomic: an update swings one symlink, so a reader sees the whole old version or the whole new one, never a mixture.

| | |
|---|---|
| `..data`, `..2026_…` | skipped — a ConfigMap key cannot start with a dot, so this can never hide a real key |
| a dangling symlink | **skipped, not fatal** — that is how a deleted key looks until the next resync, and failing would let one deletion take the process down on its next restart |
| a subdirectory | not descended into — a ConfigMap has no nesting to project, so guessing at one would invent a namespace the platform never defined |
| one trailing newline | trimmed, matching the core's `,file` option. kubelet adds none, so this only affects a hand-made directory — where an editor's newline was never part of the value |

A **flat directory works too**, which is how a `subPath` mount looks and how the mount is usually faked in a local run.

## Development, outside a cluster

```sh
kubectl proxy --port=8001
```

```go
k8s.ConfigMap("app-config",
	k8s.WithServer("http://127.0.0.1:8001"),
	k8s.WithNamespace("default"),
)
```

No bearer token is needed — `kubectl proxy` authenticates for you, and this module treats an absent token file as normal rather than as an error, precisely so this path works.

## Secrets

Values arrive base64-encoded from the API and are decoded for you, so a `Secret` behaves exactly like a `ConfigMap`:

```go
type Config struct {
	Password string `env:"PASSWORD" secret:"true"`
}
```

Mark the field `secret:"true"` and cfgkit masks it in `Explain`, in `Result.JSON`, and in error text. A malformed value is reported by **key name only** — the value never reaches the message, because an error message is a thing that gets logged.

## A missing resource is an error

This is the one place the module deliberately differs from `FromFiles`:

| | Missing means |
|---|---|
| `cfgkit.FromFiles(".env.local")` | nothing — a file path is often speculative |
| `k8s.ConfigMap("app-config")` | **an error** — you named a resource, so its absence is a manifest mistake |

Starting on compiled-in defaults because somebody forgot a ConfigMap would hide the mistake until something behaved oddly in production. When a resource genuinely is optional:

```go
k8s.ConfigMap("overrides", k8s.Optional())
```

## Typo detection comes free

The source implements `cfgkit.KeyLister`, so a key in the ConfigMap that matches no field is reported:

```go
for _, u := range res.Unknown() {
	log.Printf("config warning: %s", u)
}
```

```
HSOT (from k8s:configmaps/app-config) matched no field
```

A ConfigMap's keys were written for *this* application, which is why it can honestly enumerate — unlike the process environment, whose keys belong to the machine.

## RBAC

The pod's service account needs `get` on the resources you read:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: read-app-config
rules:
  - apiGroups: [""]
    resources: ["configmaps", "secrets"]
    resourceNames: ["app-config", "app-secrets"]
    verbs: ["get"]
```

`resourceNames` is worth keeping: this module reads named resources and never lists, so the grant can be exactly that narrow.

Without the rule you get a message that names the fix rather than the status code:

```
forbidden reading configmaps/app-config in namespace "prod" —
the service account needs a Role granting get on configmaps
```

## Options

| Option | Default |
|---|---|
| `WithNamespace(ns)` | the pod's own, from the projected service account |
| `WithServer(url)` | `https://kubernetes.default.svc` |
| `WithToken(tok)` | the projected service account token |
| `WithClient(c)` | `http.Client` with a 10s timeout, system trust store |
| `WithServiceAccountDir(dir)` | `/var/run/secrets/kubernetes.io/serviceaccount` |
| `WithContext(ctx)` | a 10s timeout |
| `Optional()` | a missing resource is an error |

**On TLS:** the default client trusts the system pool, which is right in a cluster whose CA is in your image and wrong in one where it is not. Pass a client carrying `ca.crt` from the service account directory if you need it. It is an explicit choice rather than a silent default, because a config source that quietly skips certificate verification is a worse failure than one that will not start.

## Cost

The resource is read **once**, when the source is constructed. cfgkit calls `Lookup` once per bound field, so a source that dialled the API per key would turn a fifty-field configuration into fifty round-trips at boot. Pinned by a test that counts requests.

An unreachable API server is an **error**, never a miss — that difference is a deploy proceeding with an empty password.

## Testing

The suite runs entirely against `httptest`, with no cluster, no kubeconfig and no network. That is a consequence of carrying no dependencies: the whole client is `net/http`, so the whole client can be pointed at a fake.

```sh
task ci
```

Coverage is 98.6%. The two uncovered statements are `http.NewRequestWithContext`'s error return, which cannot fire with a constant method and an already-parsed URL, and an `os.ReadFile` failure on a mounted key that `os.Stat` just succeeded on.
