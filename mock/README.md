# mock — one configuration, every format

A single canonical configuration, written seven times, plus the struct that binds it and the values it must produce.

```
config.yaml  config.toml  config.hcl  config.json
config.ini   config.properties        config.env
```

## Why it exists

Every format module in [`contrib/`](../contrib) has its own tests, and each one proves that module parses its own format correctly. None of them can prove the property a user actually relies on:

> YAML, TOML, HCL, JSON, INI, `.properties` and `.env` all bind the **same struct** to the **same values**.

That is a claim about all seven modules at once. It cannot live inside any of them — a module that imported its six siblings to check it would defeat the point of splitting them — so the fixtures live here and the proof lives in [`examples/read-formats`](../examples/read-formats), which is a separate module allowed to import them all.

## What it exports

| | |
|---|---|
| `mock.FS` | the fixtures, as an `embed.FS`, so a consumer needs no working-directory assumption |
| `mock.Fixtures` | the canonical iteration order — a **slice**, because Go randomises map iteration and a sweeping test must produce identical output twice |
| `mock.Config` | the struct every fixture binds to |
| `mock.Want()` | the values every fixture must produce, declared once |

`Want()` exists so a test asserts against one source of truth. Repeating the expected literals per format is how a format quietly drifts while its test keeps passing.

## The fixtures are idiomatic on purpose

The INI file uses sections. The HCL file uses blocks. The YAML file nests. They are not seven copies of one artificial layout, because a fixture set written in a degenerate style common to every format would prove only that the degenerate subset works — which is not what anyone's configuration looks like.

## This package is dependency-free

`mock` is part of the **root module** and imports nothing beyond `embed`. Adding it costs no consumer anything, and it is usable from a root-module test as easily as from the examples.

The format modules themselves are never imported here. That direction matters: fixtures that depended on parsers could not be used to test those parsers without circularity.

## What it caught

The fixture set earned its place on first run. `MaxConns` — the one multi-word key — bound as **zero** from YAML and TOML, silently, while every single-word field bound correctly.

Both decoders read their own tag and fall back to the *lowercased Go field name*: `maxconns`, not `max_conns`. Single-word fields hide this because lowercasing happens to match, so the bug only surfaces once a field has two words — long after the pattern is set, and with no error to point at it.

`mock.Config` now carries per-format tags, and `examples/read-formats` pins the trap with a test that fails if a decoder ever changes its fallback.

## Adding a format

1. Write `config.<ext>` here, idiomatically.
2. Add it to the `//go:embed` line and to `Fixtures`.
3. Add a case to `sourceFor` in [`examples/read-formats`](../examples/read-formats).

The proof loop ranges over `Fixtures`, so step 2 is what enrolls the format in the cross-format assertion.
