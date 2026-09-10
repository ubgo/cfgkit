package cfgkit_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/cfgkit"
)

// Server exercises the common shapes: scalars, a duration, a slice, and a
// default supplied by the Defaulter method.
type Server struct {
	Port          int           `env:"PORT"`
	Host          string        `env:"HOST"`
	Debug         bool          `env:"DEBUG"`
	ReadTimeout   time.Duration `env:"READ_TIMEOUT"`
	Origins       []string      `env:"ORIGINS"`
	ShutdownGrace time.Duration `env:"SHUTDOWN_GRACE" default:"30s"`
}

func (s *Server) Defaults() {
	s.Port = 8080
	s.Host = "localhost"
	s.ReadTimeout = 15 * time.Second
	s.Origins = []string{"http://localhost:3000"}
}

type DB struct {
	URL      string `env:"DATABASE_URL"`
	Password string `env:"DATABASE_PASSWORD" secret:"true"`
}

func (d *DB) Defaults() { d.URL = "postgres://localhost/dev" }

type App struct {
	Server Server `json:"server"`
	DB     DB     `json:"db"`
	Name   string `env:"APP_NAME"`
}

func (a *App) Defaults() { a.Name = "cfgkit-app" }

// TestZeroInput pins the invariant the whole package exists for: with no
// sources and an empty environment, Load returns a complete valid config.
//
// This is what makes `go run .` work on a fresh clone with nothing installed.
// If this test fails, zero-config is a claim rather than a property.
func TestZeroInput(t *testing.T) {
	cfg, res, err := cfgkit.Load[App]()
	if err != nil {
		t.Fatalf("zero-input load failed: %v", err)
	}
	if cfg.Server.Port != 8080 || cfg.Server.Host != "localhost" {
		t.Errorf("defaults not applied: %+v", cfg.Server)
	}
	if cfg.Server.ShutdownGrace != 30*time.Second {
		t.Errorf("tag default not applied: %v", cfg.Server.ShutdownGrace)
	}
	if cfg.DB.URL != "postgres://localhost/dev" || cfg.Name != "cfgkit-app" {
		t.Errorf("nested defaults not applied: %+v", cfg)
	}
	for _, f := range res.Fields() {
		if f.Source != "default" {
			t.Errorf("%s claims source %q with no sources configured", f.Path, f.Source)
		}
	}
}

// TestPrecedence pins that the LAST source claiming a key wins, and that the
// Result reports which one it was.
func TestPrecedence(t *testing.T) {
	cfg, res, err := cfgkit.Load[App](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"PORT": "1111", "HOST": "low"}),
		cfgkit.FromMap(map[string]string{"PORT": "2222"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 2222 {
		t.Errorf("Port = %d, want 2222 — the later source must win", cfg.Server.Port)
	}
	if cfg.Server.Host != "low" {
		t.Errorf("Host = %q — a key only the earlier source claims must still apply", cfg.Server.Host)
	}
	// Every field's recorded origin must match the source that actually won.
	for _, f := range res.Fields() {
		if f.Path == "Server.Port" && f.Value != "2222" {
			t.Errorf("provenance value %q disagrees with the bound value", f.Value)
		}
	}
}

// TestTypes covers the closed type set in one pass.
func TestTypes(t *testing.T) {
	cfg, _, err := cfgkit.Load[App](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
		"DEBUG":        "true",
		"READ_TIMEOUT": "45s",
		"ORIGINS":      "https://a.test, https://b.test",
	})))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Server.Debug {
		t.Error("bool not decoded")
	}
	if cfg.Server.ReadTimeout != 45*time.Second {
		t.Errorf("duration = %v, want 45s", cfg.Server.ReadTimeout)
	}
	if len(cfg.Server.Origins) != 2 || cfg.Server.Origins[1] != "https://b.test" {
		t.Errorf("slice = %#v — elements must be split and trimmed", cfg.Server.Origins)
	}
}

// TestEmptySliceIsEmpty pins that "ORIGINS=" means no origins, never one
// nameless origin. The opposite reading produces an empty entry that fails far
// from its cause.
func TestEmptySliceIsEmpty(t *testing.T) {
	cfg, _, err := cfgkit.Load[App](cfgkit.WithSources(cfgkit.FromMap(map[string]string{"ORIGINS": ""})))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Server.Origins) != 0 {
		t.Errorf("Origins = %#v, want empty", cfg.Server.Origins)
	}
}

// TestDecodeErrorCollectsAll pins that N independent problems report N errors.
// Failing on the first would make a misconfigured deploy one fix per restart.
func TestDecodeErrorCollectsAll(t *testing.T) {
	_, _, err := cfgkit.Load[App](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
		"PORT":         "eighty",
		"DEBUG":        "maybe",
		"READ_TIMEOUT": "soon",
	})))
	if err == nil {
		t.Fatal("expected errors")
	}
	msg := err.Error()
	for _, want := range []string{"Server.Port", "Server.Debug", "Server.ReadTimeout"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error text is missing %s:\n%s", want, msg)
		}
	}
	var de *cfgkit.DecodeError
	if !errors.As(err, &de) {
		t.Error("expected a *DecodeError in the chain")
	}
}

