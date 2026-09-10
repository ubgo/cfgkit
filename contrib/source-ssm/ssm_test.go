// The suite implements the API interface directly, so it needs no AWS, no
// credentials and no network. Taking the SDK as a dependency does not require
// taking its test harness too — which is the reason Path accepts an interface
// rather than the concrete client.
package ssm_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/ubgo/cfgkit"
	ssm "github.com/ubgo/cfgkit/contrib/source-ssm"
)

type appCfg struct {
	Host     string   `env:"HOST" default:"localhost"`
	Port     int      `env:"PORT" default:"8080"`
	Hosts    []string `env:"HOSTS"`
	Password string   `env:"PASSWORD" secret:"true"`
}

// fakeAPI records what was asked for and replies with canned pages.
type fakeAPI struct {
	pages  [][]ssmtypes.Parameter
	err    error
	calls  int
	inputs []*awsssm.GetParametersByPathInput
}

func (f *fakeAPI) GetParametersByPath(_ context.Context, in *awsssm.GetParametersByPathInput,
	_ ...func(*awsssm.Options)) (*awsssm.GetParametersByPathOutput, error) {
	f.calls++
	f.inputs = append(f.inputs, in)
	if f.err != nil {
		return nil, f.err
	}
	idx := 0
	if in.NextToken != nil {
		// The token is the index of the page to serve, which keeps the fake
		// honest about paging without inventing a token format.
		idx = len(*in.NextToken)
	}
	if idx >= len(f.pages) {
		return &awsssm.GetParametersByPathOutput{}, nil
	}
	out := &awsssm.GetParametersByPathOutput{Parameters: f.pages[idx]}
	if idx+1 < len(f.pages) {
		out.NextToken = aws.String(strings.Repeat("x", idx+1))
	}
	return out, nil
}

// param builds one Parameter Store entry.
func param(name, value string) ssmtypes.Parameter {
	return ssmtypes.Parameter{Name: aws.String(name), Value: aws.String(value),
		Type: ssmtypes.ParameterTypeString}
}

func TestPathBinds(t *testing.T) {
	api := &fakeAPI{pages: [][]ssmtypes.Parameter{{
		param("/app/config/HOST", "ssm.internal"),
		param("/app/config/PORT", "9000"),
	}}}

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		ssm.Path("/app/config", ssm.WithClient(api)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "ssm.internal" || got.Port != 9000 {
		t.Errorf("cfg = %+v", got)
	}
	for _, f := range res.Fields() {
		if f.Path == "Host" && f.Source != "ssm:/app/config" {
			t.Errorf("Host source = %q, want the path named", f.Source)
		}
	}
}

// TestPagingIsHandled is the most important test here. GetParametersByPath
// returns at most TEN parameters per call, so a configuration of eleven
// silently loses one without a paging loop — the single easiest mistake to make
// against this API, and one that only shows up as a config grows.
func TestPagingIsHandled(t *testing.T) {
	api := &fakeAPI{pages: [][]ssmtypes.Parameter{
		{param("/app/config/HOST", "from-page-1")},
		{param("/app/config/PORT", "9000")},
		{param("/app/config/PASSWORD", "hunter2")},
	}}

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		ssm.Path("/app/config", ssm.WithClient(api)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if api.calls != 3 {
		t.Errorf("made %d calls, want 3 — every page must be read", api.calls)
	}
	if got.Host != "from-page-1" || got.Port != 9000 || got.Password != "hunter2" {
		t.Errorf("cfg = %+v — a value from a later page was lost", got)
	}
}

// TestNamesArriveRelativeToThePath is what lets one struct bind from Parameter
// Store and from a .env file with no second set of tags.
func TestNamesArriveRelativeToThePath(t *testing.T) {
	type nested struct {
		DBHost string `env:"db/HOST" default:"localhost"`
	}
	api := &fakeAPI{pages: [][]ssmtypes.Parameter{{param("/app/db/HOST", "db.internal")}}}

	got, _, err := cfgkit.Load[nested](cfgkit.WithSources(ssm.Path("/app", ssm.WithClient(api))))
	if err != nil {
		t.Fatal(err)
	}
	if got.DBHost != "db.internal" {
		t.Errorf("DBHost = %q — a nested path keeps its separator", got.DBHost)
	}
}

func TestPathIsNormalised(t *testing.T) {
	for _, p := range []string{"/app/config", "app/config", "/app/config/", "app/config/"} {
		t.Run(p, func(t *testing.T) {
			api := &fakeAPI{pages: [][]ssmtypes.Parameter{{param("/app/config/HOST", "svc")}}}
			got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(ssm.Path(p, ssm.WithClient(api))))
			if err != nil {
				t.Fatal(err)
			}
			if got.Host != "svc" {
				t.Errorf("Host = %q for path %q", got.Host, p)
			}
			if aws.ToString(api.inputs[0].Path) != "/app/config" {
				t.Errorf("requested path = %q, want it normalised", aws.ToString(api.inputs[0].Path))
			}
		})
	}
}

// TestDecryptionIsOnByDefault pins the choice that matters most for a
// SecureString. A parameter that arrives as ciphertext is not a configuration
// value, and silently binding the encrypted blob is the quiet failure this
// package exists to prevent.
func TestDecryptionIsOnByDefault(t *testing.T) {
	api := &fakeAPI{pages: [][]ssmtypes.Parameter{{param("/app/config/PASSWORD", "hunter2")}}}

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		ssm.Path("/app/config", ssm.WithClient(api)),
	)); err != nil {
		t.Fatal(err)
	}
	if !aws.ToBool(api.inputs[0].WithDecryption) {
		t.Error("WithDecryption = false, want decryption on by default")
	}

	api2 := &fakeAPI{pages: [][]ssmtypes.Parameter{{param("/app/config/PASSWORD", "x")}}}
	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		ssm.Path("/app/config", ssm.WithClient(api2), ssm.WithoutDecryption()),
	)); err != nil {
		t.Fatal(err)
	}
	if aws.ToBool(api2.inputs[0].WithDecryption) {
		t.Error("WithoutDecryption did not reach the request")
	}
}

