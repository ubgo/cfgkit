# Provenance and diagnostics

Four capabilities that no other Go configuration library offers: knowing where each value came from, masking secrets structurally, validating without booting, and generating the contract file from the struct. (A fifth, reload without a caller-side mutex, lives in [Reload](reload.md); the evidence for all five is in [Comparison](comparison.md).)

## `Explain` — where did this value come from?

**The situation this solves.** An operator says the port is wrong. You check the `.env` file — it says 2310. The app is on 9001. Now you start bisecting: is it the environment? A different file? A default in code? Someone's shell profile? A leftover export in the deploy script? Every layer you added for flexibility is now a place the value could be hiding, and you are grepping.

`Explain` answers it in one line, and it is the most common configuration support question there is. Nothing else in the Go ecosystem answers it at all.

**Where to use it:** ship it behind a debug flag in every service. It costs nothing when unused and turns a bisection into a lookup. It is also the fastest way to understand an unfamiliar service's configuration — one command shows every key, every value, and which layer won.

```go
_, res, err := cfgkit.Load[Config](...)
res.Explain(os.Stdout)
```

```
FIELD    KEY           VALUE                     SOURCE
APIKey   API_KEY       ••••••                    map
DBURL    DATABASE_URL  postgres://localhost/dev  map
Port     PORT          9001                      map
Timeout  TIMEOUT       15s                       default
```

Four columns, and each answers a different question:

| Column | Question |
|---|---|
| `FIELD` | the Go path — what a developer reads in an error |
| `KEY` | the flat key — what an operator sets in a deployment |
| `VALUE` | what the field actually holds, masked when secret |
| `SOURCE` | **which source won** |

Rows sort by Go path, so two runs of the same configuration produce identical output and can be diffed.

Origins you will see: `default` (the compiled-in value), a source's own name (`environ`, `file:.env,.env.local`, `vault`), or a name with a suffix such as `map (deprecated key HYPERDX_KEY)` when a `was:` fallback supplied it.

## `Result.Unknown()` — why did my value go nowhere?

The mirror of `Explain`. That answers *where did this value come from*; this answers *why did the value I set have no effect*.

```go
if u := res.Unknown(); len(u) > 0 {
	log.Printf("config: %v", u)
}
```

```
config: [DATABAS_URL (from file:.env) matched no field]
```

`Explain` gets a careful reader close — it shows the field falling back to `default`, so the file evidently did not supply it — but it never mentions the misspelled key, because no field asked for it. `Unknown()` is the only place that key appears.

