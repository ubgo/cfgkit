# cfgkit/contrib/source-secretsmanager

**Support level: supported** — 97.1% covered, with the API faked. See [the catalogue](../../docs/catalogue.md#support-levels).

Read **AWS Secrets Manager** secrets as a cfgkit source.

```go
import secrets "github.com/ubgo/cfgkit/contrib/source-secretsmanager"

cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
	secrets.JSON("prod/app"),                              // a JSON object of key/values
	secrets.Whole("prod/db/password", "DATABASE_PASSWORD"), // a plain string secret
	cfgkit.FromEnviron(),
))
```

Like [`source-ssm`](../source-ssm/README.md), this takes `aws-sdk-go-v2` as a real dependency — [why](../source-ssm/README.md#this-one-takes-a-real-dependency).

## Two readings, because a guess would be worse

Secrets Manager stores a **blob**, and the console encourages JSON. So the reading is an argument rather than a sniff:

| | |
|---|---|
| `JSON(name)` | the payload is a JSON object; each member becomes a key |
| `Whole(name, key)` | the payload *is* the value, for one key |

Guessing from the content would be right nine times and silent the tenth. When you pick wrong, the error says so:

```
secret prod/db/password is not a JSON object (use secretsmanager.Whole for a plain string secret)
```

## The payload never enters an error

Not even the decoder's own message. A `json.SyntaxError` carries only an offset, but an `UnmarshalTypeError` **quotes the value it choked on** — and a secret is a thing that gets logged when an error is logged. So the decoder's error is dropped along with the payload.

## Binary secrets are refused by name

A certificate or a keystore is not configuration. Coercing it to a string would bind mojibake, so it fails with a message saying what it found.

## JSON values are rendered, not assumed

| In the secret | Becomes |
|---|---|
| `"9000"` | `9000` |
| `9000` (a JSON number) | `9000` — **not** `9000.000000` |
| `true` | `true` |
| `null` | `""` |
| `{"a":1}` | the JSON text, so an `UnmarshalText` field can still take it |

## Rotation

```go
secrets.JSON("prod/app", secrets.WithVersionStage("AWSPREVIOUS"))
```

`AWSPREVIOUS` is how a service that missed a rotation window can still connect while it is fixed. `WithVersionID` pins an exact version.

## `Optional()` means absent is fine, not errors are fine

A missing secret is tolerated; an **access-denied still fails the load**. Otherwise a misconfigured role looks like an empty secret, and the service starts on defaults it was never meant to use.

Not-found is matched on the SDK's error **type**, never on message text — message text is not an API.

## Options

| Option | Default |
|---|---|
| `WithClient(c)` | built from the SDK's credential chain |
| `WithContext(ctx)` | `context.Background()` |
| `WithVersionID(id)` | AWS's default (`AWSCURRENT`) |
| `WithVersionStage(s)` | AWS's default |
| `Optional()` | a missing secret is an error |

## Testing

```sh
task ci
```

`API` is an interface, so the suite needs no AWS, no credentials and no network.
