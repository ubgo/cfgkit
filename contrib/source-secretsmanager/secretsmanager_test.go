// The suite implements the API interface directly, so it needs no AWS, no
// credentials and no network.
//
// TWO STATEMENTS ARE DELIBERATELY LEFT UNCOVERED: LoadDefaultConfig”'s error
// return, which needs a broken AWS config file on the machine running the
// tests, and json.Marshal”'s error in stringify, which re-encodes a value
// json.Unmarshal produced moments earlier and so cannot fail. Both are correct
// error handling for calls that happen not to fail here.
package secretsmanager_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	sm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/ubgo/cfgkit"
	secrets "github.com/ubgo/cfgkit/contrib/source-secretsmanager"
)

type appCfg struct {
	Host     string `env:"HOST" default:"localhost"`
	Port     int    `env:"PORT" default:"8080"`
	Debug    bool   `env:"DEBUG"`
	Password string `env:"DATABASE_PASSWORD" secret:"true"`
}

// fakeAPI records the request and replies with a canned payload.
type fakeAPI struct {
	payload string
	binary  bool
	err     error
	calls   int
	inputs  []*sm.GetSecretValueInput
}

func (f *fakeAPI) GetSecretValue(_ context.Context, in *sm.GetSecretValueInput,
	_ ...func(*sm.Options)) (*sm.GetSecretValueOutput, error) {
	f.calls++
	f.inputs = append(f.inputs, in)
	if f.err != nil {
		return nil, f.err
	}
	if f.binary {
		return &sm.GetSecretValueOutput{SecretBinary: []byte{0x00, 0x01}}, nil
	}
	return &sm.GetSecretValueOutput{SecretString: aws.String(f.payload)}, nil
}

