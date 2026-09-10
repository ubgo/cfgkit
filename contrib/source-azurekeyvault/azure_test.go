// The suite runs against httptest for BOTH endpoints — the vault and the
// instance metadata service — so it needs no Azure, no credentials and no
// network. That is a consequence of the module carrying no dependencies.
//
// TWO STATEMENTS ARE DELIBERATELY LEFT UNCOVERED: the two
// http.NewRequestWithContext error returns, in fetch and in imdsToken. Both
// take a constant method and an already-parsed URL, so neither can fail. They
// are correct error handling for calls that happen not to.
package azurekeyvault_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/cfgkit"
	akv "github.com/ubgo/cfgkit/contrib/source-azurekeyvault"
)

type appCfg struct {
	Host     string `env:"HOST" default:"localhost"`
	Password string `env:"DATABASE_PASSWORD" secret:"true"`
	APIKey   string `env:"STRIPE_KEY" secret:"true"`
}

// fakeAzure stands in for the vault and the metadata service, recording what
// was asked for so a test can assert the request as well as the reply.
type fakeAzure struct {
	vault *httptest.Server
	imds  *httptest.Server

	vaultPath  string
	vaultQuery string
	authHdr    string
	imdsQuery  string
	imdsHdr    string
	imdsStatus int
	imdsBody   string
	imdsCalls  int
	vaultCalls int
}

func newAzure(t *testing.T, wantPath string, status int, value string) *fakeAzure {
	t.Helper()
	a := &fakeAzure{
		imdsStatus: http.StatusOK,
		imdsBody:   `{"access_token":"imds-token","expires_in":"3599"}`,
	}

	a.imds = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.imdsCalls++
		a.imdsQuery = r.URL.RawQuery
		a.imdsHdr = r.Header.Get("Metadata")
		w.WriteHeader(a.imdsStatus)
		_, _ = w.Write([]byte(a.imdsBody))
	}))
	a.vault = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.vaultCalls++
		a.vaultPath, a.vaultQuery = r.URL.Path, r.URL.RawQuery
		a.authHdr = r.Header.Get("Authorization")
		if wantPath != "" && r.URL.Path != wantPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]string{"value": value})
		}
	}))
	t.Cleanup(a.vault.Close)
	t.Cleanup(a.imds.Close)
	return a
}

// opts wires a source at the fake endpoints.
func (a *fakeAzure) opts(extra ...akv.Option) []akv.Option {
	return append([]akv.Option{
		akv.WithVault(a.vault.URL),
		akv.WithIMDSEndpoint(a.imds.URL),
	}, extra...)
}

func TestSecretBinds(t *testing.T) {
	a := newAzure(t, "/secrets/db-password", http.StatusOK, "hunter2")

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		akv.Secret("db-password", "DATABASE_PASSWORD", a.opts()...),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "hunter2" {
		t.Errorf("Password = %q", got.Password)
	}
	if !strings.Contains(a.vaultQuery, "api-version=7.4") {
		t.Errorf("query = %q, want the pinned api-version", a.vaultQuery)
	}
	for _, f := range res.Fields() {
		if f.Path == "Password" && f.Source != "azurekeyvault:db-password" {
			t.Errorf("source = %q, want the secret named", f.Source)
		}
		if strings.Contains(f.Value, "hunter2") {
			t.Errorf("the secret reached Explain: %q", f.Value)
		}
	}
}

// TestSecretAnswersOnlyForItsKey is what makes composing several secrets work.
// One secret holds one value, so a source bound to DATABASE_PASSWORD must be
// silent about every other key rather than offering its value to whatever asks
// first.
func TestSecretAnswersOnlyForItsKey(t *testing.T) {
	db := newAzure(t, "/secrets/db-password", http.StatusOK, "db-secret")
	stripe := newAzure(t, "/secrets/stripe-key", http.StatusOK, "sk_live")

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		akv.Secret("db-password", "DATABASE_PASSWORD", db.opts()...),
		akv.Secret("stripe-key", "STRIPE_KEY", stripe.opts()...),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "db-secret" || got.APIKey != "sk_live" {
		t.Errorf("cfg = %+v", got)
	}
	if got.Host != "localhost" {
		t.Errorf("Host = %q, want the default — no secret claims that key", got.Host)
	}
}

