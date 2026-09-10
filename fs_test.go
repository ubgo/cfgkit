// Tests for FromFS and FromStruct — the two sources koanf has and cfgkit did
// not, both added to the core because neither needs a dependency.
package cfgkit_test

import (
	"embed"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/cfgkit"
)

//go:embed testdata/embedded/*.env
var embedded embed.FS

type fsCfg struct {
	Host  string `env:"FS_HOST" json:"host" default:"localhost"`
	Port  int    `env:"FS_PORT" json:"port" default:"8080"`
	Debug bool   `env:"FS_DEBUG" json:"debug"`
}

// TestFromFSWithGoEmbed is the case this source exists for: a binary carrying
// its own defaults, overridable by a file or the environment, with nothing
// needing to exist on the target machine.
func TestFromFSWithGoEmbed(t *testing.T) {
	got, res, err := cfgkit.Load[fsCfg](cfgkit.WithSources(
		cfgkit.FromFS(embedded, "testdata/embedded/defaults.env"),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "embedded.internal" || got.Port != 9000 {
		t.Errorf("cfg = %+v", got)
	}
	for _, f := range res.Fields() {
		if f.Path == "Host" && !strings.HasPrefix(f.Source, "fs:") {
			t.Errorf("Host source = %q, want the fs source named", f.Source)
		}
	}
}

// TestFromFSLayersUnderRealSources pins the layering the doc comment promises:
// compiled-in defaults lose to a file, which loses to the environment.
func TestFromFSLayersUnderRealSources(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	if err := os.WriteFile(env, []byte("FS_PORT=7000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FS_DEBUG", "true")

	got, _, err := cfgkit.Load[fsCfg](cfgkit.WithSources(
		cfgkit.FromFS(embedded, "testdata/embedded/defaults.env"),
		cfgkit.FromFiles(env),
		cfgkit.FromEnviron(),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "embedded.internal" {
		t.Errorf("Host = %q, want the embedded value where nothing overrode it", got.Host)
	}
	if got.Port != 7000 {
		t.Errorf("Port = %d, want the file to beat the embed", got.Port)
	}
	if !got.Debug {
		t.Error("Debug = false, want the environment to beat both")
	}
}

// TestFromFSFollowsEveryFromFilesRule is the point of the shared implementation:
// the disk and embedded paths must not drift, because a difference between "the
// same config from a file" and "from an embed" is the kind of bug nobody thinks
// to look for.
func TestFromFSFollowsEveryFromFilesRule(t *testing.T) {
	t.Run("later paths win", func(t *testing.T) {
		fsys := fstest.MapFS{
			"a.env": {Data: []byte("FS_HOST=from-a\nFS_PORT=1\n")},
			"b.env": {Data: []byte("FS_HOST=from-b\n")},
		}
		got, _, err := cfgkit.Load[fsCfg](cfgkit.WithSources(cfgkit.FromFS(fsys, "a.env", "b.env")))
		if err != nil {
			t.Fatal(err)
		}
		if got.Host != "from-b" {
			t.Errorf("Host = %q, want the later path to win", got.Host)
		}
		if got.Port != 1 {
			t.Errorf("Port = %d, want the earlier path where the later is silent", got.Port)
		}
	})

	t.Run("a missing entry is silent", func(t *testing.T) {
		fsys := fstest.MapFS{"a.env": {Data: []byte("FS_HOST=present\n")}}
		got, _, err := cfgkit.Load[fsCfg](cfgkit.WithSources(
			cfgkit.FromFS(fsys, "a.env", "absent.env"),
		))
		if err != nil {
			t.Fatalf("a missing entry must not be an error: %v", err)
		}
		if got.Host != "present" {
			t.Errorf("Host = %q", got.Host)
		}
	})

	t.Run("${VAR} resolves, including across entries", func(t *testing.T) {
		fsys := fstest.MapFS{
			"base.env": {Data: []byte("HOSTNAME=db.internal\n")},
			"app.env":  {Data: []byte("FS_HOST=${HOSTNAME}\n")},
		}
		got, _, err := cfgkit.Load[fsCfg](cfgkit.WithSources(
			cfgkit.FromFS(fsys, "base.env", "app.env"),
		))
		if err != nil {
			t.Fatal(err)
		}
		if got.Host != "db.internal" {
			t.Errorf("Host = %q, want the reference resolved from the earlier entry", got.Host)
		}
	})

	t.Run("an unresolvable required reference fails the load", func(t *testing.T) {
		fsys := fstest.MapFS{"a.env": {Data: []byte("FS_HOST=${MUST_BE_SET:?set it}\n")}}
		err := cfgkit.Check[fsCfg](cfgkit.WithSources(cfgkit.FromFS(fsys, "a.env")))
		if err == nil {
			t.Fatal("want an error: the file itself declared the reference required")
		}
		if !strings.Contains(err.Error(), "MUST_BE_SET") {
			t.Errorf("error %q should name the unresolved reference", err)
		}
	})

	t.Run("keys are listed for typo detection", func(t *testing.T) {
		fsys := fstest.MapFS{"a.env": {Data: []byte("FS_HOST=x\nFS_HSOT=typo\n")}}
		_, res, err := cfgkit.Load[fsCfg](cfgkit.WithSources(cfgkit.FromFS(fsys, "a.env")))
		if err != nil {
			t.Fatal(err)
		}
		u := res.Unknown()
		if len(u) != 1 || u[0].Key != "FS_HSOT" {
			t.Fatalf("Unknown() = %v, want just the typo", u)
		}
	})
}

// TestFromFSUnreadableEntryIsAnError pins the other half of the absent rule: an
// entry that exists and cannot be read must not look like one that is absent.
func TestFromFSUnreadableEntryIsAnError(t *testing.T) {
	// A directory where a file is expected: present in the FS, unreadable as
	// a file.
	fsys := fstest.MapFS{
		"conf/a.env": {Data: []byte("FS_HOST=x\n")},
	}
	err := cfgkit.Check[fsCfg](cfgkit.WithSources(cfgkit.FromFS(fsys, "conf")))
	if err == nil {
		t.Fatal("want an error: the path exists but is not a readable file")
	}
	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("want a *SourceError, got %T: %v", err, err)
	}
}

// TestFromStructLayersAnAlreadyBuiltValue covers the case Defaults() cannot:
// values from somewhere the source list has no reach into, entering the chain
// at a chosen precedence.
func TestFromStructLayersAnAlreadyBuiltValue(t *testing.T) {
	fetched := fsCfg{Host: "from-service", Port: 9100}

	got, res, err := cfgkit.Load[fsCfg](cfgkit.WithSources(
		cfgkit.FromStruct("service", fetched),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "from-service" || got.Port != 9100 {
		t.Errorf("cfg = %+v", got)
	}
	for _, f := range res.Fields() {
		if f.Path == "Host" && f.Source != "service" {
			t.Errorf("Host source = %q, want the name given", f.Source)
		}
	}
}

// TestFromStructIsOverlaidNotAssigned pins that it behaves like every other
// structured source — a later flat source still wins.
func TestFromStructIsOverlaidNotAssigned(t *testing.T) {
	got, _, err := cfgkit.Load[fsCfg](cfgkit.WithSources(
		cfgkit.FromStruct("service", fsCfg{Host: "from-service", Port: 9100}),
		cfgkit.FromMap(map[string]string{"FS_PORT": "7000"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "from-service" {
		t.Errorf("Host = %q, want the struct's value where nothing overrode it", got.Host)
	}
	if got.Port != 7000 {
		t.Errorf("Port = %d, want the later source to win", got.Port)
	}
}

// TestFromStructNilIsSilent pins that an absent layer needs no nil check at the
// call site — "this layer has nothing to say" is a normal outcome.
func TestFromStructNilIsSilent(t *testing.T) {
	got, _, err := cfgkit.Load[fsCfg](cfgkit.WithSources(cfgkit.FromStruct("service", nil)))
	if err != nil {
		t.Fatalf("a nil value must not be an error: %v", err)
	}
	if got.Host != "localhost" {
		t.Errorf("Host = %q, want the compiled-in default", got.Host)
	}
}

// TestFromStructRejectsWhatJSONCannotEncode pins that an unencodable value is
// reported rather than silently contributing nothing.
func TestFromStructRejectsWhatJSONCannotEncode(t *testing.T) {
	err := cfgkit.Check[fsCfg](cfgkit.WithSources(
		cfgkit.FromStruct("bad", map[string]any{"fn": func() {}}),
	))
	if err == nil {
		t.Fatal("want an error for a value encoding/json cannot marshal")
	}
}
