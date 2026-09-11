# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

## [0.1.0] - 2026-09-11

First release, pinning `github.com/ubgo/cfgkit` v0.1.0.

### Added

- `Prefix(prefix, opts...)` — every key under an etcd v3 prefix as a flat cfgkit source, read in one range request.
- `WithCredentials` exchanges a username and password for a token; `WithToken` uses one you already have and skips the extra round trip.
- Implements `cfgkit.KeyLister`, so a key under the prefix matching no field is reported as a probable typo.
- Options: `WithAddress`, `WithCredentials`, `WithToken`, `WithClient`, `WithContext`, `Optional`.

### Notes

- **No dependencies**, via etcd's HTTP/JSON gateway rather than gRPC. The official client brings gRPC, protobuf and their transitive tree; the gateway is the same server through a different door, enabled by default since v3.
- **A prefix scan is a range to the successor key** — the same bytes with the last one incremented — because etcd has no prefix parameter. Unit-tested directly, including the all-`0xFF` case that has no successor.
- **An empty value omits the field** in etcd's encoding. The key still exists, so it binds as empty rather than absent.
- **An empty prefix is an error** unless `Optional()`: etcd answers 200 with no keys rather than 404, so an empty result is the only signal.
- **Only the first endpoint is used.** One request, no failover — silently trying the rest would hide a broken node.
- A disabled gateway is named as a likely cause, because its absence looks exactly like a wrong address.
