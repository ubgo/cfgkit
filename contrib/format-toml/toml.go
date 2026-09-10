// Package toml adds TOML documents as a cfgkit structured source.
//
// It is a separate module because it carries a TOML parser, and the core
// promises exactly one dependency. A program that never reads TOML never
// compiles this package and never sees BurntSushi/toml in its go.sum.
//
// TOML is worth an adapter rather than a note in the docs because its tables
// map onto nested Go structs the way a configuration tree already wants to be
// shaped, which is the same reason cfgkit models structured sources separately
// from flat ones at all.
package toml

import (
	"os"

	"github.com/BurntSushi/toml"
	"github.com/ubgo/cfgkit"
)

// Source returns a structured source that merges a TOML document onto the
// configuration struct.
//
// Fields are matched by their `toml:` tags, following the parser's own rules.
// Like every structured source it must leave absent fields untouched, which is
// what makes layering work — a document supplies what it mentions and nothing
// else. That overlay behaviour is asserted by the shared conformance suite.
func Source(name string, doc []byte) cfgkit.StructuredSource {
	return cfgkit.StructuredFunc(name, func(dst any) error {
		_, err := toml.Decode(string(doc), dst)
		return err
	})
}

// File returns a structured source that reads a TOML file.
//
// A MISSING file is not an error: it is a normal, silent outcome, matching
// FromFiles in the core. That is what lets a program ship with no config file
// at all and still run. A file that exists but cannot be read or parsed IS an
// error, because a broken configuration must never look like an absent one —
// that difference is a deploy proceeding on defaults it was never meant to use.
func File(path string) cfgkit.StructuredSource {
	return cfgkit.StructuredFunc("toml:"+path, func(dst any) error {
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		_, err = toml.Decode(string(b), dst)
		return err
	})
}
