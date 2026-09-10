# Validation

Validation is **Go**, not a tag language. Three optional interfaces, ten rule helpers, and any third-party rule catalogue you want to plug in.

## The three hooks

```go
func (c *Server) Defaults()       { c.Port = 8080 }            // phase 1
func (c *Server) Derive() error   { c.DSN = build(c); return nil }  // phase 4
func (c *Server) Validate() error { return check(c) }          // phase 5
```

All three run **depth-first, children before parents**, so a parent's hook always sees finished children. Any struct in the tree may implement any of them.

### `Defaults` — the reason zero-config works

```go
func (s *Server) Defaults() {
	s.Port = 8080
	s.ReadTimeout = 15 * time.Second
	s.Origins = []string{"http://localhost:3000"}
}
```

Preferred over `default:` tags because it is **typed and refactor-safe** — a renamed field is a compile error rather than a silently dropped default — and because it can compute: `s.TmpDir = filepath.Join(root, "tmp")`.

Both mechanisms may coexist. `Defaults()` runs first; a `default:` tag then fills only fields still at their zero value.

### `Derive` — values computed from other values

```go
func (d *derived) Derive() error {
	d.DSN = fmt.Sprintf("postgres://%s:%d/app", d.Host, d.Port)
	return nil
}
```

```
postgres://db.internal:5432/app err: <nil>
```

`Derive` runs **after** all sources are bound and **before** validation, so it may read any bound field and its output is validated like everything else. Tag the computed field `env:"-"` so no stray environment variable can overwrite it.

This is where "if `DATABASE_URL` is set, explode it into components; otherwise assemble it from components" lives — the bidirectional case every real service eventually needs.

### `Validate` — report everything

```go
func (c *Config) Validate() error {
	return errors.Join(
		cfgkit.Range("Port", c.Port, 1, 65535),
		cfgkit.NotEmpty("Name", c.Name),
	)
}
```

Use `errors.Join`. Returning on the first failure makes a misconfigured deploy a guessing game of one fix per restart.

## The rule helpers

Ten helpers. Each returns a plain `error`, so they compose with `errors.Join` and with any hand-written check beside them. They are **combinators, not a language**.

Here is what each one is for, in plain terms, with the situation that makes you want it.

### `Required` — "this must be set, or the app is broken"

```go
cfgkit.Required("DatabaseURL", c.DatabaseURL)
```

**Use it when:** the application genuinely cannot function without the value. A database URL, a signing key, an upstream API endpoint.

**Do not use it for anything with a sensible default.** A required field with a default is a contradiction, and it destroys the zero-input property that lets someone clone your repository and run it. Ask: *if this is missing, is the right response to refuse to start?* If the answer is "no, just use 8080", it is not required.

Most of the time you want the `env:"KEY,required"` tag instead, which reports the same thing one layer earlier and names the key an operator would set. Reach for the helper when the requirement is conditional or computed.

### `NotEmpty` — "the line exists but nobody filled it in"

```go
cfgkit.NotEmpty("APIKey", c.APIKey)
```

**Use it when:** the difference between *absent* and *blank* matters to the person fixing it.

`DB_PASSWORD=` in a `.env` file is a declared but unfilled value. Someone added the line, meant to come back, and did not. Telling them "DB_PASSWORD is required" sends them looking for a line that is already there — they add a second one, get confused, and lose ten minutes. Telling them it is empty points at the actual problem.

**The everyday scenario:** a teammate copies `.env.example` to `.env` and starts the app. Every key exists; none has a value. `NotEmpty` produces a list of exactly what to fill in.

### `RequiredIn` — "optional on a laptop, mandatory in production"

```go
cfgkit.RequiredIn(cfgkit.ModeProd, c.mode, "SentryDSN", c.SentryDSN)
```

**Use it when:** a value is genuinely optional during development but its absence in production is an incident waiting to happen.

