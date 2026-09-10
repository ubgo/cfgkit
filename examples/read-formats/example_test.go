// Output is pinned so a format module that starts binding differently fails
// the gate instead of quietly disagreeing with the other six.
package main

import (
	"os"
	"reflect"
	"testing"

	"github.com/ubgo/cfgkit"
	tomlsrc "github.com/ubgo/cfgkit/contrib/format-toml"
	yamlsrc "github.com/ubgo/cfgkit/contrib/format-yaml"
	"github.com/ubgo/cfgkit/mock"
)

// Example_everyFormat is the cross-module claim, checked: seven formats, one
// struct, identical values.
func Example_everyFormat() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// FORMAT      SHAPE  BINDS THE CANONICAL CONFIG
	// yaml        nested true
	// toml        nested true
	// hcl         nested true
	// json        nested true
	// ini         flat   true
	// properties  flat   true
	// env         flat   true
	//
	// every format produced: service=checkout server=0.0.0.0:8080 db=postgres://db/checkout max_conns=25
}

// TestEveryFixtureBindsTheCanonicalValues is the assertion behind the table.
//
// It ranges over mock.Fixtures rather than a list written here, so adding a
// format to the fixture set automatically adds it to this proof — a list that
// had to be updated in two places is a list that ends up covering six of seven.
func TestEveryFixtureBindsTheCanonicalValues(t *testing.T) {
	want := mock.Want()

	for _, f := range mock.Fixtures {
		t.Run(f.Format, func(t *testing.T) {
			doc, err := mock.FS.ReadFile(f.File)
			if err != nil {
				t.Fatalf("reading the fixture: %v", err)
			}
			src, err := sourceFor(f, doc)
			if err != nil {
				t.Fatalf("no source: %v", err)
			}
			got, _, err := cfgkit.Load[mock.Config](cfgkit.WithSources(src))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !reflect.DeepEqual(*got, want) {
				t.Errorf("%s bound differently:\n got  %+v / %+v / %+v\n want %+v / %+v / %+v",
					f.Format, *got, got.Server, got.Database, want, want.Server, want.Database)
			}
		})
	}
}

// missingTags mirrors mock.Config's database block EXACTLY except for the
// yaml and toml tags. Isolating that one variable is the point: any other
// difference and the test would prove something else.
//
// It is the shape a developer writes first, having reasonably assumed that one
// structural tag covers every structured format.
type missingTagsDB struct {
	URL      string `json:"url" env:"url"`
	MaxConns int    `json:"max_conns" env:"max_conns"`
}

type missingTags struct {
	Database *missingTagsDB `json:"database" env:",prefix=database."`
}

// TestMultiWordKeyNeedsAPerFormatTag pins the trap that this fixture set found.
//
// yaml.v3 and BurntSushi/toml each read their OWN tag and fall back to the
// LOWERCASED Go field name — "maxconns", which the document does not contain.
// So a multi-word key binds as ZERO and nothing is reported: no error, no
// warning, just a connection pool of size 0 in production.
//
// Single-word fields hide this, because lowercasing happens to match. That is
// what makes it worth a test: the bug only appears once a field has two words,
// long after the pattern was set.
func TestMultiWordKeyNeedsAPerFormatTag(t *testing.T) {
	yamlDoc, err := mock.FS.ReadFile("config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	tomlDoc, err := mock.FS.ReadFile("config.toml")
	if err != nil {
		t.Fatal(err)
	}

	// src is `any` for the same reason sourceFor returns it: a StructuredSource
	// and a Source share no supertype, and WithSources takes ...any.
	for _, tc := range []struct {
		name string
		src  any
	}{
		{"yaml", yamlsrc.Source("config.yaml", yamlDoc)},
		{"toml", tomlsrc.Source("config.toml", tomlDoc)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := cfgkit.Load[missingTags](cfgkit.WithSources(tc.src))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got.Database == nil {
				t.Fatal("the database block did not bind at all")
			}
			// The single-word key still binds — which is exactly why the
			// multi-word failure goes unnoticed.
			if got.Database.URL != "postgres://db/checkout" {
				t.Errorf("URL = %q; the single-word key should still bind", got.Database.URL)
			}
			if got.Database.MaxConns != 0 {
				t.Errorf("MaxConns = %d; expected 0 — if this now binds, the "+
					"decoder changed its fallback and mock.Config's per-format "+
					"tags may no longer be necessary", got.Database.MaxConns)
			}
		})
	}
}

// TestCanonicalStructCarriesEveryVocabulary guards the fixture itself. If a
// field is added to mock.Config without the full tag set, the cross-format
// proof above would start passing for the wrong reason — the new field would
// simply be absent everywhere.
func TestCanonicalStructCarriesEveryVocabulary(t *testing.T) {
	want := mock.Want()
	if want.Server == nil || want.Database == nil {
		t.Fatal("mock.Want() has a nil block; the fixture set is incomplete")
	}
	if want.Database.MaxConns == 0 {
		t.Error("mock.Want() no longer exercises a multi-word key, which is " +
			"the case the per-format tags exist for")
	}
}
