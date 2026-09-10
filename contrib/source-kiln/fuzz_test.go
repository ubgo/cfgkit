// Fuzzing the DECRYPTED variables. Decryption succeeding says nothing about
// what was inside — the plaintext is whatever someone encrypted, including a
// key with a NUL in it or a value that is not text at all.
package kiln_test

import (
	"testing"

	"github.com/ubgo/cfgkit"
	kiln "github.com/ubgo/cfgkit/contrib/source-kiln"
)

type fuzzDecrypter struct{ key, value string }

func (f fuzzDecrypter) Decrypt(string, string, string) (map[string]string, error) {
	return map[string]string{f.key: f.value}, nil
}

func FuzzDecryptedVariablesNeverPanic(f *testing.F) {
	for _, seed := range [][2]string{
		{"HOST", "h"}, {"", ""}, {"PORT", "not-an-int"}, {"\x00", "\xff"},
		{"API_HOST", "h"}, {"PORT", "99999999999999999999"},
	} {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, key, value string) {
		type cfg struct {
			Host string `env:"HOST"`
			Port int    `env:"PORT"`
		}
		// Both the plain and the prefix-stripping paths see the same pair.
		_, _, _ = cfgkit.Load[cfg](cfgkit.WithSources(
			kiln.Env("k.toml", "prod", kiln.WithDecrypter(fuzzDecrypter{key: key, value: value})),
		))
		_, _, _ = cfgkit.Load[cfg](cfgkit.WithSources(
			kiln.Env("k.toml", "prod",
				kiln.WithDecrypter(fuzzDecrypter{key: key, value: value}),
				kiln.WithPrefix("API_")),
		))
	})
}