Only sources implementing `KeyLister` contribute, so `FromEnviron` never appears here: its key set is the whole machine. Full rules: [Sources](sources.md#unknown-keys--catching-a-typo).

## `Result.Fields()` — the same data, programmatically

```go
for _, f := range res.Fields() {
	if f.Secret && f.Source == "default" {
		log.Printf("%s is still using its compiled-in default", f.Path)
	}
}
```

```go
type Field struct {
	Path   string   // "Server.Port"
	Key    string   // "PORT"
	Value  string   // rendered; masked when Secret and not revealed
	Source string   // "default" | "environ" | a Source's Name()
	Secret bool
}
```

`res.Mode()` reports the mode the load ran under. `res.JSON()` returns the whole record as JSON, masked by the same rule as `Explain`.

## Secret masking is structural

A masked value is **absent**, not styled out and not truncated. The string never enters the output at all, so a report cannot leak one by being copied, screenshotted, piped to a file, or rendered by something that ignores formatting.

```
default: {"mode":"dev","fields":[{"path":"APIKey","key":"API_KEY","value":"••••••","source":"map","secret":true},...
reveal:  {"mode":"dev","fields":[{"path":"APIKey","key":"API_KEY","value":"super-secret-value","source":"map","secret":true},...
```

`cfgkit.Reveal()` is a separate, explicit option — you have to ask for it by name.

The mask is a fixed string of constant length, so it leaks nothing about the real value, **not even its length**.

Masking also applies to error text. A decode failure on a secret field reports the field, the key, the source and the expected type — and **discards the decoder's own message**, which would otherwise quote the offending value:

```
Password (DB_PASSWORD from file:.env): the supplied value is not a valid int
```

Discarding the whole message rather than one field of it is deliberate; the reasoning and the sweep of every field shape it covers are in [Types](types.md#decode-failures-are-reported-never-zeroed).

## `Check` — fail in CI, not at container start

**The situation this solves.** Someone adds a required field. They update `.env` and `.env.staging`, and miss `.env.production`. Everything passes review, because nothing in the pipeline reads production's config. The deploy goes out on a Friday, the container starts, the config load panics, and the panic happens *before* logging is initialised — so all anyone sees is a restart loop with no message.

`Check` makes that a red build on the pull request instead.

```go
func TestConfigIsValid(t *testing.T) {
	if err := cfgkit.Check[Config](cfgkit.WithSources(
		cfgkit.FromFiles(".env.production"),
	)); err != nil {
		t.Fatal(err)
	}
}
```

`Check` runs the entire pipeline — sources, binding, derive, validate — and reports every problem **without constructing your application**. It returns only an error, so there is no configuration to accidentally use.

```
cfgkit: 2 problem(s):
Port (PORT from map): "eighty" is not a valid int
DBURL (DATABASE_URL) is required but no source supplied it
```

Why this matters: in the projects `cfgkit` was written for, configuration loaded *before* observability was initialised. A bad value surfaced only as a supervisor FATAL in container logs, after the image was deployed. `Check` in a pipeline turns that into a red build.

Run it once per environment:

```go
for _, env := range []string{"dev", "staging", "production"} {
	t.Run(env, func(t *testing.T) {
		if err := cfgkit.Check[Config](
			cfgkit.WithSources(cfgkit.FromFiles(".env."+env)),
			cfgkit.WithMode(cfgkit.ModeProd),
		); err != nil {
			t.Error(err)
		}
	})
}
```

## `Document` — generate the contract file

**The situation this solves.** Your repository has a `.env.example` that somebody wrote eighteen months ago. Since then, four keys were added and two renamed. Nobody updated it, because nothing forces them to. A new developer copies it, fills it in, and the app fails on a key the file never mentioned. In one real repository the committed sample still contained a former employee's absolute home directory path.

The file drifts because it is maintained by hand and nothing checks it. `Document` removes the hand.

**Where to use it:** a `go:generate` line or a small `cmd/`, plus a CI step that regenerates and diffs. Then the contract cannot drift, because drifting fails the build.

The struct already holds every fact a `.env.example` needs: the key, whether it is required, the default, whether it is a secret, and a description.

```go
f, _ := os.Create(".env.example")
defer f.Close()
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

The status line composes every obligation the field carries: `REQUIRED`, `REQUIRED, must not be empty`, `secret — do not commit a real value`, `formerly OLD_KEY`, `the value is a PATH to a file holding the real value`.

**Secrets are emitted with an empty value regardless of their default.** A contract file is committed, and a generator that wrote a real credential would be a leak with a schedule.

Defaults shown are the ones a reader would actually get: `Document` runs `Defaults()` and the `default:` tags first, so a computed default appears exactly as it will behave.

### Why this matters more here than anywhere else

Output is **byte-stable** across runs, so CI can regenerate and diff:

```sh
go run ./cmd/gen-env-example > .env.example.new
diff .env.example .env.example.new || { echo "contract is stale — regenerate it"; exit 1; }
```

And `dotenvctl` already treats `.env.example` as a contract:

```sh
dotenvctl matrix --contract .env.example   # exit 1 when any environment lacks a key
```

Together the loop closes, and neither half exists elsewhere:

**struct → `.env.example` → every environment checked → red build**

Without generation, that contract file is maintained by hand and drifts. In one real repository the committed sample still hardcoded a developer's absolute path.

## Gotchas

**A structured source that writes an identical value is invisible.** Provenance for structured sources is recovered by observing which leaves changed, so a source assigning the value already present leaves the earlier origin standing. The value is the same either way; only the reported name differs.

**Fields of a nil optional section are absent from the record.** When a `*Struct` section is pruned to nil because no source touched it ([Tags](tags.md#optional-sections--when-a-struct-is-nil)), its fields leave `Fields()`, `Explain` and `JSON` — reporting a value for a field of a nil section would describe memory that no longer exists. A field missing from `Explain` that you expected is usually a section that did not survive.

**`Explain` writes to any `io.Writer` and returns its error.** A closed pipe or full disk is reported rather than swallowed.

**A field bound from a structured source shows the source name, not `default`.** If you see `default` where you expected your JSON, the document did not actually set that field — check the `json:` tag.

**`Document` reports an unparseable `default:` tag** instead of emitting a contract file with a wrong value in it.