// TestJSONSecretBinds covers the shape the Secrets Manager console produces
// when you enter key/value pairs, which is the common one.
func TestJSONSecretBinds(t *testing.T) {
	api := &fakeAPI{payload: `{"HOST":"aws.internal","PORT":9000}`}

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		secrets.JSON("prod/app", secrets.WithClient(api)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "aws.internal" || got.Port != 9000 {
		t.Errorf("cfg = %+v", got)
	}
	for _, f := range res.Fields() {
		if f.Path == "Host" && f.Source != "secretsmanager:prod/app" {
			t.Errorf("Host source = %q, want the secret named", f.Source)
		}
	}
}

// TestJSONValueTypesAreRendered pins the mapping from JSON to the strings a
// Source deals in. The number row is the one that bites: encoding/json gives
// every number as a float64, and the obvious formatting turns 9000 into
// "9000.000000", which then fails to parse as an int.
func TestJSONValueTypesAreRendered(t *testing.T) {
	api := &fakeAPI{payload: `{"PORT":9000,"DEBUG":true,"HOST":null,"OBJ":{"a":1}}`}

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		secrets.JSON("prod/app", secrets.WithClient(api)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 9000 {
		t.Errorf("Port = %d — a JSON number must render without a decimal tail", got.Port)
	}
	if !got.Debug {
		t.Error("Debug = false, want a JSON bool to bind")
	}
	if got.Host != "" {
		t.Errorf("Host = %q, want empty for a JSON null", got.Host)
	}
	var unknown []string
	for _, u := range res.Unknown() {
		unknown = append(unknown, u.Key)
	}
	if !contains(unknown, "OBJ") {
		t.Errorf("Unknown() = %v, want the nested object re-encoded rather than dropped", unknown)
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

// TestWholeSecretBindsOneKey covers a plain string secret — a password, a
// token, a private key — where the payload IS the value.
func TestWholeSecretBindsOneKey(t *testing.T) {
	api := &fakeAPI{payload: "hunter2"}

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		secrets.Whole("prod/db/password", "DATABASE_PASSWORD", secrets.WithClient(api)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "hunter2" {
		t.Errorf("Password = %q", got.Password)
	}
	if got.Host != "localhost" {
		t.Errorf("Host = %q, want the default — a Whole secret claims one key only", got.Host)
	}
	for _, f := range res.Fields() {
		if strings.Contains(f.Value, "hunter2") {
			t.Errorf("the secret reached Explain: %q", f.Value)
		}
	}
}

// TestJSONOnAPlainStringSaysUseWhole pins the message for the mistake the two
// readings invite. Sniffing at the payload and guessing would be right nine
// times and silent the tenth, which is why the reading is an argument.
func TestJSONOnAPlainStringSaysUseWhole(t *testing.T) {
	api := &fakeAPI{payload: "just-a-password"}

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		secrets.JSON("prod/db/password", secrets.WithClient(api)),
	))
	if err == nil {
		t.Fatal("want an error: the payload is not a JSON object")
	}
	if !strings.Contains(err.Error(), "Whole") {
		t.Errorf("error %q should name the other reading", err)
	}
	if strings.Contains(err.Error(), "just-a-password") {
		t.Errorf("error %q must not echo the payload", err)
	}
}

// TestBinarySecretIsRefusedByName pins that a binary secret fails rather than
// binding as mojibake. It is a certificate or a keystore, not configuration.
func TestBinarySecretIsRefusedByName(t *testing.T) {
	api := &fakeAPI{binary: true}

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		secrets.JSON("prod/cert", secrets.WithClient(api)),
	))
	if err == nil {
		t.Fatal("want an error for a binary secret")
	}
	if !strings.Contains(err.Error(), "binary") {
		t.Errorf("error %q should say the payload is binary", err)
	}
}

// TestVersionPinningReachesTheRequest pins both ways of selecting a version.
// AWSPREVIOUS is what lets a service that missed a rotation window still
// connect while it is fixed, so it has to work.
func TestVersionPinningReachesTheRequest(t *testing.T) {
	t.Run("by id", func(t *testing.T) {
		api := &fakeAPI{payload: `{"HOST":"x"}`}
		if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
			secrets.JSON("s", secrets.WithClient(api), secrets.WithVersionID("v-123")),
		)); err != nil {
			t.Fatal(err)
		}
		if aws.ToString(api.inputs[0].VersionId) != "v-123" {
			t.Errorf("VersionId = %q", aws.ToString(api.inputs[0].VersionId))
		}
	})

	t.Run("by stage", func(t *testing.T) {
		api := &fakeAPI{payload: `{"HOST":"x"}`}
		if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
			secrets.JSON("s", secrets.WithClient(api), secrets.WithVersionStage("AWSPREVIOUS")),
		)); err != nil {
			t.Fatal(err)
		}
		if aws.ToString(api.inputs[0].VersionStage) != "AWSPREVIOUS" {
			t.Errorf("VersionStage = %q", aws.ToString(api.inputs[0].VersionStage))
		}
	})

	t.Run("neither by default", func(t *testing.T) {
		api := &fakeAPI{payload: `{"HOST":"x"}`}
		if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
			secrets.JSON("s", secrets.WithClient(api)),
		)); err != nil {
			t.Fatal(err)
		}
		if api.inputs[0].VersionId != nil || api.inputs[0].VersionStage != nil {
			t.Error("neither version field should be set by default — AWS defaults to AWSCURRENT")
		}
	})
}

// TestMissingSecretIsAnErrorByDefault pins the shared rule, and that the
// not-found case is matched on the SDK's error TYPE rather than message text.
func TestMissingSecretIsAnErrorByDefault(t *testing.T) {
	api := &fakeAPI{err: &smtypes.ResourceNotFoundException{
		Message: aws.String("Secrets Manager can't find the specified secret."),
	}}

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		secrets.JSON("absent", secrets.WithClient(api)),
	))
	if err == nil {
		t.Fatal("want an error: the named secret does not exist")
	}
	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("want a *SourceError, got %T", err)
	}
}

