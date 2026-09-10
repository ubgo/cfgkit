package nats_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	upstream "github.com/nats-io/nats.go"
	"github.com/ubgo/cfgkit"
	nats "github.com/ubgo/cfgkit/contrib/source-nats"
)

// entry is a nats.KeyValueEntry carrying only what this package reads.
//
// The rest of the interface is present because the vendor requires it, not
// because anything here uses it — which is itself worth pinning: if this
// package ever starts reading Revision or Operation, these zero values will
// make that visible in a test rather than in production.
type entry struct {
	bucket, key string
	value       []byte
}

func (e entry) Bucket() string                 { return e.bucket }
func (e entry) Key() string                    { return e.key }
func (e entry) Value() []byte                  { return e.value }
func (e entry) Revision() uint64               { return 1 }
func (e entry) Created() time.Time             { return time.Time{} }
func (e entry) Delta() uint64                  { return 0 }
func (e entry) Operation() upstream.KeyValueOp { return upstream.KeyValuePut }

// fake stands in for a JetStream bucket. Every test uses it — the suite never
// starts nats-server and never depends on one being installed.
type fake struct {
	data map[string]string

	// keysErr and getErr inject the failures a live bucket produces.
	keysErr error
	getErr  map[string]error

	// gets records read keys, which is how the tests prove the prefix filter
	// runs BEFORE the read rather than after.
	gets []string
}

func (f *fake) Keys(_ ...upstream.WatchOpt) ([]string, error) {
	if f.keysErr != nil {
		return nil, f.keysErr
	}
	keys := make([]string, 0, len(f.data))
	for k := range f.data {
		keys = append(keys, k)
	}
	return keys, nil
}

func (f *fake) Get(key string) (upstream.KeyValueEntry, error) {
	f.gets = append(f.gets, key)
	if err, ok := f.getErr[key]; ok {
		return nil, err
	}
	v, ok := f.data[key]
	if !ok {
		return nil, upstream.ErrKeyNotFound
	}
	return entry{bucket: "app-config", key: key, value: []byte(v)}, nil
}

func TestReadsTheBucket(t *testing.T) {
	f := &fake{data: map[string]string{"DATABASE_URL": "postgres://prod", "PORT": "8080"}}
	src := nats.Bucket("nats://ignored", "app-config", nats.WithKV(f))

	for _, want := range []struct{ key, val string }{
		{"DATABASE_URL", "postgres://prod"},
		{"PORT", "8080"},
	} {
		got, ok, err := src.Lookup(want.key)
		if err != nil {
			t.Fatalf("Lookup(%q) error: %v", want.key, err)
		}
		if !ok || got != want.val {
			t.Errorf("Lookup(%q) = %q, %v; want %q, true", want.key, got, ok, want.val)
		}
	}
}

func TestMissingKeyIsAMissNotAnError(t *testing.T) {
	f := &fake{data: map[string]string{"PORT": "8080"}}
	src := nats.Bucket("nats://ignored", "app-config", nats.WithKV(f))

	_, ok, err := src.Lookup("NOPE")
	if err != nil {
		t.Fatalf("Lookup error: %v", err)
	}
	if ok {
		t.Error("Lookup(NOPE) reported found")
	}
}

