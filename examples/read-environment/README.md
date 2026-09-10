# read-environment — variables, plain and namespaced

Two ways to read the environment, and the reason the second one exists.

```sh
go run ./read-environment
PORT=9090 WORKER_CONCURRENCY=16 WORKER_QUEUE=billing go run ./read-environment
```

Output is pinned by `example_test.go`.

## The plain form

```go
cfgkit.Load[Config](cfgkit.WithSources(cfgkit.FromEnviron()))
```

`PORT` binds `env:"PORT"`. Nothing surprising.

## The namespaced form, and why it is not the same thing

One process usually hosts several components, and short names collide immediately — a server, a worker and a cache all want `PORT`, `TIMEOUT`, `CONCURRENCY`. The environment solves this with prefixes:

```
WORKER_CONCURRENCY=16
WORKER_QUEUE=billing
```

But the worker's own struct should not have to know it is namespaced:

```go
type WorkerConfig struct {
	Concurrency int    `env:"CONCURRENCY" default:"4"`
	Queue       string `env:"QUEUE" default:"default"`
}

cfgkit.Load[WorkerConfig](cfgkit.WithSources(cfgkit.FromPrefixedEnviron("WORKER_")))
```

`FromPrefixedEnviron` **strips** the prefix before binding. That is the whole point: the same struct now reads from a namespaced environment *and* from a plain `.env` file, without either one dictating the field names.

If the prefix were kept, every component's struct would have to repeat its namespace in every tag, and moving a component between deployments — where the prefix differs — would mean editing the struct.

## What it prints

```
server: port=8080 level=info
FIELD  KEY        VALUE  SOURCE
Level  LOG_LEVEL  info   default
Port   PORT       8080   default

worker: concurrency=4 queue=default
FIELD        KEY          VALUE    SOURCE
Concurrency  CONCURRENCY  4        default
Queue        QUEUE        default  default
```

## The isolation goes both ways

Two tests pin it, because a leak in either direction is a real bug:

- `CONCURRENCY=999` set without the prefix does **not** reach the worker's load.
- `WORKER_PORT=7777` does **not** reach the server's `PORT`.

A prefixed load sees only its own namespace, and an unprefixed load does not accidentally inherit somebody else's variable.

## `FromEnviron` reports no unknown keys — on purpose

Every other source implements `KeyLister`, so a key that binds no field shows up in `Result.Unknown()` as a probable typo. `FromEnviron` does not, and cannot: the machine's environment contains `PATH`, `HOME`, `LANG` and several hundred other things that were never meant for this program. Reporting them would bury the one real typo in noise.

`FromPrefixedEnviron` *does* list keys, because a namespace is a statement of intent — `WORKER_` variables were written for this component.

## Next

- [`read-file`](../read-file) — the same idea from a `.env` file
- [`precedence`](../precedence) — the environment layered over files
- [`plugin`](../plugin) — a prefixed load per component, for a config type the host cannot name
