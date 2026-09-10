// Command read-nats reads a NATS JetStream key/value bucket.
//
// It RUNS with no NATS installed: the example starts a real nats-server
// IN-PROCESS with JetStream enabled and seeds a bucket with the vendor's own
// client. A fake would not do here — NATS speaks its own wire protocol, so
// there is no HTTP transport to stand in front of, which is also why this
// adapter takes the vendor library as a dependency.
//
// nats-server is a test-only dependency of the adapter; this example pulls it
// in explicitly because it needs a server to talk to.
package main

import (
	"fmt"
	"io"
	"os"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	upstream "github.com/nats-io/nats.go"
	"github.com/ubgo/cfgkit"
	nats "github.com/ubgo/cfgkit/contrib/source-nats"
)

// Config says nothing about NATS.
type Config struct {
	Host     string `env:"HOST" default:"localhost"`
	Port     int    `env:"PORT" default:"8080"`
	Password string `env:"PASSWORD" secret:"true"`
}

const (
	bucket = "app-config"

	// startTimeout bounds server startup: one that has not accepted a
	// connection by now is wedged, and failing beats hanging.
	startTimeout = 10 * time.Second
)

// runServer starts an in-process NATS server with JetStream enabled.
func runServer() (*natsserver.Server, error) {
	opts := natstest.DefaultTestOptions
	opts.Port = -1 // any free port, so nothing collides
	opts.JetStream = true
	dir, err := os.MkdirTemp("", "nats-example")
	if err != nil {
		return nil, err
	}
	opts.StoreDir = dir

	srv := natstest.RunServer(&opts)
	if !srv.ReadyForConnections(startTimeout) {
		srv.Shutdown()
		return nil, fmt.Errorf("nats-server did not become ready")
	}
	return srv, nil
}

// seed fills the bucket using the vendor's client, so the data under test was
// written the way a real deployment writes it.
func seed(url string, kv map[string]string) error {
	nc, err := upstream.Connect(url)
	if err != nil {
		return err
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		return err
	}
	store, err := js.CreateKeyValue(&upstream.KeyValueConfig{Bucket: bucket})
	if err != nil {
		return err
	}
	for k, v := range kv {
		if _, err := store.Put(k, []byte(v)); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	srv, err := runServer()
	if err != nil {
		return err
	}
	defer srv.Shutdown()
	// Cleanup failure is not actionable and must not mask the real result.
	storeDir := srv.JetStreamConfig().StoreDir
	defer func() { _ = os.RemoveAll(storeDir) }()

	if err := seed(srv.ClientURL(), map[string]string{
		"HOST":     "nats.internal",
		"PORT":     "4222",
		"PASSWORD": "s3cret-from-nats",
	}); err != nil {
		return err
	}

	// Real usage: nats.Bucket("nats://localhost:4222", "app-config").
	//
	// The bucket is read ONCE and the connection this package opened closes
	// immediately — nothing after construction needs it, and holding one open
	// for the life of the process to serve a map that never changes is a
	// resource leak with extra steps.
	cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
		nats.Bucket(srv.ClientURL(), bucket),
		cfgkit.FromEnviron(),
	))
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(w, "%s:%d\n", cfg.Host, cfg.Port)
	_, _ = fmt.Fprintln(w)
	return res.Explain(w)
}
