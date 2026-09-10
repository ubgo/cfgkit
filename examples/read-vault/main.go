// Command read-vault reads a secret from HashiCorp Vault's KV engine.
//
// It RUNS with no Vault installed, because the example starts a fake one
// in-process. That is possible for the same reason the adapter has no
// dependencies: the whole client is net/http, so the whole client can be
// pointed somewhere else. An example that only compiles is a code listing.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/ubgo/cfgkit"
	vault "github.com/ubgo/cfgkit/contrib/source-vault"
)

// Config is the program's contract. Nothing about it mentions Vault — that is
// the point of a Source: swapping where a value comes from is a change at the
// call site, not in the type.
type Config struct {
	Host     string `env:"HOST" default:"localhost"`
	Port     int    `env:"PORT" default:"8080"`
	Password string `env:"PASSWORD" secret:"true"`
}

// secretPath is the KV path this program reads.
//
// Named once because it appears in the request, in the SOURCE column and in
// any error — three places that must agree.
const secretPath = "app/config"

// fakeVault stands in for a real server, speaking the KV v2 reply shape.
//
// KV v2 nests values one level deeper than v1, beside their metadata. Getting
// that shape wrong is the single most common Vault integration bug, which is
// why the adapter reports a v2/v1 mismatch by name rather than as a 404.
func fakeVault() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"data": map[string]any{
					"HOST":     "vault.internal",
					"PORT":     "9000",
					"PASSWORD": "s3cret-from-vault",
				},
				"metadata": map[string]any{"version": 3},
			},
		})
	}))
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	srv := fakeVault()
	defer srv.Close()

	// In a real program this is the whole integration:
	//
	//	vault.Secret("app/config")
	//
	// The address and token are discovered the way the Vault CLI discovers
	// them — VAULT_ADDR, VAULT_TOKEN, ~/.vault-token — so a developer who can
	// already run `vault kv get` needs no extra configuration. The two options
	// below exist only to point this example at its fake.
	cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
		vault.Secret(secretPath,
			vault.WithAddress(srv.URL),
			vault.WithToken("example-token"),
		),
		cfgkit.FromEnviron(), // still wins
	))
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(w, "%s:%d\n", cfg.Host, cfg.Port)
	_, _ = fmt.Fprintln(w)
	return res.Explain(w)
}
