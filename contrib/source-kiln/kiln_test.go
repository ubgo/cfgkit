package kiln_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/cfgkit"
	kiln "github.com/ubgo/cfgkit/contrib/source-kiln"
)

// fake stands in for age decryption. Every test here uses it — the suite never
// generates a key, writes a kiln.toml, or reads the developer's ~/.kiln.
type fake struct {
	vars map[string]string
	err  error

	// calls records how many times decryption ran, which is what proves the
	// read-once promise rather than assuming it.
	calls int
	// gotKeyPath captures what the option layer passed down.
	gotConfigPath, gotKeyPath, gotFile string
}

func (f *fake) Decrypt(configPath, keyPath, file string) (map[string]string, error) {
	f.calls++
	f.gotConfigPath, f.gotKeyPath, f.gotFile = configPath, keyPath, file
	if f.err != nil {
		return nil, f.err
	}
	return f.vars, nil
}

func TestReadsDecryptedVariables(t *testing.T) {
	f := &fake{vars: map[string]string{"DATABASE_URL": "postgres://prod", "PORT": "8080"}}
	src := kiln.Env("kiln.toml", "production", kiln.WithDecrypter(f))

	for _, want := range []struct{ key, val string }{
		{"DATABASE_URL", "postgres://prod"},
		{"PORT", "8080"},
	} {
		got, ok, err := src.Lookup(want.key)
		if err != nil {
			t.Fatalf("Lookup(%q) error: %v", want.key, err)
		}
		if !ok || got != want.val {
			t.Errorf("Lookup(%q) = %q, %v; want %q, true", want.key, got, ok, want.val)
		}
	}
}

func TestConfigPathAndFileReachTheDecrypter(t *testing.T) {
	f := &fake{vars: map[string]string{"A": "1"}}
	kiln.Env("deploy/kiln.toml", "staging", kiln.WithDecrypter(f))

	if f.gotConfigPath != "deploy/kiln.toml" {
		t.Errorf("configPath = %q, want deploy/kiln.toml", f.gotConfigPath)
	}
	if f.gotFile != "staging" {
		t.Errorf("file = %q, want staging", f.gotFile)
	}
	if f.gotKeyPath != "" {
		t.Errorf("keyPath = %q, want empty (auto-discovery)", f.gotKeyPath)
	}
}

func TestWithKeyPathIsPassedDown(t *testing.T) {
	f := &fake{vars: map[string]string{"A": "1"}}
	kiln.Env("kiln.toml", "production",
		kiln.WithDecrypter(f), kiln.WithKeyPath("/keys/ci.key"))

	if f.gotKeyPath != "/keys/ci.key" {
		t.Errorf("keyPath = %q, want /keys/ci.key", f.gotKeyPath)
	}
}

func TestMissingKeyIsAMissNotAnError(t *testing.T) {
	f := &fake{vars: map[string]string{"A": "1"}}
	src := kiln.Env("kiln.toml", "production", kiln.WithDecrypter(f))

	_, ok, err := src.Lookup("NOPE")
	if err != nil {
		t.Fatalf("Lookup error: %v", err)
	}
	if ok {
		t.Error("Lookup(NOPE) reported found")
	}
}

