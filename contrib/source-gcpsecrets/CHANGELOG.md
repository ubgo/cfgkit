# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

## [0.1.0] - 2026-09-11

First release, pinning `github.com/ubgo/cfgkit` v0.1.0.

### Added

- `Secret(name, key, opts...)` — one Secret Manager secret supplying the value for one configuration key. Compose several; each is one API call and each is silent about every key but its own.
- Workload identity by default: the token and the project both come from the instance metadata server, so nothing needs configuring inside GCP.
- Implements `cfgkit.KeyLister` with the single key it can answer for, so a secret bound to a key no field wants is reported rather than silently doing nothing.
- Options: `WithProject`, `WithVersion`, `WithToken`, `WithClient`, `WithContext`, `WithEndpoints`, `Optional`.

### Notes

- **No dependencies.** Possible here and not for AWS because the asymmetry is in the platforms: GCP hands you a finished bearer token from a well-known URL, while AWS asks for a credential chain plus SigV4 signing.
- **A service-account JSON key file is not supported** — signing a JWT with an RSA key is where this would stop being a small package. Use the official client with `cfgkit.SourceFunc`.
- **A whole `.env` in one secret is deliberately not built in.** The package does not guess at a payload format; the README shows the five-line version.
- The project is resolved from `WithProject`, `$GOOGLE_CLOUD_PROJECT`, then the metadata server. Getting it wrong reads the right name from the wrong project, which is a 404 that looks like a missing secret.
- Secret names are URL-escaped, since they are caller-supplied.
- Reads are bounded: the metadata reply is capped, because that address is link-local and not something the caller controls.