**Real examples:** error tracking (Sentry, Bugsnag), a metrics endpoint, an S3 bucket for uploads, an SMTP host. You do not want to force every developer to register for Sentry before they can run `go run .` — but shipping to production with no error reporting means the first crash is invisible.

This is the helper that lets you have both. Without it you must choose: either the field is required and a fresh clone cannot run, or it is optional and production is silently degraded.

### `RequiredWhen` — "these two fields depend on each other"

```go
cfgkit.RequiredWhen(
	"Cloudflare", s.Cloudflare != nil,
	"kind=cloudflare", s.Kind == ServeCloudflare,
)
```

**Use it when:** one field decides whether another is meaningful. This is the tagged-union shape, and it turns up far more often than people expect.

**Real examples:**

- `STORAGE_KIND=s3` needs the S3 credentials; `STORAGE_KIND=local` must not have them, or somebody will believe uploads are going to S3 when they are on a disk that vanishes with the container.
- `AUTH_MODE=oidc` needs an issuer URL; `AUTH_MODE=none` must not.
- `CACHE=redis` needs `REDIS_URL`.

**Why both directions matter.** The forward direction ("you said S3, so give me credentials") catches the obvious mistake. The reverse direction ("you said local, so why are S3 credentials set?") catches the dangerous one: leftover configuration from a previous mode, which reads as working and behaves differently from what the operator believes.

Without this helper you write the same eight-line `if` block in every service, and half the time you write only the forward half.

### `OneOf` — "that is not one of the choices"

```go
cfgkit.OneOf("LogFormat", c.LogFormat, "json", "text")
```

**Use it when:** the set of valid values is only known at runtime — read from a registry, a plugin list, or a database.

