package cfgkit_test

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/cfgkit"
)

// Level is a user-defined type reached only through encoding.TextUnmarshaler —
// the single escape hatch. It proves the hatch works without cfgkit knowing the
// type exists.
type Level string

func (l *Level) UnmarshalText(b []byte) error {
	switch s := string(b); s {
	case "debug", "info", "warn", "error":
		*l = Level(s)
		return nil
	default:
		return fmt.Errorf("unknown level %q", s)
	}
}

type Exotic struct {
	Level Level    `env:"LOG_LEVEL"`
	Site  *url.URL `env:"SITE_URL"`
	Ratio *float64 `env:"RATIO"`
}

// TestTextUnmarshalerHatch pins that any type satisfying the stdlib interface
// binds without cfgkit special-casing it. This is what keeps the supported type
// set closed and small.
func TestTextUnmarshalerHatch(t *testing.T) {
	cfg, _, err := cfgkit.Load[Exotic](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
		"LOG_LEVEL": "warn",
		"SITE_URL":  "https://example.test/path",
		"RATIO":     "0.25",
	})))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Level != "warn" {
		t.Errorf("Level = %q", cfg.Level)
	}
	if cfg.Site == nil || cfg.Site.Host != "example.test" {
		t.Errorf("Site = %#v", cfg.Site)
	}
	if cfg.Ratio == nil || *cfg.Ratio != 0.25 {
		t.Errorf("Ratio = %v — a pointer to a scalar must allocate and decode", cfg.Ratio)
	}
}

// TestTextUnmarshalerError pins that the user type's own error reaches the
// caller rather than being replaced by a generic message.
func TestTextUnmarshalerError(t *testing.T) {
	_, _, err := cfgkit.Load[Exotic](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"LOG_LEVEL": "loud"}),
	))
	if err == nil || !strings.Contains(err.Error(), `unknown level "loud"`) {
		t.Errorf("want the type's own error, got: %v", err)
	}
}

type Required struct {
	Token string `env:"API_TOKEN,required"`
	Name  string `env:"NAME,notempty"`
}

// TestRequiredVsNotEmpty pins that a missing key and a declared-but-empty value
// are DIFFERENT failures. They have different causes and different fixes: one
// needs a new line in the deployment, the other needs a value in a line that
// already exists.
func TestRequiredVsNotEmpty(t *testing.T) {
	t.Run("missing key", func(t *testing.T) {
		_, _, err := cfgkit.Load[Required](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"NAME": "x"}),
		))
		var re *cfgkit.RequiredError
		if !errors.As(err, &re) || re.Empty {
			t.Errorf("want a missing-key RequiredError, got %v", err)
		}
		if !strings.Contains(err.Error(), "no source supplied it") {
			t.Errorf("message must distinguish missing from empty: %v", err)
		}
	})

	t.Run("declared but empty", func(t *testing.T) {
		_, _, err := cfgkit.Load[Required](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"API_TOKEN": "t", "NAME": ""}),
		))
		var re *cfgkit.RequiredError
		if !errors.As(err, &re) || !re.Empty {
			t.Errorf("want an empty-value RequiredError, got %v", err)
		}
		if !strings.Contains(err.Error(), "empty value") {
			t.Errorf("message must say the value is empty: %v", err)
		}
	})
}

type FileSecret struct {
	Password string `env:"DB_PASSWORD_FILE,file" secret:"true"`
}

// TestFileValue pins the Docker and Kubernetes secret convention: the variable
// holds a PATH, and the value is the file's contents. A mounted secret never
// appears in docker inspect or in a child process this way.
func TestFileValue(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pw")
	// Every tool that writes these files adds a trailing newline.
	if err := os.WriteFile(p, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := cfgkit.Load[FileSecret](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"DB_PASSWORD_FILE": p}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Password != "s3cret" {
		t.Errorf("Password = %q — the trailing newline must be trimmed", cfg.Password)
	}
}

// TestFileValueUnreadable pins that an existing-but-unreadable path aborts.
// An unreadable secret must never be indistinguishable from an unset one.
func TestFileValueUnreadable(t *testing.T) {
	_, _, err := cfgkit.Load[FileSecret](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"DB_PASSWORD_FILE": filepath.Join(t.TempDir(), "absent")}),
	))
	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Errorf("want a SourceError for an unreadable path, got %v", err)
	}
}

type UnsetMe struct {
	Token string `env:"ONE_SHOT_TOKEN,unset" secret:"true"`
}