// TestListFailureIsAnErrorNotAnEmptySource is the safety argument: a bucket
// this process may not read must not look like a bucket with nothing in it.
func TestListFailureIsAnErrorNotAnEmptySource(t *testing.T) {
	f := &fake{keysErr: errors.New("nats: permissions violation for KV access")}
	src := nats.Bucket("nats://ignored", "app-config", nats.WithKV(f))

	_, ok, err := src.Lookup("PORT")
	if err == nil {
		t.Fatal("Lookup succeeded despite a listing failure")
	}
	if ok {
		t.Error("Lookup reported found alongside an error")
	}
	for _, want := range []string{"app-config", "permissions violation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestGetFailureIsAnError(t *testing.T) {
	f := &fake{
		data:   map[string]string{"PORT": "8080"},
		getErr: map[string]error{"PORT": errors.New("stream is offline")},
	}
	src := nats.Bucket("nats://ignored", "app-config", nats.WithKV(f))

	if _, _, err := src.Lookup("PORT"); err == nil {
		t.Fatal("a read failure was accepted")
	} else if !strings.Contains(err.Error(), "PORT") {
		t.Errorf("error %q does not name the key", err)
	}
}

// TestKeyDeletedBetweenListAndReadIsSkipped pins the one failure that must NOT
// be fatal. The bucket is live and other writers exist; letting an unrelated
// deletion crash an unrelated service's boot would be the wrong trade.
func TestKeyDeletedBetweenListAndReadIsSkipped(t *testing.T) {
	f := &fake{
		data:   map[string]string{"PORT": "8080", "GONE": "x"},
		getErr: map[string]error{"GONE": upstream.ErrKeyNotFound},
	}
	src := nats.Bucket("nats://ignored", "app-config", nats.WithKV(f))

	got, ok, err := src.Lookup("PORT")
	if err != nil || !ok || got != "8080" {
		t.Fatalf("Lookup(PORT) = %q, %v, %v; want 8080, true, nil", got, ok, err)
	}
	if _, ok, _ := src.Lookup("GONE"); ok {
		t.Error("a key deleted mid-read still resolved")
	}
}

// TestEmptyBucketReportsErrNoKeysFound covers the vendor quirk that an empty
// bucket errors rather than returning an empty list. Untranslated, every empty
// bucket would look like a failure — which is a different thing with a
// different answer.
func TestEmptyBucketReportsErrNoKeysFound(t *testing.T) {
	f := &fake{keysErr: upstream.ErrNoKeysFound}
	src := nats.Bucket("nats://ignored", "app-config", nats.WithKV(f), nats.Optional())

	_, ok, err := src.Lookup("PORT")
	if err != nil {
		t.Fatalf("an empty bucket errored under Optional(): %v", err)
	}
	if ok {
		t.Error("Lookup reported found in an empty bucket")
	}
}

func TestEmptyBucketIsAnErrorByDefault(t *testing.T) {
	f := &fake{data: map[string]string{}}
	src := nats.Bucket("nats://ignored", "app-config", nats.WithKV(f))

	if _, _, err := src.Lookup("PORT"); err == nil {
		t.Fatal("an empty bucket was accepted without Optional()")
	} else if !strings.Contains(err.Error(), "nats.Optional()") {
		t.Errorf("error %q does not name the fix", err)
	}
}

// TestOptionalDoesNotSuppressRealFailures separates "nothing there" from
// "could not look" — Optional() means the first, never the second.
func TestOptionalDoesNotSuppressRealFailures(t *testing.T) {
	f := &fake{keysErr: errors.New("nats: permissions violation")}
	src := nats.Bucket("nats://ignored", "app-config", nats.WithKV(f), nats.Optional())

	if _, _, err := src.Lookup("PORT"); err == nil {
		t.Fatal("Optional() swallowed a permission failure")
	}
}

func TestPrefixFiltersAndStrips(t *testing.T) {
	f := &fake{data: map[string]string{
		"api.PORT":    "8080",
		"api.HOST":    "0.0.0.0",
		"worker.PORT": "9090",
	}}
	src := nats.Bucket("nats://ignored", "app-config",
		nats.WithKV(f), nats.WithPrefix("api."))

	got, ok, err := src.Lookup("PORT")
	if err != nil || !ok || got != "8080" {
		t.Errorf("Lookup(PORT) = %q, %v, %v; want 8080, true, nil", got, ok, err)
	}
	// The prefixed spelling must NOT also resolve — stripping is a rename, not
	// an alias, or two fields could bind the same value and drift.
	if _, ok, _ := src.Lookup("api.PORT"); ok {
		t.Error("prefixed name still resolves after stripping")
	}
	if _, ok, _ := src.Lookup("worker.PORT"); ok {
		t.Error("out-of-prefix key leaked through")
	}
}

// TestPrefixIsFilteredBeforeTheRead matters for cost, not correctness: a bucket
// serving ten services should not cost every service ten reads at boot.
func TestPrefixIsFilteredBeforeTheRead(t *testing.T) {
	f := &fake{data: map[string]string{
		"api.PORT":    "8080",
		"worker.PORT": "9090",
		"cron.PORT":   "7070",
	}}
	nats.Bucket("nats://ignored", "app-config", nats.WithKV(f), nats.WithPrefix("api."))

	if len(f.gets) != 1 || f.gets[0] != "api.PORT" {
		t.Errorf("read %v; want only [api.PORT] — out-of-prefix keys were fetched "+
			"and then discarded", f.gets)
	}
}

func TestPrefixThatMatchesNothingIsAnError(t *testing.T) {
	f := &fake{data: map[string]string{"worker.PORT": "9090"}}
	src := nats.Bucket("nats://ignored", "app-config",
		nats.WithKV(f), nats.WithPrefix("api."))

	if _, _, err := src.Lookup("PORT"); err == nil {
		t.Fatal("a prefix matching nothing was accepted")
	}
}

func TestBucketIsReadOnceNotPerLookup(t *testing.T) {
	f := &fake{data: map[string]string{"A": "1", "B": "2"}}
	src := nats.Bucket("nats://ignored", "app-config", nats.WithKV(f))

	before := len(f.gets)
	for range 10 {
		// Return values are irrelevant here; the point is the CALL COUNT.
		_, _, _ = src.Lookup("A")
		_, _, _ = src.Lookup("B")
	}
	if len(f.gets) != before {
		t.Errorf("Lookup issued %d extra reads; want 0 — a per-key read turns a "+
			"fifty-field config into fifty round trips at boot", len(f.gets)-before)
	}
	if before != 2 {
		t.Errorf("construction issued %d reads; want 2", before)
	}
}

func TestKeysListsTheBucket(t *testing.T) {
	f := &fake{data: map[string]string{"A": "1", "B": "2"}}
	src := nats.Bucket("nats://ignored", "app-config", nats.WithKV(f))

	lister, ok := src.(cfgkit.KeyLister)
	if !ok {
		t.Fatal("source does not implement cfgkit.KeyLister")
	}
	if got := lister.Keys(); len(got) != 2 {
		t.Errorf("Keys() = %v; want 2 entries", got)
	}
}

func TestKeysReportsStrippedNames(t *testing.T) {
	f := &fake{data: map[string]string{"api.PORT": "8080"}}
	src := nats.Bucket("nats://ignored", "app-config",
		nats.WithKV(f), nats.WithPrefix("api."))

	keys := src.(cfgkit.KeyLister).Keys()
	if len(keys) != 1 || keys[0] != "PORT" {
		t.Errorf("Keys() = %v; want [PORT] — an unknown-key warning naming the "+
			"unstripped spelling would point at a name no struct can bind", keys)
	}
}

func TestNameIdentifiesTheBucket(t *testing.T) {
	f := &fake{data: map[string]string{"A": "1"}}
	src := nats.Bucket("nats://ignored", "app-config", nats.WithKV(f))

	if got := src.Name(); got != "nats:app-config" {
		t.Errorf("Name() = %q, want nats:app-config", got)
	}
}

// TestDialIsUsedWithoutWithKV exercises the production path without a server,
// by dialing an address nothing listens on. It proves the dial path is wired —
// a suite that always injects a fake would never notice if it were not.
func TestDialIsUsedWithoutWithKV(t *testing.T) {
	src := nats.Bucket("nats://127.0.0.1:1", "app-config",
		nats.WithOptions(upstream.Timeout(100*time.Millisecond), upstream.MaxReconnects(0)))

	_, _, err := src.Lookup("PORT")
	if err == nil {
		t.Fatal("connecting to a closed port succeeded")
	}
	if !strings.Contains(err.Error(), "connecting to") {
		t.Errorf("error %q is not the connect failure", err)
	}
}

// TestBindsThroughLoad is the end-to-end check: a bucket reaching a real struct
// through the real cfgkit chain.
func TestBindsThroughLoad(t *testing.T) {
	type Config struct {
		DatabaseURL string `env:"DATABASE_URL"`
		Port        int    `env:"PORT"`
	}

	f := &fake{data: map[string]string{
		"DATABASE_URL": "postgres://prod/db",
		"PORT":         "8080",
	}}
	cfg, _, err := cfgkit.Load[Config](
		cfgkit.WithSources(nats.Bucket("nats://ignored", "app-config", nats.WithKV(f))),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DatabaseURL != "postgres://prod/db" || cfg.Port != 8080 {
		t.Errorf("Load = %+v; want the bucket's values", cfg)
	}
}

// TestLaterSourceStillWins pins ordering: NATS is a source like any other, so
// an operator override in the environment is not defeated by it.
func TestLaterSourceStillWins(t *testing.T) {
	type Config struct {
		Port int `env:"PORT"`
	}

	f := &fake{data: map[string]string{"PORT": "8080"}}
	t.Setenv("PORT", "9999")

	cfg, _, err := cfgkit.Load[Config](cfgkit.WithSources(
		nats.Bucket("nats://ignored", "app-config", nats.WithKV(f)),
		cfgkit.FromEnviron(),
	))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 9999 {
		t.Errorf("Port = %d; want 9999 — a later source must still override NATS", cfg.Port)
	}
}
