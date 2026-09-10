// Fuzzing parameter NAMES and VALUES together. The name decides which field a
// value lands on after the path prefix is trimmed, so a hostile or malformed
// name is as interesting as a hostile value.
package ssm_test

import (
	"context"
	"testing"

	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/ubgo/cfgkit"
	ssm "github.com/ubgo/cfgkit/contrib/source-ssm"
)

type fuzzSSMAPI struct{ name, value string }

func (f fuzzSSMAPI) GetParametersByPath(context.Context, *awsssm.GetParametersByPathInput,
	...func(*awsssm.Options)) (*awsssm.GetParametersByPathOutput, error) {
	n, v := f.name, f.value
	return &awsssm.GetParametersByPathOutput{
		Parameters: []ssmtypes.Parameter{{Name: &n, Value: &v}},
	}, nil
}

func FuzzParameterNamesAndValuesNeverPanic(f *testing.F) {
	for _, seed := range [][2]string{
		{"/acme/HOST", "h"}, {"", ""}, {"/", ""}, {"//", "x"},
		{"/acme/PORT", "not-an-int"}, {"HOST", "h"}, {"/acme/", "v"},
		{"\x00", "\xff"}, {"/acme/PORT", "99999999999999999999"},
	} {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, name, value string) {
		type cfg struct {
			Host string `env:"HOST"`
			Port int    `env:"PORT"`
		}
		_, _, _ = cfgkit.Load[cfg](cfgkit.WithSources(
			ssm.Path("/acme/", ssm.WithClient(fuzzSSMAPI{name: name, value: value})),
		))
	})
}
