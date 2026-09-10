// Fuzzing the deployed CONFIGURATION document. AppConfig hands back whatever
// was deployed to the profile, and a deploy can put anything there.
package appconfig_test

import (
	"context"
	"testing"

	ac "github.com/aws/aws-sdk-go-v2/service/appconfigdata"
	"github.com/ubgo/cfgkit"
	appconfig "github.com/ubgo/cfgkit/contrib/source-appconfig"
)

type fuzzACAPI struct{ doc []byte }

func (f fuzzACAPI) StartConfigurationSession(context.Context,
	*ac.StartConfigurationSessionInput,
	...func(*ac.Options)) (*ac.StartConfigurationSessionOutput, error) {
	tok := "t"
	return &ac.StartConfigurationSessionOutput{InitialConfigurationToken: &tok}, nil
}

func (f fuzzACAPI) GetLatestConfiguration(context.Context,
	*ac.GetLatestConfigurationInput,
	...func(*ac.Options)) (*ac.GetLatestConfigurationOutput, error) {
	next := "n"
	return &ac.GetLatestConfigurationOutput{
		Configuration: f.doc, NextPollConfigurationToken: &next,
	}, nil
}

func FuzzDeployedDocumentNeverPanics(f *testing.F) {
	for _, seed := range []string{
		"", "{}", "null", "[]", "{\"host\":\"h\"}", "{\"port\":\"x\"}",
		"{", "\x00\xff", "{\"port\":99999999999999999999}",
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, doc []byte) {
		type cfg struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		}
		_, _, _ = cfgkit.Load[cfg](cfgkit.WithSources(
			appconfig.JSON("a", "e", "p", appconfig.WithClient(fuzzACAPI{doc: doc})),
		))
	})
}
