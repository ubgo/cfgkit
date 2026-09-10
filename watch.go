package cfgkit

import (
	"sync"
	"sync/atomic"
)

// Reloading is split into three questions, and only one of them is this
// package's to answer.
//
// MUTATING live configuration in memory is rejected permanently: it is what
// makes viper and koanf hand their users a mutex. WATCHING a file is rejected
// permanently too, for a different reason — it needs fsnotify, and a
// configuration library that drags in a file-watching dependency has broken the
// promise that makes it safe to depend on. What remains, and what Watcher does,
// is BUILD A NEW CONFIGURATION AND PUBLISH IT ATOMICALLY.
//
// Three of the six dangers in a reload design come from mutation rather than
// from reloading, and all three disappear here:
//
//	data race    many readers, one writer  -> one atomic pointer, no writer
//	torn read    old host, new port         -> a generation is complete or absent
//	broken rule  an edit invalidates it     -> validated BEFORE it is published
//
// The other three — a listener that cannot change its port, a pool that cannot
// change its DSN, a component holding a copy — are properties of the running
// program, not of a config library, and no library can fix them. The rule they
// imply is stated on Current: a component holds the WATCHER, never a value read
// from it.

// Watcher holds the current configuration behind an atomic pointer, so any
// number of goroutines may read it while a reload builds the next one.
//
// It is a thin layer over Load and adds no way to configure that Load does not
// have. What it adds is a safe moment to swap.
type Watcher[T any] struct {
	// build produces a FRESH option list for every load, and taking a function
	// rather than a list is the whole correctness of this type.
	//
	// FromFiles reads and merges its files at CONSTRUCTION. A Watcher that held
	// options built once would therefore keep serving the file contents from
	// process start: Reload would run, report success, increment the
	// generation, and change nothing. That is worse than having no reload at
	// all, because it looks like it works. A function cannot be got wrong in
	// that way — the sources are rebuilt, so the files are re-read.
	build func() []Option

	// current is the published generation. Readers never take a lock.
	current atomic.Pointer[generation[T]]

	// reloading serialises reloads only. Two concurrent reloads would both do
	// the work and one would be thrown away, and the generation counter would
	// stop being meaningful.
	reloading sync.Mutex
}

// generation is one complete configuration, published as a unit.
//
// The config and its provenance travel together because they are answers about
// the same load. Publishing them as two pointers would reintroduce the torn
// read this type exists to prevent, one level up: a caller could hold a config
// from generation 4 and an Explain from generation 5.
type generation[T any] struct {
	cfg *T
	res *Result
	n   uint64
}

// NewWatcher loads the configuration and returns a Watcher holding it.
//
// build is called once now and again on every Reload. It must return a fresh
// option list each time — construct the sources inside it:
//
//	w, err := cfgkit.NewWatcher(func() []cfgkit.Option {
//		return []cfgkit.Option{cfgkit.DefaultSources()}
//	})
//
// An error from the FIRST load is returned and no Watcher is produced: a
// process must not start on a configuration that does not load. Errors from
// later reloads are different — see Reload.
func NewWatcher[T any](build func() []Option) (*Watcher[T], error) {
	if build == nil {
		return nil, errNilBuild
	}
	w := &Watcher[T]{build: build}

	cfg, res, err := Load[T](build()...)
	if err != nil {
		return nil, err
	}
	w.current.Store(&generation[T]{cfg: cfg, res: res, n: 1})
	return w, nil
}

// Current returns the newest complete configuration.
//
// The returned value is effectively immutable: a later Reload publishes a NEW
// one and never edits this. A caller may therefore hold it for the length of a
// request, read ten fields from it, and be certain all ten came from the same
// generation — with no lock, and with no chance of a torn read.
//
// THE RULE THAT MATTERS, and the one no library can enforce: a long-lived
// component must hold the WATCHER and call Current when it needs a value. A
// component handed *T at startup has taken a copy, and no reload will ever
// reach it. That is danger 6, and it is a property of the program rather than
// of this package.
//
//	// wrong: captured once, never updated
//	func NewHandler(cfg *Config) *Handler { return &Handler{cfg: cfg} }
//
//	// right: reads the current generation per request
//	func NewHandler(w *cfgkit.Watcher[Config]) *Handler { return &Handler{w: w} }
func (w *Watcher[T]) Current() *T { return w.current.Load().cfg }

// Result returns the provenance of the configuration Current would return.
func (w *Watcher[T]) Result() *Result { return w.current.Load().res }

// Snapshot returns the configuration and its provenance from the SAME
// generation.
//
// Use it wherever both are needed — an admin endpoint that prints a value and
// explains where it came from. Calling Current and Result separately can
// straddle a reload and describe a value with the wrong origin, which is the
// torn read this type exists to prevent, reintroduced by the caller.
func (w *Watcher[T]) Snapshot() (*T, *Result) {
	g := w.current.Load()
	return g.cfg, g.res
}

// Generation reports how many configurations have been published, starting at 1
// for the one loaded by NewWatcher.
//
// It is the answer to "did my reload actually take effect": a caller that sees
// the same number after a Reload knows the reload failed, without having to
// diff the configuration.
func (w *Watcher[T]) Generation() uint64 { return w.current.Load().n }

// Reload re-reads every source and builds a NEW configuration, running the full
// pipeline — defaults, sources, bind, derive, validate — BEFORE publishing it.
//
// ON ANY ERROR THE PREVIOUS CONFIGURATION STAYS IN SERVICE and the error is
// returned. A bad edit to a .env file therefore cannot take the process down,
// which is the difference between this and a watcher that assigns first and
// discovers the problem later. It also means a caller can log the error and
// keep running, rather than having to decide what to do with a half-applied
// configuration.
//
// The package RELOADS; it does not WATCH. The trigger belongs to the caller:
// SIGHUP, a timer, an admin route, or fsnotify if they want a file watch. All
// but the last are stdlib, and the last stays the caller's dependency rather
// than becoming everyone's.
func (w *Watcher[T]) Reload() error {
	// Only one reload at a time. Readers are unaffected: they never take this.
	w.reloading.Lock()
	defer w.reloading.Unlock()

	cfg, res, err := Load[T](w.build()...)
	if err != nil {
		// Nothing is published. The previous generation is still current, and
		// its generation number is unchanged, so a caller can tell.
		return err
	}

	// The swap is the only moment anything changes, and it is atomic: a reader
	// sees the whole previous generation or the whole new one, never a mix.
	prev := w.current.Load()
	w.current.Store(&generation[T]{cfg: cfg, res: res, n: prev.n + 1})
	return nil
}
