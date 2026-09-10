# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

Nothing released yet. The module is complete and covered, and waits on the core's first tag — it pins `github.com/ubgo/cfgkit`, and a published module cannot depend on an unpublished one.

### Added

- `Source(name, doc)` and `File(path)` — INI as a **flat** cfgkit source, keyed `section.key`.
- `SectionSeparator`, exported so a caller can build a key name without hardcoding the dot.
- Implements `cfgkit.KeyLister`, so a key matching no field is reported with its section.

### Notes

- **INI is flat, not structured.** YAML and TOML have a type system and a document shape and merge onto a struct; INI has neither, so it uses the same interface `.env` files do.
- **A dot separates section from key**, because an INI key may itself contain underscores and a separator that can appear inside a name makes the mapping ambiguous.
- **Names are used exactly as written** — no case folding. `[Section] Key` and `[section] key` are different keys, both addressable.
- An **absent** file is silent; one that exists and cannot be read or parsed is an error.
