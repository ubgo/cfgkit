# Writing an adapter

An adapter connects `cfgkit` to a backend or a file format. Writing one is a few dozen lines. Most of this page is about the rules that keep twenty of them behaving identically.

## First: do you need a module at all?

Often not. Any backend is a closure:

```go
src := cfgkit.SourceFunc("vault", func(key string) (string, bool, error) { ... })
```

Any format is a closure:

```go
src := cfgkit.StructuredFunc("config.yaml", func(dst any) error { ... })
```

**Write a module when the adapter is worth sharing.** Write a closure when it is yours alone. Neither is second-class — the built-in sources and the closures use the same interfaces.

## The rule that decides placement

> **A separate module exists to keep dependencies out, not to keep concerns apart.**

The `cfgkit` root module has exactly one dependency. An adapter carrying a YAML parser, a Vault client, or pflag goes in its own module under `contrib/`, so a consumer compiles only what it imports — and downloads only what it compiles.

Naming follows `<area>-<vendor>`:

| Prefix | For | Examples |
|---|---|---|
| `format-` | a parser | `format-yaml`, `format-toml` |
| `source-` | a flat backend | `source-vault`, `source-k8s` |
| `flags-` | a flag set | `flags-pflag` |

## The module layout

Copy `contrib/format-yaml` — it is the smallest complete example.

```
contrib/format-yaml/
  go.mod          module github.com/ubgo/cfgkit/contrib/format-yaml
  yaml.go         the adapter
  yaml_test.go    conformance suite + its own tests
  README.md       what it adds, how to use it, its gotchas
  LICENSE
  Taskfile.yml    the same tasks as every other module
  .gitignore      cover.out
```

The `go.mod` requires the root module by version. During development a `replace` in the repository's **`go.work`** — never in the module's own `go.mod` — points at the local checkout, so the published file stays honest.

## A flat source

```go
func Source(client *vault.Client, path string) (cfgkit.Source, error) {
	// PRE-LOAD. Lookup is called once per bound field, so a source that dials
	// the network per key turns a 200-field config into 200 round-trips.
	secrets, err := client.ReadAll(path)
	if err != nil {
		return nil, err
	}
	return cfgkit.SourceFunc("vault:"+path, func(key string) (string, bool, error) {
		v, ok := secrets[key]
		return v, ok, nil
	}), nil
}
```

The three return values are the whole contract:

| Return | Meaning |
|---|---|
| `value, true, nil` | this source claims the key |
| `"", false, nil` | **no opinion** — resolution continues to the next source |
| `"", false, err` | the source failed — **aborts the whole Load** |

**Never turn a backend failure into a miss.** An unreachable secret store that reads as "unset" is a deploy proceeding with an empty password.

**An empty value is a value.** `"", true, nil` means the backend says the key is empty. That is different from having no opinion, and conflating them silently resurrects a default the operator cleared.

## A structured source

```go
func File(path string) cfgkit.StructuredSource {
	return cfgkit.StructuredFunc("yaml:"+path, func(dst any) error {
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil      // absent is normal
			}
			return err          // present-but-unreadable is not
		}
		return yaml.Unmarshal(b, dst)
	})
}
```

Two rules:

- **A missing file is not an error.** That is what lets a program ship without one. A file that exists but cannot be read or parsed **is** an error — a broken config must never look like an absent config.
- **Leave absent fields untouched.** `encoding/json` and `yaml.v3` do this natively. An implementation that zeroes unmentioned fields breaks layering entirely.

## A flag source

Flag sources are keyed by **flag name**, not by env key, and apply only to fields carrying a `flag:` tag. Build the map of flags the user actually typed and hand it over:

```go
func Source(fs *pflag.FlagSet) cfgkit.Source {
	typed := make(map[string]string)
	fs.Visit(func(f *pflag.Flag) {      // Visit, never VisitAll
		typed[f.Name] = f.Value.String()
	})
	return cfgkit.FlagSource("pflag", typed)
}
```

**Only typed flags.** A flag declared with a default but never typed must contribute nothing, or its default silently beats every config file. See [`contrib/flags-pflag`](../contrib/flags-pflag/README.md) for the full explanation.

## Testing: against a fake, never a live service

> **An adapter whose tests need a running Vault will not be run, and an untested adapter is worse than none — because it looks supported.**

Wrap the vendor SDK behind a one-method interface and substitute a fake. `cfgkittest.Fake` is that shape:

```go
type Fake struct {
	Values map[string]string
	Err    error      // set to prove a failure aborts rather than reading as a miss
	Calls  []string   // assert you pre-loaded instead of dialling per key
}
```

## Run the shared conformance suite

This is what keeps twenty adapters consistent without twenty authors each remembering the rules.

```go
func TestConformance(t *testing.T) {
	cfgkittest.RunSourceTests(t, func(values map[string]string) cfgkit.Source {
		return mypkg.Source(values)
	})
}
```

`RunSourceTests` asserts:

- a supplied value reaches the struct **and** the source names itself in the provenance record
- a miss leaves a compiled-in default alone
- an empty value is a value, not a miss

`RunFailureTest` asserts a failing source aborts with a `*cfgkit.SourceError`.

`RunStructuredTests` asserts a document merges, the source is attributed as the origin, and fields the document does not mention are left untouched.

The built-in sources run the same suite, so they are the reference implementation rather than a special case.

## The README each module owes

State the support level honestly. A module used in production is supported; one written speculatively is best-effort until someone depends on it. Users can price that in; they cannot price in a guess.

Cover: what it adds, the constructor with a snippet, its own gotchas, and the dependency it carries.

## Checklist

- [ ] Own `go.mod`; the root module's dependency count is unchanged
- [ ] Pre-loads rather than dialling per key (flat sources)
- [ ] A backend failure aborts; it is never a miss
- [ ] A missing file is not an error; an unreadable one is
- [ ] Absent fields are left untouched (structured sources)
- [ ] Only user-typed flags are read (flag sources)
- [ ] The shared conformance suite passes
- [ ] Tests use a fake, never a live service
- [ ] README, LICENSE, Taskfile, `.gitignore`
- [ ] Registered in the root `Taskfile.yml` `CONTRIB` list and in `go.work`
