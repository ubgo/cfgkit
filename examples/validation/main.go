// Command validation fails on purpose, before anything boots.
//
// The point is WHERE the failure happens. A misconfigured deploy that panics
// at container start fails after the rollout, in a place with no logs yet.
// cfgkit.Check runs the same pipeline in CI and reports every problem at once.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ubgo/cfgkit"
)

// Config carries its own rules.
type Config struct {
	Env  string `env:"APP_ENV" default:"dev"`
	Port int    `env:"PORT" default:"8080"`

	LogLevel string `env:"LOG_LEVEL" default:"info"`

	DatabaseURL      string `env:"DATABASE_URL"`
	DatabasePassword string `env:"DATABASE_PASSWORD" secret:"true"`
}

// Validate reports EVERY problem, joined, rather than the first one.
//
// Returning early would turn a misconfigured deploy into a guessing game of
// one fix per restart — and each restart is another failed rollout.
func (c *Config) Validate() error {
	return errors.Join(
		cfgkit.Range("PORT", c.Port, 1, 65535),
		cfgkit.OneOf("LOG_LEVEL", c.LogLevel, "debug", "info", "warn", "error"),

		// Required only in production. The same struct stays runnable on a
		// laptop with nothing set, which is what keeps the zero-input promise
		// from quietly becoming "zero input, except the seven you need".
		cfgkit.RequiredIn(cfgkit.ModeProd, mode(c.Env), "DATABASE_URL", c.DatabaseURL),
		cfgkit.RequiredIn(cfgkit.ModeProd, mode(c.Env), "DATABASE_PASSWORD", c.DatabasePassword),
	)
}

// mode maps the app's own env name onto cfgkit's mode.
//
// It is a function rather than an inline comparison because the mapping is a
// policy decision — "staging counts as production for validation" is the kind
// of rule that belongs in one place.
func mode(env string) cfgkit.Mode {
	switch env {
	case "prod", "production", "staging":
		return cfgkit.ModeProd
	default:
		return cfgkit.ModeDev
	}
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	// 1. A laptop: nothing set. Valid, because the rules are mode-scoped.
	_, _ = fmt.Fprintln(w, "dev, nothing set:")
	report(w, cfgkit.Check[Config](cfgkit.WithSources(cfgkit.FromMap(nil))))

	// 2. Production with the database unset. TWO problems, reported together.
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "prod, database unset:")
	report(w, cfgkit.Check[Config](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
		"APP_ENV": "production",
	}))))

	// 3. Values that are present but wrong. Both reported, not just the first.
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "bad port and unknown log level:")
	report(w, cfgkit.Check[Config](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
		"PORT":      "70000",
		"LOG_LEVEL": "verbose",
	}))))

	return nil
}

// report prints a Check result the way a CI step would.
//
// The error is printed as cfgkit formats it, indented. Reformatting it here
// would mean this example and a real pipeline show different text for the same
// failure, which is the opposite of what an example is for.
func report(w io.Writer, err error) {
	if err == nil {
		_, _ = fmt.Fprintln(w, "  ok")
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(err.Error()), "\n") {
		_, _ = fmt.Fprintf(w, "  %s\n", line)
	}
}
