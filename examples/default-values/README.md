# default-values — running on nothing at all

```sh
go run ./default-values
```

No sources. Not an empty file, not an empty map — **nothing**:

```go
cfg, res, err := cfgkit.Load[Config]()
```

and the program still starts. That is the property the rest of the library hangs off: a program that cannot boot without a config file cannot be tried, and a program that cannot be tried does not get adopted.

## Defaults live on the field they belong to

```go
type Config struct {
	Name    string        `env:"APP_NAME" default:"demo"`
	Port    int           `env:"PORT"     default:"8080"`
	Debug   bool          `env:"DEBUG"    default:"false"`
	Timeout time.Duration `env:"TIMEOUT"  default:"30s"`

	Origins []string `env:"ORIGINS" default:"http://localhost:3000,http://localhost:5173" delim:","`

	ExtraHeader string `env:"EXTRA_HEADER"`
}
```

Not in a `defaults.yaml` that drifts from the struct, and not in an `init()` that runs somewhere else. Reading the type tells you the whole surface *and* what happens when nobody sets anything.

`ExtraHeader` has no default because the zero value **is** the sensible answer. Writing `default:""` would say the same thing with more words.

## What it prints

```
demo :8080 debug=false timeout=30s
origins: [http://localhost:3000 http://localhost:5173]

FIELD        KEY           VALUE                                        SOURCE
Debug        DEBUG         false                                        default
ExtraHeader  EXTRA_HEADER                                               default
Name         APP_NAME      demo                                         default
Origins      ORIGINS       http://localhost:3000,http://localhost:5173  default
Port         PORT          8080                                         default
Timeout      TIMEOUT       30s                                          default
```

Every field appears, including the empty one. A field missing from the table would be a field nobody can audit, which would defeat the point of provenance.

## Defaults are decoded, not stored

`"30s"` is a string in the tag and a `time.Duration` in the struct. `"a,b"` is a string in the tag and a `[]string` in the struct. Both go through the **same decoding path a real value takes** — which is what makes a default trustworthy.

A system that stored defaults raw and decoded only overrides could hold a default the type cannot represent, and you would find out the first time somebody set that field in production. Here `default:"soon"` on a `time.Duration` fails at load, on the default path, on every machine.

This is pinned by a test rather than described, because it is the kind of guarantee that quietly stops being true.

## `Defaulter`, for what a tag cannot say

A tag holds a constant. When the default is computed, implement the interface instead:

```go
func (c *Config) Defaults() {
	if c.Name == "" {
		c.Name = deriveFromHostname()
	}
}
```

It runs **before** any source, so what it sets remains overridable. The job is to supply a starting value, not to win. When the computation is substantial, [`read-struct`](../read-struct) is the cleaner shape — it keeps the baseline as data instead of mutation.

## Next

- [`read-struct`](../read-struct) — a baseline computed at startup
- [`precedence`](../precedence) — what overrides a default, and how you see it
- [`validation`](../validation) — fields that must **not** have a default