**When the set is fixed and known at compile time, prefer a typed constant** with its own `UnmarshalText` (see [Types](types.md#the-escape-hatch)). That turns a bad value into a decode error naming the field, one layer earlier, and it makes the valid set visible in your code rather than buried in a validation call. `OneOf` is the fallback for sets you cannot express as constants.

### `Range` — "that number cannot possibly be right"

```go
cfgkit.Range("Port", c.Port, 1, 65535)
cfgkit.Range("MaxConns", c.MaxConns, 1, 500)
```

**Use it when:** a number outside a window will fail later, further away, in a way that is harder to diagnose.

**The classic:** `PORT=70000`. Without this rule the application starts, calls `net.Listen`, and dies with a message about an invalid port from deep inside the standard library — with no indication that a config value caused it. With the rule, you get `Port is 70000, want between 1 and 65535` before anything opens a socket.

The same applies to pool sizes, retry counts, and timeouts: catch the impossible value at the boundary, not at the point of use.

### `Matches` — "that does not look like what I asked for"

```go
cfgkit.Matches("TenantSlug", c.TenantSlug, regexp.MustCompile(`^[a-z0-9-]+$`))
```

**Use it when:** a value's *shape* matters and getting it wrong produces a confusing downstream failure.

**Real examples:** a slug that becomes part of a URL or a DNS name, a prefix used to build database table names, an ID format you control. Catching `My Tenant!` at load time is much kinder than watching a query fail because a table name contains a space.

Do not reach for it to validate emails or URLs — those have proper parsers, and a regexp that half-works is worse than none. Use a typed field with `UnmarshalText`, or a rule catalogue (below).

### `MutuallyExclusive` — "pick one, not both"

```go
cfgkit.MutuallyExclusive(
	cfgkit.Set{Name: "DATABASE_URL", Value: c.URL},
	cfgkit.Set{Name: "DATABASE_HOST", Value: c.Host},
)
```

**Use it when:** two settings express the same thing in different forms, and having both means one is being silently ignored.

**The everyday scenario:** you support both a full `DATABASE_URL` and separate `DB_HOST`/`DB_PORT`/`DB_NAME` parts. An operator sets the URL, then later adds the host because they forgot. Your code prefers one of them. The other does nothing — but the operator believes they changed the host, and spends an afternoon wondering why the change had no effect.

Refusing to start is kinder than picking a winner silently.

### `AtLeastOneOf` — "I need at least one way to reach it"

```go
cfgkit.AtLeastOneOf(
	cfgkit.Set{Name: "DATABASE_URL", Value: c.URL},
	cfgkit.Set{Name: "DATABASE_HOST", Value: c.Host},
)
```

The other half of the pair above. **Use it when:** several fields are alternative routes to the same requirement, and none of them individually can be marked `required`.

Paired with `MutuallyExclusive`, the two express "exactly one of these" — a shape no single tag can capture.

Both take `Set{Name, Value}` pairs because Go cannot recover a field's name from its value, and an error naming *"arg 2"* is not worth printing.

### `NotWeakSecret` — "you shipped the placeholder to production"

```go
cfgkit.NotWeakSecret(c.mode, "EncryptionKey", c.EncryptionKey, "change-me-internal-key")
```

**This is the most valuable rule in the list, and the one people do not think to write.**

**The situation it prevents.** Your `.env.example` contains `ENCRYPTION_KEY=__CHANGE_ME__` so a new developer can start immediately. Someone deploying in a hurry copies that file to the server, fills in the database URL and the port, and misses the encryption key. The application starts. It works. It encrypts real customer data with a key that is published in your repository, and every attacker who reads your `.env.example` can decrypt it.

Nothing fails. There is no error, no warning, no symptom — until it matters, at which point the fix is a rekey migration over data that is already encrypted.

**What the rule does:** outside development, it refuses three kinds of value.

| Rejected | Why |
|---|---|
| `""` | the secret was never filled in |
| `__ANYTHING__` | the placeholder convention, matching `^__[A-Z0-9_]+__$` |
| a known default you name | `"change-me-internal-key"`, `"password"`, `"secret"` — whatever your templates use |

**In development it does nothing**, deliberately. That asymmetry is the whole point: the placeholder is what lets a fresh clone run without setup, so it must stay legal on a laptop and become fatal in production.

**Where to use it:** every field that holds a credential. Encryption keys, JWT signing secrets, internal API keys, webhook signing secrets, admin passwords. If leaking it would matter, guard it.

The offending value is never echoed in the error message — it is a credential, and an error message is a thing that gets logged.

The placeholder pattern is shared with `dotenvctl`, which reports such values as `!` in its drift matrix, so the same convention is enforced by both tools.

### Anything else is a five-line `if`

The library gives you combinators, not a language. A rule that is not on this list belongs in your `Validate()` method as ordinary Go:

```go
func (c *Config) Validate() error {
	var errs []error
	if c.MaxUploadMB > 0 && c.MaxUploadMB > c.DiskQuotaMB {
		errs = append(errs, fmt.Errorf("MaxUploadMB (%d) exceeds DiskQuotaMB (%d)", c.MaxUploadMB, c.DiskQuotaMB))
	}
	return errors.Join(errs...)
}
```

That is a feature, not a gap. A rule specific to your domain is clearer as code than as a call to a generic helper that almost fits.

## Conditional rules — the discriminated union

This is the case that previously needed a configuration language:

```go
type Serving struct {
	Kind       ServeMode    `env:"SERVE_KIND" default:"direct"`
	Cloudflare *PurgeConfig `env:",prefix=CF_"`
}

func (s *Serving) Validate() error {
	return cfgkit.RequiredWhen(
		"Cloudflare", s.Cloudflare != nil && s.Cloudflare.Token != "",
		"kind=cloudflare", s.Kind == ServeCloudflare,
	)
}
```

One call asserts **both directions**:

```
cfgkit: 1 problem(s):
Cloudflare: is required when kind=cloudflare, but it was not set (required_when)
cfgkit: 1 problem(s):
Cloudflare: must not be set unless kind=cloudflare (required_when)
valid: <nil>
```

| `Kind` | `Cloudflare` | Result |
|---|---|---|
| `cloudflare` | set | ✅ |
| `cloudflare` | missing | required-when error |
| `direct` | set | must-not-be-set error |
| `direct` | missing | ✅ |

The discriminant is a **real Go type** with real constants:

```go
type ServeMode string

const (
	ServeCloudflare ServeMode = "cloudflare"
	ServeDirect     ServeMode = "direct"
)
```

so the comparison cannot be a mistyped string literal. A tag-based `validate:"required_if=Kind cloudflare"` cannot promise that: rename the field or change the constant and the rule silently stops matching.

## Why not a tag DSL

Several libraries put rules in struct tags. It was considered and rejected:

- **It cannot be type-checked.** Rename `Kind` and the rule stops matching, silently.
- **It cannot be debugged.** You cannot set a breakpoint inside a string.
- **It re-introduces hardcoded values** where the compiler cannot see them — field names and enum values inside tag strings.
- **It always grows into a bad programming language.** Conditionals arrive eventually, embedded in string literals.

Go already has a language. `Validate()` has the whole of it.

## Third-party rule catalogues

Want `email`, `url`, `uuid`, `cidr`? Plug one in. The `Validator` interface is a seam, so **the dependency stays in your module**:

```go
func (c *AppConfig) Validate() error {
	return validator.New().Struct(c)
}
```

Mixing both styles is normal and expected — tags for the catalogue rules, Go for the conditional logic tags express badly:

```go
type SMTP struct {
	From string `env:"SMTP_FROM" validate:"required,email"`
	Port int    `env:"SMTP_PORT" validate:"gte=1,lte=65535"`
	User string `env:"SMTP_USER"`
	Pass string `env:"SMTP_PASS" secret:"true"`
}

func (s *SMTP) Validate() error {
	return errors.Join(
		validator.New().Struct(s),                                    // catalogue rules
		cfgkit.RequiredWhen("Pass", s.Pass != "", "user is set", s.User != ""),  // conditional
	)
}
```

This is strictly better than a library embedding the validator: a project that wants the catalogue gets it, a project that does not never compiles it, and `cfgkit` keeps its single dependency.

## Gotchas

**A zero value from a source is a real value, so `Required` will not fire on it.** `PORT=0` binds `0`, which passes `Required` because the key resolved — but fails `Range("Port", p, 1, 65535)`. Use a range or `NotEmpty` when zero is meaningless for the field, rather than expecting `Required` to catch it.

**A map is validated as a whole, never per entry.** There is no `required` for one key inside a `map[string]string`, because the whole point of a map is that the key names are unknown at compile time. `notempty` on a map field only asserts that the source supplied a non-empty *string* — check the entries you actually depend on inside `Validate`:

```go
func (a *App) Validate() error {
	if _, ok := a.Limits["default"]; !ok {
		return errors.New("Limits: a \"default\" entry is required")
	}
	return nil
}
```

**`Validate` on an optional section runs only when the section exists.** A `*Struct` field is nil unless a source set something beneath it ([Tags](tags.md#optional-sections--when-a-struct-is-nil)), and a nil section is not validated. That is the division of labour: the pointer answers *did you intend this feature*, `Validate` answers *is it complete*. A half-configured section therefore reports its own missing field, while an unused one stays silent.

**`Validate` runs even when binding failed.** Errors from every phase are collected, so you may see decode errors and validation errors together. Guard against zero values if a rule would panic on them.

**`Derive` errors are collected, not fatal-on-first.** A failing `Derive` does not stop later structs from deriving; all failures are reported together.

**A rule helper needs the current mode passed in.** `RequiredIn` and `NotWeakSecret` take it as an argument rather than reading a global, because there is no global. Get it from the `Result`, or pass the mode your application already resolved.

**Validation cannot change values.** It reports. If you want to normalise something — trim a string, default a port — do it in `Derive`, which runs first.
