# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

## [0.1.0] - 2026-09-11

First release, pinning `github.com/ubgo/cfgkit` v0.1.0.

### Added

- `Source(filename, doc)` and `File(path)` — HCL as a structured source, matched by `hcl:` tags.
- Blocks bind to nested structs, which is what makes HCL worth a structured adapter rather than a flat one.

### Notes

- **Both tag sets are needed**: `hcl:` for the decoder, `env:` for the flat sources it composes with. `gohcl` is stricter than `encoding/json` and will not guess a field name.
- That strictness cuts the other way too: a typo'd argument is a **decode error**, not silently ignored.
- **Diagnostics keep their position and stage** — `config.hcl:2,1-5` with `parsing HCL` or `decoding HCL` — because the two fail for different reasons and have different fixes.
- `filename` labels diagnostics only; it is never read from disk.
- An **absent** file is silent; one that exists and cannot be read or parsed is an error.
