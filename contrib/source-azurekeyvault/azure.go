// Package azurekeyvault reads Azure Key Vault secrets as a cfgkit source.
//
// It carries NO DEPENDENCIES, for the same reason source-gcpsecrets does not:
// reading a secret is one HTTPS GET with a bearer token, and on any Azure
// compute with a managed identity that token comes from the instance metadata
// service already present — one more GET. The official SDK brings the azcore
// pipeline and the whole identity chain for that.
//
// Where the line falls, and it is the same line the catalogue draws around AWS:
// MANAGED IDENTITY OR AN EXPLICIT TOKEN ONLY. Service-principal secrets,
// certificates, device code, Azure CLI credential chaining and workload
// identity federation each need their own flow, and reimplementing that badly
// means a service that authenticates on a laptop and not in production. A
// program that needs one already has azidentity, and cfgkit.SourceFunc wraps it
// in five lines.
package azurekeyvault

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ubgo/cfgkit"
)

const (
	// imdsBase is the link-local instance metadata service, present on Azure
	// VMs, VM scale sets, App Service and Container Apps. The IP is used
	// rather than a hostname so a broken DNS setup fails as a timeout on a
	// known address instead of an unresolvable name.
	imdsBase = "http://169.254.169.254"

	// imdsPath and imdsAPIVersion are the token endpoint. The version is
	// pinned: an unpinned api-version means a future default could change the
	// reply shape under a running fleet.
	imdsPath       = "/metadata/identity/oauth2/token"
	imdsAPIVersion = "2018-02-01"

	// vaultResource is the audience a Key Vault token must be issued for. A
	// token for the wrong audience is syntactically fine and rejected at the
	// vault, which is a confusing 401 — so it is never guessed.
	vaultResource = "https://vault.azure.net"

	// secretsAPIVersion is Key Vault's data-plane version, likewise pinned.
	secretsAPIVersion = "7.4"

	// defaultTimeout bounds each request. A configuration read happens at
	// process start, where hanging forever is worse than failing.
	defaultTimeout = 10 * time.Second

	// maxIMDSReply caps what is read from the metadata service. A token reply
	// is a few kilobytes; the cap exists because the address is link-local and
	// not something the caller controls.
	maxIMDSReply = 1 << 16
)

type options struct {
	vaultURL string
	version  string
	token    string
	clientID string
	client   *http.Client
	ctx      context.Context
	imdsBase string
	optional bool
}

// Option configures a source.
type Option func(*options)

// WithVault sets the vault to read from.
//
// A bare vault NAME is accepted and expanded to
// https://<name>.vault.azure.net, because that is how vaults are referred to
// everywhere in Azure's own tooling. A full URL is used as given, which is what
// sovereign clouds need — their vault suffix is not vault.azure.net.
func WithVault(vault string) Option { return func(o *options) { o.vaultURL = vault } }

// WithVersion reads a specific secret version rather than the current one.
//
// Pinning is worth doing for anything whose rotation should be a deliberate
// deploy rather than a value that changes under a running fleet.
func WithVersion(version string) Option { return func(o *options) { o.version = version } }

// WithToken supplies a bearer token directly, skipping the metadata service.
//
// The token must be issued for the https://vault.azure.net audience. One for a
// different audience is syntactically valid and rejected at the vault, which
// produces a 401 that looks like a permissions problem.
func WithToken(token string) Option { return func(o *options) { o.token = token } }

// WithClientID selects a USER-assigned managed identity.
//
// The default asks for the system-assigned one. A resource with several
// user-assigned identities and no system-assigned identity cannot pick for
// itself, and the metadata service answers with an error rather than a guess —
// so this is the option that failure points at.
func WithClientID(clientID string) Option { return func(o *options) { o.clientID = clientID } }

// WithClient supplies the HTTP client. The default has a 10s timeout and the
// system trust store, which is correct for Azure's public endpoints.
func WithClient(c *http.Client) Option { return func(o *options) { o.client = c } }

// WithContext bounds the reads, so a slow metadata service or vault cannot hold
// up a boot beyond what the caller allows.
func WithContext(ctx context.Context) Option { return func(o *options) { o.ctx = ctx } }

// WithIMDSEndpoint overrides the metadata service address.
//
// It is what lets this package be tested without Azure, and it is a real option
// too: App Service and Container Apps expose the identity endpoint on a
// different address, published to the process as IDENTITY_ENDPOINT.
func WithIMDSEndpoint(base string) Option {
	return func(o *options) {
		if base != "" {
			o.imdsBase = base
		}
	}
}

// Optional makes a MISSING secret an empty source rather than an error.
//
// The default is the opposite of FromFiles, deliberately, and for the same
// reason as every other remote source: you named this secret, so its absence is
// a deployment mistake rather than a normal outcome.
func Optional() Option { return func(o *options) { o.optional = true } }

// Secret reads one Key Vault secret and offers its value for a single
// configuration key.
//
// Key Vault stores one value per secret, so the mapping is stated rather than
// guessed: `key` is the config key this secret answers for. Composing several
// is how a configuration built from several secrets is expressed, and each is
// one API call:
//
//	cfgkit.WithSources(
//		azurekeyvault.Secret("db-password", "DATABASE_PASSWORD", azurekeyvault.WithVault("my-vault")),
//		azurekeyvault.Secret("stripe-key", "STRIPE_KEY", azurekeyvault.WithVault("my-vault")),
//		cfgkit.FromEnviron(),
//	)
func Secret(name, key string, opts ...Option) cfgkit.Source {
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}
	if o.imdsBase == "" {
		o.imdsBase = imdsBase
	}

	value, found, err := fetch(name, o)
	return &source{name: "azurekeyvault:" + name, key: key, value: value, found: found, err: err}
}

