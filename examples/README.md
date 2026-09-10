# cfgkit examples

Every example is a **runnable program whose output is pinned by a test**. `task ci` runs them, so an example that stops matching its README fails the build instead of quietly becoming a lie.

**Every one actually runs**, including the twelve that talk to a remote backend. There is no Vault, Consul, etcd, cluster, AWS account, GCP project or Azure subscription to set up — the HTTP-based adapters are pointed at an in-process fake, NATS and kiln use the real thing (a real server, real age encryption), and the AWS ones go through each module's narrow `API` seam.

```sh
go run ./read-file
go test ./...          # every example's output, checked
```

## Start here

| Example | What it answers |
|---|---|
| [`read-file`](read-file) | Load a `.env` file. Read this one first. |
| [`read-environment`](read-environment) | Plain and namespaced variables, and why the prefix is stripped |
| [`default-values`](default-values) | Running on nothing at all |
| [`precedence`](precedence) | Four layers, and how you see which one won |

## Sources — local

| Example | What it answers |
|---|---|
| [`read-json`](read-json) | Bind a JSON document onto nested structs |
| [`read-struct`](read-struct) | A baseline computed at startup, when a `default:` tag cannot hold it |
| [`read-formats`](read-formats) | Seven formats, one struct, identical values — and the tag trap that costs |
| [`read-commandline`](read-commandline) | Flags, stdlib and pflag — and why a flag default must never win |

## Sources — remote

| Example | What it answers |
|---|---|
| [`read-vault`](read-vault) | HashiCorp Vault KV, v1 and v2 |
| [`read-consul`](read-consul) | Consul KV, and two quirks the adapter absorbs |
| [`read-etcd`](read-etcd) | etcd v3 without gRPC, via its HTTP gateway |
| [`read-k8s`](read-k8s) | ConfigMaps and Secrets by API — **or** by projected volume, with no RBAC |
| [`read-nats`](read-nats) | JetStream KV, against a real in-process server |
| [`read-kiln`](read-kiln) | Encrypted secrets you can **commit** |
| [`read-gcpsecrets`](read-gcpsecrets) | Google Secret Manager, no SDK |
| [`read-azkeyvault`](read-azkeyvault) | Azure Key Vault, no SDK |
| [`read-s3`](read-s3) | A config document in an S3 object |
| [`read-parameterstore`](read-parameterstore) | AWS Parameter Store — and why paging is not optional |
| [`read-secretsmanager`](read-secretsmanager) | AWS Secrets Manager, both read modes |
| [`read-appconfig`](read-appconfig) | AWS AppConfig's two-call protocol |

## Auditing and gates

| Example | What it answers |
|---|---|
| [`provenance`](provenance) | Reading the `Result` in code — which fields are still on defaults |
| [`validation`](validation) | Failing in CI instead of at container start, with every problem at once |

## Patterns

| Example | What it answers |
|---|---|
| [`plugin`](plugin) | Configuring a component whose config type the host cannot name |

## Why this is its own module

`examples/go.mod` exists so the root module can keep its promise of **zero third-party dependencies**, which `task deps` enforces. An example demonstrating `format-yaml` or `source-vault` has to import it; if examples lived in the root module, every adapter any example touched would land in the dependency graph of every program that imports cfgkit.

Splitting them costs nothing, because nobody imports the examples.

## The fixtures

[`mock/`](../mock) holds one canonical configuration written in every supported format, with the struct that binds it and the values it must produce. `read-formats` uses it to prove all seven formats agree.

## Conventions every example follows

- **`run(w io.Writer) error`, with `main` as a thin wrapper.** The whole program is then exercisable in a test with a buffer, which is what makes pinned output possible at all.
- **Output is pinned in `example_test.go`**, with a comment saying what would break it.
- **Nothing is non-deterministic.** No temp paths in printed output, no map iteration where order shows, no wall-clock values — an example that cannot be pinned is an example that rots.
- **Comments explain *why*, not *what*.** The code already says what.
- **Surprising behaviour gets a test, not a paragraph** — a `${VAR}` a later override does not rewrite, a flag default that must never win, a multi-word key that binds as zero without a per-format tag.
