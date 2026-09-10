// Package gcpsecrets reads Google Secret Manager secrets as a cfgkit source.
//
// It carries NO DEPENDENCIES. Reading a secret version is one HTTPS GET with a
// bearer token, and on GCE, GKE, Cloud Run and Cloud Functions the token comes
// from the metadata server that is already there — one more GET. The official
// client brings gRPC, protobuf and the google-api stack for that.
//
// WHY THIS IS FEASIBLE HERE AND NOT FOR AWS, since the catalogue says AWS stays
// absent for exactly this reason: GCP's workload identity hands you a finished
// bearer token from a well-known URL. AWS asks you to resolve a credential
// chain and then sign each request with SigV4, and reimplementing that
// resolution badly means a service that authenticates on a laptop and not in
// production. The asymmetry is in the platforms, not in the effort.
//
// The cost is stated in the README: metadata-server or explicit-token auth
// only, so a service-account JSON key file needs the official client — signing
// a JWT with an RSA key is where this would stop being a small package.
package gcpsecrets

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ubgo/cfgkit"
)

const (
	// apiBase is Secret Manager's REST endpoint.
	apiBase = "https://secretmanager.googleapis.com"

	// metadataBase is the link-local metadata server present on every GCE,
	// GKE, Cloud Run and Cloud Functions instance. The IP is used rather than
	// metadata.google.internal so a broken DNS setup fails as a timeout on a
	// known address instead of an unresolvable name.
	metadataBase = "http://169.254.169.254"

	// metadataFlavor is required on every metadata request. Its absence is how
	// the server rejects requests that reached it by accident — a proxy, say —
	// rather than deliberately.
	metadataFlavor = "Metadata-Flavor"

	// envProject is the variable Google's own libraries read for the project.
	envProject = "GOOGLE_CLOUD_PROJECT"

	// latestVersion is Secret Manager's alias for the newest enabled version.
	latestVersion = "latest"

	// maxMetadataReply caps what is read from the metadata server. A token or
	// a project id is a few hundred bytes; the cap exists because the address
	// is link-local and not something the caller controls.
	maxMetadataReply = 1 << 16

	// defaultTimeout bounds each request. A configuration read happens at
	// process start, where hanging forever is worse than failing.
	defaultTimeout = 10 * time.Second
)

type options struct {
	project  string
	version  string
	token    string
	client   *http.Client
	ctx      context.Context
	apiBase  string
	mdBase   string
	optional bool
}

// Option configures a source.
type Option func(*options)

// WithProject sets the Google Cloud project.
//
// The default is $GOOGLE_CLOUD_PROJECT, then the project the metadata server
// reports — so inside GCP it usually needs no setting at all.
func WithProject(project string) Option { return func(o *options) { o.project = project } }

// WithVersion reads a specific secret version rather than "latest".
//
// Pinning a version is worth doing for anything whose rotation should be a
// deliberate deploy rather than a value that changes under a running fleet.
func WithVersion(version string) Option { return func(o *options) { o.version = version } }

// WithToken supplies an OAuth2 access token directly, skipping the metadata
// server.
//
// Use it when your program already has credentials — from the official client,
// from `gcloud auth print-access-token`, or from a workload identity flow you
// run yourself.
func WithToken(token string) Option { return func(o *options) { o.token = token } }

// WithClient supplies the HTTP client. The default has a 10s timeout and the
// system trust store, which is correct for Google's public endpoint.
func WithClient(c *http.Client) Option { return func(o *options) { o.client = c } }

// WithContext bounds the reads, so a slow metadata server or API cannot hold up
// a boot beyond what the caller allows.
func WithContext(ctx context.Context) Option { return func(o *options) { o.ctx = ctx } }

// WithEndpoints overrides the API and metadata addresses.
//
// It exists so the package can be tested without Google — and it is a real
// option too: Private Service Connect and a few regulated environments serve
// Secret Manager on a different host. Passing an empty string keeps a default.
func WithEndpoints(api, metadata string) Option {
	return func(o *options) {
		if api != "" {
			o.apiBase = api
		}
		if metadata != "" {
			o.mdBase = metadata
		}
	}
}

// Optional makes a MISSING secret an empty source rather than an error.
//
// The default is the opposite of FromFiles, deliberately, and for the same
// reason as every other remote source: you named this secret, so its absence is
// a deployment mistake rather than a normal outcome.
func Optional() Option { return func(o *options) { o.optional = true } }

// Secret reads one Secret Manager secret and offers its payload as the value
// for a single configuration key.
//
// One secret holds one value in this service, so the mapping is stated rather
// than guessed: `key` is the config key this secret answers for. Composing
// several is how a configuration built from several secrets is expressed, and
// each is one API call:
//
//	cfgkit.WithSources(
//		gcpsecrets.Secret("db-password", "DATABASE_PASSWORD"),
//		gcpsecrets.Secret("stripe-key", "STRIPE_KEY"),
//		cfgkit.FromEnviron(),
//	)
//
// For the common pattern of storing a whole .env file in ONE secret, read the
// payload yourself and hand it to dotenv — the README shows it in five lines.
// This package does not guess at a format.
func Secret(name, key string, opts ...Option) cfgkit.Source {
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}
	if o.version == "" {
		o.version = latestVersion
	}
	if o.apiBase == "" {
		o.apiBase = apiBase
	}
	if o.mdBase == "" {
		o.mdBase = metadataBase
	}

	value, found, err := fetch(name, o)
	return &source{
		name:  "gcpsecrets:" + name,
		key:   key,
		value: value,
		found: found,
		err:   err,
	}
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

