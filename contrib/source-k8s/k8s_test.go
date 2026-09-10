// Every test here runs against an httptest server, so the suite needs no
// cluster, no kubeconfig and no network. That is a consequence of the module
// carrying no dependencies: the whole client is net/http, so the whole client
// can be pointed at a fake.
package k8s_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/cfgkit"
	k8s "github.com/ubgo/cfgkit/contrib/source-k8s"
)

type appCfg struct {
	Host     string `env:"HOST" default:"localhost"`
	Port     int    `env:"PORT" default:"8080"`
	Password string `env:"PASSWORD" secret:"true"`
}

// apiServer stands in for the Kubernetes API. It records what was asked for, so
// a test can assert the request as well as the response.
type apiServer struct {
	*httptest.Server
	path string
	auth string
}

// newAPI serves one resource at the standard path and 404s everything else,
// which is what a real API server does.
func newAPI(t *testing.T, wantPath string, status int, body any) *apiServer {
	t.Helper()
	api := &apiServer{}
	api.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.path, api.auth = r.URL.Path, r.Header.Get("Authorization")
		if r.URL.Path != wantPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}))
	t.Cleanup(api.Close)
	return api
}

// data builds the shape the API returns for a ConfigMap or Secret.
func data(kv map[string]string) map[string]any { return map[string]any{"data": kv} }

func TestConfigMapBinds(t *testing.T) {
	api := newAPI(t, "/api/v1/namespaces/prod/configmaps/app-config", http.StatusOK,
		data(map[string]string{"HOST": "svc.internal", "PORT": "9000"}))

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		k8s.ConfigMap("app-config", k8s.WithServer(api.URL), k8s.WithNamespace("prod")),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "svc.internal" || got.Port != 9000 {
		t.Errorf("cfg = %+v", got)
	}

	// Provenance must name the concrete resource, not the type, or Explain
	// cannot answer which object a value came from when two are in the list.
	for _, f := range res.Fields() {
		if f.Path == "Host" && f.Source != "k8s:configmaps/app-config" {
			t.Errorf("Host source = %q, want the named resource", f.Source)
		}
	}
}

// TestSecretValuesAreBase64Decoded pins the one difference between the two
// resource kinds. The API returns Secret values encoded; a caller must see the
// same plain strings a ConfigMap gives, or every field would need decoding.
func TestSecretValuesAreBase64Decoded(t *testing.T) {
	enc := base64.StdEncoding.EncodeToString([]byte("hunter2"))
	api := newAPI(t, "/api/v1/namespaces/prod/secrets/app-secrets", http.StatusOK,
		data(map[string]string{"PASSWORD": enc}))

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		k8s.Secret("app-secrets", k8s.WithServer(api.URL), k8s.WithNamespace("prod")),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "hunter2" {
		t.Errorf("Password = %q, want the decoded value", got.Password)
	}

	// And the field is marked secret, so it must not appear in provenance.
	for _, f := range res.Fields() {
		if strings.Contains(f.Value, "hunter2") {
			t.Errorf("the secret reached Explain output: %q", f.Value)
		}
	}
}

// TestSecretWithBadBase64NamesTheKeyNotTheValue pins that the diagnostic for a
// malformed Secret says which key is wrong without printing what it holds. An
// error message is a thing that gets logged.
func TestSecretWithBadBase64NamesTheKeyNotTheValue(t *testing.T) {
	api := newAPI(t, "/api/v1/namespaces/prod/secrets/app-secrets", http.StatusOK,
		data(map[string]string{"PASSWORD": "!!!not-base64!!!"}))

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		k8s.Secret("app-secrets", k8s.WithServer(api.URL), k8s.WithNamespace("prod")),
	))
	if err == nil {
		t.Fatal("want an error for a value that is not valid base64")
	}
	if !strings.Contains(err.Error(), "PASSWORD") {
		t.Errorf("error %q should name the offending key", err)
	}
	if strings.Contains(err.Error(), "!!!not-base64!!!") {
		t.Errorf("error %q must not echo the secret's raw value", err)
	}
}

