# validation — failing in CI instead of at container start

```sh
go run ./validation
```

The point is **where** the failure happens. A misconfigured deploy that panics at container start fails after the rollout, in the one place with no logs yet. `cfgkit.Check` runs the identical pipeline in CI and reports every problem at once — no configuration returned, nothing booted.

```go
if err := cfgkit.Check[Config](); err != nil {
	log.Fatal(err)   // a failed pipeline step, not a failed rollout
}
```

## Rules live on the struct they describe

```go
func (c *Config) Validate() error {
	return errors.Join(
		cfgkit.Range("PORT", c.Port, 1, 65535),
		cfgkit.OneOf("LOG_LEVEL", c.LogLevel, "debug", "info", "warn", "error"),
		cfgkit.RequiredIn(cfgkit.ModeProd, mode(c.Env), "DATABASE_URL", c.DatabaseURL),
		cfgkit.RequiredIn(cfgkit.ModeProd, mode(c.Env), "DATABASE_PASSWORD", c.DatabasePassword),
	)
}
```

## `errors.Join`, not early return

This is the part worth copying. Returning on the first failure turns a misconfigured deploy into a guessing game of **one fix per restart**, and every restart is another failed rollout. Joining reports everything in one pass:

```
prod, database unset:
  cfgkit: 2 problem(s):
  DATABASE_URL: is required in mode=prod but was not set (required_in)
  DATABASE_PASSWORD: is required in mode=prod but was not set (required_in)

bad port and unknown log level:
  cfgkit: 2 problem(s):
  PORT: is 70000, want between 1 and 65535 (range)
  LOG_LEVEL: is verbose, want one of [debug info warn error] (one_of)
```

Writing this example found a bug in cfgkit itself: the `N problem(s)` header counted top-level error *groups*, and `Validate` contributes exactly one however many problems it found. Two bad fields printed **"1 problem(s)"** above two lines — so a reader who trusted the header, fixed the single problem it promised and redeployed got a second failed rollout. Fixed, and pinned by a test in both the library and here.

## Mode scoping keeps zero-input true

`RequiredIn(ModeProd, …)` means *required in production*. The same struct still runs on a laptop with nothing set:

```
dev, nothing set:
  ok
```

Without this, the zero-input promise quietly becomes "zero input, except the seven you need", and the first thing a new user hits is a wall of required-field errors.

The `mode()` function is where "staging counts as production" lives. That is a policy decision, so it belongs in one named place rather than being spelled out at four call sites — pinned by a test, because it is exactly the rule someone changes without noticing the blast radius.

## Secrets do not leak through failures

An error message is one of the easiest routes for a credential to reach a log aggregator. A validation failure names the **field**, never the value — `TestSecretIsNotEchoedByAFailure` sets a canary password, forces an unrelated failure, and asserts the canary is absent.

## Next

- [`provenance`](../provenance) — auditing which fields are still on defaults
- [`default-values`](../default-values) — the other half: fields that legitimately need none
- [`precedence`](../precedence) — where a value came from, when it turns out to be wrong
