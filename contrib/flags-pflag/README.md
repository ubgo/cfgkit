# cfgkit/contrib/flags-pflag

**Support level: supported** — used by the authors; the typed-only rule it enforces is the one viper gets wrong. See [the catalogue](../../docs/catalogue.md#support-levels).

cobra and [`pflag`](https://github.com/spf13/pflag) flag sets as a [`cfgkit`](https://github.com/ubgo/cfgkit) source.

```go
import pflagsrc "github.com/ubgo/cfgkit/contrib/flags-pflag"
```

**Support level: supported.** Its dependency is `github.com/spf13/pflag`.

## Use it

```go
var cmd = &cobra.Command{
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _, err := cfgkit.Load[Config](cfgkit.WithSources(
			cfgkit.FromFiles(".env"),
			cfgkit.FromEnviron(),
			pflagsrc.Source(cmd.Flags()),   // highest precedence
		))
		if err != nil {
			return err
		}
		return run(cfg)
	},
}
```

Fields opt in with a `flag:` tag:

```go
type Config struct {
	Port    int    `env:"PORT"     flag:"port" default:"8080"`
	Host    string `env:"HOST"     flag:"host" default:"localhost"`
	DBURL   string `env:"DATABASE_URL"`         // no flag tag — invisible to this source
}
```

A configuration with 150 fields must not produce 150 flags, so only tagged fields are ever read from a flag set.

## The rule this module exists to enforce

> **A flag counts only when the user actually typed it. A flag's own default must never enter the configuration.**

Consider `--port` declared with default `9999`, and `PORT=3000` in a `.env` file. The user types no flag.

A naive reader asks the flag set for `port`, receives `9999`, and treats it as a value. The flag default has now silently beaten the file — and it does that for **every** field that happens to have a flag.

```go
fs.Int("port", 9999, "port")   // declared, never typed
fs.Parse(nil)

cfg, res, _ := cfgkit.Load[Config](cfgkit.WithSources(
	cfgkit.FromMap(map[string]string{"PORT": "3000"}),
	cfgkit.FromFlagSet(fs),     // highest precedence
))
// Port=3000 from=map
```

The flag source had the highest precedence and still did not win, because it had nothing to say.

`pflag` spells "the user typed it" as `Flag.Changed`, and `fs.Visit` walks only those flags. `fs.VisitAll` walks every declared flag, defaults included — which is the wrong one.

This is a long-lived defect in viper: [#671 "Default value of Cobra flag overrides the viper env variable"](https://github.com/spf13/viper/issues/671), [#375 "BindPFlags functionality does not seem to match documentation"](https://github.com/spf13/viper/issues/375), [#1868 "why pflag are overriden by configuration file?"](https://github.com/spf13/viper/discussions/1868). `BindPFlags` registers every flag, so one nobody typed still contributes its default.

Koanf avoids it differently — its `posflag` provider takes the koanf instance and asks whether another provider already set the key. `cfgkit` needs neither mechanism, because its defaults live in Go: a flag never has to supply one, so the rule collapses to *typed flags win, everything else is invisible*.

## Gotchas

### `Load` must run **after** the flags are parsed

A flag set holds nothing until it is parsed, and cobra parses when the command runs.

```go
// WRONG — init() runs before cobra parses anything
var cfg = mustLoad(pflagsrc.Source(cmd.Flags()))

// RIGHT — inside RunE
RunE: func(cmd *cobra.Command, args []string) error {
	cfg, _, err := cfgkit.Load[Config](...)
}
```

A source built too early sees an empty set, silently contributes nothing, and looks exactly like *"flags do not work"*. This is also the second reason the module does not declare flags itself: it cannot control when parsing happens.

### Declare cobra flags with a **zero** default

```go
cmd.Flags().Int("port", 0, "override the HTTP port")     // right
cmd.Flags().Int("port", 8080, "override the HTTP port")  // wrong
```

The real default belongs in `Defaults()` or a `default:` tag. Two defaults for one field is two sources of truth, and the flag's is invisible to `Explain` — so a reader debugging "where did 8080 come from" gets the wrong answer.

The flag default is harmless in the sense that it never binds, but it will mislead whoever reads `--help`.

### This module does not generate flags

Some libraries build the flag set from the config struct — `Server.Host` becoming `--server-host`. That was considered and rejected:

1. **It fights cobra.** Your commands own their flags; two systems declaring the same names means the loser is whichever runs second.
2. **It cannot control parse time**, so the ordering gotcha above becomes unavoidable rather than documented.
3. **It flags everything.** 150 fields become 150 entries in `--help`, most of which nobody will ever type.

Reading an existing flag set costs one tag per field and has none of these problems.

### Flag names, not env keys

This source is keyed by **flag name** (`port`), while every other source is keyed by env key (`PORT`). The `flag:` tag is what bridges them, and it is why a field without one is skipped here rather than falling back to its env key.

## Development

```sh
task test        # includes the flag-default-never-wins test
task ci          # fmt-check + vet + race tests
task test:cover
```
