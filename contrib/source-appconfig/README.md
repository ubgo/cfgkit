# cfgkit/contrib/source-appconfig

**Support level: supported** — 97.3% covered, with both calls of the protocol faked. See [the catalogue](../../docs/catalogue.md#support-levels).

Read an **AWS AppConfig** configuration profile as a cfgkit source.

```go
import appconfig "github.com/ubgo/cfgkit/contrib/source-appconfig"

cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
	appconfig.JSON("my-app", "prod", "app-config"),
	cfgkit.FromEnviron(),
))
```

The three names are AppConfig's own: **application, environment, profile.**

Like [`source-ssm`](../source-ssm/README.md), this takes `aws-sdk-go-v2` as a real dependency — [why](../source-ssm/README.md#this-one-takes-a-real-dependency).

## AppConfig is a two-call protocol

That is the thing to know before using it:

1. `StartConfigurationSession` → a token
2. `GetLatestConfiguration(token)` → the content **and a next token**

Both calls are made, in order, and the failure message says **which one failed** — they fail for different reasons and have different fixes:

```
starting AppConfig session for my-app/prod/app-config: BadRequestException: ...
reading AppConfig my-app/prod/app-config: AccessDeniedException: ...
```

## Empty content means two different things

This is the subtlety the protocol hides, and getting it wrong is quiet:

| | |
|---|---|
| Empty on a **later poll** | *unchanged* — nothing new since the last token |
| Empty on the **first call of a session** | the profile has **no deployed content** |

cfgkit makes exactly one of each call, so an empty response here can only be the second — and it is an error. Treating it as "unchanged" would bind an empty document and look like a working read.

## No session is kept

cfgkit reads once, at boot. **Polling is [`cfgkit.Watcher`](../../docs/reload.md)'s job**, and each `Reload` starts a *fresh* session rather than carrying a token forward.

That is deliberate: holding a session open across reloads is what makes the emptiness rule above matter, and getting it wrong means a service that silently keeps its first configuration forever.

## Format-agnostic

AppConfig stores freeform documents — JSON, YAML, text — and reports a content type without parsing. So the reading is yours:

```go
appconfig.Configuration("my-app", "prod", "app-config", yaml.Unmarshal)
appconfig.JSON("my-app", "prod", "app-config")     // encoding/json, no extra dependency
```

## Options

| Option | Default |
|---|---|
| `WithClient(c)` | built from the SDK's credential chain |
| `WithContext(ctx)` | `context.Background()` |

## Testing

```sh
task ci
```

`API` covers both calls and is an interface, so the suite needs no AWS, no credentials and no network.
