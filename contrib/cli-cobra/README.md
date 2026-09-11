# cfgkit/contrib/cli-cobra

**Support level: supported** — 97.1% covered, and the covering includes every verb, the masking guarantee, the error contract, the unknown-key reporting and the no-sources footgun. See [the catalogue](../../docs/catalogue.md#support-levels).

Mount cfgkit's **check**, **explain** and **document** verbs on an existing cobra CLI.

```go
import cobracfg "github.com/ubgo/cfgkit/contrib/cli-cobra"

root.AddCommand(cobracfg.Command[Config](loadOptions))
```

All output on this page is captured from a real binary built against this module — a four-field config, a `.env` holding `PORT=9999`, `DB_HOST=db.internal` and a deliberate typo `DB_HSOT=typo.internal`.

```
$ app config check
configuration is valid

$ app config explain
FIELD      KEY         VALUE        SOURCE
DBHost     DB_HOST     db.internal  file:.env
JWTSecret  JWT_SECRET  ••••••       default
Port       PORT        9999         file:.env
Timeout    TIMEOUT     15s          default

mode: dev

1 key(s) matched no field (probable typos):
  DB_HSOT (from file:.env) matched no field

$ PORT=7777 app config explain
Port       PORT        7777         environ

$ app config document
# Postgres host
# optional
DB_HOST=localhost

# token signing key
# optional · secret — do not commit a real value
JWT_SECRET=
...
```

Note what `document` does with the secret: the key is present, the value is empty, and the comment says why. That is what makes the generated file safe to commit.

## Why this module exists

Every cfgkit user needs these three verbs, and every one of them has to write the same fifty-line `main` to get them. They cannot be shipped as a prebuilt binary: `Check`, `Explain` and `Document` are generic over *your* config struct, so the code that calls them must be compiled against your type. This package is that fifty lines, written once.

It asks nothing of the host application. The only import a caller needs is cobra, and the command opens no database, no cache and no network connection — the verbs it exposes exist precisely to run when those are absent or misconfigured. Mount it on a CLI whose other commands need a database, and it still works on a machine where the application itself could not start.

## The sources parameter is required, and that is the point

`load` is the first positional parameter rather than an option:

```go
func Command[T any](load []cfgkit.Option, opts ...Option) *cobra.Command
```

cfgkit has **no default source chain** — deliberately, because a convenience that hides precedence is the magic it exists to avoid. So a command built without sources would read nothing and cheerfully print `configuration is valid`. A gate that passes without looking is worse than no gate, so the caller is made to name the sources rather than being able to forget them.

Pass `nil` to mean "compiled-in defaults only". That is legal, and it is then an explicit choice rather than an omission — a test pins the difference.

**Pass the same options your application loads with.** A check against a different source list is a check of something the application never runs:

```go
// one definition, used by both the app and the command
func Options() []cfgkit.Option {
	return []cfgkit.Option{
		cfgkit.WithModeKey("APP_ENV"),
		cfgkit.WithSources(cfgkit.FromFiles(".env"), cfgkit.FromEnviron()),
	}
}

cfg, _, err := cfgkit.Load[Config](Options()...)   // the application
root.AddCommand(cobracfg.Command[Config](Options())) // the command
```

## The verbs

| Verb | Does | Exit code |
|---|---|---|
| `check` | Validates without constructing the application. Reports **every** problem at once, because a misconfigured deploy should be one fix, not one fix per restart. `--strict` also fails on keys that matched no field. | non-zero on any problem |
| `explain` | Prints every field with the source that set it, then the resolved mode, the files consulted, and any keys that matched no field. `--json` emits the same record as JSON. | non-zero if the load fails |
| `document` | Prints the `.env.example` contract generated from the struct. Secrets are emitted empty, so the output is safe to commit. | non-zero if the load fails |

## Keys that matched no field

A typo binds nothing, so the field keeps its default and the table looks entirely correct — the mistake is invisible unless it is named:

```
$ app config explain
FIELD      KEY         VALUE        SOURCE
DBHost     DB_HOST     db.internal  file:.env
JWTSecret  JWT_SECRET  ••••••       default
Port       PORT        9999         file:.env
Timeout    TIMEOUT     15s          default

mode: dev

1 key(s) matched no field (probable typos):
  DB_HSOT (from file:.env) matched no field
```

Only sources that can honestly enumerate their keys contribute. `FromEnviron` never does: its key set is the whole machine, and a report listing `PATH` and `HOME` would bury the one line that matters.

**It is advisory by default, and that is deliberate.** One `.env` legitimately serves several audiences — a deployment file carrying `GITHUB_SECRET_*` keys for a pipeline alongside the application's own settings is a pattern, not a mistake — so failing on an unrecognised key would break it. Use `--strict` on a file this application alone owns, where an unrecognised key really is a typo:

```
$ app config check --strict
1 key(s) matched no field:
  DB_HSOT (from file:.env) matched no field
$ echo $?
1
```

## Options

| Option | Effect |
|---|---|
| `WithUse(name)` | Renames the command, for a host whose CLI already has a `config` verb. |
| `WithOut(w)` | Redirects output. The default follows the host's own redirection via `cmd.OutOrStdout()`. |

## The error contract

Errors are **returned**, never printed and swallowed. The host keeps its own error handling and decides the exit code — which is what makes the command usable as a CI gate rather than something you have to parse stdout to interpret.

Subcommands set `SilenceUsage`, because a bad configuration key is not a bad invocation: dumping the flag list underneath a list of bad keys buries the thing the operator needs to read.

**If your `main` prints the error itself, set `SilenceErrors` on your root command.** Cobra prints a returned error too, so a root that also prints after `Execute` shows every failure twice — which on a multi-line report means the same list of bad keys scrolling past a second time:

```
Error: 1 key(s) matched no field:
  DB_HSOT (from file:.env) matched no field
1 key(s) matched no field:
  DB_HSOT (from file:.env) matched no field
```

This package cannot decide that for you: silencing errors here would swallow them for a host that expects cobra to do the printing. Pick one printer and say so on the root:

```go
root := &cobra.Command{Use: "app", SilenceErrors: true}
```

Stray positional arguments are rejected (`cobra.NoArgs`). Silently ignoring them would let `config check prod` look like it had checked prod.

## Secrets

A field tagged `secret:"true"` is masked in `explain` and in `explain --json`, and emitted empty by `document`. The field is still *listed*, so you can see whether it was set and which layer set it without seeing what it is. That is what makes the output safe to paste into an issue, and it is pinned by tests — including one asserting that `document` emits neither the live value nor the compiled-in placeholder, since the generated file is meant to be committed.

`cfgkit.Reveal()` in the load options lifts the masking, deliberately and explicitly.

## Testing

The suite covers all three verbs, both output modes, the masking guarantee, the renamed command, the writer fallback, argument rejection, the nil-load path, unknown-key reporting and `--strict`. Options pass-through is proved with `Reveal` rather than asserted: a secret's value can only appear if cfgkit actually received the option.

Every write-error return is covered by walking the failure point across **every** write a full `explain` makes — the write count is measured first rather than guessed, because `Explain`'s tabwriter emits many writes of its own and a hardcoded bound would stop inside the table and never reach the footer.

The gates were verified by breaking them: with the footer disabled the unknown-key and mode tests fail, and with `--strict` short-circuited its test fails. A gate never seen to fail is an unverified claim.

The two remaining uncovered statements are `json.Marshal` error returns in `explain --json` — one on cfgkit's own record, one on an envelope this package just built from it. Both are marshalable by construction. Checked to be unreachable, not assumed.
