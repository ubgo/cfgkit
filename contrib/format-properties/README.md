# cfgkit/contrib/format-properties

**Support level: supported** — 100% covered, and it passes the shared flat-source conformance suite. See [the catalogue](../../docs/catalogue.md#support-levels).

Read **Java-style `.properties`** files as a cfgkit source.

```go
import propsrc "github.com/ubgo/cfgkit/contrib/format-properties"

cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
	propsrc.File("application.properties"),
	cfgkit.FromEnviron(),
))
```

## What this is for

A polyglot shop that already has one. **A Spring Boot service and a Go service reading the same `application.properties`** is the case this exists for — which is why keys are used exactly as written, with no case folding and no translation to `SCREAMING_SNAKE`.

```properties
server.port=9000
database.host=db.internal
```

```go
type Config struct {
	Port int    `env:"server.port"`
	Host string `env:"database.host"`
}
```

Like [INI](../format-ini/README.md) and unlike YAML, this is a **flat** source: the format has no type system and no document shape, so it maps onto the same `Source` interface `.env` files use.

## Java's escaping is honoured

The main reason to use a parser rather than split on `=`:

```properties
long.value=one \
  two
unicode.value=café
colon.separated: also valid
spaced.key = trimmed
```

Continuation lines are joined, `\u` escapes decoded, `:` accepted as a separator, and whitespace around the separator trimmed. A naive reader mangles all four silently.

## `${...}` expansion is deliberately disabled

The format supports it. cfgkit does not use it here, because **references are resolved at the `.env` layer**, where a whole chain of files is visible ([Sources](../../docs/sources.md#var-references-are-resolved)).

Two expansion passes with different rules — depending on which source a value came from — is worse than one documented rule. So a value here is literally what the file says:

```properties
base=/srv
full=${base}/app     # binds as the text "${base}/app"
```

## Absent, and broken, are different

| | |
|---|---|
| The file does not exist | **silent** — shipping without a config file is normal |
| The file exists and cannot be read or parsed | **an error** |

An invalid `\u` escape is a parse error, not a value.

## Typo detection comes free

```
database.hsot (from application.properties) matched no field
```

## API

| | |
|---|---|
| `Source(name, doc []byte)` | a document already in memory |
| `File(path)` | a `.properties` file on disk |

## Testing

```sh
task ci
```

100% statement coverage, including the escaping rules and both halves of the absent-versus-broken rule.
