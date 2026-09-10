// Command read-etcd reads a key prefix from etcd v3.
//
// It RUNS with no etcd installed: the example starts a fake in-process. That
// is possible because the adapter speaks etcd's HTTP/JSON GATEWAY rather than
// its native gRPC API — the same server through a different door, enabled by
// default, and the reason this module needs neither gRPC nor protobuf.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/ubgo/cfgkit"
	etcd "github.com/ubgo/cfgkit/contrib/source-etcd"
)

// Config says nothing about etcd. Keys arrive relative to the prefix.
type Config struct {
	Host     string `env:"HOST" default:"localhost"`
	Port     int    `env:"PORT" default:"8080"`
	Password string `env:"PASSWORD" secret:"true"`
}

const prefix = "app/config/"

// fakeEtcd serves the gateway's range endpoint.
//
// etcd has NO prefix parameter: a prefix scan is a range from the key to its
// SUCCESSOR, which the adapter computes. The gateway takes and returns base64
// for both keys and values, because they are arbitrary bytes on the wire.
func fakeEtcd() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
		kv := func(k, v string) map[string]string {
			return map[string]string{"key": enc(k), "value": enc(v)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"kvs": []map[string]string{
			kv(prefix+"HOST", "etcd.internal"),
			kv(prefix+"PORT", "2379"),
			kv(prefix+"PASSWORD", "s3cret-from-etcd"),
		}})
	}))
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	srv := fakeEtcd()
	defer srv.Close()

	// Real usage: etcd.Prefix("app/config/", etcd.WithAddress("http://etcd:2379")).
	cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
		etcd.Prefix(prefix, etcd.WithAddress(srv.URL)),
		cfgkit.FromEnviron(),
	))
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(w, "%s:%d\n", cfg.Host, cfg.Port)
	_, _ = fmt.Fprintln(w)
	return res.Explain(w)
}