// TestMissingResourceIsAnErrorByDefault pins the deliberate difference from
// FromFiles. A file path is often speculative; a named ConfigMap is a
// deployment contract, so its absence is a manifest mistake rather than a
// normal outcome — and starting on defaults instead would hide it.
func TestMissingResourceIsAnErrorByDefault(t *testing.T) {
	api := newAPI(t, "/api/v1/namespaces/prod/configmaps/present", http.StatusOK, data(nil))

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		k8s.ConfigMap("absent", k8s.WithServer(api.URL), k8s.WithNamespace("prod")),
	))
	if err == nil {
		t.Fatal("want an error: the named resource does not exist")
	}
	if !strings.Contains(err.Error(), "Optional()") {
		t.Errorf("error %q should name the way to opt out", err)
	}

	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("want a *SourceError, got %T", err)
	}
}

// TestOptionalMissingResourceIsSilent is the opt-out, and it must leave the
// compiled-in defaults standing rather than producing empty values.
func TestOptionalMissingResourceIsSilent(t *testing.T) {
	api := newAPI(t, "/api/v1/namespaces/prod/configmaps/present", http.StatusOK, data(nil))

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		k8s.ConfigMap("absent", k8s.WithServer(api.URL), k8s.WithNamespace("prod"), k8s.Optional()),
	))
	if err != nil {
		t.Fatalf("an optional missing resource must not fail: %v", err)
	}
	if got.Host != "localhost" || got.Port != 8080 {
		t.Errorf("cfg = %+v, want the compiled-in defaults", got)
	}
}

// TestForbiddenNamesTheFix pins the message for the single most common failure.
// "403" alone sends a reader to the wrong place; the RBAC rule is the fix.
func TestForbiddenNamesTheFix(t *testing.T) {
	api := newAPI(t, "/api/v1/namespaces/prod/configmaps/app-config", http.StatusForbidden, nil)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		k8s.ConfigMap("app-config", k8s.WithServer(api.URL), k8s.WithNamespace("prod")),
	))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"forbidden", "service account", "Role"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// TestUnreachableAPIIsAnErrorNotAMiss is the rule that separates this from a
// source that simply has no opinion. An unreachable API server must never look
// like an unset key: that difference is a deploy proceeding with an empty
// password.
func TestUnreachableAPIIsAnErrorNotAMiss(t *testing.T) {
	// A server that is closed immediately, so the connection is refused.
	api := newAPI(t, "/whatever", http.StatusOK, nil)
	addr := api.URL
	api.Close()

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		k8s.ConfigMap("app-config", k8s.WithServer(addr), k8s.WithNamespace("prod")),
	))
	if err == nil {
		t.Fatal("want an error, not a silent fallback to defaults")
	}
}

func TestUnexpectedStatusIsReported(t *testing.T) {
	api := newAPI(t, "/api/v1/namespaces/prod/configmaps/app-config",
		http.StatusInternalServerError, nil)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		k8s.ConfigMap("app-config", k8s.WithServer(api.URL), k8s.WithNamespace("prod")),
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
		k8s.ConfigMap("app-config", k8s.WithServer(srv.URL), k8s.WithNamespace("prod")),
	))
	if err == nil || !strings.Contains(err.Error(), "decoding") {
		t.Errorf("want a decode error, got %v", err)
	}
}

// TestResourceWithNoDataIsEmptyNotBroken pins that a valid resource holding no
// keys is a normal outcome. It must not look like a failed read.
func TestResourceWithNoDataIsEmptyNotBroken(t *testing.T) {
	api := newAPI(t, "/api/v1/namespaces/prod/configmaps/app-config", http.StatusOK,
		map[string]any{"metadata": map[string]any{"name": "app-config"}})

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		k8s.ConfigMap("app-config", k8s.WithServer(api.URL), k8s.WithNamespace("prod")),
	))
	if err != nil {
		t.Fatalf("a ConfigMap with no data is valid: %v", err)
	}
	if got.Host != "localhost" {
		t.Errorf("Host = %q, want the default", got.Host)
	}
}

// TestKeysAreListedForUnknownDetection pins that this source implements
// KeyLister. A ConfigMap's keys were written for THIS application, so one that
// matches no field is worth reporting — unlike the process environment, whose
// keys belong to the machine.
func TestKeysAreListedForUnknownDetection(t *testing.T) {
	api := newAPI(t, "/api/v1/namespaces/prod/configmaps/app-config", http.StatusOK,
		data(map[string]string{"HOST": "svc", "HSOT": "typo"}))

	_, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		k8s.ConfigMap("app-config", k8s.WithServer(api.URL), k8s.WithNamespace("prod")),
	))
	if err != nil {
		t.Fatal(err)
	}
	u := res.Unknown()
	if len(u) != 1 || u[0].Key != "HSOT" {
		t.Fatalf("Unknown() = %v, want just the typo", u)
	}
	if !strings.Contains(u[0].Source, "app-config") {
		t.Errorf("unknown key source = %q, want the resource named", u[0].Source)
	}
}

