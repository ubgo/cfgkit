// The suite runs against httptest for BOTH endpoints — the Secret Manager API
// and the instance metadata server — so it needs no Google, no credentials and
// no network. That is a consequence of the module carrying no dependencies.
//
// THREE STATEMENTS ARE DELIBERATELY LEFT UNCOVERED:
//
//	gcp.go  two http.NewRequestWithContext error returns, in fetch and in
//	        metadataText. Both take a constant method and an already-parsed
//	        URL, so neither can fail.
//	gcp.go  the line that falls back to Secret Manager's real endpoint when no
//	        WithEndpoints is given. Covering it would mean a test that contacts
//	        googleapis.com — a third-party call in a unit suite, flaky and rude.
//	        A slightly lower number is the better trade.
package gcpsecrets_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/cfgkit"
	gcpsecrets "github.com/ubgo/cfgkit/contrib/source-gcpsecrets"
)

type appCfg struct {
	Host     string `env:"HOST" default:"localhost"`
	Password string `env:"DATABASE_PASSWORD" secret:"true"`
	APIKey   string `env:"STRIPE_KEY" secret:"true"`
}

// fakeGCP stands in for both Google endpoints, recording what was asked for.
type fakeGCP struct {
	api      *httptest.Server
	metadata *httptest.Server

	apiPath   string
	authHdr   string
	mdFlavor  string
	mdCalls   int
	apiCalls  int
	tokenBody string
	project   string
}

// newGCP serves one secret at the standard path, and a metadata server that
// hands out a token and a project id.
func newGCP(t *testing.T, wantPath string, status int, payload string) *fakeGCP {
	t.Helper()
	g := &fakeGCP{tokenBody: `{"access_token":"metadata-token","expires_in":3599}`, project: "md-project"}

	g.metadata = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mdCalls++
		g.mdFlavor = r.Header.Get("Metadata-Flavor")
		switch r.URL.Path {
		case "/computeMetadata/v1/instance/service-accounts/default/token":
			_, _ = w.Write([]byte(g.tokenBody))
		case "/computeMetadata/v1/project/project-id":
			_, _ = w.Write([]byte(g.project + "\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	g.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.apiCalls++
		g.apiPath = r.URL.Path
		g.authHdr = r.Header.Get("Authorization")
		if wantPath != "" && r.URL.Path != wantPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"payload": map[string]string{
					"data": base64.StdEncoding.EncodeToString([]byte(payload)),
				},
			})
		}
	}))
	t.Cleanup(g.api.Close)
	t.Cleanup(g.metadata.Close)
	return g
}

// opts wires a source at the fake endpoints.
func (g *fakeGCP) opts(extra ...gcpsecrets.Option) []gcpsecrets.Option {
	return append([]gcpsecrets.Option{gcpsecrets.WithEndpoints(g.api.URL, g.metadata.URL)}, extra...)
}

func TestSecretBinds(t *testing.T) {
	g := newGCP(t, "/v1/projects/md-project/secrets/db-password/versions/latest:access",
		http.StatusOK, "hunter2")

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("db-password", "DATABASE_PASSWORD", g.opts()...),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "hunter2" {
		t.Errorf("Password = %q", got.Password)
	}
	for _, f := range res.Fields() {
		if f.Path == "Password" && f.Source != "gcpsecrets:db-password" {
			t.Errorf("source = %q, want the secret named", f.Source)
		}
		if strings.Contains(f.Value, "hunter2") {
			t.Errorf("the secret reached Explain: %q", f.Value)
		}
	}
}

// TestSecretAnswersOnlyForItsKey is the property that makes composing several
// secrets work. One secret holds one value, so a source bound to
// DATABASE_PASSWORD must be silent about every other key rather than offering
// its payload to whatever asks first.
func TestSecretAnswersOnlyForItsKey(t *testing.T) {
	db := newGCP(t, "/v1/projects/md-project/secrets/db-password/versions/latest:access",
		http.StatusOK, "db-secret")
	stripe := newGCP(t, "/v1/projects/md-project/secrets/stripe-key/versions/latest:access",
		http.StatusOK, "sk_live")

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("db-password", "DATABASE_PASSWORD", db.opts()...),
		gcpsecrets.Secret("stripe-key", "STRIPE_KEY", stripe.opts()...),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "db-secret" {
		t.Errorf("Password = %q", got.Password)
	}
	if got.APIKey != "sk_live" {
		t.Errorf("APIKey = %q", got.APIKey)
	}
	if got.Host != "localhost" {
		t.Errorf("Host = %q, want the default — no secret claims that key", got.Host)
	}
}

