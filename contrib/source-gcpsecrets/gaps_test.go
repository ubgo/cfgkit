// Coverage for arms the behavioural suite does not reach: the default
// endpoints, and the metadata exchange failing. Nothing here touches the
// network — every request is intercepted by a transport.
package gcpsecrets_test

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/ubgo/cfgkit"
	gcp "github.com/ubgo/cfgkit/contrib/source-gcpsecrets"
)

// recordingTransport captures every URL asked for and refuses the request, so
// a test can assert WHERE the package would have gone without going there.
type recordingTransport struct {
	mu   sync.Mutex
	urls []string
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.urls = append(r.urls, req.URL.String())
	r.mu.Unlock()
	return nil, errors.New("intercepted: no network in tests")
}

func (r *recordingTransport) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.urls...)
}

// TestDefaultEndpointsAreGoogles pins the arm that fills in the real hosts
// when WithEndpoints is not given — the configuration every real deployment
// uses, and the one every other test overrides.
//
// Without this, the default could be wrong or empty and the whole suite would
// still pass, because every other test supplies its own endpoints.
func TestDefaultEndpointsAreGoogles(t *testing.T) {
	rt := &recordingTransport{}

	src := gcp.Secret("db-password", "PASSWORD",
		gcp.WithProject("acme"),
		gcp.WithClient(&http.Client{Transport: rt}),
	)

	// The read fails — the transport refuses — but the URL was built first.
	if _, _, err := src.Lookup("PASSWORD"); err == nil {
		t.Fatal("the intercepting transport did not cause a failure")
	}

	seen := rt.seen()
	if len(seen) == 0 {
		t.Fatal("no request was attempted")
	}
	// The default is the LINK-LOCAL IP rather than metadata.google.internal:
	// it resolves without DNS, so a workload with a broken resolver can still
	// obtain its credential.
	var hitMetadata bool
	for _, u := range seen {
		if strings.Contains(u, "169.254.169.254") {
			hitMetadata = true
		}
	}
	if !hitMetadata {
		t.Errorf("the default metadata endpoint was not used; saw %v", seen)
	}
}

// TestDefaultAPIEndpointIsUsedWithAnExplicitToken isolates the API host.
//
// Supplying a token skips the metadata exchange, so the only request left is
// the Secret Manager call itself — which must go to Google's real API host.
func TestDefaultAPIEndpointIsUsedWithAnExplicitToken(t *testing.T) {
	rt := &recordingTransport{}

	src := gcp.Secret("db-password", "PASSWORD",
		gcp.WithProject("acme"),
		gcp.WithToken("explicit-token"),
		gcp.WithClient(&http.Client{Transport: rt}),
	)

	if _, _, err := src.Lookup("PASSWORD"); err == nil {
		t.Fatal("the intercepting transport did not cause a failure")
	}

	seen := rt.seen()
	if len(seen) != 1 {
		t.Fatalf("expected exactly one request with an explicit token, saw %v", seen)
	}
	if !strings.Contains(seen[0], "secretmanager.googleapis.com") {
		t.Errorf("the default API endpoint was not used; saw %q", seen[0])
	}
	// The secret's name and version must both be in the path, or the request
	// would silently read the wrong thing.
	if !strings.Contains(seen[0], "db-password") || !strings.Contains(seen[0], "versions/latest") {
		t.Errorf("request path does not name the secret and version: %q", seen[0])
	}
}

// TestSourceStillReportsThroughLoad pins that a transport failure surfaces as
// an error from Load rather than a silent miss — the miss-versus-error rule,
// checked on the default-endpoint path too.
func TestSourceStillReportsThroughLoad(t *testing.T) {
	type Config struct {
		Password string `env:"PASSWORD" secret:"true"`
	}

	rt := &recordingTransport{}
	_, _, err := cfgkit.Load[Config](cfgkit.WithSources(
		gcp.Secret("db-password", "PASSWORD",
			gcp.WithProject("acme"),
			gcp.WithToken("t"),
			gcp.WithClient(&http.Client{Transport: rt}),
		),
	))
	if err == nil {
		t.Fatal("an unreachable secret store was treated as an unset value")
	}
}
