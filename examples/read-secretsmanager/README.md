# read-secretsmanager — AWS Secrets Manager, both ways

```sh
go run ./read-secretsmanager
```

**Runs with no AWS account**, through the module's `API` seam.

## Two modes, as separate calls

```go
smsrc.JSON("acme/checkout/config")            // the secret is a JSON object
smsrc.Whole("acme/checkout/api-key", "API_KEY")  // the secret is one opaque value
```

They are **separate calls rather than a content sniff**, and that is the design decision worth explaining. Guessing whether a payload is JSON would be right nine times and silently wrong the tenth — a secret that happens to start with `{`, a JSON string that is really an opaque token. The tenth case is the one that pages someone.

So the caller states which shape it is, and asking for the wrong one produces a message that **names the fix**:

```
source secretsmanager:acme/checkout/api-key failed for HOST: secret acme/checkout/api-key
is not a JSON object (use secretsmanager.Whole for a plain string secret)
```

## What it prints

```
sm.internal:8500

FIELD     KEY       VALUE        SOURCE
APIKey    API_KEY   ••••••       secretsmanager:acme/checkout/api-key
Host      HOST      sm.internal  secretsmanager:acme/checkout/config
Password  PASSWORD  ••••••       secretsmanager:acme/checkout/config
Port      PORT      8500         secretsmanager:acme/checkout/config
```

Two secrets, and provenance says which supplied each field — needed the moment a rotation goes wrong and someone has to find the stale one.

## A source failure is reported per field

The error above repeats once for every field that asked the source for a value. That is why the example prints only the first line. It also means the `N problem(s)` header has to be right — a count that disagreed with the list would be actively misleading here, which is [a bug this work found and fixed](../validation).

## The payload never enters an error

A failed JSON decode names the secret, never its contents — the decoder's own message is dropped precisely because it would quote the bytes it choked on. Binary secrets are refused **by name** rather than rendered as garbage.

## Next

- [`read-parameterstore`](../read-parameterstore) — for configuration that is not secret-first
- [`read-kiln`](../read-kiln) — secrets kept in the repository instead
