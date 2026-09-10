# Sources

Configuration data comes in exactly two shapes, and `cfgkit` models both rather than pretending everything is flat.

| Kind | Interface | Matched by | Used for |
|---|---|---|---|
| **Flat** | `Source.Lookup(key)` | the `env:` tag | `.env` files, the process environment, secret stores, flags |
| **Structured** | `StructuredSource.Apply(dst)` | the `json:` tag | JSON, and therefore Pkl, YAML, TOML |

## Why they cannot be one interface

Flattening nested data into keys does not work. Given:

```json
{"hyperdx": {"logsSourceId": "abc"}}
```

flattening yields `hyperdx.logsSourceId`, which matches no environment key. And deriving `HYPERDX_LOGS_SOURCE_ID` from it is impossible, because `_` means both *nesting* and *word break* — see [Tags](tags.md#keys-are-declared-never-guessed). So each shape fills the struct through its own natural mechanism, and both live in one ordered list.

## Precedence

Sources are consulted **last-to-first**: the last source in your list wins. The first source that claims a key ends the search.

```go
cfg, res, _ := cfgkit.Load[Config](cfgkit.WithSources(
	cfgkit.FromMap(map[string]string{"PORT": "1111", "HOST": "from-first"}),
	cfgkit.FromMap(map[string]string{"PORT": "2222"}),
))
```

```
Port=2222 Host=from-first Name=app
Host  map
Name  default
Port  map
```

`PORT` came from the later source. `HOST` was claimed only by the earlier one, so it still applies. `NAME` was claimed by neither, so it kept its compiled-in default — and the record says so.

There is no implicit ordering and no built-in default chain, because a convenience that hides precedence is exactly the magic this package exists to avoid.

### A source always wins — even with a zero value

**Absent and empty are different things**, and the distinction is what makes a default of `true` possible to switch off.

| The config says | The default is | Result |
|---|---|---|
| `DEBUG=false` | `true` | **`false`** |
| `HOST=` | `localhost` | **`""`** |
| `PORT=0` | `8080` | **`0`** |
| `ORIGINS=` | `a,b` | **`[]`** |
| *(key absent)* | `localhost` | `localhost` |

Only the last row keeps the default. Every other row is a deliberate choice by whoever wrote the configuration, and a loader that treated `""` or `false` as "nothing to see here" would make those choices impossible to express:

- `DEBUG=false` could never turn off a feature that defaults to on.
- `ORIGINS=` could never mean *allow no origins* — a security-relevant setting.
- `HOST=` could never clear a value.

Provenance agrees: a field a source set to its zero value reports **that source**, not `default`. Otherwise `Explain` would tell an operator their line had no effect when it did.

The same holds across the chain — a later source clearing an earlier source's value wins, so the environment can blank a value a file set.

## Built-in sources

| Constructor | Kind | Reads from | Reach for it when |
|---|---|---|---|
| `FromMap(m)` | flat | an in-memory map | **testing** — no files, no environment, no ordering surprises |
| `FromEnviron()` | flat | `os.Environ()` | always in production; it is how containers and CI pass configuration |
| `FromPrefixedEnviron(p)` | flat | `os.Environ()`, prefix stripped | one process hosts several components whose short key names would collide |
| `FromFiles(paths...)` | flat | `.env` files via `ubgo/dotenv` | local development, so nobody has to export a dozen variables by hand |
| `FromFlagSet(fs)` | flat | a parsed stdlib flag set | an operator wants to override one value for one run |
| `FromJSON(b)` | structured | a JSON object | configuration comes from Pkl, a remote API, or an embedded document |

### `FromEnviron` — the production path

This is how nearly every deployment platform passes configuration: Kubernetes, Docker Compose, systemd, Heroku, CI runners. Put it **last** in the chain so an operator can always override a file without editing it.

### `FromPrefixedEnviron` — namespacing

**The situation:** a single binary runs two components that both want `PORT` and `TIMEOUT`. Rather than renaming every field, mount each component's config under its own prefix and let `SVCA_PORT` and `SVCB_PORT` coexist. It is also how a multi-tenant process loads one configuration per tenant in a loop.

### `FromMap` — the test seam

**Always use this in tests, never `os.Setenv`.** Setting environment variables makes tests order-dependent, unsafe to run in parallel, and capable of leaking state into unrelated tests. `FromMap` gives the same result with none of that.

### `FromFiles` — local development

```go
cfgkit.FromFiles(".env", ".env.local", ".env."+string(mode))
```

Later files win, matching what Vite, Next and Rails users already know: `.env` holds committed defaults, `.env.local` is gitignored and personal, `.env.<mode>` is per-environment.

**Why this layering earns its keep:** the committed `.env` documents what exists and gives everyone working defaults, while `.env.local` lets one developer point at their own database without ever risking a commit that breaks the team.

**A missing file is not an error.** That is what allows a program to ship with no `.env` at all and still run. A file that exists but cannot be read *is* an error — a broken config must never look like an absent one.

Files are read once, at construction, so the per-field cost contract below is honoured.

## Writing your own — any backend, five lines

```go
// Pre-load once: Lookup is called per field, so a remote store must cache.
store := map[string]string{"APP_SECRET": "from-vault"}
src := cfgkit.SourceFunc("vault", func(key string) (string, bool, error) {
	v, ok := store[key]
	return v, ok, nil
})
```

```
APP_SECRET=from-vault from vault
```

The three return values carry the whole contract:

| Return | Meaning |
|---|---|
| `value, true, nil` | this source claims the key |
| `"", false, nil` | **no opinion** — resolution continues to the next source |
| `"", false, err` | the source itself failed — **aborts the whole `Load`** |

<!-- The distinction in that last row is the important one. -->

### A source failure is never a miss

```go
down := cfgkit.SourceFunc("vault", func(string) (string, bool, error) {
	return "", false, errors.New("connection refused")
})
err := cfgkit.Check[Config](cfgkit.WithSources(down))
```

```
true cfgkit: 1 problem(s):
source vault failed for APP_SECRET: connection refused
```

An unreachable secret store must never be indistinguishable from an unset variable. If it were, a deploy would proceed with an empty password and nothing would say so.

### `KeyLister` — let cfgkit catch typos in your source

A source that knows its **complete** key set can opt into typo detection by adding one method:

```go
func (v vaultSource) Keys() []string { return slices.Collect(maps.Keys(v.secrets)) }
```

cfgkit then reports any key your source supplied that matched no field — see [Unknown keys](#unknown-keys--catching-a-typo).

**Implement it only if you can answer honestly.** A source whose key set is not the application's must omit the method; a closure that cannot enumerate simply does not have it. Nothing else changes.

### The cost contract

**`Lookup` is called once per bound field.** A source that dials the network per key turns a 200-field configuration into 200 round-trips at boot. Pre-load in the constructor, as `FromFiles` does, and serve from memory.

Structured sources have no such concern: `Apply` runs once.

## Writing your own — any format, five lines

```go
src := cfgkit.StructuredFunc("config.yaml", func(dst any) error {
	b, err := os.ReadFile("config.yaml")
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, dst)   // the yaml dependency is YOURS
})
```

Because `Apply` receives your destination struct directly, **the parser lives in your code**. `cfgkit` never has to add a format and never has to refuse one, and its dependency count stays at one.

The one rule an implementation must honour: **leave absent fields untouched.** `encoding/json` and `yaml.v3` both do this natively — it is what makes layering work at all.

## Mixing both kinds

```go
cfg, res, _ := cfgkit.Load[Config](cfgkit.WithSources(
	cfgkit.FromJSON([]byte(`{"hyperdx":{"logsSourceId":"from-json","apiKey":"from-json"}}`)),
	cfgkit.FromMap(map[string]string{"HYPERDX_API_KEY": "from-env"}),
))
```

```
from-json from-env
HyperDX.APIKey         HYPERDX_API_KEY          map
HyperDX.LogsSourceID   HYPERDX_LOGS_SOURCE_ID   json
```

One list, both kinds, later wins. The JSON set both fields; the flat source then overrode one of them, and the record names each origin correctly.

## Compiled-in defaults with `go:embed`

`FromFS` reads `.env` files out of an `fs.FS`, which makes a binary carry its own defaults:

```go
//go:embed defaults.env
var defaults embed.FS

cfgkit.WithSources(
	cfgkit.FromFS(defaults, "defaults.env"),
	cfgkit.FromFiles(".env"),
	cfgkit.FromEnviron(),
)
```

It shares its whole implementation with `FromFiles`, so the two cannot drift: later paths win, a missing entry is silent, and `${VAR}` resolves across entries in the same direction. The only difference is where the bytes come from.

**Paths are `fs.FS` paths** — forward-slash separated and never rooted, even on Windows, because that is what `io/fs` specifies.

## The conventional chain, in one call

Nearly every service writes the same twelve lines:

```go
mode := cfgkit.ModeDev
if os.Getenv("APP_ENV") == "production" {
	mode = cfgkit.ModeProd
}
cfgkit.WithSources(
	cfgkit.FromFiles(".env", ".env.local", ".env."+string(mode)),
	cfgkit.FromEnviron(),
)
cfgkit.WithMode(mode)
```

`DefaultSources` is that, correct:

```go
cfg, res, err := cfgkit.Load[Config](cfgkit.DefaultSources())
```

It builds this chain, lowest precedence first:

| # | Source | What it is for |
|---|---|---|
| 1 | `.env` | committed to the repository; what a fresh clone needs |
| 2 | `.env.local` | gitignored, personal to one machine |
| 3 | `.env.<mode>` | per-environment, committed |
| 4 | `.env.<mode>.local` | per-environment and personal |
| 5 | the process environment | how a deployment sets things |
| 6 | any extra sources you pass | flags, a per-run override |

Every file is optional. The whole chain may be absent and the load still succeeds on compiled-in defaults — that is what keeps `git clone && go run .` working.

`DefaultSourcesIn(dir)` roots the chain somewhere other than the working directory: a test fixture directory, or a service in a monorepo run from the repository root.

### `${VAR}` references are resolved

`FromFiles` expands references the way Docker Compose does when it reads a `.env`:

```
DB_HOST=db.internal
DB_PORT=5432
DATABASE_URL=postgres://${DB_HOST}:${DB_PORT}/app
```

```
postgres://db.internal:5432/app
```

The Compose forms all work: `${VAR:-default}` supplies a fallback, and `${VAR:?message}` makes the **file itself** declare the reference required — an unresolvable one fails the load rather than arriving as an empty value.

**A later file may reference an earlier one.** The chain is expanded as it is merged, so `.env.local` can name a value `.env` defined:

```
.env         DB_HOST=db.internal
             DB_PORT=5432
.env.local   URL=postgres://${DB_HOST}:${DB_PORT}/app
```

```
postgres://db.internal:5432/app
```

Three rules follow, each with its own test:

| | |
|---|---|
| **A file's own definition wins** | the inherited value is only a fallback, so a shared base file cannot override the environment-specific one that referenced it |
| **References cannot reach forward** | an earlier file may not name a value a later one defines — values flow from lower precedence to higher, and a backward dependency would invert that |
| **The process environment is never consulted** | Compose reads the shell environment when *it* expands; this does not, because reading `os.Environ` behind your back is exactly what an explicit source list exists to prevent. Add `FromEnviron` to give the environment a say |

### The mode comes from the files too

This is the part that earns the helper its place, and it fixes a real bug.

`.env.<mode>` cannot be chosen until the mode is known — but the mode is itself a configuration value that an operator reasonably writes into a `.env` file. That is a chicken-and-egg, and before `DefaultSources` existed cfgkit resolved it wrongly: the mode was read with `os.Getenv` only, so this was **silently ignored**:

```
# .env.prod
APP_ENV=production
```

The mode stayed `dev`, and every `RequiredIn(ModeProd, ...)` rule quietly did not fire — a production deployment running under development strictness, with nothing in the logs to say so.

`DefaultSources` resolves the mode in a first pass, using the same precedence rule as everything else:

| # | Mode comes from | |
|---|---|---|
| 1 | `WithMode(...)` | the caller stated it; nothing overrides that |
| 2 | the process environment | how a deployment sets it |
| 3 | `.env` and `.env.local` | how a developer's machine sets it |
| 4 | `dev` | nothing said otherwise |

Only the two base files are read in that pass. Consulting `.env.<mode>` would be circular — its name is the answer.

`WithMode` works on either side of `DefaultSources` in the argument list, because the chain is resolved after every option has been applied rather than inside the option itself.

Both spellings of every mode are accepted — `prod` and `production`, `test` and `testing` — so nobody has to guess which one the library wants. Anything unrecognised is `dev`: an unset or misspelled mode means a developer's machine far more often than it means production, and every prod-only rule fails closed anyway.

### `.env.local` is skipped in test mode

The one deliberate divergence from Vite, taken from create-react-app. A developer's personal, gitignored file silently changing the result of `go test` on one machine and not another is precisely the class of bug this library exists to prevent.

`.env.test.local` **is** still loaded — it is named for the mode, so using it is a deliberate choice rather than an accident.

### Which files were even looked at?

With the filenames no longer written in `main.go`, a typo'd filename would look exactly like a file whose values were overridden. `Result.Files()` answers it:

```go
cfg, res, err := cfgkit.Load[Config](cfgkit.DefaultSources())
for _, f := range res.Files() {
	log.Println("consulted:", f)
}
```

```
consulted: .env
consulted: .env.local
consulted: .env.prod
consulted: .env.prod.local
```

Files that did not exist are listed too — that is the point. `Explain` still names the exact file that supplied each field, so nothing about provenance is lost by using the helper.

### Combining it with your own sources

Extra arguments sit at the **top**, above the environment, which is where a flag set or a per-run override belongs:

```go
cfgkit.DefaultSources(pflagsrc.Source(cmd.Flags()))
```

A `WithSources` list sits at the **bottom**, below the whole chain — a base the conventional files then override:

```go
cfgkit.Load[Config](
	cfgkit.WithSources(embeddedDefaults),   // lowest
	cfgkit.DefaultSources(),                // the chain, then environ
)
```

For any other arrangement — a secret store that the environment should be able to override, say — write the explicit list. The helper expresses the common case, not every case.

## Unknown keys — catching a typo

```env
DATABAS_URL=postgres://prod-db.internal/app     # typo: missing the E
PORT=9000
```

Without detection this is completely silent: `DATABASE_URL` falls back to its default, `PORT` works so nothing looks broken, and in production the app points at the wrong database.

```go
if u := res.Unknown(); len(u) > 0 {
	log.Printf("config: %v", u)
}
```

```
config: [DATABAS_URL (from map) matched no field]
```

### Only listable sources contribute

| Source | Reports unknown keys? | Why |
|---|---|---|
| `FromFiles` | ✅ | every key in the file was written for this app |
| `FromMap` | ✅ | you constructed it |
| `FromPrefixedEnviron` | ✅ | the prefix defines a closed set |
| **`FromEnviron`** | ❌ | its key set is the whole machine |
| `SourceFunc` | ❌ | a closure cannot enumerate |
| `FromFlagSet` | ❌ | the flag package already rejects unknown flags |

`FromEnviron` is excluded because it lacks the `KeyLister` method, not because of a flag. Your machine's `PATH`, `HOME`, `SHELL` and sixty other variables cannot enter the report, so the one line that matters is not buried.

### It reports; it never fails

**A strict mode that refused to start was considered and rejected.** One `.env` file legitimately serves several audiences — application config alongside `GITHUB_SECRET_*` keys meant for a deployment pipeline. Those keys are unknown to the binary *by design*, and failing on them would break exactly the pattern `dotenvctl`'s `--prefix` selection exists to serve.

So the caller decides. Log it, print it, gate CI on it, or ignore it.

## Gotchas

**A `was:` former key name is never reported as unknown.** A deployment still exporting the old name is doing what the tag exists to permit; flagging it would punish the migration.

**Structured sources replace slices, they do not merge them.** If defaults set `Hosts: ["localhost"]` and the document sets `["a","b"]`, the result is `["a","b"]` — not all three. Nested *structs* merge field by field; **slices and maps replace wholesale.** This is standard `encoding/json` behaviour and almost always what you want, but it is a rule to know rather than discover.

**A structured source that writes a value identical to the existing one is invisible to provenance.** Origins for structured sources are recovered by observing what changed, so an assignment that changes nothing leaves the earlier origin standing. The value is the same either way, so this only affects the reported source name.

**`FromPrefixedEnviron` strips the prefix before matching.** With `FromPrefixedEnviron("SVC_")`, a field tagged `env:"PORT"` is filled by `SVC_PORT`. That is different from the `env:",prefix=..."` tag, which prepends to the key for *every* source.
