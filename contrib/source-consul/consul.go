// Package consul reads a Consul KV prefix as a cfgkit source.
//
// It carries NO DEPENDENCIES, for the same reason source-k8s and source-vault
// do not: reading a KV prefix is a single HTTP GET with one header, and the
// official API client is a large thing to take for that. The whole client here
// is net/http and encoding/json.
//
// What that costs is stated in the README: token auth only, no ACL login flows,
// no service discovery, no blocking queries or watches, no writing. Those are
// Consul features rather than configuration features, and a program that needs
// them already has the API client — at which point cfgkit.SourceFunc wraps it
// in five lines.
package consul

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/ubgo/cfgkit"
)

const (
	// defaultAddr matches the Consul CLI's own default, so a developer running
	// `consul agent -dev` needs to configure nothing.
	defaultAddr = "http://127.0.0.1:8500"

	// envAddr and envToken are the variables the Consul CLI reads. Using the
	// same ones means an operator who can run `consul kv get` can run this.
	envAddr  = "CONSUL_HTTP_ADDR"
	envToken = "CONSUL_HTTP_TOKEN" // a variable NAME, not a credential

	// defaultTimeout bounds the single request. A configuration read happens at
	// process start, where hanging forever is worse than failing: a process
	// stuck before its readiness probe looks identical to a slow one, and
	// neither gets restarted.
	defaultTimeout = 10 * time.Second
)

type options struct {
	addr       string
	token      string
	datacenter string
	client     *http.Client
	ctx        context.Context
	optional   bool
}

// Option configures a source.
type Option func(*options)

// WithAddress overrides the Consul agent address. The default is
// $CONSUL_HTTP_ADDR, then http://127.0.0.1:8500.
//
// An address with no scheme is accepted and assumed to be http, because that is
// how CONSUL_HTTP_ADDR is conventionally written — "consul.service:8500"
// rather than a full URL.
func WithAddress(addr string) Option { return func(o *options) { o.addr = addr } }

// WithToken supplies the ACL token. The default is $CONSUL_HTTP_TOKEN.
func WithToken(token string) Option { return func(o *options) { o.token = token } }

// WithDatacenter reads from a datacenter other than the agent's own.
func WithDatacenter(dc string) Option { return func(o *options) { o.datacenter = dc } }

// WithClient supplies the HTTP client, and therefore the TLS configuration.
//
// Pass one carrying your CA when Consul is served over HTTPS with a private
// certificate authority. It is an explicit choice rather than a silent default,
// because a config source that quietly skips certificate verification is a
// worse failure than one that will not start.
func WithClient(c *http.Client) Option { return func(o *options) { o.client = c } }

// WithContext bounds the read, so a slow agent cannot hold up a boot beyond
// what the caller allows.
func WithContext(ctx context.Context) Option { return func(o *options) { o.ctx = ctx } }

// Optional makes an EMPTY prefix an empty source rather than an error.
//
// The default is the opposite of FromFiles, deliberately, and for the same
// reason as the other remote sources: you named this prefix, so finding nothing
// under it is a deployment mistake rather than a normal outcome. A service
// starting on compiled-in defaults because nobody populated the KV store is
// exactly the silent failure cfgkit exists to prevent.
func Optional() Option { return func(o *options) { o.optional = true } }

// Prefix reads every key under a Consul KV prefix as a flat source.
//
// Keys are returned RELATIVE to the prefix: with `app/config/` holding
// `app/config/PORT`, the source answers for `PORT`. That is what lets the same
// struct bind from Consul and from a .env file without a second set of tags.
//
// Nested prefixes flatten with their separator intact — `app/config/db/HOST`
// becomes `db/HOST` — because a flat source has no way to express a tree, and
// inventing one would guess at a mapping the caller has not stated. Use a
// deeper prefix, or a `env:"db/HOST"` tag, if that is what you want.
//
// The read happens ONCE, here. cfgkit calls Lookup once per bound field, so a
// source that dialled the agent per key would turn a twenty-field
// configuration into twenty round-trips at boot.
func Prefix(prefix string, opts ...Option) cfgkit.Source {
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}

	clean := strings.Trim(prefix, "/")
	name := "consul:" + clean
	data, err := fetch(clean, o)

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	return &source{name: name, data: data, err: err, keys: keys}
}

