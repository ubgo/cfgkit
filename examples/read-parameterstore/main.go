// Command read-parameterstore reads a parameter path from AWS Systems Manager.
//
// It RUNS with no AWS account: the example supplies a fake client through the
// module's API seam.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/ubgo/cfgkit"
	ssmsrc "github.com/ubgo/cfgkit/contrib/source-ssm"
)

// Config says nothing about SSM. Names arrive RELATIVE to the path, so the
// same struct binds from Parameter Store and from a .env file.
type Config struct {
	Host     string   `env:"HOST" default:"localhost"`
	Port     int      `env:"PORT" default:"8080"`
	Password string   `env:"PASSWORD" secret:"true"`
	Origins  []string `env:"ORIGINS" delim:","`
}

const path = "/acme/checkout/"

// pageSize is AWS's maximum for GetParametersByPath.
//
// It is the reason paging is NOT optional in the adapter: a configuration of
// eleven parameters silently loses one if the second page is never fetched.
// The fake pages at this boundary so the example exercises the real path.
const pageSize = 10

// fakeSSM implements ssmsrc.API and pages exactly as AWS does.
type fakeSSM struct{ params []ssmtypes.Parameter }

// GetParametersByPath pages exactly as AWS does, at the 10-parameter limit,
// so the example exercises the real paging path rather than describing it.
func (f fakeSSM) GetParametersByPath(_ context.Context, in *ssm.GetParametersByPathInput,
	_ ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error) {
	start := 0
	if in.NextToken != nil {
		// Checked, not discarded: an unparsed token would silently restart
		// paging at zero, and the fake would loop forever handing back the
		// same first page — the exact bug this example exists to rule out.
		if _, err := fmt.Sscanf(*in.NextToken, "%d", &start); err != nil {
			return nil, fmt.Errorf("malformed page token %q: %w", *in.NextToken, err)
		}
	}
	end := min(start+pageSize, len(f.params))

	out := &ssm.GetParametersByPathOutput{Parameters: f.params[start:end]}
	if end < len(f.params) {
		token := fmt.Sprintf("%d", end)
		out.NextToken = &token
	}
	return out, nil
}

// param builds one parameter. StringList is AWS's own list type and needs no
// special handling here: it arrives comma-joined, which is what delim already
// expects.
func param(name, value string, typ ssmtypes.ParameterType) ssmtypes.Parameter {
	full := path + name
	return ssmtypes.Parameter{Name: &full, Value: &value, Type: typ}
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	params := []ssmtypes.Parameter{
		param("HOST", "ssm.internal", ssmtypes.ParameterTypeString),
		param("PORT", "8443", ssmtypes.ParameterTypeString),
		param("PASSWORD", "s3cret-from-ssm", ssmtypes.ParameterTypeSecureString),
		param("ORIGINS", "https://acme.test,https://www.acme.test",
			ssmtypes.ParameterTypeStringList),
	}
	// Enough filler to force a second page, so the example proves paging
	// rather than describing it.
	for i := range pageSize {
		params = append(params, param(fmt.Sprintf("FILLER_%d", i), "x",
			ssmtypes.ParameterTypeString))
	}

	// Real usage: ssmsrc.Path("/acme/checkout/"). Decryption is on by default,
	// because a SecureString you cannot read is not configuration.
	cfg, res, err := cfgkit.Load[Config](cfgkit.WithSources(
		ssmsrc.Path(path, ssmsrc.WithClient(fakeSSM{params: params})),
		cfgkit.FromEnviron(),
	))
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(w, "%s:%d origins=%v\n", cfg.Host, cfg.Port, cfg.Origins)
	_, _ = fmt.Fprintln(w)

	// Only the bound fields are shown; the filler parameters are reported as
	// unknown keys instead, which is how a typo would surface.
	_, _ = fmt.Fprintf(w, "parameters that matched no field: %d\n", len(res.Unknown()))
	_, _ = fmt.Fprintln(w)
	return res.Explain(w)
}
