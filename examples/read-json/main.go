// Command read-json binds a JSON document.
//
// JSON is a STRUCTURED source: it carries its own shape and types, so it maps
// onto nested structs through their json tags rather than through flat keys.
// That is the difference from a .env file, where every value is a string and
// nesting has to be spelled out in the key.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/ubgo/cfgkit"
)

// Config nests, because the document does.
type Config struct {
	Service string `json:"service" env:"SERVICE" default:"api"`

	Server struct {
		Port int    `json:"port" env:"PORT" default:"8080"`
		Host string `json:"host" env:"HOST" default:"0.0.0.0"`
	} `json:"server"`

	Database struct {
		URL      string `json:"url" env:"DATABASE_URL"`
		Password string `json:"password" env:"DATABASE_PASSWORD" secret:"true"`
	} `json:"database"`
}

// document is the kind of payload a config service returns. Note it does NOT
// mention server.host — a structured source merges onto the struct rather
// than replacing it, so the compiled-in default survives.
var document = []byte(`{
  "service": "checkout",
  "server":   { "port": 9000 },
  "database": { "url": "postgres://db/checkout", "password": "s3cret" }
}`)

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
		cfgkit.FromJSON(document),
		cfgkit.FromEnviron(),
	))
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(w, "%s on %s:%d\n", cfg.Service, cfg.Server.Host, cfg.Server.Port)
	_, _ = fmt.Fprintln(w)
	return res.Explain(w)
}
