# read-vault — HashiCorp Vault KV

```sh
go run ./read-vault
```

**Runs with no Vault installed.** The example starts a fake one in-process — possible for the same reason the adapter has [no dependencies](../../contrib/source-vault/README.md): the whole client is `net/http`, so the whole client can be pointed somewhere else. An example that only compiles is a code listing.

## Real usage is one line

```go
vault.Secret("app/config")
```

Address and token are discovered the way the Vault CLI discovers them — `VAULT_ADDR`, `VAULT_TOKEN`, `~/.vault-token`. A developer who can already run `vault kv get` needs no extra configuration. The `WithAddress`/`WithToken` options in the example exist only to point it at its fake.

## What it prints

```
vault.internal:9000

FIELD     KEY       VALUE           SOURCE
Host      HOST      vault.internal  vault:secret/app/config
Password  PASSWORD  ••••••          vault:secret/app/config
Port      PORT      9000            vault:secret/app/config
```

The `SOURCE` column names **which secret**, not just "vault". A program reading three secrets needs to tell them apart the moment a rotation goes wrong.

## KV v2 versus v1

The fake speaks the **v2** reply shape, where values sit one level deeper beside their metadata:

```json
{"data": {"data": {"HOST": "..."}, "metadata": {"version": 3}}}
```

v1 is flatter and its path has no `data` segment. Both differences have to be right together or the read 404s, which is the most common Vault integration bug — so the adapter reports a version mismatch **by name** rather than letting it look like a missing secret. `WithKVVersion` pins it when discovery is not wanted.

## Next

- [`read-consul`](../read-consul) · [`read-etcd`](../read-etcd) — the other dependency-free KV stores
- [`read-kiln`](../read-kiln) — secrets that live in the repository instead
