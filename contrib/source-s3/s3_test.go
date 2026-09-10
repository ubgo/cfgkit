// The suite implements the API interface directly, so it needs no AWS, no
// credentials and no network.
//
// TWO STATEMENTS ARE DELIBERATELY LEFT UNCOVERED: LoadDefaultConfig's error
// return, which needs a broken AWS config file on the machine running the
// tests, and http.NewRequest-style plumbing inside the SDK that this package
// does not own.
package s3_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/ubgo/cfgkit"
	s3src "github.com/ubgo/cfgkit/contrib/source-s3"
)

type appCfg struct {
	Host string `env:"HOST" json:"host" default:"localhost"`
	Port int    `env:"PORT" json:"port" default:"8080"`
}

// fakeAPI serves one object and records what was asked for.
type fakeAPI struct {
	body   string
	err    error
	calls  int
	inputs []*awss3.GetObjectInput
}

func (f *fakeAPI) GetObject(_ context.Context, in *awss3.GetObjectInput,
	_ ...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
	f.calls++
	f.inputs = append(f.inputs, in)
	if f.err != nil {
		return nil, f.err
	}
	return &awss3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(f.body))}, nil
}

func TestJSONObjectBinds(t *testing.T) {
	api := &fakeAPI{body: `{"host":"s3.internal","port":9000}`}

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		s3src.JSON("my-bucket", "config/prod.json", s3src.WithClient(api)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "s3.internal" || got.Port != 9000 {
		t.Errorf("cfg = %+v", got)
	}
	if aws.ToString(api.inputs[0].Bucket) != "my-bucket" ||
		aws.ToString(api.inputs[0].Key) != "config/prod.json" {
		t.Errorf("asked for %s/%s", aws.ToString(api.inputs[0].Bucket), aws.ToString(api.inputs[0].Key))
	}
	for _, f := range res.Fields() {
		if f.Path == "Host" && f.Source != "s3:my-bucket/config/prod.json" {
			t.Errorf("Host source = %q, want bucket and key named", f.Source)
		}
	}
}

// TestAnyDecoderComposes is the point of the module being format-agnostic. An
// S3 object is bytes; what they mean is the caller's decision, so a parser this
// module does not depend on still works.
func TestAnyDecoderComposes(t *testing.T) {
	// A deliberately silly "format" — one key per line — to prove the seam
	// takes anything, not just the parsers that happen to be popular.
	lines := func(doc []byte, dst any) error {
		cfg, ok := dst.(*appCfg)
		if !ok {
			return fmt.Errorf("unexpected destination %T", dst)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(doc)), "\n") {
			k, v, found := strings.Cut(line, "=")
			if !found {
				return fmt.Errorf("bad line %q", line)
			}
			if k == "host" {
				cfg.Host = v
			}
		}
		return nil
	}

	api := &fakeAPI{body: "host=from-lines\n"}
	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		s3src.Object("b", "k", lines, s3src.WithClient(api)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "from-lines" {
		t.Errorf("Host = %q, want the caller's own decoder to have run", got.Host)
	}
}

// TestAbsentFieldsAreLeftAlone is the overlay property every structured source
// must have: a document supplies what it mentions and nothing else.
func TestAbsentFieldsAreLeftAlone(t *testing.T) {
	api := &fakeAPI{body: `{"host":"only-host"}`}

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		s3src.JSON("b", "k", s3src.WithClient(api)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 8080 {
		t.Errorf("Port = %d, want the default — the document did not mention it", got.Port)
	}
}

func TestDecodeFailureNamesTheObject(t *testing.T) {
	api := &fakeAPI{body: "{not json"}

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		s3src.JSON("my-bucket", "config/prod.json", s3src.WithClient(api)),
	))
	if err == nil {
		t.Fatal("want a decode error")
	}
	if !strings.Contains(err.Error(), "my-bucket/config/prod.json") {
		t.Errorf("error %q should name the object", err)
	}
}

