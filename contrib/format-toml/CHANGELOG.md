# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

## [0.1.0] - 2026-09-11

First release, pinning `github.com/ubgo/cfgkit` v0.1.0.

### Added

- `Source(name, doc)` and `File(path)` — TOML as a structured source, matched by `toml:` tags.
- Passes the shared `cfgkittest` conformance suite: absent fields are left untouched, so a document overlays rather than replaces.

### Notes

- TOML **tables** bind to nested structs, which is why the format is worth an adapter rather than a note in the docs: it is shaped like a configuration tree already.
- Native integers, booleans, floats and arrays arrive without a string round trip. `time.Duration` still comes from a string, because TOML has no duration type.
- An **absent** file is silent; a file that exists and cannot be read or parsed is an error. A syntax error fails the load rather than binding a partial document.
- The parser lives here and only here. A program that never reads TOML never sees `BurntSushi/toml` in its `go.sum`.
