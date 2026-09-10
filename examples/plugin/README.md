# Plugin configuration — when the host cannot name the type

A runnable answer to the one case where *"just pass the nested struct"* does not work: a **plugin whose config type the host cannot name at compile time.**

```sh
go run ./examples/plugin
PORT=9000 ACME_ENDPOINT=https://acme.prod ACME_API_KEY=sk_live go run ./examples/plugin
```

The output below is pinned by `example_test.go`, so it fails the gate rather than rotting.

## The setup

`acme/acme.go` is the plugin. Its config type is **unexported**:

```go
package acme

type config struct {
	Endpoint string `env:"ENDPOINT" default:"https://api.acme.test"`
	Retries  int    `env:"RETRIES" default:"3"`
	APIKey   string `env:"API_KEY" secret:"true"`
}
```

No other package can declare a field of that type, embed it, or unmarshal into it. `main.go` is the host, and its own struct has nothing about acme in it:

```go
type hostConfig struct {
	Port int    `env:"PORT" default:"8080"`
	Name string `env:"APP_NAME" default:"demo"`
}
```

## The whole mechanism, in one line

```go
p.Configure(cfgkit.FromPrefixedEnviron("ACME_"))
```

The host passes **where to read, not what was read.** `FromPrefixedEnviron("ACME_")` is a source meaning *"the environment, restricted to `ACME_*`, with the prefix stripped."* The plugin then runs its own `Load` against it, so when it asks for `ENDPOINT` the source looks up `ACME_ENDPOINT` — and the plugin never knows a prefix was involved.

```go
func (p *Plugin) Configure(sources ...any) error {
	cfg, res, err := cfgkit.Load[config](cfgkit.WithSources(sources...))
	if err != nil {
		return err
	}
	p.cfg, p.res = cfg, res
	return nil
}
```

The second `Load` is legal, cheap and fully isolated because cfgkit has **no global state** — there is no shared registry for two loads to collide in.

## What it produces

With nothing set, both host and plugin run on compiled-in defaults:

```
host: demo listening on :8080

the HOST's Explain — only the host's fields:
FIELD  KEY       VALUE  SOURCE
Name   APP_NAME  demo   default
Port   PORT      8080   default

the HOST using the plugin: endpoint=https://api.acme.test retries=3

the PLUGIN's Explain — only the plugin's fields, secret masked:
FIELD     KEY       VALUE                  SOURCE
APIKey    API_KEY   ••••••                 default
Endpoint  ENDPOINT  https://api.acme.test  default
Retries   RETRIES   3                      default
```

With the environment set:

```
PORT=9000 ACME_ENDPOINT=https://acme.prod ACME_RETRIES=5 ACME_API_KEY=sk_live_secret
```

```
host: demo listening on :9000

FIELD  KEY       VALUE  SOURCE
Port   PORT      9000   environ

FIELD     KEY       VALUE              SOURCE
APIKey    API_KEY   ••••••             environ:ACME_
Endpoint  ENDPOINT  https://acme.prod  environ:ACME_
Retries   RETRIES   5                  environ:ACME_
```

## What to notice

| | |
|---|---|
| **Two separate reports** | the host's `Explain` shows 2 fields, the plugin's shows 3. Neither is cluttered with the other's. |
| **`environ:ACME_` as the origin** | provenance names the prefixed view, so debugging still says exactly where a value came from. |
| **`••••••`** | the plugin marked its key `secret:"true"` and that is honoured in the plugin's own output. The host never handles the value. |
| **No key collisions** | both structs could want a bare `PORT`; the prefix keeps them apart, which is asserted by a test. |
| **The plugin's own validation** | `ACME_RETRIES=99` fails the run against the plugin's `Range(0, 10)` — a rule the host never wrote and does not know about. |
| **The plugin's own contract** | `acme.Document` emits the `ACME_` keys, so a deployment can be told what exists without the host maintaining that list. |

## Why there is no `Sub()` or `Cut()`

viper and koanf hold an untyped `map[string]any`. There is no struct to hand a component, so `Sub("acme")` cuts a smaller map out for the component to unmarshal.

cfgkit's tree is typed from the start, so that operation has no subject. Where the type is known, you pass the field:

```go
startEmailSender(cfg.SMTP)
```

Where it is not — this example — the host passes a **source** and the component brings its own type, defaults, validation, secrets, provenance and contract. That is strictly more than a sub-tree of the host's config, and it needed no API.

Full reasoning: [`docs/recipes.md`](../../docs/recipes.md#a-component-that-owns-its-own-configuration).
