# API reference

Every exported function and type, what it is for, and **when you would reach for it**. Nothing here is omitted — if it is exported, it is on this page.

**Contents:** [Loading](#loading) · [Options](#options) · [Flat sources](#flat-sources) · [Structured sources](#structured-sources) · [Interfaces you implement](#interfaces-you-implement) · [Interfaces you satisfy](#interfaces-you-satisfy) · [Capabilities](#capabilities) · [Result](#result) · [Rules](#rules) · [Errors](#errors) · [Mode](#mode)

## Loading

### `Load[T](opts...) (*T, *Result, error)`

Builds a `T` from the configured sources and returns it, the record of where every value came from, and every problem found.

**Use it when:** your application starts. This is the entry point, and usually the only one you call.

```go
cfg, res, err := cfgkit.Load[Config](
	cfgkit.WithSources(cfgkit.FromFiles(".env"), cfgkit.FromEnviron()),
)
```

**Call it once, in your bootstrap, and pass `cfg` down.** There is no global to read it back from — deliberately, so tests can hold several configurations at once and a library can never depend on a hidden singleton.

Ignore `res` with `_` if you do not want provenance. Never ignore `err`.

### `Check[T](opts...) error`

Runs the entire pipeline and reports problems **without returning the configuration**.

**Use it when:** you want configuration errors to fail a build rather than a deployment. In a test, in CI, or behind a `myapp config check` subcommand for operators.

```go
func TestProductionConfigIsValid(t *testing.T) {
	if err := cfgkit.Check[Config](
		cfgkit.WithSources(cfgkit.FromFiles(".env.production")),
		cfgkit.WithMode(cfgkit.ModeProd),
	); err != nil {
		t.Fatal(err)
	}
}
```

It returns only an error, so there is no configuration to accidentally use — which is the point. The alternative is discovering a missing key when a container restart-loops before logging exists.

### `Document[T](w, opts...) error`

Writes a `.env.example` describing every key the configuration binds.

**Use it when:** you keep a `.env.example` in the repository. Generating it is the only way it stops drifting from the code.

```go
//go:generate go run ./cmd/gen-env-example

f, _ := os.Create(".env.example")
defer f.Close()
cfgkit.Document[Config](f)
```

Pair it with a CI step that regenerates and diffs. Secrets are emitted empty; output is byte-stable across runs.

## Options

All are passed to `Load`, `Check`, and `Document`.

### `DefaultSources(extra ...any) Option`

Configures the conventional chain: `.env`, `.env.local`, `.env.<mode>`, `.env.<mode>.local`, then the process environment, then any `extra` sources — and resolves the mode from those files as well as from the environment.

**Use it when:** your project uses the conventional `.env` layout, which is most of them. It replaces about twelve lines every service otherwise writes identically, and it gets the mode right, which writing it by hand does not.

```go
cfg, res, err := cfgkit.Load[Config](cfgkit.DefaultSources())
```

`extra` sources sit at the **top**, above the environment. A `WithSources` list sits at the **bottom**, below the whole chain. Every file is optional; the whole chain may be absent.

Full behaviour, including the mode chicken-and-egg and why `.env.local` is skipped in test mode: [Sources](sources.md#the-conventional-chain-in-one-call).

### `DefaultSourcesIn(dir string, extra ...any) Option`

`DefaultSources` rooted at `dir` rather than the working directory.

**Use it when:** the configuration is not beside the process — a test fixture directory, or a service in a monorepo run from the repository root.

### `WithSources(sources...) Option`

Sets the ordered source list. A **later** source overrides an earlier one, regardless of kind.

**Use it when:** always, unless you truly want defaults only.

```go
cfgkit.WithSources(
	cfgkit.FromJSON(embedded),      // lowest
	cfgkit.FromFiles(".env"),
	cfgkit.FromEnviron(),           // highest
)
```

The order you write is the precedence. Put the environment last so an operator can always override a file without editing it.

A source wins over a default **even when its value is `""`, `false` or `0`** — otherwise `DEBUG=false` could not turn off a feature that defaults to on. Only a key no source mentions keeps its default. See [Sources](sources.md#a-source-always-wins--even-with-a-zero-value).

### `WithMode(m Mode) Option`

Sets the mode explicitly, bypassing the environment lookup.

**Use it when:** your application already knows its environment — from a flag, a build tag, or its own bootstrap — or in a test that must be deterministic. Also use it in CI to check every environment file under `ModeProd`, so the strictest rules apply everywhere.

```go
cfgkit.WithMode(cfgkit.ModeProd)
```

### `WithModeKey(key string) Option`

Changes which environment variable supplies the mode. The default is `APP_ENV`.

**Use it when:** your organisation already standardised on a different name — `CONFIG_ENV`, `ENVIRONMENT`, `RAILS_ENV`. Adopting `cfgkit` should not force every deployment to add a variable.

```go
cfgkit.WithModeKey("CONFIG_ENV")
```

### `Reveal() Option`

Allows secret values to appear in `Explain` and `JSON` output.

**Use it when:** you are deliberately dumping configuration for local debugging and want to see the real values. Never in anything that writes to a log, a ticket, or a shared terminal.

```go
_, res, _ := cfgkit.Load[Config](srcs, cfgkit.Reveal())
res.Explain(os.Stdout)     // now shows real secrets
```

It is a separate, explicit call precisely so that leaking a secret requires an act of intent.

## Flat sources

Answer lookups by key, matched through the `env:` tag.

### `FromEnviron() Source`

Reads `os.Environ()`.

**Use it when:** always, in production. Every deployment platform — Kubernetes, Compose, systemd, Heroku, CI — passes configuration this way. Put it last so it wins.

### `FromFiles(paths...) Source`

Reads `.env` files through `ubgo/dotenv`. A later path wins; **a missing file is not an error**.

**Use it when:** local development, so nobody has to export a dozen variables by hand before running the app.

```go
cfgkit.FromFiles(".env", ".env.local")
```

`.env` is committed and documents what exists; `.env.local` is gitignored and personal.

### `FromPrefixedEnviron(prefix) Source`

Reads `os.Environ()`, matching only keys carrying `prefix`, which is stripped before matching.

**Use it when:** one process hosts several components whose short key names would collide, or when you load one configuration per tenant in a loop.

```go
for _, tenant := range tenants {
	cfg, _, _ := cfgkit.Load[TenantConfig](
		cfgkit.WithSources(cfgkit.FromPrefixedEnviron(strings.ToUpper(tenant) + "_")),
	)
}
```

`ACME_PORT` and `GLOBEX_PORT` coexist without either struct knowing about prefixes.

### `FromMap(m) Source`

Reads an in-memory map.

**Use it when:** testing. **Always prefer this over `os.Setenv`**, which makes tests order-dependent, unsafe to parallelise, and able to leak state into unrelated tests.

```go
cfg, _, err := cfgkit.Load[Config](cfgkit.WithSources(
	cfgkit.FromMap(map[string]string{"DATABASE_URL": "postgres://test"}),
))
```

### `FromFlagSet(fs *flag.FlagSet) Source`

Reads a parsed stdlib flag set — **only the flags the user actually typed**.

**Use it when:** your binary takes flags and an operator should be able to override one value for one run without editing a file.

```go
fs.Parse(os.Args[1:])
cfgkit.WithSources(cfgkit.FromEnviron(), cfgkit.FromFlagSet(fs))
```

Fields opt in with a `flag:` tag. For cobra, use [`contrib/flags-pflag`](../contrib/flags-pflag/README.md).

### `SourceFunc(name, fn) Source`

Wraps a function as a flat source.

**Use it when:** you need a backend `cfgkit` does not ship — Vault, AWS Secrets Manager, Consul, a database table, a company-internal service. This is how the catalogue stays open-ended without the library growing dependencies.

```go
secrets, err := vault.ReadAll(ctx, "secret/data/app")   // pre-load!
src := cfgkit.SourceFunc("vault", func(key string) (string, bool, error) {
	v, ok := secrets[key]
	return v, ok, nil
})
```

**Pre-load in the constructor.** `Lookup` is called once per bound field, so a function that dials the network per key turns a 200-field config into 200 round-trips at boot.

Return `("", false, err)` when the backend itself fails — never a silent miss, or an unreachable store looks like an unset variable.

### `FlagSource(name, typed) Source`

Builds a flag source from a map of flag names the user typed.

**Use it when:** you are writing an adapter for a flag package other than the standard library's — pflag, urfave/cli, or your own. Filter to typed flags yourself, then hand the map over.

```go
typed := map[string]string{}
fs.Visit(func(f *pflag.Flag) { typed[f.Name] = f.Value.String() })
return cfgkit.FlagSource("pflag", typed)
```

It exists so a flag adapter can live in its own module without pflag entering `cfgkit`'s dependencies. Most applications never call it directly.

## Structured sources

Merge nested data onto the struct, matched through the `json:` tag.

### `FromFS(fsys fs.FS, paths...) Source`

`.env` files inside an `fs.FS` — most usefully an `embed.FS`.

**Use it when:** the binary should carry its own defaults, so it runs on a machine with no config file at all and is still overridable.

```go
//go:embed defaults.env
var defaults embed.FS

cfgkit.WithSources(
	cfgkit.FromFS(defaults, "defaults.env"),  // compiled in, lowest
	cfgkit.FromFiles(".env"),                 // optional local override
	cfgkit.FromEnviron(),                     // deployment wins
)
```

Every `FromFiles` rule holds — later paths win, a missing entry is silent, `${VAR}` resolves across entries — because both share one implementation. **Paths are `fs.FS` paths**: forward slashes, never rooted, even on Windows.

### `FromStruct(name string, v any) StructuredSource`

An already-populated Go struct, merged through `json:` tags.

**Use it when:** values come from somewhere the source list cannot reach — a config service with its own client, a test fixture, a struct your own logic assembled — and need to enter the chain at a chosen precedence.

**Not a replacement for `Defaults()`**, which is how a struct supplies its own baseline: that is typed and refactor-safe, where this round-trips through JSON. A `nil` value is silent, so an optional layer needs no nil check.

### `FromJSON(b []byte) StructuredSource`

Merges a JSON object.

**Use it when:** configuration arrives as a document rather than as keys — evaluated Pkl embedded at build time, a response from a config service, a JSON blob in a ConfigMap.

```go
//go:embed config.prod.json
var embedded []byte

cfgkit.WithSources(cfgkit.FromJSON(embedded), cfgkit.FromEnviron())
```

This is the Pkl seam: `pkl eval -f json` during the build, `go:embed` the result, and the `pkl` binary never has to exist at run time.

### `StructuredFunc(name, fn) StructuredSource`

Wraps a function as a structured source.

**Use it when:** you want a format `cfgkit` does not ship — YAML, TOML, HCL, INI, or something proprietary. The parser lives in **your** module, so the library never has to add a format.

```go
cfgkit.StructuredFunc("config.yaml", func(dst any) error {
	b, err := os.ReadFile("config.yaml")
	if err != nil {
		if os.IsNotExist(err) {
			return nil       // absent is fine
		}
		return err           // present-but-broken is not
	}
	return yaml.Unmarshal(b, dst)
})
```

Your function must **leave absent fields untouched** — that is what makes layering work.

## Interfaces you implement

Optional. Any struct in the tree may implement any of them; all run depth-first, children before parents.

### `Defaulter` — `Defaults()`

Populates a struct before binding.

**Use it when:** a field has a sensible default. Which should be nearly all of them — this is what makes a fresh clone run.

```go
func (s *Server) Defaults() {
	s.Port = 8080
	s.ReadTimeout = 15 * time.Second
}
```

**Prefer this over `default:` tags** when the value is typed, computed, or a slice. A renamed field becomes a compile error rather than a silently dropped default.

### Optional sections and `,init`

A `*Struct` field is nil unless a source set something beneath it, so `cfg.SMTP != nil` means "the operator configured email". `env:",init"` forces allocation for a section whose defaults stand on their own. Full rules and the table: [Tags](tags.md#optional-sections--when-a-struct-is-nil).

### `Deriver` — `Derive() error`

Computes fields from other fields, after binding and before validation.

**Use it when:** a value is assembled rather than supplied. A DSN built from parts, a public URL from a scheme and host, a cache directory under an already-resolved root.

```go
func (d *DB) Derive() error {
	if d.DSN == "" {
		d.DSN = fmt.Sprintf("postgres://%s@%s:%d/%s", d.User, d.Host, d.Port, d.Name)
	}
	return nil
}
```

Tag the computed field `env:"-"` so no stray variable can overwrite it. This is also where "if `DATABASE_URL` is set, explode it into parts; otherwise assemble it" belongs.

### `Validator` — `Validate() error`

Checks the finished configuration.

**Use it when:** a value can be structurally valid but semantically wrong — a port out of range, two mutually exclusive settings, a placeholder secret in production.

```go
func (c *Config) Validate() error {
	return errors.Join(
		cfgkit.Range("Port", c.Port, 1, 65535),
		cfgkit.NotWeakSecret(c.mode, "Key", c.Key),
	)
}
```

Use `errors.Join`. Returning on the first failure makes a misconfigured deploy one fix per restart.

## Interfaces you satisfy

Implement these when writing an adapter. See [Writing an adapter](writing-adapters.md).

### `Source` — `Name() string`, `Lookup(key) (string, bool, error)`

The flat-source contract. `SourceFunc` implements it for you; implement it directly when your adapter needs state or its own methods.

### `StructuredSource` — `Name() string`, `Apply(dst any) error`

The structured-source contract. `StructuredFunc` implements it for you.

## Capabilities

Three optional extension points. Each may change **what a value reads as**; none may change **which source won** — that firewall is enforced in code and pinned by a test, because provenance that cannot be trusted is worse than none.

### `Transformer` / `TransformerFunc` — rewrite a value after lookup

Runs after a source supplies a value and before it is decoded. Transformers **chain** in registration order, each seeing the previous output.

**Use it when:** values arrive in a form the field's type cannot parse but a mechanical step can fix — ciphertext, base64, a value a platform wrapped in quotes, a legacy format you are migrating away from.

```go
cfgkit.WithTransform(func(key, v, source string) (string, error) {
	s, ok := strings.CutPrefix(v, "encrypted:")
	if !ok {
		return v, nil          // decline: pass through unchanged
	}
	return vault.Decrypt(s)
})
```

It receives the **key and the source name**, so a transformer can act on one origin only: decrypt values that came from Vault, leave the same key alone when it came from a local `.env`.

An error aborts the load. Return the value unchanged rather than an error when the transformer simply does not apply.

### `WithTransform(fn)` and `WithTransformer(t)`

`WithTransform` takes a plain function — the common case. `WithTransformer` takes a type, for a transformer that needs its own state: a decryption client, a cache, a connection.

### `Decoder` / `DecoderFunc` — parse a type the core does not know

Consulted **before** the built-in type set and before the `encoding.TextUnmarshaler` hatch, so a registered decoder wins over both.

**Use it when:** the type is not yours to change — a struct from a third-party package with no unmarshaler — or a type must be parsed differently *in configuration* than everywhere else in the program.

**If the type is yours, implement `encoding.TextUnmarshaler` instead.** The rule then travels with the type rather than with the load call, and every other package that parses that type gets it too.

```go
cfgkit.WithDecoder(cfgkit.DecoderFunc(func(typ reflect.Type, raw string) (any, bool, error) {
	if typ != reflect.TypeOf(pgx.ConnConfig{}) {
		return nil, false, nil     // decline
	}
	c, err := pgx.ParseConfig(raw)
	return *c, true, err
}))
```

Three return shapes: `(value, true, nil)` claims the type, `(nil, false, nil)` declines, `(nil, true, err)` claims it and rejects the value.

**A claimed type becomes a leaf.** A struct a decoder claims is bound from one key rather than walked into as a section — otherwise the decoder would never be reached. The predicate is probed with an empty string, so a decoder must decide by **type**, not by value.

### `WithDecoder(d)`

Registers a decoder. Later registrations are consulted first, so a late override wins.

### `Observer` / `ObserverFunc` — watch every resolution

The interface has one method, `ObserveResolve(f Field)`, told about each field once it resolves. Cannot change anything, which is what makes it safe to register several.

**Use it when:** auditing (record which secrets were read and from where), metrics (count fields still on their compiled-in defaults), or a startup warning about deprecated keys still in use.

```go
cfgkit.WithObserver(cfgkit.ObserverFunc(func(f cfgkit.Field) {
	if f.Secret {
		audit.Record(f.Path, f.Source)     // name and origin, never the value
	}
}))
```

**Observers receive secret values UNMASKED**, because an audit sink needs the real value. An observer that logs must consult `Field.Secret` itself — cfgkit cannot know whether a given sink is a safe place for a credential, so it does not pretend to.

### `WithObserver(ob)`

Registers an observer. All registered observers run, in registration order.

## Result

### `Result.Explain(w io.Writer) error`

Writes an aligned table of every field, its value, and its origin.

**Use it when:** somebody asks why a value is what it is. Ship it behind a debug flag in every service — it turns a bisection through five config layers into a lookup.

```go
if os.Getenv("CONFIG_DEBUG") != "" {
	res.Explain(os.Stderr)    // safe: secrets are absent
}
```

### `Result.Files() []string`

The `.env` chain `DefaultSources` consulted, lowest precedence first, **including files that did not exist**. Empty for an explicit source list, where the caller already knows.

**Use it when:** you use `DefaultSources` and need to answer "which files were even looked at?" — with the filenames no longer in `main.go`, a typo'd filename would otherwise look exactly like a file whose values were overridden.

```go
for _, f := range res.Files() {
	log.Println("consulted:", f)
}
```

### `Result.Unknown() []UnknownKey`

Every key a source supplied that matched no field — almost always a typo.

**Use it when:** you want a misspelled key in a `.env` file to be visible instead of silent. Log it at startup, or fail CI on it while production tolerates a shared file.

```go
if u := res.Unknown(); len(u) > 0 {
	log.Printf("config: %v", u)
}
```

Only sources implementing `KeyLister` contribute — `FromEnviron` cannot, because its key set is the whole machine. A `was:` former key name is never reported. Advisory only, never fatal: one `.env` may legitimately carry keys for a deployment pipeline alongside the app's own.

### `KeyLister` — let your source report typos

```go
type KeyLister interface {
	Keys() []string
}
```

**Implement it on your own source when** its key set genuinely belongs to the application — a secret store path, a config service namespace. Four lines buys typo detection. **Omit it when you cannot enumerate honestly**; nothing else changes.

### `Result.Fields() []Field`

The same data programmatically, sorted by Go path.

**Use it when:** you want to act on provenance rather than read it. Warn at startup about secrets still on their compiled-in default; assert in a test that production values come from the environment and not from a file.

```go
for _, f := range res.Fields() {
	if f.Secret && f.Source == "default" {
		log.Printf("WARNING: %s is using its built-in default", f.Path)
	}
}
```

### `Result.JSON() ([]byte, error)`

The whole record as JSON, masked by the same rule as `Explain`.

**Use it when:** a machine consumes it — a health endpoint, a support bundle, a diagnostic upload.

### `Result.Mode() Mode`

The mode the load resolved.

**Use it when:** you need to pass the mode to rules, or branch on it during bootstrap. This is how a `Validate` method gets the mode without a global.

### `Field`

One resolved field: `Path`, `Key`, `Value`, `Source`, `Secret`. Returned by `Result.Fields()`.

## Reload

### `NewWatcher[T](build func() []Option) (*Watcher[T], error)`

Loads the configuration and returns a `Watcher` holding it. `build` is called now and again on every `Reload`, and **must construct the sources inside it** — `FromFiles` reads at construction, so a static option list would reload nothing.

**Use it when:** configuration can change while the process runs and you want the change without a restart.

An error from the first load returns no `Watcher`: a process must not start on a configuration that does not load.

### `(*Watcher[T]) Current() *T`

The newest complete configuration. Safe from any goroutine with no lock, and stable — a later `Reload` publishes a new value rather than editing this one.

**The rule:** a component holds the `Watcher`, never a value read from it. See [Reload](reload.md#the-one-rule-you-have-to-follow).

### `(*Watcher[T]) Reload() error`

Re-reads every source and runs the whole pipeline **before** publishing. On any error the previous configuration stays in service and the error is returned, so a bad edit cannot take the process down.

**Use it when:** your trigger fires — SIGHUP, a timer, an admin route, or your own `fsnotify`. The package reloads; it does not watch.

### `(*Watcher[T]) Snapshot() (*T, *Result)`

The configuration and its provenance from the **same** generation.

**Use it when:** you need both. Calling `Current` and `Result` separately can straddle a reload and describe a value with the wrong origin.

### `(*Watcher[T]) Result() *Result`

Provenance for the configuration `Current` would return.

### `(*Watcher[T]) Generation() uint64`

How many configurations have been published, starting at 1. Only successful reloads increment it, so an unchanged number is how you know a reload failed.

## Rules

Every rule returns a plain `error`, so they compose with `errors.Join` and with your own checks. Full treatment with scenarios in [Validation](validation.md#the-rule-helpers).

| Rule | Use it when |
|---|---|
| `Required(field, v)` | the app genuinely cannot run without the value |
| `NotEmpty(field, v)` | the difference between absent and blank matters to whoever is fixing it |
| `RequiredIn(mode, current, field, v)` | optional on a laptop, mandatory in production |
| `RequiredWhen(field, present, cond, holds)` | one field decides whether another is meaningful |
| `OneOf(field, v, allowed...)` | the valid set is known only at runtime |
| `Range(field, v, lo, hi)` | an out-of-window number would fail later and further away |
| `Matches(field, v, re)` | the value's shape matters — a slug, an ID format |
| `MutuallyExclusive(sets...)` | two settings express the same thing and one is being ignored |
| `AtLeastOneOf(sets...)` | several fields are alternative routes to one requirement |
| `NotWeakSecret(mode, field, v, known...)` | the field holds a credential — **use this on every one** |

### `Set`

A `{Name, Value}` pair for `MutuallyExclusive` and `AtLeastOneOf`.

**Why it exists:** Go cannot recover a field's name from its value, and an error naming *"arg 2"* is not worth printing.

## Errors

All are returned inside the joined error from `Load` and `Check`. Reach for them with `errors.As` when you want to react programmatically rather than print.

### `*DecodeError`

A value could not be parsed into its field's type. Carries `Path`, `Key`, `Source`, `Value`, and the cause. `Value` is empty for a secret field.

**Use it when:** you want to report bad input differently from missing input — for example, exiting with a distinct code so a deploy script can tell a typo from an unset variable.

### `*RequiredError`

A required field did not resolve. `Empty` distinguishes *missing* from *supplied but blank*.

**Use it when:** you want to tell an operator which of the two mistakes they made.

### `*ValidationError`

A rule failed. `Rule` names which one — `required_when`, `not_weak_secret`, `range` — so you can match on the rule rather than on message text.

### `*SourceError`

A source itself failed. **This is never a miss.**

**Use it when:** distinguishing "the secret store is down" from "the value is not configured". The first is retryable; the second is not.

### `*UnreachableFieldError` and `*UnreachableHookError`

The struct itself is shaped so that something can never work. Both are reported **before any source runs**, because they depend only on the type — see [Recipes](recipes.md#extending-a-frameworks-configuration).

**Use it when:** never, programmatically. They exist so a mistake in the struct fails at the first `Load` instead of quietly binding nothing.

```go
var se *cfgkit.SourceError
if errors.As(err, &se) {
	return fmt.Errorf("config backend %s unavailable, retrying: %w", se.Source, err)
}
```

### `Unwrap` on `DecodeError` and `SourceError`

Both wrapping errors expose their cause, so `errors.Is` reaches through them.

**Use it when:** you care about the underlying failure rather than the config layer — retrying on `context.DeadlineExceeded` from a secret store, or matching `fs.ErrNotExist` from a `file`-tagged field.

```go
if errors.Is(err, fs.ErrNotExist) {
	// the mounted secret file was not there
}
```

## Mode

### `Mode` and the constants

`ModeDev`, `ModeTest`, `ModeProd`.

**Use it when:** behaviour must differ between a laptop and production. One knob rather than twenty booleans — see [Modes](modes.md).

Resolved from `APP_ENV` unless you pass `WithMode`, defaulting to `ModeDev` because an unset mode means a developer's machine far more often than production.

### `Option`

The function type every `With*` returns. You will not construct one directly, but it is what makes the option list open to future settings without breaking callers.