// TestTokenComesFromTheMetadataServer pins the workload-identity path, which is
// the one every deployment inside GCP uses. The Metadata-Flavor header is
// required: without it the server rejects the request, and the failure would
// look like missing credentials rather than a malformed call.
func TestTokenComesFromTheMetadataServer(t *testing.T) {
	g := newGCP(t, "/v1/projects/md-project/secrets/db-password/versions/latest:access",
		http.StatusOK, "hunter2")

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("db-password", "DATABASE_PASSWORD", g.opts()...),
	)); err != nil {
		t.Fatal(err)
	}
	if g.authHdr != "Bearer metadata-token" {
		t.Errorf("Authorization = %q, want the metadata server's token", g.authHdr)
	}
	if g.mdFlavor != "Google" {
		t.Errorf("Metadata-Flavor = %q, want Google", g.mdFlavor)
	}
}

// TestExplicitTokenSkipsTheMetadataServer pins that WithToken avoids the extra
// round trips entirely, which is why it exists.
func TestExplicitTokenSkipsTheMetadataServer(t *testing.T) {
	g := newGCP(t, "/v1/projects/explicit/secrets/db-password/versions/latest:access",
		http.StatusOK, "hunter2")

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("db-password", "DATABASE_PASSWORD",
			g.opts(gcpsecrets.WithToken("given"), gcpsecrets.WithProject("explicit"))...),
	)); err != nil {
		t.Fatal(err)
	}
	if g.mdCalls != 0 {
		t.Errorf("made %d metadata calls, want none when both token and project are given", g.mdCalls)
	}
	if g.authHdr != "Bearer given" {
		t.Errorf("Authorization = %q", g.authHdr)
	}
}

// TestProjectResolutionOrder pins the three ways a project is found. Getting
// this wrong reads the right secret name from the wrong project, which is a 404
// that looks like a missing secret.
func TestProjectResolutionOrder(t *testing.T) {
	t.Run("WithProject wins", func(t *testing.T) {
		t.Setenv("GOOGLE_CLOUD_PROJECT", "from-env")
		g := newGCP(t, "/v1/projects/explicit/secrets/s/versions/latest:access", http.StatusOK, "v")
		if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
			gcpsecrets.Secret("s", "DATABASE_PASSWORD", g.opts(gcpsecrets.WithProject("explicit"))...),
		)); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("then the environment", func(t *testing.T) {
		t.Setenv("GOOGLE_CLOUD_PROJECT", "from-env")
		g := newGCP(t, "/v1/projects/from-env/secrets/s/versions/latest:access", http.StatusOK, "v")
		if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
			gcpsecrets.Secret("s", "DATABASE_PASSWORD", g.opts()...),
		)); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("then the metadata server", func(t *testing.T) {
		t.Setenv("GOOGLE_CLOUD_PROJECT", "")
		g := newGCP(t, "/v1/projects/md-project/secrets/s/versions/latest:access", http.StatusOK, "v")
		if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
			gcpsecrets.Secret("s", "DATABASE_PASSWORD", g.opts()...),
		)); err != nil {
			t.Fatal(err)
		}
		// The trailing newline the metadata server sends must be trimmed, or
		// the project lands in the URL path with a %0A on the end.
		if !strings.Contains(g.apiPath, "/projects/md-project/") {
			t.Errorf("path = %q, want the project trimmed", g.apiPath)
		}
	})
}

// TestVersionIsPinnable pins that a specific version reaches the URL. Rotation
// under a running fleet is exactly what pinning avoids, so it must work.
func TestVersionIsPinnable(t *testing.T) {
	g := newGCP(t, "/v1/projects/md-project/secrets/db-password/versions/7:access",
		http.StatusOK, "old-value")

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("db-password", "DATABASE_PASSWORD", g.opts(gcpsecrets.WithVersion("7"))...),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "old-value" {
		t.Errorf("Password = %q, want the pinned version's value", got.Password)
	}
}

// TestNamesAreEscaped pins that a name with a URL-significant character cannot
// change which resource is read. A secret name is caller-supplied, so it is
// escaped rather than trusted.
func TestNamesAreEscaped(t *testing.T) {
	g := newGCP(t, "", http.StatusNotFound, "")

	_ = cfgkit.Check[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("../../evil", "DATABASE_PASSWORD",
			g.opts(gcpsecrets.WithProject("p"), gcpsecrets.WithToken("t"))...),
	))
	if strings.Contains(g.apiPath, "/evil") && !strings.Contains(g.apiPath, "%2F") {
		t.Errorf("path = %q — a name with slashes must be escaped, not traverse the API", g.apiPath)
	}
}