// TestBearerTokenIsSent pins that the credential reaches the request. Without
// it every read outside a proxy is a 401, and the failure would look like a
// cluster problem rather than a client one.
func TestBearerTokenIsSent(t *testing.T) {
	api := newAPI(t, "/api/v1/namespaces/prod/configmaps/app-config", http.StatusOK,
		data(map[string]string{"HOST": "svc"}))

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		k8s.ConfigMap("app-config",
			k8s.WithServer(api.URL), k8s.WithNamespace("prod"), k8s.WithToken("tok-123")),
	)); err != nil {
		t.Fatal(err)
	}
	if api.auth != "Bearer tok-123" {
		t.Errorf("Authorization = %q, want the bearer token", api.auth)
	}
}

// TestNoTokenOutsideACluster pins the documented development path: talking to
// `kubectl proxy`, where there is no service account token and demanding one
// would make the path impossible.
func TestNoTokenOutsideACluster(t *testing.T) {
	api := newAPI(t, "/api/v1/namespaces/default/configmaps/app-config", http.StatusOK,
		data(map[string]string{"HOST": "svc"}))

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		k8s.ConfigMap("app-config", k8s.WithServer(api.URL), k8s.WithNamespace("default")),
	)); err != nil {
		t.Fatalf("no token must not be an error outside a cluster: %v", err)
	}
	if api.auth != "" {
		t.Errorf("Authorization = %q, want none", api.auth)
	}
}

// TestNamespaceIsRequiredOutsideACluster pins that the failure names the
// remedy. Outside a pod there is no namespace file, and "no such file" alone
// would not tell anyone to pass WithNamespace.
//
// It points at an EMPTY directory rather than relying on the host lacking a
// service account, so the result does not depend on where the suite runs.
func TestNamespaceIsRequiredOutsideACluster(t *testing.T) {
	api := newAPI(t, "/api/v1/namespaces/x/configmaps/app-config", http.StatusOK, nil)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		k8s.ConfigMap("app-config",
			k8s.WithServer(api.URL), k8s.WithServiceAccountDir(t.TempDir())),
	))
	if err == nil {
		t.Fatal("want an error: there is no namespace to read outside a cluster")
	}
	if !strings.Contains(err.Error(), "WithNamespace") {
		t.Errorf("error %q should name the remedy", err)
	}
}

// TestInClusterServiceAccountIsUsed covers the path a real pod takes: the
// namespace and the bearer token both come from the projected volume, with no
// options passed at all beyond where that volume is.
//
// Without WithServiceAccountDir this path could only be exercised inside a
// cluster, which means in practice it would never be tested — and it is the
// path every production deployment actually uses.
func TestInClusterServiceAccountIsUsed(t *testing.T) {
	sa := t.TempDir()
	writeFile(t, filepath.Join(sa, "namespace"), "team-a\n")
	writeFile(t, filepath.Join(sa, "token"), "projected-token\n")

	api := newAPI(t, "/api/v1/namespaces/team-a/configmaps/app-config", http.StatusOK,
		data(map[string]string{"HOST": "svc.internal"}))

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		k8s.ConfigMap("app-config", k8s.WithServer(api.URL), k8s.WithServiceAccountDir(sa)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "svc.internal" {
		t.Errorf("Host = %q", got.Host)
	}
	// Both files are read, and trailing newlines are trimmed — kubelet writes
	// them, and a namespace of "team-a\n" would produce a 404 on a URL path.
	if api.auth != "Bearer projected-token" {
		t.Errorf("Authorization = %q, want the projected token trimmed", api.auth)
	}
}

// TestUnreadableTokenIsAnError pins that a token file which exists and cannot
// be read fails the load. An ABSENT token is fine — that is the kubectl-proxy
// path — but an unreadable one means a broken mount, and proceeding
// unauthenticated would turn it into a confusing 401 from the API server.
func TestUnreadableTokenIsAnError(t *testing.T) {
	sa := t.TempDir()
	writeFile(t, filepath.Join(sa, "namespace"), "team-a")
	// A directory where the token file belongs: present, unreadable.
	if err := os.Mkdir(filepath.Join(sa, "token"), 0o700); err != nil {
		t.Fatal(err)
	}

	api := newAPI(t, "/api/v1/namespaces/team-a/configmaps/app-config", http.StatusOK, nil)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		k8s.ConfigMap("app-config", k8s.WithServer(api.URL), k8s.WithServiceAccountDir(sa)),
	))
	if err == nil {
		t.Fatal("want an error: the token exists and cannot be read")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("error %q should say the token could not be read", err)
	}
}

