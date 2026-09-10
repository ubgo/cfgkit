# cfgkit documentation

`cfgkit` turns layered sources into a validated, typed Go struct. Defaults live in Go and are compiled into the binary, so **a complete valid configuration exists with zero input** — no config file, no environment variable, no tool installed. Every source above that is an override of a value that already exists.

## Guides

| Guide | Covers |
|---|---|
| [Getting started](getting-started.md) | install, the first `Load`, the pipeline, `--json`-style output, errors |
| [**API reference**](api.md) | **every exported function and type, with when to reach for it** |
| [Sources](sources.md) | the two source kinds, every built-in constructor, precedence, writing your own |
| [Tags](tags.md) | every struct tag and every `env:` option, with the rules each one carries |
| [Types](types.md) | the supported type set, maps, the escape hatch, decode failures |
| [Validation](validation.md) | `Defaults`, `Derive`, `Validate`, all ten rule helpers, conditional rules, third-party validators |
| [Provenance and diagnostics](provenance.md) | `Explain`, `Result.JSON`, secret masking, `Check`, `Document` |
| [Capabilities](capabilities.md) | `Transformer`, `Decoder`, `Observer` — the firewall, and the gotchas of each |
| [Modes](modes.md) | dev/test/prod, the strictness inversion, weak-secret refusal |
| [Recipes](recipes.md) | end-to-end workflows: Pkl at build time, Docker secrets, CI gates, multi-environment |
| [Reload](reload.md) | `Watcher[T]` — safe reload with no mutex, and the one rule you must follow |
| [Comparison](comparison.md) | the full matrix against six alternatives, and when each of them is the better choice |
| [Catalogue](catalogue.md) | every adapter module, its dependencies and its support level |
| [Writing an adapter](writing-adapters.md) | the contrib module contract and the shared conformance suite |

## contrib modules

Adapters that carry a dependency live in their own module, so a consumer compiles only what it imports.

| Module | Adds | Guide |
|---|---|---|
| `contrib/format-hcl` | HCL documents and files | [README](../contrib/format-hcl/README.md) |
| `contrib/format-ini` | INI documents and files — **flat** | [README](../contrib/format-ini/README.md) |
| `contrib/format-properties` | Java `.properties` — **flat** | [README](../contrib/format-properties/README.md) |
| `contrib/format-toml` | TOML documents and files | [README](../contrib/format-toml/README.md) |
| `contrib/format-yaml` | YAML documents and files | [README](../contrib/format-yaml/README.md) |
| `contrib/flags-pflag` | cobra / pflag flag sets | [README](../contrib/flags-pflag/README.md) |
| `contrib/source-azurekeyvault` | Azure Key Vault — **no dependencies** | [README](../contrib/source-azurekeyvault/README.md) |
| `contrib/source-consul` | Consul KV prefixes — **no dependencies** | [README](../contrib/source-consul/README.md) |
| `contrib/source-etcd` | etcd v3 prefixes, via its HTTP gateway — **no dependencies** | [README](../contrib/source-etcd/README.md) |
| `contrib/source-gcpsecrets` | Google Secret Manager — **no dependencies** | [README](../contrib/source-gcpsecrets/README.md) |
| `contrib/source-k8s` | Kubernetes ConfigMaps and Secrets — **no dependencies** | [README](../contrib/source-k8s/README.md) |
| `contrib/source-vault` | HashiCorp Vault KV secrets — **no dependencies** | [README](../contrib/source-vault/README.md) |
| `contrib/source-appconfig` | AWS AppConfig profiles | [README](../contrib/source-appconfig/README.md) |
| `contrib/source-s3` | a config document in an S3 object | [README](../contrib/source-s3/README.md) |
| `contrib/source-secretsmanager` | AWS Secrets Manager | [README](../contrib/source-secretsmanager/README.md) |
| `contrib/source-ssm` | AWS Parameter Store | [README](../contrib/source-ssm/README.md) |
| `contrib/source-kiln` | kiln-encrypted env files — the one source whose file is **safe to commit** | [README](../contrib/source-kiln/README.md) |
| `contrib/source-nats` | NATS JetStream key/value buckets | [README](../contrib/source-nats/README.md) |

## Design promises (hold everywhere)

- **Zero input works.** `Load` with no sources and an empty environment returns a valid configuration, provided `Defaults` is complete.
- **Precedence is positional.** The value of a field equals the last source that claimed its key. Nothing is re-ordered internally.
- **`Load` never writes to `os.Environ`,** with exactly one opt-in exception: a field tagged `unset` removes its own key.
- **A flag default never wins.** Only flags the user actually typed count.
- **Secrets never appear in output.** A `secret:"true"` value is absent from `Explain`, `JSON` and error text unless `Reveal()` is passed.
- **All errors, not the first.** N independent problems report N errors.
- **No global state.** `Load` returns a value the caller owns. There is nowhere in the package to hide state.

## Every snippet here is tested

Every command output and code sample in these guides comes from a runnable example in [`example_test.go`](../example_test.go) or [`example_guide_test.go`](../example_guide_test.go), whose `// Output:` blocks `go test` verifies. If behaviour changes, the example fails and both get fixed together — so nothing on these pages can quietly become fiction.

## Runnable examples

| Example | Shows |
|---|---|
| [`examples/plugin`](../examples/plugin/README.md) | a component configuring itself when the host cannot name its type — and why there is no `Sub()` |
