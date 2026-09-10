// The suite runs against httptest, so it needs no Vault, no network and no
// token. That is a consequence of the module carrying no dependencies: the
// whole client is net/http, so the whole client can be pointed at a fake.
//
// TWO STATEMENTS ARE DELIBERATELY LEFT UNCOVERED, and claiming 100% by
// contorting a test around them would be the dishonest kind:
//
//	vault.go  http.NewRequestWithContext's error return — the method is a
//	          constant and the URL is already parsed, so it cannot fail.
//	vault.go  json.Marshal's error in stringify — the value being re-encoded
//	          came out of json.Unmarshal moments earlier, so it is marshalable
//	          by construction.
//
// Both are correct error handling for calls that happen not to fail here.
// Deleting them would raise the number and remove a real path.
package vault_test

import (
	"context"
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
	vault "github.com/ubgo/cfgkit/contrib/source-vault"
)

type appCfg struct {
	Host     string `env:"HOST" default:"localhost"`
	Port     int    `env:"PORT" default:"8080"`
	Debug    bool   `env:"DEBUG"`
	Password string `env:"PASSWORD" secret:"true"`
}

// vaultServer stands in for Vault, recording the request so a test can assert
// the path and headers as well as the response.
type vaultServer struct {
	*httptest.Server
	path  string
	token string
	ns    string
}

func newVault(t *testing.T, wantPath string, status int, body any) *vaultServer {
	t.Helper()
	v := &vaultServer{}
	v.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v.path = r.URL.Path
		v.token = r.Header.Get("X-Vault-Token")
		v.ns = r.Header.Get("X-Vault-Namespace")
		if wantPath != "" && r.URL.Path != wantPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}))
	t.Cleanup(v.Close)
	return v
}

// kv2 builds the reply shape of the current KV engine: values nested one level
// deeper, beside their metadata.
func kv2(kv map[string]any) map[string]any {
	return map[string]any{"data": map[string]any{
		"data":     kv,
		"metadata": map[string]any{"version": 3},
	}}
}

// kv1 builds the legacy engine's flatter reply.
func kv1(kv map[string]any) map[string]any { return map[string]any{"data": kv} }

// withToken keeps every test from having to think about credential discovery.
func withToken(opts ...vault.Option) []vault.Option {
	return append([]vault.Option{vault.WithToken("test-token")}, opts...)
}

func TestKV2Binds(t *testing.T) {
	srv := newVault(t, "/v1/secret/data/app/config", http.StatusOK,
		kv2(map[string]any{"HOST": "vault.internal", "PORT": "9000"}))

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", withToken(vault.WithAddress(srv.URL))...),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "vault.internal" || got.Port != 9000 {
		t.Errorf("cfg = %+v", got)
	}
	for _, f := range res.Fields() {
		if f.Path == "Host" && f.Source != "vault:secret/app/config" {
			t.Errorf("Host source = %q, want the named path", f.Source)
		}
	}
}

// TestKV1Binds pins the legacy engine, whose path has no "data" segment and
// whose reply is one level flatter. Both differences have to be right together
// or the read 404s.
func TestKV1Binds(t *testing.T) {
	srv := newVault(t, "/v1/secret/app/config", http.StatusOK,
		kv1(map[string]any{"HOST": "legacy.internal"}))

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", withToken(
			vault.WithAddress(srv.URL), vault.WithKVVersion(vault.KV1))...),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "legacy.internal" {
		t.Errorf("Host = %q", got.Host)
	}
}

// TestWrongKVVersionSaysSo pins the diagnostic for the mistake this API's one
// required decision invites. Reading a KV1 mount as KV2 gives a 404 that looks
// like a missing secret, so the message has to name the other possibility.
func TestWrongKVVersionSaysSo(t *testing.T) {
	// Only the KV1 path exists; the default KV2 path will 404.
	srv := newVault(t, "/v1/secret/app/config", http.StatusOK, kv1(map[string]any{"HOST": "x"}))

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", withToken(vault.WithAddress(srv.URL))...),
	))
	if err == nil {
		t.Fatal("want a 404: KV2's path is not where this mount lives")
	}
	if !strings.Contains(err.Error(), "KV version") {
		t.Errorf("error %q should suggest the KV version as a cause", err)
	}
}

