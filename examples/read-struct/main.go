// Command read-struct uses an in-memory struct as a configuration source.
//
// It exists for defaults that are not literals: computed at startup, fetched
// from a metadata service, or shipped by a library for whoever embeds it. A
// `default:` tag can only hold a constant — FromStruct holds anything the
// program can compute before Load runs.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/ubgo/cfgkit"
)

// Config is what the program binds.
type Config struct {
	Service     string `env:"SERVICE" default:"api"`
	Region      string `env:"REGION"`
	Concurrency int    `env:"CONCURRENCY"`
	Endpoint    string `env:"ENDPOINT"`
}

// baseline is the same configuration expressed as VALUES rather than tags.
//
// FromStruct round-trips through JSON, so the json tags are what it matches
// on — which is also why it composes with nested structs for free.
type baseline struct {
	Region      string `json:"REGION"`
	Concurrency int    `json:"CONCURRENCY"`
	Endpoint    string `json:"ENDPOINT"`
}

// tiers maps a deployment tier to the settings that follow from it. This is
// the shape a `default:` tag cannot express: the value depends on something
// only known at runtime.
var tiers = map[string]baseline{
	"small": {Concurrency: 4, Endpoint: "https://small.internal"},
	"large": {Concurrency: 64, Endpoint: "https://large.internal"},
}

// derive builds the baseline from inputs discovered at startup — here a tier
// name and a region that would realistically come from instance metadata.
func derive(tier, region string) baseline {
	b := tiers[tier]
	b.Region = region
	return b
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
		cfgkit.FromStruct("baseline", derive("large", "us-east-1")),

		// Still last, still wins. A computed baseline is a source like any
		// other — it does not get special authority for being in Go.
		cfgkit.FromEnviron(),
	))
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(w, "%s in %s: concurrency=%d endpoint=%s\n",
		cfg.Service, cfg.Region, cfg.Concurrency, cfg.Endpoint)
	_, _ = fmt.Fprintln(w)
	return res.Explain(w)
}
