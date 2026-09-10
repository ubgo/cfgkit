# read-parameterstore — AWS Systems Manager

```sh
go run ./read-parameterstore
```

**Runs with no AWS account**, through the module's `API` seam.

## Real usage

```go
ssm.Path("/acme/checkout/")
```

Decryption is **on by default**, because a `SecureString` you cannot read is not configuration. `WithoutDecryption()` opts out.

## What it prints

```
ssm.internal:8443 origins=[https://acme.test https://www.acme.test]

parameters that matched no field: 10

FIELD     KEY       VALUE                                    SOURCE
Host      HOST      ssm.internal                             ssm:/acme/checkout
Origins   ORIGINS   https://acme.test,https://www.acme.test  ssm:/acme/checkout
Password  PASSWORD  ••••••                                   ssm:/acme/checkout
Port      PORT      8443                                     ssm:/acme/checkout
```

## Paging is not optional

This is the failure the example exists to demonstrate. **AWS returns at most 10 parameters per call.** A configuration of eleven silently loses one if the second page is never fetched — no error, no warning, just a field quietly sitting on its default.

The fake pages at exactly the AWS boundary and the fixture holds more than one page, so the filler parameters can only be reported as unknown keys if page two was actually read. `TestPagingIsNotOptional` asserts that count.

That is the difference between a test that checks paging *exists* and one that would notice if it stopped working.

## Names arrive relative to the path

The store holds `/acme/checkout/HOST`; the struct says `env:"HOST"`. Same rule as Consul and etcd, for the same reason: one struct binds from Parameter Store and from a `.env` file.

## `StringList` needs no special handling

AWS's own list type arrives comma-joined, which is exactly what a `delim:","` tag already expects. No branch in the adapter, no special case in your struct.

## Next

- [`read-secretsmanager`](../read-secretsmanager) — for values that are secrets first
- [`read-s3`](../read-s3) — why the AWS adapters take the SDK