// TestKV2ShapeMismatchSuggestsKV1 covers the other direction: the path
// happened to resolve, but the envelope is flat, so the values would silently
// be missing rather than wrong.
func TestKV2ShapeMismatchSuggestsKV1(t *testing.T) {
	srv := newVault(t, "/v1/secret/data/app/config", http.StatusOK,
		map[string]any{"data": "not-an-object"})

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", withToken(vault.WithAddress(srv.URL))...),
	))
	if err == nil {
		t.Fatal("want an error for a response that is not KV v2 shaped")
	}
	if !strings.Contains(err.Error(), "KV1") {
		t.Errorf("error %q should name the likely fix", err)
	}
}

// TestValueTypesAreRendered pins the mapping from Vault's JSON to the strings
// a Source deals in. A KV secret may hold any JSON, so this has to be stated
// rather than assumed — particularly the number case, where the obvious
// implementation renders 9000 as "9000.000000" and then fails to parse.
func TestValueTypesAreRendered(t *testing.T) {
	srv := newVault(t, "/v1/secret/data/app/config", http.StatusOK,
		kv2(map[string]any{
			"HOST":  "vault.internal",
			"PORT":  9000, // a JSON number, not a string
			"DEBUG": true, // a JSON bool
			"NIL":   nil,  // null
			"OBJ":   map[string]any{"a": 1},
		}))

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", withToken(vault.WithAddress(srv.URL))...),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 9000 {
		t.Errorf("Port = %d — a JSON number must render without a decimal tail", got.Port)
	}
	if !got.Debug {
		t.Error("Debug = false, want a JSON bool to bind")
	}

	// The object survives as JSON so a field with an UnmarshalText could take
	// it, rather than being silently dropped.
	var unknown []string
	for _, u := range res.Unknown() {
		unknown = append(unknown, u.Key)
	}
	if !contains(unknown, "OBJ") {
		t.Errorf("Unknown() = %v, want OBJ listed rather than dropped", unknown)
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

// TestSecretIsMasked pins that a Vault value marked secret cannot reach output.
// It is the whole reason to read from Vault rather than a file.
func TestSecretIsMasked(t *testing.T) {
	srv := newVault(t, "/v1/secret/data/app/config", http.StatusOK,
		kv2(map[string]any{"PASSWORD": "hunter2"}))

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", withToken(vault.WithAddress(srv.URL))...),
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

func TestMissingSecretIsAnErrorByDefault(t *testing.T) {
	srv := newVault(t, "/v1/secret/data/present", http.StatusOK, kv2(nil))

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		vault.Secret("absent", withToken(vault.WithAddress(srv.URL))...),
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
	srv := newVault(t, "/v1/secret/data/present", http.StatusOK, kv2(nil))

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		vault.Secret("absent", withToken(vault.WithAddress(srv.URL), vault.Optional())...),
	))
	if err != nil {
		t.Fatalf("an optional missing secret must not fail: %v", err)
	}
	if got.Host != "localhost" || got.Port != 8080 {
		t.Errorf("cfg = %+v, want the compiled-in defaults", got)
	}
}

// TestSealedVaultSaysSealed pins that 503 is named rather than reported as a
// generic outage. A sealed Vault has a specific, well-known remedy.
func TestSealedVaultSaysSealed(t *testing.T) {
	srv := newVault(t, "", http.StatusServiceUnavailable, nil)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", withToken(vault.WithAddress(srv.URL))...),
	))
	if err == nil || !strings.Contains(err.Error(), "sealed") {
		t.Errorf("want the sealed state named, got %v", err)
	}
}

