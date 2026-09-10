# Recipes

Complete workflows, end to end. Flags and tags are documented in [Tags](tags.md) and [Sources](sources.md); this page is about composing them.

## The conventional source chain

```go
cfg, res, err := cfgkit.Load[Config](cfgkit.DefaultSources())
```

`.env` holds committed defaults, `.env.local` is gitignored and personal, `.env.<mode>` and `.env.<mode>.local` are per-environment, and the process environment beats all of them. Missing files are fine — that is what makes a fresh clone run.

The mode is resolved from those files as well as from the environment, so `APP_ENV=production` in a `.env` file works. Doing that by hand is where it goes wrong; the reasoning is in [Sources](sources.md#the-mode-comes-from-the-files-too).

To state the chain explicitly instead — worth it when a reader of `main.go` should see the filenames:

```go
cfg, res, err := cfgkit.Load[Config](
	cfgkit.WithSources(
		cfgkit.FromFiles(".env", ".env.local", ".env.prod"),
		cfgkit.FromEnviron(),
	),
	cfgkit.WithMode(cfgkit.ModeProd),   // required: a file cannot set it here
)
```

## Pkl at build time, not run time

If you already write configuration in Pkl, keep it — but evaluate it during the build so the `pkl` binary never has to exist in your production image.

**1. Evaluate to JSON as part of the build:**

```sh
pkl eval -f json pkl/env/prod/app.pkl -o config.prod.json
```

**2. Embed it:**

```go
//go:embed config.prod.json
var pklConfig []byte
```

**3. Feed it as a structured source:**

```go
cfg, _, err := cfgkit.Load[Config](cfgkit.WithSources(
	cfgkit.FromJSON(pklConfig),        // the Pkl-derived values
	cfgkit.FromFiles(".env"),          // local overrides
	cfgkit.FromEnviron(),              // deployment overrides
))
```

Fields are matched by their `json:` tags, so the nested Pkl output lands on the nested struct with no flattening and no key translation.

**What this buys:** the JVM-backed `pkl` binary leaves the runtime image, configuration load stops being a subprocess spawn at every boot, and a config error becomes a **build** failure rather than a container-start panic that fires before observability exists.

**What it costs:** one build step, and the regenerate-and-commit discipline any generated artefact needs.

Nothing in `cfgkit` imports Pkl, and a developer who has never heard of it never has to.

## Docker and Kubernetes mounted secrets

Both platforms mount a secret as a **file** and pass the path in the environment:

```yaml
# docker-compose.yml
services:
  api:
    environment:
      DB_PASSWORD_FILE: /run/secrets/db_password
    secrets:
      - db_password
```

```go
type DB struct {
	Password string `env:"DB_PASSWORD_FILE,file" secret:"true"`
}
```

The value never appears in `docker inspect`, in `/proc/<pid>/environ`, or in any child process. The trailing newline every tool adds is trimmed for you.

For a value that *is* in the environment but must not reach children:

```go
Token string `env:"ONE_SHOT_TOKEN,unset" secret:"true"`
```

## CI: validate every environment before deploying

```go
func TestEveryEnvironmentIsValid(t *testing.T) {
	for _, env := range []string{"dev", "staging", "production"} {
		t.Run(env, func(t *testing.T) {
			err := cfgkit.Check[Config](
				cfgkit.WithSources(cfgkit.FromFiles(".env." + env)),
				cfgkit.WithMode(cfgkit.ModeProd),   // strictest rules everywhere
			)
			if err != nil {
				t.Error(err)
			}
		})
	}
}
```

A stale environment file now fails a build instead of a container.

## Generate and enforce the contract

**Generate** `.env.example` from the struct:

```go
//go:build ignore

package main

func main() {
	f, err := os.Create(".env.example")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := cfgkit.Document[app.Config](f); err != nil {
		log.Fatal(err)
	}
}
```

**Fail CI when it drifts:**

```sh
go run gen_env_example.go
git diff --exit-code .env.example || { echo "contract is stale — commit the regenerated file"; exit 1; }
```

**Then enforce it across environments** with `dotenvctl`:

```sh
dotenvctl matrix --contract .env.example    # exit 1 when any environment lacks a contract key
```

The loop closes: **struct → contract → every environment checked → red build.**

## A secret store, without a dependency in cfgkit

```go
// Pre-load once — Lookup runs per field.
secrets, err := vaultClient.ReadAll(ctx, "secret/data/app")
if err != nil {
	return err
}

vault := cfgkit.SourceFunc("vault", func(key string) (string, bool, error) {
	v, ok := secrets[key]
	return v, ok, nil
})

cfg, _, err := cfgkit.Load[Config](cfgkit.WithSources(
	cfgkit.FromFiles(".env"),
	vault,                    // beats the file
	cfgkit.FromEnviron(),     // beats everything
))
```

If the store is unreachable, return the error from the closure rather than a miss — an empty password must never look like a configured one.

## YAML without adding a dependency to cfgkit

Either use the contrib module:

```go
import yamlsrc "github.com/ubgo/cfgkit/contrib/format-yaml"

cfgkit.WithSources(yamlsrc.File("config.yaml"), cfgkit.FromEnviron())
```

Or inline it, keeping the parser in your own module:

```go
cfgkit.StructuredFunc("config.yaml", func(dst any) error {
	b, err := os.ReadFile("config.yaml")
	if err != nil {
		if os.IsNotExist(err) {
			return nil        // absent is fine
		}
		return err            // present-but-broken is not
	}
	return yaml.Unmarshal(b, dst)
})
```

## cobra: flags on top of everything

```go
var cmd = &cobra.Command{
	RunE: func(cmd *cobra.Command, args []string) error {
		// Load INSIDE RunE — cobra has parsed by now.
		cfg, _, err := cfgkit.Load[Config](cfgkit.WithSources(
			cfgkit.FromFiles(".env"),
			cfgkit.FromEnviron(),
			pflagsrc.Source(cmd.Flags()),   // highest precedence
		))
		if err != nil {
			return err
		}
		return run(cfg)
	},
}

func init() {
	cmd.Flags().Int("port", 0, "override the HTTP port")   // zero default!
}
```

Two rules, both easy to get wrong — see [`contrib/flags-pflag`](../contrib/flags-pflag/README.md).

## urfave/cli, without an adapter module

`contrib/flags-pflag` exists because cobra is everywhere. urfave/cli needs no module at all — `FlagSource` is the seam, and the whole adapter is six lines you can paste:

```go
// urfave/cli v3 — inside Action, after parsing
func run(ctx context.Context, cmd *cli.Command) error {
	typed := map[string]string{}
	for _, name := range cmd.FlagNames() {
		if cmd.IsSet(name) {                 // ONLY what the user typed
			typed[name] = cmd.String(name)
		}
	}

	cfg, _, err := cfgkit.Load[Config](cfgkit.WithSources(
		cfgkit.DefaultSources(),
		cfgkit.FlagSource("urfave", typed),   // highest precedence
	))
	...
}
```

v2 is the same with `*cli.Context` in place of `*cli.Command` — both expose `IsSet` and `String`.

**The `IsSet` check is the whole point.** Without it you read every flag's *default* and it silently beats your `.env` file, which is the long-standing viper defect ([#671](https://github.com/spf13/viper/issues/671)). And build the source **inside** `Action`: a flag set holds nothing until it is parsed, so a source built in `init()` sees an empty set and looks like "flags do not work".

A module was considered and rejected: it would carry no dependency (both urfave versions satisfy the same two methods), so it would add an import path and a release cadence in exchange for six lines you can read. See [when a module is not the right answer](catalogue.md#when-a-module-is-not-the-right-answer).

## An optional feature, switched on by configuring it

Email, object storage, an external cache — anything the app runs without.

```go
type App struct {
	Port int   `env:"PORT" default:"8080"`
	SMTP *SMTP `env:",prefix=SMTP_"`     // nil = no email
}

type SMTP struct {
	Host string `env:"HOST"`
	User string `env:"USER"`
	Port int    `env:"PORT" default:"587"`
}

func (s *SMTP) Validate() error {
	return cfgkit.Required("Host", s.Host)   // only runs if the section exists
}
```

```go
if cfg.SMTP != nil {
	startEmailSender(cfg.SMTP)
}
```

| Deployment sets | Result |
|---|---|
| nothing | `SMTP` is nil; the app runs without email |
| `SMTP_HOST=…` | section allocated, `Port` defaults to 587 |
| `SMTP_USER=…` only | section allocated, load **fails**: "Host is required" |

The third row is the value of this shape. A half-configured feature fails at startup with the field named, rather than starting and failing on the first send.

## Feature flags nobody has to declare in Go

The case a map is for: the names are not known when the struct is written.

```go
type App struct {
	Flags map[string]string `env:"FLAGS" doc:"feature toggles, name:on|off"`
}

func (a *App) On(name string) bool { return a.Flags[name] == "on" }
```

```
FLAGS=new-checkout:on,dark-mode:off
```

Adding `beta-search:on` next month is a one-line edit to a `.env` file and a restart — **no Go change, no deploy of new code.** That is the entire justification for reaching for a map; if the names are known, use named `bool` fields instead, where a typo is a compile error.

The same shape covers per-tenant limits and arbitrary extra headers:

```go
Limits  map[string]int    `env:"TENANT_LIMITS"`   // acme:1000,globex:500
Headers map[string]string `env:"EXTRA_HEADERS"`   // X-Env:prod,X-Region:eu
```

Watch for values containing a colon — a DSN or a `host:port` is fine, because only the first colon splits. See [Types](types.md#the-gotcha-only-the-first-separator-splits).

## Extending a framework's configuration

A framework exports its config type; a project embeds it and adds its own:

```go
type Config struct {
	volt.Config                                   // everything the framework defines

	Stripe struct {
		Key string `env:"KEY" secret:"true"`
	} `env:",prefix=STRIPE_"`
}

cfg, res, err := cfgkit.Load[Config](cfgkit.DefaultSources())
```

One `Load`, one struct, one `Explain`. Field paths stay **flat** — `Port`, not `Config.Port` — matching how Go itself promotes them.

**Why this shape rather than editing the framework's config file:** the project never touches the framework's own declarations, so upgrading the framework is a version bump instead of a merge. The alternative — a shared file with a `// PROJECT SPECIFIC` region in the middle — puts the project's settings inside a file the framework also owns, and every upgrade becomes a three-way merge of that file.

The framework's `Defaults()` and `Validate()` run as part of the project's single `Load`, so the framework can enforce its own invariants without the project doing anything.

### The embedded type must be exported

```go
type Config struct {
	frameworkConfig   // unexported: hooks cannot run
}
```

Go does not allow calling a method on a value reached through an unexported embedded field, so `frameworkConfig.Validate()` **could never run**. cfgkit reports it instead of letting it silently do nothing:

```
frameworkConfig (cfgkit.frameworkConfig): Validate() can never run, because Go does not
allow calling a method on a value reached through an unexported embedded field — export the type
```

An unexported type with no hooks binds fine — its *fields* are writable even though its methods are not, which is the same rule `encoding/json` follows.

Embedding an unexported type **by pointer** never works, because the pointer itself cannot be allocated:

```
config (*cfgkit.internalConfig): an embedded pointer to an unexported type cannot be
allocated, so no source can fill the fields beneath it — embed it by value, or export the type
```

In practice this only bites within one package: across packages the type has to be exported anyway, or you could not name it.

### An embedded pointer is always allocated

```go
type Config struct {
	*volt.Config          // always allocated
	SMTP *SMTPConfig      // nil until a source sets something beneath it
}
```

The two look alike and mean different things. An **embedded** pointer is part of the outer type's identity — Go promotes its methods, so the outer struct answers `Defaults()` whether or not the pointer is set — and cfgkit allocates it before anything else runs. A **named** `*Struct` field is the *"this feature is absent"* idiom and stays `nil` unless a source sets something inside it ([Tags](tags.md#optional-sections--when-a-struct-is-nil)).

## A component that owns its own configuration

There is no `Sub()` or `Cut()` in cfgkit, and there does not need to be. Two patterns cover what they are for.

**When you know the component's type**, pass the nested struct. It is already typed, so there is nothing to extract:

```go
type App struct {
	Port int   `env:"PORT" default:"8080"`
	SMTP *SMTP `env:",prefix=SMTP_"`
}

startEmailSender(cfg.SMTP)     // that is the whole "sub-tree" API
```

**When you do not** — a plugin registry, where the host cannot name the plugin's config type at compile time — the plugin brings its own type and loads it from a source the host hands over.

There is a complete runnable version of this in [`examples/plugin`](../examples/plugin/README.md), whose output is pinned by tests. The shape:

```go
// In the plugin. Note that `config` is UNEXPORTED — no other package can
// declare a field of it, embed it, or unmarshal into it.
package acme

type config struct {
	Endpoint string `env:"ENDPOINT" default:"https://api.acme.test"`
	Retries  int    `env:"RETRIES" default:"3"`
	APIKey   string `env:"API_KEY" secret:"true"`
}

func (c *config) Validate() error { return cfgkit.Range("Retries", c.Retries, 0, 10) }

func (p *Plugin) Configure(sources ...any) error {
	cfg, res, err := cfgkit.Load[config](cfgkit.WithSources(sources...))
	if err != nil {
		return err
	}
	p.cfg, p.res = cfg, res
	return nil
}
```

```go
// In the host, whose own config struct contains nothing about acme:
p.Configure(cfgkit.FromPrefixedEnviron("ACME_"))
```

The host passes **where to read, not what was read.** `FromPrefixedEnviron("ACME_")` means *"the environment, restricted to `ACME_*`, with the prefix stripped"* — so when the plugin asks for `ENDPOINT` the source looks up `ACME_ENDPOINT`, and the plugin never knows a prefix was involved. Swapping that one line for `FromFiles` or a secret store changes where every plugin reads from, without any plugin knowing.

The second `Load` is legal, cheap and fully isolated because there is **no global state** — there is no shared registry for two loads to collide in.

With `PORT=9000 ACME_ENDPOINT=https://acme.prod ACME_RETRIES=5 ACME_API_KEY=sk_live_secret`:

```
host: demo listening on :9000

the HOST's Explain — only the host's fields:
FIELD  KEY       VALUE  SOURCE
Name   APP_NAME  demo   default
Port   PORT      9000   environ

the PLUGIN's Explain — only the plugin's fields, secret masked:
FIELD     KEY       VALUE              SOURCE
APIKey    API_KEY   ••••••             environ:ACME_
Endpoint  ENDPOINT  https://acme.prod  environ:ACME_
Retries   RETRIES   5                  environ:ACME_
```

**Two separate reports**, neither cluttered with the other's fields. The origin reads `environ:ACME_`, naming the prefixed view, so debugging still says exactly where a value came from. The secret is masked in the plugin's own output, and the host never handles the value at all. `ACME_RETRIES=99` fails the load against the plugin's own `Range` rule — which the host never wrote.

**To report on one sub-tree of an existing load**, filter the fields — `Path` is a plain dotted string:

```go
for _, f := range res.Fields() {
	if strings.HasPrefix(f.Path, "SMTP.") {
		fmt.Printf("%s = %s (%s)\n", f.Path, f.Value, f.Source)
	}
}
```

```
SMTP.Host = mail.test (map)
SMTP.Port = 587 (default)
```

**Why viper and koanf need `Sub`/`Cut` and this does not:** they hold an untyped `map[string]any`, so there is no struct to hand anyone — `Sub` is how you get a piece of the map to unmarshal into a type. cfgkit's tree is typed from the start, so the operation has no subject. Adding it would mean inventing an untyped intermediate that the rest of the design exists to avoid.

And what replaces it is more, not less: a component that brings its own type, defaults, validation, secret marking, provenance and `.env` contract is strictly better served than one handed a slice of somebody else's config tree.

## Multi-tenant: one process, several configurations

Because there is no global state, a process can hold several configurations at once:

```go
for _, tenant := range tenants {
	cfg, _, err := cfgkit.Load[TenantConfig](cfgkit.WithSources(
		cfgkit.FromPrefixedEnviron(strings.ToUpper(tenant) + "_"),
	))
	if err != nil {
		return err
	}
	start(tenant, cfg)
}
```

This is impossible with a package-level `Get()`, and it is why there is not one.

## Catching typos in a config file

```go
cfg, res, err := cfgkit.Load[Config](...)
if err != nil {
	log.Fatal(err)
}
for _, u := range res.Unknown() {
	log.Printf("config warning: %s", u)   // "DATABAS_URL (from file:.env) matched no field"
}
```

Ship this in every service. A typo'd key is otherwise completely silent: the field falls back to its default, everything else works, and the mistake surfaces as wrong behaviour rather than a message.

To be strict in CI while tolerant in production — a shared `.env` may legitimately hold keys for a deploy pipeline:

```go
func TestNoTypos(t *testing.T) {
	_, res, err := cfgkit.Load[Config](cfgkit.WithSources(cfgkit.FromFiles(".env.example")))
	if err != nil {
		t.Fatal(err)
	}
	if u := res.Unknown(); len(u) > 0 {
		t.Errorf("keys matching no field: %v", u)
	}
}
```

## Debugging "why is my port 9001?"

```go
cfg, res, err := cfgkit.Load[Config](...)
if err != nil {
	log.Fatal(err)
}
if os.Getenv("CONFIG_DEBUG") != "" {
	res.Explain(os.Stderr)   // safe: secrets are absent
}
```

Ship it behind a flag in every service. It costs nothing and answers the question directly instead of by bisection.

## Testing your own application's config

```go
func TestConfigDefaults(t *testing.T) {
	// FromMap is the seam: no files, no environment, no ordering surprises.
	cfg, _, err := cfgkit.Load[Config](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"DATABASE_URL": "postgres://test"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want the default", cfg.Port)
	}
}
```

Never call `os.Setenv` in a config test. `FromMap` gives you the same result without making tests order-dependent or unsafe to parallelise.
