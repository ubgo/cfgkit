// The suite runs against httptest: no etcd, no network, no credentials. That is
// a consequence of using the HTTP gateway rather than gRPC — the whole client
// is net/http, so the whole client can be pointed at a fake.
//
// THREE STATEMENTS ARE DELIBERATELY LEFT UNCOVERED, and reaching them would
// need a test that constructs an impossible state:
//
//	etcd.go  two json.Marshal error returns — both marshal a map[string]string,
//	         which cannot fail.
//	etcd.go  http.NewRequestWithContext's error — a constant method with an
//	         already-parsed URL.
//
// All three are correct error handling for calls that happen not to fail.
// Deleting them would raise the number and remove a real path.
package etcd_test

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
	etcd "github.com/ubgo/cfgkit/contrib/source-etcd"
)

type appCfg struct {
	Host     string `env:"HOST" default:"localhost"`
	Port     int    `env:"PORT" default:"8080"`
	Password string `env:"PASSWORD" secret:"true"`
}

// cluster stands in for etcd's HTTP gateway, recording each request so a test
// can assert the range it asked for as well as the reply.
type cluster struct {
	*httptest.Server
	auth     string
	rangeReq map[string]string
	authReq  map[string]string
	calls    int
}

// kv builds one entry of a range reply, with etcd's base64 encoding.
func kv(key, value string) map[string]string {
	return map[string]string{
		"key":   base64.StdEncoding.EncodeToString([]byte(key)),
		"value": base64.StdEncoding.EncodeToString([]byte(value)),
	}
}

// newCluster serves /v3/kv/range with the given reply, and /v3/auth/authenticate
// with a token, which is the whole surface this package uses.
func newCluster(t *testing.T, status int, kvs []map[string]string) *cluster {
	t.Helper()
	c := &cluster{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/v3/auth/authenticate":
			c.authReq = map[string]string{}
			_ = json.Unmarshal(body, &c.authReq)
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "issued-token"})
		case "/v3/kv/range":
			c.calls++
			c.auth = r.Header.Get("Authorization")
			c.rangeReq = map[string]string{}
			_ = json.Unmarshal(body, &c.rangeReq)
			w.WriteHeader(status)
			if status == http.StatusOK {
				_ = json.NewEncoder(w).Encode(map[string]any{"kvs": kvs})
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(c.Close)
	return c
}

// decodeB64 is the inverse of what the source sends, so a test can assert the
// range it asked for in readable form.
func decodeB64(t *testing.T, s string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("not base64: %q", s)
	}
	return string(b)
}

// TestPrefixBinds also pins the property that makes the source usable: keys
// arrive RELATIVE to the prefix, so one struct binds from etcd and from a .env
// file with no second set of tags.
func TestPrefixBinds(t *testing.T) {
	c := newCluster(t, http.StatusOK, []map[string]string{
		kv("app/config/HOST", "etcd.internal"),
		kv("app/config/PORT", "9000"),
	})

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(c.URL)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "etcd.internal" || got.Port != 9000 {
		t.Errorf("cfg = %+v", got)
	}
	for _, f := range res.Fields() {
		if f.Path == "Host" && f.Source != "etcd:app/config" {
			t.Errorf("Host source = %q, want the prefix named", f.Source)
		}
	}
}

// TestRangeIsAPrefixScan pins how a prefix is expressed. etcd has no "prefix"
// parameter: a prefix scan is a range from the key to its SUCCESSOR, the same
// bytes with the last one incremented. Getting it wrong reads one key, or the
// whole keyspace, and neither failure announces itself.
func TestRangeIsAPrefixScan(t *testing.T) {
	c := newCluster(t, http.StatusOK, []map[string]string{kv("app/config/HOST", "svc")})

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(c.URL)),
	)); err != nil {
		t.Fatal(err)
	}

	if got, want := decodeB64(t, c.rangeReq["key"]), "app/config/"; got != want {
		t.Errorf("range start = %q, want %q", got, want)
	}
	if got, want := decodeB64(t, c.rangeReq["range_end"]), "app/config0"; got != want {
		// '/' + 1 == '0'. The successor of "app/config/" is "app/config0".
		t.Errorf("range end = %q, want %q — the successor of the prefix", got, want)
	}
}