// TestTokenComesFromTheMetadataService pins the managed-identity path, which is
// what every deployment inside Azure uses. Two details are load-bearing: the
// Metadata header, without which IMDS rejects the call, and the RESOURCE, which
// must be the vault audience or the token is refused at the vault with a 401
// that looks like a permissions problem.
func TestTokenComesFromTheMetadataService(t *testing.T) {
	a := newAzure(t, "/secrets/db-password", http.StatusOK, "hunter2")

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		akv.Secret("db-password", "DATABASE_PASSWORD", a.opts()...),
	)); err != nil {
		t.Fatal(err)
	}
	if a.authHdr != "Bearer imds-token" {
		t.Errorf("Authorization = %q, want the metadata service's token", a.authHdr)
	}
	if a.imdsHdr != "true" {
		t.Errorf("Metadata header = %q, want true", a.imdsHdr)
	}
	if !strings.Contains(a.imdsQuery, "resource=https%3A%2F%2Fvault.azure.net") {
		t.Errorf("IMDS query = %q, want the vault audience", a.imdsQuery)
	}
	if !strings.Contains(a.imdsQuery, "api-version=2018-02-01") {
		t.Errorf("IMDS query = %q, want the pinned api-version", a.imdsQuery)
	}
}

func TestExplicitTokenSkipsTheMetadataService(t *testing.T) {
	a := newAzure(t, "/secrets/db-password", http.StatusOK, "hunter2")

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		akv.Secret("db-password", "DATABASE_PASSWORD", a.opts(akv.WithToken("given"))...),
	)); err != nil {
		t.Fatal(err)
	}
	if a.imdsCalls != 0 {
		t.Errorf("made %d metadata calls, want none when a token is given", a.imdsCalls)
	}
	if a.authHdr != "Bearer given" {
		t.Errorf("Authorization = %q", a.authHdr)
	}
}

// TestUserAssignedIdentityIsRequestable pins that WithClientID reaches IMDS.
// Without it, a resource with several user-assigned identities cannot say which
// one it means.
func TestUserAssignedIdentityIsRequestable(t *testing.T) {
	a := newAzure(t, "/secrets/s", http.StatusOK, "v")

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		akv.Secret("s", "DATABASE_PASSWORD", a.opts(akv.WithClientID("11111111-2222"))...),
	)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.imdsQuery, "client_id=11111111-2222") {
		t.Errorf("IMDS query = %q, want the client id", a.imdsQuery)
	}
}

// TestAmbiguousIdentityNamesTheOption pins the message for the failure this
// API's one real ambiguity produces. IMDS answers 400 when a resource has
// several user-assigned identities and none was chosen, and "400" alone tells
// nobody what to do.
func TestAmbiguousIdentityNamesTheOption(t *testing.T) {
	a := newAzure(t, "", http.StatusOK, "")
	a.imdsStatus = http.StatusBadRequest
	a.imdsBody = `{"error":"invalid_request"}`

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		akv.Secret("s", "DATABASE_PASSWORD", a.opts()...),
	))
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "WithClientID") {
		t.Errorf("error %q should name the option that resolves the ambiguity", err)
	}
}

// TestBareVaultNameIsExpanded pins the convenience Azure's own tooling implies:
// vaults are referred to by name everywhere, so a name must work.
//
// It asserts the expansion through a FAKE TRANSPORT rather than by letting a
// request go out. The first version of this test reached the real
// my-vault.vault.azure.net and asserted on the 401 that came back — a live call
// to a third party from a unit suite, which is the thing this package's own
// README criticises. A transport records the URL and answers locally.
func TestBareVaultNameIsExpanded(t *testing.T) {
	var gotHost, gotPath string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotHost, gotPath = r.URL.Host, r.URL.Path
		return jsonResponse(r, `{"value":"v"}`), nil
	})}

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		akv.Secret("db-password", "DATABASE_PASSWORD",
			akv.WithVault("my-vault"), akv.WithToken("t"), akv.WithClient(client)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if gotHost != "my-vault.vault.azure.net" {
		t.Errorf("host = %q, want the bare name expanded", gotHost)
	}
	if gotPath != "/secrets/db-password" {
		t.Errorf("path = %q", gotPath)
	}
	if got.Password != "v" {
		t.Errorf("Password = %q", got.Password)
	}
}

// jsonResponse builds a canned 200 for a fake transport.
func jsonResponse(r *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Request:    r,
	}
}

// TestFullVaultURLIsUsedAsGiven is the other half, and it is what sovereign
// clouds need: their vault suffix is not vault.azure.net, so a full URL must
// pass through untouched.
func TestFullVaultURLIsUsedAsGiven(t *testing.T) {
	a := newAzure(t, "/secrets/s", http.StatusOK, "v")

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		akv.Secret("s", "DATABASE_PASSWORD", a.opts()...),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "v" {
		t.Errorf("Password = %q", got.Password)
	}
}

func TestNoVaultNamesTheOption(t *testing.T) {
	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		akv.Secret("s", "DATABASE_PASSWORD", akv.WithToken("t")),
	))
	if err == nil {
		t.Fatal("want an error: there is no vault to read from")
	}
	if !strings.Contains(err.Error(), "WithVault") {
		t.Errorf("error %q should name the option", err)
	}
}

