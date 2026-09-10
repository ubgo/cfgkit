# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

Nothing released yet. The module is complete and covered, and waits on the core's first tag — it pins `github.com/ubgo/cfgkit`, and a published module cannot depend on an unpublished one.

### Added

- `Secret(path, opts...)` — a Vault KV secret as a flat cfgkit source, for both the KV v2 and KV v1 engines.
- Token discovery follows the Vault CLI: `WithToken`, then `$VAULT_TOKEN`, then `~/.vault-token`. An operator who can run `vault kv get` needs no new configuration.
- Implements `cfgkit.KeyLister`, so a key in the secret matching no field is reported as a probable typo.
- Options: `WithAddress`, `WithToken`, `WithNamespace`, `WithMount`, `WithKVVersion`, `WithClient`, `WithHomeDir`, `WithContext`, `Optional`.

### Notes

- **No dependencies.** Reading one secret is a single GET with one header; the official SDK brings a release cadence of its own for that.
- **The KV version is explicit.** Vault will not say which engine a mount is without a second call, and guessing wrong produces a 404 that looks like a missing secret — so both error paths name the version as a likely cause.
- **JSON values are rendered, not assumed.** A number arrives as a `float64` and must not become `9000.000000`; an object is re-encoded as JSON so a field with an `UnmarshalText` can still take it; `null` becomes the empty string.
- **A missing secret is an error**, unlike `FromFiles`. You named the path, so its absence is a deployment mistake; `Optional()` opts out.
- **Read once**, at construction — every read is also an audit log entry.
- 403, 503 and a missing token each produce a message naming the fix rather than the status code.
