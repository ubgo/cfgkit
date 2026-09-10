# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

Nothing released yet. The module is complete and covered, and waits on the core's first tag — it pins `github.com/ubgo/cfgkit`, and a published module cannot depend on an unpublished one.

### Added

- `Env(configPath, file, opts...)` — one environment from a kiln-encrypted file as a flat cfgkit source. `configPath` is the `kiln.toml`; `file` is the environment named inside it. Both come from kiln's own model rather than being invented here.
- Implements `cfgkit.KeyLister`, so a variable in the file that matches no field is reported through `Result.Unknown()` as a probable typo. Listed names are the stripped ones when a prefix is set.
- Options: `WithKeyPath`, `WithPrefix`, `WithDecrypter`, `Optional`.

### Notes

- **This is the one source whose file is safe to commit.** Every other secret adapter moves the secrets somewhere else and leaves the repository with half a configuration; kiln keeps the encrypted file next to the code and lets `kiln.toml` decide who can read it. That is a different trade, not a different vendor, which is why it earns a module.
- **It carries a dependency, unlike the six no-SDK sources**, and for the reason the catalogue states: there is no transport here, so the hard part is age decryption and role-based access rather than an HTTP call. A hand-rolled version of either would be a worse copy of something that exists, and cryptography that is subtly wrong does not announce itself.
- **A decryption failure is an error, never a miss.** An identity that is denied access must not be indistinguishable from an unset key, because that difference is a deploy proceeding with an empty password. `Optional()` covers an empty environment only, and never suppresses a failure.
- **`WithPrefix` strips**, where koanf's provider filters and keeps. One encrypted file can then serve several components without every struct repeating the namespace, matching what `FromPrefixedEnviron` already means in the core. It is a rename rather than an alias — `API_PORT` no longer resolves once the prefix is stripped.
- **Read once**, at construction. `Lookup` runs per bound field, and decrypting per key would unlock the identity once per field and leave that many more copies of plaintext in memory. Values are copied to strings before kiln's `cleanup()` wipes the buffers behind them.
- **The `Decrypter` seam keeps most of the suite key-free**, and it is a real seam besides: a caller holding an unlocked identity can avoid a second key discovery. But faking decryption and then asserting the values come back would only assert the fake, so a second test file does the real thing — generates an age identity, writes a `kiln.toml`, encrypts a fixture with kiln itself, and reads it back through `Load`. The access-denial claim is proved the same way, by encrypting for one identity and reading with another, and asserting kiln's own `security error: cannot decrypt` rather than merely a non-nil error. Auto-discovery is covered as well — the path a developer who simply runs the program takes — by redirecting `HOME` to a temp directory and planting a generated key there, so nothing ever reads the identity on the machine running the suite.