func TestPrefixIsTrimmedConsistently(t *testing.T) {
	for _, prefix := range []string{"app/config", "app/config/", "/app/config", "/app/config/"} {
		t.Run(prefix, func(t *testing.T) {
			c := newCluster(t, http.StatusOK, []map[string]string{kv("app/config/HOST", "svc")})
			got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
				etcd.Prefix(prefix, etcd.WithAddress(c.URL)),
			))
			if err != nil {
				t.Fatal(err)
			}
			if got.Host != "svc" {
				t.Errorf("Host = %q for prefix %q", got.Host, prefix)
			}
		})
	}
}

// TestNestedKeysKeepTheirSeparator pins the documented flattening: a flat
// source cannot express a tree, and inventing a mapping would guess at
// something the caller never stated.
func TestNestedKeysKeepTheirSeparator(t *testing.T) {
	type nested struct {
		DBHost string `env:"db/HOST" default:"localhost"`
	}
	c := newCluster(t, http.StatusOK, []map[string]string{kv("app/db/HOST", "db.internal")})

	got, _, err := cfgkit.Load[nested](cfgkit.WithSources(
		etcd.Prefix("app", etcd.WithAddress(c.URL)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.DBHost != "db.internal" {
		t.Errorf("DBHost = %q", got.DBHost)
	}
}

// TestEmptyValueIsPresentNotMissing pins etcd's encoding quirk: the value field
// is OMITTED for an empty value. The key still exists, so it must bind as empty
// — a present empty value beats a default, an absent one does not.
func TestEmptyValueIsPresentNotMissing(t *testing.T) {
	c := newCluster(t, http.StatusOK, []map[string]string{
		{"key": base64.StdEncoding.EncodeToString([]byte("app/config/HOST"))},
	})

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(c.URL)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "" {
		t.Errorf("Host = %q, want empty — the key exists with no value", got.Host)
	}
}

func TestSecretIsMasked(t *testing.T) {
	c := newCluster(t, http.StatusOK, []map[string]string{kv("app/config/PASSWORD", "hunter2")})

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(c.URL)),
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

func TestBadBase64NamesTheKeyNotTheValue(t *testing.T) {
	c := newCluster(t, http.StatusOK, []map[string]string{{
		"key":   base64.StdEncoding.EncodeToString([]byte("app/config/PASSWORD")),
		"value": "!!!not-base64!!!",
	}})

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(c.URL)),
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

func TestBadBase64KeyIsReported(t *testing.T) {
	c := newCluster(t, http.StatusOK, []map[string]string{{"key": "!!!", "value": "eA=="}})

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(c.URL)),
	))
	if err == nil || !strings.Contains(err.Error(), "key") {
		t.Errorf("want a key decode error, got %v", err)
	}
}

// TestEmptyPrefixIsAnErrorByDefault pins the rule shared with the other remote
// sources. etcd answers 200 with no kvs rather than 404, so an empty result is
// the ONLY signal that a prefix holds nothing.
func TestEmptyPrefixIsAnErrorByDefault(t *testing.T) {
	c := newCluster(t, http.StatusOK, nil)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(c.URL)),
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
	c := newCluster(t, http.StatusOK, nil)

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(c.URL), etcd.Optional()),
	))
	if err != nil {
		t.Fatalf("an optional empty prefix must not fail: %v", err)
	}
	if got.Host != "localhost" || got.Port != 8080 {
		t.Errorf("cfg = %+v, want the compiled-in defaults", got)
	}
}

