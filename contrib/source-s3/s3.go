// Package s3 reads a configuration document from an S3 object.
//
// LIKE THE OTHER AWS ADAPTERS it takes a real dependency, for the reason the
// catalogue gives: AWS credential resolution is the part worth delegating, and
// a worse reimplementation authenticates on a laptop and not in production.
//
// IT IS A STRUCTURED SOURCE, and deliberately format-agnostic. An S3 object is
// bytes; what those bytes mean is the caller's decision, so this package
// fetches and hands them to a decoder you name. JSON is provided because
// encoding/json is in the standard library; YAML, TOML and HCL compose through
// the adapters that already parse them, without this module depending on any of
// them.
package s3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/ubgo/cfgkit"
)

// maxObject caps how much of an object is read.
//
// A configuration document is kilobytes. The cap exists because the size is
// decided by whoever can write the bucket, and an unbounded read of a
// mistakenly-uploaded database dump would be an out-of-memory at boot rather
// than an error.
const maxObject = 8 << 20 // 8 MiB

// API is the slice of the S3 client this package uses.
//
// An interface rather than the concrete client, so a test implements it in ten
// lines and this module's suite needs no AWS, no credentials and no network.
type API interface {
	GetObject(ctx context.Context, in *awss3.GetObjectInput,
		optFns ...func(*awss3.Options)) (*awss3.GetObjectOutput, error)
}

type options struct {
	client    API
	ctx       context.Context
	versionID string
	optional  bool
}

// Option configures a source.
type Option func(*options)

// WithClient supplies the S3 client. The default builds one from the SDK's
// standard credential chain.
func WithClient(c API) Option { return func(o *options) { o.client = c } }

// WithContext bounds the read, so a slow bucket cannot hold up a boot beyond
// what the caller allows.
func WithContext(ctx context.Context) Option { return func(o *options) { o.ctx = ctx } }

// WithVersionID pins an object version.
//
// Worth using for a configuration document in a versioned bucket: it turns
// "whatever is in the bucket right now" into a value that changes only when a
// deployment changes it.
func WithVersionID(id string) Option { return func(o *options) { o.versionID = id } }

// Optional makes a MISSING object an empty source rather than an error.
//
// The default is the opposite of FromFiles, deliberately: you named this bucket
// and key, so its absence is a deployment mistake rather than a normal outcome.
func Optional() Option { return func(o *options) { o.optional = true } }

// Decoder turns an object's bytes into fields on the destination struct.
//
// It is the same shape as cfgkit.StructuredFunc's callback, so any parser
// composes: json.Unmarshal, yaml.Unmarshal, or a contrib adapter's own
// decoder. That is what keeps this module free of a format dependency.
type Decoder func(doc []byte, dst any) error

// Object returns a structured source that reads an S3 object and decodes it
// with decode.
//
//	s3src.Object("my-bucket", "config/prod.yaml", yaml.Unmarshal)
//
// The object is fetched ONCE, when the source is constructed, like every other
// source here.
func Object(bucket, key string, decode Decoder, opts ...Option) cfgkit.StructuredSource {
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}

	name := "s3:" + bucket + "/" + key
	doc, found, err := fetch(bucket, key, o)

	return cfgkit.StructuredFunc(name, func(dst any) error {
		if err != nil {
			return err
		}
		if !found {
			// An optional object that is absent: leave the struct untouched,
			// which is what every structured source does when it has nothing
			// to say.
			return nil
		}
		if decode == nil {
			return fmt.Errorf("%s: no decoder given", name)
		}
		if err := decode(doc, dst); err != nil {
			return fmt.Errorf("decoding %s: %w", name, err)
		}
		return nil
	})
}

// JSON is Object with encoding/json, which is the one format this module can
// offer without taking a dependency for it.
func JSON(bucket, key string, opts ...Option) cfgkit.StructuredSource {
	return Object(bucket, key, json.Unmarshal, opts...)
}

func fetch(bucket, key string, o *options) ([]byte, bool, error) {
	ctx := o.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	client := o.client
	if client == nil {
		cfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, false, fmt.Errorf("loading AWS configuration: %w", err)
		}
		client = awss3.NewFromConfig(cfg)
	}

	in := &awss3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}
	if o.versionID != "" {
		in.VersionId = aws.String(o.versionID)
	}

	out, err := client.GetObject(ctx, in)
	if err != nil {
		if o.optional && isNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("reading s3://%s/%s: %w", bucket, key, err)
	}
	// The body is read-only and already fully consumed; there is
	// nothing a close failure could tell the caller to do.
	defer func() { _ = out.Body.Close() }()

	// Bounded, and the limit is checked rather than silently truncating: a
	// half-read configuration document parses to something, and something is
	// worse than an error.
	doc, err := io.ReadAll(io.LimitReader(out.Body, maxObject+1))
	if err != nil {
		return nil, false, fmt.Errorf("reading s3://%s/%s: %w", bucket, key, err)
	}
	if len(doc) > maxObject {
		return nil, false, fmt.Errorf("s3://%s/%s is larger than %d bytes, "+
			"which is not a configuration document", bucket, key, maxObject)
	}
	return doc, true, nil
}

// isNotFound reports whether an error is S3's missing-object.
//
// BOTH shapes are matched, and that is not redundancy: S3 answers NoSuchKey
// when the caller may list the bucket, and a bare 404 (NotFound) when it may
// not — so a role with GetObject but not ListBucket sees a different error for
// the same missing object.
func isNotFound(err error) bool {
	var noKey *s3types.NoSuchKey
	if errors.As(err, &noKey) {
		return true
	}
	var notFound *s3types.NotFound
	return errors.As(err, &notFound)
}