func TestInvalidVaultIsReported(t *testing.T) {
	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		akv.Secret("s", "DATABASE_PASSWORD", akv.WithVault("https://not a url"), akv.WithToken("t")),
	))
	if err == nil || !strings.Contains(err.Error(), "invalid vault") {
		t.Errorf("want an invalid-vault error, got %v", err)
	}
}

// TestVersionIsPinnable pins that a version becomes a path segment, which is
// how Key Vault addresses one — not a query parameter.
func TestVersionIsPinnable(t *testing.T) {
	a := newAzure(t, "/secrets/db-password/abc123", http.StatusOK, "old-value")

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		akv.Secret("db-password", "DATABASE_PASSWORD", a.opts(akv.WithVersion("abc123"))...),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "old-value" {
		t.Errorf("Password = %q, want the pinned version's value", got.Password)
	}
}

// TestNamesAreEscaped pins that a caller-supplied name cannot change which
// resource is read.
func TestNamesAreEscaped(t *testing.T) {
	a := newAzure(t, "", http.StatusNotFound, "")

	_ = cfgkit.Check[appCfg](cfgkit.WithSources(
		akv.Secret("../../evil", "DATABASE_PASSWORD", a.opts(akv.WithToken("t"))...),
	))
	if strings.Contains(a.vaultPath, "/evil") && !strings.Contains(a.vaultPath, "%2F") {
		t.Errorf("path = %q — a name with slashes must be escaped", a.vaultPath)
	}
}

func TestMissingSecretIsAnErrorByDefault(t *testing.T) {
	a := newAzure(t, "/secrets/present", http.StatusOK, "v")

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		akv.Secret("absent", "DATABASE_PASSWORD", a.opts()...),
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
	a := newAzure(t, "/secrets/present", http.StatusOK, "v")

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		akv.Secret("absent", "DATABASE_PASSWORD", a.opts(akv.Optional())...),
	))
	if err != nil {
		t.Fatalf("an optional missing secret must not fail: %v", err)
	}
	if got.Host != "localhost" {
		t.Errorf("Host = %q, want the default", got.Host)
	}
	if u := res.Unknown(); len(u) != 0 {
		t.Errorf("Unknown() = %v, want empty — an absent optional secret lists no keys", u)
	}
}

// TestForbiddenNamesBothPermissionModels pins the message for the most common
// Azure failure. Key Vault has TWO permission systems — access policies and
// RBAC — and a reader who knows only one will look in the wrong place.
func TestForbiddenNamesBothPermissionModels(t *testing.T) {
	a := newAzure(t, "", http.StatusForbidden, "")

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		akv.Secret("s", "DATABASE_PASSWORD", a.opts(akv.WithToken("t"))...),
	))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"access policy", "Key Vault Secrets User"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// TestUnauthorizedMentionsTheAudience pins the hint for a genuinely confusing
// failure: a token for the wrong audience is syntactically valid, so the vault
// rejects it with a 401 that reads as a permissions problem.
func TestUnauthorizedMentionsTheAudience(t *testing.T) {
	a := newAzure(t, "", http.StatusUnauthorized, "")

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		akv.Secret("s", "DATABASE_PASSWORD", a.opts(akv.WithToken("wrong-audience"))...),
	))
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "vault.azure.net") {
		t.Errorf("error %q should name the audience the token must carry", err)
	}
}

func TestUnexpectedStatusIsReported(t *testing.T) {
	a := newAzure(t, "", http.StatusInternalServerError, "")

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		akv.Secret("s", "DATABASE_PASSWORD", a.opts(akv.WithToken("t"))...),
	))
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("want the status reported, got %v", err)
	}
}

func TestMalformedJSONIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	t.Cleanup(srv.Close)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		akv.Secret("s", "DATABASE_PASSWORD", akv.WithVault(srv.URL), akv.WithToken("t")),
	))
	if err == nil || !strings.Contains(err.Error(), "decoding") {
		t.Errorf("want a decode error, got %v", err)
	}
}

// TestNoMetadataServiceSaysWhatItMeans pins the message a developer running
// outside Azure hits first. A connection failure to a link-local address tells
// them nothing they can act on.
func TestNoMetadataServiceSaysWhatItMeans(t *testing.T) {
	a := newAzure(t, "", http.StatusOK, "")
	dead := a.imds.URL
	a.imds.Close()

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		akv.Secret("s", "DATABASE_PASSWORD",
			akv.WithVault(a.vault.URL), akv.WithIMDSEndpoint(dead)),
	))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"managed identity", "WithToken"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestMetadataWithoutATokenFails(t *testing.T) {
	a := newAzure(t, "", http.StatusOK, "")
	a.imdsBody = `{}`

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		akv.Secret("s", "DATABASE_PASSWORD", a.opts()...),
	))
	if err == nil || !strings.Contains(err.Error(), "no access token") {
		t.Errorf("want a missing-token error, got %v", err)
	}
}