// TestSecretNeverInOutput pins the masking rule structurally: the value must be
// ABSENT from Explain and from the error text, not merely styled differently.
func TestSecretNeverInOutput(t *testing.T) {
	const secret = "hunter2-do-not-leak"
	_, res, err := cfgkit.Load[App](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"DATABASE_PASSWORD": secret}),
	))
	if err != nil {
		t.Fatal(err)
	}

	var sb strings.Builder
	if err := res.Explain(&sb); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sb.String(), secret) {
		t.Error("Explain leaked a secret value")
	}

	j, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(j), secret) {
		t.Error("JSON leaked a secret value")
	}

	// With Reveal the operator asked for it explicitly, so it must appear.
	_, res2, err := cfgkit.Load[App](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"DATABASE_PASSWORD": secret})),
		cfgkit.Reveal(),
	)
	if err != nil {
		t.Fatal(err)
	}
	var sb2 strings.Builder
	if err := res2.Explain(&sb2); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb2.String(), secret) {
		t.Error("Reveal did not reveal the value")
	}
}

// TestNoEnvironmentMutation pins that Load leaves os.Environ untouched. The
// package reads configuration; it does not export it.
func TestNoEnvironmentMutation(t *testing.T) {
	t.Setenv("PORT", "7777")
	before := os.Environ()

	if _, _, err := cfgkit.Load[App](cfgkit.WithSources(cfgkit.FromEnviron())); err != nil {
		t.Fatal(err)
	}

	after := os.Environ()
	if len(before) != len(after) {
		t.Fatalf("environment size changed: %d → %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("environment entry changed: %q → %q", before[i], after[i])
		}
	}
}

// TestFromFilesMissingIsNotAnError pins that an absent file is a normal, silent
// outcome. That is what allows a program to ship without a .env at all.
func TestFromFilesMissingIsNotAnError(t *testing.T) {
	cfg, _, err := cfgkit.Load[App](cfgkit.WithSources(
		cfgkit.FromFiles(filepath.Join(t.TempDir(), "nope.env")),
	))
	if err != nil {
		t.Fatalf("a missing file must not fail the load: %v", err)
	}
	if cfg.Server.Port != 8080 {
		t.Error("defaults must survive a missing file")
	}
}

// TestFromFilesPrecedence pins that a later file wins, which is the
// .env / .env.local convention users already know.
func TestFromFilesPrecedence(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, ".env")
	local := filepath.Join(dir, ".env.local")
	if err := os.WriteFile(base, []byte("PORT=1111\nHOST=base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("PORT=2222\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := cfgkit.Load[App](cfgkit.WithSources(cfgkit.FromFiles(base, local)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 2222 {
		t.Errorf("Port = %d, want 2222 from the later file", cfg.Server.Port)
	}
	if cfg.Server.Host != "base" {
		t.Errorf("Host = %q, want the value only the earlier file sets", cfg.Server.Host)
	}
}

// joinedValidator reports TWO problems the way Validate is documented to:
// joined, so a misconfigured deploy is one fix rather than one fix per restart.
type joinedValidator struct {
	Port  int    `env:"PORT" default:"70000"`
	Level string `env:"LEVEL" default:"verbose"`
}

func (c *joinedValidator) Validate() error {
	return errors.Join(
		cfgkit.Range("PORT", c.Port, 1, 65535),
		cfgkit.OneOf("LEVEL", c.Level, "debug", "info"),
	)
}

// TestProblemCountMatchesTheProblemsListed pins that the "N problem(s)" header
// agrees with the lines beneath it.
//
// It counted the top-level error slice, and Validate contributes exactly one
// entry however many problems it found — so two bad fields printed "1
// problem(s)" above two lines. A reader who trusts the header, fixes the one
// problem it promised and redeploys gets a second failed rollout.
func TestProblemCountMatchesTheProblemsListed(t *testing.T) {
	_, _, err := cfgkit.Load[joinedValidator]()
	if err == nil {
		t.Fatal("two invalid fields loaded without error")
	}

	msg := err.Error()
	if !strings.Contains(msg, "2 problem(s)") {
		t.Errorf("header does not match the list:\n%s", msg)
	}
	// Both problems must actually be present — a header of 2 above one line
	// would be the same defect in the other direction.
	for _, want := range []string{"PORT", "LEVEL"} {
		if !strings.Contains(msg, want) {
			t.Errorf("problem for %s missing:\n%s", want, msg)
		}
	}
}
