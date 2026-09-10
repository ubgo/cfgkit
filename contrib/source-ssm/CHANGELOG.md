# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

Nothing released yet. The module is complete and covered, and waits on the core's first tag — it pins `github.com/ubgo/cfgkit`, and a published module cannot depend on an unpublished one.

### Added

- `Path(path, opts...)` — every parameter under a Parameter Store path as a flat cfgkit source, names arriving relative to the path.
- Options: `WithClient`, `WithContext`, `WithoutDecryption`, `WithoutRecursion`, `Optional`.
- Implements `cfgkit.KeyLister`.

### Notes

- **Takes `aws-sdk-go-v2` as a real dependency**, unlike the six no-SDK remote sources. AWS credential resolution — environment, shared config, IMDS, IRSA, SSO, assume-role — is the part worth delegating; a worse reimplementation authenticates on a laptop and not in production.
- **Paging is not optional.** `GetParametersByPath` returns at most ten parameters per call, so a configuration of eleven silently loses one without the loop. It is the easiest mistake to make against this API and only shows up as a config grows.
- **Decryption is on by default.** A SecureString arriving as ciphertext is not a configuration value, and binding the encrypted blob is the quiet failure this package exists to prevent.
- A **StringList** needs no special handling: it arrives comma-separated, which is exactly what cfgkit's slice decoder expects.
- `API` is an interface, so the suite needs no AWS, no credentials and no network.