func TestRecursionIsOnByDefault(t *testing.T) {
	api := &fakeAPI{pages: [][]ssmtypes.Parameter{{param("/app/config/HOST", "svc")}}}
	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		ssm.Path("/app/config", ssm.WithClient(api)),
	)); err != nil {
		t.Fatal(err)
	}
	if !aws.ToBool(api.inputs[0].Recursive) {
		t.Error("Recursive = false, want recursion on by default")
	}

	api2 := &fakeAPI{pages: [][]ssmtypes.Parameter{{param("/app/config/HOST", "svc")}}}
	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		ssm.Path("/app/config", ssm.WithClient(api2), ssm.WithoutRecursion()),
	)); err != nil {
		t.Fatal(err)
	}
	if aws.ToBool(api2.inputs[0].Recursive) {
		t.Error("WithoutRecursion did not reach the request")
	}
}

// TestStringListNeedsNoSpecialHandling pins that a StringList binds to a Go
// slice as it is. It arrives comma-separated, which is exactly what cfgkit's
// slice decoder expects — so a branch that split and rejoined it would only add
// a way to be wrong.
func TestStringListNeedsNoSpecialHandling(t *testing.T) {
	api := &fakeAPI{pages: [][]ssmtypes.Parameter{{{
		Name:  aws.String("/app/config/HOSTS"),
		Value: aws.String("a.test,b.test"),
		Type:  ssmtypes.ParameterTypeStringList,
	}}}}

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		ssm.Path("/app/config", ssm.WithClient(api)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Hosts) != 2 || got.Hosts[0] != "a.test" {
		t.Errorf("Hosts = %v, want the StringList split by the normal slice decoder", got.Hosts)
	}
}

func TestSecretIsMasked(t *testing.T) {
	api := &fakeAPI{pages: [][]ssmtypes.Parameter{{param("/app/config/PASSWORD", "hunter2")}}}

	got, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		ssm.Path("/app/config", ssm.WithClient(api)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "hunter2" {
		t.Errorf("Password = %q", got.Password)
	}
	for _, f := range res.Fields() {
		if strings.Contains(f.Value, "hunter2") {
			t.Errorf("the secret reached Explain: %q", f.Value)
		}
	}
}

// TestEmptyPathIsAnErrorByDefault pins the rule shared with every other remote
// source: you named the path, so finding nothing under it is a deployment
// mistake rather than a normal outcome.
func TestEmptyPathIsAnErrorByDefault(t *testing.T) {
	api := &fakeAPI{}

	err := cfgkit.Check[appCfg](cfgkit.WithSources(ssm.Path("/app/config", ssm.WithClient(api))))
	if err == nil {
		t.Fatal("want an error: nothing lives under this path")
	}
	if !strings.Contains(err.Error(), "Optional()") {
		t.Errorf("error %q should name the way to opt out", err)
	}
	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("want a *SourceError, got %T", err)
	}
}

func TestOptionalEmptyPathIsSilent(t *testing.T) {
	api := &fakeAPI{}

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		ssm.Path("/app/config", ssm.WithClient(api), ssm.Optional()),
	))
	if err != nil {
		t.Fatalf("an optional empty path must not fail: %v", err)
	}
	if got.Host != "localhost" || got.Port != 8080 {
		t.Errorf("cfg = %+v, want the compiled-in defaults", got)
	}
}

