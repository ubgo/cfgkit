// Package cfgkittest provides the harness every cfgkit adapter is tested with.
//
// It exists because of one rule: an adapter is tested against an INTERFACE with
// a fake, never against a live service. An adapter whose tests need a running
// Vault will not be run, and an untested adapter is worse than no adapter —
// it looks supported.
//
// The package also carries the conformance suite. A contrib module calls
// RunSourceTests and inherits every guarantee the core makes, so twenty
// adapters behave identically without twenty authors each remembering the
// rules.
package cfgkittest

import (
	"errors"
	"testing"

	"github.com/ubgo/cfgkit"
)

// Fake is the one-method client every adapter is written against. A real
// adapter wraps its vendor SDK behind this shape; its tests substitute a Fake
// and never open a socket.
type Fake struct {
	// Values are returned by Get, keyed exactly as the backend keys them.
	Values map[string]string
	// Err, when set, is returned by every Get. It proves the adapter reports a
	// backend failure rather than swallowing it as a miss.
	Err error
	// Calls records every key requested, so a test can assert the adapter
	// pre-loads rather than dialling once per field.
	Calls []string
}

// Get implements the client shape an adapter depends on.
func (f *Fake) Get(key string) (string, bool, error) {
	f.Calls = append(f.Calls, key)
	if f.Err != nil {
		return "", false, f.Err
	}
	v, ok := f.Values[key]
	return v, ok, nil
}

// probe is the struct the conformance suite binds. It stays deliberately small:
// the suite checks source behaviour, not the binder, which the core already
// covers.
type probe struct {
	Value   string `env:"CFGKITTEST_VALUE"`
	Untouch string `env:"CFGKITTEST_UNTOUCHED" default:"kept"`
}

// RunSourceTests asserts every guarantee a flat Source must make.
//
// newSource is called once per subtest with the values the source should serve;
// it returns the Source under test. An adapter that passes this suite behaves
// exactly like the built-in sources, which is the entire point of running it.
func RunSourceTests(t *testing.T, newSource func(values map[string]string) cfgkit.Source) {
	t.Helper()

	t.Run("supplies a value", func(t *testing.T) {
		cfg, res, err := cfgkit.Load[probe](cfgkit.WithSources(
			newSource(map[string]string{"CFGKITTEST_VALUE": "found"}),
		))
		if err != nil {
			t.Fatalf("load failed: %v", err)
		}
		if cfg.Value != "found" {
			t.Errorf("Value = %q, want %q", cfg.Value, "found")
		}
		// A source must name itself in the record, or Explain cannot answer
		// "where did this value come from".
		for _, f := range res.Fields() {
			if f.Path == "Value" && f.Source == "default" {
				t.Error("the source did not claim the value it supplied")
			}
		}
	})

	t.Run("a miss leaves the default alone", func(t *testing.T) {
		cfg, _, err := cfgkit.Load[probe](cfgkit.WithSources(newSource(nil)))
		if err != nil {
			t.Fatalf("an empty source must not fail the load: %v", err)
		}
		if cfg.Untouch != "kept" {
			t.Errorf("Untouch = %q — a miss must not overwrite a default", cfg.Untouch)
		}
	})

	t.Run("an empty value is a value", func(t *testing.T) {
		// "" is a real answer, distinct from "no opinion". A source that
		// conflates them silently resurrects a default the operator cleared.
		cfg, _, err := cfgkit.Load[probe](cfgkit.WithSources(
			newSource(map[string]string{"CFGKITTEST_UNTOUCHED": ""}),
		))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Untouch != "" {
			t.Errorf("Untouch = %q, want the empty value the source supplied", cfg.Untouch)
		}
	})
}

// RunFailureTest asserts the rule that separates a broken backend from an unset
// key: a source error must ABORT the load.
//
// An unreachable secret store that looks like a miss is a deploy proceeding
// with an empty password, so this is a correctness test, not a nicety.
func RunFailureTest(t *testing.T, failing cfgkit.Source) {
	t.Helper()

	_, _, err := cfgkit.Load[probe](cfgkit.WithSources(failing))
	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Errorf("a failing source must abort the load with a *cfgkit.SourceError, got: %v", err)
	}
}

// RunStructuredTests asserts every guarantee a StructuredSource must make.
//
// newSource receives a document in the adapter's own format and returns the
// source under test, so a YAML module passes YAML and a TOML module passes TOML.
// The document must set only CFGKITTEST_VALUE's field, named "value".
func RunStructuredTests(t *testing.T, doc string, newSource func(doc string) cfgkit.StructuredSource) {
	t.Helper()

	type sprobe struct {
		Value string `json:"value" yaml:"value" toml:"value"`
		Kept  string `json:"kept"  yaml:"kept"  toml:"kept"`
	}

	t.Run("merges the document", func(t *testing.T) {
		cfg, res, err := cfgkit.Load[sprobe](cfgkit.WithSources(newSource(doc)))
		if err != nil {
			t.Fatalf("load failed: %v", err)
		}
		if cfg.Value == "" {
			t.Error("the document's value did not reach the struct")
		}
		// Provenance for a structured source is recovered by observation, so a
		// source that changed a field must be named as its origin.
		for _, f := range res.Fields() {
			if f.Path == "Value" && f.Source == "default" {
				t.Error("the structured source was not attributed as the origin")
			}
		}
	})

	t.Run("leaves absent fields untouched", func(t *testing.T) {
		// This is the overlay semantic the whole layering depends on: a
		// document that mentions one field must not zero the others.
		cfg, _, err := cfgkit.Load[sprobe](cfgkit.WithSources(
			cfgkit.FromJSON([]byte(`{"kept":"before"}`)),
			newSource(doc),
		))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Kept != "before" {
			t.Errorf("Kept = %q — a structured source must not clear fields it does not mention", cfg.Kept)
		}
	})
}