// TestDecryptionFailureIsAnErrorNotAMiss is the whole safety argument for this
// adapter: an identity denied access must not look like an unset key, because
// that difference is a deploy proceeding with an empty password.
func TestDecryptionFailureIsAnErrorNotAMiss(t *testing.T) {
	f := &fake{err: errors.New("no identity in kiln.toml grants access")}
	src := kiln.Env("kiln.toml", "production", kiln.WithDecrypter(f))

	_, ok, err := src.Lookup("DATABASE_URL")
	if err == nil {
		t.Fatal("Lookup succeeded on a decryption failure")
	}
	if ok {
		t.Error("Lookup reported found alongside an error")
	}
	for _, want := range []string{"production", "kiln.toml", "no identity"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestEmptyFileIsAnErrorByDefault(t *testing.T) {
	f := &fake{vars: map[string]string{}}
	src := kiln.Env("kiln.toml", "production", kiln.WithDecrypter(f))

	if _, _, err := src.Lookup("ANY"); err == nil {
		t.Fatal("empty environment accepted without Optional()")
	} else if !strings.Contains(err.Error(), "kiln.Optional()") {
		t.Errorf("error %q does not name the fix", err)
	}
}

func TestOptionalAcceptsAnEmptyFile(t *testing.T) {
	f := &fake{vars: map[string]string{}}
	src := kiln.Env("kiln.toml", "production", kiln.WithDecrypter(f), kiln.Optional())

	_, ok, err := src.Lookup("ANY")
	if err != nil {
		t.Fatalf("Optional() still errored: %v", err)
	}
	if ok {
		t.Error("Lookup reported found in an empty source")
	}
}

// TestOptionalDoesNotSuppressRealFailures separates "nothing there" from
// "could not look" — Optional() means the first, never the second.
func TestOptionalDoesNotSuppressRealFailures(t *testing.T) {
	f := &fake{err: errors.New("kiln.toml: no such file")}
	src := kiln.Env("kiln.toml", "production", kiln.WithDecrypter(f), kiln.Optional())

	if _, _, err := src.Lookup("A"); err == nil {
		t.Fatal("Optional() swallowed a decryption failure")
	}
}

func TestPrefixFiltersAndStrips(t *testing.T) {
	f := &fake{vars: map[string]string{
		"API_PORT":    "8080",
		"API_HOST":    "0.0.0.0",
		"WORKER_PORT": "9090",
	}}
	src := kiln.Env("kiln.toml", "production",
		kiln.WithDecrypter(f), kiln.WithPrefix("API_"))

	got, ok, err := src.Lookup("PORT")
	if err != nil || !ok || got != "8080" {
		t.Errorf("Lookup(PORT) = %q, %v, %v; want 8080, true, nil", got, ok, err)
	}
	// The prefixed spelling must NOT also resolve — stripping is a rename, not
	// an alias, or a struct could bind either name and drift.
	if _, ok, _ := src.Lookup("API_PORT"); ok {
		t.Error("prefixed name still resolves after stripping")
	}
	if _, ok, _ := src.Lookup("WORKER_PORT"); ok {
		t.Error("out-of-prefix variable leaked through")
	}
}

func TestPrefixThatMatchesNothingIsAnError(t *testing.T) {
	f := &fake{vars: map[string]string{"WORKER_PORT": "9090"}}
	src := kiln.Env("kiln.toml", "production",
		kiln.WithDecrypter(f), kiln.WithPrefix("API_"))

	if _, _, err := src.Lookup("PORT"); err == nil {
		t.Fatal("a prefix matching nothing was accepted")
	}
}

func TestDecryptionHappensOncePerSourceNotPerLookup(t *testing.T) {
	f := &fake{vars: map[string]string{"A": "1", "B": "2", "C": "3"}}
	src := kiln.Env("kiln.toml", "production", kiln.WithDecrypter(f))

	for range 10 {
		// Return values are irrelevant here; the point is the CALL COUNT.
		_, _, _ = src.Lookup("A")
		_, _, _ = src.Lookup("B")
	}
	if f.calls != 1 {
		t.Errorf("decrypted %d times; want 1 — unlocking per lookup leaves that "+
			"many more copies of plaintext in memory", f.calls)
	}
}

func TestKeysListsTheEnvironment(t *testing.T) {
	f := &fake{vars: map[string]string{"A": "1", "B": "2"}}
	src := kiln.Env("kiln.toml", "production", kiln.WithDecrypter(f))

	lister, ok := src.(cfgkit.KeyLister)
	if !ok {
		t.Fatal("source does not implement cfgkit.KeyLister")
	}
	keys := lister.Keys()
	if len(keys) != 2 {
		t.Fatalf("Keys() = %v; want 2 entries", keys)
	}
	seen := map[string]bool{}
	for _, k := range keys {
		seen[k] = true
	}
	if !seen["A"] || !seen["B"] {
		t.Errorf("Keys() = %v; want A and B", keys)
	}
}

func TestKeysReportsStrippedNames(t *testing.T) {
	f := &fake{vars: map[string]string{"API_PORT": "8080"}}
	src := kiln.Env("kiln.toml", "production",
		kiln.WithDecrypter(f), kiln.WithPrefix("API_"))

	keys := src.(cfgkit.KeyLister).Keys()
	if len(keys) != 1 || keys[0] != "PORT" {
		t.Errorf("Keys() = %v; want [PORT] — an unknown-key warning naming the "+
			"unstripped spelling would point at a name no struct can bind", keys)
	}
}

func TestNameIdentifiesTheEnvironment(t *testing.T) {
	f := &fake{vars: map[string]string{"A": "1"}}
	src := kiln.Env("kiln.toml", "staging", kiln.WithDecrypter(f))

	if got := src.Name(); got != "kiln:staging" {
		t.Errorf("Name() = %q, want kiln:staging", got)
	}
}

// TestBindsThroughLoad is the end-to-end check: a decrypted environment
// reaching a real struct through the real cfgkit chain.
func TestBindsThroughLoad(t *testing.T) {
	type Config struct {
		DatabaseURL string `env:"DATABASE_URL"`
		Port        int    `env:"PORT"`
	}

	f := &fake{vars: map[string]string{
		"DATABASE_URL": "postgres://prod/db",
		"PORT":         "8080",
	}}
	cfg, _, err := cfgkit.Load[Config](
		cfgkit.WithSources(kiln.Env("kiln.toml", "production", kiln.WithDecrypter(f))),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DatabaseURL != "postgres://prod/db" || cfg.Port != 8080 {
		t.Errorf("Load = %+v; want the decrypted values", cfg)
	}
}

// TestLaterSourceStillWins pins ordering: kiln is a source like any other, so
// an operator override in the environment is not defeated by encryption.
func TestLaterSourceStillWins(t *testing.T) {
	type Config struct {
		Port int `env:"PORT"`
	}

	f := &fake{vars: map[string]string{"PORT": "8080"}}
	t.Setenv("PORT", "9999")

	cfg, _, err := cfgkit.Load[Config](cfgkit.WithSources(
		kiln.Env("kiln.toml", "production", kiln.WithDecrypter(f)),
		cfgkit.FromEnviron(),
	))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 9999 {
		t.Errorf("Port = %d; want 9999 — a later source must still override kiln", cfg.Port)
	}
}

// TestDefaultDecrypterIsTheRealOne exercises the production path without any
// key material, by pointing at a kiln.toml that does not exist. It proves the
// default is actually wired — a nil decrypter reaching Lookup would panic, and
// a test suite that always injects a fake would never notice.
func TestDefaultDecrypterIsTheRealOne(t *testing.T) {
	src := kiln.Env(t.TempDir()+"/absent.toml", "production")

	_, _, err := src.Lookup("ANY")
	if err == nil {
		t.Fatal("a missing kiln.toml was accepted")
	}
	if !strings.Contains(err.Error(), "absent.toml") {
		t.Errorf("error %q does not name the config file", err)
	}
}
