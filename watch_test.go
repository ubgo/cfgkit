// Tests for CONFIG_SPEC §2 — Watcher, the accepted third of "hot reload".
//
// The claim being pinned is specific, because it is the one viper and koanf do
// not make: a reload is safe under concurrent reads with no lock in the caller,
// a bad edit cannot reach the running process, and a reader can never see a
// half-applied configuration.
package cfgkit_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ubgo/cfgkit"
)

type watchCfg struct {
	Host    string `env:"W_HOST" default:"localhost"`
	Port    int    `env:"W_PORT" default:"8080"`
	Timeout int    `env:"W_TIMEOUT" default:"30"`
}

// Validate is the rule a bad edit has to break for the "old config stays"
// property to be worth anything.
func (c *watchCfg) Validate() error {
	return cfgkit.Range("Port", c.Port, 1, 65535)
}

// writeEnv replaces the file's whole contents, the way an operator editing a
// deployment's .env would.
func writeEnv(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// newFileWatcher is the shape every test here uses: sources are constructed
// INSIDE the build function, which is what makes a reload see new file content.
func newFileWatcher(t *testing.T, path string) *cfgkit.Watcher[watchCfg] {
	t.Helper()
	w, err := cfgkit.NewWatcher[watchCfg](func() []cfgkit.Option {
		return []cfgkit.Option{cfgkit.WithSources(cfgkit.FromFiles(path))}
	})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// TestWatcherRereadsTheFile is the test that justifies the API taking a
// FUNCTION rather than an option list.
//
// FromFiles reads and merges at construction, so a Watcher holding options
// built once would serve the process-start contents forever: Reload would
// succeed, bump the generation, and change nothing. This passes only because
// the sources are rebuilt.
func TestWatcherRereadsTheFile(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	writeEnv(t, env, "W_HOST=first\n")

	w := newFileWatcher(t, env)
	if got := w.Current().Host; got != "first" {
		t.Fatalf("Host = %q, want first", got)
	}

	writeEnv(t, env, "W_HOST=second\n")
	if err := w.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := w.Current().Host; got != "second" {
		t.Errorf("Host = %q, want second — Reload must re-read the file", got)
	}
}

// TestWatcherRejectsAStaticOptionList states the trap as a compile-shaped fact
// rather than a comment: there is no constructor that accepts an already-built
// option list, so the mistake cannot be made.
func TestWatcherRejectsAStaticOptionList(t *testing.T) {
	if _, err := cfgkit.NewWatcher[watchCfg](nil); err == nil {
		t.Fatal("want an error for a nil build function")
	} else if !strings.Contains(err.Error(), "fresh option list") {
		t.Errorf("error %q should say what the build function must return", err)
	}
}

// TestWatcherFirstLoadFailureReturnsNoWatcher pins that a process cannot start
// on a configuration that does not load. Later failures are treated
// differently, which is the next test.
func TestWatcherFirstLoadFailureReturnsNoWatcher(t *testing.T) {
	w, err := cfgkit.NewWatcher[watchCfg](func() []cfgkit.Option {
		return []cfgkit.Option{cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"W_PORT": "999999"}), // fails Validate
		)}
	})
	if err == nil {
		t.Fatal("want the initial load to fail")
	}
	if w != nil {
		t.Error("no Watcher may be returned when the first load fails")
	}
}

