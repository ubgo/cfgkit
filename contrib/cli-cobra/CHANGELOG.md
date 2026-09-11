# Changelog

Kept in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form, following [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This module versions **independently of the core**, which is the point of it being a separate module: a fix here does not force a release of `cfgkit`, and a release of `cfgkit` does not force one here.

## [Unreleased]

## [0.1.0] - 2026-09-11

First release, pinning `github.com/ubgo/cfgkit` v0.1.0.

### Added

- `Command[T](load, opts...)` — a cobra `config` command with `check`, `explain` and `document` subcommands, bound to the caller's own configuration type.
- `explain` reports the **resolved mode**, the **files consulted**, and any **keys that matched no field** — the last being the failure that is otherwise invisible, since a typo binds nothing and the field simply keeps its default.
- `check --strict` fails on those unmatched keys. Advisory by default, because one `.env` legitimately serves several audiences.
- `explain --json` emits a typed envelope: cfgkit's own provenance record embedded verbatim as `json.RawMessage` under `provenance`, plus `unknown` and `files`, which live on `Result` and are not in its JSON. Embedding rather than re-declaring means a field added upstream appears here without a change, and this package can never misreport one. A `map[string]any` round-trip was rejected: it re-parses JSON this program just produced and adds two error branches that cannot fire.
- Options: `WithUse` (rename the command), `WithOut` (redirect output).

### Notes

- **These verbs cannot ship as a binary.** `Check`, `Explain` and `Document` are generic over the caller's struct, so the code calling them must be compiled against that type. Every cfgkit user therefore writes the same fifty-line main; this module is that main, written once.
- **`load` is a required positional parameter, not an option.** cfgkit has no default source chain — by design — so a command built without sources would read nothing and print `configuration is valid`. A gate that passes without looking is worse than no gate, so the caller is made to name the sources. `nil` means "defaults only" and is then an explicit choice; a test pins the difference.
- **Errors are returned, never printed and swallowed**, so the host keeps its exit code and the command works as a CI gate. Subcommands set `SilenceUsage`: a bad configuration key is not a bad invocation, and the flag list buries the problem.
- **It opens nothing.** No database, no cache, no network. That is the whole point — a command for diagnosing a broken configuration must run on a machine where the application cannot start. It mounts happily on a CLI whose other commands do need infrastructure.
- **Masking is tested, not assumed**, including that `document` emits neither the live secret nor the compiled-in placeholder, since the generated contract is committed. Options pass-through is proved with `Reveal`, whose effect is unambiguous.
