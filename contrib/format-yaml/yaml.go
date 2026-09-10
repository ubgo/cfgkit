// Package yaml adds YAML documents as a cfgkit structured source.
//
// It is a separate module for one reason: it carries a YAML parser, and the
// core promises exactly one dependency. A program that never reads YAML never
// compiles this package, never downloads its parser, and never sees it in
// go.sum.
//
// That is the whole rule for the catalogue — a source carrying a dependency
// gets its own module — and this package is its smallest possible illustration.
// The adapter itself is four lines; everything else here is documentation.
package yaml

import (
	"os"

	"github.com/ubgo/cfgkit"
	"gopkg.in/yaml.v3"
)

// Source returns a structured source that merges a YAML document onto the
// configuration struct.
//
// Fields are matched by their `yaml:` tags, following the parser's own rules.
// Like every structured source it must leave absent fields untouched, which
// yaml.v3 does natively — that overlay behaviour is what the layering depends
// on, and it is asserted by the shared conformance suite.
func Source(name string, doc []byte) cfgkit.StructuredSource {
	return cfgkit.StructuredFunc(name, func(dst any) error {
		return yaml.Unmarshal(doc, dst)
	})
}

// File returns a structured source that reads a YAML file.
//
// A MISSING file is not an error: it is a normal, silent outcome, matching
// FromFiles in the core. That is what lets a program ship with no config file
// at all and still run. A file that exists but cannot be read or parsed IS an
// error, because a broken config must never look like an absent one.
func File(path string) cfgkit.StructuredSource {
	return cfgkit.StructuredFunc("yaml:"+path, func(dst any) error {
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		return yaml.Unmarshal(b, dst)
	})
}
