package toml_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ubgo/cfgkit"
	"github.com/ubgo/cfgkit/cfgkittest"
	tomlsrc "github.com/ubgo/cfgkit/contrib/format-toml"
)

// TestConformance runs the shared adapter suite. Passing it means this module
// behaves exactly like the built-in sources — the whole reason the suite lives
// in cfgkittest rather than being rewritten per adapter.
func TestConformance(t *testing.T) {
	cfgkittest.RunStructuredTests(t, "value = \"from-doc\"\n", func(doc string) cfgkit.StructuredSource {
		return tomlsrc.Source("toml", []byte(doc))
	})
}

type cfg struct {
	Name string `toml:"name" env:"NAME" default:"default-name"`
	Port int    `toml:"port" env:"PORT" default:"8080"`
}

func TestSourceBinds(t *testing.T) {
	c, res, err := cfgkit.Load[cfg](cfgkit.WithSources(
		tomlsrc.Source("app.toml", []byte("name = \"from-toml\"\nport = 9000\n")),
	))
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "from-toml" || c.Port != 9000 {
		t.Errorf("cfg = %+v", c)
	}
	for _, f := range res.Fields() {
		if f.Path == "Name" && f.Source != "app.toml" {
			t.Errorf("Name source = %q, want the document named", f.Source)
		}
	}
}

// TestTablesBindToNestedStructs is the reason TOML is worth an adapter rather
// than a note. A table maps onto a nested struct the way a configuration tree
// already wants to be shaped, which is exactly what a structured source is for
// — and what a flat key/value source cannot express.
func TestTablesBindToNestedStructs(t *testing.T) {
	type database struct {
		Host string `toml:"host" env:"HOST" default:"localhost"`
		Port int    `toml:"port" env:"PORT" default:"5432"`
	}
	type app struct {
		Name     string   `toml:"name" env:"NAME" default:"app"`
		Database database `toml:"database" env:",prefix=DB_"`
	}

	doc := `
name = "billing"

[database]
host = "db.internal"
port = 6543
`
	got, _, err := cfgkit.Load[app](cfgkit.WithSources(tomlsrc.Source("app.toml", []byte(doc))))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "billing" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Database.Host != "db.internal" || got.Database.Port != 6543 {
		t.Errorf("Database = %+v, want the table bound to the nested struct", got.Database)
	}
}

// TestAbsentFieldsAreLeftAlone is the overlay property every structured source
// must have, and the one the whole layering depends on: a document supplies
// what it mentions and nothing else, so a later source can still win and an
// earlier value is not erased by silence.
func TestAbsentFieldsAreLeftAlone(t *testing.T) {
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		tomlsrc.Source("app.toml", []byte("name = \"only-name\"\n")),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 8080 {
		t.Errorf("Port = %d, want the default — the document did not mention it", got.Port)
	}
}

// TestNativeTypesBind pins that TOML's own scalar types arrive intact. Unlike a
// .env file, TOML has real integers, booleans and datetimes, so they must not
// be round-tripped through strings and re-parsed.
func TestNativeTypesBind(t *testing.T) {
	type typed struct {
		Port    int           `toml:"port" env:"PORT"`
		Debug   bool          `toml:"debug" env:"DEBUG"`
		Timeout time.Duration `toml:"timeout" env:"TIMEOUT"`
		Ratio   float64       `toml:"ratio" env:"RATIO"`
		Hosts   []string      `toml:"hosts" env:"HOSTS"`
	}

	doc := `
port = 9000
debug = true
timeout = "30s"
ratio = 1.5
hosts = ["a.test", "b.test"]
`
	got, _, err := cfgkit.Load[typed](cfgkit.WithSources(tomlsrc.Source("app.toml", []byte(doc))))
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 9000 || !got.Debug || got.Ratio != 1.5 {
		t.Errorf("cfg = %+v", got)
	}
	if got.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", got.Timeout)
	}
	if len(got.Hosts) != 2 || got.Hosts[0] != "a.test" {
		t.Errorf("Hosts = %v", got.Hosts)
	}
}

func TestFileBinds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("name = \"from-file\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, res, err := cfgkit.Load[cfg](cfgkit.WithSources(tomlsrc.File(path)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "from-file" {
		t.Errorf("Name = %q", got.Name)
	}
	for _, f := range res.Fields() {
		if f.Path == "Name" && f.Source != "toml:"+path {
			t.Errorf("Name source = %q, want the path named", f.Source)
		}
	}
}

// TestFileAbsentIsSilent and TestFileUnreadableIsAnError are one rule stated in
// two halves: an ABSENT file is normal, because shipping without a config file
// is normal, while a file that exists and cannot be read is an error. Without
// the second, a broken configuration would be indistinguishable from an absent
// one, and a deploy would proceed on defaults it was never meant to use.
func TestFileAbsentIsSilent(t *testing.T) {
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		tomlsrc.File(filepath.Join(t.TempDir(), "nothing-here.toml")),
	))
	if err != nil {
		t.Fatalf("a missing file must not be an error: %v", err)
	}
	if got.Name != "default-name" {
		t.Errorf("Name = %q, want the compiled-in default", got.Name)
	}
}

func TestFileUnreadableIsAnError(t *testing.T) {
	dir := t.TempDir()
	asDir := filepath.Join(dir, "config.toml")
	if err := os.Mkdir(asDir, 0o700); err != nil {
		t.Fatal(err)
	}

	err := cfgkit.Check[cfg](cfgkit.WithSources(tomlsrc.File(asDir)))
	if err == nil {
		t.Fatal("want an error: the path exists but cannot be read")
	}
	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("want a *SourceError, got %T: %v", err, err)
	}
}

// TestMalformedDocumentIsAnError pins that a syntax error fails the load rather
// than binding a partial document. Half a configuration is worse than none,
// because it looks like it worked.
func TestMalformedDocumentIsAnError(t *testing.T) {
	t.Run("from a document", func(t *testing.T) {
		err := cfgkit.Check[cfg](cfgkit.WithSources(
			tomlsrc.Source("app.toml", []byte("name = \"unterminated\nport = ")),
		))
		if err == nil {
			t.Fatal("want a parse error")
		}
	})

	t.Run("from a file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.toml")
		if err := os.WriteFile(path, []byte("this is not = = toml"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := cfgkit.Check[cfg](cfgkit.WithSources(tomlsrc.File(path)))
		if err == nil {
			t.Fatal("want a parse error")
		}
	})
}

// TestPrecedenceAgainstOtherSources pins that a TOML document composes like any
// other source: position in the list decides, and a flat source can still
// override a value the document set.
func TestPrecedenceAgainstOtherSources(t *testing.T) {
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		tomlsrc.Source("app.toml", []byte("name = \"from-toml\"\nport = 9000\n")),
		cfgkit.FromMap(map[string]string{"PORT": "7000"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "from-toml" {
		t.Errorf("Name = %q, want the document's value where nothing overrode it", got.Name)
	}
	if got.Port != 7000 {
		t.Errorf("Port = %d, want the later flat source to win", got.Port)
	}
}
