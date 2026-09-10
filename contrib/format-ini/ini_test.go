package ini_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/cfgkit"
	"github.com/ubgo/cfgkit/cfgkittest"
	inisrc "github.com/ubgo/cfgkit/contrib/format-ini"
)

// TestConformance runs the shared adapter suite. INI is a FLAT source, so it
// takes the flat half — the same suite the built-in .env and environ sources
// pass.
func TestConformance(t *testing.T) {
	cfgkittest.RunSourceTests(t, func(kv map[string]string) cfgkit.Source {
		var b strings.Builder
		for k, v := range kv {
			b.WriteString(k + " = " + v + "\n")
		}
		return inisrc.Source("ini", []byte(b.String()))
	})
}

type cfg struct {
	Port int    `env:"port" default:"8080"`
	Host string `env:"database.host" default:"localhost"`
	User string `env:"database.user" default:"postgres"`
}

// TestSectionsBecomeDottedKeys pins the mapping, which is the one thing a
// reader must know to use this. A dot separates section from key because an INI
// key may itself contain underscores, and a separator that can appear inside a
// name makes the mapping ambiguous.
func TestSectionsBecomeDottedKeys(t *testing.T) {
	doc := `
port = 9000

[database]
host = db.internal
user = app
`
	got, res, err := cfgkit.Load[cfg](cfgkit.WithSources(inisrc.Source("app.ini", []byte(doc))))
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 9000 {
		t.Errorf("Port = %d — a key outside any section is addressed by its own name", got.Port)
	}
	if got.Host != "db.internal" || got.User != "app" {
		t.Errorf("cfg = %+v, want the section's keys as database.*", got)
	}
	for _, f := range res.Fields() {
		if f.Path == "Host" && f.Source != "app.ini" {
			t.Errorf("Host source = %q", f.Source)
		}
	}
}

// TestKeysAreCaseSensitive pins that names are used exactly as written. An INI
// file is written by an operator, and guessing at a canonical case would make a
// working file stop working.
func TestKeysAreCaseSensitive(t *testing.T) {
	type mixed struct {
		Upper string `env:"Section.Key" default:"unset"`
		Lower string `env:"section.key" default:"unset"`
	}
	doc := "[Section]\nKey = upper\n\n[section]\nkey = lower\n"

	got, _, err := cfgkit.Load[mixed](cfgkit.WithSources(inisrc.Source("app.ini", []byte(doc))))
	if err != nil {
		t.Fatal(err)
	}
	if got.Upper != "upper" || got.Lower != "lower" {
		t.Errorf("cfg = %+v, want both spellings addressable separately", got)
	}
}

func TestAbsentKeyLeavesTheDefault(t *testing.T) {
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		inisrc.Source("app.ini", []byte("port = 9000\n")),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "localhost" {
		t.Errorf("Host = %q, want the default — the file did not mention it", got.Host)
	}
}

func TestEmptyValueIsPresentNotMissing(t *testing.T) {
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		inisrc.Source("app.ini", []byte("[database]\nhost =\n")),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "" {
		t.Errorf("Host = %q, want empty — the key exists with no value, which beats the default", got.Host)
	}
}

func TestFileBinds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.ini")
	if err := os.WriteFile(path, []byte("[database]\nhost = from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, res, err := cfgkit.Load[cfg](cfgkit.WithSources(inisrc.File(path)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "from-file" {
		t.Errorf("Host = %q", got.Host)
	}
	for _, f := range res.Fields() {
		if f.Path == "Host" && f.Source != "ini:"+path {
			t.Errorf("Host source = %q, want the path named", f.Source)
		}
	}
}

// TestFileAbsentIsSilent and TestFileUnreadableIsAnError are one rule in two
// halves: shipping without a config file is normal, but a file that exists and
// cannot be read must not be indistinguishable from an absent one.
func TestFileAbsentIsSilent(t *testing.T) {
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		inisrc.File(filepath.Join(t.TempDir(), "nothing-here.ini")),
	))
	if err != nil {
		t.Fatalf("a missing file must not be an error: %v", err)
	}
	if got.Host != "localhost" {
		t.Errorf("Host = %q, want the compiled-in default", got.Host)
	}
}

func TestFileUnreadableIsAnError(t *testing.T) {
	dir := t.TempDir()
	asDir := filepath.Join(dir, "config.ini")
	if err := os.Mkdir(asDir, 0o700); err != nil {
		t.Fatal(err)
	}

	err := cfgkit.Check[cfg](cfgkit.WithSources(inisrc.File(asDir)))
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
		if err := cfgkit.Check[cfg](cfgkit.WithSources(
			inisrc.Source("app.ini", []byte("[unterminated\nhost = x\n")),
		)); err == nil {
			t.Fatal("want a parse error")
		}
	})

	t.Run("from a file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.ini")
		if err := os.WriteFile(path, []byte("[unterminated\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := cfgkit.Check[cfg](cfgkit.WithSources(inisrc.File(path))); err == nil {
			t.Fatal("want a parse error")
		}
	})
}

func TestKeysAreListedForUnknownDetection(t *testing.T) {
	_, res, err := cfgkit.Load[cfg](cfgkit.WithSources(
		inisrc.Source("app.ini", []byte("port = 1\n\n[database]\nhsot = typo\n")),
	))
	if err != nil {
		t.Fatal(err)
	}
	u := res.Unknown()
	if len(u) != 1 || u[0].Key != "database.hsot" {
		t.Fatalf("Unknown() = %v, want the typo with its section", u)
	}
}

func TestPrecedenceAgainstOtherSources(t *testing.T) {
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		inisrc.Source("app.ini", []byte("port = 9000\n\n[database]\nhost = from-ini\n")),
		cfgkit.FromMap(map[string]string{"port": "7000"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "from-ini" {
		t.Errorf("Host = %q, want the file's value where nothing overrode it", got.Host)
	}
	if got.Port != 7000 {
		t.Errorf("Port = %d, want the later source to win", got.Port)
	}
}
