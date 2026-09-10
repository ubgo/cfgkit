package properties_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/cfgkit"
	"github.com/ubgo/cfgkit/cfgkittest"
	propsrc "github.com/ubgo/cfgkit/contrib/format-properties"
)

// TestConformance runs the shared adapter suite. A .properties file is a FLAT
// source, so it takes the flat half — the same suite the built-in .env and
// environ sources pass.
func TestConformance(t *testing.T) {
	cfgkittest.RunSourceTests(t, func(kv map[string]string) cfgkit.Source {
		var b strings.Builder
		for k, v := range kv {
			b.WriteString(k + "=" + v + "\n")
		}
		return propsrc.Source("props", []byte(b.String()))
	})
}

type cfg struct {
	Port int    `env:"server.port" default:"8080"`
	Host string `env:"database.host" default:"localhost"`
}

// TestDottedKeysAreUsedAsWritten pins the mapping. Java's convention is dotted
// names, and they are used exactly as written — no case folding, no
// translation to SCREAMING_SNAKE — because the point of this adapter is that a
// Spring service and a Go service read the SAME file.
func TestDottedKeysAreUsedAsWritten(t *testing.T) {
	doc := "server.port=9000\ndatabase.host=db.internal\n"

	got, res, err := cfgkit.Load[cfg](cfgkit.WithSources(propsrc.Source("app.properties", []byte(doc))))
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 9000 || got.Host != "db.internal" {
		t.Errorf("cfg = %+v", got)
	}
	for _, f := range res.Fields() {
		if f.Path == "Host" && f.Source != "app.properties" {
			t.Errorf("Host source = %q", f.Source)
		}
	}
}

// TestJavaEscapesAreHonoured is the reason to use a parser rather than split on
// "=". Continuation lines and \u escapes are part of the format, and a naive
// reader silently mangles both.
func TestJavaEscapesAreHonoured(t *testing.T) {
	type esc struct {
		Long    string `env:"long.value"`
		Unicode string `env:"unicode.value"`
		Colon   string `env:"colon.separated"`
		Spaced  string `env:"spaced.key"`
	}
	doc := "long.value=one \\\n  two\n" +
		"unicode.value=caf\\u00e9\n" +
		"colon.separated: with-a-colon\n" +
		"spaced.key = trimmed\n"

	got, _, err := cfgkit.Load[esc](cfgkit.WithSources(propsrc.Source("app.properties", []byte(doc))))
	if err != nil {
		t.Fatal(err)
	}
	if got.Long != "one two" {
		t.Errorf("Long = %q, want the continuation line joined", got.Long)
	}
	if got.Unicode != "café" {
		t.Errorf("Unicode = %q, want the \\u escape decoded", got.Unicode)
	}
	if got.Colon != "with-a-colon" {
		t.Errorf("Colon = %q — a colon is a legal separator in this format", got.Colon)
	}
	if got.Spaced != "trimmed" {
		t.Errorf("Spaced = %q, want whitespace around the separator trimmed", got.Spaced)
	}
}

// TestExpansionIsDisabled pins a deliberate divergence from the format. The
// spec supports ${...}, but cfgkit resolves references at the .env layer where
// a whole chain of files is visible — and two expansion passes with different
// rules, depending on which source a value came from, is worse than one
// documented rule.
func TestExpansionIsDisabled(t *testing.T) {
	type raw struct {
		Base string `env:"base"`
		Full string `env:"full"`
	}
	doc := "base=/srv\nfull=${base}/app\n"

	got, _, err := cfgkit.Load[raw](cfgkit.WithSources(propsrc.Source("app.properties", []byte(doc))))
	if err != nil {
		t.Fatal(err)
	}
	if got.Full != "${base}/app" {
		t.Errorf("Full = %q, want the literal text — expansion belongs to the .env layer", got.Full)
	}
}

func TestEmptyValueIsPresentNotMissing(t *testing.T) {
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		propsrc.Source("app.properties", []byte("database.host=\n")),
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
	path := filepath.Join(dir, "application.properties")
	if err := os.WriteFile(path, []byte("database.host=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, res, err := cfgkit.Load[cfg](cfgkit.WithSources(propsrc.File(path)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "from-file" {
		t.Errorf("Host = %q", got.Host)
	}
	for _, f := range res.Fields() {
		if f.Path == "Host" && f.Source != "properties:"+path {
			t.Errorf("Host source = %q, want the path named", f.Source)
		}
	}
}

func TestFileAbsentIsSilent(t *testing.T) {
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		propsrc.File(filepath.Join(t.TempDir(), "nothing-here.properties")),
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
	asDir := filepath.Join(dir, "application.properties")
	if err := os.Mkdir(asDir, 0o700); err != nil {
		t.Fatal(err)
	}

	err := cfgkit.Check[cfg](cfgkit.WithSources(propsrc.File(asDir)))
	if err == nil {
		t.Fatal("want an error: the path exists but cannot be read")
	}
	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("want a *SourceError, got %T: %v", err, err)
	}
}

// TestMalformedDocumentIsAnError pins that a broken file fails the load rather
// than binding a partial document.
func TestMalformedDocumentIsAnError(t *testing.T) {
	// An invalid \u escape is the format's own way to be malformed.
	bad := []byte("key=caf\\uZZZZ\n")

	t.Run("from a document", func(t *testing.T) {
		if err := cfgkit.Check[cfg](cfgkit.WithSources(
			propsrc.Source("app.properties", bad),
		)); err == nil {
			t.Fatal("want a parse error")
		}
	})

	t.Run("from a file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "application.properties")
		if err := os.WriteFile(path, bad, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := cfgkit.Check[cfg](cfgkit.WithSources(propsrc.File(path))); err == nil {
			t.Fatal("want a parse error")
		}
	})
}

func TestKeysAreListedForUnknownDetection(t *testing.T) {
	_, res, err := cfgkit.Load[cfg](cfgkit.WithSources(
		propsrc.Source("app.properties", []byte("server.port=1\ndatabase.hsot=typo\n")),
	))
	if err != nil {
		t.Fatal(err)
	}
	u := res.Unknown()
	if len(u) != 1 || u[0].Key != "database.hsot" {
		t.Fatalf("Unknown() = %v, want just the typo", u)
	}
}

func TestPrecedenceAgainstOtherSources(t *testing.T) {
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		propsrc.Source("app.properties", []byte("server.port=9000\ndatabase.host=from-props\n")),
		cfgkit.FromMap(map[string]string{"server.port": "7000"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "from-props" {
		t.Errorf("Host = %q, want the file's value where nothing overrode it", got.Host)
	}
	if got.Port != 7000 {
		t.Errorf("Port = %d, want the later source to win", got.Port)
	}
}
