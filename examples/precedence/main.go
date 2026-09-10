// Command precedence stacks four sources and shows which one won each field.
//
// Precedence is POSITIONAL: the value of a field is whatever the last source
// to claim its key said. Nothing is reordered internally, so the order in the
// code is the order that runs — and Explain names the winner.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/ubgo/cfgkit"
)

// Config is the program's contract.
type Config struct {
	AppName  string `env:"APP_NAME" default:"app"`
	Port     int    `env:"PORT" default:"8080"`
	LogLevel string `env:"LOG_LEVEL" default:"info"`

	FeatureNewCheckout bool `env:"FEATURE_NEW_CHECKOUT" default:"false"`
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
		// 1. compiled-in defaults come from the struct tags, always first
		// 2. the image's baseline
		cfgkit.FromFiles("base.env"),
		// 3. the environment-specific overlay, setting only what differs
		cfgkit.FromFiles("prod.env"),
		// 4. an operator's own variables, which must always be able to win
		cfgkit.FromMap(map[string]string{"PORT": "9090"}),
	))
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(w, "%s :%d level=%s new-checkout=%t\n",
		cfg.AppName, cfg.Port, cfg.LogLevel, cfg.FeatureNewCheckout)
	_, _ = fmt.Fprintln(w)

	if err := res.Explain(w); err != nil {
		return err
	}

	// A key that no field binds is REPORTED, never fatal. Strict-fail would
	// break the common case of one file serving several audiences — app
	// config beside deploy-pipeline variables.
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "keys no field claimed:")
	for _, u := range res.Unknown() {
		_, _ = fmt.Fprintf(w, "  %s (from %s)\n", u.Key, u.Source)
	}
	return nil
}