func TestPermissionDeniedNamesThePolicy(t *testing.T) {
	srv := newVault(t, "", http.StatusForbidden, nil)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", withToken(vault.WithAddress(srv.URL))...),
	))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"permission denied", "policy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestUnexpectedStatusIsReported(t *testing.T) {
	srv := newVault(t, "", http.StatusInternalServerError, nil)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", withToken(vault.WithAddress(srv.URL))...),
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
		vault.Secret("app/config", withToken(vault.WithAddress(srv.URL))...),
	))
	if err == nil || !strings.Contains(err.Error(), "decoding") {
		t.Errorf("want a decode error, got %v", err)
	}
}

// TestUnreachableVaultIsAnErrorNotAMiss is the rule that separates a broken
// secret store from one that simply has no opinion. The difference is a deploy
// proceeding with an empty password.
func TestUnreachableVaultIsAnErrorNotAMiss(t *testing.T) {
	srv := newVault(t, "", http.StatusOK, nil)
	addr := srv.URL
	srv.Close()

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", withToken(vault.WithAddress(addr))...),
	))
	if err == nil {
		t.Fatal("want an error, not a silent fallback to defaults")
	}
}

func TestEmptyResponseIsEmptyNotBroken(t *testing.T) {
	srv := newVault(t, "/v1/secret/data/app/config", http.StatusOK, map[string]any{})

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", withToken(vault.WithAddress(srv.URL))...),
	))
	if err != nil {
		t.Fatalf("a response with no data is valid: %v", err)
	}
	if got.Host != "localhost" {
		t.Errorf("Host = %q, want the default", got.Host)
	}
}

func TestKeysAreListedForUnknownDetection(t *testing.T) {
	srv := newVault(t, "/v1/secret/data/app/config", http.StatusOK,
		kv2(map[string]any{"HOST": "svc", "HSOT": "typo"}))

	_, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", withToken(vault.WithAddress(srv.URL))...),
	))
	if err != nil {
		t.Fatal(err)
	}
	u := res.Unknown()
	if len(u) != 1 || u[0].Key != "HSOT" {
		t.Fatalf("Unknown() = %v, want just the typo", u)
	}
}

// TestMountAndNamespaceReachTheRequest pins the two settings an enterprise
// deployment always needs, and which fail confusingly when silently dropped.
func TestMountAndNamespaceReachTheRequest(t *testing.T) {
	srv := newVault(t, "/v1/kv/data/app/config", http.StatusOK,
		kv2(map[string]any{"HOST": "svc"}))

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", withToken(
			vault.WithAddress(srv.URL),
			vault.WithMount("kv"),
			vault.WithNamespace("team-a"))...),
	)); err != nil {
		t.Fatal(err)
	}
	if srv.ns != "team-a" {
		t.Errorf("X-Vault-Namespace = %q, want team-a", srv.ns)
	}
}

func TestTokenReachesTheRequest(t *testing.T) {
	srv := newVault(t, "/v1/secret/data/app/config", http.StatusOK,
		kv2(map[string]any{"HOST": "svc"}))

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", vault.WithAddress(srv.URL), vault.WithToken("tok-123")),
	)); err != nil {
		t.Fatal(err)
	}
	if srv.token != "tok-123" {
		t.Errorf("X-Vault-Token = %q", srv.token)
	}
}

// TestTokenResolutionFollowsTheCLI pins the discovery order. Matching the Vault
// CLI is the point: an operator who can run `vault kv get` should not need new
// configuration to run this.
func TestTokenResolutionFollowsTheCLI(t *testing.T) {
	srv := newVault(t, "/v1/secret/data/app/config", http.StatusOK,
		kv2(map[string]any{"HOST": "svc"}))

	t.Run("VAULT_TOKEN is used", func(t *testing.T) {
		t.Setenv("VAULT_TOKEN", "from-env")
		if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
			vault.Secret("app/config", vault.WithAddress(srv.URL), vault.WithHomeDir(t.TempDir())),
		)); err != nil {
			t.Fatal(err)
		}
		if srv.token != "from-env" {
			t.Errorf("token = %q, want the environment's", srv.token)
		}
	})

	t.Run("the ~/.vault-token file is used", func(t *testing.T) {
		t.Setenv("VAULT_TOKEN", "")
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, ".vault-token"), []byte("from-file\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
			vault.Secret("app/config", vault.WithAddress(srv.URL), vault.WithHomeDir(home)),
		)); err != nil {
			t.Fatal(err)
		}
		// The trailing newline `vault login` writes must be trimmed, or the
		// header is malformed and every read is a 403.
		if srv.token != "from-file" {
			t.Errorf("token = %q, want the file's value trimmed", srv.token)
		}
	})

	t.Run("WithToken beats both", func(t *testing.T) {
		t.Setenv("VAULT_TOKEN", "from-env")
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, ".vault-token"), []byte("from-file"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
			vault.Secret("app/config", vault.WithAddress(srv.URL),
				vault.WithHomeDir(home), vault.WithToken("explicit")),
		)); err != nil {
			t.Fatal(err)
		}
		if srv.token != "explicit" {
			t.Errorf("token = %q, want the explicit one", srv.token)
		}
	})
}

