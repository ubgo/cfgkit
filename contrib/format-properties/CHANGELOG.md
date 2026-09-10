# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

Nothing released yet. The module is complete and covered, and waits on the core's first tag — it pins `github.com/ubgo/cfgkit`, and a published module cannot depend on an unpublished one.

### Added

- `Source(name, doc)` and `File(path)` — Java-style `.properties` as a **flat** cfgkit source.
- Implements `cfgkit.KeyLister`, so a key matching no field is reported as a probable typo.

### Notes

- **The case this serves** is a Spring Boot service and a Go service reading the same `application.properties`, so keys are used exactly as written — no case folding, no translation.
- **Java's escaping is honoured**: continuation lines, `\u` escapes, `:` as a separator, whitespace trimmed around it. A naive split on `=` mangles all four.
- **`${...}` expansion is disabled**, deliberately. cfgkit resolves references at the `.env` layer where a whole chain of files is visible, and two expansion passes with different rules depending on the source is worse than one documented rule.
- An **absent** file is silent; one that exists and cannot be read or parsed is an error — an invalid `\u` escape included.
