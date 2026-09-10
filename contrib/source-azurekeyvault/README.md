# cfgkit/contrib/source-azurekeyvault

**Support level: supported** — 98.2% covered, with both Azure endpoints faked. See [the catalogue](../../docs/catalogue.md#support-levels).

Read **Azure Key Vault** secrets as a cfgkit source.

```go
import akv "github.com/ubgo/cfgkit/contrib/source-azurekeyvault"

cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
	cfgkit.DefaultSources(),
	akv.Secret("db-password", "DATABASE_PASSWORD", akv.WithVault("my-vault")),
	akv.Secret("stripe-key", "STRIPE_KEY", akv.WithVault("my-vault")),
))
```

On any Azure compute with a managed identity — VM, scale set, App Service, Container Apps — that is the whole configuration. The token comes from the instance metadata service already there.

## No dependencies

```
require github.com/ubgo/cfgkit v0.0.0
```

Reading a secret is one HTTPS GET with a bearer token, and managed identity hands you that token from a well-known local address — one more GET. The official SDK brings the `azcore` pipeline and the whole identity chain for that.

## Where the line falls

**Managed identity, or a token you supply. Nothing else.**

| Not supported | What to do instead |
|---|---|
| Service-principal secret or certificate | authenticate with `azidentity`, pass the token to `WithToken` |
| Device code, Azure CLI chaining | same |
| Workload identity federation | same |
| Listing, setting, or rotating secrets | `cfgkit` is read-only, and names what it reads |

This is the same line [the catalogue](../../docs/catalogue.md#what-is-deliberately-absent) draws around AWS: each of those flows is its own protocol, and reimplementing them badly means a service that authenticates on a laptop and not in production. Five lines with the SDK you already have:

```go
cred, _ := azidentity.NewDefaultAzureCredential(nil)
tok, _ := cred.GetToken(ctx, policy.TokenRequestOptions{
	Scopes: []string{"https://vault.azure.net/.default"},
})
akv.Secret("db-password", "DATABASE_PASSWORD", akv.WithVault("my-vault"), akv.WithToken(tok.Token))
```

## Vault names and sovereign clouds

```go
akv.WithVault("my-vault")                         // → https://my-vault.vault.azure.net
akv.WithVault("https://my-vault.vault.usgovcloudapi.net")   // used as given
```

A bare name is expanded because that is how vaults are referred to throughout Azure's own tooling. A full URL passes through untouched, which is what a sovereign cloud needs — its vault suffix is not `vault.azure.net`.

## One secret, one key

Key Vault stores one value per secret, so the mapping is stated rather than guessed:

```go
akv.Secret("db-password", "DATABASE_PASSWORD", akv.WithVault("my-vault"))
//          ^ the secret    ^ the config key it answers for
```

Compose several — each is one API call, and each is silent about every key but its own. A source that offered its value to whatever asked first would fill the first field it met.

## Identities

```go
akv.Secret(..., akv.WithClientID("11111111-2222-..."))   // a user-assigned identity
```

The default asks for the **system-assigned** identity. A resource with several user-assigned identities and no system-assigned one cannot pick for itself, and the metadata service answers `400` rather than guessing — so that failure names this option:

```
the metadata service refused the token request (400 Bad Request) — if this
resource has more than one user-assigned identity, name it with
azurekeyvault.WithClientID
```

## Errors name the fix

| Azure says | This says |
|---|---|
| `403` | `the identity needs the Get secret permission, through an access policy or the Key Vault Secrets User role` |
| `401` | `the token was rejected. A token for the wrong audience looks like this; it must be issued for https://vault.azure.net` |
| `404` | `not found in <vault> (pass azurekeyvault.Optional() if it is genuinely optional)` |

Two of those messages exist because the underlying failure is genuinely confusing. Key Vault has **two permission systems** — access policies and RBAC — and a reader who knows only one will look in the wrong place. And a token issued for the **wrong audience** is syntactically valid, so the vault refuses it with a `401` that reads as a permissions problem rather than a token one.

## Versions

The current version by default. Pin one for anything whose rotation should be a deliberate deploy:

```go
akv.Secret("db-password", "DATABASE_PASSWORD", akv.WithVault("v"), akv.WithVersion("abc123"))
```

Key Vault addresses a version as a **path segment**, not a query parameter — `/secrets/name/version`.

## Options

| Option | Default |
|---|---|
| `WithVault(v)` | none — required |
| `WithVersion(v)` | the current version |
| `WithToken(t)` | the instance metadata service |
| `WithClientID(id)` | the system-assigned identity |
| `WithClient(c)` | `http.Client` with a 10s timeout |
| `WithContext(ctx)` | a 10s timeout |
| `WithIMDSEndpoint(url)` | the link-local metadata address |
| `Optional()` | a missing secret is an error |

`WithIMDSEndpoint` is a real option as well as a test seam: App Service and Container Apps publish their identity endpoint on a different address as `IDENTITY_ENDPOINT`.

## Cost

One API call per secret, at construction. Every read is also an Azure Monitor entry and a billed operation, so a per-key read would bill and log once per bound field. Pinned by a test that counts calls.

An unreachable vault is an **error**, never a miss — that difference is a deploy proceeding with an empty password.

## Testing

Both endpoints are faked, so the suite needs no Azure, no credentials and no network:

```sh
task ci
```

Both API versions — the vault's and the metadata service's — are **pinned** rather than left to default, since an unpinned version means a future default could change the reply shape under a running fleet.
