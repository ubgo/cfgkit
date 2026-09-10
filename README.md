<h1 align="center">cfgkit</h1>
<p align="center"><strong>Typed configuration from layered sources, for Go.</strong></p>
<p align="center">Defaults compiled in, so a valid configuration exists with zero input — and every value can tell you where it came from.</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/ubgo/cfgkit"><img src="https://pkg.go.dev/badge/github.com/ubgo/cfgkit.svg" alt="Go Reference on pkg.go.dev"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-2ea44f" alt="License: Apache-2.0"></a>
  <img src="https://img.shields.io/badge/dependencies-1-2ea44f" alt="One dependency: ubgo/dotenv">
  <img src="https://img.shields.io/badge/coverage-100%25%20core%20·%20≥96.9%25%20contrib-2ea44f" alt="100% statement coverage on the core, at least 96.9% on every contrib module">
  <img src="https://img.shields.io/badge/platforms-linux%20%7C%20macos%20%7C%20windows-2ea44f" alt="Built and tested on linux, macOS and Windows">
</p>

A Go configuration library that binds environment variables, `.env` files, JSON, YAML, command-line flags, and any secret store you care to add into one validated, typed struct. Defaults live in Go and are compiled into the binary, so **a complete valid configuration exists with zero input** — no config file, no environment variable, no tool installed. Every source above that is an override of a value that already exists. It reports which source set each field, masks secrets structurally, validates without booting your application, and generates the `.env.example` contract from your struct.

```sh
go get github.com/ubgo/cfgkit                              # the library
go get github.com/ubgo/cfgkit/contrib/source-vault         # any adapter you want, separately
```

