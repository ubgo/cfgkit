// Package vault reads a HashiCorp Vault secret as a cfgkit source.
//
// It carries NO DEPENDENCIES, for the same reason source-k8s does not: reading
// one secret is a single HTTP GET with one header, and the official SDK is a
// large thing to take for that. The whole client here is net/http and
// encoding/json.
//
// What that costs is stated in the README rather than discovered later: token
// auth only, no AppRole or Kubernetes or AWS login flows, no lease renewal, no
// dynamic credentials. Those are Vault features rather than configuration
// features, and a program that needs them already has the SDK — at which point
// cfgkit.SourceFunc wraps it in five lines, because the Source interface is the
// extension point and this module never has to be the only way in.
package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/ubgo/cfgkit"
)

const (
	// defaultAddr matches the Vault CLI's own default, so a developer with a
	// dev server running needs to configure nothing.
	defaultAddr = "http://127.0.0.1:8200"

	// envAddr and envToken are the variables the Vault CLI uses. Reading the
	// same ones means an operator who can run `vault kv get` can run this.
	envAddr  = "VAULT_ADDR"
	envToken = "VAULT_TOKEN" // a variable NAME, not a credential

	// tokenFile is where `vault login` writes the token.
	tokenFile = ".vault-token" // a FILENAME, not a credential

	// defaultTimeout bounds the single request. A configuration read happens at
	// process start, where hanging forever is worse than failing: a process
	// stuck before its readiness probe looks identical to a slow one, and
	// neither gets restarted.
	defaultTimeout = 10 * time.Second

	// defaultMount is Vault's own default KV mount path.
	defaultMount = "secret"
)

// KVVersion selects the key/value engine's API shape. The two differ in both
// the request path and the response envelope, and Vault does not tell you which
// one a mount is without a second call — so it is stated rather than guessed.
type KVVersion int

const (
	// KV2 is the current engine, mounted by default in a modern Vault. Its
	// path carries a "data" segment and its response nests values one level
	// deeper.
	KV2 KVVersion = 2
	// KV1 is the legacy engine, still in use on older mounts.
	KV1 KVVersion = 1
)

type options struct {
	addr      string
	token     string
	namespace string
	mount     string
	version   KVVersion
	client    *http.Client
	ctx       context.Context
	optional  bool
	homeDir   string
}

// Option configures a source.
type Option func(*options)

// WithAddress overrides the Vault address. The default is $VAULT_ADDR, then
// Vault's own default of http://127.0.0.1:8200.
func WithAddress(addr string) Option { return func(o *options) { o.addr = addr } }

// WithToken supplies the token directly.
//
// The default resolution order matches the Vault CLI: $VAULT_TOKEN, then the
// ~/.vault-token file that `vault login` writes. Matching the CLI means anyone
// who can run `vault kv get` can run this without new configuration.
func WithToken(token string) Option { return func(o *options) { o.token = token } }

// WithNamespace sets the Vault Enterprise namespace.
func WithNamespace(ns string) Option { return func(o *options) { o.namespace = ns } }

// WithMount sets the secrets engine mount path. The default is "secret",
// which is Vault's own.
func WithMount(mount string) Option { return func(o *options) { o.mount = mount } }

// WithKVVersion selects the key/value engine version. The default is KV2.
//
// It is explicit because Vault will not tell you which version a mount is
// without a second API call, and guessing wrong produces a 404 that looks like
// a missing secret rather than a misconfigured client — a confusing failure to
// debug at boot.
func WithKVVersion(v KVVersion) Option { return func(o *options) { o.version = v } }

// WithClient supplies the HTTP client, and therefore the TLS configuration.
//
// Pass one carrying your CA when Vault uses a private certificate authority.
// It is an explicit choice rather than a silent default, because a secret
// source that quietly skips certificate verification is a worse failure than
// one that will not start.
func WithClient(c *http.Client) Option { return func(o *options) { o.client = c } }