func TestNoDecoderIsAnError(t *testing.T) {
	api := &fakeAPI{body: "{}"}

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		s3src.Object("b", "k", nil, s3src.WithClient(api)),
	))
	if err == nil || !strings.Contains(err.Error(), "no decoder") {
		t.Errorf("want a missing-decoder error, got %v", err)
	}
}

// TestOversizedObjectIsRefused pins the bound. The size is decided by whoever
// can write the bucket, so an unbounded read of a mistakenly-uploaded database
// dump would be an out-of-memory at boot rather than an error.
func TestOversizedObjectIsRefused(t *testing.T) {
	api := &fakeAPI{body: strings.Repeat("x", (8<<20)+1)}

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		s3src.JSON("b", "huge", s3src.WithClient(api)),
	))
	if err == nil {
		t.Fatal("want an error for an object past the cap")
	}
	if !strings.Contains(err.Error(), "not a configuration document") {
		t.Errorf("error %q should say why the size is refused", err)
	}
}

// TestBothNotFoundShapesAreRecognised is not redundancy. S3 answers NoSuchKey
// when the caller may list the bucket and a bare NotFound when it may not — so
// a role with GetObject but no ListBucket sees a different error for the same
// missing object, and Optional() must honour both.
func TestBothNotFoundShapesAreRecognised(t *testing.T) {
	for name, apiErr := range map[string]error{
		"NoSuchKey": &s3types.NoSuchKey{Message: aws.String("The specified key does not exist.")},
		"NotFound":  &s3types.NotFound{Message: aws.String("Not Found")},
	} {
		t.Run(name, func(t *testing.T) {
			got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
				s3src.JSON("b", "absent", s3src.WithClient(&fakeAPI{err: apiErr}), s3src.Optional()),
			))
			if err != nil {
				t.Fatalf("an optional missing object must not fail: %v", err)
			}
			if got.Host != "localhost" {
				t.Errorf("Host = %q, want the default", got.Host)
			}
		})
	}
}

func TestMissingObjectIsAnErrorByDefault(t *testing.T) {
	api := &fakeAPI{err: &s3types.NoSuchKey{Message: aws.String("nope")}}

	err := cfgkit.Check[appCfg](cfgkit.WithSources(s3src.JSON("b", "absent", s3src.WithClient(api))))
	if err == nil {
		t.Fatal("want an error: the named object does not exist")
	}
	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("want a *SourceError, got %T", err)
	}
}

// TestOptionalDoesNotSwallowOtherFailures is the important half of Optional: it
// means "absent is fine", not "errors are fine". An access-denied must still
// fail, or a misconfigured role looks like an empty configuration.
func TestOptionalDoesNotSwallowOtherFailures(t *testing.T) {
	api := &fakeAPI{err: errors.New("AccessDenied: not authorized")}

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		s3src.JSON("b", "k", s3src.WithClient(api), s3src.Optional()),
	))
	if err == nil {
		t.Fatal("Optional() must not swallow an access-denied error")
	}
	if !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("error %q should keep the SDK's message", err)
	}
}

func TestVersionPinningReachesTheRequest(t *testing.T) {
	api := &fakeAPI{body: `{"host":"x"}`}

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		s3src.JSON("b", "k", s3src.WithClient(api), s3src.WithVersionID("v-123")),
	)); err != nil {
		t.Fatal(err)
	}
	if aws.ToString(api.inputs[0].VersionId) != "v-123" {
		t.Errorf("VersionId = %q", aws.ToString(api.inputs[0].VersionId))
	}
}

func TestReadHappensOncePerSource(t *testing.T) {
	api := &fakeAPI{body: `{"host":"svc","port":9000}`}

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		s3src.JSON("b", "k", s3src.WithClient(api)),
	)); err != nil {
		t.Fatal(err)
	}
	if api.calls != 1 {
		t.Errorf("made %d calls, want exactly 1", api.calls)
	}
}

