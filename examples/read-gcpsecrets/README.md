# read-gcpsecrets — Google Secret Manager

```sh
go run ./read-gcpsecrets
```

**Runs with no GCP project.** The example stands up a fake API *and* a fake metadata server — faking both is the point, because on GCP the credential comes from the metadata server and that exchange is where real integrations break.

## No dependencies, unlike the AWS adapters

This is the rule the catalogue follows, applied in the other direction: *use the platform's own mechanism when the credential is a file or a header; use the vendor's library when the hard part is not the transport.*

Workload identity hands back a **finished bearer token** from one URL. There is no credential chain to reproduce and no request signing. So the whole adapter is `net/http`, and your `go.sum` gains nothing.

## Real usage

```go
gcp.Secret("checkout-db-password", "PASSWORD")
```

The project and the token both come from the metadata server the workload already runs against, so in-cluster that is the entire integration.

## What it prints

```
host=localhost password-loaded=true

FIELD     KEY       VALUE      SOURCE
Host      HOST      localhost  default
Password  PASSWORD  ••••••     gcpsecrets:checkout-db-password
```

## One secret, one key — stated, not guessed

Secret Manager stores **one value per secret**, so the mapping to a config key is explicit: `Secret(name, key)`. An adapter that tried to infer the key from the secret's name would guess, and guess differently from whoever named the secret.

## The metadata exchange is verified, not assumed

The fake metadata server **rejects any request without `Metadata-Flavor: Google`**. That header is what stops a confused-deputy attack from fetching a token. Because the fake refuses without it, a load that succeeds proves the header was sent — the test does not merely assume the exchange happened.

## Next

- [`read-azkeyvault`](../read-azkeyvault) — the same shape on Azure
- [`read-secretsmanager`](../read-secretsmanager) — the AWS equivalent, and why it differs
