# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

Nothing released yet. The module is complete and covered, and waits on the core's first tag — it pins `github.com/ubgo/cfgkit`, and a published module cannot depend on an unpublished one.

### Added

- `Object(bucket, key, decode, opts...)` — an S3 object decoded by a function you supply.
- `JSON(bucket, key, opts...)` — the same with `encoding/json`.
- Options: `WithClient`, `WithContext`, `WithVersionID`, `Optional`.

### Notes

- **Format-agnostic by design.** An S3 object is bytes; what they mean is the caller's decision, so YAML, TOML and HCL compose through the adapters that already parse them without this module depending on any of them.
- **Reads are capped at 8 MiB**, and the limit is checked rather than silently truncating: a half-read configuration document parses to *something*, and something is worse than an error. The size is decided by whoever can write the bucket.
- **Both not-found shapes are recognised** — `NoSuchKey` when the caller may list the bucket, a bare `NotFound` when it may not. A role with `GetObject` but no `ListBucket` sees the second for the same missing object.
- `Optional()` means *absent is fine*, not *errors are fine*.
