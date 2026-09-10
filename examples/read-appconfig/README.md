# read-appconfig — AWS AppConfig

```sh
go run ./read-appconfig
```

**Runs with no AWS account**, through the module's `API` seam.

## A two-call protocol

AppConfig is not a get. You **start a session**, then **read from it**:

```
calls: [StartConfigurationSession GetLatestConfiguration]
```

The adapter makes both and names **which one failed**, because they fail for different reasons: permissions on the profile versus nothing deployed to it. One opaque error covering both would send you to the wrong console page.

## Real usage

```go
acsrc.JSON("checkout", "production", "config")
```

Three coordinates — application, environment, profile — and all three appear in the `SOURCE` column, because "appconfig" alone would not distinguish staging from production.

## What it prints

```
checkout on 0.0.0.0:9100
calls: [StartConfigurationSession GetLatestConfiguration]

FIELD        KEY      VALUE     SOURCE
Server.Host  HOST     0.0.0.0   default
Server.Port  PORT     9100      appconfig:checkout/production/config
Service      SERVICE  checkout  appconfig:checkout/production/config
```

## Empty content means two different things

On a *later* poll, an empty response means "unchanged". On the **first** call of a session it means "nothing deployed to this profile".

cfgkit reads once, so it treats the empty first response as an **error**. The other reading would bind an empty document and look like it worked — a service booting on defaults while its operator believes the profile was applied.

## No session is kept

Polling for changes is [`Watcher`](../../docs/reload.md)'s job. A source that held a session open would be doing lifecycle management the caller never asked for, and would keep a token alive across a config reload it knows nothing about.

## Next

- [`read-s3`](../read-s3) — the simpler AWS document source
- [`read-parameterstore`](../read-parameterstore) — flat keys rather than a document
