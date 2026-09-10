package kiln_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	upstream "github.com/thunderbottom/kiln/pkg/kiln"
	"github.com/ubgo/cfgkit"
	kiln "github.com/ubgo/cfgkit/contrib/source-kiln"
)

// vault builds a real kiln setup in a temp directory: a fresh age identity, a
// kiln.toml granting it access, and an encrypted environment file written by
// kiln itself. It returns the config path and the key path.
//
// It exists because the Decrypter seam — which every other test in this package
// uses — cannot prove the one thing this module claims: that pointing it at a
// kiln.toml yields the decrypted values. Faking the decryption and then
// asserting the values come back would be asserting the fake. These tests fake
// nothing.
//
// No key material outside the temp directory is touched, and none is committed.
func vault(t *testing.T, envName string, vars map[string]string) (configPath, keyPath string) {
	t.Helper()

	dir := t.TempDir()

	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generating an age identity: %v", err)
	}

	keyPath = filepath.Join(dir, "kiln.key")
	if err := os.WriteFile(keyPath, []byte(id.String()+"\n"), 0o600); err != nil {
		t.Fatalf("writing the key: %v", err)
	}

	configPath = filepath.Join(dir, "kiln.toml")
	config := fmt.Sprintf(`[recipients]
test = %q

[files.%s]
filename = %q
access = ["*"]
`, id.Recipient().String(), envName, filepath.Join(dir, envName+".env"))
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("writing kiln.toml: %v", err)
	}

	cfg, err := upstream.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("loading the config just written: %v", err)
	}
	identity, err := upstream.NewIdentityFromKey(keyPath)
	if err != nil {
		t.Fatalf("opening the key just written: %v", err)
	}
	defer identity.Cleanup()

	encrypted := make(map[string][]byte, len(vars))
	for k, v := range vars {
		encrypted[k] = []byte(v)
	}
	if err := upstream.SetMultipleEnvironmentVars(identity, cfg, envName, encrypted); err != nil {
		t.Fatalf("encrypting the fixture: %v", err)
	}
	return configPath, keyPath
}

// TestAgainstARealEncryptedFile is the test that proves the default path works
// end to end: real age encryption, real access control, real decryption.
func TestAgainstARealEncryptedFile(t *testing.T) {
	configPath, keyPath := vault(t, "production", map[string]string{
		"DATABASE_URL": "postgres://prod/db",
		"PORT":         "8080",
	})

	type Config struct {
		DatabaseURL string `env:"DATABASE_URL"`
		Port        int    `env:"PORT"`
	}

	cfg, _, err := cfgkit.Load[Config](cfgkit.WithSources(
		kiln.Env(configPath, "production", kiln.WithKeyPath(keyPath)),
	))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DatabaseURL != "postgres://prod/db" || cfg.Port != 8080 {
		t.Errorf("Load = %+v; want the decrypted values", cfg)
	}
}

// TestRealFilePrefixStripping repeats the prefix rule against real ciphertext,
// since key naming is where a fake most easily diverges from the real thing.
func TestRealFilePrefixStripping(t *testing.T) {
	configPath, keyPath := vault(t, "shared", map[string]string{
		"API_PORT":    "8080",
		"WORKER_PORT": "9090",
	})

	src := kiln.Env(configPath, "shared",
		kiln.WithKeyPath(keyPath), kiln.WithPrefix("API_"))

	got, ok, err := src.Lookup("PORT")
	if err != nil || !ok || got != "8080" {
		t.Fatalf("Lookup(PORT) = %q, %v, %v; want 8080, true, nil", got, ok, err)
	}
	if _, ok, _ := src.Lookup("WORKER_PORT"); ok {
		t.Error("out-of-prefix variable leaked through against a real file")
	}
}

