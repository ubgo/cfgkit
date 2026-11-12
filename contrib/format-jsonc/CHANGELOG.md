# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**.

## [Unreleased]

### Added

- `File(path, opts...)` and `Document(name, doc, opts...)`: JSONC as a structured source, parsed by `github.com/ubgo/textdata/jsonc`, matched by `json:` tags. textdata/jsonc options pass through (`WithExpand`, `WithLookup`, `WithPlugin`, `Strict`).
- Per-value provenance: `Origins`, `LocateField`, `Locate` and `Locations` report the `file:line` each value was written at.
- Syntax errors, wrong-type values and expansion errors report `file:line:col`.
- Passes the shared `cfgkittest` conformance suite; a missing file is silent, a broken one is an error.
