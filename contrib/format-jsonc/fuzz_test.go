// Fuzzing for the format-jsonc source.
package jsonc_test

import (
	"testing"

	"github.com/ubgo/cfgkit"
	jsoncsrc "github.com/ubgo/cfgkit/contrib/format-jsonc"
	"github.com/ubgo/textdata/jsonc"
)

// FuzzJsoncNeverPanics feeds arbitrary bytes through Load and every
// provenance call. A config document arrives from places anybody can write to,
// so the source must bind or return an error: never panic, never hang. Errors
// are the expected outcome for most inputs; only a crash is a finding.
func FuzzJsoncNeverPanics(f *testing.F) {
	for _, seed := range []string{
		"",
		`{"name":"x"}`,
		"// c\n{\"name\":\"x\",}",
		`{"server":{"port":"x"}}`,
		`{"hosts":[1,2]}`,
		`[1,2]`,
		"{\"name\":\"${A:-b}\"}",
		"{",
		"/* unclosed",
		"\xef\xbb\xbf{}",
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, doc []byte) {
		src := jsoncsrc.Document("fuzz.jsonc", doc)
		got, res, _ := cfgkit.Load[cfg](cfgkit.WithSources(src))
		for _, l := range src.Locations() {
			_ = l.String()
		}
		src.Locate("server.port")
		src.Origins(res, got)
		for _, p := range []string{"Name", "Server.Port", "Hosts", "Region"} {
			src.LocateField(got, p)
		}
		// The same bytes with expansion on (no command substitution, so no
		// input can run anything).
		_, _, _ = cfgkit.Load[cfg](cfgkit.WithSources(jsoncsrc.Document("fuzz.jsonc", doc, jsonc.WithExpand())))
	})
}