// Name identifies the source in provenance output — "gcpsecrets:db-password",
// naming the secret so Explain says which one a value came from.
func (s *source) Name() string { return s.name }

// Lookup answers only for the key this secret was bound to.
//
// A read failure is an ERROR, never a miss. An unreachable Secret Manager must
// not be indistinguishable from an unset key, because that difference is a
// deploy proceeding with an empty password.
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

// accessResponse is the subset of the access reply this package reads.
type accessResponse struct {
	Payload struct {
		Data string `json:"data"` // base64
	} `json:"payload"`
}

// tokenResponse is the metadata server's reply.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
}

func fetch(name string, o *options) (string, bool, error) {
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
	var err error
	if token == "" {
		token, err = metadataToken(ctx, client, o.mdBase)
		if err != nil {
			return "", false, err
		}
	}

	project := o.project
	if project == "" {
		project = os.Getenv(envProject)
	}
	if project == "" {
		project, err = metadataProject(ctx, client, o.mdBase)
		if err != nil {
			return "", false, err
		}
	}

	u, err := url.Parse(o.apiBase)
	if err != nil {
		return "", false, fmt.Errorf("invalid API endpoint %q: %w", o.apiBase, err)
	}
	// Built with explicit escaping rather than path.Join: the ":access" suffix
	// is part of the method name in Google's REST mapping, not a path segment,
	// and Join would leave it alone but a future refactor to url.JoinPath
	// would escape the colon and produce a 404 nobody could explain.
	u.Path = fmt.Sprintf("/v1/projects/%s/secrets/%s/versions/%s:access",
		url.PathEscape(project), url.PathEscape(name), url.PathEscape(o.version))

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
		return "", false, fmt.Errorf("secret %s (version %s) not found in project %q "+
			"(pass gcpsecrets.Optional() if it is genuinely optional)", name, o.version, project)
	case resp.StatusCode == http.StatusForbidden:
		return "", false, fmt.Errorf("secret %s: permission denied — the service account needs "+
			"roles/secretmanager.secretAccessor on it", name)
	case resp.StatusCode == http.StatusUnauthorized:
		return "", false, fmt.Errorf("secret %s: unauthorized — the token was rejected", name)
	case resp.StatusCode != http.StatusOK:
		return "", false, fmt.Errorf("reading secret %s: unexpected status %s", name, resp.Status)
	}

	var out accessResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", false, fmt.Errorf("decoding secret %s: %w", name, err)
	}
	raw, err := base64.StdEncoding.DecodeString(out.Payload.Data)
	if err != nil {
		// The secret NAME is named and the payload is not. An error message is
		// a thing that gets logged.
		return "", false, fmt.Errorf("secret %s: payload is not valid base64", name)
	}
	return string(raw), true, nil
}

// metadataToken asks the instance metadata server for an access token.
func metadataToken(ctx context.Context, client *http.Client, base string) (string, error) {
	var out tokenResponse
	err := metadataJSON(ctx, client, base,
		"/computeMetadata/v1/instance/service-accounts/default/token", &out)
	if err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("the metadata server returned no access token")
	}
	return out.AccessToken, nil
}

// metadataProject asks the metadata server which project this instance is in.
func metadataProject(ctx context.Context, client *http.Client, base string) (string, error) {
	body, err := metadataText(ctx, client, base, "/computeMetadata/v1/project/project-id")
	if err != nil {
		return "", fmt.Errorf("no project given and the metadata server could not supply one "+
			"(outside GCP, pass gcpsecrets.WithProject or set %s): %w", envProject, err)
	}
	return strings.TrimSpace(body), nil
}

func metadataJSON(ctx context.Context, client *http.Client, base, path string, out any) error {
	body, err := metadataText(ctx, client, base, path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(body), out); err != nil {
		return fmt.Errorf("decoding the metadata server's reply: %w", err)
	}
	return nil
}

// metadataText performs one metadata request.
//
// The error deliberately names what the absence MEANS — no workload identity —
// rather than reporting a connection failure to a link-local address, which
// tells a reader running outside GCP nothing they can act on.
func metadataText(ctx context.Context, client *http.Client, base, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set(metadataFlavor, "Google")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("no credentials: the metadata server is unreachable, so this is "+
			"not running with workload identity — pass gcpsecrets.WithToken outside GCP: %w", err)
	}
	// The body is read-only and already fully consumed; there is
	// nothing a close failure could tell the caller to do.
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("metadata server returned %s for %s", resp.Status, path)
	}
	// Bounded: a metadata reply is a token or a project id. Reading an
	// unbounded body from a link-local address a caller does not control would
	// be the only place this package could be made to allocate without limit.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadataReply))
	if err != nil {
		return "", fmt.Errorf("reading the metadata server's reply: %w", err)
	}
	return string(body), nil
}