// TestCredentialsAreExchangedForAToken pins the two-request flow, and that the
// token reaches the range call. Without the second half, authentication appears
// to succeed and every read is unauthorized.
func TestCredentialsAreExchangedForAToken(t *testing.T) {
	c := newCluster(t, http.StatusOK, []map[string]string{kv("app/config/HOST", "svc")})

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(c.URL), etcd.WithCredentials("root", "pw")),
	)); err != nil {
		t.Fatal(err)
	}
	if c.authReq["name"] != "root" || c.authReq["password"] != "pw" {
		t.Errorf("auth request = %v, want the credentials", c.authReq)
	}
	if c.auth != "issued-token" {
		t.Errorf("Authorization = %q, want the issued token on the range call", c.auth)
	}
}

// TestExplicitTokenSkipsAuthentication pins that WithToken avoids the extra
// round trip, which is the reason it exists beside WithCredentials.
func TestExplicitTokenSkipsAuthentication(t *testing.T) {
	c := newCluster(t, http.StatusOK, []map[string]string{kv("app/config/HOST", "svc")})

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(c.URL), etcd.WithToken("given")),
	)); err != nil {
		t.Fatal(err)
	}
	if c.authReq != nil {
		t.Error("an explicit token must not trigger an authenticate call")
	}
	if c.auth != "given" {
		t.Errorf("Authorization = %q, want the given token", c.auth)
	}
}

// TestAuthenticationWithoutATokenFails pins that a server answering with no
// token is an error rather than an unauthenticated read that fails later with a
// worse message.
func TestAuthenticationWithoutATokenFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/auth/authenticate" {
			_ = json.NewEncoder(w).Encode(map[string]string{})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(srv.URL), etcd.WithCredentials("root", "pw")),
	))
	if err == nil || !strings.Contains(err.Error(), "no token") {
		t.Errorf("want a missing-token error, got %v", err)
	}
}

func TestUnauthorizedNamesTheOptions(t *testing.T) {
	c := newCluster(t, http.StatusUnauthorized, nil)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(c.URL)),
	))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"WithCredentials", "WithToken"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should name %q", err, want)
		}
	}
}

func TestPermissionDeniedNamesTheRole(t *testing.T) {
	c := newCluster(t, http.StatusForbidden, nil)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(c.URL)),
	))
	if err == nil || !strings.Contains(err.Error(), "role") {
		t.Errorf("want the role named, got %v", err)
	}
}

// TestGatewayDisabledSaysSo pins the failure unique to this transport. The
// gateway is the one thing a cluster can switch off, and its absence looks
// exactly like a wrong address unless the message says otherwise.
func TestGatewayDisabledSaysSo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(srv.URL)),
	))
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "grpc-gateway") {
		t.Errorf("error %q should name the gateway as the likely cause", err)
	}
}

func TestUnexpectedStatusIsReported(t *testing.T) {
	c := newCluster(t, http.StatusInternalServerError, nil)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(c.URL)),
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
		etcd.Prefix("app/config", etcd.WithAddress(srv.URL)),
	))
	if err == nil || !strings.Contains(err.Error(), "decoding") {
		t.Errorf("want a decode error, got %v", err)
	}
}

func TestUnreachableClusterIsAnErrorNotAMiss(t *testing.T) {
	c := newCluster(t, http.StatusOK, nil)
	addr := c.URL
	c.Close()

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(addr)),
	))
	if err == nil {
		t.Fatal("want an error, not a silent fallback to defaults")
	}
}

func TestKeysAreListedForUnknownDetection(t *testing.T) {
	c := newCluster(t, http.StatusOK, []map[string]string{
		kv("app/config/HOST", "svc"), kv("app/config/HSOT", "typo"),
	})

	_, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(c.URL)),
	))
	if err != nil {
		t.Fatal(err)
	}
	u := res.Unknown()
	if len(u) != 1 || u[0].Key != "HSOT" {
		t.Fatalf("Unknown() = %v, want just the typo", u)
	}
}

