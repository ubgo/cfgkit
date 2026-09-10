// Package k8s reads a Kubernetes ConfigMap or Secret as a cfgkit source.
//
// IT CARRIES NO DEPENDENCIES, which is unusual for a Kubernetes client and is
// the main design decision here. Reading one named resource needs four HTTP
// GETs' worth of API surface, and `client-go` is a very large dependency to
// take for that — it pulls in the API machinery, several codec layers and a
// version treadmill tied to the cluster's release cadence. The whole thing is
// net/http, encoding/json, and the service-account files kubelet already mounts
// into every pod.
//
// The cost is real and stated in full under "What this does not do" in the
// README: in-cluster service-account auth only, no kubeconfig, no exec or OIDC
// credential plugins, no watch. If you need any of those you already have
// client-go in your program, and wrapping it takes five lines through
// cfgkit.SourceFunc — the interface is the extension point, so this module
// never has to be the only way.
package k8s

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/ubgo/cfgkit"
)

// The paths kubelet mounts into every pod that has a service account. They are
// constants of the platform rather than of this package, which is why they are
// named here once instead of appearing inline.
const (
	// defaultServiceAccountDir is where kubelet mounts the projected service
	// account by default. It is a constant of the platform rather than of this
	// package, and WithServiceAccountDir exists because some clusters project
	// it elsewhere.
	defaultServiceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

	tokenFile     = "token" // a FILENAME, not a credential
	namespaceFile = "namespace"

	// defaultServer is the in-cluster API address. It resolves through the
	// cluster's own DNS and is served with the CA the pod already trusts.
	defaultServer = "https://kubernetes.default.svc"

	// defaultTimeout bounds the single request this package makes. A
	// configuration read happens at process start, where hanging forever is
	// worse than failing: a pod stuck before its readiness probe looks
	// identical to a pod that is merely slow, and neither gets restarted.
	defaultTimeout = 10 * time.Second
)

// options collects what a caller may override. Every field has a working
// in-cluster default, so the zero value is the common case.
type options struct {
	namespace string
	server    string
	token     string
	client    *http.Client
	ctx       context.Context
	optional  bool
	saDir     string
}

// serviceAccountDir returns where to look for the projected service account.
func (o *options) serviceAccountDir() string {
	if o.saDir != "" {
		return o.saDir
	}
	return defaultServiceAccountDir
}

// Option configures a source.
type Option func(*options)

// WithNamespace sets the namespace to read from.
//
// The default is the pod's own namespace, read from the file kubelet mounts.
// Reading another namespace needs an RBAC rule that grants it, so this is worth
// setting explicitly rather than discovering through a 403.
func WithNamespace(ns string) Option { return func(o *options) { o.namespace = ns } }

// WithServer overrides the API server address.
//
// Use it to read through `kubectl proxy` during development, which is the
// supported way to run this outside a cluster:
//
//	kubectl proxy --port=8001
//	k8s.ConfigMap("app-config", k8s.WithServer("http://127.0.0.1:8001"), k8s.WithNamespace("default"))
func WithServer(server string) Option { return func(o *options) { o.server = server } }

// WithToken overrides the bearer token. The default is the pod's service
// account token.
func WithToken(token string) Option { return func(o *options) { o.token = token } }

// WithClient supplies the HTTP client, and therefore the TLS configuration.
//
// The default trusts the system pool, which is correct in a cluster whose CA is
// installed in the image, and wrong in one where it is not — pass a client
// carrying the CA from /var/run/secrets/kubernetes.io/serviceaccount/ca.crt if
// your image does not already trust it. It is an explicit choice rather than a
// silent default because a config source that quietly skips certificate
// verification is a worse failure than one that will not start.
func WithClient(c *http.Client) Option { return func(o *options) { o.client = c } }

// WithServiceAccountDir overrides where the projected service account is
// mounted.
//
// The default is the path kubelet uses, and most deployments never touch this.
// It exists because a projected volume can be mounted anywhere, and because
// naming the directory is what makes the in-cluster path testable at all — the
// alternative is a package-level variable that only tests write, which is
// hidden state wearing a disguise.
func WithServiceAccountDir(dir string) Option { return func(o *options) { o.saDir = dir } }

// WithContext bounds the read, so a slow API server cannot hold up a boot
// beyond what the caller allows.
func WithContext(ctx context.Context) Option { return func(o *options) { o.ctx = ctx } }

// Optional makes a MISSING resource an empty source rather than an error.
//
// The default is the opposite of FromFiles, deliberately. A file path is often
// speculative — `.env.local` is expected not to exist — but a named ConfigMap
// is a deployment contract: you asked for `app-config` by name, so its absence
// is a mistake in the manifest, and starting on defaults instead would hide it
// until something behaved oddly in production.
//
// Use this when the resource genuinely is optional, such as an override map a
// cluster may or may not define.
func Optional() Option { return func(o *options) { o.optional = true } }

// ConfigMap reads a ConfigMap's data as a flat source.
//
// The read happens ONCE, here, not per key: cfgkit's Source contract calls
// Lookup once per bound field, so a source that dialled the API per key would
// turn a fifty-field configuration into fifty round-trips at boot.
//
// An error is reported when the source is first consulted rather than returned
// here, which keeps construction total and lets a caller build a source list
// without error handling at every line. cfgkit aborts the Load on it.
func ConfigMap(name string, opts ...Option) cfgkit.Source {
	return newSource("configmaps", name, false, opts)
}

