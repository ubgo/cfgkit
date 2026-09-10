package nats_test

import (
	"time"

	"testing"

	natstest "github.com/nats-io/nats-server/v2/test"
	upstream "github.com/nats-io/nats.go"
	"github.com/ubgo/cfgkit"
	nats "github.com/ubgo/cfgkit/contrib/source-nats"
)

// serverStartTimeout bounds startup: a server that has not accepted a
// connection by now is wedged, and failing beats hanging the suite.
const serverStartTimeout = 10 * time.Second

// runServer starts an in-process NATS server with JetStream enabled.
//
// It exists because the dial path — connect, open JetStream, resolve the
// bucket — cannot be faked at a transport the way the HTTP-based sources in
// this catalogue are: NATS speaks its own protocol. Without a real server the
// claim "reads a NATS KV bucket" would be verified by nothing, and the failure
// that hides is worse than the limitation it describes.
//
// nats-server is a TEST-ONLY dependency. Module graph pruning keeps it out of
// the build list of anything that imports this package, so it costs a consumer
// nothing.
func runServer(t *testing.T) string {
	t.Helper()

	opts := natstest.DefaultTestOptions
	opts.Port = -1 // any free port, so parallel packages never collide
	opts.JetStream = true
	opts.StoreDir = t.TempDir()

	srv := natstest.RunServer(&opts)
	t.Cleanup(srv.Shutdown)

	if !srv.ReadyForConnections(serverStartTimeout) {
		t.Fatal("nats-server did not become ready")
	}
	return srv.ClientURL()
}

// seed creates a bucket and fills it, using the vendor's own client — so the
// data under test was written the way a real deployment writes it.
func seed(t *testing.T, url, bucket string, kvs map[string]string) {
	t.Helper()

	nc, err := upstream.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	kv, err := js.CreateKeyValue(&upstream.KeyValueConfig{Bucket: bucket})
	if err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	for k, v := range kvs {
		if _, err := kv.Put(k, []byte(v)); err != nil {
			t.Fatalf("put %q: %v", k, err)
		}
	}
}

// TestAgainstARealServer is the one test that proves the dial path works.
// Every other test here fakes the bucket; this one does not fake anything.
func TestAgainstARealServer(t *testing.T) {
	url := runServer(t)
	seed(t, url, "app-config", map[string]string{
		"DATABASE_URL": "postgres://prod/db",
		"PORT":         "8080",
	})

	type Config struct {
		DatabaseURL string `env:"DATABASE_URL"`
		Port        int    `env:"PORT"`
	}

	cfg, _, err := cfgkit.Load[Config](
		cfgkit.WithSources(nats.Bucket(url, "app-config")),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DatabaseURL != "postgres://prod/db" || cfg.Port != 8080 {
		t.Errorf("Load = %+v; want the bucket's values", cfg)
	}
}

// TestRealServerPrefixStripping repeats the prefix rule against real storage,
// because key naming is exactly where a fake can quietly diverge from a server.
func TestRealServerPrefixStripping(t *testing.T) {
	url := runServer(t)
	seed(t, url, "shared", map[string]string{
		"api.PORT":    "8080",
		"worker.PORT": "9090",
	})

	src := nats.Bucket(url, "shared", nats.WithPrefix("api."))

	got, ok, err := src.Lookup("PORT")
	if err != nil || !ok || got != "8080" {
		t.Fatalf("Lookup(PORT) = %q, %v, %v; want 8080, true, nil", got, ok, err)
	}
	if _, ok, _ := src.Lookup("worker.PORT"); ok {
		t.Error("out-of-prefix key leaked through against a real server")
	}
}

// TestRealServerMissingBucket pins that naming a bucket that does not exist is
// an error rather than an empty source — the same distinction the faked tests
// make, confirmed against the error the server actually returns.
func TestRealServerMissingBucket(t *testing.T) {
	url := runServer(t)

	src := nats.Bucket(url, "does-not-exist")

	if _, _, err := src.Lookup("PORT"); err == nil {
		t.Fatal("a nonexistent bucket was accepted")
	}
}

// TestRealServerEmptyBucket confirms the ErrNoKeysFound translation against the
// server that raises it, rather than against this package's belief about it.
func TestRealServerEmptyBucket(t *testing.T) {
	url := runServer(t)
	seed(t, url, "empty", nil)

	src := nats.Bucket(url, "empty", nats.Optional())

	if _, ok, err := src.Lookup("PORT"); err != nil {
		t.Fatalf("an empty bucket errored under Optional(): %v", err)
	} else if ok {
		t.Error("Lookup reported found in an empty bucket")
	}
}

// TestServerWithoutJetStreamIsReported pins the failure a fleet actually hits:
// NATS is running and reachable, but JetStream was never enabled on it.
//
// It must be an error, and it must not look like an empty bucket — "nothing
// configured" and "the feature is off" have completely different fixes, and a
// deploy that treats the second as the first boots on defaults.
func TestServerWithoutJetStreamIsReported(t *testing.T) {
	opts := natstest.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = false // the whole point of this test

	srv := natstest.RunServer(&opts)
	t.Cleanup(srv.Shutdown)
	if !srv.ReadyForConnections(serverStartTimeout) {
		t.Fatal("nats-server did not become ready")
	}

	src := nats.Bucket(srv.ClientURL(), "app-config")

	_, ok, err := src.Lookup("PORT")
	if err == nil {
		t.Fatal("a server without JetStream was accepted")
	}
	if ok {
		t.Error("Lookup reported found alongside an error")
	}
	// Optional() must NOT rescue this: it covers an empty bucket, never a
	// server that cannot serve buckets at all.
	optional := nats.Bucket(srv.ClientURL(), "app-config", nats.Optional())
	if _, _, err := optional.Lookup("PORT"); err == nil {
		t.Error("Optional() swallowed a missing-JetStream failure")
	}
}
