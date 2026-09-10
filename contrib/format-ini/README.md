# cfgkit/contrib/format-ini

**Support level: supported** — 100% covered, and it passes the shared flat-source conformance suite. See [the catalogue](../../docs/catalogue.md#support-levels).

Read **INI** documents and files as a cfgkit source.

```go
import inisrc "github.com/ubgo/cfgkit/contrib/format-ini"

cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
	inisrc.File("config.ini"),
	cfgkit.FromEnviron(),          // still wins over the file
))
```

## INI is a FLAT source, not a structured one

This is the design decision worth knowing, and it is why INI differs from the YAML and TOML adapters.

YAML, TOML and JSON have a type system and a document shape, so they **merge onto a struct** through their own tags. INI has neither: every value is a string, and its only structure is one level of sections. So it maps onto the same flat `Source` interface `.env` files use.

```ini
port = 8080

[database]
host = db.internal
user = app
```

```go
type Config struct {
	Port int    `env:"port"`
	Host string `env:"database.host"`
	User string `env:"database.user"`
}
```

A key outside any section is addressed by its own name. Everything else is `section.key`.

**Why a dot:** an INI key may itself contain underscores, and a separator that can appear inside a name makes the mapping ambiguous — the same reason cfgkit refuses to derive `HYPERDX_LOGS_SOURCE_ID` by splitting a Go field path.

## Names are used exactly as written

No case folding, no translation. `[Section] Key` and `[section] key` are two different keys, and both are addressable. An INI file is written by an operator, and guessing at a canonical case would make a working file stop working.

## Absent, and broken, are different

| | |
|---|---|
| The file does not exist | **silent** — shipping without a config file is normal |
| The file exists and cannot be read or parsed | **an error** |

Without the second, a broken configuration would be indistinguishable from an absent one, and a deploy would proceed on defaults it was never meant to use.

## Typo detection comes free

`cfgkit.KeyLister` is implemented, so a key in the file matching no field is reported with its section:

```
database.hsot (from app.ini) matched no field
```

## API

| | |
|---|---|
| `Source(name, doc []byte)` | an INI document already in memory |
| `File(path)` | an INI file on disk |
| `SectionSeparator` | the `.` joining section to key, exported so a caller can build a key name |

## Testing

```sh
task ci
```

100% statement coverage, including both halves of the absent-versus-broken rule.
