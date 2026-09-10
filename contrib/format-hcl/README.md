# cfgkit/contrib/format-hcl

**Support level: supported** — 100% covered. See [the catalogue](../../docs/catalogue.md#support-levels).

Read **HCL** documents and files as a cfgkit structured source.

```go
import hclsrc "github.com/ubgo/cfgkit/contrib/format-hcl"

cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
	hclsrc.File("config.hcl"),
	cfgkit.FromEnviron(),          // still wins over the file
))
```

## Blocks are why HCL is a structured source

```hcl
name = "billing"

database {
  host = "db.internal"
  port = 6543
}
```

```go
type Config struct {
	Name     string    `hcl:"name,optional" env:"NAME" default:"app"`
	Database *Database `hcl:"database,block" env:",prefix=DB_"`
}
```

Blocks are HCL's reason to exist, and they map onto nested structs the way a configuration tree already wants to be shaped. That is what a *structured* source is for — and why this differs from [INI](../format-ini/README.md) and [properties](../format-properties/README.md), which are flat.

Native integers, booleans, floats and lists arrive without a string round trip.

## You need both tag sets

```go
Name string `hcl:"name,optional" env:"NAME"`
```

`gohcl` is **stricter than `encoding/json`**: it will not guess a field name, so the `hcl:` tag is required rather than optional. The `env:` tag is what lets the same struct also bind from a `.env` file or the environment.

That strictness is a feature in the other direction too — a typo'd argument is a **decode error**, not something silently ignored:

```
decoding HCL: config.hcl:1,1-5: Unsupported argument; An argument named "nmae" is not expected here.
```

## Diagnostics keep their position

HCL reports errors with a line and column, and this adapter keeps them rather than flattening to a bare message. `config.hcl:2,1-5` is navigable; *"invalid HCL"* sends a reader to read the whole file.

The stage is named too — `parsing HCL` or `decoding HCL` — because they fail for different reasons and have different fixes.

The `filename` argument to `Source` is used **only** for those diagnostics; it is never read from disk. Passing `""` works and produces position-only messages.

## Absent, and broken, are different

| | |
|---|---|
| The file does not exist | **silent** — shipping without a config file is normal |
| The file exists and cannot be read or parsed | **an error** |

## It is a separate module

```
require (
	github.com/hashicorp/hcl/v2 v2.24.0
	github.com/ubgo/cfgkit v0.0.0
)
```

The HCL parser is substantial, and the core promises one dependency. A program that never reads HCL never compiles this package.

## API

| | |
|---|---|
| `Source(filename, doc []byte)` | a document in memory; `filename` labels diagnostics |
| `File(path)` | an HCL file on disk |

## Testing

```sh
task ci
```

100% statement coverage, including both failure stages and both halves of the absent-versus-broken rule.
