# read-etcd — etcd v3

```sh
go run ./read-etcd
```

**Runs with no etcd installed**, and that is possible because of the adapter's central decision: it speaks etcd's **HTTP/JSON gateway** rather than the native gRPC API.

## Why the gateway

etcd's official client brings gRPC *and* protobuf. For a program that reads its configuration once at boot, that is an enormous dependency for one request. The gateway is the same server through a different door, enabled by default.

The one thing that can go wrong — a cluster with the gateway switched off — is named in the error rather than left looking like a wrong address.

## Real usage

```go
etcd.Prefix("app/config/", etcd.WithAddress("http://etcd:2379"))
```

## What it prints

```
etcd.internal:2379

FIELD     KEY       VALUE          SOURCE
Host      HOST      etcd.internal  etcd:app/config
Password  PASSWORD  ••••••         etcd:app/config
Port      PORT      2379           etcd:app/config
```

## A prefix scan is a range to the successor key

etcd has **no prefix parameter**. Scanning `app/config/` means ranging from that key to its successor — increment the last byte — which the adapter computes and unit-tests, including the all-`0xFF` edge case where there is no successor.

Keys and values are base64 in both directions, because they are arbitrary bytes on the wire.

## Not supported, deliberately

First endpoint only, no failover — stated rather than silent. A program that needs client-side failover already has the official client, and `cfgkit.SourceFunc` wraps it in five lines.

## Next

- [`read-consul`](../read-consul) — the same shape without the gRPC problem
- [`read-k8s`](../read-k8s) — where configuration usually lives if you have etcd
