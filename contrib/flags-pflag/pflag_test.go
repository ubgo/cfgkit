package pflag_test

import (
	"testing"

	goflag "github.com/spf13/pflag"
	"github.com/ubgo/cfgkit"
	pflagsrc "github.com/ubgo/cfgkit/contrib/flags-pflag"
)

type cfg struct {
	Port   int    `env:"PORT" flag:"port" default:"8080"`
	Host   string `env:"HOST" flag:"host" default:"localhost"`
	NoFlag string `env:"NO_FLAG" default:"untouched"`
}

// TestFlagDefaultNeverWins is the test this whole module exists to pass.
//
// It reproduces the viper defect (GH-671, GH-375): a flag declared with a
// default but never typed must contribute NOTHING, so a config source still
// wins. Reading every flag instead of only the changed ones inverts precedence
// silently and kills the config file.
func TestFlagDefaultNeverWins(t *testing.T) {
	fs := goflag.NewFlagSet("app", goflag.ContinueOnError)
	fs.Int("port", 9999, "port") // never typed — must not apply
	fs.String("host", "flagdef", "host")
	if err := fs.Parse([]string{"--host", "typed.example"}); err != nil {
		t.Fatal(err)
	}

	c, res, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"PORT": "3000"}),
		pflagsrc.Source(fs), // highest precedence
	))
	if err != nil {
		t.Fatal(err)
	}

	if c.Port != 3000 {
		t.Errorf("Port = %d, want 3000 — an untyped flag's default must not win", c.Port)
	}
	if c.Host != "typed.example" {
		t.Errorf("Host = %q, want the typed flag value", c.Host)
	}

	for _, f := range res.Fields() {
		if f.Path == "Port" && f.Source == "pflag" {
			t.Error("Port must not be attributed to the flag source")
		}
		if f.Path == "Host" && f.Source != "pflag" {
			t.Errorf("Host source = %q, want pflag", f.Source)
		}
	}
}

// TestOptIn pins that a field without a flag tag ignores the flag set, so a
// config with 150 fields never becomes 150 flags.
func TestOptIn(t *testing.T) {
	fs := goflag.NewFlagSet("app", goflag.ContinueOnError)
	fs.String("no-flag", "", "")
	if err := fs.Parse([]string{"--no-flag", "hijacked"}); err != nil {
		t.Fatal(err)
	}

	c, _, err := cfgkit.Load[cfg](cfgkit.WithSources(pflagsrc.Source(fs)))
	if err != nil {
		t.Fatal(err)
	}
	if c.NoFlag != "untouched" {
		t.Errorf("NoFlag = %q — a field with no flag tag must ignore flags", c.NoFlag)
	}
}

// TestUnparsedSetContributesNothing pins the ordering gotcha: a set built
// before Parse holds nothing, and must fail closed rather than inventing values.
func TestUnparsedSetContributesNothing(t *testing.T) {
	fs := goflag.NewFlagSet("app", goflag.ContinueOnError)
	fs.Int("port", 9999, "port")
	// Deliberately NOT parsed — this is what a Load in init() would see.

	c, _, err := cfgkit.Load[cfg](cfgkit.WithSources(pflagsrc.Source(fs)))
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 8080 {
		t.Errorf("Port = %d, want the struct default — an unparsed set has no typed flags", c.Port)
	}
}
