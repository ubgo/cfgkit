# cfgkit/contrib/source-consul

**Support level: supported** — 98.9% covered, including Consul's own quirks around valueless and folder keys. See [the catalogue](../../docs/catalogue.md#support-levels).

Read a **Consul KV** prefix as a cfgkit source.

```go
import consul "github.com/ubgo/cfgkit/contrib/source-consul"

cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
	consul.Prefix("app/config"),
	cfgkit.FromEnviron(),          // still wins over the KV store
))
```

With `CONSUL_HTTP_ADDR` and `CONSUL_HTTP_TOKEN` already exported — the same variables the Consul CLI reads — that is the whole configuration. Anyone who can run `consul kv get` can run this.

## It carries no dependencies

```
require github.com/ubgo/cfgkit v0.0.0
```

That is the entire `go.mod`. Reading a KV prefix is a single HTTP GET with one header; the official API client is a large thing to take for that. The whole client here is `net/http` and `encoding/json`.

## Keys arrive relative to the prefix

This is what makes the source usable at all:

```
consul kv put app/config/HOST db.internal
consul kv put app/config/PORT 9000
```

```go
type Config struct {
	Host string `env:"HOST"`   // no consul-specific tag
	Port int    `env:"PORT"`
}
```

The same struct binds from Consul and from a `.env` file, with no second set of tags.

**Nested prefixes keep their separator.** `app/db/HOST` under prefix `app` becomes `db/HOST`, not `DB_HOST`:

```go
DBHost string `env:"db/HOST"`
```

A flat source has no way to express a tree, and inventing a mapping would guess at something you never stated — the same reason cfgkit refuses to derive `HYPERDX_LOGS_SOURCE_ID` from a Go field path. Use a deeper prefix if you want a flatter namespace.

## Two Consul quirks, handled

| | |
|---|---|
| **Folder keys** | a nested tree reports its own prefix as a valueless key. It is skipped — binding it would offer an empty value under an empty name |
| **Valueless keys** | a key with `null` for a value is **present and empty**, not absent. That matters: a present empty value beats a default, an absent one does not |

## An empty prefix is an error

Consul answers `404` for a prefix with nothing under it, so from the outside "empty" and "missing" are the same thing — and either is a deployment mistake when you named the prefix yourself.

```go
consul.Prefix("app/overrides", consul.Optional())
```

Same rule as [`source-k8s`](../source-k8s/README.md) and [`source-vault`](../source-vault/README.md): a service starting on compiled-in defaults because nobody populated the store is exactly the silent failure cfgkit exists to prevent.

## A bare host:port works

`CONSUL_HTTP_ADDR` is conventionally written without a scheme, so this is accepted:

```go
consul.WithAddress("consul.service.consul:8500")
```

Without that, the value parses as a URL whose *scheme* is `consul.service.consul`, and the failure is an error about an unsupported protocol that names nothing a reader would recognise.

## Errors name the fix

| Consul says | This says |
|---|---|
| `403` | `permission denied — the ACL token needs read on this prefix` |
| `404` | `no keys under this prefix (pass consul.Optional() if that is expected)` |

An unreachable agent is an **error**, never a miss. That difference is a deploy proceeding with an empty password.

## What this does not do

| Not supported | What to do instead |
|---|---|
| ACL login flows (auth methods) | authenticate however you like, then pass the token |
| Blocking queries / watches | pair `cfgkit.Watcher` with your own trigger — see [Reload](../../docs/reload.md) |
| Service discovery | this reads KV; discovery is a different problem |
| Writing | `cfgkit` is read-only everywhere |
| Transactions, sessions, locks | those need the API client |

If you need any of them, your program already has `hashicorp/consul/api`, and `cfgkit.SourceFunc` wraps it in five lines.

## Options

| Option | Default |
|---|---|
| `WithAddress(addr)` | `$CONSUL_HTTP_ADDR`, then `http://127.0.0.1:8500` |
| `WithToken(tok)` | `$CONSUL_HTTP_TOKEN` |
| `WithDatacenter(dc)` | the agent's own |
| `WithClient(c)` | `http.Client` with a 10s timeout |
| `WithContext(ctx)` | a 10s timeout |
| `Optional()` | an empty prefix is an error |

**On TLS:** pass a client carrying your CA when Consul is served over HTTPS with a private certificate authority. It is explicit rather than a silent default, because a config source that quietly skips certificate verification is a worse failure than one that will not start.

## Cost

The prefix is read **once**, recursively, when the source is constructed. cfgkit calls `Lookup` once per bound field, so a source that dialled the agent per key would turn a twenty-field configuration into twenty round-trips at boot. Pinned by a test that counts requests.

## Typo detection comes free

`cfgkit.KeyLister` is implemented, so a key under the prefix that matches no field is reported:

```
HSOT (from consul:app/config) matched no field
```

## Testing

Entirely `httptest` — no agent, no network, no token:

```sh
task ci
```
