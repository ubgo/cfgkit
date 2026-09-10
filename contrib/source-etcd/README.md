# cfgkit/contrib/source-etcd

**Support level: supported** — 97.5% covered, including etcd's range-successor convention and its omitted-empty-value encoding. See [the catalogue](../../docs/catalogue.md#support-levels).

Read an **etcd v3** key prefix as a cfgkit source.

```go
import etcd "github.com/ubgo/cfgkit/contrib/source-etcd"

cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
	etcd.Prefix("app/config"),
	cfgkit.FromEnviron(),          // still wins over the store
))
```

## No dependencies — and for etcd that needs explaining

etcd's native v3 API is **gRPC**, and the official client brings gRPC, protobuf and their transitive tree. This package uses etcd's **HTTP/JSON gateway** instead: the same server, a different door, enabled by default since v3 (`--enable-grpc-gateway`, which defaults to true).

```
require github.com/ubgo/cfgkit v0.0.0
```

That is the entire `go.mod`. For a program that reads its configuration once at boot, a gRPC stack is a great deal of dependency for one range request.

## What this does not do

| Not supported | Why / what instead |
|---|---|
| Watches | needs a stream; pair `cfgkit.Watcher` with your own trigger — see [Reload](../../docs/reload.md) |
| Leases, transactions, elections | gRPC features, not configuration features |
| Failover across endpoints | one request, one endpoint — see below |
| A cluster with the gateway disabled | use `go.etcd.io/etcd/client/v3` and `cfgkit.SourceFunc` |

The gateway being switchable is the one failure unique to this transport, and it looks exactly like a wrong address — so the message names it:

```
etcd app/config: the HTTP gateway is not serving /v3/kv/range
(is the cluster started with --enable-grpc-gateway=false?)
```

## Keys arrive relative to the prefix

```
etcdctl put /app/config/HOST db.internal
etcdctl put /app/config/PORT 9000
```

```go
type Config struct {
	Host string `env:"HOST"`   // no etcd-specific tag
	Port int    `env:"PORT"`
}
```

One struct binds from etcd and from a `.env` file, with no second set of tags. Nested prefixes keep their separator — `/app/db/HOST` under prefix `app` is `db/HOST`, not `DB_HOST` — because a flat source cannot express a tree, and inventing a mapping would guess at something you never stated.

## How a prefix scan actually works

etcd has **no prefix parameter**. A prefix scan is a range from the key to its *successor*: the same bytes with the last one incremented.

```
key       = "app/config/"
range_end = "app/config0"     ← '/' + 1 == '0'
```

Getting this wrong reads one key, or the entire keyspace, and neither failure announces itself — so it has its own test, including the case where a prefix is entirely `0xFF` and has no successor (etcd's convention there is a `range_end` of a single zero byte, meaning "to the end of the keyspace").

## Two etcd encodings, handled

| | |
|---|---|
| Keys and values are **base64** | decoded for you, so values look like any other source's |
| An empty value **omits the field** | the key still exists, so it binds as empty rather than absent — a present empty value beats a default, an absent one does not |

## Authentication

```go
etcd.Prefix("app/config", etcd.WithCredentials("root", "pw"))   // one extra request
etcd.Prefix("app/config", etcd.WithToken(alreadyIssued))        // no extra request
```

`WithCredentials` exchanges the pair for a token at `/v3/auth/authenticate`, then sends it on the range call. That costs a round trip, which is why it is opt-in — a cluster with authentication disabled needs neither option. A rejected login says *authenticating with etcd*, rather than failing later on the read with a message about permissions.

## An empty prefix is an error

etcd answers `200` with **no keys** rather than `404`, so an empty result is the only signal that a prefix holds nothing:

```go
etcd.Prefix("app/overrides", etcd.Optional())
```

Same rule as the other remote sources: you named the prefix, so finding nothing under it is a deployment mistake, and starting on compiled-in defaults would hide it.

## One endpoint, no failover

`$ETCDCTL_ENDPOINTS` may list several; **only the first is used.** This package makes one request and does not fail over, and silently trying the rest would hide a broken first node behind a working second one. Point `WithAddress` at a load balancer, or at the endpoint you mean.

A bare `host:port` is accepted, since that is how endpoints are conventionally written.

## Options

| Option | Default |
|---|---|
| `WithAddress(addr)` | first of `$ETCDCTL_ENDPOINTS`, then `http://127.0.0.1:2379` |
| `WithCredentials(user, pw)` | none |
| `WithToken(tok)` | none |
| `WithClient(c)` | `http.Client` with a 10s timeout |
| `WithContext(ctx)` | a 10s timeout |
| `Optional()` | an empty prefix is an error |

**On TLS:** etcd is usually served with a private CA, so `WithClient` is the option most real deployments need. It is explicit rather than a silent default, because a config source that quietly skips certificate verification is a worse failure than one that will not start.

## Cost

One range request, at construction. cfgkit calls `Lookup` once per bound field, so a source that ranged per key would turn a twenty-field configuration into twenty round-trips at boot. Pinned by a test that counts calls.

An unreachable cluster is an **error**, never a miss — that difference is a deploy proceeding with an empty password.

## Testing

Entirely `httptest` — no etcd, no network, no credentials:

```sh
task ci
```
