# read-formats — seven formats, one struct, identical values

```sh
go run ./read-formats
```

Every format module has its own tests, and each proves that module parses its own format. None of them can prove the thing you actually depend on: that **YAML, TOML, HCL, JSON, INI, `.properties` and `.env` all land on the same struct with the same values.** That is a cross-module claim, so it needs a shared fixture and a place outside every module to check it.

The fixture is [`mock/`](../../mock) — one canonical configuration written seven times, idiomatically in each format.

## What it prints

```
FORMAT      SHAPE  BINDS THE CANONICAL CONFIG
yaml        nested true
toml        nested true
hcl         nested true
json        nested true
ini         flat   true
properties  flat   true
env         flat   true

every format produced: service=checkout server=0.0.0.0:8080 db=postgres://db/checkout max_conns=25
```

## Two families, not seven special cases

| Shape | Formats | How a section reaches a nested struct |
|---|---|---|
| **Structured** | YAML, TOML, HCL, JSON | The document has its own shape and types. It merges onto the struct through the struct's document tags. |
| **Flat** | INI, `.properties`, `.env` | Every value is a string and there is no real nesting, so a section becomes part of the **key**: `server.port`. |

The flat family reaches nested structs with a prefix on the parent field:

```go
Server *Server `env:",prefix=server."`
```

The children then stay unprefixed (`env:"port"`), which is what lets the same child struct be reused under a different parent.

## The tag cost is real, and this is where you find out

One struct reading every format needs **four tag vocabularies**:

```go
Port int `json:"port" yaml:"port" toml:"port" hcl:"port,optional" env:"port"`
```

That looks like belt and braces until you hit the case this fixture set actually caught.

**yaml.v3 and BurntSushi/toml each read their own tag, and fall back to the lowercased Go field name.** For `Port` the fallback is `port`, which matches — so single-word fields work with no tag and everything seems fine. For `MaxConns` the fallback is `maxconns`, and the document says `max_conns`.

It does not error. It does not warn. The field binds as **zero**, and you get a connection pool of size 0 in production.

That is pinned by `TestMultiWordKeyNeedsAPerFormatTag`, which loads a struct identical to the canonical one except for the missing `yaml`/`toml` tags and asserts that `MaxConns` comes back 0 while `URL` still binds. If a decoder ever changes its fallback, that test fails and tells you the tags may no longer be needed — the failure is informative in both directions.

**A program reading only one format needs only that format's vocabulary.** The four-way tags here are the price of *this example's* ambition, not a tax on normal use.

## Adding a format

Add the fixture to [`mock/`](../../mock), add one line to `mock.Fixtures`, and add a case to `sourceFor`. The proof loop ranges over `mock.Fixtures`, so the new format joins the cross-format assertion automatically — a list that had to be updated in two places is a list that ends up covering six of seven.

## Next

- [`mock/`](../../mock) — the fixture set, and why it is not in any format module
- [`read-json`](../read-json) — structured merging in detail
- [`read-file`](../read-file) — the flat family, in detail
