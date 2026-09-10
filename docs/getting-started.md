# Getting started

## Install

```sh
go get github.com/ubgo/cfgkit
```

One dependency comes with it: `github.com/ubgo/dotenv`, which is itself stdlib-only.

## The smallest thing that works

```go
type Server struct {
	Host string `env:"HOST" default:"localhost"`
	Port int    `env:"PORT" default:"8080"`
}

cfg, _, err := cfgkit.Load[Server]()
fmt.Printf("%s:%d err=%v\n", cfg.Host, cfg.Port, err)
```

```
localhost:8080 err=<nil>
```

No sources. No environment variables. No file. That is the property everything else is built on: **the absence of configuration is a valid configuration.** It is what lets someone clone your repository and run it, and it is pinned by a test.

## Adding sources

For the conventional layout, one call does it:

```go
cfg, res, err := cfgkit.Load[Config](cfgkit.DefaultSources())
```

That reads `.env`, `.env.local`, `.env.<mode>`, `.env.<mode>.local`, then the process environment — and works out the mode from those files, which is not as simple as it looks. See [Sources](sources.md#the-conventional-chain-in-one-call).

To state the chain yourself:

```go
cfg, res, err := cfgkit.Load[Config](
	cfgkit.WithSources(
		cfgkit.FromFiles(".env", ".env.local"),
		cfgkit.FromEnviron(),   // last = highest precedence
	),
)
```

Three return values, and each matters:

| Value | What it is |
|---|---|
| `cfg` | your typed struct, fully populated |
| `res` | the record of **where every value came from** — see [Provenance](provenance.md) |
| `err` | **every** problem found, joined — never just the first |

Sources are consulted last-to-first, so the last one in the list wins. There is no hidden default chain: what you write is the precedence.

## The pipeline

`Load` runs five phases in order. Knowing them explains almost every behaviour in the library.

| # | Phase | What happens |
|---|---|---|
| 1 | **Defaults** | `Defaults()` on every struct, then `default:` tags fill anything still zero |
| 2 | **Sources** | structured sources merge onto the struct; flat sources are collected |
| 3 | **Bind** | every leaf is resolved against the flat sources, recording its origin |
| — | *(prune)* | an optional `*Struct` section no source touched goes back to `nil` ([Tags](tags.md#optional-sections--when-a-struct-is-nil)) |
| — | *(report)* | keys a listable source supplied that no field wanted are recorded in `res.Unknown()` ([Sources](sources.md#unknown-keys--catching-a-typo)) |
| 4 | **Derive** | `Derive()` computes values from other values |
| 5 | **Validate** | `Validate()` checks the finished result |

Phases 1, 4 and 5 run depth-first — children before parents — so a parent's hook always sees finished children.

## Errors report everything

A misconfigured deployment must not be a guessing game of one fix per restart.

```go
err := cfgkit.Check[Config](cfgkit.WithSources(
	cfgkit.FromMap(map[string]string{"PORT": "eighty"}),
))
fmt.Println(err)
```

```
cfgkit: 2 problem(s):
Port (PORT from map): "eighty" is not a valid int
DBURL (DATABASE_URL) is required but no source supplied it
```

Two problems, two errors, one run. Each names the Go field path, the key an operator would set, and the source that supplied the bad value.

Six typed errors are available for programmatic handling:

| Error | Means |
|---|---|
| `*DecodeError` | a value could not be parsed into the field's type |
| `*RequiredError` | a required field did not resolve — `Empty` distinguishes missing from blank |
| `*ValidationError` | a rule failed; `Rule` names which |
| `*SourceError` | a source itself failed — **never** a miss |
| `*UnreachableFieldError` | the struct is shaped so no source could reach a field |
| `*UnreachableHookError` | a `Defaults`/`Derive`/`Validate` could never be called |

```go
var de *cfgkit.DecodeError
if errors.As(err, &de) {
	log.Printf("bad value for %s (from %s)", de.Path, de.Source)
}
```

## Next

- [Sources](sources.md) — the two kinds, the conventional chain, and how to add any backend in five lines
- [Tags](tags.md) — the complete reference
- [Validation](validation.md) — hooks and rules
- [Provenance](provenance.md) — `Explain`, `Check`, `Document`
- [Reload](reload.md) — `Watcher[T]`, for config that changes while the process runs
- [Comparison](comparison.md) — against viper, koanf and four others, honestly
- [Recipes](recipes.md) — Pkl, Docker secrets, CI gates
