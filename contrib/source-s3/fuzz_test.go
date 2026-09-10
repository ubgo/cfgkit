// Fuzzing the OBJECT path: whatever bytes the bucket holds, this package must
// either bind them or return an error.
//
// An S3 object is written by something else — a pipeline, a human, another
// service — and may be truncated, half-written, or simply not the format
// anyone expected. None of that may crash the program reading its config.
package s3_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/ubgo/cfgkit"
	s3src "github.com/ubgo/cfgkit/contrib/source-s3"
)

// fuzzAPI serves whatever bytes the fuzzer produced.
type fuzzAPI struct{ body []byte }

func (f fuzzAPI) GetObject(context.Context, *awss3.GetObjectInput,
	...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
	return &awss3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(f.body))}, nil
}

func FuzzS3ObjectNeverPanics(f *testing.F) {
	for _, seed := range []string{
		"", "{}", "null", "[]", "{\"host\":\"h\"}", "{\"port\":\"not-an-int\"}",
		"{\"nested\":{\"inner\":1}}", "{", "\x00\xff", "<html/>",
		"{\"port\":99999999999999999999}",
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		type nested struct {
			Inner string `json:"inner"`
		}
		type cfg struct {
			Host   string  `json:"host"`
			Port   int     `json:"port"`
			Ratio  float64 `json:"ratio"`
			Nested *nested `json:"nested"`
		}
		_, _, _ = cfgkit.Load[cfg](cfgkit.WithSources(
			s3src.JSON("b", "k", s3src.WithClient(fuzzAPI{body: body})),
		))
	})
}
