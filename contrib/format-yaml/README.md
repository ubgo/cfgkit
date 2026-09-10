# cfgkit/contrib/format-yaml

**Support level: supported** — used by the authors, and the shared conformance suite covers the overlay behaviour the layering depends on. See [the catalogue](../../docs/catalogue.md#support-levels).

YAML documents and files as a [`cfgkit`](https://github.com/ubgo/cfgkit) structured source.

```go
import yamlsrc "github.com/ubgo/cfgkit/contrib/format-yaml"
```

**Support level: supported.** Its dependency is `gopkg.in/yaml.v3`.

## Why this is a separate module

The `cfgkit` root module has exactly one dependency, and it keeps that promise by putting anything carrying another into its own module. A program that never reads YAML never compiles this package, never downloads a YAML parser, and never sees one in its `go.sum`.

The adapter itself is four lines. Everything else here is documentation.

## Use it

```go
cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
	yamlsrc.File("config.yaml"),
	cfgkit.FromEnviron(),          // later source wins
))
```

Two constructors:

| Constructor | Reads |
|---|---|
| `File(path)` | a YAML file; **a missing file is not an error** |
| `Source(name, doc)` | a YAML document you already hold, e.g. from `go:embed` |

```go
//go:embed config.yaml
var configYAML []byte

yamlsrc.Source("embedded", configYAML)
```

## Fields are matched by `yaml:` tags

```go
type Config struct {
	Name string `yaml:"name" env:"NAME" default:"default-name"`
	Port int    `yaml:"port" env:"PORT" default:"8080"`
}
```

Two independent tags on the same field, and neither constrains the other:

- `yaml:` — how **this** source finds the field, by nested path
- `env:` — how flat sources find it, by key

A field the document does not mention keeps whatever it already had, so YAML layers cleanly under `.env` files and the environment.

## Missing versus broken

| Situation | Result |
|---|---|
| The file does not exist | **not an error** — defaults stand |
| The file exists but cannot be read | error |
| The file exists but is malformed YAML | error |

That distinction is deliberate and shared with the core's `FromFiles`. It is what lets a program ship with no config file at all, while a broken file still fails loudly instead of masquerading as an absent one.

## Layering with flat sources

```go
cfg, _, err := cfgkit.Load[Config](cfgkit.WithSources(
	yamlsrc.Source("yaml", []byte("name: from-yaml\nport: 1111\n")),
	cfgkit.FromMap(map[string]string{"PORT": "2222"}),
))
// cfg.Port == 2222   — the later flat source wins
// cfg.Name == "from-yaml"  — a key only the YAML sets still applies
```

Both kinds live in one ordered list. A later source wins regardless of kind.

## Gotchas

**Slices replace, they do not merge.** If defaults set `Hosts: ["localhost"]` and the document sets `["a","b"]`, the result is `["a","b"]` — not all three. Nested structs merge field by field; slices and maps replace wholesale. This is `yaml.v3` behaviour, matching `encoding/json`.

**YAML's type inference can surprise you.** `port: 08` is an error in YAML (an invalid octal), and `version: 1.10` becomes the float `1.1`. Quote values whose textual form matters: `version: "1.10"`.

**`yaml:` tags are lowercase by default.** `yaml.v3` lowercases a field name when no tag is present, so `LogsSourceID` looks for `logssourceid`. Tag it explicitly rather than relying on that.

**Provenance works, with one blind spot.** A field this source sets is attributed to it in `Explain`. A source that writes a value *identical* to the one already there is invisible, and the earlier origin stands — the value is the same either way.

## Development

```sh
task test        # includes the shared conformance suite
task ci          # fmt-check + vet + race tests
task test:cover
```

The module runs `cfgkittest.RunStructuredTests`, so it inherits every guarantee the core makes about structured sources rather than re-asserting them by hand.