// TestDefaultClientUsesTheSDKChain covers the path a real deployment takes. The
// context is CANCELLED first, so nothing leaves the machine.
func TestDefaultClientUsesTheSDKChain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		s3src.JSON("b", "k", s3src.WithContext(ctx)),
	))
	if err == nil {
		t.Fatal("want an error: the context was cancelled before the request")
	}
}

func TestPrecedenceAgainstOtherSources(t *testing.T) {
	api := &fakeAPI{body: `{"host":"from-s3"}`}

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		s3src.JSON("b", "k", s3src.WithClient(api)),
		cfgkit.FromMap(map[string]string{"PORT": "1234"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "from-s3" {
		t.Errorf("Host = %q, want the document's value", got.Host)
	}
	if got.Port != 1234 {
		t.Errorf("Port = %d, want the later flat source to win", got.Port)
	}
}

// TestPayloadBuiltWithEncodingJSONBinds checks the JSON helper agrees with the
// standard marshaller it wraps, rather than only with hand-written literals.
func TestPayloadBuiltWithEncodingJSONBinds(t *testing.T) {
	b, err := json.Marshal(map[string]any{"host": "marshalled", "port": 7000})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		s3src.JSON("b", "k", s3src.WithClient(&fakeAPI{body: string(b)})),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "marshalled" || got.Port != 7000 {
		t.Errorf("cfg = %+v", got)
	}
}

// TestTruncatedBodyIsReported covers a connection that drops part-way through
// the object. It must fail rather than decode whatever arrived: a half-read
// configuration document parses to something, and something is worse than an
// error.
func TestTruncatedBodyIsReported(t *testing.T) {
	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		s3src.JSON("b", "k", s3src.WithClient(&truncatingAPI{})),
	))
	if err == nil {
		t.Fatal("want an error for a body that cannot be read")
	}
	if !strings.Contains(err.Error(), "b/k") {
		t.Errorf("error %q should name the object", err)
	}
}

// truncatingAPI returns a body that fails part-way, the way a dropped
// connection does.
type truncatingAPI struct{}

func (truncatingAPI) GetObject(_ context.Context, _ *awss3.GetObjectInput,
	_ ...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
	return &awss3.GetObjectOutput{Body: io.NopCloser(brokenReader{})}, nil
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// TestSDKConfigurationFailureIsNamed covers the one arm
// TestDefaultClientUsesTheSDKChain cannot reach.
//
// That test cancels the context, and LoadDefaultConfig survives a cancelled
// context — it does no I/O unless it has to — so the failure comes from the
// API call and the config-loading arm never runs. Making LoadDefaultConfig
// ITSELF fail needs a broken shared config.
//
// What it proves: a credential chain that cannot resolve is reported as a
// failure naming the stage, not mistaken for a store with nothing in it. The
// environment points at a malformed file inside t.TempDir(), so nothing reads
// the developer's real ~/.aws and nothing reaches the network.
func TestSDKConfigurationFailureIsNamed(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "config")
	if err := os.WriteFile(broken, []byte("[[[ not a valid shared config\n"), 0o600); err != nil {
		t.Fatalf("writing the broken config: %v", err)
	}

	t.Setenv("AWS_CONFIG_FILE", broken)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", broken)
	t.Setenv("AWS_PROFILE", "does-not-exist")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true") // never reach for IMDS

	// A StructuredSource merges a document, so it is driven with Apply
	// rather than Lookup.
	var into struct {
		Host string `json:"host"`
	}
	err := s3src.JSON("acme-config", "app.json").Apply(&into)
	if err == nil {
		t.Fatal("an unresolvable credential chain was treated as an empty result")
	}
	if !strings.Contains(err.Error(), "loading AWS configuration") {
		t.Errorf("the error does not name the stage that failed: %v", err)
	}
}
