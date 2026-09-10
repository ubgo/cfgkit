# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

Nothing released yet. The module is complete and covered, and waits on the core's first tag — it pins `github.com/ubgo/cfgkit`, and a published module cannot depend on an unpublished one.

### Added

- `Source(*pflag.FlagSet)` — cobra and pflag flags as a cfgkit source.
- Only flags the user actually **typed** are offered, via pflag's `Visit`. A flag's own default never reaches the config, so it cannot silently beat a `.env` file or an environment variable — the failure viper has open as [#671](https://github.com/spf13/viper/issues/671).

### Notes

- Fields opt in with the `flag:` tag; a field without one is never bound from a flag set.
- Build the source inside `RunE`, after cobra has parsed. Constructing it earlier offers an empty flag set.
