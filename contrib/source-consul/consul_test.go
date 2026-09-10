// The suite runs against httptest: no Consul agent, no network, no token. That
// is a consequence of the module carrying no dependencies — the whole client is
// net/http, so the whole client can be pointed at a fake.
package consul_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/cfgkit"
	consul "github.com/ubgo/cfgkit/contrib/source-consul"
)

type appCfg struct {
	Host     string `env:"HOST" default:"localhost"`
	Port     int    `env:"PORT" default:"8080"`
	Password string `env:"PASSWORD" secret:"true"`
}

// agent stands in for a Consul agent, recording the request so a test can
// assert the path, query and headers as well as the response.
type agent struct {
	*httptest.Server
	path  string
	query string
	token string
}

// pair builds one entry of Consul's reply: an absolute key with a base64 value.
func pair(key, value string) map[string]any {
	return map[string]any{"Key": key, "Value": base64.StdEncoding.EncodeToString([]byte(value))}
}

func newAgent(t *testing.T, wantPath string, status int, body any) *agent {
	t.Helper()
	a := &agent{}
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.path, a.query = r.URL.Path, r.URL.RawQuery
		a.token = r.Header.Get("X-Consul-Token")
		if wantPath != "" && r.URL.Path != wantPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}))
	t.Cleanup(a.Close)
	return a
}

// TestPrefixBinds also pins the property that makes this source usable at all:
// keys arrive RELATIVE to the prefix, so the same struct binds from Consul and
// from a .env file with no second set of tags.
func TestPrefixBinds(t *testing.T) {
	a := newAgent(t, "/v1/kv/app/config", http.StatusOK, []map[string]any{
		pair("app/config/HOST", "consul.internal"),
		pair("app/config/PORT", "9000"),
	})

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		consul.Prefix("app/config", consul.WithAddress(a.URL)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "consul.internal" || got.Port != 9000 {
		t.Errorf("cfg = %+v", got)
	}
	if !strings.Contains(a.query, "recurse=true") {
		t.Errorf("query = %q, want a recursive read", a.query)
	}
	for _, f := range res.Fields() {
		if f.Path == "Host" && f.Source != "consul:app/config" {
			t.Errorf("Host source = %q, want the prefix named", f.Source)
		}
	}
}

// TestPrefixIsTrimmedConsistently pins that a trailing or leading slash on the
// prefix changes nothing. An operator writing "app/config/" and one writing
// "/app/config" must get the same keys, or the same deployment behaves
// differently depending on a character nobody thinks about.
func TestPrefixIsTrimmedConsistently(t *testing.T) {
	for _, prefix := range []string{"app/config", "app/config/", "/app/config", "/app/config/"} {
		t.Run(prefix, func(t *testing.T) {
			a := newAgent(t, "/v1/kv/app/config", http.StatusOK, []map[string]any{
				pair("app/config/HOST", "consul.internal"),
			})
			got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
				consul.Prefix(prefix, consul.WithAddress(a.URL)),
			))
			if err != nil {
				t.Fatal(err)
			}
			if got.Host != "consul.internal" {
				t.Errorf("Host = %q for prefix %q", got.Host, prefix)
			}
		})
	}
}

