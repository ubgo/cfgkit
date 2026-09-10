// Tests for CONFIG_SPEC §4.3 — reporting a key that matched no field.
//
// Explain answers "where did this value come from". This answers the mirror
// question: "why did my value go nowhere".
package cfgkit_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/cfgkit"
)

type unknownCfg struct {
	DatabaseURL string `env:"DATABASE_URL" default:"postgres://localhost/dev"`
	Port        int    `env:"PORT" default:"8080"`
	APIKey      string `env:"HYPERDX_API_KEY" was:"HYPERDX_KEY"`
}

// TestUnknownKeyInFile is the motivating case: a typo in a .env file used to be
// completely silent — the field fell back to its default and no diagnostic ever
// mentioned the misspelled key.
func TestUnknownKeyInFile(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	if err := os.WriteFile(env, []byte(
		"DATABAS_URL=postgres://prod-db.internal/app\n"+ // typo: missing the E
			"PORT=9000\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, res, err := cfgkit.Load[unknownCfg](cfgkit.WithSources(cfgkit.FromFiles(env)))
	if err != nil {
		t.Fatal(err)
	}

	// The load still succeeds — the finding is advisory, not fatal.
	if cfg.Port != 9000 {
		t.Errorf("Port = %d, the rest of the file must still apply", cfg.Port)
	}

	u := res.Unknown()
	if len(u) != 1 {
		t.Fatalf("Unknown() = %v, want exactly the typo", u)
	}
	if u[0].Key != "DATABAS_URL" {
		t.Errorf("reported %q, want the misspelled key", u[0].Key)
	}
	if !strings.Contains(u[0].Source, "file:") {
		t.Errorf("source = %q, want the file that supplied it", u[0].Source)
	}
	if !strings.Contains(u[0].String(), "matched no field") {
		t.Errorf("String() = %q, want a line an operator can read", u[0].String())
	}
}

// TestUnknownExcludesFromEnviron is the load-bearing test.
//
// FromEnviron does not implement KeyLister, so the machine's own PATH, HOME and
// sixty other variables cannot enter the report. If they could, the one line
// that matters would be buried and nobody would read the feature.
func TestUnknownExcludesFromEnviron(t *testing.T) {
	t.Setenv("SOME_UNRELATED_VARIABLE", "x")

	_, res, err := cfgkit.Load[unknownCfg](cfgkit.WithSources(cfgkit.FromEnviron()))
	if err != nil {
		t.Fatal(err)
	}
	if u := res.Unknown(); len(u) != 0 {
		t.Errorf("Unknown() = %v, want empty — FromEnviron cannot enumerate", u)
	}
}

// TestUnknownFromMapAndPrefixed pins the other two listable sources.
func TestUnknownFromMapAndPrefixed(t *testing.T) {
	t.Run("FromMap", func(t *testing.T) {
		_, res, err := cfgkit.Load[unknownCfg](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"PORT": "1", "PROT": "2"}),
		))
		if err != nil {
			t.Fatal(err)
		}
		u := res.Unknown()
		if len(u) != 1 || u[0].Key != "PROT" {
			t.Errorf("Unknown() = %v, want just PROT", u)
		}
	})

	t.Run("FromPrefixedEnviron", func(t *testing.T) {
		// The prefix defines a closed set that belongs to this application, so
		// unlike FromEnviron it can honestly enumerate.
		t.Setenv("SVC_PORT", "1")
		t.Setenv("SVC_PROT", "2")
		t.Setenv("NOT_MINE", "3") // outside the prefix — must not appear

		_, res, err := cfgkit.Load[unknownCfg](cfgkit.WithSources(
			cfgkit.FromPrefixedEnviron("SVC_"),
		))
		if err != nil {
			t.Fatal(err)
		}
		u := res.Unknown()
		if len(u) != 1 || u[0].Key != "PROT" {
			t.Errorf("Unknown() = %v, want just PROT with the prefix stripped", u)
		}
	})
}

// TestUnknownDoesNotReportFormerKeyNames pins the rule that keeps `was:` safe.
//
// A deployment still exporting an old key name is doing exactly what the tag
// exists to permit. Reporting it as a typo would punish the migration.
func TestUnknownDoesNotReportFormerKeyNames(t *testing.T) {
	cfg, res, err := cfgkit.Load[unknownCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"HYPERDX_KEY": "from-old-name"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "from-old-name" {
		t.Errorf("APIKey = %q, the former name must still bind", cfg.APIKey)
	}
	if u := res.Unknown(); len(u) != 0 {
		t.Errorf("Unknown() = %v, want empty — a `was:` name is wanted, not unknown", u)
	}
}

type prefixedCfg struct {
	HyperDX struct {
		APIKey string `env:"API_KEY"`
	} `env:",prefix=HYPERDX_"`
}

// TestUnknownUsesTheFullPrefixedKey pins that a prefixed field's full key is
// what counts as wanted. Comparing against the bare suffix would report every
// correctly-set prefixed key as a typo.
func TestUnknownUsesTheFullPrefixedKey(t *testing.T) {
	_, res, err := cfgkit.Load[prefixedCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"HYPERDX_API_KEY": "k"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if u := res.Unknown(); len(u) != 0 {
		t.Errorf("Unknown() = %v, want empty — the full prefixed key is wanted", u)
	}
}

// TestUnknownIsAdvisory pins the decision that a shared file is not punished.
//
// sync_go's .env.prod carries GITHUB_SECRET_* keys for its deployment pipeline
// alongside the application's own configuration. Those keys are unknown to the
// binary BY DESIGN, so a loader that refused to start would break the
// one-file-several-audiences pattern dotenvctl's --prefix selection serves.
func TestUnknownIsAdvisory(t *testing.T) {
	_, res, err := cfgkit.Load[unknownCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{
			"PORT":                      "9000",
			"GITHUB_SECRET_GHCR_PAT":    "ghp_x", // for the deploy pipeline
			"GITHUB_SECRET_DEPLOY_PATH": "/srv",  // not for this binary
		}),
	))
	if err != nil {
		t.Fatalf("a shared file must load cleanly: %v", err)
	}
	if len(res.Unknown()) != 2 {
		t.Errorf("Unknown() = %v, want both deploy keys reported but tolerated", res.Unknown())
	}
}

// TestUnknownIsSorted pins deterministic output, so a caller can diff it.
func TestUnknownIsSorted(t *testing.T) {
	_, res, err := cfgkit.Load[unknownCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"ZZZ": "1", "AAA": "2", "MMM": "3"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	u := res.Unknown()
	if len(u) != 3 || u[0].Key != "AAA" || u[2].Key != "ZZZ" {
		t.Errorf("Unknown() = %v, want sorted", u)
	}
}
