# Modes

One knob, not twenty booleans.

```go
type Mode string

const (
	ModeDev  Mode = "dev"
	ModeTest Mode = "test"
	ModeProd Mode = "prod"
)
```

## What a mode is for, in plain terms

Some behaviour should differ between a laptop and production, and the list is short but important: whether a missing error-tracker is tolerable, whether a placeholder secret is allowed, whether migrations run automatically, how verbose the logs are.

You could express each of those as its own setting. Then you have twenty booleans, an operator who has to know all twenty, and a million possible combinations of which perhaps two were ever run. A mode collapses that into **one thing to set and two things to test**.

**Set it once, in your deployment.** `APP_ENV=production` in a container, nothing on a laptop. Everything else follows.


## Where the mode comes from

With an explicit source list, the mode is read from `APP_ENV` in the **process environment** only — `WithMode` sets it, `WithModeKey` renames the variable.

With [`DefaultSources`](sources.md#the-mode-comes-from-the-files-too) it is also read from `.env` and `.env.local`, in this order:

| # | Mode comes from |
|---|---|
| 1 | `WithMode(...)` |
| 2 | the process environment |
| 3 | `.env` and `.env.local` |
| 4 | `dev` |

**This matters more than it looks.** With an explicit source list, `APP_ENV=production` written into a `.env` file does **not** set the mode — it is an ordinary key that binds to an ordinary field, and the mode stays `dev`. Every `RequiredIn(ModeProd, ...)` rule then quietly does not fire.

If your `.env.prod` carries `APP_ENV=production` and you are not using `DefaultSources`, pass the mode explicitly:

```go
cfgkit.Load[Config](
	cfgkit.WithSources(...),
	cfgkit.WithMode(cfgkit.ModeProd),   // do not rely on the file
)
```

Both spellings work everywhere: `prod` / `production`, `test` / `testing`. Anything unrecognised is `dev`, because an unset or misspelled mode means a developer's machine far more often than production — and every prod-only rule fails closed anyway.

## Why one knob

Every independent on/off flag creates a second code path that nobody exercises. Twenty flags is a million combinations and two that were ever run.

Worse, in a zero-config library the **lenient** path is what every new user hits first — so it must be the well-tested one, not the forgotten one. Two modes mean two configurations that are actually run.

## Resolution

```go
cfgkit.Load[Config](cfgkit.WithMode(cfgkit.ModeProd))   // explicit
cfgkit.Load[Config](cfgkit.WithModeKey("CONFIG_ENV"))   // from a different variable
cfgkit.Load[Config]()                                   // from APP_ENV, else dev
```

| `APP_ENV` | Mode |
|---|---|
| `prod`, `production` | `ModeProd` |
| `test`, `testing` | `ModeTest` |
| anything else, or unset | `ModeDev` |

Matching is case-insensitive. **Dev is the default** because an unset mode means a developer's machine far more often than production — and every prod-only rule fails closed anyway, so guessing dev is the safe guess.

`res.Mode()` reports what a load resolved.

## The rule that makes zero-config safe

> **Every convenience that makes development frictionless must hard-fail in production.**

A generated dev secret, a permissive default, an auto-created directory: delightful on a laptop, catastrophic if it silently survives to production. Zero-config is only safe if it is loud at the boundary.

## Where modes actually get used

In practice a mode drives three kinds of decision, and only three:

| Decision | Dev | Prod |
|---|---|---|
| **Is this value allowed to be missing?** | yes | no — `RequiredIn` |
| **Is this placeholder secret acceptable?** | yes | no — `NotWeakSecret` |
| **Which bundle of convenience defaults applies?** | permissive | strict, in your `Defaults()` |

Everything else — the port, the database URL, the log level — is just configuration, and belongs in a source rather than in a mode.

## `RequiredIn` — strict only where it matters

```go
func (c *Core) Validate() error {
	return cfgkit.RequiredIn(cfgkit.ModeProd, c.mode, "SentryDSN", c.SentryDSN)
}
```

Absent in dev: fine, the app runs. Absent in prod: refuses to boot.

That asymmetry is the whole point. A field that is `required` unconditionally would break the zero-input property; a field that is never required lets an empty production secret through.

## `NotWeakSecret` — the placeholder guard

```go
const placeholder = "__CHANGE_ME__"

cfgkit.NotWeakSecret(cfgkit.ModeDev,  "Key", placeholder)
cfgkit.NotWeakSecret(cfgkit.ModeProd, "Key", placeholder)
```

```
dev:  <nil>
prod: Key: is still a placeholder in mode=prod; set a real value (not_weak_secret)
```

It rejects three things, outside dev only:

| Rejected | Why |
|---|---|
| `""` | an unfilled secret |
| `__ANYTHING__` | the placeholder convention, matching `^__[A-Z0-9_]+__$` |
| a caller-supplied known default | `NotWeakSecret(mode, "Key", v, "change-me-internal-key")` |

The placeholder pattern is shared with `dotenvctl`, which reports such values as `!` in its drift matrix — so the same convention is enforced by both tools.

**The offending value is never echoed** in the error. It is a credential field, and an error message gets logged.

## A worked mode-aware config

```go
type Core struct {
	mode          cfgkit.Mode  `env:"-"`
	EncryptionKey string       `env:"ENCRYPTION_KEY" secret:"true" default:"__DEV_ONLY__"`
	SentryDSN     string       `env:"SENTRY_DSN"`
	AutoMigrate   bool         `env:"AUTO_MIGRATE"`
}

func (c *Core) Defaults() {
	c.AutoMigrate = true   // convenient in dev
}

func (c *Core) Validate() error {
	return errors.Join(
		cfgkit.NotWeakSecret(c.mode, "EncryptionKey", c.EncryptionKey),
		cfgkit.RequiredIn(cfgkit.ModeProd, c.mode, "SentryDSN", c.SentryDSN),
	)
}
```

A fresh clone runs: the encryption key has a dev placeholder, Sentry is absent, migrations run automatically. In production all three become errors or must be set explicitly — and the failure happens in `Check` during CI, not at container start.

Pass the mode into the struct with a `Deriver`, or resolve it once in your bootstrap and hand it to the rules.

## Gotchas

**Rules take the mode as an argument.** There is no global to read it from — that is deliberate. Get it from `res.Mode()` or from the mode your application already resolved.

**The mode does not change binding.** It only affects rules that ask for it. A value present in the environment binds identically in every mode; what differs is whether its absence is tolerated.

**`ModeTest` is not automatically strict.** It exists so tests can be distinguished from development, but no built-in rule treats it specially. Use it where you need behaviour that is neither dev-lenient nor prod-strict.

**Do not add a second knob.** If you find yourself wanting `AUTO_MIGRATE` on in prod for one deployment, set the variable — do not add a mode. Modes select *bundles of defaults*; individual overrides are what sources are for.