// TestNestedKeysKeepTheirSeparator pins the documented flattening. A flat
// source has no way to express a tree, and inventing a mapping — turning
// db/HOST into DB_HOST, say — would guess at something the caller never stated.
func TestNestedKeysKeepTheirSeparator(t *testing.T) {
	type nested struct {
		DBHost string `env:"db/HOST" default:"localhost"`
	}
	a := newAgent(t, "/v1/kv/app", http.StatusOK, []map[string]any{
		pair("app/db/HOST", "db.internal"),
	})

	got, _, err := cfgkit.Load[nested](cfgkit.WithSources(
		consul.Prefix("app", consul.WithAddress(a.URL)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.DBHost != "db.internal" {
		t.Errorf("DBHost = %q, want the nested key addressable by its full relative path", got.DBHost)
	}
}

// TestFolderKeyIsSkipped covers Consul's own quirk: a nested tree reports its
// "folder" as a valueless key equal to the prefix. Binding it would offer an
// empty value under an empty name.
func TestFolderKeyIsSkipped(t *testing.T) {
	a := newAgent(t, "/v1/kv/app/config", http.StatusOK, []map[string]any{
		{"Key": "app/config/", "Value": nil},
		pair("app/config/HOST", "consul.internal"),
	})

	_, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		consul.Prefix("app/config", consul.WithAddress(a.URL)),
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range res.Unknown() {
		if u.Key == "" {
			t.Error("the folder key was bound as an empty key")
		}
	}
}

// TestValuelessKeyIsEmptyNotMissing pins that a key Consul reports with a null
// value is PRESENT and empty, not absent. The difference matters: a present
// empty value beats a default, and an absent one does not.
func TestValuelessKeyIsEmptyNotMissing(t *testing.T) {
	a := newAgent(t, "/v1/kv/app/config", http.StatusOK, []map[string]any{
		{"Key": "app/config/HOST", "Value": nil},
	})

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		consul.Prefix("app/config", consul.WithAddress(a.URL)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "" {
		t.Errorf("Host = %q, want empty — the key exists with no value, which beats the default", got.Host)
	}
}

func TestSecretIsMasked(t *testing.T) {
	a := newAgent(t, "/v1/kv/app/config", http.StatusOK, []map[string]any{
		pair("app/config/PASSWORD", "hunter2"),
	})

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		consul.Prefix("app/config", consul.WithAddress(a.URL)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "hunter2" {
		t.Errorf("Password = %q", got.Password)
	}
	for _, f := range res.Fields() {
		if strings.Contains(f.Value, "hunter2") {
			t.Errorf("the secret reached Explain: %q", f.Value)
		}
	}
}

// TestBadBase64NamesTheKeyNotTheValue pins that a corrupt value is reported by
// key name only. A Consul value may hold a credential, and an error message is
// a thing that gets logged.
func TestBadBase64NamesTheKeyNotTheValue(t *testing.T) {
	a := newAgent(t, "/v1/kv/app/config", http.StatusOK, []map[string]any{
		{"Key": "app/config/PASSWORD", "Value": "!!!not-base64!!!"},
	})

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		consul.Prefix("app/config", consul.WithAddress(a.URL)),
	))
	if err == nil {
		t.Fatal("want an error for a value that is not valid base64")
	}
	if !strings.Contains(err.Error(), "PASSWORD") {
		t.Errorf("error %q should name the offending key", err)
	}
	if strings.Contains(err.Error(), "!!!not-base64!!!") {
		t.Errorf("error %q must not echo the raw value", err)
	}
}

// TestEmptyPrefixIsAnErrorByDefault pins the rule shared with the other remote
// sources. Consul answers 404 for a prefix with nothing under it, so from the
// outside "empty" and "missing" are the same thing — and either is a
// deployment mistake when you named the prefix yourself.
func TestEmptyPrefixIsAnErrorByDefault(t *testing.T) {
	a := newAgent(t, "/v1/kv/populated", http.StatusOK, []map[string]any{})

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		consul.Prefix("empty", consul.WithAddress(a.URL)),
	))
	if err == nil {
		t.Fatal("want an error: nothing lives under this prefix")
	}
	if !strings.Contains(err.Error(), "Optional()") {
		t.Errorf("error %q should name the way to opt out", err)
	}
	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("want a *SourceError, got %T", err)
	}
}

