# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

## [0.1.0] - 2026-09-11

First release, pinning `github.com/ubgo/cfgkit` v0.1.0.

### Added

- `Source(name, doc)` and `File(path)` — YAML as a structured source, matched by `yaml:` tags.
- Passes the shared `cfgkittest` conformance suite: absent fields are left untouched, so a YAML document overlays rather than replaces.

### Notes

- An **absent** file is silent; a file that exists and cannot be read or parsed is an error. A broken configuration must never be indistinguishable from an absent one.
- The YAML parser lives here and only here. A program that never reads YAML never compiles this module and never sees `gopkg.in/yaml.v3` in its `go.sum`.