// source is the loaded prefix. It is immutable after construction, which is
// what makes it safe for the concurrent use cfgkit's Source contract requires.
type source struct {
	name string
	data map[string]string
	err  error
	keys []string
}

// Name identifies the source in provenance output — "consul:app/config" —
// naming the concrete prefix so Explain can say which one a value came from.
func (s *source) Name() string { return s.name }

// Lookup returns the value for key.
//
// A read failure is an ERROR, never a miss. An unreachable agent must not be
// indistinguishable from an unset key, because that difference is a deploy
// proceeding with an empty password.
func (s *source) Lookup(key string) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	v, ok := s.data[key]
	return v, ok, nil
}

// Keys implements cfgkit.KeyLister, so a key under the prefix that matches no
// field is reported as a probable typo. A prefix's keys were written for this
// application, which is why it can honestly enumerate.
func (s *source) Keys() []string { return s.keys }

// kvPair is the subset of Consul's reply this package reads. Flags, indexes and
// session information are Consul's business; unmarshalling only what is used
// means a change elsewhere in the API cannot break this.
type kvPair struct {
	Key   string `json:"Key"`
	Value string `json:"Value"` // base64, or null for a key with no value
}

func fetch(prefix string, o *options) (map[string]string, error) {
	endpoint, err := agentURL(o)
	if err != nil {
		return nil, err
	}
	endpoint.Path = path.Join(endpoint.Path, "v1", "kv", prefix)

	q := endpoint.Query()
	q.Set("recurse", "true")
	if o.datacenter != "" {
		q.Set("dc", o.datacenter)
	}
	endpoint.RawQuery = q.Encode()

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
	req.Header.Set("Accept", "application/json")

	token := o.token
	if token == "" {
		token = os.Getenv(envToken)
	}
	if token != "" {
		req.Header.Set("X-Consul-Token", token)
	}

	client := o.client
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reading consul kv %s: %w", prefix, err)
	}
	// The body is read-only and already fully consumed; there is
	// nothing a close failure could tell the caller to do.
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotFound && o.optional:
		// Consul answers 404 for a prefix with nothing under it, which is why
		// Optional() talks about an EMPTY prefix rather than a missing one:
		// from the outside the two are the same thing.
		return map[string]string{}, nil
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("consul kv %s: no keys under this prefix "+
			"(pass consul.Optional() if that is expected)", prefix)
	case resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("consul kv %s: permission denied — "+
			"the ACL token needs read on this prefix", prefix)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("reading consul kv %s: unexpected status %s", prefix, resp.Status)
	}

	var pairs []kvPair
	if err := json.NewDecoder(resp.Body).Decode(&pairs); err != nil {
		return nil, fmt.Errorf("decoding consul kv %s: %w", prefix, err)
	}
	return flatten(pairs, prefix)
}

// agentURL resolves the agent address.
//
// A bare host:port is accepted because that is how CONSUL_HTTP_ADDR is
// conventionally written. Without this, "consul.service:8500" parses as a URL
// whose SCHEME is "consul.service", and the request fails with a message about
// an unsupported protocol that names nothing a reader would recognise.
func agentURL(o *options) (*url.URL, error) {
	addr := o.addr
	if addr == "" {
		addr = os.Getenv(envAddr)
	}
	if addr == "" {
		addr = defaultAddr
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid Consul address %q: %w", addr, err)
	}
	return u, nil
}

// flatten turns Consul's absolute keys into keys relative to the prefix, and
// decodes the base64 values.
//
// A key equal to the prefix itself is skipped: Consul returns the "folder" of a
// nested tree as a valueless key, and binding it would offer an empty value
// under an empty name.
func flatten(pairs []kvPair, prefix string) (map[string]string, error) {
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		key := strings.TrimPrefix(strings.TrimPrefix(p.Key, prefix), "/")
		if key == "" {
			continue
		}
		if p.Value == "" {
			// A key with no value. Consul writes null here; an empty string is
			// a real, distinct value and arrives base64-encoded as "".
			out[key] = ""
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(p.Value)
		if err != nil {
			// The KEY is named and the value is not: a Consul value may hold a
			// credential, and an error message is a thing that gets logged.
			return nil, fmt.Errorf("consul kv %s: value for %q is not valid base64", prefix, key)
		}
		out[key] = string(raw)
	}
	return out, nil
}
