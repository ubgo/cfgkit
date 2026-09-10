# cfgkit/contrib/source-ssm

**Support level: supported** — 98.1% covered, with the API faked. See [the catalogue](../../docs/catalogue.md#support-levels).

Read **AWS Systems Manager Parameter Store** parameters as a cfgkit source.

```go
import ssm "github.com/ubgo/cfgkit/contrib/source-ssm"

cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
	ssm.Path("/app/config"),
	cfgkit.FromEnviron(),          // still wins
))
```

## This one takes a real dependency

Six remote sources in this catalogue — [k8s](../source-k8s/README.md), [vault](../source-vault/README.md), [consul](../source-consul/README.md), [etcd](../source-etcd/README.md), [GCP](../source-gcpsecrets/README.md), [Azure](../source-azurekeyvault/README.md) — reach their service with nothing but `net/http`, because on those platforms a credential is a file or a header.

**AWS is different in kind.** Signing is straightforward; *credential resolution* is not — environment variables, the shared config file, IMDS, IRSA web identity, SSO, and assume-role chains, each changing independently of this package. A worse reimplementation produces a service that authenticates on a developer's laptop and not in production, which is the failure hardest to catch before it matters.

So `aws-sdk-go-v2` does the part it is good at. That is exactly what `contrib/` exists to make safe: a program that never reads Parameter Store never compiles this package and never sees the SDK in its `go.sum`.

## Names arrive relative to the path

```
/app/config/HOST → HOST
/app/config/PORT → PORT
```

One struct binds from Parameter Store and from a `.env` file with no second set of tags. A nested path keeps its separator: `/app/config/db/HOST` becomes `db/HOST`, addressable as `env:"db/HOST"`.

## Paging is not optional

`GetParametersByPath` returns **at most ten parameters per call.** A configuration of eleven silently loses one without a paging loop.

It is the easiest mistake to make against this API, and the worst kind: it works during development and breaks the day someone adds an eleventh parameter. Pinned by a test that serves three pages and checks all three were read.

## Decryption is on by default

A `SecureString` that arrives as ciphertext is **not a configuration value**, and silently binding the encrypted blob is the quiet failure cfgkit exists to prevent.

```go
ssm.Path("/app/config", ssm.WithoutDecryption())   // needs a reason
```

Turning it off usually means the role deliberately lacks `kms:Decrypt`.

## StringList needs no special handling

It arrives comma-separated, which is exactly what cfgkit's slice decoder expects:

```go
Hosts []string `env:"HOSTS"`
```

A branch that split and rejoined it would only add a way to be wrong.

## An empty path is an error

```go
ssm.Path("/app/overrides", ssm.Optional())
```

Same rule as every other remote source: you named the path, so finding nothing under it is a deployment mistake.

## Options

| Option | Default |
|---|---|
| `WithClient(c)` | built from the SDK's credential chain |
| `WithContext(ctx)` | `context.Background()` |
| `WithoutDecryption()` | decryption **on** |
| `WithoutRecursion()` | recursion **on** |
| `Optional()` | an empty path is an error |

## Testing

`API` is an interface, so the suite implements it directly — no AWS, no credentials, no network:

```sh
task ci
```

Taking the SDK as a dependency does not require taking its test harness too.
