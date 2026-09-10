# read-nats — NATS JetStream KV

```sh
go run ./read-nats
```

**Runs with no NATS installed** — and unlike every other remote example here, it does *not* use a fake. It starts a real `nats-server` in-process with JetStream enabled and seeds a bucket with the vendor's own client.

## Why a real server rather than a fake

The HTTP-based adapters can be pointed at an `httptest` server because their whole client is `net/http`. NATS speaks **its own wire protocol**, so there is no transport to stand a fake in front of.

That cuts both ways. It is why the adapter takes `nats.go` as a dependency — a hand-rolled client would be a worse copy of one that exists — and it is why "reads a NATS KV bucket" would otherwise be a claim nothing verified.

`nats-server` is a **test-only** dependency of the adapter, and module graph pruning keeps it out of the build list of anything that imports it.

## Why a NATS source at all

A fleet already running NATS has the connection, the credentials and the operational story in place. Standing up Consul or etcd beside it means a second cluster to run, a second credential to rotate, and a second thing to page someone about — all to do the same job.

## Real usage

```go
nats.Bucket("nats://localhost:4222", "app-config")
```

## What it prints

```
nats.internal:4222

FIELD     KEY       VALUE          SOURCE
Host      HOST      nats.internal  nats:app-config
Password  PASSWORD  ••••••         nats:app-config
Port      PORT      4222           nats:app-config
```

## The connection does not stay open

The bucket is read **once**, at construction, and the connection this package opened closes immediately. Nothing after construction needs it, and holding one open for the life of the process to serve a map that never changes is a resource leak with extra steps.

Under `WithKV` — where you hand over a bucket you already have — nothing is dialed **and nothing is closed**. A package that closed a connection it did not open would break the program still using it.

## Two things that are deliberately not failures

- **An empty bucket.** JetStream reports `ErrNoKeysFound` rather than an empty list. Untranslated, every empty bucket would look like a failure — a different thing with a different answer. It is still an error by default; `Optional()` covers it.
- **A key deleted between the list and the read.** The bucket is live and other writers exist. Letting an unrelated deletion crash an unrelated service's boot would be the wrong trade, so the key is skipped.

## Next

- [`read-consul`](../read-consul) · [`read-etcd`](../read-etcd) — the alternatives you avoid running