// WithContext bounds the read, so a slow Vault cannot hold up a boot beyond
// what the caller allows.
func WithContext(ctx context.Context) Option { return func(o *options) { o.ctx = ctx } }

// WithHomeDir overrides where the ~/.vault-token file is looked for.
//
// It exists so the token-file path can be tested without writing to a
// developer's real home directory, and it is a genuine option rather than a
// test hook: a service account with a non-standard HOME needs it too.
func WithHomeDir(dir string) Option { return func(o *options) { o.homeDir = dir } }

// Optional makes a MISSING secret an empty source rather than an error.
//
// The default is the opposite of FromFiles, deliberately, and for the same
// reason as source-k8s: you named this secret path, so its absence is a
// deployment mistake. A service starting on compiled-in defaults because a
// secret was never written is exactly the silent failure this package exists
// to prevent.
func Optional() Option { return func(o *options) { o.optional = true } }

// Secret reads a Vault KV secret at path and returns its data as a flat source.
//
// The read happens ONCE, here. cfgkit calls Lookup once per bound field, so a
// source that dialled Vault per key would turn a twenty-field configuration
// into twenty round-trips — and twenty audit log entries — at boot.
//
// An error is reported when the source is first consulted rather than returned
// here, which keeps construction total and lets a caller write a source list
// without error handling on every line. cfgkit aborts the Load on it.
func Secret(secretPath string, opts ...Option) cfgkit.Source {
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}
	if o.mount == "" {
		o.mount = defaultMount
	}
	if o.version == 0 {
		o.version = KV2
	}

	name := "vault:" + o.mount + "/" + strings.TrimPrefix(secretPath, "/")
	data, err := fetch(secretPath, o)

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	return &source{name: name, data: data, err: err, keys: keys}
}

// source is the loaded secret. It is immutable after construction, which is
// what makes it safe for the concurrent use cfgkit's Source contract requires.
type source struct {
	name string
	data map[string]string
	err  error
	keys []string
}

// Name identifies the source in provenance output — "vault:secret/app/config",
// naming the concrete path so Explain can say which secret a value came from
// when several are in the list.
func (s *source) Name() string { return s.name }

// Lookup returns the value for key.
//
// A read failure is an ERROR, never a miss. An unreachable or sealed Vault must
// not be indistinguishable from an unset key, because that difference is a
// deploy proceeding with an empty password.
func (s *source) Lookup(key string) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	v, ok := s.data[key]
	return v, ok, nil
}

// Keys implements cfgkit.KeyLister, so a key in the secret that matches no
// field is reported as a probable typo. A secret's keys were written for this
// application, which is why it can honestly enumerate.
func (s *source) Keys() []string { return s.keys }

// kvResponse is the subset of Vault's reply this package reads. Only `data` is
// modelled: warnings, lease information and metadata are Vault's business, and
// unmarshalling only what is used means a change elsewhere cannot break this.
type kvResponse struct {
	Data json.RawMessage `json:"data"`
}

// kv2Envelope is KV v2's extra nesting: the values live at data.data, with
// data.metadata beside them.
type kv2Envelope struct {
	Data map[string]any `json:"data"`
}

