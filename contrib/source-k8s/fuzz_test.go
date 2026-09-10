// Fuzzing the RESPONSE path: whatever the server sends, this package must
// either bind it or return an error.
//
// A configuration source is trusted infrastructure right up until it is not —
// a compromised or simply buggy server, a truncated response, a proxy that
// injects an error page. None of that may crash the program that is trying to
// read its own configuration.
package k8s_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ubgo/cfgkit"
	k8ssrc "github.com/ubgo/cfgkit/contrib/source-k8s"
)

// fuzzCfg is wide enough to exercise scalar decoding on whatever keys the
// fuzzed body happens to produce.
type fuzzCfg struct {
	Host string `env:"HOST"`
	Port int    `env:"PORT"`
	On   bool   `env:"ON"`
}

// FuzzK8sResponseNeverPanics serves arbitrary bytes as the response body.
func FuzzK8sResponseNeverPanics(f *testing.F) {
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
		_, _, _ = cfgkit.Load[fuzzCfg](cfgkit.WithSources(k8ssrc.ConfigMap("cm", k8ssrc.WithServer(srv.URL), k8ssrc.WithToken("t"), k8ssrc.WithNamespace("default"))))
	})
}