One dependency: [`github.com/ubgo/dotenv`](https://github.com/ubgo/dotenv), which is itself stdlib-only. **Minimum Go: 1.22** — checked by a CI job that builds and tests the core with a real 1.22 toolchain, not asserted here. Adapter modules under `contrib/` are floored by the SDKs they wrap and may need more.

**Three things ship here, and each installs on its own:**

| | What it is | Get it | Docs |
|---|---|---|---|
| **Library** | the loader, binder, validation, provenance and contract generation | `go get github.com/ubgo/cfgkit` | [docs/](docs/README.md) |
| **18 adapter modules** | YAML, TOML, HCL, INI, `.properties`, pflag, Vault, Consul, etcd, Kubernetes, NATS, kiln, GCP, Azure, and four AWS services — **each its own Go module**, so you compile only what you import | `go get github.com/ubgo/cfgkit/contrib/<name>` | [catalogue](docs/catalogue.md) |
| **22 runnable examples** | every source and pattern, each with a README and output pinned by a test | `go run ./examples/read-file` | [examples/](examples/README.md) |

**Full documentation:** [docs/](docs/README.md) — [getting started](docs/getting-started.md) · [API reference](docs/api.md) · [sources](docs/sources.md) · [tags](docs/tags.md) · [types](docs/types.md) · [validation](docs/validation.md) · [provenance](docs/provenance.md) · [modes](docs/modes.md) · [capabilities](docs/capabilities.md) · [recipes](docs/recipes.md) · [writing an adapter](docs/writing-adapters.md)

**Contents:** [Quick start](#quick-start) · [What it does that others do not](#what-it-does-that-others-do-not) · [Sources](#sources) · [Keys are declared, never guessed](#keys-are-declared-never-guessed) · [Tags](#tags) · [Types](#types) · [Hooks](#hooks) · [Modes](#modes) · [contrib](#contrib) · [Gotchas](#gotchas) · [Testing](#testing) · [FAQ](#faq)

## Quick start

```go
type Config struct {
	Port     int           `env:"PORT"      default:"8080" doc:"HTTP listen port"`
	Timeout  time.Duration `env:"TIMEOUT"   default:"15s"`
	DBURL    string        `env:"DATABASE_URL,required"    doc:"Postgres connection string"`
	APIKey   string        `env:"API_KEY"   secret:"true"  doc:"Upstream API key"`
}

cfg, res, err := cfgkit.Load[Config](
	cfgkit.WithSources(
		cfgkit.FromFiles(".env", ".env.local"),
		cfgkit.FromEnviron(),   // last = highest precedence
	),
)
```

`cfg` is typed. `res` knows where every value came from. `err` reports *every* problem, not the first.

## What it does that others do not

| | cfgkit | viper | koanf | confx |
|---|---|---|---|---|
| Which source set this value | ✅ `Explain` | ❌ | ❌ | ❌ |
| Secret masking | ✅ structural | ❌ | ❌ | ❌ |
| Validate without booting | ✅ `Check` | ❌ | ❌ | ❌ |
| Generate `.env.example` | ✅ `Document` | ❌ | ❌ | ❌ |
| Valid config with zero input | ✅ pinned by a test | ❌ | ❌ | ⚠️ |
| Flag defaults cannot beat a file | ✅ | ❌ [#671](https://github.com/spf13/viper/issues/671) | ✅ | ⚠️ |
| Reload without a caller-side mutex | ✅ `Watcher` | ❌ | ❌ | ❌ |
| Dependencies | 1 | many | few | several |

The full matrix — six libraries, twenty rows, and where each alternative is the better choice — is in [docs/comparison.md](docs/comparison.md).

### Where did this value come from?

The most common configuration question, and nothing else answers it:

```go
res.Explain(os.Stdout)
```

```
FIELD    KEY           VALUE                     SOURCE
APIKey   API_KEY       ••••••                    map
DBURL    DATABASE_URL  postgres://localhost/dev  map
Port     PORT          9001                      map
Timeout  TIMEOUT       15s                       default
```

Secrets are **absent**, not styled out — the string never enters the output. Pass `cfgkit.Reveal()` when you actually want it.

Every snippet on this page is produced by a runnable example in [`example_test.go`](example_test.go) whose `// Output:` block is checked by `go test`. If the code's output changes, the test fails — so these cannot drift into fiction.

### Fail in CI, not at container start

```go
if err := cfgkit.Check[Config](opts...); err != nil {
	log.Fatal(err)
}
```

Runs the whole pipeline — sources, binding, derive, validate — without constructing an application. A stale environment file becomes a red build instead of a panic after deploy.

### Generate the contract file

```go
cfgkit.Document[Config](f)
```

```env
# Upstream API key
# optional · secret — do not commit a real value
API_KEY=

# Postgres connection string
# REQUIRED
DATABASE_URL=

# HTTP listen port
# optional
PORT=8080

# Request timeout
# optional
TIMEOUT=15s
```

Output is byte-stable, so CI can regenerate and diff. Pair it with [`dotenvctl matrix --contract .env.example`](https://github.com/ubgo/dotenv) and the loop closes: **struct → contract → every environment checked → red build.**

## Sources

Two shapes, because config data has two shapes.

**Flat** sources answer lookups by key and are matched by the `env:` tag:

```go
cfgkit.FromEnviron()
cfgkit.FromPrefixedEnviron("SVC_")
cfgkit.FromFiles(".env", ".env.local")     // missing file is not an error
cfgkit.FromFlagSet(fs)                     // stdlib flag
cfgkit.FromMap(m)                          // the test seam
```

**Structured** sources merge nested data and are matched by the `json:` tag:

```go
cfgkit.FromJSON(embeddedPkl)
```

Both live in one ordered list; a later source wins regardless of kind.

### The conventional chain, in one call

```go
cfg, res, err := cfgkit.Load[Config](cfgkit.DefaultSources())
```

`.env` → `.env.local` → `.env.<mode>` → `.env.<mode>.local` → the process environment, every file optional. It also resolves the **mode** from those files, not only from the environment — so `APP_ENV=production` in a `.env` file actually selects prod strictness and the `.env.prod` file. Writing that chain by hand is where it goes wrong. `Result.Files()` reports what was consulted; `Explain` still names the exact file per field.

### Any backend, in five lines

```go
secrets, _ := vault.ReadAll(ctx, "secret/data/app")     // pre-load: Lookup runs once per field
src := cfgkit.SourceFunc("vault", func(key string) (string, bool, error) {
	v, ok := secrets[key]
	return v, ok, nil
})
```

### Any format, in five lines

```go
src := cfgkit.StructuredFunc("config.yaml", func(dst any) error {
	b, err := os.ReadFile("config.yaml")
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, dst)   // the yaml dependency is YOURS
})
```

The parser lives in your code, so cfgkit never has to add a format and never has to refuse one.

## Keys are declared, never guessed

```go
type HyperDX struct {
	LogsSourceID string `env:"HYPERDX_LOGS_SOURCE_ID"`
}
```

No library can split `HYPERDX_LOGS_SOURCE_ID` correctly — `_` means both nesting and word break, so it has at least six valid readings. Declaring the key also means **renaming a Go field can never silently change which variable feeds it**, which is the failure that compiles, passes tests, and breaks production only.

Declare a prefix once on the parent when the repetition grates:

```go
type Config struct {
	HyperDX HyperDX `env:",prefix=HYPERDX_"`
}
```

## Tags

| Tag | Meaning |
|---|---|
| `env:"KEY"` | the flat key that fills this field |
| `env:",prefix=P_"` | on a struct: prepend to every key beneath it |
| `env:"KEY,required"` | the key must resolve |
| `env:"KEY,notempty"` | must resolve **and** not be empty — a different failure, with a different message |
| `env:"KEY,file"` | the value is a *path*; read the file at it (Docker/Kubernetes secrets) |
| `env:"KEY,unset"` | remove the key from the environment after reading, so children do not inherit it |
| `env:"-"` | never bound; set by `Derive` or left at its default |
| `json:"name"` | the nested path a structured source fills it from |
| `default:"v"` | default, parsed by the field's decoder |
| `secret:"true"` | mask everywhere |
| `delim:";"` | slice element / map entry separator (default `,`) |
| `kvdelim:"="` | map key-to-value separator (default `:`) |
| `flag:"port"` | the command-line flag that may set it |
| `was:"OLD_KEY"` | a former key name, so a rename does not break deployments |
| `doc:"text"` | description, used by `Document` |

## Types

`string`, `bool`, every sized int/uint, floats, `time.Duration`, `time.Time` (RFC3339), and slices of those. Anything else implements `encoding.TextUnmarshaler` — one escape hatch, so the library never grows a type zoo. `encoding.BinaryUnmarshaler` is also accepted, because `*url.URL` implements only that form.

Maps too, for the one case a named field cannot serve — the key names are not known when you write the struct:

```go
Flags map[string]string `env:"FLAGS"`   // FLAGS=new-checkout:on,dark-mode:off
```

Entries split on `,`, key from value on the **first** `:` — so `primary:postgres://db:5432/app` survives intact. If the names *are* known, use named fields instead; a map gives up every guarantee the type system offers.

## Hooks

```go
func (c *Server) Defaults()        { c.Port = 8080 }          // before binding
func (c *Server) Derive() error    { c.DSN = build(c); ... }  // after binding
func (c *Server) Validate() error  { ... }                    // after deriving
```

All three run depth-first, children before parents. Validation is Go, not a tag language — so a renamed field is a compile error rather than a rule that silently stops matching.

```go
func (p *PublicServe) Validate() error {
	return cfgkit.RequiredWhen(
		"Cloudflare", p.Cloudflare != nil,
		"kind=cloudflare", p.Kind == ServeKindCloudflare,
	)
}
```

That one call asserts both directions: required when the condition holds, forbidden when it does not.

Helpers: `Required` · `NotEmpty` · `RequiredIn` · `RequiredWhen` · `OneOf` · `Range` · `Matches` · `MutuallyExclusive` · `AtLeastOneOf` · `NotWeakSecret`.

### Custom validators

Want a catalogue — email, URL, UUID, CIDR? Plug one into the same seam. The dependency stays in **your** module:

```go
func (c *Config) Validate() error {
	return errors.Join(
		validator.New().Struct(c),                    // go-playground/validator tags
		cfgkit.RequiredWhen(...),                     // conditional logic in Go
	)
}
```

Tags for the catalogue rules, Go for the conditional ones, both joined so all errors surface at once.

## Modes

One knob, not twenty booleans — because every independent flag creates a path nobody tests:

```go
cfgkit.Load[Config](cfgkit.WithMode(cfgkit.ModeProd))
```

Resolved from `APP_ENV` when not passed; `dev` by default. The rule it enforces:

> **Every convenience that makes development frictionless must hard-fail in production.**

```go
cfgkit.NotWeakSecret(mode, "EncryptionKey", c.Key)  // fine in dev, refuses to boot in prod
```

## contrib

Adapters carrying a dependency live in their own module, so you compile only what you import.

| Module | Adds | Guide |
|---|---|---|
| `contrib/format-hcl` | HCL documents and files | [README](contrib/format-hcl/README.md) |
| `contrib/format-ini` | INI documents and files — **flat** | [README](contrib/format-ini/README.md) |
| `contrib/format-properties` | Java `.properties` — **flat** | [README](contrib/format-properties/README.md) |
| `contrib/format-toml` | TOML documents and files | [README](contrib/format-toml/README.md) |
| `contrib/format-yaml` | YAML documents and files | [README](contrib/format-yaml/README.md) |
| `contrib/flags-pflag` | cobra / pflag flag sets | [README](contrib/flags-pflag/README.md) |
| `contrib/source-azurekeyvault` | Azure Key Vault — **no dependencies** | [README](contrib/source-azurekeyvault/README.md) |
| `contrib/source-consul` | Consul KV prefixes — **no dependencies** | [README](contrib/source-consul/README.md) |
| `contrib/source-etcd` | etcd v3 prefixes, via its HTTP gateway — **no dependencies** | [README](contrib/source-etcd/README.md) |
| `contrib/source-gcpsecrets` | Google Secret Manager — **no dependencies** | [README](contrib/source-gcpsecrets/README.md) |
| `contrib/source-k8s` | Kubernetes ConfigMaps and Secrets — **no dependencies** | [README](contrib/source-k8s/README.md) |
| `contrib/source-vault` | HashiCorp Vault KV secrets — **no dependencies** | [README](contrib/source-vault/README.md) |
| `contrib/source-appconfig` | AWS AppConfig profiles | [README](contrib/source-appconfig/README.md) |
| `contrib/source-s3` | a config document in an S3 object | [README](contrib/source-s3/README.md) |
| `contrib/source-secretsmanager` | AWS Secrets Manager | [README](contrib/source-secretsmanager/README.md) |
| `contrib/source-ssm` | AWS Parameter Store | [README](contrib/source-ssm/README.md) |
| `contrib/source-kiln` | kiln-encrypted env files — the one source whose file is **safe to commit** | [README](contrib/source-kiln/README.md) |
| `contrib/source-nats` | NATS JetStream key/value buckets | [README](contrib/source-nats/README.md) |

Every module's dependencies and support level: [the catalogue](docs/catalogue.md).

```go
import yamlsrc "github.com/ubgo/cfgkit/contrib/format-yaml"

cfgkit.WithSources(yamlsrc.File("config.yaml"), cfgkit.FromEnviron())
```

Writing your own? `cfgkittest.RunSourceTests` and `RunStructuredTests` are the shared conformance suite — pass them and your adapter behaves exactly like the built-ins. Full guide: [writing an adapter](docs/writing-adapters.md).

## Gotchas

**Flags: `Load` must run *after* parsing.** With cobra that means inside `RunE`, never `init()`. A flag set built too early holds nothing, contributes nothing, and looks like "flags do not work".

**Flags have no defaults here.** Declare cobra flags with a zero default and put the real one in `Defaults()`. Two defaults for one field is two sources of truth, and the flag's is invisible to `Explain`.

**Structured sources replace slices.** Nested structs merge field by field; slices and maps replace wholesale. Standard `encoding/json` behaviour.

**`Lookup` runs once per field.** A remote backend must pre-load or cache, or a 200-field config becomes 200 round-trips at boot.

## Testing

| | |
|---|---|
| Statement coverage — core | **100.0%** |
| Statement coverage — every format/flags module | **100.0%** |
| Statement coverage — source modules | 96.9%–100.0% |
| Test functions | 727 across 20 modules |
| Fuzz targets | 23 — 6 core, 17 contrib |
| Dependencies | 1 |

Re-measure any of it with `task cover`, which spans every module, or `task test:uncovered` for what is left per function.

Every code sample in this README is a runnable example in [`example_test.go`](example_test.go) whose `// Output:` block is checked by `go test`. The guard was verified by breaking it: change a character in an expected output and the test fails.

Invariants pinned by tests: zero-input, precedence, no environment mutation, secret masking, error completeness, idempotence, contract completeness, and the capability firewall. The core is at **100.0% statement coverage** — every function, every branch — and so is every format and flags module.

The source modules sit between 96.9% and 100.0%, and **the thirteen uncovered statements are classified rather than rounded away**. All of them fall into three groups, each verified unreachable rather than assumed:

| Not covered | Why it cannot fire |
|---|---|
| `http.NewRequestWithContext`'s error return (8 sites) | The method is a constant and the URL is an already-parsed `*url.URL`. Checked by feeding prefixes containing newlines, `NUL` and `DEL`: `URL.String()` percent-encodes every one, so the request URI is always valid. |
| `json.Marshal`'s error in `stringify` (2 sites) | The value came out of `json.Unmarshal` moments earlier, so it is marshalable by construction. |
| `nc.JetStream()`'s error in `source-nats` | The legacy client builds its context lazily. Checked against a server with JetStream **disabled**: this call still succeeds and the failure surfaces at `KeyValue` instead — which is pinned by its own test. |

None is deleted, because each guards a call that would return a nil value on failure, and the next caller is not guaranteed to be as lucky. Every one of them runs under `task ci` on Linux, macOS and Windows — including `GOOS` builds for all three, because a portability claim that nothing compiles for is a claim, not a fact. The same gate is defined as a GitHub Actions workflow in `.github/workflows/ci.yml`, currently **manual-only**: uncomment the `push` trigger to have it run on every commit. `FuzzResolutionNeverPanics` ran 6 million executions with no panic and no double-binding; re-run any target with `task fuzz` or all six with `task fuzz:all`.

**The conformance harness is itself tested.** Every adapter claims conformance by calling `cfgkittest.RunSourceTests` and passing, and that claim is worth exactly what the suite's ability to FAIL a bad source is worth. So the harness is fed five sources that each violate one guarantee — ignoring values, claiming keys it has none for, treating a cleared value as no opinion, reporting a backend failure as a miss, and zeroing fields a document omits — and each must be caught **on the check that should catch it**, not merely somewhere. The fixture set in [`mock/`](mock) is guarded the same way: a format added to the directory but left out of `Fixtures` would silently never be proved, so a test walks the embedded files and fails on the gap.

**Every parser and every response path is fuzzed**, not just the core. All five format modules take arbitrary bytes; the six HTTP-based sources are fed arbitrary response bodies through `httptest`; and the six that go through an SDK or library seam are fed arbitrary payloads through it. That is the case worth defending: a configuration source is trusted infrastructure right up until it is not — a compromised server, a truncated response, a proxy that injects an error page, an object half-written by a pipeline. None of it may crash the program trying to read its own configuration. `task fuzz:contrib` sweeps all seventeen, discovering targets from the source so a new one enrolls by existing.

Fuzzing is there because a table of edge cases is only as good as the imagination that wrote it. Two of the targets assert **properties** rather than the absence of a crash: a string field is a byte pipe (whatever goes in comes out, invalid UTF-8 and NUL included), and splitting a list then rejoining it reproduces the input. The second one earned its keep on its first run by finding that `"0\r,"` round-trips to `"0,"` — list elements are trimmed with `TrimSpace`, so a carriage return is stripped. That is the behaviour you want, since it makes a CRLF `.env` produce clean elements on Linux, but nothing had written it down.

**The uncovered remainder is enumerable, not mystery.** What is left is defensive: the `addrOf` guard for a value that cannot be addressed (unreachable through the public API, since `Load` always starts from an addressable struct), the `forEachStruct` branch for a nil pointer section, `Explain`'s per-row write-error returns beyond the first, and the `collectLeaves` nil-section arm. Each is a line or two whose absence from the count is explained rather than ignored.

## Reload

```go
w, _ := cfgkit.NewWatcher[Config](func() []cfgkit.Option {
	return []cfgkit.Option{cfgkit.DefaultSources()}
})

cfg := w.Current()      // safe from any goroutine, no mutex
w.Reload()              // on your trigger: SIGHUP, a timer, an admin route
```

`Reload` runs the whole pipeline and validates **before** it publishes, so a bad edit is reported and the process keeps running on the configuration it already had. Readers take no lock, and a held value never changes underneath them. viper and koanf both tell you in their own docs to add a mutex; this needs none. One rule: a component holds the `Watcher`, never a value read from it — see [Reload](docs/reload.md).

## Runnable examples

**Twenty-two of them, and every one actually runs** — including the twelve that talk to a remote backend. There is no Vault, Consul, etcd, cluster, AWS account, GCP project or Azure subscription to set up first.

```sh
go run ./examples/read-file
go run ./examples/read-vault      # starts a fake Vault in-process
go run ./examples/read-nats       # starts a REAL nats-server in-process
```

| | |
|---|---|
| **Start here** | [`read-file`](examples/read-file), [`read-environment`](examples/read-environment), [`default-values`](examples/default-values), [`precedence`](examples/precedence) |
| **Local sources** | [`read-json`](examples/read-json), [`read-struct`](examples/read-struct), [`read-formats`](examples/read-formats), [`read-commandline`](examples/read-commandline) |
| **Remote sources** | [`read-vault`](examples/read-vault), [`read-consul`](examples/read-consul), [`read-etcd`](examples/read-etcd), [`read-k8s`](examples/read-k8s), [`read-nats`](examples/read-nats), [`read-kiln`](examples/read-kiln), [`read-gcpsecrets`](examples/read-gcpsecrets), [`read-azkeyvault`](examples/read-azkeyvault), [`read-s3`](examples/read-s3), [`read-parameterstore`](examples/read-parameterstore), [`read-secretsmanager`](examples/read-secretsmanager), [`read-appconfig`](examples/read-appconfig) |
| **Auditing** | [`provenance`](examples/provenance), [`validation`](examples/validation) |
| **Patterns** | [`plugin`](examples/plugin) — a plugin configuring itself when the host cannot name its config type, the case `viper.Sub()` exists for, answered without an API |

Every example's output is **pinned by a test** and run by `task ci`, so documentation that drifts from the code fails the build instead of quietly becoming a lie. Full index and conventions: [`examples/README.md`](examples/README.md).

## FAQ

**Does it read my config file at startup with a global `Get()`?** No. There is no package-level state and no `Get`. `Load` returns a value your code owns and passes where it is needed — so tests can run in parallel with different configurations, and a library can never depend on a hidden singleton.

**Is there a `Sub()` / `Cut()` for handing part of the config to a component?** No, and it is not missing. viper and koanf hold an untyped `map[string]any`, so `Sub` is how you cut out a piece for a component to unmarshal. Here the tree is typed: when you know the component's type you pass the field (`startEmailSender(cfg.SMTP)`), and when you do not — a plugin — the host passes a **source** and the plugin runs its own `Load`, bringing its own type, defaults, validation, secrets and provenance. See [`examples/plugin`](examples/plugin/README.md).

**How is this different from viper or koanf?** Five capabilities neither has: `Explain` reports which source set each field, secrets are structurally masked, `Check` validates without constructing your application, and `Document` generates the `.env.example` contract from your struct. Reload is safe with no lock in the caller, where both of them tell you in their own documentation to add a mutex. cfgkit also has one dependency where viper has many.

**Why doesn't it guess the key from my field name?** Because it cannot be done correctly. `HYPERDX_LOGS_SOURCE_ID` has at least six valid readings, since `_` separates both nesting levels and words within a name. Declaring the key also means renaming a Go field can never silently change which environment variable feeds it.

**Can I use YAML or TOML?** Yes — either through `contrib/format-yaml`, or in five lines of your own code with `StructuredFunc`, keeping the parser dependency in your module rather than in cfgkit.

**Does it support Vault, AWS Secrets Manager, or Kubernetes secrets?** All three ship as dedicated modules, along with Consul, etcd, NATS, kiln, Google Secret Manager, Azure Key Vault, S3, Parameter Store and AppConfig — [the full catalogue](docs/catalogue.md). Vault, Consul, etcd, Kubernetes, GCP and Azure carry **no dependencies at all**, because on those platforms a credential is a file or a header. For mounted secrets, `env:"KEY,file"` reads the value from the path the variable points at, and `source-k8s` can read a projected volume directly with no API permission. Anything not in the catalogue is still five lines behind `SourceFunc`.

**Will it change my process environment?** No, with one opt-in exception: a field tagged `unset` removes its own key after reading, so a child process cannot inherit the secret. That exception is pinned by a test.

**How do I validate with `go-playground/validator`?** Call it inside your `Validate()` method. The `Validator` interface is a seam, so the catalogue rules are available with the dependency in your module and not in cfgkit's.

**Does it support hot reload?** Yes, through `Watcher[T]` — and without the mutex both alternatives require. It publishes an immutable snapshot behind an atomic pointer and validates **before** publishing, so a bad edit is reported and the previous configuration stays in service. What it deliberately does not do is watch files: that needs `fsnotify`, so the trigger is yours — SIGHUP, a timer, an admin route, or your own watcher. See [Reload](docs/reload.md).

## License

Apache-2.0

<sub>cfgkit is a typed Go configuration library — layered sources, compiled-in defaults, environment variables, .env files, JSON, YAML, command-line flags, secret stores, cross-field validation, secret masking, provenance reporting, and .env.example generation. Apache-2.0 licensed.</sub>
