# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

Nothing released yet. The module is complete and covered, and waits on the core's first tag — it pins `github.com/ubgo/cfgkit`, and a published module cannot depend on an unpublished one.

### Added

- `Prefix(prefix, opts...)` — every key under a Consul KV prefix, read recursively, as a flat cfgkit source.
- Keys arrive **relative to the prefix**, so the same struct binds from Consul and from a `.env` file with no second set of tags.
- Address and token default to `$CONSUL_HTTP_ADDR` and `$CONSUL_HTTP_TOKEN`, the variables the Consul CLI reads.
- Implements `cfgkit.KeyLister`, so a key under the prefix matching no field is reported as a probable typo.
- Options: `WithAddress`, `WithToken`, `WithDatacenter`, `WithClient`, `WithContext`, `Optional`.

### Notes

- **No dependencies.** Reading a prefix is a single GET with one header.
- **A bare `host:port` address is accepted**, because that is how `CONSUL_HTTP_ADDR` is conventionally written. Without it the value parses as a URL whose scheme is the hostname, and the failure names nothing a reader would recognise.
- **Consul's folder keys are skipped** — a nested tree reports its own prefix as a valueless key, and binding it would offer an empty value under an empty name.
- **A valueless key is present and empty, not absent.** A present empty value beats a default; an absent one does not.
- **An empty prefix is an error** unless `Optional()`: Consul answers 404 either way, so from the outside empty and missing are the same thing, and both are a deployment mistake when you named the prefix.
- **Read once**, recursively, at construction.
