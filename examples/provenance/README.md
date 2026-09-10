# provenance — reading the Result in code

```sh
go run ./provenance
```

`Explain` writes a table for a human at 3am. `Result.Fields()` and `Result.JSON()` are for the **program**: a `/debug/config` endpoint, a startup log line, a CI step that refuses to promote a build.

## The audit that actually catches things

```go
for _, f := range res.Fields() {
	if f.Source == sourceDefault {
		fmt.Printf("  %s (%s)\n", f.Path, f.Key)
	}
}
```

In production, a field still sitting on its compiled-in default is usually a mistake — and it is **invisible without provenance**, because the value looks perfectly reasonable. `postgres://localhost/dev` is a valid URL. It is only wrong because nobody set it.

```
still on a default:
  AppName (APP_NAME)
  DatabasePassword (DATABASE_PASSWORD)
  DatabaseURL (DATABASE_URL)
```

That is three fields a production deploy gate should refuse, and no amount of validating the *values* would have found them. This is the capability no other Go configuration library offers.

## The JSON view

```json
{"mode":"prod","fields":[{"path":"AppName","key":"APP_NAME","value":"demo","source":"default","secret":false}, …]}
```

Every field carries `path`, `key`, `value`, `source` and `secret`. A test parses it rather than string-matching, because output that merely *looks* like JSON is worse than none.

## Masking follows the field, not the rendering

`DatabasePassword` is `••••••` in the JSON exactly as it is in the table. That matters because `/debug/config` reaches the output through a different code path from `Explain`, and it is precisely the endpoint where a password escapes. A test asserts the real value never appears.

`cfgkit.Reveal()` opts out, deliberately and explicitly. A library that could *never* print a secret would just be worked around with `fmt.Println`, which is worse — the point is that it takes saying so.

## Mode is not inferred from your sources

```go
cfgkit.Load[Config](
	cfgkit.WithMode(cfgkit.ModeProd),
	cfgkit.WithSources(cfgkit.FromMap(...)),
)
```

With an explicit source list the mode is stated, not guessed. Inferring it from a value inside those sources would be circular in the general case.

The inference *does* exist, but it belongs to `DefaultSources`, which resolves the mode in a first pass over the base files before it can choose `.env.<mode>`. That is a different problem with a different answer, and conflating them is how `APP_ENV=production` ends up silently ignored while every `RequiredIn(ModeProd, …)` rule quietly does not fire.

## Next

- [`precedence`](../precedence) — the human-facing view of the same data
- [`validation`](../validation) — turning an audit into a gate that fails the build
- [`read-file`](../read-file) — where the `SOURCE` column comes from
