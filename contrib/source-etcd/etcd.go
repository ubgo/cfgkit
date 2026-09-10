// Package etcd reads an etcd v3 key prefix as a cfgkit source.
//
// It carries NO DEPENDENCIES, which for etcd needs more explanation than it did
// for Consul or Vault: etcd's native v3 API is gRPC, and the official client
// brings gRPC, protobuf and their transitive tree. This package uses etcd's
// HTTP/JSON GATEWAY instead — the same server, a different door, enabled by
// default since v3 (`--enable-grpc-gateway`, which defaults to true).
//
// The trade is stated in the README rather than discovered later: no watches,
// no leases, no transactions, no gRPC-only features, and a cluster that has
// explicitly disabled the gateway needs the official client. A program that
// needs any of those already has `go.etcd.io/etcd/client/v3`, and
// cfgkit.SourceFunc wraps it in five lines.
package etcd

import (
	"bytes"
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
	// defaultAddr matches etcd's own default client address, so a developer
	// running a local etcd needs to configure nothing.
	defaultAddr = "http://127.0.0.1:2379"

	// envEndpoints is what etcdctl reads. Only the FIRST endpoint is used:
	// this package makes one request and does not do failover, and pretending
	// otherwise by silently trying the rest would hide a broken first node.
	envEndpoints = "ETCDCTL_ENDPOINTS"

	// defaultTimeout bounds the request. A configuration read happens at
	// process start, where hanging forever is worse than failing.
	defaultTimeout = 10 * time.Second
)

type options struct {
	addr     string
	token    string
	user     string
	password string
	client   *http.Client
	ctx      context.Context
	optional bool
}

// Option configures a source.
type Option func(*options)

// WithAddress overrides the etcd address. The default is the first entry of
// $ETCDCTL_ENDPOINTS, then http://127.0.0.1:2379.
//
// A bare host:port is accepted and assumed to be http, because that is how
// endpoints are conventionally written.
func WithAddress(addr string) Option { return func(o *options) { o.addr = addr } }

// WithToken supplies an already-issued auth token, sent as the Authorization
// header.
//
// Use it when your program authenticates elsewhere. It skips the
// authentication round trip WithCredentials makes.
func WithToken(token string) Option { return func(o *options) { o.token = token } }

// WithCredentials authenticates with a username and password before reading.
//
// It costs one extra request — etcd issues a token from /v3/auth/authenticate —
// which is why it is opt-in rather than the default. A cluster with
// authentication disabled needs neither this nor WithToken.
func WithCredentials(user, password string) Option {
	return func(o *options) { o.user, o.password = user, password }
}

// WithClient supplies the HTTP client, and therefore the TLS configuration.
//
// etcd is usually served over TLS with a private CA, so this is the option most
// real deployments need. It is explicit rather than a silent default, because a
// config source that quietly skips certificate verification is a worse failure
// than one that will not start.
func WithClient(c *http.Client) Option { return func(o *options) { o.client = c } }

// WithContext bounds the read, so a slow or partitioned cluster cannot hold up
// a boot beyond what the caller allows.
func WithContext(ctx context.Context) Option { return func(o *options) { o.ctx = ctx } }

// Optional makes an EMPTY prefix an empty source rather than an error.
//
// The default is the opposite of FromFiles, deliberately, and for the same
// reason as the other remote sources: you named this prefix, so finding nothing
// under it is a deployment mistake. A service starting on compiled-in defaults
// because nobody populated the store is the silent failure cfgkit exists to
// prevent.
func Optional() Option { return func(o *options) { o.optional = true } }

// Prefix reads every key under an etcd prefix as a flat source.
//
// Keys arrive RELATIVE to the prefix: with `/app/config/` holding
// `/app/config/PORT`, the source answers for `PORT`. That is what lets one
// struct bind from etcd and from a .env file with no second set of tags.
//
// Nested prefixes flatten with their separator intact — `/app/config/db/HOST`
// becomes `db/HOST` — because a flat source cannot express a tree and inventing
// a mapping would guess at something the caller never stated.
//
// The read happens ONCE, here, as a single range request. cfgkit calls Lookup
// once per bound field, so a source that ranged per key would turn a
// twenty-field configuration into twenty round-trips at boot.
func Prefix(prefix string, opts ...Option) cfgkit.Source {
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}

	clean := strings.Trim(prefix, "/")
	name := "etcd:" + clean
	data, err := fetch(clean, o)

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	return &source{name: name, data: data, err: err, keys: keys}
}

// source is the loaded prefix, immutable after construction — which is what
// makes it safe for the concurrent use cfgkit's Source contract requires.
type source struct {
	name string
	data map[string]string
	err  error
	keys []string
}

// Name identifies the source in provenance output — "etcd:app/config".
func (s *source) Name() string { return s.name }

// Lookup returns the value for key.
//
// A read failure is an ERROR, never a miss. An unreachable cluster must not be
// indistinguishable from an unset key, because that difference is a deploy
// proceeding with an empty password.
func (s *source) Lookup(key string) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	v, ok := s.data[key]
	return v, ok, nil
}

// Keys implements cfgkit.KeyLister, so a key under the prefix matching no field
// is reported as a probable typo.
func (s *source) Keys() []string { return s.keys }

// rangeResponse is the subset of the gateway's reply this package reads.
// Revisions, lease ids and the cluster header are etcd's business.
type rangeResponse struct {
	Kvs []struct {
		Key   string `json:"key"`   // base64
		Value string `json:"value"` // base64, absent for an empty value
	} `json:"kvs"`
}

