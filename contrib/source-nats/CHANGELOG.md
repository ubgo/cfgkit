# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

Nothing released yet. The module is complete and covered, and waits on the core's first tag — it pins `github.com/ubgo/cfgkit`, and a published module cannot depend on an unpublished one.

### Added

- `Bucket(url, bucket, opts...)` — a NATS JetStream key/value bucket as a flat cfgkit source. `url` accepts the comma-separated form NATS itself accepts.
- Implements `cfgkit.KeyLister`, so a key in the bucket that matches no field is reported through `Result.Unknown()` as a probable typo. Listed names are the stripped ones when a prefix is set.
- Options: `WithPrefix`, `WithKV`, `WithOptions`, `Optional`.

### Notes

- **A fleet already running NATS has the store already.** The connection, the credentials and the operational story exist; adding Consul or etcd beside it would mean a second cluster to run, rotate and page on for the same job.
- **It carries a dependency, unlike the six no-SDK sources**, and for the reason the catalogue states: NATS speaks its own wire protocol, so there is no URL to GET and a hand-rolled client would be a worse copy of one that exists. `nats-server` is a test-only dependency for the in-process server the suite runs, and graph pruning keeps it out of an importer's build list.
- **Read once, then the connection closes.** `Lookup` runs per bound field, so a per-key read would turn a fifty-field configuration into fifty round trips at boot, and holding a connection for the life of the process to serve a map that never changes is a resource leak with extra steps. Under `WithKV` nothing is dialed or closed — a package that closed a connection it did not open would break the program still using it.
- **`WithPrefix` strips**, where koanf's provider filters and keeps, matching what `FromPrefixedEnviron` already means in the core. It is a rename rather than an alias, and the filter runs before the read so a bucket serving ten services does not cost every service ten reads at boot.
- **A permission failure is an error, never a miss**, because a source reporting "not found" when it meant "not allowed" would let a deploy proceed with an empty password. Two cases are deliberately not failures: an empty bucket (JetStream reports `ErrNoKeysFound` rather than an empty list, so it is translated; `Optional()` covers it) and a key deleted between the list and the read (the bucket is live and other writers exist — an unrelated deletion must not crash an unrelated service's boot).
- **The tests run against a real server.** The `KV` seam covers this package's own decisions without one, but the dial path cannot be faked at a transport the way the HTTP sources are, so the suite starts nats-server in-process with JetStream enabled and seeds a bucket with the vendor's client. Without that, "reads a NATS KV bucket" would be a claim nothing verified.
