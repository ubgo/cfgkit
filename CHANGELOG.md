# Changelog

All notable changes to **cfgkit** are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
### Changed
### Deprecated
### Removed
### Fixed
### Security

## [0.1.0] - 2026-09-11

First release.

### Added

- `Load[T]` — layered sources bound into a typed struct, with defaults compiled into the binary so a valid configuration exists with **zero input**.
- Provenance: `Result.Explain` reports which source set every field, and `Result.JSON` the same record as JSON. Secrets are absent from both unless `Reveal()` is passed.
- `Check[T]` validates without constructing the application, reporting every problem at once via `errors.Join` rather than the first.
- `Document[T]` generates the `.env.example` contract from the struct, so it cannot drift from the code.
- `Watcher[T]` publishes an immutable snapshot behind an atomic pointer and validates before publishing, so a bad edit leaves the previous configuration in service — and needs no lock in the caller.
- Modes (`dev` / `stag` / `prod`) selecting which validation rules apply, `Deriver` and `Validator` seams, `secret`, `unset`, `file`, `was` and `flag` tags, and the `cfgkittest` conformance harness for adapter authors.
- 19 contrib modules, each its own Go module: five document formats, pflag, a mountable cobra `config` command, and eleven secret and configuration backends — six of which carry no dependencies at all.

<!--
Release process:
  1. Move the relevant [Unreleased] entries under a new version heading below.
  2. Date it: ## [1.2.0] - YYYY-MM-DD
  3. Tag the release (e.g. v1.2.0) and update the link refs at the bottom.
-->

[Unreleased]: https://github.com/ubgo/cfgkit/commits/main
