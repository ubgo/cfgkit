// Package ssm reads AWS Systems Manager Parameter Store parameters as a cfgkit
// source.
//
// UNLIKE THE OTHER REMOTE SOURCES IN THIS CATALOGUE, IT TAKES A REAL
// DEPENDENCY, and that is the decision worth explaining because the rest do
// not. source-k8s, source-vault, source-consul, source-etcd, source-gcpsecrets
// and source-azurekeyvault each reach their service with net/http, because on
// those platforms a credential is a file or a header.
//
// AWS is different in kind. Signing is straightforward; CREDENTIAL RESOLUTION
// is not — environment variables, the shared config file, IMDS, IRSA web
// identity, SSO, and assume-role chains, each of which changes independently of
// this package. A worse reimplementation of that produces a service which
// authenticates on a developer's laptop and not in production, which is the
// failure mode hardest to catch before it matters. So aws-sdk-go-v2 does the
// part it is good at, and this module is a thin, well-documented adapter over
// it. That is exactly what the contrib/ split exists to make safe: a program
// that never reads Parameter Store never compiles this package and never sees
// the SDK in its go.sum.
package ssm

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/ubgo/cfgkit"
)

// API is the slice of the Parameter Store client this package uses.
//
// It is an interface rather than the concrete client for one reason: a test
// can implement it in ten lines, so this module's suite needs no AWS, no
// credentials and no network. Taking the SDK as a dependency does not require
// taking its test harness too.
type API interface {
	GetParametersByPath(ctx context.Context, in *ssm.GetParametersByPathInput,
		optFns ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error)
}

type options struct {
	client    API
	ctx       context.Context
	decrypt   bool
	recursive bool
	optional  bool
}

// Option configures a source.
type Option func(*options)

// WithClient supplies the Parameter Store client.
//
// The default builds one from the SDK's standard credential chain, which is the
// whole reason this module takes the dependency. Pass your own when the program
// already has a configured client, or to point at a different region or a
// LocalStack endpoint.
func WithClient(c API) Option { return func(o *options) { o.client = c } }

// WithContext bounds the read, so a slow API cannot hold up a boot beyond what
// the caller allows.
func WithContext(ctx context.Context) Option { return func(o *options) { o.ctx = ctx } }

// WithoutDecryption reads SecureString parameters WITHOUT decrypting them.
//
// Decryption is the default because a SecureString that arrives as ciphertext
// is not a configuration value, and silently binding the encrypted blob would
// be the quiet kind of failure this package exists to prevent. Turning it off
// needs a reason — usually that the role deliberately lacks kms:Decrypt.
func WithoutDecryption() Option { return func(o *options) { o.decrypt = false } }

// WithoutRecursion reads only the parameters directly under the path, rather
// than the whole subtree.
func WithoutRecursion() Option { return func(o *options) { o.recursive = false } }

// Optional makes an EMPTY path an empty source rather than an error.
//
// The default is the opposite of FromFiles, deliberately, and for the same
// reason as every other remote source: you named this path, so finding nothing
// under it is a deployment mistake rather than a normal outcome.
func Optional() Option { return func(o *options) { o.optional = true } }

// Path reads every parameter under a Parameter Store path as a flat source.
//
// Names arrive RELATIVE to the path: with `/app/config/` holding
// `/app/config/PORT`, the source answers for `PORT`. That is what lets one
// struct bind from Parameter Store and from a .env file with no second set of
// tags. A nested path keeps its separator — `/app/config/db/HOST` becomes
// `db/HOST` — because a flat source cannot express a tree.
//
// The read happens ONCE, here, paging through the whole path. cfgkit calls
// Lookup once per bound field, so a source that called the API per key would
// turn a twenty-field configuration into twenty round-trips, and Parameter
// Store's throughput is rate-limited per account.
func Path(path string, opts ...Option) cfgkit.Source {
	o := &options{decrypt: true, recursive: true}
	for _, fn := range opts {
		fn(o)
	}

	clean := "/" + strings.Trim(path, "/")
	data, err := fetch(clean, o)

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	return &source{name: "ssm:" + clean, data: data, err: err, keys: keys}
}

// source is the loaded path, immutable after construction — which is what makes
// it safe for the concurrent use cfgkit's Source contract requires.
type source struct {
	name string
	data map[string]string
	err  error
	keys []string
}

// Name identifies the source in provenance output — "ssm:/app/config".
func (s *source) Name() string { return s.name }

// Lookup returns the value for key.
//
// A read failure is an ERROR, never a miss. An unreachable or forbidden
// Parameter Store must not be indistinguishable from an unset key, because that
// difference is a deploy proceeding with an empty password.
func (s *source) Lookup(key string) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	v, ok := s.data[key]
	return v, ok, nil
}

// Keys implements cfgkit.KeyLister, so a parameter under the path that matches
// no field is reported as a probable typo.
func (s *source) Keys() []string { return s.keys }

func fetch(path string, o *options) (map[string]string, error) {
	ctx := o.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	client := o.client
	if client == nil {
		cfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			// The SDK's own message names which step of the chain failed,
			// which is more useful than anything this package could add.
			return nil, fmt.Errorf("loading AWS configuration: %w", err)
		}
		client = ssm.NewFromConfig(cfg)
	}

	out := map[string]string{}
	var token *string
	for {
		page, err := client.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
			Path:           aws.String(path),
			Recursive:      aws.Bool(o.recursive),
			WithDecryption: aws.Bool(o.decrypt),
			NextToken:      token,
		})
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		for _, p := range page.Parameters {
			name, value := aws.ToString(p.Name), aws.ToString(p.Value)
			rel := strings.TrimPrefix(strings.TrimPrefix(name, path), "/")
			if rel == "" {
				continue
			}
			// A StringList needs NO special handling: it arrives
			// comma-separated, which is exactly what cfgkit's slice decoder
			// already expects. This comment exists so nobody adds a branch
			// that splits and rejoins it.
			out[rel] = value
		}
		// PAGING IS NOT OPTIONAL: GetParametersByPath returns at most ten
		// parameters per call, so a configuration of eleven silently loses one
		// without this loop. That is the single easiest mistake to make
		// against this API.
		if page.NextToken == nil || aws.ToString(page.NextToken) == "" {
			break
		}
		token = page.NextToken
	}

	if len(out) == 0 && !o.optional {
		return nil, fmt.Errorf("no parameters under %s "+
			"(pass ssm.Optional() if that is expected)", path)
	}
	return out, nil
}