// source holds the single value this secret supplies. It is immutable after
// construction, which is what makes it safe for concurrent use.
type source struct {
	name  string
	key   string
	value string
	found bool
	err   error
}

// Name identifies the source in provenance output — "azurekeyvault:db-password".
func (s *source) Name() string { return s.name }

// Lookup answers only for the key this secret was bound to.
//
// A read failure is an ERROR, never a miss. An unreachable vault must not be
// indistinguishable from an unset key, because that difference is a deploy
// proceeding with an empty password.
func (s *source) Lookup(key string) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	if key != s.key || !s.found {
		return "", false, nil
	}
	return s.value, true, nil
}

// Keys implements cfgkit.KeyLister with the single key this source can answer
// for, so a secret bound to a key no field wants is reported rather than
// silently doing nothing.
func (s *source) Keys() []string {
	if !s.found {
		return nil
	}
	return []string{s.key}
}

// secretResponse is the subset of the vault's reply this package reads. Its
// attributes, tags and content type are Key Vault's business.
type secretResponse struct {
	Value string `json:"value"`
}

// tokenResponse is the metadata service's reply.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
}

func fetch(name string, o *options) (string, bool, error) {
	base, err := vaultURL(o.vaultURL)
	if err != nil {
		return "", false, err
	}

	ctx := o.ctx
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), defaultTimeout)
		defer cancel()
	}
	client := o.client
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}

	token := o.token
	if token == "" {
		token, err = imdsToken(ctx, client, o)
		if err != nil {
			return "", false, err
		}
	}

	// The version is a path segment, and an empty one means "current" — which
	// Key Vault spells as a trailing slash rather than as a keyword.
	u := *base
	u.Path = "/secrets/" + url.PathEscape(name)
	if o.version != "" {
		u.Path += "/" + url.PathEscape(o.version)
	}
	q := u.Query()
	q.Set("api-version", secretsAPIVersion)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("reading secret %s: %w", name, err)
	}
	// The body is read-only and already fully consumed; there is
	// nothing a close failure could tell the caller to do.
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotFound && o.optional:
		return "", false, nil
	case resp.StatusCode == http.StatusNotFound:
		return "", false, fmt.Errorf("secret %s not found in %s "+
			"(pass azurekeyvault.Optional() if it is genuinely optional)", name, base.Host)
	case resp.StatusCode == http.StatusForbidden:
		return "", false, fmt.Errorf("secret %s: forbidden — the identity needs the Get "+
			"secret permission, through an access policy or the Key Vault Secrets User role", name)
	case resp.StatusCode == http.StatusUnauthorized:
		return "", false, fmt.Errorf("secret %s: unauthorized — the token was rejected. "+
			"A token for the wrong audience looks like this; it must be issued for %s",
			name, vaultResource)
	case resp.StatusCode != http.StatusOK:
		return "", false, fmt.Errorf("reading secret %s: unexpected status %s", name, resp.Status)
	}

	var out secretResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", false, fmt.Errorf("decoding secret %s: %w", name, err)
	}
	return out.Value, true, nil
}

// vaultURL turns a vault name or URL into the base address to read from.
func vaultURL(vault string) (*url.URL, error) {
	if vault == "" {
		return nil, fmt.Errorf("no vault given: pass azurekeyvault.WithVault with the vault " +
			"name or its full URL")
	}
	if !strings.Contains(vault, "://") {
		// A bare name, which is how vaults are referred to in Azure's own
		// tooling. A full URL is left alone, which is what a sovereign cloud
		// needs — its vault suffix is not vault.azure.net.
		vault = "https://" + vault + ".vault.azure.net"
	}
	u, err := url.Parse(vault)
	if err != nil {
		return nil, fmt.Errorf("invalid vault %q: %w", vault, err)
	}
	return u, nil
}

// imdsToken asks the instance metadata service for a Key Vault token.
func imdsToken(ctx context.Context, client *http.Client, o *options) (string, error) {
	u, err := url.Parse(o.imdsBase)
	if err != nil {
		return "", fmt.Errorf("invalid IMDS endpoint %q: %w", o.imdsBase, err)
	}
	u.Path = imdsPath
	q := u.Query()
	q.Set("api-version", imdsAPIVersion)
	q.Set("resource", vaultResource)
	if o.clientID != "" {
		q.Set("client_id", o.clientID)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	// Required on every IMDS request. Its absence is how the service rejects
	// calls that arrived by accident — through a proxy, say — rather than
	// deliberately.
	req.Header.Set("Metadata", "true")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("no credentials: the instance metadata service is unreachable, "+
			"so this is not running with a managed identity — pass azurekeyvault.WithToken "+
			"outside Azure: %w", err)
	}
	// The body is read-only and already fully consumed; there is
	// nothing a close failure could tell the caller to do.
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIMDSReply))
	if err != nil {
		return "", fmt.Errorf("reading the metadata service's reply: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// IMDS answers 400 when a resource has several user-assigned
		// identities and none was chosen. Naming the option is the difference
		// between a fixable message and a status code.
		if resp.StatusCode == http.StatusBadRequest {
			return "", fmt.Errorf("the metadata service refused the token request (%s) — "+
				"if this resource has more than one user-assigned identity, name it with "+
				"azurekeyvault.WithClientID", resp.Status)
		}
		return "", fmt.Errorf("metadata service returned %s", resp.Status)
	}

	var out tokenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decoding the metadata service's reply: %w", err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("the metadata service returned no access token")
	}
	return out.AccessToken, nil
}