func TestOptionalMissingSecretIsSilent(t *testing.T) {
	api := &fakeAPI{err: &smtypes.ResourceNotFoundException{Message: aws.String("nope")}}

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		secrets.JSON("absent", secrets.WithClient(api), secrets.Optional()),
	))
	if err != nil {
		t.Fatalf("an optional missing secret must not fail: %v", err)
	}
	if got.Host != "localhost" || got.Port != 8080 {
		t.Errorf("cfg = %+v, want the compiled-in defaults", got)
	}
}

// TestOptionalDoesNotSwallowOtherFailures is the important half of Optional.
// It means "absent is fine", not "errors are fine" — an access-denied must
// still fail the load, or a misconfigured role looks like an empty secret.
func TestOptionalDoesNotSwallowOtherFailures(t *testing.T) {
	api := &fakeAPI{err: errors.New("AccessDeniedException: not authorized")}

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		secrets.JSON("s", secrets.WithClient(api), secrets.Optional()),
	))
	if err == nil {
		t.Fatal("Optional() must not swallow an access-denied error")
	}
	if !strings.Contains(err.Error(), "AccessDeniedException") {
		t.Errorf("error %q should keep the SDK's message", err)
	}
}

func TestAPIFailureIsAnErrorNotAMiss(t *testing.T) {
	api := &fakeAPI{err: errors.New("AccessDeniedException: not authorized")}

	err := cfgkit.Check[appCfg](cfgkit.WithSources(secrets.JSON("s", secrets.WithClient(api))))
	if err == nil {
		t.Fatal("want an error, not a silent fallback to defaults")
	}
	if !strings.Contains(err.Error(), "s") {
		t.Errorf("error %q should name the secret", err)
	}
}

func TestKeysAreListedForUnknownDetection(t *testing.T) {
	api := &fakeAPI{payload: `{"HOST":"svc","HSOT":"typo"}`}

	_, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		secrets.JSON("s", secrets.WithClient(api)),
	))
	if err != nil {
		t.Fatal(err)
	}
	u := res.Unknown()
	if len(u) != 1 || u[0].Key != "HSOT" {
		t.Fatalf("Unknown() = %v, want just the typo", u)
	}
}

func TestReadHappensOncePerSource(t *testing.T) {
	api := &fakeAPI{payload: `{"HOST":"svc","PORT":9000}`}

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		secrets.JSON("s", secrets.WithClient(api)),
	)); err != nil {
		t.Fatal(err)
	}
	if api.calls != 1 {
		t.Errorf("made %d calls, want exactly 1 — every read is billed and logged", api.calls)
	}
}

// TestDefaultClientUsesTheSDKChain covers the path a real deployment takes: no
// WithClient, so the SDK's credential chain builds the client. The context is
// CANCELLED first, so nothing leaves the machine and the result does not depend
// on whether AWS credentials exist here.
func TestDefaultClientUsesTheSDKChain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		secrets.JSON("prod/app", secrets.WithContext(ctx)),
	))
	if err == nil {
		t.Fatal("want an error: the context was cancelled before the request")
	}
	if !strings.Contains(err.Error(), "prod/app") &&
		!strings.Contains(err.Error(), "AWS configuration") {
		t.Errorf("error %q should name the secret or the configuration step", err)
	}
}

func TestPrecedenceAgainstOtherSources(t *testing.T) {
	api := &fakeAPI{payload: `{"HOST":"from-aws"}`}

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"HOST": "from-map", "PORT": "1234"}),
		secrets.JSON("s", secrets.WithClient(api)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "from-aws" {
		t.Errorf("Host = %q, want the later source to win", got.Host)
	}
	if got.Port != 1234 {
		t.Errorf("Port = %d, want the earlier source where the later is silent", got.Port)
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

	_, _, err := secrets.JSON("acme/config").Lookup("HOST")
	if err == nil {
		t.Fatal("an unresolvable credential chain was treated as an empty result")
	}
	if !strings.Contains(err.Error(), "loading AWS configuration") {
		t.Errorf("the error does not name the stage that failed: %v", err)
	}
}
