# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

## [0.1.0] - 2026-09-11

First release, pinning `github.com/ubgo/cfgkit` v0.1.0.

### Added

- `JSON(name, opts...)` — a secret whose payload is a JSON object, each member a configuration key. This is what the console produces for key/value pairs.
- `Whole(name, key, opts...)` — a plain string secret supplying one key.
- Options: `WithClient`, `WithContext`, `WithVersionID`, `WithVersionStage`, `Optional`.

### Notes

- **The two readings are an argument, not a guess.** Sniffing at the payload would be right nine times and silent the tenth, so `JSON` and `Whole` are separate calls and `JSON` on a plain string names `Whole` in its error.
- **The payload never enters an error message** — the decoder's error is dropped with it, because an `UnmarshalTypeError` quotes the value it choked on and a secret is a thing that gets logged.
- **Binary secrets are refused by name.** A certificate or keystore is not configuration, and coercing it to a string would bind mojibake.
- **JSON values are rendered deliberately**: a number must not become `9000.000000`.
- `Optional()` means *absent is fine*, not *errors are fine* — an access-denied still fails the load.
- Not-found is matched on the SDK's error **type**, not message text.
