# Types

The supported type set is deliberately **closed and small**. Anything outside it goes through one escape hatch, so the library never grows a type zoo and never has to say no to your type.

## The set

| Category | Types |
|---|---|
| Strings | `string` |
| Booleans | `bool` — anything `strconv.ParseBool` accepts (`true`, `1`, `t`, `T`, `TRUE`, …) |
| Signed integers | `int`, `int8`, `int16`, `int32`, `int64` |
| Unsigned integers | `uint`, `uint8`, `uint16`, `uint32`, `uint64` |
| Floats | `float32`, `float64` |
| Duration | `time.Duration` — `"30s"`, `"2m30s"`, `"1h"` |
| Time | `time.Time` — **RFC3339 only** |
| Slices | `[]T` for any supported scalar |
| Maps | `map[K]V` where both `K` and `V` are supported scalars |
| Pointers | `*T` for any supported scalar, allocated on demand |
| Structs | walked as sections, not bound |
| **Anything else** | must implement `encoding.TextUnmarshaler` |

```go
type Config struct {
	Timeout time.Duration `env:"TIMEOUT" default:"30s"`
	Origins []string      `env:"ORIGINS"`
	Ports   []int         `env:"PORTS" delim:";"`
}
```

```
2m30s [https://a.test https://b.test] [80 443]
```

## Slices

Split on `,` by default, or on whatever `delim:` says. **Elements are trimmed**, because `"a, b"` is what a human writes and `" b"` is never the intended value.

An empty string yields an **empty slice**, not a one-element slice containing `""`:

```
ORIGINS=          →  []string{}        (no origins)
ORIGINS=a         →  []string{"a"}
```

The opposite reading would silently produce an empty entry that fails far from its cause.

## Maps

**Use a map only when the key names are not known when you write the struct.** Feature flags, per-tenant limits, arbitrary extra headers — the cases where adding an entry should mean editing a `.env` file, not editing Go.

```go
type Config struct {
	Flags    map[string]string        `env:"FLAGS"`
	Limits   map[string]int           `env:"LIMITS"`
	Timeouts map[string]time.Duration `env:"TIMEOUTS"`
}
```

```
FLAGS=new-checkout:on,dark-mode:off
LIMITS=acme:1000,globex:500
TIMEOUTS=read:30s,write:1m
```

```
new-checkout=on dark-mode=off
acme=1000 globex=500
read=30s write=1m0s
```

Entries are separated by `,` (the `delim:` tag), and each entry's key from its value by `:` (the `kvdelim:` tag). Both keys and values go through the **same decoder every other field uses**, which is why `map[string]time.Duration` needs no extra code.

### The gotcha: only the FIRST separator splits

A map value is very often a URL or a `host:port`, so most entries contain more colons than the one that matters:

```
DSNS=primary:postgres://user@db1:5432/app,cache:redis://cache:6379
```

```
postgres://user@db1:5432/app
redis://cache:6379
```

Splitting on every colon would corrupt every connection string — the most common thing a map holds — so the split is on the first occurrence only. `caarlos0/env` and `go-envconfig` do the same.

If a **key** must contain a colon, or a value must contain a comma, change the separators rather than fighting them:

```go
Rules map[string]string `env:"RULES" delim:";" kvdelim:"="`
```

```
RULES=a=1,2,3;b=4      →  {"a": "1,2,3", "b": "4"}
```

**Quoting in a `.env` file does not protect a separator.** dotenv resolves quotes first and hands cfgkit one flat string, so by the time the map is split the quotes are gone:

```
NOTES="greeting:hello, world"
```

splits into `greeting:hello` and ` world`, and the second has no `:` — a decode error, not a two-word value. Change the separator instead:

```go
Notes map[string]string `env:"NOTES" delim:"|"`
```

### Rules that follow

| Input | Result | Why |
|---|---|---|
| `FLAGS=` | empty, non-nil map | same as slices: "no flags", never one flag with an empty name |
| *(key absent)* | `nil` | the field keeps its zero value, so you can tell "never configured" from "configured to hold nothing" |
| `FLAGS=a:1,a:2` | `{"a": "2"}` | last wins, matching how the source chain resolves any repeat |
| `FLAGS=ok:yes,broken` | error naming `broken` and `":"` | a missing separator is almost always a value that ate a comma |