// authResponse carries the token etcd issues for a username and password.
type authResponse struct {
	Token string `json:"token"`
}

func fetch(prefix string, o *options) (map[string]string, error) {
	endpoint, err := clusterURL(o)
	if err != nil {
		return nil, err
	}

	client := o.client
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}

	ctx := o.ctx
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), defaultTimeout)
		defer cancel()
	}

	token := o.token
	if token == "" && o.user != "" {
		token, err = authenticate(ctx, client, endpoint, o.user, o.password)
		if err != nil {
			return nil, err
		}
	}

	// A prefix scan in etcd is a range from the prefix to its successor: the
	// same bytes with the last one incremented. That is etcd's own convention
	// and there is no dedicated "prefix" parameter to use instead.
	key := prefix + "/"
	body, err := json.Marshal(map[string]string{
		"key":       base64.StdEncoding.EncodeToString([]byte(key)),
		"range_end": base64.StdEncoding.EncodeToString(prefixEnd(key)),
	})
	if err != nil {
		return nil, err
	}

	var out rangeResponse
	if err := post(ctx, client, endpoint, "/v3/kv/range", token, body, &out, prefix); err != nil {
		return nil, err
	}

	if len(out.Kvs) == 0 {
		if o.optional {
			return map[string]string{}, nil
		}
		// etcd answers 200 with no kvs rather than 404, so an empty result is
		// the only signal that a prefix holds nothing.
		return nil, fmt.Errorf("etcd %s: no keys under this prefix "+
			"(pass etcd.Optional() if that is expected)", prefix)
	}

	data := make(map[string]string, len(out.Kvs))
	for _, kv := range out.Kvs {
		rawKey, err := base64.StdEncoding.DecodeString(kv.Key)
		if err != nil {
			return nil, fmt.Errorf("etcd %s: a key is not valid base64", prefix)
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(string(rawKey), key), "/")
		if rel == "" {
			continue
		}
		if kv.Value == "" {
			// etcd omits the field for an empty value. The key still EXISTS,
			// so it must bind as empty rather than be absent: a present empty
			// value beats a default and an absent one does not.
			data[rel] = ""
			continue
		}
		rawVal, err := base64.StdEncoding.DecodeString(kv.Value)
		if err != nil {
			// The KEY is named and the value is not: an etcd value may hold a
			// credential, and an error message is a thing that gets logged.
			return nil, fmt.Errorf("etcd %s: value for %q is not valid base64", prefix, rel)
		}
		data[rel] = string(rawVal)
	}
	return data, nil
}

// authenticate exchanges a username and password for a token.
func authenticate(ctx context.Context, client *http.Client, base *url.URL, user, password string) (string, error) {
	body, err := json.Marshal(map[string]string{"name": user, "password": password})
	if err != nil {
		return "", err
	}
	var out authResponse
	if err := post(ctx, client, base, "/v3/auth/authenticate", "", body, &out, "auth"); err != nil {
		return "", fmt.Errorf("authenticating with etcd: %w", err)
	}
	if out.Token == "" {
		return "", fmt.Errorf("etcd returned no token for user %q", user)
	}
	return out.Token, nil
}

// post makes one JSON request against the gateway and decodes the reply.
//
// It is shared by the range and authenticate calls so their error handling
// cannot drift apart — the two failures a reader most needs distinguished are
// "wrong credentials" and "no permission", and both come back the same way.
func post(ctx context.Context, client *http.Client, base *url.URL, endpointPath, token string, body []byte, out any, what string) error {
	u := *base
	u.Path = path.Join(u.Path, endpointPath)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("reading etcd %s: %w", what, err)
	}
	// The body is read-only and already fully consumed; there is
	// nothing a close failure could tell the caller to do.
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("etcd %s: unauthorized — "+
			"pass etcd.WithCredentials or etcd.WithToken", what)
	case resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("etcd %s: permission denied — "+
			"the user's role needs read on this range", what)
	case resp.StatusCode == http.StatusNotFound:
		// The gateway is the one thing that can be switched off, and its
		// absence looks exactly like a wrong address unless it is named.
		return fmt.Errorf("etcd %s: the HTTP gateway is not serving %s "+
			"(is the cluster started with --enable-grpc-gateway=false?)", what, endpointPath)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("reading etcd %s: unexpected status %s", what, resp.Status)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding etcd %s: %w", what, err)
	}
	return nil
}

// clusterURL resolves the address to talk to.
func clusterURL(o *options) (*url.URL, error) {
	addr := o.addr
	if addr == "" {
		// Only the first endpoint: this package makes one request and does no
		// failover, and silently trying the rest would hide a broken node.
		if eps := os.Getenv(envEndpoints); eps != "" {
			addr = strings.TrimSpace(strings.Split(eps, ",")[0])
		}
	}
	if addr == "" {
		addr = defaultAddr
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid etcd address %q: %w", addr, err)
	}
	return u, nil
}

// prefixEnd returns the smallest key greater than every key with this prefix,
// which is how etcd expresses a prefix scan.
//
// It increments the last byte that is not 0xFF, dropping the bytes after it. A
// prefix of all 0xFF has no successor, and etcd's own convention for that is a
// range_end of a single zero byte, meaning "to the end of the keyspace".
func prefixEnd(prefix string) []byte {
	end := []byte(prefix)
	for i := len(end) - 1; i >= 0; i-- {
		if end[i] < 0xFF {
			end[i]++
			return end[:i+1]
		}
	}
	return []byte{0}
}
