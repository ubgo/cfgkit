// Command read-gcpsecrets reads a secret from Google Secret Manager.
//
// It RUNS with no GCP project: the example stands up a fake API and a fake
// metadata server. Faking BOTH is the point — on GCP the credential comes from
// the metadata server, and that exchange is where a real integration breaks.
//
// This adapter carries NO dependencies, unlike the AWS ones, and the reason is
// the rule the catalogue follows: workload identity hands back a finished
// bearer token from one URL, where AWS needs a credential chain plus SigV4.
// When the credential is a header, the platform's own mechanism is enough.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/ubgo/cfgkit"
	gcp "github.com/ubgo/cfgkit/contrib/source-gcpsecrets"
)

// Config binds one field per secret: Secret Manager stores ONE value per
// secret, so the mapping is stated rather than guessed.
type Config struct {
	Host     string `env:"HOST" default:"localhost"`
	Password string `env:"PASSWORD" secret:"true"`
}

const (
	project    = "acme-prod"
	secretName = "checkout-db-password"
)

// fakeMetadata is the instance metadata server, which answers with a finished
// access token. Requests must carry Metadata-Flavor: Google.
func fakeMetadata() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata-Flavor") != "Google" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"metadata-token","expires_in":3599}`)
	}))
}

// fakeAPI is Secret Manager. The payload is base64 because a secret is
// arbitrary bytes on the wire.
func fakeAPI(value string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"payload": map[string]string{
				"data": base64.StdEncoding.EncodeToString([]byte(value)),
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
	api := fakeAPI("s3cret-from-gcp")
	defer api.Close()
	md := fakeMetadata()
	defer md.Close()

	// Real usage on GCP is one line — gcp.Secret("checkout-db-password",
	// "PASSWORD") — because the project and the token both come from the
	// metadata server the workload is already running against.
	cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
		gcp.Secret(secretName, "PASSWORD",
			gcp.WithProject(project),
			gcp.WithEndpoints(api.URL, md.URL),
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