Explain and Document render a map **sorted by key**, because Go randomises map iteration and the `git diff --exit-code .env.example` check in [Recipes](recipes.md#generate-and-enforce-the-contract) would otherwise fail at random. The rendered form is also exactly what the parser reads back, so `Document` output re-loads.

### When NOT to use a map

If you know the names, do not use a map:

```go
// Better — typed, autocompleted, a typo is a compile error.
type Flags struct {
	NewCheckout bool `env:"FLAG_NEW_CHECKOUT"`
	DarkMode    bool `env:"FLAG_DARK_MODE"`
}
```

A map gives you none of that. `cfg.Flags["dark-mdoe"]` compiles fine and silently returns `""`. There is also no `required` per entry, no per-entry default, and no `Explain` line per entry — the whole map is one field with one origin.

## Time and Duration

`time.Duration` is special-cased **before** the integer branch. It has an integer kind underneath, so without that check `"30s"` would fail to parse and `"30"` would silently mean *30 nanoseconds* — a bug that looks like a working timeout.

`time.Time` accepts **RFC3339 and nothing else**. Accepting several layouts would make `"2026-01-02"` mean different instants depending on which layout matched first; one unambiguous format is the safer trade.

## Pointers

`*T` for a scalar is allocated on demand and decoded into. This is how you express *"unset means nil, empty means set-to-empty"* — a distinction a bare `string` cannot make.

**When you need it:** whenever "the operator did not set this" and "the operator deliberately set this to nothing" mean different things. A `*bool` feature flag distinguishes *not configured* (fall back to a computed default) from *explicitly off*. A plain `bool` cannot: `false` is both.

```go
Ratio *float64 `env:"RATIO"`   // nil when RATIO is absent, non-nil when RATIO=""
```

## The escape hatch

**When you need this.** Sooner than you think. The moment a config value is not a plain scalar — a log level, a URL, an environment enum, a duration expressed oddly, a comma-separated set of feature flags — you want it to arrive as a real type rather than a `string` you re-parse at every use site.

The payoff is that **validation moves to where the type is defined**. A `LogLevel` that rejects `loud` in its own `UnmarshalText` is validated once, everywhere it is used, and the error names the field. The alternative — a `string` field plus an `OneOf` call in `Validate()` — puts the rule far from the type and lets any other code path skip it.

**A type outside the set must implement `encoding.TextUnmarshaler`.**

```go
type Level string

func (l *Level) UnmarshalText(b []byte) error {
	switch s := string(b); s {
	case "debug", "info", "warn", "error":
		*l = Level(s)
		return nil
	default:
		return fmt.Errorf("unknown level %q", s)
	}
}
```

Four lines, and now `Level` binds from any source, validates its own values, and reports its own error message. `net.IP`, `netip.Addr`, `uuid.UUID`, `slog.Level` and most library types already implement it.

**Why one hatch rather than a growing switch:** a library that special-cases each type grows forever and still cannot cover the next one. One stdlib interface covers all of them, and it puts the parsing rules where the type is defined rather than where it is used.

The hatch is checked **first**, so a user type always wins over the built-in handling of its underlying kind. That is what makes `Level` (a `string`) validate instead of accepting anything.

### `BinaryUnmarshaler` is also accepted

`*url.URL` implements only `encoding.BinaryUnmarshaler` — its `UnmarshalBinary` takes the URL text verbatim. A URL is far too common in configuration to exclude over that stdlib inconsistency, so the binary form is accepted as a fallback. Text is tried first, so a type implementing both keeps its textual meaning.

This was found by a test: before it, the walker treated `*url.URL` as a plain struct, descended into it, and allocated its internal `*url.Userinfo`.

## Decode failures are reported, never zeroed

```
Port (PORT from map): "eighty" is not a valid int
```

Every branch reports. A silent zero is the failure mode this library exists to prevent: a service comes up on the wrong port with nothing in the logs.

The message names the field path, the key an operator would set, the source that supplied the value, and the offending text.

**For a `secret:"true"` field the decoder's message is discarded entirely** and replaced with one built only from the field's declared type:

```
Password (DB_PASSWORD from file:.env): the supplied value is not a valid int
```

The field, the key and the expected type are all still there — everything an operator needs — and nothing derived from the value is. Discarding the whole message is deliberate: every decoder quotes the offending text, and a slice or map error quotes the offending element or key on top of that, so there is no safe subset to keep. An error message is a thing that gets logged, and a logged credential is a leak.

## Unsupported types fail loudly

```go
type Config struct {
	Ch chan int `env:"U_CHAN"`
}
```

```
unsupported type chan int (implement encoding.TextUnmarshaler for it)
```

Named, with the remedy. A skipped field would be worse: it would look configured and never be.

## Gotchas

**A struct with its own unmarshaler is a leaf.** `time.Time` and `*url.URL` are structs, but they bind from one key rather than being walked into. Any type of yours implementing the hatch behaves the same way.

**Unsigned types reject negatives.** `W_U=-1` is a decode error, not a wrap-around.

**Sized integers are range-checked.** `int8` rejects `200`.

**A map is a leaf, not a section.** `map[string]string` binds from ONE key. It is never walked into, so it does not behave like a nested struct.

**A map value may contain the key/value separator.** Only the first one splits — see the section above. This is the single most important thing to know about maps.

**An absent map is `nil`, an empty one is not.** `cfg.Flags == nil` means nothing ever set it; `len(cfg.Flags) == 0` with a non-nil map means someone deliberately set `FLAGS=`.