func fetch(secretPath string, o *options) (map[string]string, error) {
	addr := o.addr
	if addr == "" {
		addr = os.Getenv(envAddr)
	}
	if addr == "" {
		addr = defaultAddr
	}
	endpoint, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid Vault address %q: %w", addr, err)
	}

	// KV v2 inserts a "data" segment between the mount and the path; KV v1 does
	// not. Getting this wrong is a 404 that looks like a missing secret, which
	// is why the version is an explicit option rather than a guess.
	clean := strings.Trim(secretPath, "/")
	if o.version == KV2 {
		endpoint.Path = path.Join(endpoint.Path, "v1", o.mount, "data", clean)
	} else {
		endpoint.Path = path.Join(endpoint.Path, "v1", o.mount, clean)
	}

	token, err := resolveToken(o)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, fmt.Errorf("no Vault token: set %s, run `vault login`, or pass vault.WithToken", envToken)
	}

	ctx := o.ctx
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), defaultTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Vault-Token", token)
	req.Header.Set("Accept", "application/json")
	if o.namespace != "" {
		req.Header.Set("X-Vault-Namespace", o.namespace)
	}

	client := o.client
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reading vault %s: %w", clean, err)
	}
	// The body is read-only and already fully consumed; there is
	// nothing a close failure could tell the caller to do.
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotFound && o.optional:
		return map[string]string{}, nil
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("vault %s/%s not found "+
			"(check the mount and KV version, or pass vault.Optional() if it is genuinely optional)",
			o.mount, clean)
	case resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("vault %s/%s: permission denied — "+
			"the token's policy needs read on this path", o.mount, clean)
	case resp.StatusCode == http.StatusServiceUnavailable:
		// Vault's own status code for a sealed or standby node. Naming it
		// saves the reader from reading it as a generic outage.
		return nil, fmt.Errorf("vault is sealed or unavailable (503) reading %s/%s", o.mount, clean)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("reading vault %s/%s: unexpected status %s", o.mount, clean, resp.Status)
	}

	var out kvResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding vault %s/%s: %w", o.mount, clean, err)
	}

	raw, err := unwrap(out.Data, o.version)
	if err != nil {
		return nil, fmt.Errorf("vault %s/%s: %w", o.mount, clean, err)
	}
	return stringify(raw), nil
}

// unwrap peels the envelope, which differs between the two KV versions.
func unwrap(data json.RawMessage, version KVVersion) (map[string]any, error) {
	if len(data) == 0 {
		// A response with no data is valid and means no keys. It must not look
		// like a failed read.
		return map[string]any{}, nil
	}
	if version == KV2 {
		var env kv2Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			return nil, fmt.Errorf("unexpected KV v2 response shape "+
				"(is this mount actually KV v1? pass vault.WithKVVersion(vault.KV1)): %w", err)
		}
		return env.Data, nil
	}
	var flat map[string]any
	if err := json.Unmarshal(data, &flat); err != nil {
		return nil, fmt.Errorf("unexpected KV v1 response shape: %w", err)
	}
	return flat, nil
}

// stringify renders Vault's JSON values as the strings cfgkit binds from.
//
// A KV secret may hold any JSON, while a cfgkit Source deals in strings, so the
// mapping has to be stated: a string passes through untouched, a number or bool
// is formatted, and an object or array is re-encoded as JSON so a field can
// still take it through encoding.TextUnmarshaler. Null becomes the empty
// string, which is what "present but unset" means everywhere else here.
func stringify(in map[string]any) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		switch t := v.(type) {
		case nil:
			out[k] = ""
		case string:
			out[k] = t
		case bool:
			out[k] = strconv.FormatBool(t)
		case float64:
			// encoding/json gives every number as float64. 'g' with -1
			// precision renders 8080 as "8080" rather than "8080.000000",
			// which matters because the value is about to be parsed as an int.
			out[k] = strconv.FormatFloat(t, 'g', -1, 64)
		default:
			// An object or array: re-encode rather than drop it, so a field
			// with an UnmarshalText can still receive it.
			b, err := json.Marshal(t)
			if err != nil {
				continue
			}
			out[k] = string(b)
		}
	}
	return out
}

// resolveToken follows the Vault CLI's own order, so an operator who can run
// `vault kv get` can run this with no new configuration.
func resolveToken(o *options) (string, error) {
	if o.token != "" {
		return o.token, nil
	}
	if v := os.Getenv(envToken); v != "" {
		return v, nil
	}

	home := o.homeDir
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			// No home directory is not an error by itself — the caller may
			// still be about to fail on a missing token, which is a clearer
			// message than one about $HOME.
			return "", nil // absence is handled by the caller, so it is not an error here
		}
		home = h
	}

	b, err := os.ReadFile(path.Join(home, tokenFile))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading %s: %w", tokenFile, err)
	}
	return strings.TrimSpace(string(b)), nil
}
