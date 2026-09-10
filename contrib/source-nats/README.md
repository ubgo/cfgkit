# cfgkit/contrib/source-nats

**Support level: supported** — 96.9% covered, and the covering includes a real NATS server: the suite starts one in-process with JetStream enabled, writes a bucket with the vendor's own client, and reads it back. See [the catalogue](../../docs/catalogue.md#support-levels).

Read a NATS **JetStream key/value bucket** as a cfgkit source.

```go
import nats "github.com/ubgo/cfgkit/contrib/source-nats"

cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
	cfgkit.FromFiles(".env"),
	nats.Bucket("nats://localhost:4222", "app-config"),
	cfgkit.FromEnviron(),        // still wins over everything
))
```

## Why a NATS source

A fleet that already runs NATS has the connection, the credentials and the operational story in place. Adding Consul or etcd beside it means running a second cluster to do the same job, with a second set of credentials to rotate and a second thing to page someone about. A KV bucket is the store that fleet already has.

## It carries a dependency, on purpose

Six sources in this catalogue take none, because reading a remote secret is one HTTP GET and an SDK is a large price for that. This one takes `github.com/nats-io/nats.go`, for the reason the catalogue states: **use the platform's own mechanism when the credential is a file or a header; use the vendor's library when the hard part is not the transport.**

NATS speaks its own wire protocol. There is no URL to GET, so a hand-rolled client would be a worse copy of one that already exists.

`nats-server` also appears in `go.mod`, as a **test-only** dependency for the in-process server the suite runs. Module graph pruning keeps it out of the build list of anything that imports this package, so it costs you nothing.

## Connections are not held open

The bucket is read **once**, at construction, and the connection this package opened is closed immediately afterwards.

`Lookup` runs once per bound field, so reading per key would turn a fifty-field configuration into fifty round trips at boot — and holding a connection for the life of the process to serve a map that never changes again is a resource leak with extra steps.

If your program already has a connection, hand over the bucket and this package will not dial at all:

```go
nats.Bucket("", "", nats.WithKV(kv))   // your handle, your lifetime
```

Nothing is closed in that case. A package that closed a connection it did not open would break the program still using it.

## Prefixes strip

```go
nats.Bucket(url, "shared", nats.WithPrefix("api."))
// api.PORT in the bucket  →  binds to `env:"PORT"`
```

This differs from koanf's provider, which filters on the prefix but keeps it. Stripping is deliberate: the point of a prefix is that one bucket can serve several services without every struct repeating the namespace in its tags. It is the same thing `cfgkit.FromPrefixedEnviron` does in the core, and the two meaning different things would be worse than either meaning.

Stripping is a rename, not an alias — after `WithPrefix("api.")`, `PORT` resolves and `api.PORT` does not. The filter also runs **before** the read, so a bucket serving ten services does not cost every service ten reads at boot.

## Failures are errors, never misses

A bucket this process may not read gets an **error**, not an empty source. That distinction is the safety argument: a source reporting "not found" when it meant "not allowed" would let a deploy proceed with an empty database password, and nothing downstream could tell the difference.

Two cases are deliberately *not* failures:

- **An empty bucket.** JetStream reports `ErrNoKeysFound` rather than an empty list, so it is translated — untranslated, every empty bucket would look like a failure, which is a different thing with a different answer. It is still an error by default (you named this bucket), and `nats.Optional()` opts out of that and nothing else.
- **A key deleted between the list and the read.** The bucket is live and other writers exist; letting an unrelated deletion crash an unrelated service's boot would be the wrong trade. The key is skipped.

## Options

| Option | Effect |
|---|---|
| `WithPrefix(prefix)` | Keep only prefixed keys, and strip the prefix |
| `WithKV(kv)` | Use an already-open bucket; nothing is dialed or closed |
| `WithOptions(...)` | NATS connect options — credentials, TLS, timeouts |
| `Optional()` | An empty bucket is an empty source, not an error |

`WithOptions` takes the vendor's own `nats.Option` values rather than a re-declared subset, because any subset would be a list this package has to keep chasing.

## What this does not do

| Not supported | What to do instead |
|---|---|
| Watching for changes | pair `cfgkit.Watcher` with `kv.Watch` — see [Reload](../../docs/reload.md) |
| Nested keys from dotted names | keys bind by their literal name; use `WithPrefix` for namespacing |
| Object store, streams, subjects | this is the KV bucket only — `cfgkit.SourceFunc` wraps anything else in five lines |

## Interfaces

- `cfgkit.Source` — the flat lookup.
- `cfgkit.KeyLister` — a key in the bucket that matches no field is reported through `Result.Unknown()` as a probable typo. A configuration bucket's keys were written for this application, which is why it can honestly enumerate. Listed names are the stripped ones, or the warning would point at a spelling no struct can bind.
