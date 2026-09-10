# Reload

Configuration changes while the process is running. `Watcher[T]` makes that safe without asking you for a mutex.

```go
w, err := cfgkit.NewWatcher[Config](func() []cfgkit.Option {
	return []cfgkit.Option{cfgkit.DefaultSources()}
})
if err != nil {
	log.Fatal(err)          // a process must not start on a config that will not load
}

cfg := w.Current()          // *Config, safe to hold, safe to read from any goroutine
```

## "Hot reload" is three different things

Two of them are permanent non-goals here, and saying which is which is most of the design.

| | |
|---|---|
| **Mutating live config in memory** | rejected permanently — it is what forces a mutex on every reader |
| **Watching a file from inside the library** | rejected permanently — it needs `fsnotify`, and the one-dependency promise is worth more |
| **Building a new config and publishing it atomically** | this page |

## The six dangers, and which ones a library can fix

Any reload design has to answer all six. Three come from *mutation* rather than from reloading, and building a new value instead of editing the old one removes them outright:

| # | Danger | Answered by |
|---|---|---|
| 1 | **Data race** — many readers, one writer | one atomic pointer; there is no writer to race with |
| 2 | **Torn read** — old host with new port | a generation is published whole or not at all |
| 3 | **An invariant breaks** — an edit invalidates a rule that held at boot | validated **before** publication; a failure publishes nothing |
| 4 | The listener cannot change its port | ❌ not a config problem |
| 5 | An open pool keeps its old DSN | ❌ not a config problem |
| 6 | A component captured a copy | ❌ not a config problem — but see the rule below |

**4, 5 and 6 are properties of your running program**, and no configuration library can fix them. Being explicit about that is more useful than pretending otherwise.

## The one rule you have to follow

**A component holds the `Watcher`, never a value read from it.**

```go
// WRONG — captured once at startup, and no reload will ever reach it.
func NewHandler(cfg *Config) *Handler { return &Handler{cfg: cfg} }

// RIGHT — reads the current generation when it needs one.
func NewHandler(w *cfgkit.Watcher[Config]) *Handler { return &Handler{w: w} }

func (h *Handler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	cfg := h.w.Current()      // one generation, held for this request
	...
}
```

That is danger 6, and it is the only one this design hands back to you. Everything else it takes.

## Why the constructor takes a function

This is the part that is easy to get wrong, so the API does not let you:

```go
cfgkit.NewWatcher[Config](func() []cfgkit.Option { ... })   // a function, not a list
```

`FromFiles` **reads and merges its files when it is constructed**. A watcher holding an option list built once would therefore keep serving the file contents from process start: `Reload` would run, report success, bump the generation, and change nothing.

That is worse than having no reload at all, because it looks like it works. Taking a function makes the sources get rebuilt, so the files get re-read. It is pinned by a test — building the sources outside the function fails with *"Reload must re-read the file"*.

## A bad edit cannot take the process down

`Reload` runs the whole pipeline — defaults, sources, bind, derive, validate — **before** it publishes anything. On any error the previous configuration stays in service and the error is returned.

```go
if err := w.Reload(); err != nil {
	log.Printf("config reload failed, keeping generation %d: %v", w.Generation(), err)
	// the process keeps running, correctly, on what it already had
}
```

```
gen 1: first:9000
reload: <nil>
gen 2: second:9001
reload: true          ← EX_PORT=not-a-number
gen 2: second:9001    ← unchanged
```

`Generation()` counts only *published* configurations, so an unchanged number is how you know a reload failed without diffing anything.

## Triggering it

The package **reloads**; it does not **watch**. The trigger is yours, and all but the last of these are stdlib:

**SIGHUP** — the traditional one:

```go
sig := make(chan os.Signal, 1)
signal.Notify(sig, syscall.SIGHUP)
go func() {
	for range sig {
		if err := w.Reload(); err != nil {
			log.Printf("reload failed: %v", err)
		}
	}
}()
```

**A timer** — for configuration that lives in a store rather than a file:

```go
go func() {
	for range time.Tick(30 * time.Second) {
		if err := w.Reload(); err != nil {
			log.Printf("reload failed: %v", err)
		}
	}
}()
```

**An admin endpoint** — explicit, auditable, and it can report the result:

```go
mux.HandleFunc("POST /admin/config/reload", func(rw http.ResponseWriter, r *http.Request) {
	if err := w.Reload(); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	fmt.Fprintf(rw, "reloaded, generation %d\n", w.Generation())
})
```

**`fsnotify`** — if you want a real file watch, it stays *your* dependency rather than becoming everyone's.

## Explaining the current generation

```go
cfg, res := w.Snapshot()
res.Explain(os.Stderr)
```

Use `Snapshot` rather than `Current()` and `Result()` separately whenever you need both. Two separate calls can straddle a reload and describe a value with the wrong origin — the torn read, reintroduced one level up.

## How this compares

| | viper | koanf | `Watcher` |
|---|---|---|---|
| Safe under concurrent reads | ❌ caller adds a mutex | ❌ caller adds a mutex | ✅ atomic pointer |
| Torn read possible | ✅ yes | ✅ yes | ❌ no |
| Validated before publication | ❌ no | ❌ no | ✅ yes |
| A bad edit reaches the process | ✅ yes | ✅ yes | ❌ no — the old config stays |
| A bound struct updates | ❌ re-unmarshal by hand | ⚠️ build a new one | ✅ `Current()` returns it |
| Dependency for the trigger | `fsnotify`, always | `fsnotify` in the provider | none — you choose |

Both incumbents say this in their own documentation. Viper's FAQ:

> "No, you will need to synchronize access to the viper yourself (for example by using the `sync` package). Concurrent reads and writes can cause a panic."

Koanf's:

> "This is not goroutine safe if there are concurrent `*Get()` calls happening on the koanf object while it is doing a `Load()`. Such scenarios will need mutex locking."

The concurrency claim here is not a claim: `TestConcurrentReadsUnderContinuousReload` runs eight unsynchronised readers against a writer doing 200 reloads, under `-race`, in the standard gate.

## Gotchas

**A missing file is not an error, on reload too.** Delete the `.env` and the next reload succeeds with compiled-in defaults. That is consistent with [Sources](sources.md), but it does mean a deleted file is silently a valid configuration — check `Result.Files()` if that matters to you.

**Reloads serialise; reads do not.** Two concurrent `Reload` calls take a lock so only one runs at a time. Readers never touch it.

**`Current()` is stable.** The value you hold does not change under you — a reload publishes a *new* one. That is what makes it safe to read ten fields from it without locking, and it is also why a component that captures one never sees an update.

**There is no callback on reload.** `Reload` returns; do whatever you need after it. A hook would run while holding the reload lock and invite exactly the mutation this design removes.