func TestOptionalEmptyPrefixIsSilent(t *testing.T) {
	a := newAgent(t, "/v1/kv/populated", http.StatusOK, []map[string]any{})

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		consul.Prefix("empty", consul.WithAddress(a.URL), consul.Optional()),
	))
	if err != nil {
		t.Fatalf("an optional empty prefix must not fail: %v", err)
	}
	if got.Host != "localhost" || got.Port != 8080 {
		t.Errorf("cfg = %+v, want the compiled-in defaults", got)
	}
}

func TestPermissionDeniedNamesTheACL(t *testing.T) {
	a := newAgent(t, "", http.StatusForbidden, nil)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		consul.Prefix("app/config", consul.WithAddress(a.URL)),
	))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"permission denied", "ACL token"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestUnexpectedStatusIsReported(t *testing.T) {
	a := newAgent(t, "", http.StatusInternalServerError, nil)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		consul.Prefix("app/config", consul.WithAddress(a.URL)),
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
		consul.Prefix("app/config", consul.WithAddress(srv.URL)),
	))
	if err == nil || !strings.Contains(err.Error(), "decoding") {
		t.Errorf("want a decode error, got %v", err)
	}
}

// TestUnreachableAgentIsAnErrorNotAMiss is the rule that separates a broken KV
// store from one that simply has no opinion. The difference is a deploy
// proceeding with an empty password.
func TestUnreachableAgentIsAnErrorNotAMiss(t *testing.T) {
	a := newAgent(t, "", http.StatusOK, nil)
	addr := a.URL
	a.Close()

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		consul.Prefix("app/config", consul.WithAddress(addr)),
	))
	if err == nil {
		t.Fatal("want an error, not a silent fallback to defaults")
	}
}

func TestKeysAreListedForUnknownDetection(t *testing.T) {
	a := newAgent(t, "/v1/kv/app/config", http.StatusOK, []map[string]any{
		pair("app/config/HOST", "svc"),
		pair("app/config/HSOT", "typo"),
	})

	_, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		consul.Prefix("app/config", consul.WithAddress(a.URL)),
	))
	if err != nil {
		t.Fatal(err)
	}
	u := res.Unknown()
	if len(u) != 1 || u[0].Key != "HSOT" {
		t.Fatalf("Unknown() = %v, want just the typo", u)
	}
}

// TestBareHostPortAddress pins the convenience that avoids a genuinely
// confusing failure. CONSUL_HTTP_ADDR is conventionally written without a
// scheme, and "consul.service:8500" parses as a URL whose scheme is
// "consul.service" — producing an error about an unsupported protocol that
// names nothing a reader would recognise.
func TestBareHostPortAddress(t *testing.T) {
	a := newAgent(t, "/v1/kv/app/config", http.StatusOK, []map[string]any{
		pair("app/config/HOST", "svc"),
	})
	bare := strings.TrimPrefix(a.URL, "http://")

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		consul.Prefix("app/config", consul.WithAddress(bare)),
	))
	if err != nil {
		t.Fatalf("a bare host:port must work: %v", err)
	}
	if got.Host != "svc" {
		t.Errorf("Host = %q", got.Host)
	}
}

func TestInvalidAddressIsReported(t *testing.T) {
	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		consul.Prefix("app/config", consul.WithAddress("http://not a url")),
	))
	if err == nil || !strings.Contains(err.Error(), "invalid Consul address") {
		t.Errorf("want an invalid-address error, got %v", err)
	}
}

// TestAddressAndTokenFromEnvironment pins that the Consul CLI's own variables
// are honoured, which is how nearly every deployment already configures this.
func TestAddressAndTokenFromEnvironment(t *testing.T) {
	a := newAgent(t, "/v1/kv/app/config", http.StatusOK, []map[string]any{
		pair("app/config/HOST", "svc"),
	})
	t.Setenv("CONSUL_HTTP_ADDR", a.URL)
	t.Setenv("CONSUL_HTTP_TOKEN", "env-token")

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(consul.Prefix("app/config")))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "svc" {
		t.Errorf("Host = %q", got.Host)
	}
	if a.token != "env-token" {
		t.Errorf("X-Consul-Token = %q, want the environment's", a.token)
	}
}