func TestMissingSecretIsAnErrorByDefault(t *testing.T) {
	g := newGCP(t, "/v1/projects/md-project/secrets/present/versions/latest:access",
		http.StatusOK, "v")

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("absent", "DATABASE_PASSWORD", g.opts()...),
	))
	if err == nil {
		t.Fatal("want an error: the named secret does not exist")
	}
	if !strings.Contains(err.Error(), "Optional()") {
		t.Errorf("error %q should name the way to opt out", err)
	}
	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("want a *SourceError, got %T", err)
	}
}

func TestOptionalMissingSecretIsSilent(t *testing.T) {
	g := newGCP(t, "/v1/projects/md-project/secrets/present/versions/latest:access",
		http.StatusOK, "v")

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("absent", "DATABASE_PASSWORD", g.opts(gcpsecrets.Optional())...),
	))
	if err != nil {
		t.Fatalf("an optional missing secret must not fail: %v", err)
	}
	if got.Host != "localhost" {
		t.Errorf("Host = %q, want the default", got.Host)
	}
	// And it must not claim a key it cannot answer for.
	if u := res.Unknown(); len(u) != 0 {
		t.Errorf("Unknown() = %v, want empty — an absent optional secret lists no keys", u)
	}
}

func TestPermissionDeniedNamesTheRole(t *testing.T) {
	g := newGCP(t, "", http.StatusForbidden, "")

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("db-password", "DATABASE_PASSWORD",
			g.opts(gcpsecrets.WithProject("p"), gcpsecrets.WithToken("t"))...),
	))
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "secretAccessor") {
		t.Errorf("error %q should name the IAM role that fixes it", err)
	}
}

func TestUnauthorizedIsReported(t *testing.T) {
	g := newGCP(t, "", http.StatusUnauthorized, "")

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("s", "DATABASE_PASSWORD",
			g.opts(gcpsecrets.WithProject("p"), gcpsecrets.WithToken("bad"))...),
	))
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("want an unauthorized error, got %v", err)
	}
}

func TestUnexpectedStatusIsReported(t *testing.T) {
	g := newGCP(t, "", http.StatusInternalServerError, "")

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("s", "DATABASE_PASSWORD",
			g.opts(gcpsecrets.WithProject("p"), gcpsecrets.WithToken("t"))...),
	))
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("want the status reported, got %v", err)
	}
}

func TestMalformedPayloadIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"payload": map[string]string{"data": "!!!not-base64!!!"},
		})
	}))
	t.Cleanup(srv.Close)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("db-password", "DATABASE_PASSWORD",
			gcpsecrets.WithEndpoints(srv.URL, ""),
			gcpsecrets.WithProject("p"), gcpsecrets.WithToken("t")),
	))
	if err == nil {
		t.Fatal("want an error for a payload that is not valid base64")
	}
	if !strings.Contains(err.Error(), "db-password") {
		t.Errorf("error %q should name the secret", err)
	}
	if strings.Contains(err.Error(), "!!!not-base64!!!") {
		t.Errorf("error %q must not echo the payload", err)
	}
}

func TestMalformedJSONIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	t.Cleanup(srv.Close)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("s", "DATABASE_PASSWORD",
			gcpsecrets.WithEndpoints(srv.URL, ""),
			gcpsecrets.WithProject("p"), gcpsecrets.WithToken("t")),
	))
	if err == nil || !strings.Contains(err.Error(), "decoding") {
		t.Errorf("want a decode error, got %v", err)
	}
}

// TestNoMetadataServerSaysWhatItMeans pins the message a developer running
// outside GCP hits first. A connection failure to a link-local address tells
// them nothing; naming workload identity and the remedy does.
func TestNoMetadataServerSaysWhatItMeans(t *testing.T) {
	g := newGCP(t, "", http.StatusOK, "")
	dead := g.metadata.URL
	g.metadata.Close()

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("s", "DATABASE_PASSWORD", gcpsecrets.WithEndpoints(g.api.URL, dead)),
	))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"workload identity", "WithToken"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// TestMetadataWithoutATokenFails pins that a metadata server answering with no
// token is an error rather than an unauthenticated read failing later with a
// worse message.
func TestMetadataWithoutATokenFails(t *testing.T) {
	g := newGCP(t, "", http.StatusOK, "")
	g.tokenBody = `{}`

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("s", "DATABASE_PASSWORD", g.opts()...),
	))
	if err == nil || !strings.Contains(err.Error(), "no access token") {
		t.Errorf("want a missing-token error, got %v", err)
	}
}

func TestMetadataTokenNotJSONIsReported(t *testing.T) {
	g := newGCP(t, "", http.StatusOK, "")
	g.tokenBody = "not json"

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("s", "DATABASE_PASSWORD", g.opts()...),
	))
	if err == nil || !strings.Contains(err.Error(), "metadata") {
		t.Errorf("want a metadata decode error, got %v", err)
	}
}

