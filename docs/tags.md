# Tags

Every tag is optional except `env:` on a leaf you want bound from a flat source.

## Reference

| Tag | Meaning |
|---|---|
| `env:"KEY"` | the flat key that fills this field |
| `env:",prefix=P_"` | on a **struct** field: prepend `P_` to every key beneath it |
| `env:"KEY,required"` | the key must resolve |
| `env:"KEY,notempty"` | must resolve **and** not be the empty string (implies `required`) |
| `env:"KEY,file"` | the value is a **path**; read the file at it |
| `env:"KEY,unset"` | remove the key from the environment after reading |
| `env:",init"` | on a **struct pointer**: always allocate it, even when no source sets anything beneath |
| `env:"-"` | never bound from any source; set by `Derive` or left at its default |
| `json:"name"` | the nested path a **structured** source fills this field from |
| `default:"v"` | a default, parsed by the field's own decoder |
| `secret:"true"` | mask in `Explain`, `JSON`, and error text |
| `delim:";"` | slice element / map entry separator (default `,`) |
| `kvdelim:"="` | map key-to-value separator (default `:`) |
| `flag:"port"` | the command-line flag that may set this field |
| `was:"OLD_KEY"` | a former key name; comma-separate for several |
| `doc:"text"` | a one-line description, used by `Document` |

Options after the key combine freely: `env:"DB_PASSWORD_FILE,file,required"`.

## When you reach for each one

The short version, before the detail below.

| Tag | Reach for it when |
|---|---|
| `env:"KEY"` | always — this is how a field gets a value from the environment or a `.env` file |
| `env:",prefix="` | a section's keys share a prefix and repeating it on every field is noise |
| `required` | the app genuinely cannot run without it and refusing to start is the right answer |
| `notempty` | someone will copy `.env.example` and forget to fill this in |
| `file` | you deploy to Docker Swarm or Kubernetes and mount secrets as files |
| `unset` | this process spawns child processes and the secret must not follow them |
| `env:",init"` | an optional section whose defaults are a complete working setup, so callers need no nil check |
| `env:"-"` | `Derive` computes it and no environment variable should be able to override it |
| `json:` | the value can also come from a JSON, Pkl, or YAML document |
| `default:` | a one-line literal default; use `Defaults()` when it is computed or typed |
| `secret:` | leaking the value would matter — so it must never appear in a log or a report |
| `delim:` | the value is a list or a map and commas appear inside the items |
| `kvdelim:` | the value is a map and its KEYS contain colons |
| `flag:` | an operator will want to override this for one run without editing a file |
| `was:` | you are renaming a key that live deployments already set |
| `doc:` | you generate `.env.example` and want the reader to know what the key is for |

## `delim` and `kvdelim` — separators for compound values

A slice and a map both arrive as ONE string, so something has to say where one element ends and the next begins.

```go
Ports []int             `env:"PORTS"`                        // "80,443"
Rules map[string]string `env:"RULES"`                        // "a:1,b:2"
Odd   map[string]string `env:"ODD" delim:";" kvdelim:"="`    // "a=1,2,3;b=4"
```

Reach for them only when the **data** contains the default separator. `delim:";"` when a list item contains a comma; `kvdelim:"="` when a map key contains a colon.

Reach for `delim:` on a map when values contain commas — including when the `.env` line is quoted, because quoting is resolved by the parser before cfgkit ever sees the value.

