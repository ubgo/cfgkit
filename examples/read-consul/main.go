// Command read-consul reads a key prefix from Consul's KV store.
//
// It RUNS with no Consul installed: the example starts a fake in-process.
// That is possible because the adapter is net/http and encoding/json, so it
// can be pointed anywhere — the same property that keeps it dependency-free.
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
	consul "github.com/ubgo/cfgkit/contrib/source-consul"
)

// Config says nothing about Consul. Keys arrive RELATIVE to the prefix, which
// is what lets the same struct bind from Consul and from a .env file.
type Config struct {
	Host     string `env:"HOST" default:"localhost"`
	Port     int    `env:"PORT" default:"8080"`
	Password string `env:"PASSWORD" secret:"true"`
}

// prefix is the KV namespace this service owns.
const prefix = "app/checkout/"

// entry is one row of Consul's KV reply. Values are base64 in the wire format,
// which is why a corrupt value must name the KEY and not echo the bytes.
type entry struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// fakeConsul serves a recursive KV read.
func fakeConsul() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
		_ = json.NewEncoder(w).Encode([]entry{
			// A "folder" key: Consul creates these for the prefix itself, and
			// they carry no value. Skipping them is the adapter's job.
			{Key: prefix, Value: ""},
			{Key: prefix + "HOST", Value: enc("consul.internal")},
			{Key: prefix + "PORT", Value: enc("7000")},
			{Key: prefix + "PASSWORD", Value: enc("s3cret-from-consul")},
		})
	}))
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	srv := fakeConsul()
	defer srv.Close()

	// Real usage is one line: consul.Prefix("app/checkout/"). The address
	// comes from CONSUL_HTTP_ADDR and the token from CONSUL_HTTP_TOKEN, so a
	// machine that can already run `consul kv get` needs nothing more.
	cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
		consul.Prefix(prefix, consul.WithAddress(srv.URL)),
		cfgkit.FromEnviron(),
	))
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(w, "%s:%d\n", cfg.Host, cfg.Port)
	_, _ = fmt.Fprintln(w)
	return res.Explain(w)
}