// TestOnlyTheFirstEndpointIsUsed pins the documented limit. This package makes
// one request and does no failover; silently trying the rest would hide a
// broken first node behind a working second one.
func TestOnlyTheFirstEndpointIsUsed(t *testing.T) {
	c := newCluster(t, http.StatusOK, []map[string]string{kv("app/config/HOST", "svc")})
	t.Setenv("ETCDCTL_ENDPOINTS", c.URL+",http://unused.invalid:2379")

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(etcd.Prefix("app/config")))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "svc" {
		t.Errorf("Host = %q", got.Host)
	}
}

func TestBareHostPortAddress(t *testing.T) {
	c := newCluster(t, http.StatusOK, []map[string]string{kv("app/config/HOST", "svc")})
	bare := strings.TrimPrefix(c.URL, "http://")

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(bare)),
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
		etcd.Prefix("app/config", etcd.WithAddress("http://not a url")),
	))
	if err == nil || !strings.Contains(err.Error(), "invalid etcd address") {
		t.Errorf("want an invalid-address error, got %v", err)
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
		etcd.Prefix("app/config", etcd.WithAddress(srv.URL), etcd.WithContext(ctx)),
	))
	if err == nil {
		t.Fatal("want the read to fail once the context expires")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %v — the context did not bound the read", elapsed)
	}
}

func TestCustomClientIsUsed(t *testing.T) {
	c := newCluster(t, http.StatusOK, []map[string]string{kv("app/config/HOST", "svc")})

	used := false
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		used = true
		return http.DefaultTransport.RoundTrip(r)
	})}

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(c.URL), etcd.WithClient(client)),
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
	c := newCluster(t, http.StatusOK, []map[string]string{
		kv("app/config/HOST", "svc"), kv("app/config/PORT", "9000"),
	})

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(c.URL)),
	)); err != nil {
		t.Fatal(err)
	}
	if c.calls != 1 {
		t.Errorf("made %d range calls, want exactly 1 — Lookup runs once per field", c.calls)
	}
}

func TestPrecedenceAgainstOtherSources(t *testing.T) {
	c := newCluster(t, http.StatusOK, []map[string]string{kv("app/config/HOST", "from-etcd")})

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"HOST": "from-map", "PORT": "1234"}),
		etcd.Prefix("app/config", etcd.WithAddress(c.URL)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "from-etcd" {
		t.Errorf("Host = %q, want the later source to win", got.Host)
	}
	if got.Port != 1234 {
		t.Errorf("Port = %d, want the earlier source where the later is silent", got.Port)
	}
}

// TestKeyEqualToThePrefixIsSkipped covers the key that is the prefix itself.
// Binding it would offer a value under an empty name.
func TestKeyEqualToThePrefixIsSkipped(t *testing.T) {
	c := newCluster(t, http.StatusOK, []map[string]string{
		kv("app/config/", "the-folder"),
		kv("app/config/HOST", "svc"),
	})

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(c.URL)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "svc" {
		t.Errorf("Host = %q", got.Host)
	}
	for _, u := range res.Unknown() {
		if u.Key == "" {
			t.Error("the prefix itself was bound as an empty key")
		}
	}
}

// TestAuthenticationFailureIsReported pins that a rejected login says so,
// rather than failing later on the read with a message about permissions.
func TestAuthenticationFailureIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/auth/authenticate" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithAddress(srv.URL), etcd.WithCredentials("root", "wrong")),
	))
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "authenticating with etcd") {
		t.Errorf("error %q should say the failure was during authentication", err)
	}
}

// TestDefaultAddressIsEtcdsOwn pins where a read goes when neither WithAddress
// nor ETCDCTL_ENDPOINTS says otherwise.
func TestDefaultAddressIsEtcdsOwn(t *testing.T) {
	t.Setenv("ETCDCTL_ENDPOINTS", "")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		etcd.Prefix("app/config", etcd.WithContext(ctx)),
	))
	if err == nil {
		t.Skip("something is listening on 127.0.0.1:2379 — a local etcd is running")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:2379") {
		t.Errorf("error %q should name etcd's default address", err)
	}
}
