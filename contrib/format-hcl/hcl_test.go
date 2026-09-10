package hcl_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/cfgkit"
	hclsrc "github.com/ubgo/cfgkit/contrib/format-hcl"
)

// cfg needs BOTH tag sets: hcl: for the decoder and env: for the flat sources
// it composes with. gohcl is stricter than encoding/json — it will not guess a
// field name — so the tag is required rather than optional.
type cfg struct {
	Name string `hcl:"name,optional" env:"NAME" default:"app"`
	Port int    `hcl:"port,optional" env:"PORT" default:"8080"`
}

func TestSourceBinds(t *testing.T) {
	doc := `
name = "billing"
port = 9000
`
	got, res, err := cfgkit.Load[cfg](cfgkit.WithSources(hclsrc.Source("app.hcl", []byte(doc))))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "billing" || got.Port != 9000 {
		t.Errorf("cfg = %+v", got)
	}
	for _, f := range res.Fields() {
		if f.Path == "Name" && f.Source != "hcl:app.hcl" {
			t.Errorf("Name source = %q, want the document named", f.Source)
		}
	}
}

// TestAbsentFieldsAreLeftAlone is the overlay property every structured source
// must have: a document supplies what it mentions and nothing else, so a later
// source can still win and an earlier value is not erased by silence.
func TestAbsentFieldsAreLeftAlone(t *testing.T) {
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		hclsrc.Source("app.hcl", []byte(`name = "only-name"`)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 8080 {
		t.Errorf("Port = %d, want the default — the document did not mention it", got.Port)
	}
}

// TestNestedBlocksBind pins what HCL is actually for. Blocks are its reason to
// exist, and they map onto nested structs the way a config tree wants to be
// shaped — which is why this is a structured source and INI is not.
func TestNestedBlocksBind(t *testing.T) {
	type database struct {
		Host string `hcl:"host,optional" env:"HOST" default:"localhost"`
		Port int    `hcl:"port,optional" env:"PORT" default:"5432"`
	}
	type app struct {
		Name     string    `hcl:"name,optional" env:"NAME" default:"app"`
		Database *database `hcl:"database,block" env:",prefix=DB_"`
	}

	doc := `
name = "billing"

database {
  host = "db.internal"
  port = 6543
}
`
	got, _, err := cfgkit.Load[app](cfgkit.WithSources(hclsrc.Source("app.hcl", []byte(doc))))
	if err != nil {
		t.Fatal(err)
	}
	if got.Database == nil {
		t.Fatal("Database = nil, but the document declared the block")
	}
	if got.Database.Host != "db.internal" || got.Database.Port != 6543 {
		t.Errorf("Database = %+v", got.Database)
	}
}

// TestNativeTypesBind pins that HCL's own scalars arrive without a string round
// trip — the same property TOML has and a .env file cannot.
func TestNativeTypesBind(t *testing.T) {
	type typed struct {
		Port  int      `hcl:"port,optional" env:"PORT"`
		Debug bool     `hcl:"debug,optional" env:"DEBUG"`
		Ratio float64  `hcl:"ratio,optional" env:"RATIO"`
		Hosts []string `hcl:"hosts,optional" env:"HOSTS"`
	}
	doc := `
port  = 9000
debug = true
ratio = 1.5
hosts = ["a.test", "b.test"]
`
	got, _, err := cfgkit.Load[typed](cfgkit.WithSources(hclsrc.Source("app.hcl", []byte(doc))))
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 9000 || !got.Debug || got.Ratio != 1.5 {
		t.Errorf("cfg = %+v", got)
	}
	if len(got.Hosts) != 2 || got.Hosts[0] != "a.test" {
		t.Errorf("Hosts = %v", got.Hosts)
	}
}

func TestFileBinds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.hcl")
	if err := os.WriteFile(path, []byte(`name = "from-file"`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, res, err := cfgkit.Load[cfg](cfgkit.WithSources(hclsrc.File(path)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "from-file" {
		t.Errorf("Name = %q", got.Name)
	}
	for _, f := range res.Fields() {
		if f.Path == "Name" && f.Source != "hcl:"+path {
			t.Errorf("Name source = %q, want the path named", f.Source)
		}
	}
}

func TestFileAbsentIsSilent(t *testing.T) {
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		hclsrc.File(filepath.Join(t.TempDir(), "nothing-here.hcl")),
	))
	if err != nil {
		t.Fatalf("a missing file must not be an error: %v", err)
	}
	if got.Name != "app" {
		t.Errorf("Name = %q, want the compiled-in default", got.Name)
	}
}

func TestFileUnreadableIsAnError(t *testing.T) {
	dir := t.TempDir()
	asDir := filepath.Join(dir, "config.hcl")
	if err := os.Mkdir(asDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := cfgkit.Check[cfg](cfgkit.WithSources(hclsrc.File(asDir))); err == nil {
		t.Fatal("want an error: the path exists but cannot be read")
	}
}

// TestDiagnosticsKeepTheirPosition is the reason to keep HCL's own error type
// rather than flatten it to a message. "config.hcl:2,1-5" is navigable;
// "invalid HCL" sends a reader to read the whole file.
func TestDiagnosticsKeepTheirPosition(t *testing.T) {
	t.Run("a parse error", func(t *testing.T) {
		err := cfgkit.Check[cfg](cfgkit.WithSources(
			hclsrc.Source("config.hcl", []byte("name = \n")),
		))
		if err == nil {
			t.Fatal("want a parse error")
		}
		if !strings.Contains(err.Error(), "config.hcl") {
			t.Errorf("error %q should name the file", err)
		}
		if !strings.Contains(err.Error(), "parsing HCL") {
			t.Errorf("error %q should say which stage failed", err)
		}
	})

	t.Run("a decode error", func(t *testing.T) {
		// Parses fine; the argument does not exist on the struct. gohcl is
		// strict about that, which is a feature — a typo'd argument in an HCL
		// file is a mistake, not something to ignore.
		err := cfgkit.Check[cfg](cfgkit.WithSources(
			hclsrc.Source("config.hcl", []byte("nmae = \"typo\"\n")),
		))
		if err == nil {
			t.Fatal("want a decode error for an argument that matches no field")
		}
		if !strings.Contains(err.Error(), "decoding HCL") {
			t.Errorf("error %q should say which stage failed", err)
		}
	})

	t.Run("from a file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "broken.hcl")
		if err := os.WriteFile(path, []byte("name = \n"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := cfgkit.Check[cfg](cfgkit.WithSources(hclsrc.File(path)))
		if err == nil {
			t.Fatal("want a parse error")
		}
		if !strings.Contains(err.Error(), "broken.hcl") {
			t.Errorf("error %q should name the file", err)
		}
	})
}

// TestUnnamedDocumentStillWorks pins the empty-filename case, which produces a
// usable but position-only diagnostic.
func TestUnnamedDocumentStillWorks(t *testing.T) {
	got, res, err := cfgkit.Load[cfg](cfgkit.WithSources(
		hclsrc.Source("", []byte(`name = "unnamed"`)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "unnamed" {
		t.Errorf("Name = %q", got.Name)
	}
	for _, f := range res.Fields() {
		if f.Path == "Name" && f.Source != "hcl" {
			t.Errorf("source = %q, want the bare label when no filename is given", f.Source)
		}
	}
}

func TestPrecedenceAgainstOtherSources(t *testing.T) {
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		hclsrc.Source("app.hcl", []byte("name = \"from-hcl\"\nport = 9000\n")),
		cfgkit.FromMap(map[string]string{"PORT": "7000"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "from-hcl" {
		t.Errorf("Name = %q, want the document's value where nothing overrode it", got.Name)
	}
	if got.Port != 7000 {
		t.Errorf("Port = %d, want the later flat source to win", got.Port)
	}
}
