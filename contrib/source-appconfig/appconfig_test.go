// The suite implements the API interface directly, so it needs no AWS, no
// credentials and no network.
//
// ONE STATEMENT IS DELIBERATELY LEFT UNCOVERED: LoadDefaultConfig's error
// return, which needs a broken AWS config file on the machine running the
// tests. It is correct error handling for a call that happens not to fail here.
package appconfig_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ac "github.com/aws/aws-sdk-go-v2/service/appconfigdata"
	"github.com/ubgo/cfgkit"
	appconfig "github.com/ubgo/cfgkit/contrib/source-appconfig"
)

type appCfg struct {
	Host string `env:"HOST" json:"host" default:"localhost"`
	Port int    `env:"PORT" json:"port" default:"8080"`
}

// fakeAPI implements both calls of the protocol and records them.
type fakeAPI struct {
	token      string
	content    []byte
	startErr   error
	getErr     error
	startCalls int
	getCalls   int
	startIn    *ac.StartConfigurationSessionInput
	getIn      *ac.GetLatestConfigurationInput
}

func (f *fakeAPI) StartConfigurationSession(_ context.Context, in *ac.StartConfigurationSessionInput,
	_ ...func(*ac.Options)) (*ac.StartConfigurationSessionOutput, error) {
	f.startCalls++
	f.startIn = in
	if f.startErr != nil {
		return nil, f.startErr
	}
	return &ac.StartConfigurationSessionOutput{InitialConfigurationToken: aws.String(f.token)}, nil
}

func (f *fakeAPI) GetLatestConfiguration(_ context.Context, in *ac.GetLatestConfigurationInput,
	_ ...func(*ac.Options)) (*ac.GetLatestConfigurationOutput, error) {
	f.getCalls++
	f.getIn = in
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &ac.GetLatestConfigurationOutput{
		Configuration:              f.content,
		NextPollConfigurationToken: aws.String("next-token"),
	}, nil
}

func TestConfigurationBinds(t *testing.T) {
	api := &fakeAPI{token: "tok-1", content: []byte(`{"host":"appconfig.internal","port":9000}`)}

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		appconfig.JSON("my-app", "prod", "app-config", appconfig.WithClient(api)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "appconfig.internal" || got.Port != 9000 {
		t.Errorf("cfg = %+v", got)
	}
	for _, f := range res.Fields() {
		if f.Path == "Host" && f.Source != "appconfig:my-app/prod/app-config" {
			t.Errorf("Host source = %q, want all three identifiers", f.Source)
		}
	}
}

// TestBothCallsAreMadeInOrder pins the two-call protocol, which is the thing to
// get right about AppConfig. The session's token must reach the second call —
// without it the read is rejected, and the failure looks like a permissions
// problem rather than a protocol one.
func TestBothCallsAreMadeInOrder(t *testing.T) {
	api := &fakeAPI{token: "tok-1", content: []byte(`{"host":"x"}`)}

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		appconfig.JSON("my-app", "prod", "app-config", appconfig.WithClient(api)),
	)); err != nil {
		t.Fatal(err)
	}
	if api.startCalls != 1 || api.getCalls != 1 {
		t.Fatalf("start=%d get=%d, want exactly one of each", api.startCalls, api.getCalls)
	}
	if aws.ToString(api.startIn.ApplicationIdentifier) != "my-app" ||
		aws.ToString(api.startIn.EnvironmentIdentifier) != "prod" ||
		aws.ToString(api.startIn.ConfigurationProfileIdentifier) != "app-config" {
		t.Errorf("session started for the wrong target: %+v", api.startIn)
	}
	if aws.ToString(api.getIn.ConfigurationToken) != "tok-1" {
		t.Errorf("token = %q, want the session's initial token",
			aws.ToString(api.getIn.ConfigurationToken))
	}
}

// TestEmptyFirstResponseIsAnError is the subtlety this protocol hides. On a
// LATER poll, empty content means "unchanged". On the FIRST call of a session
// it cannot — so empty here means the profile has no deployed content, and
// treating it as "unchanged" would bind an empty document and look like a
// working read.
func TestEmptyFirstResponseIsAnError(t *testing.T) {
	api := &fakeAPI{token: "tok-1", content: nil}

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		appconfig.JSON("my-app", "prod", "app-config", appconfig.WithClient(api)),
	))
	if err == nil {
		t.Fatal("want an error: the first response of a session carries the configuration")
	}
	if !strings.Contains(err.Error(), "no deployed content") {
		t.Errorf("error %q should say what empty means here", err)
	}
	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("want a *SourceError, got %T", err)
	}
}