// TestBadReloadKeepsTheOldConfiguration is the property that distinguishes this
// from every watcher that assigns first and validates later: an operator's typo
// is reported and the process keeps running on what it had.
func TestBadReloadKeepsTheOldConfiguration(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	writeEnv(t, env, "W_HOST=good\nW_PORT=9000\n")

	w := newFileWatcher(t, env)
	before := w.Generation()

	t.Run("a value that fails validation", func(t *testing.T) {
		writeEnv(t, env, "W_HOST=broken\nW_PORT=999999\n")
		err := w.Reload()
		if err == nil {
			t.Fatal("want the reload to fail")
		}
		// Checked before dereferencing, so the failure NAMES the rule instead
		// of arriving as a segfault. A Reload that stores whatever Load
		// returned publishes a nil configuration, because Load hands back nil
		// on error — and every reader would then panic.
		if w.Current() == nil {
			t.Fatal("Reload published a nil configuration; a failed load must publish NOTHING")
		}
		if got := w.Current().Host; got != "good" {
			t.Errorf("Host = %q, want the PREVIOUS value — a bad edit must not reach the process", got)
		}
		if got := w.Current().Port; got != 9000 {
			t.Errorf("Port = %d, want the previous 9000", got)
		}
		if w.Generation() != before {
			t.Errorf("generation moved to %d on a failed reload; a caller could not tell it failed", w.Generation())
		}
	})

	t.Run("a value that fails to decode", func(t *testing.T) {
		writeEnv(t, env, "W_PORT=not-a-number\n")
		if err := w.Reload(); err == nil {
			t.Fatal("want the reload to fail")
		}
		if w.Current() == nil {
			t.Fatal("Reload published a nil configuration; a failed load must publish NOTHING")
		}
		if got := w.Current().Host; got != "good" {
			t.Errorf("Host = %q, want the previous value", got)
		}
	})

	t.Run("and a later good edit still applies", func(t *testing.T) {
		// The watcher must not be left poisoned by the failures above.
		writeEnv(t, env, "W_HOST=recovered\nW_PORT=7000\n")
		if err := w.Reload(); err != nil {
			t.Fatal(err)
		}
		if got := w.Current().Host; got != "recovered" {
			t.Errorf("Host = %q, want recovered", got)
		}
		if w.Generation() != before+1 {
			t.Errorf("generation = %d, want %d — exactly one successful reload", w.Generation(), before+1)
		}
	})
}

// TestGenerationCountsOnlyPublishedConfigurations pins the counter's meaning,
// which is what lets a caller answer "did my reload take effect" without
// diffing the configuration.
func TestGenerationCountsOnlyPublishedConfigurations(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	writeEnv(t, env, "W_PORT=8080\n")

	w := newFileWatcher(t, env)
	if w.Generation() != 1 {
		t.Errorf("generation = %d, want 1 for the initial load", w.Generation())
	}
	for i := 2; i <= 4; i++ {
		if err := w.Reload(); err != nil {
			t.Fatal(err)
		}
		if w.Generation() != uint64(i) {
			t.Errorf("generation = %d, want %d", w.Generation(), i)
		}
	}
}

// TestSnapshotPairsConfigWithItsOwnProvenance pins that the two are published
// as a unit. Reading them separately can straddle a reload and describe a value
// with the wrong origin — the torn read, reintroduced one level up.
func TestSnapshotPairsConfigWithItsOwnProvenance(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	writeEnv(t, env, "W_HOST=from-file\n")

	w := newFileWatcher(t, env)
	cfg, res := w.Snapshot()

	if cfg.Host != "from-file" {
		t.Fatalf("Host = %q", cfg.Host)
	}
	var found bool
	for _, f := range res.Fields() {
		if f.Path == "Host" {
			found = true
			if !strings.Contains(f.Source, "file:") {
				t.Errorf("Host source = %q, want the file", f.Source)
			}
			if f.Value != cfg.Host {
				t.Errorf("provenance says %q but the config holds %q — different generations", f.Value, cfg.Host)
			}
		}
	}
	if !found {
		t.Error("Host missing from the snapshot's provenance")
	}
}

// TestConcurrentReadsUnderContinuousReload is the headline claim, and it is
// only meaningful under -race, which `task ci` runs.
//
// Viper's own FAQ says the caller must add a mutex; koanf's says the same. Here
// there is no lock in the reader at all.
func TestConcurrentReadsUnderContinuousReload(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	writeEnv(t, env, "W_HOST=h0\nW_PORT=8080\nW_TIMEOUT=30\n")

	w := newFileWatcher(t, env)

	var (
		stop    atomic.Bool
		wg      sync.WaitGroup
		reads   atomic.Int64
		reloads atomic.Int64
	)

	// Readers: no locking anywhere, which is the point.
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				cfg := w.Current()
				// Every field must come from the SAME generation. The file
				// always writes a consistent triple, so an inconsistent one
				// here would be a torn read.
				if cfg.Port != 8080 && cfg.Port != 9090 {
					t.Errorf("Port = %d, from no generation that was ever written", cfg.Port)
					return
				}
				if (cfg.Port == 8080) != (cfg.Timeout == 30) {
					t.Errorf("torn read: Port=%d with Timeout=%d", cfg.Port, cfg.Timeout)
					return
				}
				reads.Add(1)
			}
		}()
	}

	// One writer, flipping the file between two complete, valid states.
	wg.Add(1)
	go func() {
		defer wg.Done()
		bodies := []string{
			"W_HOST=h0\nW_PORT=8080\nW_TIMEOUT=30\n",
			"W_HOST=h1\nW_PORT=9090\nW_TIMEOUT=60\n",
		}
		for i := range 200 {
			writeEnv(t, env, bodies[i%2])
			if err := w.Reload(); err != nil {
				t.Errorf("reload %d: %v", i, err)
				return
			}
			reloads.Add(1)
		}
	}()

	// Let the readers run against a moving target for a moment.
	time.Sleep(50 * time.Millisecond)
	stop.Store(true)
	wg.Wait()

	if reads.Load() == 0 || reloads.Load() == 0 {
		t.Fatalf("the test did not exercise anything: %d reads, %d reloads", reads.Load(), reloads.Load())
	}
}