You do **not** need `kvdelim` just because a map *value* contains a colon — an entry splits on the FIRST separator only, so `primary:postgres://db:5432/app` already parses correctly. That is the most common misreading of this tag; see [Types](types.md#the-gotcha-only-the-first-separator-splits).

## Keys are declared, never guessed

This is the design decision that shapes the whole tag system.

```go
type HyperDX struct {
	LogsSourceID string `env:"HYPERDX_LOGS_SOURCE_ID"`
}
```

A library *could* try to derive that key by splitting the Go path on underscores. It cannot work, because `_` carries two meanings in the same string — it separates nesting levels **and** words inside one name. That single key has at least six structurally valid readings:

```
HYPERDX_LOGS_SOURCE_ID
   ├── HyperDX.Logs.Source.ID
   ├── HyperDX.Logs.SourceID
   ├── HyperDX.LogsSource.ID
   ├── HyperDX.LogsSourceID      ← the real field
   ├── HyperDXLogs.SourceID
   └── HyperDXLogsSourceID
```

It is ambiguous in the other direction too: `HyperDX.LogsSourceID` and `HyperDX.Logs.SourceID` are different fields that would generate the *same* key.

**And the failure that actually costs money is renaming.** If keys were derived from field names, renaming `APIKey` to `Key` would silently change the variable from `HYPERDX_API_KEY` to `HYPERDX_KEY`. The code compiles. The tests pass, because tests set config directly. Production keeps exporting the old name, the field arrives empty, and the first symptom is an integration failing at 3am.

Declaring the key costs one tag and removes all of it. Real key names do not mirror struct shape anyway — `PORT` usually has no `SERVER_` prefix, and `PGHOST` was named by a vendor.

## Prefixes

**Reach for this when** a section's keys all start the same way and repeating it fifteen times is noise you will eventually get wrong. It also makes a section reusable: the same `Redis` struct can be mounted twice under different prefixes for a cache and a queue, without the struct knowing.

Declare a prefix once on the parent:

```go
type Config struct {
	HyperDX HyperDX `env:",prefix=HYPERDX_" json:"hyperdx"`
}

type HyperDX struct {
	LogsSourceID string `env:"LOGS_SOURCE_ID" json:"logsSourceId"`
	APIKey       string `env:"API_KEY"        json:"apiKey" secret:"true"`
}
```

Prefixes accumulate down the tree, so a nested section inside a prefixed section gets both. The key recorded in `Explain` is always the full one an operator would set — `HYPERDX_LOGS_SOURCE_ID`.

## Two tags, one per layer

A field bound by both a flat and a structured source carries a tag for each, and they constrain each other in no way at all:

- `json:` says how a **structured** source finds the field — by nested path.
- `env:` says how a **flat** source finds it — by key.

A project that never touches JSON or YAML omits the `json:` tags; a project fed only by Pkl omits the `env:` ones. Neither audience pays for the other.

## `required` and `notempty` are different failures

```go
type Config struct {
	Token string `env:"TOKEN,required"`
	Name  string `env:"NAME,notempty"`
}
```

```
cfgkit: 1 problem(s):
Token (TOKEN) is required but no source supplied it
cfgkit: 1 problem(s):
Name (NAME) is required but resolved to an empty value
```

| Rule | Fails when | The operator must |
|---|---|---|
| `required` | no source supplies the key at all | add the variable |
| `notempty` | a source supplies it, but the value is `""` | fill in a line that already exists |

`DB_PASSWORD=` in a `.env` file is *declared but unfilled*. Reporting that as "required" sends someone looking for a line that is already there.

## `file` — Docker and Kubernetes secrets

**Reach for this when** you deploy to Docker Swarm or Kubernetes. Both mount secrets as files by convention, and a value in a file is meaningfully safer than one in the environment: it does not appear in `docker inspect`, it is not readable from `/proc/<pid>/environ` by anything that can see the process, and it is not inherited by child processes. Without this tag you would need a wrapper script to read the file and re-export it — which puts the secret back in the environment and undoes the point.

Container platforms mount a secret as a **file** and pass the *path* in the environment:

```
DB_PASSWORD_FILE=/run/secrets/db_password
```

```go
Password string `env:"DB_PASSWORD_FILE,file" secret:"true"`
```

```
"s3cret" err=<nil>
```

The trailing newline is trimmed — every tool that writes these files adds one, and no secret intends it. A **missing** path is a normal miss, so a later source or the default still applies; a path that exists but cannot be read is a `SourceError` and aborts, because an unreadable secret must never look like an unset one.

This is the only way to consume a mounted secret without a wrapper script, and it is why the pattern exists: a value in a file never appears in `docker inspect`, in `/proc/<pid>/environ`, or in a child process.

## `unset` — do not let children inherit a secret

**Reach for this when** your process starts other processes. A CLI that shells out to `git` or `gh`, a build tool that runs compilers, a job runner that executes user-supplied commands — every one of them inherits your entire environment by default, including credentials they have no business seeing. A leaked token in a subprocess is a leak in that subprocess's logs, its crash dumps, and anything it starts in turn.

```go
Token string `env:"ONE_SHOT_TOKEN,unset" secret:"true"`
```

After the value is read, the key is removed from the process environment, so any child process started later cannot see it.

**Two limits.** This is the single documented exception to *"`Load` never writes to `os.Environ`"*, and it touches only its own key. And a second `Load` in the same process sees a different environment — a test that loads twice must set the variable again.

## `was` — rename a key without breaking deployments

**Reach for this when** you want to rename an environment variable that live deployments already set. Normally that rename is a coordinated change: you cannot merge the code until every environment is updated, and if one is missed the value silently arrives empty. With `was`, the rename is safe to merge immediately — old deployments keep working, and the provenance tells you which ones still need migrating. Remove the tag once `Explain` stops reporting the deprecated origin anywhere.

```go
APIKey string `env:"HYPERDX_API_KEY" was:"HYPERDX_KEY"`
```

```
from-old-name
map (deprecated key HYPERDX_KEY)
```

The current key is tried across every source first. Only when all of them decline is a former name tried. A value arriving through the old name is **flagged in the provenance**, so an operator can see what to migrate before the tag is removed. `was` takes a comma-separated list for several former names.

## `default` and the zero value

A `default:` applies only when **no source mentions the key at all**. A source that supplies `""`, `false` or `0` wins:

```go
Debug bool `env:"DEBUG" default:"true"`
```

`DEBUG=false` gives you `false`. If it did not, a feature defaulting to on could never be turned off. See [Sources](sources.md#a-source-always-wins--even-with-a-zero-value) for the full table.

## Optional sections — when a `*Struct` is nil

**Reach for a pointer section when** a feature is switched on by being configured at all:

```go
type App struct {
	Port int   `env:"PORT" default:"8080"`
	SMTP *SMTP `env:",prefix=SMTP_"`     // nil = this app does not send email
}
```

so your code can write the check every Go programmer writes without being told:

```go
if cfg.SMTP != nil {
	startEmailSender(cfg.SMTP)
}
```

**The rule: a section is allocated when a SOURCE set at least one field beneath it, and is nil otherwise.**

| What a source sets | `cfg.SMTP` | `Host` | `Port` (default 587) |
|---|---|---|---|
| nothing | **`nil`** | — | — |
| `SMTP_HOST=mail.example.com` | allocated | `mail.example.com` | `587` |
| `SMTP_USER=bob` | allocated | `""` | `587` |
| both | allocated | set | `587` |

**Any descendant, not all of them.** Setting `SMTP_HOST` says *I mean to use email*. Requiring every field would make a section with ten optional fields impossible to switch on without setting all ten.

**Only a source counts as intent.** A `default:` tag or a `Defaults()` method does **not** allocate the section. Otherwise any section holding one default would always be non-nil, and the nil check would be useless again.

### Two questions, two mechanisms

| Question | Answered by |
|---|---|
| Did the operator intend to use this feature? | the pointer — nil or not |
| Is their configuration complete? | `Validate()` on the section, which only runs when it exists |

```go
func (s *SMTP) Validate() error {
	return cfgkit.Required("Host", s.Host)
}
```

So `SMTP_USER=bob` alone gives a non-nil section that fails with *"Host is required"* — the message that person needs. An always-allocated section would instead start an email sender pointed at an empty host, failing on the first send far from the cause.

### `,init` — always allocate

Some sections are meaningful unconfigured, because their defaults are a complete working setup:

```go
type App struct {
	Cache *Cache `env:",prefix=CACHE_,init"`   // never nil
}
```

`cfg.Cache.TTL` then needs no guard.

### Gotchas

**A `default:` inside the section does not bring it into existence.** This is the one that surprises people:

```go
type SMTP struct {
	Host string `env:"HOST"`
	Port int    `env:"PORT" default:"587"`   // ← does NOT allocate SMTP
}
```

Set nothing and `cfg.SMTP` is nil, defaults and all. If a default counted as intent, every section holding one would always be non-nil — which is the behaviour this replaces.

**Nested sections prune deepest-first.** An outer section whose only content is an empty inner section is itself pruned, because nothing beneath it was ever sourced. Set one field three levels down and every section on the path is allocated.

**Pruned fields leave the provenance record.** `Explain` does not list `SMTP.Host` when `SMTP` is nil — reporting a value for a field of a nil section would describe memory that no longer exists. If a field you expected is missing from `Explain`, check whether its section survived.

**`Validate` on a pruned section never runs**, so a `Required` rule inside an unused optional section cannot fail a load. That is the point, but it means a section's rules protect it only once somebody starts configuring it.

**`Derive` on a pruned section never runs either.** A computed field inside an optional section stays at its zero value when the section is absent — which is moot, since the section is nil.

**A non-pointer nested struct is never pruned.** Only `*Struct` participates. If you want the nil semantics, the field must be a pointer.

**`,init` goes on the POINTER field, not on the section type.** It is an option on the parent's `env:` tag:

```go
Cache *Cache `env:",prefix=CACHE_,init"`     // right
```

There is no way for a type to declare "always allocate me" from its own definition, because the same type may be optional in one config and mandatory in another.

### Prior art

The ecosystem is split, and both major libraries ship the same knob under opposite defaults: `go-envconfig` allocates by default with `noinit` to opt out; `caarlos0/env` stays nil with `,init` to opt in. cfgkit follows `caarlos0` on both the direction and the tag name — nil is what the language already says a pointer means, and the shared name means anyone arriving from that library already knows it.

## `secret` — masking is structural

**Reach for this when** leaking the value would matter: any password, token, signing key, or connection string containing credentials.

The reason it is worth tagging rather than just being careful: diagnostics get shared. Someone debugging a deployment runs your config dump and pastes it into a ticket, a chat, or a screenshot. Without the tag, that paste contains a live credential and nobody notices for months. With it, the value is **absent** from the output — not styled out and not truncated, so it cannot leak through a copy, a screenshot, or a renderer that ignores formatting.

Tagging also withholds the value from error messages, which is where credentials most often escape: an error is a thing that gets logged.

See [Provenance](provenance.md#secret-masking-is-structural).

## `doc` — the source text for the contract file

**Reach for this when** you generate `.env.example` with `Document`. The tag becomes the comment above the key, so the person filling in that file learns what it is for without reading your source.

Write it for the operator, not the developer: *"Postgres connection string"* rather than *"the DSN passed to sql.Open"*. The audience is whoever is deploying at 2am.

See [Provenance](provenance.md#document--generate-the-contract-file).

## Gotchas

**A leaf with no `env:` tag is not an error.** It simply is not bound from a flat source. It may still be filled by a structured source through its `json:` tag, or computed in `Derive`.

**`env:"-"` means never bound.** Use it for values that `Derive` computes, so a stray environment variable cannot overwrite them.

**Unexported fields are skipped silently.** A config struct may hold private state, and reflection cannot set it.

**A type with its own unmarshaler is a leaf, not a section.** `time.Time` and `*url.URL` are structs, but their own parser owns the whole string, so `cfgkit` binds them from one key instead of walking into their fields.
