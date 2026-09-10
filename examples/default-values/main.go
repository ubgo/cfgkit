// Command default-values runs on nothing at all.
//
// This is the property the whole library hangs off: Load with no sources and
// an empty environment returns a VALID configuration. A program that cannot
// start without a config file cannot be tried, and a program that cannot be
// tried does not get adopted.
package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ubgo/cfgkit"
)

// Config declares a default for every field that has a sensible one.
type Config struct {
	Name    string        `env:"APP_NAME" default:"demo"`
	Port    int           `env:"PORT" default:"8080"`
	Debug   bool          `env:"DEBUG" default:"false"`
	Timeout time.Duration `env:"TIMEOUT" default:"30s"`

	// A slice default is split on the delimiter, so a list needs no special
	// source and no JSON smuggled into an environment variable.
	Origins []string `env:"ORIGINS" default:"http://localhost:3000,http://localhost:5173" delim:","`

	// No default: the zero value IS the sensible answer. Declaring
	// default:"" would say the same thing with more words.
	ExtraHeader string `env:"EXTRA_HEADER"`
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	// No sources at all. Not an empty file, not an empty map — nothing.
	cfg, res, err := cfgkit.Load[Config]()
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(w, "%s :%d debug=%t timeout=%s\n", cfg.Name, cfg.Port, cfg.Debug, cfg.Timeout)
	_, _ = fmt.Fprintf(w, "origins: %v\n", cfg.Origins)
	_, _ = fmt.Fprintln(w)
	return res.Explain(w)
}