func TestMetadataNotJSONIsReported(t *testing.T) {
	a := newAzure(t, "", http.StatusOK, "")
	a.imdsBody = "not json"

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		akv.Secret("s", "DATABASE_PASSWORD", a.opts()...),
	))
	if err == nil || !strings.Contains(err.Error(), "metadata service") {
		t.Errorf("want a metadata decode error, got %v", err)
	}
}

func TestMetadataUnexpectedStatusIsReported(t *testing.T) {
	a := newAzure(t, "", http.StatusOK, "")
	a.imdsStatus = http.StatusInternalServerError

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		akv.Secret("s", "DATABASE_PASSWORD", a.opts()...),
	))
	if err == nil || !strings.Contains(err.Error(), "metadata service returned") {
		t.Errorf("want the metadata status reported, got %v", err)
	}
}

func TestInvalidIMDSEndpointIsReported(t *testing.T) {
	a := newAzure(t, "", http.StatusOK, "")

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		akv.Secret("s", "DATABASE_PASSWORD",
			akv.WithVault(a.vault.URL), akv.WithIMDSEndpoint("://not a url")),
	))
	if err == nil || !strings.Contains(err.Error(), "invalid IMDS endpoint") {
		t.Errorf("want an invalid-endpoint error, got %v", err)
	}
}

func TestUnreachableVaultIsAnErrorNotAMiss(t *testing.T) {
	a := newAzure(t, "", http.StatusOK, "")
	dead := a.vault.URL
	a.vault.Close()

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		akv.Secret("s", "DATABASE_PASSWORD", akv.WithVault(dead), akv.WithToken("t")),
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

	start := time.Now()
	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		akv.Secret("s", "DATABASE_PASSWORD",
			akv.WithVault(srv.URL), akv.WithToken("t"),
			akv.WithContext(deadline(t, 50*time.Millisecond))),
	))
	if err == nil {
		t.Fatal("want the read to fail once the context expires")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %v — the context did not bound the read", elapsed)
	}
}

func TestCustomClientIsUsed(t *testing.T) {
	a := newAzure(t, "/secrets/s", http.StatusOK, "v")

	used := false
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		used = true
		return http.DefaultTransport.RoundTrip(r)
	})}

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		akv.Secret("s", "DATABASE_PASSWORD", a.opts(akv.WithToken("t"), akv.WithClient(client))...),
	)); err != nil {
		t.Fatal(err)
	}
	if !used {
		t.Error("the supplied client was not used, so its TLS configuration would be ignored")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestReadHappensOncePerSource is the cost contract: every read is also an
// Azure Monitor entry and a billed operation.
func TestReadHappensOncePerSource(t *testing.T) {
	a := newAzure(t, "/secrets/s", http.StatusOK, "v")

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		akv.Secret("s", "DATABASE_PASSWORD", a.opts(akv.WithToken("t"))...),
	)); err != nil {
		t.Fatal(err)
	}
	if a.vaultCalls != 1 {
		t.Errorf("made %d vault calls, want exactly 1", a.vaultCalls)
	}
}

func TestPrecedenceAgainstOtherSources(t *testing.T) {
	a := newAzure(t, "/secrets/s", http.StatusOK, "from-azure")

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"DATABASE_PASSWORD": "placeholder", "HOST": "from-map"}),
		akv.Secret("s", "DATABASE_PASSWORD", a.opts(akv.WithToken("t"))...),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "from-azure" {
		t.Errorf("Password = %q, want the later source to win", got.Password)
	}
	if got.Host != "from-map" {
		t.Errorf("Host = %q, want the earlier source where the later is silent", got.Host)
	}
}

// deadline returns a context bounded to d, cleaned up with the test.
func deadline(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

// TestTruncatedMetadataReplyIsReported covers a real failure the fake servers
// cannot produce: the connection drops part-way through the metadata reply.
// Reading it must fail rather than yield a half-token that the vault then
// rejects with a message about credentials.
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
		akv.Secret("s", "DATABASE_PASSWORD",
			akv.WithVault("https://vault.invalid"),
			akv.WithIMDSEndpoint("http://imds.invalid"),
			akv.WithClient(client)),
	))
	if err == nil {
		t.Fatal("want an error for a reply that cannot be read")
	}
	if !strings.Contains(err.Error(), "reading the metadata service") {
		t.Errorf("error %q should say the reply could not be read", err)
	}
}

// brokenReader fails part-way, the way a dropped connection does.
type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
