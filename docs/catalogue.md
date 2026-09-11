# The catalogue

Every adapter that carries a dependency, or talks to something outside the process, lives in its own module. Import one and you compile one; import none and `cfgkit` still has exactly one dependency.

```go
import (
	yamlsrc "github.com/ubgo/cfgkit/contrib/format-yaml"
	k8s     "github.com/ubgo/cfgkit/contrib/source-k8s"
)
```

## What ships today

| Module | Adds | Dependencies | Support |
|---|---|---|---|
| [`format-hcl`](../contrib/format-hcl/README.md) | HCL documents and files | `hashicorp/hcl/v2` | **supported** |
| [`format-ini`](../contrib/format-ini/README.md) | INI documents and files (flat) | `gopkg.in/ini.v1` | **supported** |
| [`format-properties`](../contrib/format-properties/README.md) | Java `.properties` (flat) | `magiconair/properties` | **supported** |
| [`format-toml`](../contrib/format-toml/README.md) | TOML documents and files | `BurntSushi/toml` | **supported** |
| [`format-yaml`](../contrib/format-yaml/README.md) | YAML documents and files | `gopkg.in/yaml.v3` | **supported** |
| [`flags-pflag`](../contrib/flags-pflag/README.md) | cobra / pflag flag sets | `spf13/pflag` | **supported** |
| [`cli-cobra`](../contrib/cli-cobra/README.md) | a cobra `config` command — `check`, `explain`, `document` | `spf13/cobra` | **supported** |
| [`source-azurekeyvault`](../contrib/source-azurekeyvault/README.md) | Azure Key Vault | **none** | **supported** |
| [`source-consul`](../contrib/source-consul/README.md) | Consul KV prefixes | **none** | **supported** |
| [`source-etcd`](../contrib/source-etcd/README.md) | etcd v3 prefixes | **none** | **supported** |
| [`source-gcpsecrets`](../contrib/source-gcpsecrets/README.md) | Google Secret Manager | **none** | **supported** |
| [`source-k8s`](../contrib/source-k8s/README.md) | Kubernetes ConfigMaps and Secrets, by API **or mounted volume** | **none** | **supported** |
| [`source-vault`](../contrib/source-vault/README.md) | HashiCorp Vault KV secrets | **none** | **supported** |
| [`source-appconfig`](../contrib/source-appconfig/README.md) | AWS AppConfig profiles | `aws-sdk-go-v2` | **supported** |
| [`source-s3`](../contrib/source-s3/README.md) | a config document in an S3 object | `aws-sdk-go-v2` | **supported** |
| [`source-secretsmanager`](../contrib/source-secretsmanager/README.md) | AWS Secrets Manager | `aws-sdk-go-v2` | **supported** |
| [`source-ssm`](../contrib/source-ssm/README.md) | AWS Parameter Store | `aws-sdk-go-v2` | **supported** |
| [`source-kiln`](../contrib/source-kiln/README.md) | kiln-encrypted env files — the one source whose file is **safe to commit** | `thunderbottom/kiln` | **supported** |
| [`source-nats`](../contrib/source-nats/README.md) | NATS JetStream key/value buckets | `nats-io/nats.go` | **supported** |

