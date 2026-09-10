// Package secretsmanager reads AWS Secrets Manager secrets as a cfgkit source.
//
// LIKE source-ssm AND UNLIKE THE OTHER REMOTE SOURCES, it takes a real
// dependency. AWS credential resolution — environment, shared config, IMDS,
// IRSA, SSO, assume-role chains — is the part worth delegating, because a
// worse reimplementation authenticates on a developer's laptop and not in
// production. aws-sdk-go-v2 does that; this module is a thin adapter over it,
// in its own module so a program that never reads Secrets Manager never
// compiles the SDK.
//
// THE ONE REAL DESIGN QUESTION here is what a secret's payload means. Secrets
// Manager stores a blob, and the console encourages JSON — so this package
// offers both readings explicitly, JSON and Whole, rather than sniffing at the
// content and guessing. A guess that is right nine times and silent the tenth
// is worse than an argument.
package secretsmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	sm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/ubgo/cfgkit"
)

// API is the slice of the Secrets Manager client this package uses.
//
// It is an interface rather than the concrete client so a test can implement it
// in ten lines: this module's suite needs no AWS, no credentials and no
// network. Taking the SDK as a dependency does not require taking its test
// harness too.
type API interface {
	GetSecretValue(ctx context.Context, in *sm.GetSecretValueInput,
		optFns ...func(*sm.Options)) (*sm.GetSecretValueOutput, error)
}

type options struct {
	client    API
	ctx       context.Context
	versionID string
	stage     string
	optional  bool
}

// Option configures a source.
type Option func(*options)

// WithClient supplies the Secrets Manager client.
//
// The default builds one from the SDK's standard credential chain, which is the
// whole reason this module takes the dependency.
func WithClient(c API) Option { return func(o *options) { o.client = c } }

// WithContext bounds the read, so a slow API cannot hold up a boot beyond what
// the caller allows.
func WithContext(ctx context.Context) Option { return func(o *options) { o.ctx = ctx } }

// WithVersionID pins an exact secret version.
func WithVersionID(id string) Option { return func(o *options) { o.versionID = id } }

// WithVersionStage reads a labelled version — AWSCURRENT by default, or
// AWSPREVIOUS to read the value before the last rotation.
//
// Rotation is what makes this worth having: AWSPREVIOUS is how a service that
// missed a rotation window can still connect while it is fixed.
func WithVersionStage(stage string) Option { return func(o *options) { o.stage = stage } }

// Optional makes a MISSING secret an empty source rather than an error.
//
// The default is the opposite of FromFiles, deliberately, and for the same
// reason as every other remote source: you named this secret, so its absence is
// a deployment mistake.
func Optional() Option { return func(o *options) { o.optional = true } }

// JSON reads a secret whose payload is a JSON object, and offers each member as
// a configuration key.
//
// This is what the Secrets Manager console produces when you enter key/value
// pairs, and it is the common shape:
//
//	{"DATABASE_URL": "postgres://...", "PORT": 9000}
//
// Values are rendered the way any JSON-shaped source is: a string passes
// through, a number or bool is formatted, null becomes empty, and a nested
// object or array is re-encoded as JSON so a field with an UnmarshalText can
// still take it. A number renders as "9000" rather than "9000.000000", which
// the obvious implementation gets wrong.
//
// A payload that is NOT a JSON object is an error here rather than a silent
// empty source — use Whole for a plain string secret.
func JSON(name string, opts ...Option) cfgkit.Source {
	return newSource(name, "", opts)
}

// Whole reads a secret whose payload is a single value, and offers it as the
// value for one configuration key.
//
// Use it for a plain string secret — a password, a token, a private key:
//
//	secretsmanager.Whole("prod/db/password", "DATABASE_PASSWORD")
func Whole(name, key string, opts ...Option) cfgkit.Source {
	return newSource(name, key, opts)
}

func newSource(name, wholeKey string, opts []Option) cfgkit.Source {
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}

	data, err := fetch(name, wholeKey, o)
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	return &source{name: "secretsmanager:" + name, data: data, err: err, keys: keys}
}

// source is the loaded secret, immutable after construction.
type source struct {
	name string
	data map[string]string
	err  error
	keys []string
}

// Name identifies the source in provenance output.
func (s *source) Name() string { return s.name }

// Lookup returns the value for key.
//
// A read failure is an ERROR, never a miss. An unreachable or forbidden Secrets
// Manager must not be indistinguishable from an unset key, because that
// difference is a deploy proceeding with an empty password.
func (s *source) Lookup(key string) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	v, ok := s.data[key]
	return v, ok, nil
}

// Keys implements cfgkit.KeyLister, so a member of a JSON secret that matches
// no field is reported as a probable typo.
func (s *source) Keys() []string { return s.keys }

func fetch(name, wholeKey string, o *options) (map[string]string, error) {
	ctx := o.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	client := o.client
	if client == nil {
		cfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("loading AWS configuration: %w", err)
		}
		client = sm.NewFromConfig(cfg)
	}

	in := &sm.GetSecretValueInput{SecretId: aws.String(name)}
	if o.versionID != "" {
		in.VersionId = aws.String(o.versionID)
	}
	if o.stage != "" {
		in.VersionStage = aws.String(o.stage)
	}

	out, err := client.GetSecretValue(ctx, in)
	if err != nil {
		if o.optional && isNotFound(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("reading secret %s: %w", name, err)
	}

	// A secret is either a string or binary. Binary is not configuration — it
	// is a certificate or a keystore — so it is refused by name rather than
	// coerced into a string that would bind as mojibake.
	if out.SecretString == nil {
		return nil, fmt.Errorf("secret %s holds binary data, which is not configuration", name)
	}
	payload := aws.ToString(out.SecretString)

	if wholeKey != "" {
		return map[string]string{wholeKey: payload}, nil
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		// The PAYLOAD never enters the message, and the decoder's error is
		// dropped with it: a json.SyntaxError carries an offset, but an
		// UnmarshalTypeError quotes the value it choked on, and a secret is a
		// thing that gets logged when an error is logged.
		return nil, fmt.Errorf("secret %s is not a JSON object "+
			"(use secretsmanager.Whole for a plain string secret)", name)
	}
	return stringify(obj), nil
}

// isNotFound reports whether an error is Secrets Manager's not-found.
//
// It matches the SDK's own error TYPE rather than message text, because message
// text is not an API and changes without notice.
func isNotFound(err error) bool {
	var nf *smtypes.ResourceNotFoundException
	return errors.As(err, &nf)
}

// stringify renders JSON values as the strings a Source deals in.
//
// The mapping is stated rather than assumed because a secret may hold any JSON.
// Numbers matter most: encoding/json gives every number as a float64, and the
// obvious formatting turns 9000 into "9000.000000", which then fails to parse
// as an int.
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
			out[k] = strconv.FormatFloat(t, 'g', -1, 64)
		default:
			b, err := json.Marshal(t)
			if err != nil {
				continue
			}
			out[k] = string(b)
		}
	}
	return out
}