// TestUnsetAfterRead pins the one documented exception to "Load never writes to
// os.Environ": a field tagged unset removes its OWN key, so a child process
// started later cannot inherit the secret.
func TestUnsetAfterRead(t *testing.T) {
	t.Setenv("ONE_SHOT_TOKEN", "abc")
	t.Setenv("KEEP_ME", "still-here")

	cfg, _, err := cfgkit.Load[UnsetMe](cfgkit.WithSources(cfgkit.FromEnviron()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "abc" {
		t.Fatalf("value must be read before the key is removed, got %q", cfg.Token)
	}
	if _, ok := os.LookupEnv("ONE_SHOT_TOKEN"); ok {
		t.Error("the tagged key must be gone from the environment")
	}
	if _, ok := os.LookupEnv("KEEP_ME"); !ok {
		t.Error("unset must touch only its own key")
	}
}

type Renamed struct {
	APIKey string `env:"HYPERDX_API_KEY" was:"HYPERDX_KEY" secret:"true"`
}

// TestFormerKeyName pins that a renamed key keeps old deployments working, and
// that the provenance says the old name was used so an operator knows to
// migrate.
func TestFormerKeyName(t *testing.T) {
	t.Run("old name still works", func(t *testing.T) {
		cfg, res, err := cfgkit.Load[Renamed](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"HYPERDX_KEY": "old"}),
		))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.APIKey != "old" {
			t.Errorf("APIKey = %q, want the value from the former key", cfg.APIKey)
		}
		var origin string
		for _, f := range res.Fields() {
			if f.Path == "APIKey" {
				origin = f.Source
			}
		}
		if !strings.Contains(origin, "deprecated") {
			t.Errorf("origin = %q — it must flag the deprecated key", origin)
		}
	})

	t.Run("current name wins", func(t *testing.T) {
		cfg, _, err := cfgkit.Load[Renamed](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"HYPERDX_KEY": "old", "HYPERDX_API_KEY": "new"}),
		))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.APIKey != "new" {
			t.Errorf("APIKey = %q — the current key must win over the former one", cfg.APIKey)
		}
	})
}

// Prefixed proves that a prefix declared once on a parent composes down the
// tree, so a field names only its own suffix.
type Prefixed struct {
	HyperDX HyperDX `env:",prefix=HYPERDX_" json:"hyperdx"`
}

type HyperDX struct {
	LogsSourceID string `env:"LOGS_SOURCE_ID" json:"logsSourceId"`
	APIKey       string `env:"API_KEY" json:"apiKey" secret:"true"`
}

// TestPrefixComposition pins the answer to the HYPERDX_LOGS_SOURCE_ID problem:
// the key is DECLARED, never inferred by splitting on "_", so the struct shape
// and the key name stay independent.
func TestPrefixComposition(t *testing.T) {
	cfg, res, err := cfgkit.Load[Prefixed](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
		"HYPERDX_LOGS_SOURCE_ID": "abc123",
		"HYPERDX_API_KEY":        "k",
	})))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HyperDX.LogsSourceID != "abc123" {
		t.Errorf("LogsSourceID = %q", cfg.HyperDX.LogsSourceID)
	}
	// The recorded key must be the full one an operator sets.
	for _, f := range res.Fields() {
		if f.Path == "HyperDX.LogsSourceID" && f.Key != "HYPERDX_LOGS_SOURCE_ID" {
			t.Errorf("recorded key = %q, want the full prefixed key", f.Key)
		}
	}
}