func TestExplicitTokenBeatsTheEnvironment(t *testing.T) {
	a := newAgent(t, "/v1/kv/app/config", http.StatusOK, []map[string]any{
		pair("app/config/HOST", "svc"),
	})
	t.Setenv("CONSUL_HTTP_TOKEN", "env-token")

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		consul.Prefix("app/config", consul.WithAddress(a.URL), consul.WithToken("explicit")),
	)); err != nil {
		t.Fatal(err)
	}
	if a.token != "explicit" {
		t.Errorf("X-Consul-Token = %q, want the explicit one", a.token)
	}
}

func TestDatacenterReachesTheQuery(t *testing.T) {
	a := newAgent(t, "/v1/kv/app/config", http.StatusOK, []map[string]any{
		pair("app/config/HOST", "svc"),
	})

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		consul.Prefix("app/config", consul.WithAddress(a.URL), consul.WithDatacenter("dc2")),
	)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.query, "dc=dc2") {
		t.Errorf("query = %q, want the datacenter", a.query)
	}
}

func TestContextBoundsTheRead(t *testing.T) {
	// The handler blocks until the TEST releases it, not until the request
	// context is cancelled. Blocking on the request context leaves the
	// connection active when the client gives up, and httptest.Server.Close
	// then waits on it — a hang rather than a failure, which is the worst way
	// for a test to break in CI.
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
		consul.Prefix("app/config", consul.WithAddress(srv.URL), consul.WithContext(ctx)),
	))
	if err == nil {
		t.Fatal("want the read to fail once the context expires")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %v — the context did not bound the read", elapsed)
	}
}

func TestCustomClientIsUsed(t *testing.T) {
	a := newAgent(t, "/v1/kv/app/config", http.StatusOK, []map[string]any{
		pair("app/config/HOST", "svc"),
	})

	used := false
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		used = true
		return http.DefaultTransport.RoundTrip(r)
	})}

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		consul.Prefix("app/config", consul.WithAddress(a.URL), consul.WithClient(client)),
	)); err != nil {
		t.Fatal(err)
	}
	if !used {
		t.Error("the supplied client was not used, so its TLS configuration would be ignored")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReadHappensOncePerSource(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode([]map[string]any{
			pair("app/config/HOST", "svc"), pair("app/config/PORT", "9000"),
		})
	}))
	t.Cleanup(srv.Close)

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		consul.Prefix("app/config", consul.WithAddress(srv.URL)),
	)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("made %d reads, want exactly 1 — Lookup runs once per field", calls)
	}
}

func TestPrecedenceAgainstOtherSources(t *testing.T) {
	a := newAgent(t, "/v1/kv/app/config", http.StatusOK, []map[string]any{
		pair("app/config/HOST", "from-consul"),
	})

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"HOST": "from-map", "PORT": "1234"}),
		consul.Prefix("app/config", consul.WithAddress(a.URL)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "from-consul" {
		t.Errorf("Host = %q, want the later source to win", got.Host)
	}
	if got.Port != 1234 {
		t.Errorf("Port = %d, want the earlier source where the later is silent", got.Port)
	}
}

// TestDefaultAddressIsConsulsOwn pins where a read goes when neither
// WithAddress nor CONSUL_HTTP_ADDR says otherwise. Matching the CLI's default
// means a developer running `consul agent -dev` configures nothing — and a
// suite that always passes WithAddress would never notice if it changed.
func TestDefaultAddressIsConsulsOwn(t *testing.T) {
	t.Setenv("CONSUL_HTTP_ADDR", "")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		consul.Prefix("app/config", consul.WithContext(ctx)),
	))
	if err == nil {
		t.Skip("something is listening on 127.0.0.1:8500 — a local agent is running")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:8500") {
		t.Errorf("error %q should name Consul's default address", err)
	}
}
