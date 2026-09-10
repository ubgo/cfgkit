# read-commandline — flags, stdlib and pflag

```sh
go run ./read-commandline
go run ./read-commandline --port=7000
```

## The rule that makes flags safe to put last

**A flag counts only when the user actually typed it.**

A flag's own default must never reach the configuration. Declare `--port` with default `8080`, put `PORT=3000` in a `.env` file, type no flag — and a reader that simply asks the flag set for `port` gets `8080` and treats it as a value. The flag default silently beats the file, and because the flag layer sits last, **nothing below it can ever win again** for any field that happens to have a flag.

The standard library offers two walks and only one is correct:

| | |
|---|---|
| `fs.VisitAll` | every declared flag, defaults included — **wrong** |
| `fs.Visit` | only the flags the user typed — **correct** |

This is a long-lived defect in viper ([#671](https://github.com/spf13/viper/issues/671), [#375](https://github.com/spf13/viper/issues/375)). koanf avoids it by asking the config object whether another provider already set the key, which needs a back-reference. cfgkit needs neither mechanism: its defaults already live in Go, so a flag never has to supply one and the rule collapses to *typed flags win, everything else is invisible*.

## What it prints

Nothing typed — the file wins, and the absurd `9999` flag default is nowhere:

```
stdlib flag: from-file:8080 level=info
Port      PORT       8080       map
```

`--port=7000` typed — the flag wins, and provenance names which flag package:

```
stdlib flag: from-file:7000 level=info
Port      PORT       7000       flags

pflag: from-file:7000 level=info
Port      PORT       7000       pflag
```

## Flags are opt-in per field

```go
Port     int    `env:"PORT" default:"8080" flag:"port"`
LogLevel string `env:"LOG_LEVEL" default:"info"`   // no flag, ever
```

Without a `flag:` tag a field is invisible to a flag source, however the flag is spelled. That is deliberate: a configuration with 150 fields must not silently produce a 150-flag command line, so exposing a field on the CLI is a decision you write down.

The tag also holds the flag **name**, which is why it is separate from `env:`. Flags are conventionally lowercase and env keys conventionally are not.

## Ordering, with cobra

A flag set holds nothing until it is parsed, and with cobra that happens when the command **runs**. So `Load` belongs inside `RunE` — never in `init()` or a package-level variable. A `Load` that runs too early sees an empty flag set, silently ignores every flag, and looks exactly like "flags do not work".

## Next

- [`precedence`](../precedence) — where the flag layer sits among the others
- [`read-environment`](../read-environment) — the layer flags usually override