// TestRealFileWrongKeyIsAnError is the safety claim proved rather than
// asserted: an identity the file does not grant access to gets an error, not an
// empty result that a deploy would happily proceed on.
func TestRealFileWrongKeyIsAnError(t *testing.T) {
	configPath, _ := vault(t, "production", map[string]string{"DATABASE_URL": "postgres://prod/db"})

	// A different identity, never named in that kiln.toml.
	intruder, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generating an age identity: %v", err)
	}
	wrongKey := filepath.Join(t.TempDir(), "other.key")
	if err := os.WriteFile(wrongKey, []byte(intruder.String()+"\n"), 0o600); err != nil {
		t.Fatalf("writing the key: %v", err)
	}

	src := kiln.Env(configPath, "production", kiln.WithKeyPath(wrongKey))

	got, ok, err := src.Lookup("DATABASE_URL")
	if err == nil {
		t.Fatalf("an unauthorised identity read the file: got %q, %v", got, ok)
	}
	if ok {
		t.Error("Lookup reported found alongside an error")
	}
	// Assert the failure is a DECRYPTION denial, not an unreadable key file —
	// otherwise this test would still pass if the fixture were broken, which is
	// the failure mode that makes a security test worthless.
	if !strings.Contains(err.Error(), "decrypt") && !strings.Contains(err.Error(), "identit") {
		t.Errorf("error %q does not look like an access denial", err)
	}
}

// TestRealFileUnknownEnvironment pins that naming an environment the config
// does not define is an error rather than an empty source.
func TestRealFileUnknownEnvironment(t *testing.T) {
	configPath, keyPath := vault(t, "production", map[string]string{"PORT": "8080"})

	src := kiln.Env(configPath, "staging", kiln.WithKeyPath(keyPath))

	if _, _, err := src.Lookup("PORT"); err == nil {
		t.Fatal("an undefined environment was accepted")
	}
}

// TestKeyAutoDiscovery covers the default key path — the branch that runs when
// no WithKeyPath is given.
//
// It redirects HOME to a temp directory first, so it exercises kiln's real
// discovery logic against a key this test created, and never reads whatever
// identity happens to be on the machine running the suite. Leaving this branch
// uncovered would mean the most commonly used path — a developer who just runs
// the program — was the one path no test touched.
func TestKeyAutoDiscovery(t *testing.T) {
	configPath, keyPath := vault(t, "production", map[string]string{"PORT": "8080"})

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".kiln"), 0o700); err != nil {
		t.Fatalf("preparing the fake home: %v", err)
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("reading the generated key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".kiln", "kiln.key"), key, 0o600); err != nil {
		t.Fatalf("planting the key: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // the same redirect on Windows

	src := kiln.Env(configPath, "production")

	got, ok, err := src.Lookup("PORT")
	if err != nil {
		t.Fatalf("auto-discovery failed: %v", err)
	}
	if !ok || got != "8080" {
		t.Errorf("Lookup(PORT) = %q, %v; want 8080, true", got, ok)
	}
}

// TestKeyDiscoveryFailureIsReported pins the arm where no key exists anywhere.
//
// HOME is redirected to an empty directory, so kiln's discovery finds nothing
// — the situation a developer hits on a fresh machine before running `kiln
// init`. It must be an error naming the problem, not a nil-key crash further
// down.
func TestKeyDiscoveryFailureIsReported(t *testing.T) {
	configPath, _ := vault(t, "production", map[string]string{"PORT": "8080"})

	empty := t.TempDir()
	t.Setenv("HOME", empty)
	t.Setenv("USERPROFILE", empty) // the same redirect on Windows

	// No WithKeyPath, so discovery runs and has nowhere to look.
	src := kiln.Env(configPath, "production")

	if _, _, err := src.Lookup("PORT"); err == nil {
		t.Fatal("a missing key was accepted")
	}
}

// TestMalformedKeyIsReported pins the other key-loading arm: a file exists at
// the named path but is not an age identity.
//
// It is a distinct failure from "no key" and deserves a distinct message — a
// truncated or half-copied key file is a real thing that happens, and being
// told "not found" would send someone looking for the wrong problem.
func TestMalformedKeyIsReported(t *testing.T) {
	configPath, _ := vault(t, "production", map[string]string{"PORT": "8080"})

	bad := filepath.Join(t.TempDir(), "broken.key")
	if err := os.WriteFile(bad, []byte("this is not an age identity\n"), 0o600); err != nil {
		t.Fatalf("writing the broken key: %v", err)
	}

	src := kiln.Env(configPath, "production", kiln.WithKeyPath(bad))

	if _, _, err := src.Lookup("PORT"); err == nil {
		t.Fatal("a malformed key file was accepted")
	}
}
