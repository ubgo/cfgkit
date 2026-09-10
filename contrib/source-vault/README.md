# cfgkit/contrib/source-vault

**Support level: supported** — every failure mode Vault can return is exercised against a fake server. See [the catalogue](../../docs/catalogue.md#support-levels).

Read a **HashiCorp Vault** KV secret as a cfgkit source.

```go
import vault "github.com/ubgo/cfgkit/contrib/source-vault"

cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
	cfgkit.FromFiles(".env"),
	vault.Secret("app/config"),   // the credentials
	cfgkit.FromEnviron(),         // still wins over everything
))
```

With `VAULT_ADDR` and `VAULT_TOKEN` already exported — or after `vault login` — that is the whole configuration. The token is discovered exactly the way the Vault CLI discovers it, so anyone who can run `vault kv get` can run this.

## It carries no dependencies

```
require github.com/ubgo/cfgkit v0.0.0
```

That is the entire `go.mod`. Reading one secret is a single HTTP GET with one header; the official SDK is a large thing to take for that, and it comes with a release cadence of its own. The whole client here is `net/http` and `encoding/json`.

## What this does not do

Stated in full, because the omissions are what the paragraph above costs:

| Not supported | What to do instead |
|---|---|
| AppRole, Kubernetes, AWS, JWT login | authenticate however you like, then pass the token to `WithToken` |
| Lease renewal | configuration is read once at boot; nothing here holds a lease |
| Dynamic credentials (database, PKI) | those need renewal, so they need the SDK |
| Writing secrets | `cfgkit` is read-only everywhere |
| Listing paths | name the secret you want |

If you need any of them, your program already has the SDK — and `cfgkit.SourceFunc` wraps it in five lines:

```go
sec, err := client.KVv2("secret").Get(ctx, "app/config")
if err != nil {
	return err
}
src := cfgkit.SourceFunc("vault", func(key string) (string, bool, error) {
	v, ok := sec.Data[key].(string)
	return v, ok, nil
})
```

The `Source` interface is the extension point, so this module never has to be the only way in.

## KV v1 and KV v2

Vault's two key/value engines differ in both the request path and the response shape, and Vault will not tell you which one a mount is without a second API call. So it is **stated**, not guessed:

```go
vault.Secret("app/config")                                  // KV v2, the default
vault.Secret("app/config", vault.WithKVVersion(vault.KV1))  // legacy mounts
```

Getting it wrong is the one confusing failure this API invites — a 404 that looks like a missing secret — so both error messages name the version as a likely cause:

```
vault secret/app/config not found (check the mount and KV version, or pass
vault.Optional() if it is genuinely optional)
```

## Values

A KV secret can hold any JSON, while a cfgkit source deals in strings, so the mapping is fixed and worth knowing:

| In Vault | Becomes |
|---|---|
| `"9000"` | `9000` |
| `9000` (a JSON number) | `9000` — **not** `9000.000000` |
| `true` | `true` |
| `null` | `""` |
| `{"a": 1}` or `[1,2]` | the JSON text, so a field with an `UnmarshalText` can still take it |

The number row is the one that bites: `encoding/json` gives every number as a `float64`, and the obvious formatting renders `9000` as `9000.000000`, which then fails to parse as an `int`. Pinned by a test.

## Secrets stay secret

```go
type Config struct {
	Password string `env:"PASSWORD" secret:"true"`
}
```

cfgkit masks the value in `Explain`, in `Result.JSON`, and in error text. That is the point of reading from Vault rather than a file, so it is asserted rather than assumed.

## A missing secret is an error

Same rule as [`source-k8s`](../source-k8s/README.md), and the same reason: you named this path, so its absence is a deployment mistake rather than a normal outcome. A service starting on compiled-in defaults because a secret was never written is exactly the silent failure cfgkit exists to prevent.

```go
vault.Secret("app/optional-overrides", vault.Optional())
```

## Errors name the fix

| Vault says | This says |
|---|---|
| `403` | `permission denied — the token's policy needs read on this path` |
| `503` | `vault is sealed or unavailable` |
| no token anywhere | `set VAULT_TOKEN, run vault login, or pass vault.WithToken` |

An unreachable or sealed Vault is an **error**, never a miss. That difference is a deploy proceeding with an empty password.

## Options

| Option | Default |
|---|---|
| `WithAddress(addr)` | `$VAULT_ADDR`, then `http://127.0.0.1:8200` |
| `WithToken(tok)` | `$VAULT_TOKEN`, then `~/.vault-token` |
| `WithNamespace(ns)` | none (Vault Enterprise) |
| `WithMount(m)` | `secret` |
| `WithKVVersion(v)` | `KV2` |
| `WithClient(c)` | `http.Client` with a 10s timeout |
| `WithHomeDir(dir)` | the OS home directory |
| `WithContext(ctx)` | a 10s timeout |
| `Optional()` | a missing secret is an error |

**On TLS:** pass a client carrying your CA when Vault uses a private certificate authority. It is explicit rather than a silent default, because a secret source that quietly skips certificate verification is a worse failure than one that will not start.

## Cost

The secret is read **once**, when the source is constructed. This matters more here than for a file: every read is also an **audit log entry**, so a source that dialled per key would fill an operator's audit trail at every boot. cfgkit calls `Lookup` once per bound field, and a test counts the requests.

## Typo detection comes free

The source implements `cfgkit.KeyLister`, so a key in the secret matching no field is reported:

```
HSOT (from vault:secret/app/config) matched no field
```

## Testing

Entirely `httptest` — no Vault, no network, no token:

```sh
task ci
```

Coverage is 98.4%. The two uncovered statements are named at the top of `vault_test.go` and are unreachable by construction, not untested.
