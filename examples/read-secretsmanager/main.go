// Command read-secretsmanager reads from AWS Secrets Manager, both ways.
//
// The module offers two, and they are SEPARATE CALLS rather than a content
// sniff: guessing whether a secret holds JSON or a bare string would be right
// nine times and silently wrong the tenth.
//
//   - JSON(name)        — the secret is a JSON object; each field is a key.
//   - Whole(name, key)  — the secret is one opaque value bound to one key.
//
// It RUNS with no AWS account, through the module's API seam.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	sm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/ubgo/cfgkit"
	smsrc "github.com/ubgo/cfgkit/contrib/source-secretsmanager"
)

// Config draws from both secrets at once.
type Config struct {
	Host     string `env:"HOST" default:"localhost"`
	Port     int    `env:"PORT" default:"8080"`
	Password string `env:"PASSWORD" secret:"true"`

	// Lives in its own secret, as a bare string.
	APIKey string `env:"API_KEY" secret:"true"`
}

const (
	jsonSecret  = "acme/checkout/config"
	wholeSecret = "acme/checkout/api-key"
)

// fakeSM implements smsrc.API, serving both secrets.
type fakeSM struct{ values map[string]string }

// GetSecretValue serves both fixtures and reports an unknown name as an
// error, mirroring what Secrets Manager does for a secret that is not there.
func (f fakeSM) GetSecretValue(_ context.Context, in *sm.GetSecretValueInput,
	_ ...func(*sm.Options)) (*sm.GetSecretValueOutput, error) {
	v, ok := f.values[*in.SecretId]
	if !ok {
		return nil, fmt.Errorf("secret %q not found", *in.SecretId)
	}
	return &sm.GetSecretValueOutput{SecretString: &v}, nil
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	client := fakeSM{values: map[string]string{
		jsonSecret: `{"HOST":"sm.internal","PORT":"8500","PASSWORD":"s3cret-from-sm"}`,
		// Not JSON, and not meant to be. Asking JSON() to read this reports
		// that Whole is the right call rather than failing obscurely.
		wholeSecret: "ak_live_51H8xEXAMPLE",
	}}

	// Real usage drops the WithClient: smsrc.JSON("acme/checkout/config").
	cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
		smsrc.JSON(jsonSecret, smsrc.WithClient(client)),
		smsrc.Whole(wholeSecret, "API_KEY", smsrc.WithClient(client)),
		cfgkit.FromEnviron(),
	))
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(w, "%s:%d\n", cfg.Host, cfg.Port)
	_, _ = fmt.Fprintln(w)
	if err := res.Explain(w); err != nil {
		return err
	}

	// Reading the bare string with JSON() is the mistake worth showing,
	// because the error names the fix instead of reporting a parse failure.
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "JSON() against a bare string:")
	_, _, err = cfgkit.Load[Config](cfgkit.WithSources(
		smsrc.JSON(wholeSecret, smsrc.WithClient(client)),
	))
	// A source-level failure is reported once PER FIELD that asked the source
	// for a value, so the same sentence repeats. Only the first line is shown
	// here; the point is that it names the fix rather than reporting a raw
	// JSON parse error the caller would have to interpret.
	_, _ = fmt.Fprintf(w, "  %s\n", firstProblem(err))
	return nil
}

// firstProblem returns the first PROBLEM line, skipping cfgkit's count header.
//
// The header is "cfgkit: N problem(s):" and carries no diagnosis; the line
// after it is the one that names the fix.
func firstProblem(err error) string {
	if err == nil {
		return "<nil>"
	}
	lines := strings.Split(err.Error(), "\n")
	if len(lines) > 1 {
		return lines[1]
	}
	return lines[0]
}
