// Command provenance reads the Result in code rather than printing a table.
//
// Explain is for a human at 3am. Result.Fields and Result.JSON are for the
// program itself: a /debug/config endpoint, a startup log line, a CI step that
// asserts production is not running on a default.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/ubgo/cfgkit"
)

// Config is deliberately small; the interest is in the Result, not the struct.
type Config struct {
	AppName string `env:"APP_NAME" default:"demo"`
	Port    int    `env:"PORT" default:"8080"`

	DatabaseURL      string `env:"DATABASE_URL" default:"postgres://localhost/dev"`
	DatabasePassword string `env:"DATABASE_PASSWORD" default:"dev-password" secret:"true"`
}

// sourceDefault is the source name cfgkit reports for a compiled-in default.
//
// Named rather than written as a literal at each comparison: it is a value
// with a closed set of meanings, and a typo in a string comparison is a check
// that silently never fires.
const sourceDefault = "default"

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	_, res, err := cfgkit.Load[Config](
		// Stated explicitly. With an explicit source list, the mode is not
		// inferred from the values — that inference belongs to DefaultSources,
		// which resolves it in a first pass before choosing .env.<mode>.
		// Deriving it from an arbitrary source here would be circular.
		cfgkit.WithMode(cfgkit.ModeProd),
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"PORT": "9090"})),
	)
	if err != nil {
		return err
	}

	// 1. The audit a deploy gate actually wants: which fields are still on
	// their compiled-in default? In production that is usually a mistake, and
	// it is invisible without provenance — the value looks perfectly fine.
	_, _ = fmt.Fprintln(w, "still on a default:")
	for _, f := range res.Fields() {
		if f.Source == sourceDefault {
			_, _ = fmt.Fprintf(w, "  %s (%s)\n", f.Path, f.Key)
		}
	}

	// 2. Secrets stay masked in the structured output too. Masking follows the
	// FIELD, so it survives every rendering — table, JSON, error message.
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "as JSON, for a /debug/config endpoint:")
	doc, err := res.JSON()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "%s\n", doc)

	return nil
}
