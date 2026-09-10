// Command read-commandline binds command-line flags, from the standard
// library and from pflag.
//
// The rule that makes flags work as a config layer is this: a flag counts
// ONLY when the user actually typed it. A flag's own default must never reach
// the configuration, because it would silently outrank the file and the
// environment — the highest-precedence layer would then always be "set", and
// nothing below it could ever win.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/spf13/pflag"
	"github.com/ubgo/cfgkit"
	pflagsrc "github.com/ubgo/cfgkit/contrib/flags-pflag"
)

// Config opts individual fields into the flag layer.
//
// Flags are OPT-IN per field, via the `flag:` tag. Without it a field is
// invisible to a flag source, however the flag is spelled. That is deliberate:
// a configuration with 150 fields must not silently produce a 150-flag
// command line, so exposing a field on the CLI is a decision you write down.
//
// The tag also holds the flag NAME, which is why it is separate from `env:`.
// Flags are conventionally lowercase and env keys conventionally are not, and
// a source keyed by flag name can then coexist with sources keyed by env key.
type Config struct {
	Host string `env:"HOST" default:"localhost" flag:"host"`
	Port int    `env:"PORT" default:"8080" flag:"port"`

	// No flag tag: this one is configurable by file and environment only, and
	// no --log-level exists however it is typed.
	LogLevel string `env:"LOG_LEVEL" default:"info"`
}

func main() {
	if err := run(os.Stdout, os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer, args []string) error {
	// --- standard library ---------------------------------------------
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	// The flag's default here is 9999, deliberately absurd: if it ever
	// reached the configuration it would be obvious.
	fs.Int("port", 9999, "port to listen on")
	fs.String("host", "flag-default", "host to bind")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"HOST": "from-file", "PORT": "8080"}),
		cfgkit.FromFlagSet(fs), // last: an operator's flag must be able to win
	))
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "stdlib flag: %s:%d level=%s\n", cfg.Host, cfg.Port, cfg.LogLevel)
	if err := res.Explain(w); err != nil {
		return err
	}

	// --- pflag (cobra) -------------------------------------------------
	pfs := pflag.NewFlagSet("app", pflag.ContinueOnError)
	pfs.SetOutput(io.Discard)
	pfs.Int("port", 9999, "port to listen on")
	pfs.String("host", "flag-default", "host to bind")
	if err := pfs.Parse(args); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(w)
	pcfg, pres, err := cfgkit.Load[Config](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"HOST": "from-file", "PORT": "8080"}),
		pflagsrc.Source(pfs),
	))
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "pflag: %s:%d level=%s\n", pcfg.Host, pcfg.Port, pcfg.LogLevel)
	return pres.Explain(w)
}
