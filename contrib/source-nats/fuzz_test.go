// Fuzzing the BUCKET contents. A KV bucket is written by other services, so
// its keys and values are as arbitrary as anything else on the network.
package nats_test

import (
	"testing"

	upstream "github.com/nats-io/nats.go"
	"github.com/ubgo/cfgkit"
	nats "github.com/ubgo/cfgkit/contrib/source-nats"
)

// fuzzKV holds exactly one entry, whose key and value the fuzzer chooses.
type fuzzKV struct{ key, value string }

func (f fuzzKV) Keys(...upstream.WatchOpt) ([]string, error) { return []string{f.key}, nil }

func (f fuzzKV) Get(key string) (upstream.KeyValueEntry, error) {
	if key != f.key {
		return nil, upstream.ErrKeyNotFound
	}
	return entry{bucket: "b", key: f.key, value: []byte(f.value)}, nil
}

func FuzzBucketContentsNeverPanic(f *testing.F) {
	for _, seed := range [][2]string{
		{"HOST", "h"}, {"", ""}, {"PORT", "not-an-int"}, {"\x00", "\xff"},
		{"api.HOST", "h"}, {"PORT", "99999999999999999999"},
	} {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, key, value string) {
		type cfg struct {
			Host string `env:"HOST"`
			Port int    `env:"PORT"`
		}
		_, _, _ = cfgkit.Load[cfg](cfgkit.WithSources(
			nats.Bucket("", "b", nats.WithKV(fuzzKV{key: key, value: value})),
		))
		// The prefix-stripping path takes a different branch through the
		// same data, so it is fuzzed too.
		_, _, _ = cfgkit.Load[cfg](cfgkit.WithSources(
			nats.Bucket("", "b",
				nats.WithKV(fuzzKV{key: key, value: value}),
				nats.WithPrefix("api.")),
		))
	})
}