// TestConcurrentReloads pins that reloads serialise. Without the mutex two
// reloads would both do the work and one result would be discarded, leaving the
// generation counter meaning nothing.
func TestConcurrentReloads(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	writeEnv(t, env, "W_PORT=8080\n")

	w := newFileWatcher(t, env)

	const n = 20
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.Reload(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if got := w.Generation(); got != n+1 {
		t.Errorf("generation = %d, want %d — every successful reload publishes exactly once", got, n+1)
	}
}

// TestCurrentValueIsStableAcrossAReload is the guarantee that lets a request
// handler hold one configuration for its whole life without locking.
func TestCurrentValueIsStableAcrossAReload(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	writeEnv(t, env, "W_HOST=before\n")

	w := newFileWatcher(t, env)
	held := w.Current() // as a request handler would, at the start of a request

	writeEnv(t, env, "W_HOST=after\n")
	if err := w.Reload(); err != nil {
		t.Fatal(err)
	}

	if held.Host != "before" {
		t.Errorf("the held configuration changed to %q; a reload must publish a NEW one, never edit the old", held.Host)
	}
	if w.Current().Host != "after" {
		t.Errorf("Current() = %q, want the new generation", w.Current().Host)
	}
}

// TestWatcherWithNoSourcesAtAll keeps the zero-config guarantee true for the
// reload path too: a Watcher over nothing is valid and reloads fine.
func TestWatcherWithNoSourcesAtAll(t *testing.T) {
	w, err := cfgkit.NewWatcher[watchCfg](func() []cfgkit.Option { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if w.Current().Host != "localhost" || w.Current().Port != 8080 {
		t.Errorf("cfg = %+v, want the compiled-in defaults", w.Current())
	}
	if err := w.Reload(); err != nil {
		t.Fatal(err)
	}
	if w.Generation() != 2 {
		t.Errorf("generation = %d, want 2", w.Generation())
	}
}

// TestWatcherSeesADeletedFile pins the case an operator causes by accident: the
// file goes away, and a missing file is not an error (§4), so the configuration
// falls back to compiled-in defaults rather than the process failing.
func TestWatcherSeesADeletedFile(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	writeEnv(t, env, "W_HOST=from-file\n")

	w := newFileWatcher(t, env)
	if w.Current().Host != "from-file" {
		t.Fatalf("Host = %q", w.Current().Host)
	}

	if err := os.Remove(env); err != nil {
		t.Fatal(err)
	}
	if err := w.Reload(); err != nil {
		t.Fatalf("a missing file is not an error: %v", err)
	}
	if got := w.Current().Host; got != "localhost" {
		t.Errorf("Host = %q, want the compiled-in default once the file is gone", got)
	}
}

// TestResultTracksTheCurrentGeneration pins that provenance follows the swap.
// A stale Result would explain a value the process is no longer running on,
// which is worse than no explanation.
func TestResultTracksTheCurrentGeneration(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	writeEnv(t, env, "W_HOST=before\n")

	w := newFileWatcher(t, env)
	before := valueOf(t, w.Result(), "Host")
	if before != "before" {
		t.Fatalf("Result says Host=%q at generation 1", before)
	}

	writeEnv(t, env, "W_HOST=after\n")
	if err := w.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := valueOf(t, w.Result(), "Host"); got != "after" {
		t.Errorf("Result says Host=%q after a reload, want after", got)
	}
}

// valueOf reads one field's rendered value out of a provenance record.
func valueOf(t *testing.T, res *cfgkit.Result, path string) string {
	t.Helper()
	for _, f := range res.Fields() {
		if f.Path == path {
			return f.Value
		}
	}
	t.Fatalf("field %s not in the provenance record", path)
	return ""
}