// TestStructuredSource pins that nested data merges by shape through the json
// tags, leaves absent fields untouched, and is overridden by a later flat
// source.
func TestStructuredSource(t *testing.T) {
	blob := []byte(`{"hyperdx":{"logsSourceId":"from-json"}}`)

	cfg, _, err := cfgkit.Load[Prefixed](cfgkit.WithSources(
		cfgkit.FromJSON(blob),
		cfgkit.FromMap(map[string]string{"HYPERDX_API_KEY": "from-env"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HyperDX.LogsSourceID != "from-json" {
		t.Errorf("LogsSourceID = %q — the structured source must fill it", cfg.HyperDX.LogsSourceID)
	}
	if cfg.HyperDX.APIKey != "from-env" {
		t.Errorf("APIKey = %q — a flat source must still bind alongside", cfg.HyperDX.APIKey)
	}
}

// TestStructuredSourceError pins that a failing Apply aborts the load rather
// than producing a half-merged configuration.
func TestStructuredSourceError(t *testing.T) {
	_, _, err := cfgkit.Load[Prefixed](cfgkit.WithSources(
		cfgkit.StructuredFunc("broken", func(any) error { return errors.New("bad json") }),
	))
	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Errorf("want a SourceError, got %v", err)
	}
}

// TestSourceErrorIsNotAMiss pins the rule that separates an unreachable store
// from an unset variable — the difference between a failed deploy and a deploy
// running with an empty password.
func TestSourceErrorIsNotAMiss(t *testing.T) {
	boom := cfgkit.SourceFunc("vault", func(string) (string, bool, error) {
		return "", false, errors.New("connection refused")
	})
	_, _, err := cfgkit.Load[Required](cfgkit.WithSources(boom))
	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Errorf("a source failure must abort, not fall through as a miss: %v", err)
	}
}

// Derived exercises the computed-value hook.
type Derived struct {
	Host string `env:"DB_HOST"`
	Port int    `env:"DB_PORT"`
	DSN  string `env:"-"`
}

func (d *Derived) Defaults() { d.Host, d.Port = "localhost", 5432 }

func (d *Derived) Derive() error {
	if d.DSN == "" {
		d.DSN = fmt.Sprintf("postgres://%s:%d/app", d.Host, d.Port)
	}
	return nil
}

func (d *Derived) Validate() error {
	if d.Port < 1 || d.Port > 65535 {
		return fmt.Errorf("port %d out of range", d.Port)
	}
	return nil
}

// TestDeriveAndValidate pins the ordering: Derive sees bound values, Validate
// sees derived ones, and a field tagged "-" is never bound from any source.
func TestDeriveAndValidate(t *testing.T) {
	cfg, _, err := cfgkit.Load[Derived](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"DB_HOST": "db.internal", "DB_PORT": "6543", "DSN": "ignored"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	want := "postgres://db.internal:6543/app"
	if cfg.DSN != want {
		t.Errorf("DSN = %q, want %q", cfg.DSN, want)
	}

	if _, _, err := cfgkit.Load[Derived](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"DB_PORT": "70000"}),
	)); err == nil {
		t.Error("Validate must reject an out-of-range port")
	}
}

// TestCheck pins that Check runs the full pipeline without handing back a
// configuration — the CI gate.
func TestCheck(t *testing.T) {
	if err := cfgkit.Check[App](); err != nil {
		t.Errorf("Check on a fully defaulted config must pass: %v", err)
	}
	if err := cfgkit.Check[Required](); err == nil {
		t.Error("Check must report a missing required key")
	}
}

// TestModeResolution pins that the mode comes from the environment when it is
// not passed, and defaults to dev.
func TestModeResolution(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want cfgkit.Mode
	}{
		{"", cfgkit.ModeDev},
		{"production", cfgkit.ModeProd},
		{"prod", cfgkit.ModeProd},
		{"test", cfgkit.ModeTest},
		{"nonsense", cfgkit.ModeDev},
	} {
		t.Setenv("APP_ENV", tc.env)
		_, res, err := cfgkit.Load[App]()
		if err != nil {
			t.Fatal(err)
		}
		if res.Mode() != tc.want {
			t.Errorf("APP_ENV=%q → mode %q, want %q", tc.env, res.Mode(), tc.want)
		}
	}

	t.Setenv("MY_ENV", "prod")
	_, res, err := cfgkit.Load[App](cfgkit.WithModeKey("MY_ENV"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode() != cfgkit.ModeProd {
		t.Errorf("WithModeKey ignored: %q", res.Mode())
	}

	_, res, err = cfgkit.Load[App](cfgkit.WithMode(cfgkit.ModeTest))
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode() != cfgkit.ModeTest {
		t.Errorf("WithMode ignored: %q", res.Mode())
	}
}

// TestPrefixedEnviron pins the namespacing source.
func TestPrefixedEnviron(t *testing.T) {
	t.Setenv("SVCA_PORT", "9090")
	cfg, _, err := cfgkit.Load[App](cfgkit.WithSources(cfgkit.FromPrefixedEnviron("SVCA_")))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("Port = %d, want the prefixed value", cfg.Server.Port)
	}
}

// TestLoadRejectsNonStruct pins the one shape error the API cannot express in
// its type parameter.
func TestLoadRejectsNonStruct(t *testing.T) {
	if _, _, err := cfgkit.Load[int](); err == nil {
		t.Error("Load must reject a non-struct type")
	}
}
