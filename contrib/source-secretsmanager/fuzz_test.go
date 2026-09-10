// Fuzzing the SECRET payload. A secret is opaque by definition — the value
// may be JSON, may be a bare token, may be truncated by a partial rotation.
package secretsmanager_test

import (
	"context"
	"testing"

	sm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/ubgo/cfgkit"
	secrets "github.com/ubgo/cfgkit/contrib/source-secretsmanager"
)

type fuzzSMAPI struct{ value string }

func (f fuzzSMAPI) GetSecretValue(context.Context, *sm.GetSecretValueInput,
	...func(*sm.Options)) (*sm.GetSecretValueOutput, error) {
	v := f.value
	return &sm.GetSecretValueOutput{SecretString: &v}, nil
}

func FuzzSecretPayloadNeverPanics(f *testing.F) {
	for _, seed := range []string{
		"", "{}", "null", "[]", "{\"HOST\":\"h\"}", "{\"HOST\":1}",
		"{\"HOST\":{\"nested\":true}}", "plain-token", "{", "\x00\xff",
		"{\"HOST\":99999999999999999999}",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		type cfg struct {
			Host string `env:"HOST"`
			Port int    `env:"PORT"`
		}
		// Both read modes see the same payload: JSON expects an object, Whole
		// expects anything at all. Neither may panic.
		_, _, _ = cfgkit.Load[cfg](cfgkit.WithSources(
			secrets.JSON("s", secrets.WithClient(fuzzSMAPI{value: value})),
		))
		_, _, _ = cfgkit.Load[cfg](cfgkit.WithSources(
			secrets.Whole("s", "HOST", secrets.WithClient(fuzzSMAPI{value: value})),
		))
	})
}
