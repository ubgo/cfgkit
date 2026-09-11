# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

## [0.1.0] - 2026-09-11

First release, pinning `github.com/ubgo/cfgkit` v0.1.0.

### Added

- `Secret(name, key, opts...)` — one Key Vault secret supplying the value for one configuration key. Compose several; each is one API call and each is silent about every key but its own.
- Managed identity by default, system-assigned or user-assigned via `WithClientID`.
- Implements `cfgkit.KeyLister` with the single key it can answer for.
- Options: `WithVault`, `WithVersion`, `WithToken`, `WithClientID`, `WithClient`, `WithContext`, `WithIMDSEndpoint`, `Optional`.

### Notes

- **No dependencies.** One HTTPS GET for the secret, one for the token.
- **Managed identity or an explicit token only.** Service principals, certificates, device code and workload identity federation each need their own flow; use `azidentity` and pass the token.
- **A bare vault name is expanded** to `https://<name>.vault.azure.net`, since that is how vaults are named in Azure's tooling. A full URL passes through, which is what sovereign clouds need.
- **Both API versions are pinned** — the vault's and the metadata service's. An unpinned version means a future default could change the reply shape under a running fleet.
- The `403` message names **both** permission models, because Key Vault has two and a reader who knows one will look in the wrong place. The `401` message names the audience, because a token for the wrong one is syntactically valid and fails as if it were a permissions problem.
- A version is a **path segment**, not a query parameter.
