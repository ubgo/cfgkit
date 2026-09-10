# read-struct — a baseline computed at startup

```sh
go run ./read-struct
CONCURRENCY=8 go run ./read-struct    # still overridable
```

A `default:` tag holds a **constant**. This is for the defaults that are not: computed from the machine, fetched from a metadata service, or shipped by a library for whoever embeds it.

## The shape

```go
type baseline struct {
	Region      string `json:"REGION"`
	Concurrency int    `json:"CONCURRENCY"`
	Endpoint    string `json:"ENDPOINT"`
}

cfgkit.Load[Config](cfgkit.WithSources(
	cfgkit.FromStruct("baseline", derive("large", "us-east-1")),
	cfgkit.FromEnviron(),
))
```

`FromStruct` round-trips through JSON, so **`json` tags are what it matches on**. That is not an implementation detail to work around — it is why the same call composes with nested structs for free, and why a config-service payload and an in-memory baseline are interchangeable.

The first argument is the name that shows up in `Explain`. Give it something a reader will recognise in a `SOURCE` column at 3am.

## What it prints

```
api in us-east-1: concurrency=64 endpoint=https://large.internal

FIELD        KEY          VALUE                   SOURCE
Concurrency  CONCURRENCY  64                      baseline
Endpoint     ENDPOINT     https://large.internal  baseline
Region       REGION       us-east-1               baseline
Service      SERVICE      api                     default

structured sources applied: baseline
```

`Service` is in no tier, so its tag default stands. A structured source **merges onto** the struct; it does not replace it. A source that replaced would blank every field it happened to omit, which is how config systems lose settings nobody edited.

## Being written in Go buys it no authority

`FromEnviron` is listed after it, so an operator still wins — pinned by a test, not just asserted. This matters more than it sounds: a baseline that could not be overridden would be a hardcoded value with extra ceremony, and the first incident requiring a quick override would find it immovable.

## When to use this instead of `Defaults()`

Both supply starting values. The difference is shape, not power:

| | Use |
|---|---|
| The value is computed from a couple of inputs | `FromStruct` — the baseline stays **data**, so it can be tested, logged and diffed on its own |
| You are patching one field conditionally | `Defaulter` — see [`default-values`](../default-values) |

`FromStruct` also shows up in `Explain` under its own name, so the layer is visible. A `Defaults()` method is invisible in provenance — its values report as `default`, indistinguishable from a tag.

## Next

- [`default-values`](../default-values) — constants, and the `Defaulter` interface
- [`read-json`](../read-json) — the same merge semantics from a document
- [`precedence`](../precedence) — where a baseline sits in a full stack
