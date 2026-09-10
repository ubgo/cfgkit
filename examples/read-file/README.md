# read-file — configuration from a `.env` file

The example to read first. A `.env` file is where almost every program starts, and the rest of this directory is a variation on what happens here.

```sh
go run ./read-file
PORT=9090 go run ./read-file      # the environment wins
```

Output is pinned by `example_test.go`, so it fails the gate rather than rotting.

## The struct is the contract

```go
type Config struct {
	AppName string `env:"APP_NAME" default:"app" doc:"identifies this service in logs"`
	Port    int    `env:"PORT"     default:"8080" doc:"the port the HTTP server binds"`
	BaseURL string `env:"BASE_URL"`

	DatabaseURL      string `env:"DATABASE_URL"`
	DatabasePassword string `env:"DATABASE_PASSWORD" secret:"true"`
}
```

Every key the program reads is declared in one place, with its type and its default. "What can I configure?" is answered by reading a type, not by grepping for `os.Getenv`.

## Two sources, and the order matters

```go
cfgkit.Load[Config](cfgkit.WithSources(
	cfgkit.FromFiles("app.env"),
	cfgkit.FromEnviron(),          // last, so an operator can always override
))
```

Precedence is **positional**: the value of a field is whatever the last source to claim its key said. Nothing is reordered internally, so the order you read in the code is the order that runs.

## What it prints

```
checkout listening on http://localhost:8080

where each value came from:
FIELD             KEY                VALUE                              SOURCE
AppName           APP_NAME           checkout                           file:app.env
BaseURL           BASE_URL           http://localhost:8080              file:app.env
DatabasePassword  DATABASE_PASSWORD  ••••••                             file:app.env
DatabaseURL       DATABASE_URL       postgres://localhost/checkout_dev  file:app.env
Greeting          GREETING           hello, world                       file:app.env
Port              PORT               8080                               file:app.env
```

Three things in that output are worth naming.

**The SOURCE column.** It answers the question layered configuration actually raises in production — not *what is this value* but *which layer decided it*. Run with `PORT=9090` and the row becomes `9090  environ`.

**`DATABASE_PASSWORD` is masked.** `secret:"true"` changes nothing about how the value is read; it changes where it can appear. It is masked in `Explain`, in `Result.JSON()`, and in error messages. `cfgkit.Reveal()` opts out when you genuinely need to print it.

**`BASE_URL` resolved `${PORT}`.** The `.env` parser resolves references, not the shell, so the same file behaves identically under Docker, systemd, and `go run`.

## The reference is resolved once, by the file

Override `PORT=9090` and `BASE_URL` still reads `http://localhost:8080`. That surprises people, so it is pinned by a test rather than left to be discovered.

`${PORT}` was expanded when **the file** was parsed, against the file's own `PORT`. A later source overriding `PORT` does not retroactively rewrite a value that another source already resolved — doing so would mean a source's output depends on sources listed after it, and precedence would stop being positional.

If you want the override to reach `BASE_URL`, build it from the port in Go rather than in the file. That keeps the composition visible in code, where it can be tested.

## Next

- [`read-environment`](../read-environment) — the environment alone, and prefixed variables
- [`default-values`](../default-values) — what happens when no source sets anything
- [`precedence`](../precedence) — several sources, and reading the result
