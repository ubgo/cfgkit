// Command read-kiln reads secrets from a kiln-encrypted env file.
//
// It RUNS with no setup: the example generates an age identity, writes a
// kiln.toml, and encrypts a fixture with kiln itself — all in a temp
// directory, all discarded on exit. Nothing touches ~/.kiln.
//
// kiln is the one source in the catalogue whose FILE IS SAFE TO COMMIT. Every
// other secret adapter answers "where do I put the secrets so they are not in
// the repository" by moving them somewhere else, which leaves a developer who
// clones the repository holding half a configuration. kiln keeps the encrypted
// file next to the code and lets kiln.toml decide who can read it.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	upstream "github.com/thunderbottom/kiln/pkg/kiln"
	"github.com/ubgo/cfgkit"
	kiln "github.com/ubgo/cfgkit/contrib/source-kiln"
)

// Config says nothing about kiln.
type Config struct {
	Host     string `env:"HOST" default:"localhost"`
	Port     int    `env:"PORT" default:"8080"`
	Password string `env:"PASSWORD" secret:"true"`
}

// envName is the environment declared inside kiln.toml. kiln's own model has
// one file per environment, and this is the name of the one we read.
const envName = "production"

// setup builds a real kiln vault in dir and returns the config and key paths.
func setup(dir string, vars map[string]string) (configPath, keyPath string, err error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", "", err
	}

	keyPath = filepath.Join(dir, "kiln.key")
	if err := os.WriteFile(keyPath, []byte(id.String()+"\n"), 0o600); err != nil {
		return "", "", err
	}

	// access = ["*"] grants every recipient in the file. A real kiln.toml
	// names the identities that may read each environment, which is how the
	// committed ciphertext stays readable by the right people only.
	configPath = filepath.Join(dir, "kiln.toml")
	doc := fmt.Sprintf(`[recipients]
deployer = %q

[files.%s]
filename = %q
access = ["*"]
`, id.Recipient().String(), envName, filepath.Join(dir, envName+".env"))
	if err := os.WriteFile(configPath, []byte(doc), 0o600); err != nil {
		return "", "", err
	}

	cfg, err := upstream.LoadConfig(configPath)
	if err != nil {
		return "", "", err
	}
	identity, err := upstream.NewIdentityFromKey(keyPath)
	if err != nil {
		return "", "", err
	}
	defer identity.Cleanup()

	encrypted := make(map[string][]byte, len(vars))
	for k, v := range vars {
		encrypted[k] = []byte(v)
	}
	if err := upstream.SetMultipleEnvironmentVars(identity, cfg, envName, encrypted); err != nil {
		return "", "", err
	}
	return configPath, keyPath, nil
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	dir, err := os.MkdirTemp("", "kiln-example")
	if err != nil {
		return err
	}
	// Cleanup failure is not actionable and must not mask the real result.
	defer func() { _ = os.RemoveAll(dir) }()

	configPath, keyPath, err := setup(dir, map[string]string{
		"HOST":     "kiln.internal",
		"PORT":     "8600",
		"PASSWORD": "s3cret-from-kiln",
	})
	if err != nil {
		return err
	}

	// Real usage: kiln.Env("kiln.toml", "production"). The key is DISCOVERED
	// the way the kiln CLI discovers it — ~/.kiln/kiln.key, ~/.ssh/id_ed25519
	// — so a developer who can already run `kiln` needs nothing more.
	// WithKeyPath here only points the example at its throwaway identity.
	cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
		kiln.Env(configPath, envName, kiln.WithKeyPath(keyPath)),
		cfgkit.FromEnviron(),
	))
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(w, "%s:%d\n", cfg.Host, cfg.Port)
	_, _ = fmt.Fprintln(w)
	if err := res.Explain(w); err != nil {
		return err
	}

	// A denied identity is an ERROR, never a miss. A source reporting "not
	// found" when it meant "not allowed" would let a deploy proceed with an
	// empty password, and nothing downstream could tell the difference.
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "with an identity the file does not grant:")
	intruder, err := age.GenerateX25519Identity()
	if err != nil {
		return err
	}
	wrongKey := filepath.Join(dir, "intruder.key")
	if err := os.WriteFile(wrongKey, []byte(intruder.String()+"\n"), 0o600); err != nil {
		return err
	}
	_, _, err = src(configPath, wrongKey).Lookup("PASSWORD")
	// Only the diagnosis is printed. The full message also names the config
	// path, which is a temp directory here and would differ on every run.
	_, _ = fmt.Fprintf(w, "  %s\n", diagnosis(err))
	return nil
}

// diagnosis returns the last colon-separated segment of an error message —
// the part that says what actually went wrong, without the path prefix the
// wrapping added.
func diagnosis(err error) string {
	if err == nil {
		return "<nil>"
	}
	parts := strings.Split(err.Error(), ": ")
	return parts[len(parts)-1]
}

// src builds a source for one identity, so the denial case reads clearly.
func src(configPath, keyPath string) cfgkit.Source {
	return kiln.Env(configPath, envName, kiln.WithKeyPath(keyPath))
}
