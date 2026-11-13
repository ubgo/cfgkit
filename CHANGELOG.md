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

## [0.1.3] - 2026-11-13

### Changed

- Requires `github.com/ubgo/textdata/dotenv` v0.1.4 (and its base v0.1.4), the first public textdata release; its v0.1.1-v0.1.3 tags are retracted. `FromFiles` behaves the same.

## [0.1.2] - 2026-11-13

### Changed

- Requires `github.com/ubgo/textdata/dotenv` v0.1.2 (and its base v0.1.2). `FromFiles` behaves the same.

## [0.1.1] - 2026-11-13

### Changed

- `.env` files are parsed by `github.com/ubgo/textdata/dotenv` v0.1.1, the new home of `github.com/ubgo/dotenv` (now archived). Behaviour is unchanged; the core still has one dependency, plus that module's stdlib-only base `github.com/ubgo/textdata`.

### Added

- `contrib/format-jsonc` (its own module, first release v0.1.1) is listed in the contrib catalogue.

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
[0.1.2]: https://github.com/ubgo/cfgkit/releases/tag/v0.1.2
[0.1.1]: https://github.com/ubgo/cfgkit/releases/tag/v0.1.1
