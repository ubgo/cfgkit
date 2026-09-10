// Command read-azkeyvault reads a secret from Azure Key Vault.
//
// It RUNS with no Azure subscription: the example stands up a fake vault and a
// fake IMDS endpoint. Faking both matters — on Azure the credential comes from
// the instance metadata service, and that exchange is where integrations break.
//
// Like the GCP adapter and unlike the AWS ones, this carries NO dependencies:
// managed identity returns a finished bearer token from one URL, so the
// platform's own mechanism is enough.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/ubgo/cfgkit"
	az "github.com/ubgo/cfgkit/contrib/source-azurekeyvault"
)

// Config binds one field per secret: Key Vault stores one value per secret.
type Config struct {
	Host     string `env:"HOST" default:"localhost"`
	Password string `env:"PASSWORD" secret:"true"`
}

const secretName = "checkout-db-password"

// fakeIMDS is the instance metadata service. A request must carry Metadata:
// true, which is what stops a browser being tricked into fetching a token.
func fakeIMDS() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata") != "true" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"imds-token","expires_in":"3599"}`)
	}))
}

// fakeVault serves the secret itself.
func fakeVault(value string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"value": value})
	}))
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	vault := fakeVault("s3cret-from-azure")
	defer vault.Close()
	imds := fakeIMDS()
	defer imds.Close()

	// Real usage: az.Secret("checkout-db-password", "PASSWORD",
	// az.WithVault("acme-prod")). A bare vault NAME is expanded to the full
	// https://<name>.vault.azure.net URL, because that is how everyone refers
	// to a vault in practice.
	cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
		az.Secret(secretName, "PASSWORD",
			az.WithVault(vault.URL),
			az.WithIMDSEndpoint(imds.URL),
		),
		cfgkit.FromEnviron(),
	))
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(w, "host=%s password-loaded=%t\n", cfg.Host, cfg.Password != "")
	_, _ = fmt.Fprintln(w)
	return res.Explain(w)
}
