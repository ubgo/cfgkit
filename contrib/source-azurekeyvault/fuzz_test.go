// Fuzzing the RESPONSE path: whatever the server sends, this package must
// either bind it or return an error.
//
// A configuration source is trusted infrastructure right up until it is not —
// a compromised or simply buggy server, a truncated response, a proxy that
// injects an error page. None of that may crash the program that is trying to
// read its own configuration.
package azurekeyvault_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ubgo/cfgkit"
	azsrc "github.com/ubgo/cfgkit/contrib/source-azurekeyvault"
)

// fuzzCfg is wide enough to exercise scalar decoding on whatever keys the
// fuzzed body happens to produce.
type fuzzCfg struct {
	Host string `env:"HOST"`
	Port int    `env:"PORT"`
	On   bool   `env:"ON"`
}

// FuzzAzureResponseNeverPanics serves arbitrary bytes as the response body.
func FuzzAzureResponseNeverPanics(f *testing.F) {
	for _, seed := range []string{
		"", "{}", "[]", "null", "{\"data\":{}}", "{\"data\":null}",
		"{\"data\":{\"data\":{\"HOST\":\"h\"}}}",
		"[{\"Key\":\"a\",\"Value\":\"!!!\"}]",
		"{\"kvs\":[{\"key\":\"YQ==\",\"value\":\"!!!not-base64\"}]}",
		"{\"value\":\"v\"}", "{\"payload\":{\"data\":\"!!!\"}}",
		"<html>error</html>", "\x00\xff", "{\"data\":[1,2,3]}",
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(body)
		}))
		defer srv.Close()

		// Errors are the expected outcome for almost every input; only a
		// panic or a hang is a finding.
		_, _, _ = cfgkit.Load[fuzzCfg](cfgkit.WithSources(azsrc.Secret("s", "HOST", azsrc.WithVault(srv.URL), azsrc.WithToken("t"))))
	})
}