// TestNoTokenNamesEveryWayToSupplyOne pins the message for the first thing a
// newcomer hits. "401" would send them to Vault's docs; this sends them to the
// three things that actually fix it.
func TestNoTokenNamesEveryWayToSupplyOne(t *testing.T) {
	t.Setenv("VAULT_TOKEN", "")
	srv := newVault(t, "", http.StatusOK, nil)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", vault.WithAddress(srv.URL), vault.WithHomeDir(t.TempDir())),
	))
	if err == nil {
		t.Fatal("want an error: there is no token anywhere")
	}
	for _, want := range []string{"VAULT_TOKEN", "vault login", "WithToken"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// TestUnreadableTokenFileIsAnError pins that a token file which exists and
// cannot be read fails rather than falling through to "no token". The two have
// different causes and different fixes.
func TestUnreadableTokenFileIsAnError(t *testing.T) {
	t.Setenv("VAULT_TOKEN", "")
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".vault-token"), 0o700); err != nil {
		t.Fatal(err)
	}
	srv := newVault(t, "", http.StatusOK, nil)

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", vault.WithAddress(srv.URL), vault.WithHomeDir(home)),
	))
	if err == nil || !strings.Contains(err.Error(), ".vault-token") {
		t.Errorf("want the unreadable token file named, got %v", err)
	}
}

// TestAddressFromEnvironment pins that $VAULT_ADDR is honoured, which is how
// nearly every deployment points at its own Vault.
func TestAddressFromEnvironment(t *testing.T) {
	srv := newVault(t, "/v1/secret/data/app/config", http.StatusOK,
		kv2(map[string]any{"HOST": "svc"}))
	t.Setenv("VAULT_ADDR", srv.URL)

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", vault.WithToken("t")),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "svc" {
		t.Errorf("Host = %q", got.Host)
	}
}

func TestInvalidAddressIsReported(t *testing.T) {
	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", vault.WithAddress("://not a url"), vault.WithToken("t")),
	))
	if err == nil || !strings.Contains(err.Error(), "invalid Vault address") {
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
		vault.Secret("app/config", withToken(vault.WithAddress(srv.URL), vault.WithContext(ctx))...),
	))
	if err == nil {
		t.Fatal("want the read to fail once the context expires")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %v — the context did not bound the read", elapsed)
	}
}

func TestCustomClientIsUsed(t *testing.T) {
	srv := newVault(t, "/v1/secret/data/app/config", http.StatusOK,
		kv2(map[string]any{"HOST": "svc"}))

	used := false
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		used = true
		return http.DefaultTransport.RoundTrip(r)
	})}

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", withToken(vault.WithAddress(srv.URL), vault.WithClient(client))...),
	)); err != nil {
		t.Fatal(err)
	}
	if !used {
		t.Error("the supplied client was not used, so its TLS configuration would be ignored")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestReadHappensOncePerSource is the cost contract, and it matters more here
// than elsewhere: every read is also an audit log entry, so a per-key source
// would fill an operator's audit trail at every boot.
func TestReadHappensOncePerSource(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(kv2(map[string]any{"HOST": "svc", "PORT": "9000"}))
	}))
	t.Cleanup(srv.Close)

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", withToken(vault.WithAddress(srv.URL))...),
	)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("made %d reads, want exactly 1 — each is an audit log entry", calls)
	}
}

