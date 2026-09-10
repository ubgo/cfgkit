// Tests for the fixture set's own consistency.
//
// The cross-format proof lives in examples/read-formats, which is where the
// parsers may be imported. What CANNOT live there is a check that the fixture
// set is complete: a format added to the directory but left out of Fixtures
// would simply not be proved, and the proof would still pass — reporting
// success for six formats while claiming seven.
//
// These tests are the guard against that drift, and they belong here because
// this is the package that owns the invariant.
package mock_test

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/cfgkit/mock"
)

// TestEveryFixtureIsEmbedded pins that each entry in Fixtures names a file the
// embed directive actually captured.
//
// A typo'd filename, or a file added to Fixtures but not to //go:embed, fails
// here rather than at the point some other package tries to read it.
func TestEveryFixtureIsEmbedded(t *testing.T) {
	for _, f := range mock.Fixtures {
		t.Run(f.Format, func(t *testing.T) {
			b, err := mock.FS.ReadFile(f.File)
			if err != nil {
				t.Fatalf("fixture %q is listed but not embedded: %v", f.File, err)
			}
			if len(b) == 0 {
				t.Errorf("fixture %q is embedded but empty", f.File)
			}
		})
	}
}

// TestEveryEmbeddedFileIsListed is the mirror, and the one that catches real
// drift: a config file added to the directory and to //go:embed but forgotten
// in Fixtures.
//
// Nothing would fail without this. The cross-format proof ranges over
// Fixtures, so the new format would silently never be proved — the worst kind
// of gap, because the suite stays green while covering less than it says.
func TestEveryEmbeddedFileIsListed(t *testing.T) {
	listed := make(map[string]bool, len(mock.Fixtures))
	for _, f := range mock.Fixtures {
		listed[f.File] = true
	}

	err := fs.WalkDir(mock.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasPrefix(filepath.Base(path), "config.") {
			return nil
		}
		if !listed[path] {
			t.Errorf("%s is embedded but missing from Fixtures, so no test proves it binds", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the embedded fixtures: %v", err)
	}
}

// TestFixtureFormatsAreUnique pins that no format is listed twice. A duplicate
// would make the cross-format proof run one format twice and report a higher
// count than it earned.
func TestFixtureFormatsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range mock.Fixtures {
		if seen[f.Format] {
			t.Errorf("format %q appears twice in Fixtures", f.Format)
		}
		seen[f.Format] = true
	}
}

// TestWantIsFullyPopulated pins that the expected value exercises everything
// the fixture set exists to check.
//
// If Want() ever lost its nested blocks, every format would still "agree" —
// on a value with nothing in it. The proof would pass while proving nothing,
// which is precisely the failure this fixture set was built to expose.
func TestWantIsFullyPopulated(t *testing.T) {
	want := mock.Want()

	if want.Service == "" {
		t.Error("Want().Service is empty; the top-level scalar is not exercised")
	}
	if want.Server == nil || want.Database == nil {
		t.Fatal("Want() has a nil block; nesting is not exercised")
	}
	if want.Server.Port == 0 || want.Server.Host == "" {
		t.Error("Want().Server is not fully populated")
	}
	if want.Database.URL == "" {
		t.Error("Want().Database.URL is empty")
	}
	// The multi-word key is the whole reason the per-format tags exist.
	if want.Database.MaxConns == 0 {
		t.Error("Want() no longer exercises a multi-word key, which is the case " +
			"the yaml/toml tags were added for")
	}
}

// TestWantIsIndependentPerCall pins that Want returns a fresh value.
//
// It hands back pointers to nested blocks. If it returned a shared instance, a
// test that mutated one would corrupt every later test in the same run — the
// kind of cross-test coupling that produces failures depending on -run order.
func TestWantIsIndependentPerCall(t *testing.T) {
	a, b := mock.Want(), mock.Want()

	if a.Server == b.Server {
		t.Fatal("Want() returns a shared Server pointer; one test could corrupt another")
	}
	a.Server.Port = 1
	if b.Server.Port == 1 {
		t.Error("mutating one Want() affected another")
	}
}

// TestFlatFormatsAreMarked pins the Flat flag against the formats that
// genuinely have no nesting. It is what tells a reader why those files use
// dotted keys, and a wrong flag would make the fixture set's documentation
// disagree with its own data.
func TestFlatFormatsAreMarked(t *testing.T) {
	wantFlat := map[string]bool{
		"yaml": false, "toml": false, "hcl": false, "json": false,
		"ini": true, "properties": true, "env": true,
	}

	for _, f := range mock.Fixtures {
		want, known := wantFlat[f.Format]
		if !known {
			t.Errorf("format %q is not classified here; add it so the flag stays checked", f.Format)
			continue
		}
		if f.Flat != want {
			t.Errorf("format %q has Flat=%t, want %t", f.Format, f.Flat, want)
		}
	}
}

// TestEveryFixtureMentionsTheCanonicalValues is a cheap textual guard.
//
// It cannot parse — that needs the format modules, which this module may not
// import — but a fixture that no longer contains the service name or the port
// has drifted from the others, and catching that here is better than watching
// the cross-format proof fail with a diff nobody can read.
func TestEveryFixtureMentionsTheCanonicalValues(t *testing.T) {
	want := mock.Want()

	for _, f := range mock.Fixtures {
		t.Run(f.Format, func(t *testing.T) {
			b, err := mock.FS.ReadFile(f.File)
			if err != nil {
				t.Fatal(err)
			}
			doc := string(b)

			for _, needle := range []string{
				want.Service,
				want.Database.URL,
				"8080",      // Server.Port
				"max_conns", // the multi-word key, spelled the same everywhere
			} {
				if !strings.Contains(doc, needle) {
					t.Errorf("%s does not mention %q; it has drifted from the canonical config",
						f.File, needle)
				}
			}
		})
	}
}