// Secret reads a Secret's data as a flat source.
//
// Values arrive base64-encoded from the API and are decoded here, so a caller
// sees the same plain strings a ConfigMap gives. Mark the fields `secret:"true"`
// so cfgkit masks them in Explain and keeps them out of error text.
func Secret(name string, opts ...Option) cfgkit.Source {
	return newSource("secrets", name, true, opts)
}

// resource is the subset of the Kubernetes API response this package reads. The
// rest of the object — metadata, immutability, binary data — is deliberately
// not modelled: unmarshalling only what is used means a change elsewhere in the
// API cannot break this.
type resource struct {
	Data map[string]string `json:"data"`
}

// newSource performs the read and returns a source over the result.
func newSource(kind, name string, decode bool, opts []Option) cfgkit.Source {
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}

	sourceName := "k8s:" + kind + "/" + name
	data, err := fetch(kind, name, decode, o)

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}

	return &source{name: sourceName, data: data, err: err, keys: keys}
}

// source is the loaded resource. It is immutable after construction, which is
// what makes it safe for the concurrent use cfgkit's Source contract requires.
type source struct {
	name string
	data map[string]string
	err  error
	keys []string
}

// Name identifies the source in provenance output — "k8s:configmaps/app-config",
// naming the concrete resource rather than the type, so Explain says which
// object a value came from.
func (s *source) Name() string { return s.name }

// Lookup returns the value for key.
//
// A read failure is returned as an ERROR, never as a miss. An unreachable API
// server must not be indistinguishable from an unset key, because that
// difference is a deploy proceeding with an empty password.
func (s *source) Lookup(key string) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	v, ok := s.data[key]
	return v, ok, nil
}

// Keys implements cfgkit.KeyLister, so a key in the resource that matches no
// field is reported through Result.Unknown() as a probable typo.
//
// A ConfigMap CAN honestly enumerate, which is why it implements this and
// FromEnviron does not: its keys were written for this application, so every
// one of them not matching a field is worth a word.
func (s *source) Keys() []string { return s.keys }

// fetch performs the single API read.
func fetch(kind, name string, decode bool, o *options) (map[string]string, error) {
	ns, err := namespace(o)
	if err != nil {
		return nil, err
	}
	token, err := bearerToken(o)
	if err != nil {
		return nil, err
	}

	server := o.server
	if server == "" {
		server = defaultServer
	}
	endpoint, err := url.Parse(server)
	if err != nil {
		return nil, fmt.Errorf("invalid API server %q: %w", server, err)
	}
	endpoint.Path = path.Join(endpoint.Path, "api", "v1", "namespaces", ns, kind, name)

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
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")

	client := o.client
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reading %s/%s: %w", kind, name, err)
	}
	// The body is read-only and already fully consumed; there is
	// nothing a close failure could tell the caller to do.
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotFound && o.optional:
		return map[string]string{}, nil
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%s/%s not found in namespace %q "+
			"(pass k8s.Optional() if it is genuinely optional)", kind, name, ns)
	case resp.StatusCode == http.StatusForbidden:
		// The single most common failure, and the message names the fix: the
		// pod's service account has no RBAC rule granting this read.
		return nil, fmt.Errorf("forbidden reading %s/%s in namespace %q — "+
			"the service account needs a Role granting get on %s", kind, name, ns, kind)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("reading %s/%s: unexpected status %s", kind, name, resp.Status)
	}

	var out resource
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding %s/%s: %w", kind, name, err)
	}
	if out.Data == nil {
		// A resource with no data is valid and means no keys, which is not the
		// same as a failed read and must not look like one.
		return map[string]string{}, nil
	}

	if !decode {
		return out.Data, nil
	}
	return decodeSecretData(out.Data, name)
}

// decodeSecretData base64-decodes a Secret's values, which is how the API
// returns them.
func decodeSecretData(data map[string]string, name string) (map[string]string, error) {
	out := make(map[string]string, len(data))
	for k, v := range data {
		raw, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			// The KEY is named and the value is not. A Secret's value must not
			// reach an error message, which is a thing that gets logged.
			return nil, fmt.Errorf("secret %s: value for %q is not valid base64", name, k)
		}
		out[k] = string(raw)
	}
	return out, nil
}

// namespace resolves which namespace to read from.
func namespace(o *options) (string, error) {
	if o.namespace != "" {
		return o.namespace, nil
	}
	p := filepath.Join(o.serviceAccountDir(), namespaceFile)
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("no namespace given and %s is unreadable "+
			"(outside a cluster, pass k8s.WithNamespace): %w", p, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// bearerToken resolves the credential.
//
// Outside a cluster the token file is absent, and that is NOT an error: a
// caller talking to `kubectl proxy` needs no bearer token, and demanding one
// would make the documented development path impossible.
func bearerToken(o *options) (string, error) {
	if o.token != "" {
		return o.token, nil
	}
	b, err := os.ReadFile(filepath.Join(o.serviceAccountDir(), tokenFile))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading service account token: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}
