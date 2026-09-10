# read-consul — Consul KV

```sh
go run ./read-consul
```

**Runs with no Consul installed.** The adapter is `net/http` and `encoding/json`, so it can be pointed at a fake — the same property that keeps it dependency-free.

## Real usage

```go
consul.Prefix("app/checkout/")
```

Address from `CONSUL_HTTP_ADDR`, token from `CONSUL_HTTP_TOKEN`. A bare `host:port` is accepted because that is how `CONSUL_HTTP_ADDR` is conventionally written.

## What it prints

```
consul.internal:7000

FIELD     KEY       VALUE            SOURCE
Host      HOST      consul.internal  consul:app/checkout
Password  PASSWORD  ••••••           consul:app/checkout
Port      PORT      7000             consul:app/checkout
```

## Keys arrive relative to the prefix

The store holds `app/checkout/HOST`; the struct says `env:"HOST"`. Trimming is what lets **one struct bind from Consul and from a `.env` file** — without it, every field tag would have to embed the deployment's namespace, and moving between environments would mean editing the struct.

Nested keys keep their separator rather than being guessed into a nested struct, because inventing that mapping would assume something the store never said.

## Two Consul quirks the adapter absorbs

- **Folder keys.** Consul creates a valueless entry for the prefix itself. Binding it would produce a field named `""`, so it is skipped — pinned by a test, because the load must still succeed rather than report a phantom key.
- **Base64 values.** Everything is base64 on the wire, so a corrupt value names the **key** and never echoes the bytes — an error message is an easy way for a secret to reach a log aggregator.

## Next

- [`read-etcd`](../read-etcd) — the same shape, different store
- [`read-vault`](../read-vault) — secrets rather than plain KV
