// Tests for CONFIG_SPEC §4.5 — the conventional source chain.
//
// The chain itself is a convenience. The part that earns its keep is
// resolveChainMode: before it, APP_ENV written into a .env file was silently
// ignored, the mode stayed dev, and every RequiredIn(ModeProd, …) rule quietly
// did not fire — a production deployment running under development strictness
// with nothing in the logs to say so.
package cfgkit_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/cfgkit"
)

type chainCfg struct {
	Host string `env:"CHAIN_HOST" default:"compiled-in"`
	Port int    `env:"CHAIN_PORT" default:"8080"`
}

// writeChain creates the named files in a fresh directory. The value written to
// each is the file's own name, so a test can read the result and say exactly
// which file won.
func writeChain(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestChainWithNoFilesAtAll is the zero-config guarantee, restated for the
// chain: the whole chain may be absent and the load still succeeds. If this
// ever fails, `git clone && go run .` stops working, which is the property the
// package exists for.
func TestChainWithNoFilesAtAll(t *testing.T) {
	dir := t.TempDir()
	cfg, res, err := cfgkit.Load[chainCfg](cfgkit.DefaultSourcesIn(dir))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "compiled-in" || cfg.Port != 8080 {
		t.Errorf("cfg = %+v, want the compiled-in defaults", cfg)
	}
	if res.Mode() != cfgkit.ModeDev {
		t.Errorf("mode = %q, want dev", res.Mode())
	}
}

// TestChainFileOrder pins the precedence every file in the chain has. Each file
// sets the same key to its own name, so the winner names itself.
func TestChainFileOrder(t *testing.T) {
	dir := writeChain(t, map[string]string{
		".env":           "CHAIN_HOST=.env\n",
		".env.local":     "CHAIN_HOST=.env.local\n",
		".env.dev":       "CHAIN_HOST=.env.dev\n",
		".env.dev.local": "CHAIN_HOST=.env.dev.local\n",
	})

	cfg, _, err := cfgkit.Load[chainCfg](cfgkit.DefaultSourcesIn(dir))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != ".env.dev.local" {
		t.Errorf("Host = %q, want .env.dev.local — the last file in the chain", cfg.Host)
	}
}

// TestChainFileOrderStepwise removes one file at a time from the top, so every
// position in the chain is asserted rather than only the winner.
func TestChainFileOrderStepwise(t *testing.T) {
	all := map[string]string{
		".env":           "CHAIN_HOST=.env\n",
		".env.local":     "CHAIN_HOST=.env.local\n",
		".env.dev":       "CHAIN_HOST=.env.dev\n",
		".env.dev.local": "CHAIN_HOST=.env.dev.local\n",
	}
	// Highest precedence first; each step drops the current winner.
	order := []string{".env.dev.local", ".env.dev", ".env.local", ".env"}

	for i, want := range order {
		t.Run(want, func(t *testing.T) {
			present := map[string]string{}
			for name, body := range all {
				present[name] = body
			}
			for _, dropped := range order[:i] {
				delete(present, dropped)
			}

			cfg, _, err := cfgkit.Load[chainCfg](cfgkit.DefaultSourcesIn(writeChain(t, present)))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Host != want {
				t.Errorf("Host = %q, want %q", cfg.Host, want)
			}
		})
	}
}

// TestChainEnvironBeatsEveryFile pins the rule a deployment depends on: what is
// exported in the process environment wins over anything on disk.
func TestChainEnvironBeatsEveryFile(t *testing.T) {
	dir := writeChain(t, map[string]string{
		".env":           "CHAIN_HOST=.env\n",
		".env.dev.local": "CHAIN_HOST=.env.dev.local\n",
	})
	t.Setenv("CHAIN_HOST", "environ")

	cfg, _, err := cfgkit.Load[chainCfg](cfgkit.DefaultSourcesIn(dir))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "environ" {
		t.Errorf("Host = %q, want environ", cfg.Host)
	}
}

// ---------------------------------------------------------------------------
// The mode, and the bug that made this decision worth acting on.
// ---------------------------------------------------------------------------

// TestModeFromDotenvFile is the regression test for the defect this work found.
//
// Before the two-pass resolution, this returned dev: resolveMode called
// os.Getenv directly, so APP_ENV in a file was never seen. A team putting
// APP_ENV=production in .env.prod got development strictness in production.
func TestModeFromDotenvFile(t *testing.T) {
	dir := writeChain(t, map[string]string{".env": "APP_ENV=production\n"})

	_, res, err := cfgkit.Load[chainCfg](cfgkit.DefaultSourcesIn(dir))
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode() != cfgkit.ModeProd {
		t.Errorf("mode = %q, want prod — APP_ENV in a .env file must be honoured", res.Mode())
	}
}

// TestModeSelectsTheModeFile closes the loop: the mode read from a file must
// then choose which .env.<mode> file is loaded.
func TestModeSelectsTheModeFile(t *testing.T) {
	dir := writeChain(t, map[string]string{
		".env":      "APP_ENV=production\nCHAIN_HOST=base\n",
		".env.dev":  "CHAIN_HOST=wrong-dev-file\n",
		".env.prod": "CHAIN_HOST=right-prod-file\n",
	})

	cfg, res, err := cfgkit.Load[chainCfg](cfgkit.DefaultSourcesIn(dir))
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode() != cfgkit.ModeProd {
		t.Fatalf("mode = %q, want prod", res.Mode())
	}
	if cfg.Host != "right-prod-file" {
		t.Errorf("Host = %q, want the prod file — the mode must pick the file", cfg.Host)
	}
}

// TestModePrecedence pins the whole ladder in one place. It is the same
// precedence rule the rest of the package uses, so there is nothing new to
// learn — which is the point.
func TestModePrecedence(t *testing.T) {
	dir := writeChain(t, map[string]string{
		".env":       "APP_ENV=production\n",
		".env.local": "APP_ENV=test\n",
	})

	t.Run("explicit WithMode wins over everything", func(t *testing.T) {
		t.Setenv("APP_ENV", "production")
		_, res, err := cfgkit.Load[chainCfg](
			cfgkit.DefaultSourcesIn(dir),
			cfgkit.WithMode(cfgkit.ModeDev),
		)
		if err != nil {
			t.Fatal(err)
		}
		if res.Mode() != cfgkit.ModeDev {
			t.Errorf("mode = %q, want dev — the caller stated it", res.Mode())
		}
	})

	t.Run("WithMode wins whichever side of DefaultSources it is on", func(t *testing.T) {
		// Resolving the chain inside the Option would make these two differ,
		// which is the kind of ordering rule nobody remembers.
		_, before, err := cfgkit.Load[chainCfg](
			cfgkit.WithMode(cfgkit.ModeProd), cfgkit.DefaultSourcesIn(dir))
		if err != nil {
			t.Fatal(err)
		}
		_, after, err := cfgkit.Load[chainCfg](
			cfgkit.DefaultSourcesIn(dir), cfgkit.WithMode(cfgkit.ModeProd))
		if err != nil {
			t.Fatal(err)
		}
		if before.Mode() != cfgkit.ModeProd || after.Mode() != cfgkit.ModeProd {
			t.Errorf("mode = %q / %q, want prod in both argument orders", before.Mode(), after.Mode())
		}
	})

	t.Run("environ beats the files", func(t *testing.T) {
		t.Setenv("APP_ENV", "prod")
		_, res, err := cfgkit.Load[chainCfg](cfgkit.DefaultSourcesIn(dir))
		if err != nil {
			t.Fatal(err)
		}
		if res.Mode() != cfgkit.ModeProd {
			t.Errorf("mode = %q, want prod from the environment", res.Mode())
		}
	})

	t.Run("dot-env-local beats dot-env", func(t *testing.T) {
		_, res, err := cfgkit.Load[chainCfg](cfgkit.DefaultSourcesIn(dir))
		if err != nil {
			t.Fatal(err)
		}
		if res.Mode() != cfgkit.ModeTest {
			t.Errorf("mode = %q, want test — .env.local overrides .env", res.Mode())
		}
	})

	t.Run("nothing set means dev", func(t *testing.T) {
		_, res, err := cfgkit.Load[chainCfg](cfgkit.DefaultSourcesIn(t.TempDir()))
		if err != nil {
			t.Fatal(err)
		}
		if res.Mode() != cfgkit.ModeDev {
			t.Errorf("mode = %q, want dev", res.Mode())
		}
	})
}

// TestModeSpellings pins that an operator never has to guess which spelling the
// library wants. "prod" is the Go convention, "production" the Node and Rails
// one, and both appear in real deployments.
func TestModeSpellings(t *testing.T) {
	cases := map[string]cfgkit.Mode{
		"prod": cfgkit.ModeProd, "production": cfgkit.ModeProd, "PRODUCTION": cfgkit.ModeProd,
		"  prod  ": cfgkit.ModeProd,
		"test":     cfgkit.ModeTest, "testing": cfgkit.ModeTest, "TEST": cfgkit.ModeTest,
		"dev": cfgkit.ModeDev, "development": cfgkit.ModeDev,
		// Anything unrecognised is dev: an unset or misspelled mode means a
		// developer's machine far more often than production, and every
		// prod-only rule fails closed anyway.
		"prodution": cfgkit.ModeDev, "": cfgkit.ModeDev, "staging": cfgkit.ModeDev,
	}

	for text, want := range cases {
		t.Run(text, func(t *testing.T) {
			dir := writeChain(t, map[string]string{".env": "APP_ENV=" + text + "\n"})
			_, res, err := cfgkit.Load[chainCfg](cfgkit.DefaultSourcesIn(dir))
			if err != nil {
				t.Fatal(err)
			}
			if res.Mode() != want {
				t.Errorf("APP_ENV=%q gave mode %q, want %q", text, res.Mode(), want)
			}
		})
	}
}

// TestModeKeyIsOverridable pins that WithModeKey reaches the first pass too. If
// it did not, a caller who renamed the key would get the chain resolved against
// a variable they never set.
func TestModeKeyIsOverridable(t *testing.T) {
	dir := writeChain(t, map[string]string{
		".env":      "DEPLOY_ENV=production\nAPP_ENV=dev\n",
		".env.prod": "CHAIN_HOST=prod-file\n",
	})

	cfg, res, err := cfgkit.Load[chainCfg](
		cfgkit.DefaultSourcesIn(dir),
		cfgkit.WithModeKey("DEPLOY_ENV"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode() != cfgkit.ModeProd {
		t.Errorf("mode = %q, want prod from DEPLOY_ENV", res.Mode())
	}
	if cfg.Host != "prod-file" {
		t.Errorf("Host = %q, want the prod file", cfg.Host)
	}
}

// ---------------------------------------------------------------------------
// Test mode excludes .env.local.
// ---------------------------------------------------------------------------

// TestLocalFileIsSkippedInTestMode pins the one deliberate divergence from
// Vite, taken from create-react-app. A developer's personal gitignored file
// silently changing the result of `go test` on one machine and not another is
// the exact class of bug this package exists to make impossible.
func TestLocalFileIsSkippedInTestMode(t *testing.T) {
	dir := writeChain(t, map[string]string{
		".env":       "CHAIN_HOST=committed\n",
		".env.local": "CHAIN_HOST=personal-machine\n",
	})

	t.Run("test mode ignores it", func(t *testing.T) {
		cfg, _, err := cfgkit.Load[chainCfg](
			cfgkit.DefaultSourcesIn(dir), cfgkit.WithMode(cfgkit.ModeTest))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Host != "committed" {
			t.Errorf("Host = %q, want the committed value — a test must not depend on one machine", cfg.Host)
		}
	})

	t.Run("every other mode uses it", func(t *testing.T) {
		for _, mode := range []cfgkit.Mode{cfgkit.ModeDev, cfgkit.ModeProd} {
			cfg, _, err := cfgkit.Load[chainCfg](
				cfgkit.DefaultSourcesIn(dir), cfgkit.WithMode(mode))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Host != "personal-machine" {
				t.Errorf("mode %s: Host = %q, want the local file honoured", mode, cfg.Host)
			}
		}
	})

	t.Run("the mode-specific local file is still loaded in test", func(t *testing.T) {
		// Only `.env.local` is excluded. `.env.test.local` is named for the
		// mode, so using it is a deliberate choice rather than an accident.
		d := writeChain(t, map[string]string{
			".env":            "CHAIN_HOST=committed\n",
			".env.test.local": "CHAIN_HOST=explicit-test-override\n",
		})
		cfg, _, err := cfgkit.Load[chainCfg](
			cfgkit.DefaultSourcesIn(d), cfgkit.WithMode(cfgkit.ModeTest))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Host != "explicit-test-override" {
			t.Errorf("Host = %q, want the mode-specific local file", cfg.Host)
		}
	})
}

// ---------------------------------------------------------------------------
// Composition and reporting.
// ---------------------------------------------------------------------------

// TestChainExtraSourcesSitOnTop pins where an extra source lands: above the
// environment, which is where a flag set or a per-run override belongs.
func TestChainExtraSourcesSitOnTop(t *testing.T) {
	dir := writeChain(t, map[string]string{".env": "CHAIN_HOST=file\n"})
	t.Setenv("CHAIN_HOST", "environ")

	cfg, _, err := cfgkit.Load[chainCfg](cfgkit.DefaultSourcesIn(dir,
		cfgkit.FromMap(map[string]string{"CHAIN_HOST": "override"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "override" {
		t.Errorf("Host = %q, want the extra source to win", cfg.Host)
	}
}

// TestChainCombinesWithExplicitSources pins that an explicitly listed source
// sits BELOW the chain — a base the conventional files then override, which is
// the only ordering under which combining the two is not a silent trap.
func TestChainCombinesWithExplicitSources(t *testing.T) {
	dir := writeChain(t, map[string]string{".env": "CHAIN_HOST=from-chain\n"})

	cfg, _, err := cfgkit.Load[chainCfg](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{
			"CHAIN_HOST": "from-explicit",
			"CHAIN_PORT": "9999",
		})),
		cfgkit.DefaultSourcesIn(dir),
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "from-chain" {
		t.Errorf("Host = %q, want the chain to override the explicit base", cfg.Host)
	}
	if cfg.Port != 9999 {
		t.Errorf("Port = %d, want the explicit source's value where the chain is silent", cfg.Port)
	}
}

// TestChainProvenanceNamesTheFile is what keeps the convenience honest. The
// filenames are no longer in main.go, so Explain must still say exactly which
// file supplied each value.
func TestChainProvenanceNamesTheFile(t *testing.T) {
	dir := writeChain(t, map[string]string{
		".env":     "CHAIN_HOST=from-file\n",
		".env.dev": "CHAIN_PORT=9000\n",
	})

	_, res, err := cfgkit.Load[chainCfg](cfgkit.DefaultSourcesIn(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Fields() {
		if f.Path == "Host" && !strings.Contains(f.Source, ".env") {
			t.Errorf("Host source = %q, want the file named", f.Source)
		}
	}

	// And Files answers "which files were even looked at?" — with the names
	// gone from main.go, a typo'd filename would otherwise be invisible.
	files := res.Files()
	if len(files) != 4 {
		t.Fatalf("Files() = %v, want the four-file dev chain", files)
	}
	for i, want := range []string{".env", ".env.local", ".env.dev", ".env.dev.local"} {
		if filepath.Base(files[i]) != want {
			t.Errorf("Files()[%d] = %q, want %q", i, files[i], want)
		}
	}
}

// TestChainFilesReflectsTheTestModeExclusion keeps the report and the behaviour
// in step: a file that is not consulted must not be listed as consulted.
func TestChainFilesReflectsTheTestModeExclusion(t *testing.T) {
	_, res, err := cfgkit.Load[chainCfg](
		cfgkit.DefaultSourcesIn(t.TempDir()), cfgkit.WithMode(cfgkit.ModeTest))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Files() {
		if filepath.Base(f) == ".env.local" {
			t.Errorf("Files() = %v, must not list .env.local in test mode", res.Files())
		}
	}
}

// TestChainFilesIsEmptyForAnExplicitList pins that Files reports the CHAIN, not
// every file ever read: with an explicit list the caller already knows.
func TestChainFilesIsEmptyForAnExplicitList(t *testing.T) {
	_, res, err := cfgkit.Load[chainCfg](cfgkit.WithSources(cfgkit.FromMap(nil)))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files()) != 0 {
		t.Errorf("Files() = %v, want empty for an explicit source list", res.Files())
	}
}

// TestChainDoesNotMutateTheEnvironment restates the package-wide invariant for
// the new code path — the first pass reads the environment and must not write
// to it.
func TestChainDoesNotMutateTheEnvironment(t *testing.T) {
	dir := writeChain(t, map[string]string{
		".env":      "APP_ENV=production\nCHAIN_HOST=file\n",
		".env.prod": "CHAIN_PORT=9000\n",
	})
	before := os.Environ()

	if _, _, err := cfgkit.Load[chainCfg](cfgkit.DefaultSourcesIn(dir)); err != nil {
		t.Fatal(err)
	}

	after := os.Environ()
	if len(before) != len(after) {
		t.Fatalf("the environment changed: %d vars before, %d after", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("environment entry changed: %q became %q", before[i], after[i])
		}
	}
}

// TestChainIsIdempotent pins that loading twice from the same directory gives
// the same answer — the chain must not consume or cache anything.
func TestChainIsIdempotent(t *testing.T) {
	dir := writeChain(t, map[string]string{".env": "APP_ENV=production\nCHAIN_HOST=x\n"})

	first, r1, err := cfgkit.Load[chainCfg](cfgkit.DefaultSourcesIn(dir))
	if err != nil {
		t.Fatal(err)
	}
	second, r2, err := cfgkit.Load[chainCfg](cfgkit.DefaultSourcesIn(dir))
	if err != nil {
		t.Fatal(err)
	}
	if *first != *second || r1.Mode() != r2.Mode() {
		t.Errorf("two loads differ: %+v %q vs %+v %q", first, r1.Mode(), second, r2.Mode())
	}
}

// TestChainMalformedFileIsReportedOnce pins that the first pass swallowing a
// read error does not swallow it altogether: the real load consults the same
// files and must report the problem.
func TestChainMalformedFileIsReportedOnce(t *testing.T) {
	dir := t.TempDir()
	// A directory where a file is expected: unreadable in a way no .env parser
	// can recover from.
	if err := os.Mkdir(filepath.Join(dir, ".env"), 0o700); err != nil {
		t.Fatal(err)
	}

	err := cfgkit.Check[chainCfg](cfgkit.DefaultSourcesIn(dir))
	if err == nil {
		t.Fatal("want the unreadable file reported")
	}
	if strings.Count(err.Error(), ".env") == 0 {
		t.Errorf("error %q should name the file", err)
	}
}

// TestDefaultSourcesUsesTheWorkingDirectory covers the no-argument form, which
// is the one nearly every service will call. It resolves against the process's
// working directory, so a program run from its own repository root finds its
// own .env without being told where it is.
func TestDefaultSourcesUsesTheWorkingDirectory(t *testing.T) {
	dir := writeChain(t, map[string]string{
		".env":     "APP_ENV=production\nCHAIN_HOST=cwd-file\n",
		".env.dev": "CHAIN_HOST=wrong\n",
	})
	chdir(t, dir)

	cfg, res, err := cfgkit.Load[chainCfg](cfgkit.DefaultSources())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "cwd-file" {
		t.Errorf("Host = %q, want the file in the working directory", cfg.Host)
	}
	if res.Mode() != cfgkit.ModeProd {
		t.Errorf("mode = %q, want prod", res.Mode())
	}
}

// chdir changes the working directory for the duration of the test and
// restores it afterwards.
//
// testing.T.Chdir does exactly this and arrived in Go 1.24. It is written out
// here so the module keeps building on the minimum Go version it declares —
// a library that claims to support 1.22 and whose own tests need 1.24 has not
// really checked the claim.
//
// It does NOT run in parallel with anything: the working directory is process
// state, so a parallel test changing it would corrupt every other test's view.
func chdir(t *testing.T, dir string) {
	t.Helper()

	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("reading the working directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("changing to %s: %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Fatalf("restoring the working directory: %v", err)
		}
	})
}