// TestAnyDecoderComposes is the point of being format-agnostic: AppConfig
// stores freeform documents and reports a content type without parsing, so the
// reading is the caller's.
func TestAnyDecoderComposes(t *testing.T) {
	upper := func(doc []byte, dst any) error {
		cfg, ok := dst.(*appCfg)
		if !ok {
			return errors.New("unexpected destination")
		}
		cfg.Host = strings.ToUpper(strings.TrimSpace(string(doc)))
		return nil
	}

	api := &fakeAPI{token: "t", content: []byte("plain-text\n")}
	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		appconfig.Configuration("a", "e", "p", upper, appconfig.WithClient(api)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "PLAIN-TEXT" {
		t.Errorf("Host = %q, want the caller's own decoder to have run", got.Host)
	}
}

func TestAbsentFieldsAreLeftAlone(t *testing.T) {
	api := &fakeAPI{token: "t", content: []byte(`{"host":"only-host"}`)}

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		appconfig.JSON("a", "e", "p", appconfig.WithClient(api)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 8080 {
		t.Errorf("Port = %d, want the default — the document did not mention it", got.Port)
	}
}

func TestSessionFailureNamesTheStage(t *testing.T) {
	api := &fakeAPI{startErr: errors.New("BadRequestException: no such profile")}

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		appconfig.JSON("a", "e", "p", appconfig.WithClient(api)),
	))
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "starting AppConfig session") {
		t.Errorf("error %q should say which of the two calls failed", err)
	}
	if api.getCalls != 0 {
		t.Error("the second call must not run after the first failed")
	}
}

func TestReadFailureNamesTheStage(t *testing.T) {
	api := &fakeAPI{token: "t", getErr: errors.New("AccessDeniedException: not authorized")}

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		appconfig.JSON("a", "e", "p", appconfig.WithClient(api)),
	))
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "reading AppConfig") {
		t.Errorf("error %q should say which of the two calls failed", err)
	}
	if !strings.Contains(err.Error(), "AccessDeniedException") {
		t.Errorf("error %q should keep the SDK's message", err)
	}
}

func TestDecodeFailureNamesTheProfile(t *testing.T) {
	api := &fakeAPI{token: "t", content: []byte("{not json")}

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		appconfig.JSON("my-app", "prod", "app-config", appconfig.WithClient(api)),
	))
	if err == nil {
		t.Fatal("want a decode error")
	}
	if !strings.Contains(err.Error(), "my-app/prod/app-config") {
		t.Errorf("error %q should name the profile", err)
	}
}

func TestNoDecoderIsAnError(t *testing.T) {
	api := &fakeAPI{token: "t", content: []byte("{}")}

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		appconfig.Configuration("a", "e", "p", nil, appconfig.WithClient(api)),
	))
	if err == nil || !strings.Contains(err.Error(), "no decoder") {
		t.Errorf("want a missing-decoder error, got %v", err)
	}
}

// TestReadHappensOncePerSource pins that a session is started once. cfgkit
// reads at boot; polling for changes is Watcher's job, and each Reload starts a
// fresh session rather than reusing a token.
func TestReadHappensOncePerSource(t *testing.T) {
	api := &fakeAPI{token: "t", content: []byte(`{"host":"svc","port":9000}`)}

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		appconfig.JSON("a", "e", "p", appconfig.WithClient(api)),
	)); err != nil {
		t.Fatal(err)
	}
	if api.startCalls != 1 || api.getCalls != 1 {
		t.Errorf("start=%d get=%d, want one of each for a whole Load",
			api.startCalls, api.getCalls)
	}
}

// TestDefaultClientUsesTheSDKChain covers the path a real deployment takes. The
// context is CANCELLED first, so nothing leaves the machine.
func TestDefaultClientUsesTheSDKChain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		appconfig.JSON("a", "e", "p", appconfig.WithContext(ctx)),
	))
	if err == nil {
		t.Fatal("want an error: the context was cancelled before the request")
	}
}

func TestPrecedenceAgainstOtherSources(t *testing.T) {
	api := &fakeAPI{token: "t", content: []byte(`{"host":"from-appconfig"}`)}

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		appconfig.JSON("a", "e", "p", appconfig.WithClient(api)),
		cfgkit.FromMap(map[string]string{"PORT": "1234"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "from-appconfig" {
		t.Errorf("Host = %q", got.Host)
	}
	if got.Port != 1234 {
		t.Errorf("Port = %d, want the later flat source to win", got.Port)
	}
}

// TestPayloadBuiltWithEncodingJSONBinds checks the JSON helper agrees with the
// standard marshaller it wraps, not only with hand-written literals.
func TestPayloadBuiltWithEncodingJSONBinds(t *testing.T) {
	b, err := json.Marshal(map[string]any{"host": "marshalled", "port": 7000})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		appconfig.JSON("a", "e", "p", appconfig.WithClient(&fakeAPI{token: "t", content: b})),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "marshalled" || got.Port != 7000 {
		t.Errorf("cfg = %+v", got)
	}
}

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
	err := appconfig.JSON("checkout", "production", "config").Apply(&into)
	if err == nil {
		t.Fatal("an unresolvable credential chain was treated as an empty result")
	}
	if !strings.Contains(err.Error(), "loading AWS configuration") {
		t.Errorf("the error does not name the stage that failed: %v", err)
	}
}
