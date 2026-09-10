# read-s3 — a config document in an S3 object

```sh
go run ./read-s3
```

**Runs with no AWS account**, through the module's `API` seam.

## Why the AWS adapters take the SDK

Six sources in this catalogue have no dependencies, because on those platforms a credential is a file or a header. AWS is different: signing is straightforward, but **credential resolution** is not — environment, shared config, IMDS, IRSA web identity, SSO, assume-role chains, each evolving independently.

Reimplementing that would be a worse version of something the SDK does well, and getting it subtly wrong means a service that authenticates on a laptop and not in production. So the AWS modules take the dependency — which is exactly what `contrib/` exists to make safe. A program that never reads S3 never compiles it.

## The seam

```go
type API interface {
	GetObject(ctx, *s3.GetObjectInput, ...) (*s3.GetObjectOutput, error)
}
```

One method, because reading one object is one call. A narrow interface is what makes the fake in this example a few lines instead of a mock framework.

## Real usage

```go
s3.JSON("acme-config", "checkout/production.json")
```

## Format-agnostic on purpose

An object is **bytes**. `s3.JSON` is a convenience; `s3.Object(bucket, key, decode)` takes any decoder, so YAML or TOML compose without this module depending on either.

```go
s3.Object(bucket, key, func(doc []byte, dst any) error {
	return yaml.Unmarshal(doc, dst)
})
```

## What it prints

```
checkout on 0.0.0.0:9000

FIELD        KEY      VALUE     SOURCE
Server.Host  HOST     0.0.0.0   default
Server.Port  PORT     9000      s3:acme-config/checkout/production.json
Service      SERVICE  checkout  s3:acme-config/checkout/production.json
```

`server.host` is in no document and no variable, so the default stands: a structured source **merges onto** the struct rather than replacing it.

## Two safeguards worth knowing

- **Reads are capped at 8 MiB, and the limit is CHECKED** — a half-read document still parses into something, which is worse than failing.
- **Both not-found shapes are recognised.** S3 returns `NoSuchKey` or a bare `NotFound` depending on whether you hold `ListBucket`, and treating one as an unexpected error would make the behaviour depend on IAM policy.

## Next

- [`read-parameterstore`](../read-parameterstore) · [`read-secretsmanager`](../read-secretsmanager) · [`read-appconfig`](../read-appconfig)