func TestPrecedenceAgainstOtherSources(t *testing.T) {
	srv := newVault(t, "/v1/secret/data/app/config", http.StatusOK,
		kv2(map[string]any{"PASSWORD": "from-vault"}))

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"HOST": "from-map", "PASSWORD": "placeholder"}),
		vault.Secret("app/config", withToken(vault.WithAddress(srv.URL))...),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "from-vault" {
		t.Errorf("Password = %q, want the later source to win", got.Password)
	}
	if got.Host != "from-map" {
		t.Errorf("Host = %q, want the earlier source where the later is silent", got.Host)
	}
}

// TestDefaultAddressIsVaultsOwn pins where a read goes when neither WithAddress
// nor VAULT_ADDR says otherwise. Matching the CLI's default means a developer
// with `vault server -dev` running needs to configure nothing — and a test that
// always passes WithAddress would never notice if it changed.
//
// It asserts the TARGET, not a successful read: nothing is listening on 8200
// here, and the failure names the address, which is the thing under test.
func TestDefaultAddressIsVaultsOwn(t *testing.T) {
	t.Setenv("VAULT_ADDR", "")

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", vault.WithToken("t"),
			vault.WithContext(deadline(t, 2*time.Second))),
	))
	if err == nil {
		t.Skip("something is listening on 127.0.0.1:8200 — a dev Vault is running")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:8200") {
		t.Errorf("error %q should name Vault's default address", err)
	}
}

// TestKV1ShapeMismatchIsReported covers the legacy engine's decode failure. It
// has its own branch because the KV1 envelope is flat, so a KV2 response read
// as KV1 fails differently from the reverse.
func TestKV1ShapeMismatchIsReported(t *testing.T) {
	srv := newVault(t, "/v1/secret/app/config", http.StatusOK,
		map[string]any{"data": "not-an-object"})

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", withToken(
			vault.WithAddress(srv.URL), vault.WithKVVersion(vault.KV1))...),
	))
	if err == nil {
		t.Fatal("want an error for a response that is not KV v1 shaped")
	}
	if !strings.Contains(err.Error(), "KV v1") {
		t.Errorf("error %q should name the engine version it could not read", err)
	}
}

// TestTokenFileFoundThroughHomeDir covers the path taken when WithHomeDir is
// NOT passed — the real one, where the token file is found through the
// operating system's idea of the home directory.
func TestTokenFileFoundThroughHomeDir(t *testing.T) {
	t.Setenv("VAULT_TOKEN", "")
	home := t.TempDir()
	t.Setenv("HOME", home) // what os.UserHomeDir reads on unix
	if err := os.WriteFile(filepath.Join(home, ".vault-token"), []byte("via-home"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := newVault(t, "/v1/secret/data/app/config", http.StatusOK,
		kv2(map[string]any{"HOST": "svc"}))

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", vault.WithAddress(srv.URL)),
	)); err != nil {
		t.Skipf("os.UserHomeDir did not follow $HOME on this platform: %v", err)
	}
	if srv.token != "via-home" {
		t.Errorf("token = %q, want the file found through the home directory", srv.token)
	}
}

// TestNoHomeDirectoryFallsThroughToNoToken pins that an undiscoverable home
// directory is not itself reported. "no token" is the actionable message; "$HOME
// is unset" would send the reader somewhere that does not help.
func TestNoHomeDirectoryFallsThroughToNoToken(t *testing.T) {
	t.Setenv("VAULT_TOKEN", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "") // the same thing on Windows

	srv := newVault(t, "", http.StatusOK, nil)
	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		vault.Secret("app/config", vault.WithAddress(srv.URL)),
	))
	if err == nil {
		t.Skip("a home directory was still discoverable on this platform")
	}
	if !strings.Contains(err.Error(), "no Vault token") {
		t.Errorf("error %q should be about the missing token, not about $HOME", err)
	}
}

// deadline returns a context bounded to d, cleaned up with the test.
func deadline(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}