// writeFile creates a file with the given contents.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidServerURLIsReported(t *testing.T) {
	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		k8s.ConfigMap("app-config", k8s.WithServer("://not a url"), k8s.WithNamespace("prod")),
	))
	if err == nil || !strings.Contains(err.Error(), "invalid API server") {
		t.Errorf("want an invalid-server error, got %v", err)
	}
}

// TestContextBoundsTheRead pins that a caller can cap a slow API server. A boot
// that hangs before the readiness probe is indistinguishable from one that is
// merely slow, and neither gets restarted.
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
		k8s.ConfigMap("app-config",
			k8s.WithServer(srv.URL), k8s.WithNamespace("prod"), k8s.WithContext(ctx)),
	))
	if err == nil {
		t.Fatal("want the read to fail once the context expires")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %v — the context did not bound the read", elapsed)
	}
}

// TestCustomClientIsUsed pins the TLS seam. A cluster whose CA is not in the
// image needs a client carrying it, and passing one must actually take effect.
func TestCustomClientIsUsed(t *testing.T) {
	api := newAPI(t, "/api/v1/namespaces/prod/configmaps/app-config", http.StatusOK,
		data(map[string]string{"HOST": "svc"}))

	used := false
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		used = true
		return http.DefaultTransport.RoundTrip(r)
	})}

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		k8s.ConfigMap("app-config",
			k8s.WithServer(api.URL), k8s.WithNamespace("prod"), k8s.WithClient(client)),
	)); err != nil {
		t.Fatal(err)
	}
	if !used {
		t.Error("the supplied client was not used, so its TLS configuration would be ignored")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestReadHappensOncePerSource is the cost contract. cfgkit calls Lookup once
// per bound field, so a source that dialled per key would turn a fifty-field
// configuration into fifty round-trips at boot.
func TestReadHappensOncePerSource(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(data(map[string]string{"HOST": "svc", "PORT": "9000"}))
	}))
	t.Cleanup(srv.Close)

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		k8s.ConfigMap("app-config", k8s.WithServer(srv.URL), k8s.WithNamespace("prod")),
	)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("made %d API calls, want exactly 1 — Lookup runs once per field", calls)
	}
}

// TestPrecedenceAgainstOtherSources pins that this composes like any other flat
// source: position in the list decides, nothing about being remote changes it.
func TestPrecedenceAgainstOtherSources(t *testing.T) {
	api := newAPI(t, "/api/v1/namespaces/prod/configmaps/app-config", http.StatusOK,
		data(map[string]string{"HOST": "from-k8s"}))

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"HOST": "from-map", "PORT": "1234"}),
		k8s.ConfigMap("app-config", k8s.WithServer(api.URL), k8s.WithNamespace("prod")),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "from-k8s" {
		t.Errorf("Host = %q, want the later source to win", got.Host)
	}
	if got.Port != 1234 {
		t.Errorf("Port = %d, want the earlier source where the later is silent", got.Port)
	}
}

// TestDefaultServerIsTheInClusterAddress pins where a read goes when no server
// is given, which is the case every production deployment uses. Getting this
// wrong would send every in-cluster read somewhere else, and no test that
// always passes WithServer would ever notice.
//
// It asserts the TARGET rather than a successful read: resolving
// kubernetes.default.svc from outside a cluster fails, and that failure names
// the address, which is the thing under test.
func TestDefaultServerIsTheInClusterAddress(t *testing.T) {
	sa := t.TempDir()
	writeFile(t, filepath.Join(sa, "namespace"), "team-a")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		k8s.ConfigMap("app-config", k8s.WithServiceAccountDir(sa), k8s.WithContext(ctx)),
	))
	if err == nil {
		t.Skip("kubernetes.default.svc resolved — this suite is running inside a cluster")
	}
	if !strings.Contains(err.Error(), "kubernetes.default.svc") {
		t.Errorf("error %q should name the in-cluster API address", err)
	}
}
