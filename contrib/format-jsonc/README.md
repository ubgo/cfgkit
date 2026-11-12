# cfgkit/contrib/format-jsonc

**Support level: best-effort** — complete and tested, built on `github.com/ubgo/textdata/jsonc`, which is not tagged yet. See [the catalogue](../../docs/catalogue.md#support-levels).

JSONC files and documents (JSON with `//` and `/* */` comments, trailing commas and an optional BOM) as a [`cfgkit`](https://github.com/ubgo/cfgkit) structured source, with errors and provenance that point at `file:line`.

```go
import jsoncsrc "github.com/ubgo/cfgkit/contrib/format-jsonc"
```

Its dependency is `github.com/ubgo/textdata/jsonc` (stdlib only).

## Why this is a separate module

The `cfgkit` root module has exactly one dependency, and it keeps that promise by putting anything carrying another into its own module. A program that never reads JSONC never compiles this package.

## Use it

```go
src := jsoncsrc.File("config.jsonc")

cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
	src,
	cfgkit.FromEnviron(), // later source wins
))
```

| Constructor | Reads |
|---|---|
| `File(path, opts...)` | a JSONC file, re-read on every Apply; **a missing file is not an error** |
| `Document(name, doc, opts...)` | a JSONC document you already hold, e.g. from `go:embed`; `name` appears in errors and provenance |

`opts` are `github.com/ubgo/textdata/jsonc` options, passed through unchanged:

| Option | Effect |
|---|---|
| none | comments and trailing commas accepted, nothing expanded |
| `jsonc.WithExpand()` | `${VAR}`, `${VAR:-default}`, `${VAR:?message}` in string values are expanded before decoding |
| `jsonc.WithLookup(fn)` | where `${NAME}` resolves; the package never reads the environment on its own, so pass `os.LookupEnv` to use it |
| `jsonc.WithPlugin(p...)` | textdata plugins: resolvers (`${gopass:...}`), transformers, a command runner |
| `jsonc.WithCommandSubstitution(true)` | allow `$(command)`; off by default, turn it on only for files you trust |
| `jsonc.Strict()` | plain JSON: comments and trailing commas are errors |

```go
jsoncsrc.File("config.jsonc", jsonc.WithExpand(), jsonc.WithLookup(os.LookupEnv))
```

Fields are matched by `json:` tags, by `encoding/json` itself: member names match case-insensitively, the last duplicate wins, embedded structs are promoted, and a field the document does not mention keeps whatever it already had, so a JSONC file layers cleanly under `.env` files and the environment.

## Provenance: which line set this value

cfgkit's `Explain` names the source (`jsonc:config.jsonc`). The source itself knows the line, after a `Load`:

```go
for path, loc := range src.Origins(res, cfg) {
	fmt.Println(path, loc) // Server.Port config.jsonc:7
}

loc, ok := src.LocateField(cfg, "Server.Port") // any field, env-tagged or not
loc, ok = src.Locate("server.port")           // by jsonc path or JSON Pointer
all := src.Locations()                         // every value, in document order
```

| Method | Answers |
|---|---|
| `Origins(res, cfg)` | every field cfgkit attributes to this source (fields with an `env:` tag; cfgkit records only those), Go path to `Location` |
| `LocateField(cfg, goPath)` | where the value of one Go field path was written; `cfg` supplies the type for `json:` tag translation |
| `Locate(path)` | where the value at a jsonc path (`a.b[2]`) or JSON Pointer (`/a/b/2`) was written |
| `Locations()` | every scalar and empty container, in document order |

A `Location` holds `File`, the canonical `Path`, the value's `Pos` and the member name's `KeyPos` (line, byte column, offset). `String()` renders `file:line` at the member name.

Locations describe the file as written, before expansion, and the document the most recent `Apply` read: after a reload they follow the new contents.

## Errors carry file:line:col

| Situation | Result |
|---|---|
| The file does not exist | **not an error**: defaults stand, no locations |
| The file exists but cannot be read | error |
| Syntax error | `config.jsonc:3:3: missing comma ...` |
| A value of the wrong type | `config.jsonc:7:13: json: cannot unmarshal string into ... of type int`, at the value |
| `${VAR:?message}` unset, with expansion on | `config.jsonc:2:11: ... required variable ...`, at the string |

Errors are `*pos.Error` / `pos.ErrorList` from `github.com/ubgo/textdata/pos`, wrapped by cfgkit's `*SourceError`; `pos.First(err)` returns the first position.

## Gotchas

**Slices and maps replace, they do not merge.** Nested structs merge field by field. This is `encoding/json` behaviour.

**JSON5 is not JSONC.** Unquoted keys, single quotes, hex numbers and `NaN` are errors.

**Integers stay exact.** The document is decoded straight into the struct, never through a map of `float64`, so an `int64` above 2^53 survives, with or without expansion.

**An identical value is invisible to cfgkit's provenance.** A source that writes the value already present leaves the earlier origin standing in `Explain`, and so in `Origins`. `LocateField` still answers where this file wrote it.

## Development

```sh
task test
task ci          # fmt-check + vet + race tests
```

Until `github.com/ubgo/textdata/jsonc` is tagged, build through a workspace that uses a local textdata checkout next to this repository; the module runs `cfgkittest.RunStructuredTests`, a fuzz target over Load and every provenance call, and table tests for errors, expansion and provenance.
