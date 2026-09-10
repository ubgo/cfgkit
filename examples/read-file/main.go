// Command read-file loads configuration from a .env file.
//
// It is the example to read first: a .env file is the source almost every
// program starts with, and everything else in this directory is a variation
// on what happens here.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/ubgo/cfgkit"
)

// Config is the whole configuration surface of this program.
//
// The struct is the contract. Every key the program reads is declared here
// with its type and its default, so "what can I configure?" is answered by
// reading one type rather than grepping for os.Getenv.
type Config struct {
	AppName string `env:"APP_NAME" default:"app" doc:"identifies this service in logs"`
	Port    int    `env:"PORT" default:"8080" doc:"the port the HTTP server binds"`
	BaseURL string `env:"BASE_URL" doc:"public origin, used to build absolute links"`

	Greeting string `env:"GREETING" default:"hi"`

	// secret:"true" keeps the value out of Explain, JSON and error messages.
	// It changes nothing about how the value is read — only how it is shown.
	DatabaseURL      string `env:"DATABASE_URL"`
	DatabasePassword string `env:"DATABASE_PASSWORD" secret:"true"`
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	cfg, res, err := cfgkit.Load[Config](
		cfgkit.WithSources(
			cfgkit.FromFiles("app.env"),

			// The environment comes LAST, so an operator can always override
			// the file without editing it. Precedence is positional: the last
			// source that claims a key wins, and nothing is reordered.
			cfgkit.FromEnviron(),
		),
	)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(w, "%s listening on %s\n", cfg.AppName, cfg.BaseURL)
	_, _ = fmt.Fprintln(w)

	// Explain answers the question every layered configuration eventually
	// raises: not "what is the value" but "which source decided it".
	_, _ = fmt.Fprintln(w, "where each value came from:")
	return res.Explain(w)
}
