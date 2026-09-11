# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

## [0.1.0] - 2026-09-11

First release, pinning `github.com/ubgo/cfgkit` v0.1.0.

### Added

- `Configuration(app, env, profile, decode, opts...)` — one AppConfig profile, decoded by a function you supply.
- `JSON(app, env, profile, opts...)` — the same with `encoding/json`.
- Options: `WithClient`, `WithContext`.

### Notes

- **AppConfig is a two-call protocol**: `StartConfigurationSession` returns a token, `GetLatestConfiguration` exchanges it for the content. The failure message says which of the two failed, because they fail for different reasons.
- **Empty content on the FIRST call means no deployed content**, not "unchanged". Empty means unchanged only on a *later* poll, and treating the first that way would bind an empty document and look like a working read.
- **No session is kept.** cfgkit reads at boot; polling is `cfgkit.Watcher`'s job, and each `Reload` starts a fresh session rather than carrying a token — which is what keeps the emptiness rule from mattering.
- Format-agnostic: AppConfig stores freeform documents and reports a content type without parsing, so the reading is the caller's.
