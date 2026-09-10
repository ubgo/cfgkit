# cfgkit/contrib/format-toml

**Support level: supported** — 100% covered, and it passes the shared conformance suite that pins overlay behaviour. See [the catalogue](../../docs/catalogue.md#support-levels).

Read **TOML** documents and files as a cfgkit structured source.

```go
import tomlsrc "github.com/ubgo/cfgkit/contrib/format-toml"

cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
	tomlsrc.File("config.toml"),
	cfgkit.FromEnviron(),          // still wins over the file
))
```

## Why TOML gets an adapter

Because its **tables map onto nested structs** the way a configuration tree already wants to be shaped:

```toml
name = "billing"

[database]
host = "db.internal"
port = 6543
```

```go
type App struct {
	Name     string   `toml:"name" env:"NAME" default:"app"`
	Database Database `toml:"database" env:",prefix=DB_"`
}
```

That is what a *structured* source is for, and what a flat key/value source cannot express — the same reason cfgkit models the two kinds separately at all. See [Sources](../../docs/sources.md#why-they-cannot-be-one-interface).

## Native types arrive intact

Unlike a `.env` file, TOML has real integers, booleans, floats, arrays and datetimes, so nothing is round-tripped through a string and re-parsed:

```toml
port    = 9000
debug   = true
timeout = "30s"
ratio   = 1.5
hosts   = ["a.test", "b.test"]
```

`time.Duration` still comes from a string, because TOML's own duration type does not exist — that is a TOML fact rather than a cfgkit one.

## It is a separate module

```
require (
	github.com/BurntSushi/toml v1.6.0
	github.com/ubgo/cfgkit v0.0.0
)
```

It carries a parser, and the core promises exactly one dependency. A program that never reads TOML never compiles this package and never sees `BurntSushi/toml` in its `go.sum`. That is the whole rule for the catalogue.

## Absent, and broken, are different

| | |
|---|---|
| The file does not exist | **silent** — shipping without a config file is normal, and matches `FromFiles` |
| The file exists and cannot be read or parsed | **an error** |

Without the second, a broken configuration would be indistinguishable from an absent one, and a deploy would proceed on defaults it was never meant to use. A syntax error fails the load rather than binding a partial document — half a configuration is worse than none, because it looks like it worked.

## Overlay, not replace

A document supplies what it mentions and nothing else. A field the file does not name keeps whatever it had, so a later source can still win and an earlier value is not erased by silence:

```go
tomlsrc.Source("app.toml", []byte(`name = "from-toml"`)),
cfgkit.FromMap(map[string]string{"PORT": "7000"}),   // still wins
```

This is the property every structured source must have, and it is asserted by `cfgkittest.RunStructuredTests` rather than trusted.

## API

| | |
|---|---|
| `Source(name, doc []byte)` | a TOML document already in memory — embedded, fetched, or generated |
| `File(path)` | a TOML file on disk |

`name` appears in `Explain` as the origin, so give it something a reader will recognise.

## Testing

```sh
task ci
```

100% statement coverage, including both halves of the absent-versus-broken rule.
