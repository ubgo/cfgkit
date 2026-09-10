# cfgkit/contrib/source-gcpsecrets

**Support level: supported** — 97.4% covered, with both Google endpoints faked. See [the catalogue](../../docs/catalogue.md#support-levels).

Read **Google Secret Manager** secrets as a cfgkit source.

```go
import gcpsecrets "github.com/ubgo/cfgkit/contrib/source-gcpsecrets"

cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
	cfgkit.DefaultSources(),
	gcpsecrets.Secret("db-password", "DATABASE_PASSWORD"),
	gcpsecrets.Secret("stripe-key", "STRIPE_KEY"),
))
```

On GCE, GKE, Cloud Run or Cloud Functions that is the whole configuration: the token and the project both come from the metadata server that is already there.

## No dependencies — and why that is possible here but not for AWS

```
require github.com/ubgo/cfgkit v0.0.0
```

Reading a secret version is one HTTPS GET with a bearer token, and workload identity hands you a finished bearer token from a well-known URL — one more GET. The official client brings gRPC, protobuf and the google-api stack for that.

[The catalogue](../../docs/catalogue.md#what-is-deliberately-absent) says AWS Parameter Store stays absent for exactly the reason this module exists. The asymmetry is in the **platforms**, not the effort:

| | Getting a credential |
|---|---|
| **GCP** | one GET to the metadata server returns a finished token |
| **AWS** | resolve a credential chain — environment, shared config, IMDS, IRSA, SSO, assume-role — then sign every request with SigV4 |

Reimplementing AWS's resolution badly means a service that authenticates on a laptop and not in production. GCP's has one honest shape, so it fits in forty lines.

## One secret, one key

Secret Manager stores **one value per secret**, so the mapping is stated rather than guessed:

```go
gcpsecrets.Secret("db-password", "DATABASE_PASSWORD")
//                 ^ the secret     ^ the config key it answers for
```

Compose several — each is one API call, and each is silent about every key but its own. That last part matters: a source that offered its payload to whatever asked first would fill the first field it met.

### A whole `.env` in one secret

A common pattern, and deliberately not built in — this package does not guess at a format. Five lines with the parser you already have:

```go
payload, err := readSecret(ctx, "app-env")   // your call, or this package's
if err != nil {
	return err
}
f, err := dotenv.Read(bytes.NewReader(payload))
if err != nil {
	return err
}
src := cfgkit.FromMap(f)
```

## Authentication

| | |
|---|---|
| Inside GCP | nothing to configure — the metadata server supplies the token |
| Outside GCP | `WithToken(...)`, e.g. from `gcloud auth print-access-token` |
| A service-account JSON key | **not supported** — signing a JWT with an RSA key is where this stops being a small package. Use the official client and `cfgkit.SourceFunc` |

Running outside GCP without a token gives a message that says what the absence *means*, rather than a connection failure to a link-local address:

```
no credentials: the metadata server is unreachable, so this is not running with
workload identity — pass gcpsecrets.WithToken outside GCP
```

## The project

Resolved in order: `WithProject`, then `$GOOGLE_CLOUD_PROJECT`, then the metadata server. Getting it wrong reads the right secret name from the wrong project — a `404` that looks like a missing secret — so all three are tested.

## Versions

`latest` by default. Pin one for anything whose rotation should be a deliberate deploy rather than a value that changes under a running fleet:

```go
gcpsecrets.Secret("db-password", "DATABASE_PASSWORD", gcpsecrets.WithVersion("7"))
```

## Errors name the fix

| Google says | This says |
|---|---|
| `403` | `permission denied — the service account needs roles/secretmanager.secretAccessor on it` |
| `404` | `not found in project "x" (pass gcpsecrets.Optional() if it is genuinely optional)` |
| `401` | `unauthorized — the token was rejected` |

A missing secret is an **error** by default, like every other remote source here: you named it, so its absence is a deployment mistake. An unreachable API is an error too, never a miss — that difference is a deploy proceeding with an empty password.

## Options

| Option | Default |
|---|---|
| `WithProject(p)` | `$GOOGLE_CLOUD_PROJECT`, then the metadata server |
| `WithVersion(v)` | `latest` |
| `WithToken(t)` | the metadata server |
| `WithClient(c)` | `http.Client` with a 10s timeout |
| `WithContext(ctx)` | a 10s timeout |
| `WithEndpoints(api, md)` | Google's, and the link-local metadata address |
| `Optional()` | a missing secret is an error |

`WithEndpoints` exists for Private Service Connect and a few regulated environments — and it is what lets this suite run without Google.

## Cost

One API call per secret, at construction. Every access is also billed and audit-logged, so a per-key read would bill and log once per bound field. Pinned by a test that counts calls.

## Testing

Both endpoints are faked, so the suite needs no Google, no credentials and no network:

```sh
task ci
```