Every format and flags module is at **100.0%** statement coverage. The source modules run from **96.9% to 100.0%** and `cli-cobra` sits at **97.1%**, and the sixteen uncovered statements across all of them are enumerated in the [README's testing section](../README.md#testing) rather than rounded away — they are `http.NewRequestWithContext` error returns that cannot fire on an already-parsed URL, `json.Marshal` errors on values that cannot hold a channel or a cycle, one lazily-built JetStream context, and two marshals of cfgkit's own provenance record. Each was checked to be unreachable, not assumed.

All of them pass the shared conformance suite where it applies, run in the same gate as the core, and are **fuzzed**: the format modules against arbitrary document bytes, the HTTP sources against arbitrary response bodies, and the SDK-backed sources against arbitrary payloads through their client seam. `task fuzz:contrib` runs all seventeen targets. The two whose backend cannot be faked at a transport are tested against the real thing instead: `source-nats` starts an in-process NATS server, and `source-kiln` generates an age identity and encrypts a fixture with kiln itself.

## Support levels

A catalogue that grows faster than its maintenance is a catalogue of traps, so each module says which of two things it is:

**Supported** — used in production by the authors, or covered by a test suite thorough enough to stand in for that. Bugs are fixed, and the module moves with the core.

**Best-effort** — complete and tested, but nobody here depends on it yet. It works; it has simply not met a real deployment's edge cases. It becomes supported the moment somebody depends on it and says so.

The distinction is about *evidence*, not effort. A best-effort module is not a lower standard of code — every module in this repository meets the same gate — it is an honest statement about how much reality it has been through.

## Why a separate module per adapter

A subdirectory with its own `go.mod` is a **separate module**, not a subpackage. `go get github.com/ubgo/cfgkit` resolves the root module's requirements only: the YAML and TOML parsers and pflag named in `contrib/*/go.mod` are never fetched, never built, and never appear in your `go.sum`.

So the catalogue can grow to twenty adapters carrying six vendor SDKs between them, and a program importing only `cfgkit` still downloads exactly one dependency.

The cost is that each module pins a version of the root and needs a bump when the root's API changes. `go.work` removes that during development, and `task bump:contrib` does it at release time.

## Six of these have no dependencies at all

`source-k8s`, `source-vault`, `source-consul`, `source-etcd`, `source-gcpsecrets` and `source-azurekeyvault` talk to real remote services and still require nothing but the core. That is deliberate, and it is worth explaining because a Kubernetes or Vault client without its official SDK is unusual.

Reading one named resource is a **single HTTP request with one header**. The official SDKs are large — API machinery, codec layers, credential chains — and each brings a release cadence tied to its own product. For a program that only wants to read its configuration at boot, that is a great deal of dependency for one request.

`source-etcd` is the strongest case: etcd's native API is **gRPC**, so the official client brings gRPC and protobuf with it. The module uses etcd's HTTP/JSON gateway instead — the same server through a different door, enabled by default — and the one thing that can go wrong, a cluster with the gateway switched off, is named in the error rather than left looking like a wrong address.

The price is stated in each module's README rather than discovered later:

| | `source-k8s` | `source-vault` |
|---|---|---|
| Auth | in-cluster service account only | token only |
| Not supported | kubeconfig, exec/OIDC plugins | AppRole, Kubernetes, AWS, JWT login |
| Also not | watching, listing, `binaryData` | leases, dynamic credentials, writing |

Both omissions have the same answer: if you need them, your program already has the SDK, and `cfgkit.SourceFunc` wraps it in five lines. The `Source` interface is the extension point, so a small module never has to be the only way in.

## When a module is *not* the right answer

Adding a module to the catalogue is not the only way to add a source, and often not the best one.

**Reach for `SourceFunc` instead when** the backend is yours, or you already have its client in your program:

```go
secrets, err := vaultClient.KVv2("secret").Get(ctx, "app/config")   // your SDK, your auth
if err != nil {
	return err
}
src := cfgkit.SourceFunc("vault", func(key string) (string, bool, error) {
	v, ok := secrets.Data[key].(string)
	return v, ok, nil
})
```

Five lines, no new module, and the credential chain is whatever your program already does. Full guide: [writing an adapter](writing-adapters.md).

### Flat formats and structured ones

Not every format is a structured source, and the split is not arbitrary:

| | |
|---|---|
| **Structured** — YAML, TOML, HCL, JSON | a type system and a document shape, so they merge onto the struct through their own tags |
| **Flat** — INI, `.properties`, `.env` | every value a string, no nesting to speak of, so they answer key lookups like the environment does |

An INI section becomes part of the key (`database.host`) rather than a nested struct, because inventing a mapping from one level of sections to a Go type would guess at something the file never said.

**A module earns its place when** the adapter is worth writing once for everyone — a wire format, a widely-deployed backend, a set of failure messages that took effort to get right.

A worked example of the judgement: **urfave/cli has no module and will not get one.** Both v2 and v3 expose `IsSet` and `String`, so an adapter would carry no dependency at all — it would add an import path, a version, and a release cadence in exchange for six lines a caller can read at a glance. `contrib/flags-pflag` exists because pflag genuinely is a dependency. The [urfave recipe](recipes.md#urfavecli-without-an-adapter-module) is the answer instead.

## What is deliberately absent

**Remote key/value stores as a built-in.** viper and koanf ship etcd and Consul in the box. A built-in client puts that dependency in every consumer's `go.sum`, including the programs that only ever read a `.env` file — so here they belong in their own module, behind the `Source` interface, costing an import and buying the one-dependency guarantee.

Nothing, now — but *how* the AWS modules arrived is the part worth keeping.

They were held back deliberately while the six no-SDK sources were built, because the approach that works for Kubernetes, Vault, Consul, etcd, GCP and Azure does **not** transfer. On those platforms a credential is a file or a header. On AWS, signing is straightforward but **credential resolution** is not — environment variables, the shared config file, IMDS, IRSA web identity, SSO, and assume-role chains, each changing independently of this package. Reimplementing that would be a worse version of something `aws-sdk-go-v2` already does well, and getting it subtly wrong means a service that authenticates on a laptop and not in production.

So the four AWS modules **take the SDK as a real dependency**, which was always the stated alternative — and is exactly what the `contrib/` split exists to make safe. A program that never reads Parameter Store never compiles it and never sees the SDK in its `go.sum`.

That is the rule the catalogue actually follows: *use the platform's own mechanism when the credential is a file or a header, and the vendor's library when the hard part is not the transport.*

The rule was first written as "…when the credential chain is the hard part", which was true of the only cases it had then. Two later modules made it too narrow, and the wording above is the repair:

- **`source-nats`** has no transport to hand-roll at all. NATS speaks its own wire protocol, so there is no URL to GET.
- **`source-kiln`** has no network. The hard part is age decryption and an access-control model, and cryptography that is subtly wrong does not announce itself.

Six modules take the first path and six take the second, which is what makes it a rule rather than a preference.

## Writing your own

`cfgkittest.RunSourceTests` and `RunStructuredTests` are the shared conformance suite. Passing them means an adapter behaves exactly like the built-ins — precedence, absent values, error-versus-miss — so a caller cannot tell where a value came from except by asking `Explain`.

Full guide, including the module scaffold and the rules each adapter must honour: [writing an adapter](writing-adapters.md).
