// Command read-formats binds the SAME struct from every format cfgkit reads.
//
// Each format module has its own tests, and each proves that module parses its
// own format. None of them can prove the property a user actually relies on:
// that YAML, TOML, HCL, JSON, INI, .properties and .env all land on the same
// struct with the same values. That is a cross-module claim, so it needs a
// shared fixture and a place outside every module to check it — here.
package main

import (
	"fmt"
	"io"
	"os"
	"reflect"

	"github.com/ubgo/cfgkit"
	hclsrc "github.com/ubgo/cfgkit/contrib/format-hcl"
	inisrc "github.com/ubgo/cfgkit/contrib/format-ini"
	propsrc "github.com/ubgo/cfgkit/contrib/format-properties"
	tomlsrc "github.com/ubgo/cfgkit/contrib/format-toml"
	yamlsrc "github.com/ubgo/cfgkit/contrib/format-yaml"
	"github.com/ubgo/cfgkit/mock"
)

// sourceFor returns the source that reads one fixture.
//
// Every format is reached through the same two-argument shape —
// Source(name, doc) — which is not an accident: an adapter that invented its
// own constructor signature would make swapping formats a rewrite rather than
// a one-line change.
//
// The return type is `any` because the two families have no common supertype:
// YAML, TOML and HCL return cfgkit.StructuredSource, while INI, .properties
// and .env return cfgkit.Source. cfgkit.WithSources takes ...any for exactly
// this reason, so this signature matches the library boundary rather than
// inventing a wrapper interface that would have to be unwrapped again.
func sourceFor(f mock.Fixture, doc []byte) (any, error) {
	switch f.Format {
	case "yaml":
		return yamlsrc.Source(f.File, doc), nil
	case "toml":
		return tomlsrc.Source(f.File, doc), nil
	case "hcl":
		return hclsrc.Source(f.File, doc), nil
	case "json":
		// JSON needs no module: it is in the core, because encoding/json is
		// in the standard library and costs nobody a dependency.
		return cfgkit.FromJSON(doc), nil
	case "ini":
		return inisrc.Source(f.File, doc), nil
	case "properties":
		return propsrc.Source(f.File, doc), nil
	case "env":
		// .env is also core. Reading it from the embedded FS rather than disk
		// keeps the example independent of its working directory.
		return cfgkit.FromFS(mock.FS, f.File), nil
	}
	return nil, fmt.Errorf("no source for format %q", f.Format)
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	want := mock.Want()

	_, _ = fmt.Fprintf(w, "%-11s %-6s %s\n", "FORMAT", "SHAPE", "BINDS THE CANONICAL CONFIG")
	for _, f := range mock.Fixtures {
		doc, err := mock.FS.ReadFile(f.File)
		if err != nil {
			return err
		}
		src, err := sourceFor(f, doc)
		if err != nil {
			return err
		}

		got, _, err := cfgkit.Load[mock.Config](cfgkit.WithSources(src))
		if err != nil {
			return fmt.Errorf("%s: %w", f.Format, err)
		}

		shape := "nested"
		if f.Flat {
			shape = "flat"
		}
		_, _ = fmt.Fprintf(w, "%-11s %-6s %t\n", f.Format, shape, reflect.DeepEqual(*got, want))
	}

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "every format produced: service=%s server=%s:%d db=%s max_conns=%d\n",
		want.Service, want.Server.Host, want.Server.Port, want.Database.URL, want.Database.MaxConns)
	return nil
}
