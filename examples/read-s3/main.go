// Command read-s3 reads a configuration document out of an S3 object.
//
// It RUNS with no AWS account: the example supplies a fake client through the
// module's API seam. That seam exists because the AWS adapters take the SDK as
// a real dependency — credential resolution is the part worth delegating — and
// a package that takes an SDK must still be testable without an account.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/ubgo/cfgkit"
	s3 "github.com/ubgo/cfgkit/contrib/source-s3"
)

// Config nests, because an S3 object holds a document rather than flat keys.
type Config struct {
	Service string `json:"service" env:"SERVICE" default:"api"`

	Server struct {
		Port int    `json:"port" env:"PORT" default:"8080"`
		Host string `json:"host" env:"HOST" default:"0.0.0.0"`
	} `json:"server"`
}

const (
	bucket = "acme-config"
	key    = "checkout/production.json"
)

// document is what the object contains.
var document = []byte(`{"service":"checkout","server":{"port":9000}}`)

// fakeS3 implements s3.API — one method, because reading one object is one
// call. A narrow interface is what makes faking it a few lines instead of a
// mock framework.
type fakeS3 struct{ body []byte }

// GetObject serves the fixture, and refuses any other object so a wrong
// bucket or key fails loudly rather than returning the right answer by luck.
func (f fakeS3) GetObject(_ context.Context, in *awss3.GetObjectInput,
	_ ...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
	if *in.Bucket != bucket || *in.Key != key {
		return nil, fmt.Errorf("unexpected object s3://%s/%s", *in.Bucket, *in.Key)
	}
	return &awss3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(f.body))}, nil
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	// Real usage: s3.JSON("acme-config", "checkout/production.json").
	// The credential chain is the SDK's — environment, shared config, IMDS,
	// IRSA, SSO, assume-role — which is exactly the part not worth rewriting.
	cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
		s3.JSON(bucket, key, s3.WithClient(fakeS3{body: document})),
		cfgkit.FromEnviron(),
	))
	if err != nil {
		return err
	}

	// Host was in no document and no variable, so the default stands: an
	// object is a document, and a structured source merges onto the struct.
	_, _ = fmt.Fprintf(w, "%s on %s:%d\n", cfg.Service, cfg.Server.Host, cfg.Server.Port)
	_, _ = fmt.Fprintln(w)
	return res.Explain(w)
}
