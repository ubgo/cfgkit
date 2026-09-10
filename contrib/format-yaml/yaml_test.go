package yaml_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ubgo/cfgkit"
	"github.com/ubgo/cfgkit/cfgkittest"
	yamlsrc "github.com/ubgo/cfgkit/contrib/format-yaml"
)

// TestConformance runs the shared adapter suite. Passing it means this module
// behaves exactly like the built-in sources — the whole reason the suite lives
// in cfgkittest rather than being rewritten per adapter.
func TestConformance(t *testing.T) {
	cfgkittest.RunStructuredTests(t, "value: from-doc\n", func(doc string) cfgkit.StructuredSource {
		return yamlsrc.Source("yaml", []byte(doc))
	})
}

type cfg struct {
	Name string `yaml:"name" env:"NAME" default:"default-name"`
	Port int    `yaml:"port" env:"PORT" default:"8080"`
}

func TestFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("name: from-yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c, res, err := cfgkit.Load[cfg](cfgkit.WithSources(yamlsrc.File(path)))
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "from-yaml" {
		t.Errorf("Name = %q, want the file's value", c.Name)
	}
	if c.Port != 8080 {
		t.Errorf("Port = %d — a field the document omits must keep its default", c.Port)
	}
	for _, f := range res.Fields() {
		if f.Path == "Name" && f.Source == "default" {
			t.Error("the yaml source was not attributed as the origin")
		}
	}
}

// TestFileMissingIsNotAnError pins the rule shared with the core: an absent
// file is a normal outcome, which is what lets a program ship without one.
func TestFileMissingIsNotAnError(t *testing.T) {
	c, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		yamlsrc.File(filepath.Join(t.TempDir(), "absent.yaml")),
	))
	if err != nil {
		t.Fatalf("a missing file must not fail the load: %v", err)
	}
	if c.Name != "default-name" {
		t.Error("defaults must survive a missing file")
	}
}

// TestFileMalformedIsAnError pins the other half: a file that exists but cannot
// be parsed must NOT look like an absent one.
func TestFileMalformedIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("name: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cfgkit.Load[cfg](cfgkit.WithSources(yamlsrc.File(path))); err == nil {
		t.Error("a malformed document must fail the load")
	}
}

// TestFlatSourceOverridesYAML pins that the layering works across kinds: a
// later flat source beats an earlier structured one.
func TestFlatSourceOverridesYAML(t *testing.T) {
	c, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		yamlsrc.Source("yaml", []byte("name: from-yaml\nport: 1111\n")),
		cfgkit.FromMap(map[string]string{"PORT": "2222"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 2222 {
		t.Errorf("Port = %d, want the later flat source to win", c.Port)
	}
	if c.Name != "from-yaml" {
		t.Errorf("Name = %q — a key only the yaml sets must survive", c.Name)
	}
}

// TestFileUnreadableIsAnError pins the other half of the missing-file rule. An
// ABSENT file is silent, because shipping without a config file is normal. A
// file that exists and cannot be read is an error, because a broken config must
// never be indistinguishable from an absent one — that difference is a deploy
// proceeding on defaults it was never meant to use.
func TestFileUnreadableIsAnError(t *testing.T) {
	// A directory where a file is expected: present, and unreadable in a way
	// no parser can recover from.
	dir := t.TempDir()
	asDir := filepath.Join(dir, "config.yaml")
	if err := os.Mkdir(asDir, 0o700); err != nil {
		t.Fatal(err)
	}

	type cfg struct {
		Host string `env:"HOST" yaml:"host" default:"localhost"`
	}
	err := cfgkit.Check[cfg](cfgkit.WithSources(yamlsrc.File(asDir)))
	if err == nil {
		t.Fatal("want an error: the path exists but cannot be read")
	}

	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("want a *SourceError, got %T: %v", err, err)
	}
}

// TestFileAbsentIsSilent is the contrast, stated next to it so the pair reads
// as one rule rather than two behaviours.
func TestFileAbsentIsSilent(t *testing.T) {
	type cfg struct {
		Host string `env:"HOST" yaml:"host" default:"localhost"`
	}
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		yamlsrc.File(filepath.Join(t.TempDir(), "nothing-here.yaml")),
	))
	if err != nil {
		t.Fatalf("a missing file must not be an error: %v", err)
	}
	if got.Host != "localhost" {
		t.Errorf("Host = %q, want the compiled-in default", got.Host)
	}
}
