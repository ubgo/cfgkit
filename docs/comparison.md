# Comparison

Stated plainly so it can be argued with. Every row was checked against each library's own current documentation and issue tracker, not from memory — where a claim rests on a specific issue or FAQ entry, it is linked.

Libraries surveyed: [viper](https://github.com/spf13/viper), [koanf](https://github.com/knadh/koanf), [qor5/confx](https://github.com/qor5/confx), [sethvargo/go-envconfig](https://github.com/sethvargo/go-envconfig), [caarlos0/env](https://github.com/caarlos0/env), [ilyakaznacheev/cleanenv](https://github.com/ilyakaznacheev/cleanenv).

**A dash means not checked, not absent.** Three rows — unknown-key reporting, optional sections and map types — were added after the original survey, and only the cells backed by a direct reading of that library's documentation carry a mark. Filling the rest in from plausibility would make the table look complete and be worth less.

## The full matrix

| | **cfgkit** | viper | koanf | qor5/confx | go-envconfig | caarlos0/env | cleanenv |
|---|---|---|---|---|---|---|---|
| Valid config with zero input | ✅ enforced by an invariant | ❌ | ❌ | ⚠️ a default struct or file | ⚠️ tag defaults | ⚠️ tag defaults | ⚠️ tag defaults |
| **Provenance — which source won** | ✅ `Explain` per field | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| **Secret masking** | ✅ structural | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| **Validate without booting** | ✅ `Check` | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| **Generate the contract file** | ✅ `Document` | ❌ | ❌ | ⚠️ a hand-kept embedded file | ❌ | ❌ | ⚠️ help text only |
| **Reload without a caller-side mutex** | ✅ `Watcher` | ❌ caller adds a mutex | ❌ caller adds a mutex | ❌ | ❌ | ❌ | ⚠️ `env-upd` |
| Unknown-key reporting | ✅ `Unknown()`, advisory | ❌ | ❌ | — | — | — | — |
| Cross-field / conditional rules | ✅ typed Go, collected errors | ❌ | ❌ | ✅ validator tags | ❌ | ❌ | ❌ |
| Catalogue rules (email, URL, …) | ✅ via the `Validator` seam, caller's dependency | ❌ | ❌ | ✅ built in, always a dependency | ❌ | ❌ | ❌ |
| Value held in a file | ✅ `,file` | ❌ | ⚠️ via a provider | ❌ | ❌ | ✅ `,file` | ❌ |
| Unset after read | ✅ `,unset` | ❌ | ❌ | ❌ | ❌ | ✅ `,unset` | ❌ |
| `required` vs `notempty` | ✅ both | ❌ | ❌ | ✅ via validator | ⚠️ `required` | ✅ both | ⚠️ `env-required` |
| Former key name | ✅ `was:` | ⚠️ aliases | ❌ | ❌ | ❌ | ❌ | ❌ |
| Optional sections (`*Struct` stays nil) | ✅ `,init` to override | — | — | — | ⚠️ eager, `noinit` to opt out | ✅ `,init` | — |
| Map types | ✅ `delim` + `kvdelim` | ✅ untyped map | ✅ untyped map | — | ✅ | ✅ | — |
| Global state | none | package singleton | none | none | none | none | none |
| Third-party dependencies | **one, ours, stdlib-only** | many | few | several | one | one | few |
| Pluggable sources | ✅ two interfaces | ⚠️ fixed set | ✅ providers | ❌ fixed set | ⚠️ `Lookuper` | ❌ | ❌ |
| `.env` fidelity | ✅ full parser: multiline, quoting, `${}` | ⚠️ basic | ⚠️ basic | ❌ | ❌ | ❌ | ⚠️ basic |
| CLI flags as a source | ✅ stdlib in core, pflag in a module | ⚠️ pflag, but flag defaults leak | ✅ providers, needs a back-reference | ✅ **generates** the flags | ❌ | ❌ | ⚠️ via `flag` |
| Remote KV built in | ❌ own module — `source-consul` and `source-etcd` ship them | ✅ | ✅ | ❌ | ❌ | ❌ | ❌ |
| AWS Parameter Store / Secrets Manager / S3 / AppConfig | ✅ four modules | ❌ | ⚠️ some | ❌ | ❌ | ❌ | ❌ |
| Kubernetes ConfigMap / Secret | ✅ `contrib/source-k8s`, no dependencies | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| Google Secret Manager | ✅ `contrib/source-gcpsecrets`, no dependencies | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| Azure Key Vault | ✅ `contrib/source-azurekeyvault`, no dependencies | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| Vault, without its SDK | ✅ `contrib/source-vault`, no dependencies | ⚠️ via SDK | ⚠️ via SDK | ❌ | ❌ | ❌ | ❌ |
| NATS JetStream KV | ✅ `contrib/source-nats` | ❌ | ✅ | ❌ | ❌ | ❌ | ❌ |
| Encrypted config **committed to the repo** | ✅ `contrib/source-kiln` | ❌ | ✅ | ❌ | ❌ | ❌ | ❌ |

## Five rows are genuinely novel

Provenance, secret masking, validate-without-boot, contract generation, and mutex-free reload. **No library surveyed has any of them.**

A sixth is narrower but worth naming: neither viper nor koanf has a **Kubernetes** provider, and clusters are where configuration most often lives somewhere other than a file.

The source rows below the novel five are parity rows, not advantages — koanf reached most of those backends first, and two of them (NATS, kiln) exist here because koanf had them. What differs is the packaging: koanf's providers are separate modules too, but its own dependency count is not the point of comparison — the point is that a program here reading only a `.env` file compiles none of it.

Everything else in the table exists somewhere. The claim is not that each feature is unique — it is that nobody ships them together, and nobody ships any of them on top of a real `.env` parser.

## What is honestly not novel

**Conditional validation.** `qor5/confx` has it through `go-playground/validator` tags, including a `skip_nested_unless` extension for exactly the discriminated-union case in [Validation](validation.md). The difference is only *where the dependency lives*: confx embeds the validator for everyone, cfgkit exposes a seam so the caller decides. That is a smaller claim than it might look, and it is the honest one.

**Map types, `,file`, `,unset`, `,init`.** All present in `caarlos0/env` or `go-envconfig`, and cfgkit's spellings deliberately match theirs so a struct moved from either keeps working.

## One deliberate loss

**Remote key/value stores are not built in.** viper and koanf both ship etcd and Consul support in the box; cfgkit does not, and will not.

A built-in Vault client puts the Vault SDK in every consumer's `go.sum`, including the ones that only ever read a `.env` file. Remote backends belong in their own module behind the `Source` interface — which costs an import and buys the one-dependency guarantee. See [Writing an adapter](writing-adapters.md).

## The reload row, in detail

This is the row most likely to be disputed, so here is the evidence.

**Viper** watches with `fsnotify` and rewrites its internal map. Its own FAQ:

> "No, you will need to synchronize access to the viper yourself (for example by using the `sync` package). Concurrent reads and writes can cause a panic."

Three long-standing issues track exactly this: a data race in `WatchConfig()`/`ReadInConfig()` (#174), `UnmarshalKey` unsafe for concurrent access (#482), and "concurrency-safe way to use `viper.WatchConfig()`?" (#378). Separately, a struct produced by `Unmarshal` does **not** update on reload — the caller must re-unmarshal inside the callback.

**Koanf** exposes `Watch(cb)` on several providers, and its documentation gives correct advice — *"throw away the old config and load a fresh copy"* — but supplies no mechanism to publish that copy safely:

> "This is not goroutine safe if there are concurrent `*Get()` calls happening on the koanf object while it is doing a `Load()`. Such scenarios will need mutex locking."

A fork of koanf exists whose entire purpose is to add one `RWMutex` around every public method.

**cfgkit** publishes a new immutable generation behind an atomic pointer and validates before publishing. `TestConcurrentReadsUnderContinuousReload` runs eight unsynchronised readers against 200 reloads under `-race`, in the standard gate. Details and the one rule you must follow: [Reload](reload.md).

## Where each alternative is the better choice

Ending a comparison without this would make it marketing rather than an assessment.

| Choose | When |
|---|---|
| **viper** | you want remote KV, many formats, and live watching in the box, and a package-level singleton suits your program |
| **koanf** | you want viper's breadth with a cleaner provider model and fewer dependencies, and you are content to manage concurrency yourself |
| **caarlos0/env** or **go-envconfig** | environment variables are your *only* source and you want the smallest possible surface — both are excellent at exactly that |
| **qor5/confx** | you want `go-playground/validator`'s rule catalogue and generated CLI flags, and the dependency is not a concern |
| **cleanenv** | you want a tiny library with help-text generation and nothing else |
| **cfgkit** | you have layered sources and need to answer *which one won*, you want a bad config to fail CI instead of a container, and you want the dependency count to stay at one |

## Method

Every ✅ in the cfgkit column corresponds to a passing test or a verified example in this repository — see [the invariants list](../README.md#testing). Columns for other libraries were read from their current documentation, README and issue trackers in September 2026.

If a row is wrong, it is a bug in this page. The libraries compared here are good, actively maintained, and solve problems cfgkit does not attempt.