// TestNoProjectAnywhereNamesTheRemedy pins the other half of credential
// discovery: a token but no project.
func TestNoProjectAnywhereNamesTheRemedy(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	// A metadata server that issues tokens but has no project id.
	md := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			_, _ = w.Write([]byte(`{"access_token":"t"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(md.Close)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("s", "DATABASE_PASSWORD", gcpsecrets.WithEndpoints("http://unused.invalid", md.URL)),
	))
	if err == nil {
		t.Fatal("want an error: there is no project anywhere")
	}
	for _, want := range []string{"WithProject", "GOOGLE_CLOUD_PROJECT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestInvalidEndpointIsReported(t *testing.T) {
	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("s", "DATABASE_PASSWORD",
			gcpsecrets.WithEndpoints("://not a url", ""),
			gcpsecrets.WithProject("p"), gcpsecrets.WithToken("t")),
	))
	if err == nil || !strings.Contains(err.Error(), "invalid API endpoint") {
		t.Errorf("want an invalid-endpoint error, got %v", err)
	}
}

func TestUnreachableAPIIsAnErrorNotAMiss(t *testing.T) {
	g := newGCP(t, "", http.StatusOK, "")
	dead := g.api.URL
	g.api.Close()

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("s", "DATABASE_PASSWORD",
			gcpsecrets.WithEndpoints(dead, g.metadata.URL)),
	))
	if err == nil {
		t.Fatal("want an error, not a silent fallback to defaults")
	}
}

func TestContextBoundsTheRead(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("s", "DATABASE_PASSWORD",
			gcpsecrets.WithEndpoints(srv.URL, srv.URL), gcpsecrets.WithContext(ctx)),
	))
	if err == nil {
		t.Fatal("want the read to fail once the context expires")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %v — the context did not bound the read", elapsed)
	}
}

func TestCustomClientIsUsed(t *testing.T) {
	g := newGCP(t, "/v1/projects/p/secrets/s/versions/latest:access", http.StatusOK, "v")

	used := false
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		used = true
		return http.DefaultTransport.RoundTrip(r)
	})}

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("s", "DATABASE_PASSWORD",
			g.opts(gcpsecrets.WithProject("p"), gcpsecrets.WithToken("t"),
				gcpsecrets.WithClient(client))...),
	)); err != nil {
		t.Fatal(err)
	}
	if !used {
		t.Error("the supplied client was not used, so its TLS configuration would be ignored")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestReadHappensOncePerSource is the cost contract. Every read is also a
// Secret Manager access charge and an audit log entry, so a per-key read would
// bill and log once per bound field.
func TestReadHappensOncePerSource(t *testing.T) {
	g := newGCP(t, "/v1/projects/p/secrets/s/versions/latest:access", http.StatusOK, "v")

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("s", "DATABASE_PASSWORD",
			g.opts(gcpsecrets.WithProject("p"), gcpsecrets.WithToken("t"))...),
	)); err != nil {
		t.Fatal(err)
	}
	if g.apiCalls != 1 {
		t.Errorf("made %d API calls, want exactly 1", g.apiCalls)
	}
}

func TestPrecedenceAgainstOtherSources(t *testing.T) {
	g := newGCP(t, "/v1/projects/p/secrets/s/versions/latest:access", http.StatusOK, "from-gcp")

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"DATABASE_PASSWORD": "placeholder", "HOST": "from-map"}),
		gcpsecrets.Secret("s", "DATABASE_PASSWORD",
			g.opts(gcpsecrets.WithProject("p"), gcpsecrets.WithToken("t"))...),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "from-gcp" {
		t.Errorf("Password = %q, want the later source to win", got.Password)
	}
	if got.Host != "from-map" {
		t.Errorf("Host = %q, want the earlier source where the later is silent", got.Host)
	}
}

// TestTruncatedMetadataReplyIsReported covers a real failure the other tests
// cannot produce: the connection drops part-way through the metadata server's
// reply. Reading it must fail rather than yield a half-token that then gets
// rejected by the API with a message about credentials.
func TestTruncatedMetadataReplyIsReported(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(brokenReader{}),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		gcpsecrets.Secret("s", "DATABASE_PASSWORD",
			gcpsecrets.WithEndpoints("http://api.invalid", "http://md.invalid"),
			gcpsecrets.WithClient(client)),
	))
	if err == nil {
		t.Fatal("want an error for a reply that cannot be read")
	}
	if !strings.Contains(err.Error(), "reading the metadata server") {
		t.Errorf("error %q should say the reply could not be read", err)
	}
}

// brokenReader fails part-way, the way a dropped connection does.
type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
