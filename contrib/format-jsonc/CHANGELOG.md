# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**.

## [Unreleased]

## [0.1.2] - 2026-11-13

### Fixed

- Tests now cover how `LocateField` applies the `json:` tag rules (`-`, untagged names, embedded non-structs, named and pointer embedded structs), `Locate` on missing paths and on JSON Pointer digit keys under an object, a broken document, and array-element field paths. Statement coverage went from 92.4% to 98.1%. No behaviour change.

## [0.1.1] - 2026-11-13

First release, pinning `github.com/ubgo/cfgkit` v0.1.1 and `github.com/ubgo/textdata/jsonc` v0.1.2.

### Added

- `File(path, opts...)` and `Document(name, doc, opts...)`: JSONC as a structured source, parsed by `github.com/ubgo/textdata/jsonc`, matched by `json:` tags. textdata/jsonc options pass through (`WithExpand`, `WithLookup`, `WithPlugin`, `Strict`).
- Per-value provenance: `Origins`, `LocateField`, `Locate` and `Locations` report the `file:line` each value was written at.
- Syntax errors, wrong-type values and expansion errors report `file:line:col`.
- Passes the shared `cfgkittest` conformance suite; a missing file is silent, a broken one is an error.
