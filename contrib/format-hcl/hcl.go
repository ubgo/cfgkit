// Package hcl adds HCL documents as a cfgkit structured source.
//
// It is a separate module because it carries the HCL parser, which is
// substantial, and the core promises exactly one dependency.
//
// HCL IS STRUCTURED, unlike the INI and properties adapters: it has a type
// system and nested blocks, so it merges onto the destination struct through
// `hcl:` tags the way YAML and TOML do through theirs.
package hcl

import (
	"fmt"
	"os"

	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/ubgo/cfgkit"
)

// Source returns a structured source that merges an HCL document onto the
// configuration struct.
//
// Fields are matched by their `hcl:` tags, following gohcl's rules — which
// means a struct read from HCL needs those tags in addition to `env:`, because
// HCL's decoder is stricter than JSON's about what it will accept.
//
// filename is used in DIAGNOSTICS rather than read from disk. HCL reports errors
// with a line and column, and a name makes them navigable; passing "" produces
// messages that say only where in an unnamed buffer the problem is.
func Source(filename string, doc []byte) cfgkit.StructuredSource {
	return cfgkit.StructuredFunc(name(filename), func(dst any) error {
		return decode(filename, doc, dst)
	})
}

// File returns a structured source that reads an HCL file.
//
// A MISSING file is not an error: it is a normal, silent outcome, matching
// FromFiles in the core. A file that exists but cannot be read or parsed IS an
// error, because a broken configuration must never look like an absent one.
func File(path string) cfgkit.StructuredSource {
	return cfgkit.StructuredFunc(name(path), func(dst any) error {
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		return decode(path, b, dst)
	})
}

// name labels the source in provenance output.
func name(filename string) string {
	if filename == "" {
		return "hcl"
	}
	return "hcl:" + filename
}

// decode parses and merges in one step.
//
// HCL's diagnostics carry a position, and they are kept rather than flattened
// to a bare message: "config.hcl:4,3-8: Unsupported argument" is navigable,
// while "invalid HCL" sends a reader to read the whole file.
func decode(filename string, doc []byte, dst any) error {
	p := hclparse.NewParser()
	f, diags := p.ParseHCL(doc, filename)
	if diags.HasErrors() {
		return fmt.Errorf("parsing HCL: %w", diags)
	}
	if diags := gohcl.DecodeBody(f.Body, nil, dst); diags.HasErrors() {
		return fmt.Errorf("decoding HCL: %w", diags)
	}
	return nil
}
