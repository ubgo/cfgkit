// Fuzzing for the format-hcl parser.
package hcl_test

import (
	"testing"

	"github.com/ubgo/cfgkit"
	hclsrc "github.com/ubgo/cfgkit/contrib/format-hcl"
)

// FuzzHCLNeverPanics feeds arbitrary bytes to the parser.
//
// A configuration document is attacker-adjacent in more deployments than
// people assume: it arrives from a mounted volume, an S3 object, a config
// server, or a repository anybody can open a pull request against. The parser
// must therefore either bind or return an error — never panic, and never hang.
//
// Errors are the expected outcome for almost every input. Only a crash is a
// finding.
func FuzzHCLNeverPanics(f *testing.F) {
	for _, seed := range []string{
		"",
		"name = \"x\"",
		"nested {\n  inner = \"v\"\n}",
		"{",
		"a =",
		"name = name",
		"port = 1e999",
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, doc []byte) {
		type nested struct {
			Inner string `json:"inner" yaml:"inner" toml:"inner" hcl:"inner,optional" env:"INNER"`
		}
		type cfg struct {
			Name   string  `json:"name" yaml:"name" toml:"name" hcl:"name,optional" env:"NAME"`
			Port   int     `json:"port" yaml:"port" toml:"port" hcl:"port,optional" env:"PORT"`
			Ratio  float64 `json:"ratio" yaml:"ratio" toml:"ratio" hcl:"ratio,optional" env:"RATIO"`
			On     bool    `json:"on" yaml:"on" toml:"on" hcl:"on,optional" env:"ON"`
			Nested *nested `json:"nested" yaml:"nested" toml:"nested" hcl:"nested,block" env:",prefix=nested."`
		}

		_, _, _ = cfgkit.Load[cfg](cfgkit.WithSources(hclsrc.Source("fuzz.hcl", doc)))
	})
}
