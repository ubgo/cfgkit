# cfgkit/contrib/source-s3

**Support level: supported** — 98.1% covered, with the API faked. See [the catalogue](../../docs/catalogue.md#support-levels).

Read a configuration document from an **S3 object**.

```go
import s3src "github.com/ubgo/cfgkit/contrib/source-s3"

cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
	s3src.JSON("my-bucket", "config/prod.json"),
	cfgkit.FromEnviron(),
))
```

Like [`source-ssm`](../source-ssm/README.md), this takes `aws-sdk-go-v2` as a real dependency — [why](../source-ssm/README.md#this-one-takes-a-real-dependency).

## Format-agnostic on purpose

An S3 object is **bytes**. What they mean is your decision, so this module fetches and hands them to a decoder you name:

```go
s3src.Object("my-bucket", "config/prod.yaml", yaml.Unmarshal)
s3src.Object("my-bucket", "config/prod.toml", toml.Unmarshal)
s3src.JSON("my-bucket", "config/prod.json")     // encoding/json, no extra dependency
```

That is what keeps this module free of a format dependency: YAML, TOML and HCL compose through the adapters that already parse them, and this one never has to pick.

## Reads are capped

**8 MiB**, and the limit is *checked* rather than silently truncating — a half-read configuration document parses to *something*, and something is worse than an error.

The cap exists because the object's size is decided by whoever can write the bucket. Without it, a mistakenly-uploaded database dump is an out-of-memory at boot rather than a message.

## Both not-found shapes

S3 answers **`NoSuchKey`** when the caller may list the bucket, and a bare **`NotFound`** when it may not. A role with `GetObject` but no `ListBucket` therefore sees a different error for the same missing object, and `Optional()` honours both — which is why the check looks redundant and is not.

```go
s3src.JSON("my-bucket", "config/overrides.json", s3src.Optional())
```

`Optional()` means *absent is fine*, not *errors are fine*: an access-denied still fails the load.

## Versioning

```go
s3src.JSON("my-bucket", "config/prod.json", s3src.WithVersionID("v-123"))
```

Worth doing in a versioned bucket: it turns *"whatever is in the bucket right now"* into a value that changes only when a deployment changes it.

## Options

| Option | Default |
|---|---|
| `WithClient(c)` | built from the SDK's credential chain |
| `WithContext(ctx)` | `context.Background()` |
| `WithVersionID(id)` | the current version |
| `Optional()` | a missing object is an error |

## Testing

```sh
task ci
```

`API` is an interface, so the suite needs no AWS, no credentials and no network — including a test that drops the connection part-way through the object.