// TestAPIFailureIsAnErrorNotAMiss is the rule that separates a broken store
// from one that simply has no opinion. The difference is a deploy proceeding
// with an empty password.
func TestAPIFailureIsAnErrorNotAMiss(t *testing.T) {
	api := &fakeAPI{err: errors.New("AccessDeniedException: not authorized")}

	err := cfgkit.Check[appCfg](cfgkit.WithSources(ssm.Path("/app/config", ssm.WithClient(api))))
	if err == nil {
		t.Fatal("want an error, not a silent fallback to defaults")
	}
	// The SDK's own message is preserved: it names the AWS error type, which is
	// what a reader searches for.
	if !strings.Contains(err.Error(), "AccessDeniedException") {
		t.Errorf("error %q should keep the SDK's message", err)
	}
	if !strings.Contains(err.Error(), "/app/config") {
		t.Errorf("error %q should name the path", err)
	}
}

func TestPathEqualToAParameterIsSkipped(t *testing.T) {
	api := &fakeAPI{pages: [][]ssmtypes.Parameter{{
		param("/app/config", "the-path-itself"),
		param("/app/config/HOST", "svc"),
	}}}

	_, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		ssm.Path("/app/config", ssm.WithClient(api)),
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range res.Unknown() {
		if u.Key == "" {
			t.Error("the path itself was bound as an empty key")
		}
	}
}

func TestKeysAreListedForUnknownDetection(t *testing.T) {
	api := &fakeAPI{pages: [][]ssmtypes.Parameter{{
		param("/app/config/HOST", "svc"),
		param("/app/config/HSOT", "typo"),
	}}}

	_, res, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		ssm.Path("/app/config", ssm.WithClient(api)),
	))
	if err != nil {
		t.Fatal(err)
	}
	u := res.Unknown()
	if len(u) != 1 || u[0].Key != "HSOT" {
		t.Fatalf("Unknown() = %v, want just the typo", u)
	}
}

func TestContextIsPassedThrough(t *testing.T) {
	api := &fakeAPI{pages: [][]ssmtypes.Parameter{{param("/app/config/HOST", "svc")}}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		ssm.Path("/app/config", ssm.WithClient(api), ssm.WithContext(ctx)),
	)); err != nil {
		t.Fatal(err)
	}
	if api.calls != 1 {
		t.Errorf("calls = %d", api.calls)
	}
}

func TestReadHappensOncePerSource(t *testing.T) {
	api := &fakeAPI{pages: [][]ssmtypes.Parameter{{
		param("/app/config/HOST", "svc"), param("/app/config/PORT", "9000"),
	}}}

	if _, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		ssm.Path("/app/config", ssm.WithClient(api)),
	)); err != nil {
		t.Fatal(err)
	}
	if api.calls != 1 {
		t.Errorf("made %d calls for a single page, want 1 — Parameter Store is rate-limited", api.calls)
	}
}

func TestPrecedenceAgainstOtherSources(t *testing.T) {
	api := &fakeAPI{pages: [][]ssmtypes.Parameter{{param("/app/config/HOST", "from-ssm")}}}

	got, _, err := cfgkit.Load[appCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"HOST": "from-map", "PORT": "1234"}),
		ssm.Path("/app/config", ssm.WithClient(api)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "from-ssm" {
		t.Errorf("Host = %q, want the later source to win", got.Host)
	}
	if got.Port != 1234 {
		t.Errorf("Port = %d, want the earlier source where the later is silent", got.Port)
	}
}

// TestDefaultClientUsesTheSDKChain covers the path a real deployment takes:
// no WithClient, so the SDK's own credential chain builds the client. That
// chain is the entire reason this module takes the dependency, and a suite that
// always injects a fake would never execute it.
//
// The context is CANCELLED before the call, so the client is built and the
// request fails immediately — nothing leaves the machine, and the test does not
// depend on whether AWS credentials happen to exist here.
func TestDefaultClientUsesTheSDKChain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := cfgkit.Check[appCfg](cfgkit.WithSources(
		ssm.Path("/app/config", ssm.WithContext(ctx)),
	))
	if err == nil {
		t.Fatal("want an error: the context was cancelled before the request")
	}
	// Either step may report it — building the config or making the call — and
	// both are correct. What matters is that neither panics and the path is
	// named, so an operator can tell which source failed.
	if !strings.Contains(err.Error(), "/app/config") &&
		!strings.Contains(err.Error(), "AWS configuration") {
		t.Errorf("error %q should name the path or the configuration step", err)
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

	_, _, err := ssm.Path("/acme/checkout/").Lookup("HOST")
	if err == nil {
		t.Fatal("an unresolvable credential chain was treated as an empty result")
	}
	if !strings.Contains(err.Error(), "loading AWS configuration") {
		t.Errorf("the error does not name the stage that failed: %v", err)
	}
}
