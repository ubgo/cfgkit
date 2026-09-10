// Fuzz targets for the decoding surface.
//
// A table of edge cases is only as good as the imagination that wrote it.
// These generate the input space instead of enumerating it, and assert
// PROPERTIES that must hold for every input rather than outcomes for the ones
// somebody thought of.
package cfgkit_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ubgo/cfgkit"
)

// scalarTypes covers every scalar kind the decoder handles, so one fuzzed
// value is tried against all of them at once.
type scalarTypes struct {
	S   string        `env:"V"`
	B   bool          `env:"V"`
	I   int           `env:"V"`
	I8  int8          `env:"V"`
	I64 int64         `env:"V"`
	U   uint          `env:"V"`
	U8  uint8         `env:"V"`
	F32 float32       `env:"V"`
	F64 float64       `env:"V"`
	D   time.Duration `env:"V"`
	T   time.Time     `env:"V"`
}

// FuzzScalarDecodeNeverPanics pins the only universal guarantee the decoder
// can make about arbitrary text: it either binds or returns an error.
//
// A panic here would be reachable from any .env file, any environment
// variable and any remote store — every one of which is attacker-adjacent
// input in some deployment.
func FuzzScalarDecodeNeverPanics(f *testing.F) {
	for _, seed := range []string{
		"", " ", "0", "-0", "+1", "1e400", "NaN", "Inf", "-Inf",
		"9223372036854775808", "-9223372036854775809", "0x10", "1_000",
		"true", "TRUE", "yes", "30s", "1d", "2026-01-02T15:04:05Z",
		"\x00", "\xff\xfe", "\n", "\r\n", strings.Repeat("9", 400),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		// Errors are the expected outcome for most inputs. A panic is not.
		_, _, _ = cfgkit.Load[scalarTypes](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"V": raw}),
		))
	})
}

// stringOnly is the narrowest possible config: one string field.
type stringOnly struct {
	V string `env:"V"`
}

// FuzzStringIsABytePipe asserts a PROPERTY rather than the absence of a crash:
// whatever bytes go in, exactly those bytes come out.
//
// It is the guarantee configuration actually depends on. Certificates, JSON
// blobs, base64 and binary-ish secrets all travel through string fields, and
// any normalisation — UTF-8 validation, trimming, newline folding — would
// corrupt one of them silently. Silent corruption of a private key is far
// worse than a parse error.
func FuzzStringIsABytePipe(f *testing.F) {
	for _, seed := range []string{
		"", " ", "  padded  ", "\x00", "\xff\xfe\xfd", "line\nbreak",
		"a\r\nb", "🔥", "-----BEGIN KEY-----\nabc\n-----END KEY-----",
		strings.Repeat("x", 4096),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		cfg, _, err := cfgkit.Load[stringOnly](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"V": raw}),
		))
		if err != nil {
			t.Fatalf("a string field rejected %d bytes: %v", len(raw), err)
		}
		if cfg.V != raw {
			t.Fatalf("value was altered: got %d bytes, want %d", len(cfg.V), len(raw))
		}
	})
}

// listTypes exercises slice splitting with a single-character delimiter.
type listTypes struct {
	S []string        `env:"V" delim:","`
	I []int           `env:"V" delim:","`
	D []time.Duration `env:"V" delim:","`
}

// FuzzSliceDecodeNeverPanics fuzzes the splitter, which is where an empty
// element, a lone delimiter or a 10,000-field list would surface.
func FuzzSliceDecodeNeverPanics(f *testing.F) {
	for _, seed := range []string{
		"", ",", ",,", "a", "a,", ",a", "a,,b", " a , b ",
		"1,2,3", "1,x,3", strings.Repeat("a,", 500), "\x00,\xff",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		_, _, _ = cfgkit.Load[listTypes](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"V": raw}),
		))
	})
}

// FuzzStringSliceRoundTripsThroughItsDelimiter asserts the splitter's
// structural property: joining the result on the delimiter reproduces the
// input, once the per-element trimming is accounted for.
//
// Elements carrying surrounding WHITESPACE are skipped rather than asserted,
// because trimming is lossy by design there — " a , b " is meant to become
// [a b], and demanding an exact round trip would pin the wrong rule.
//
// "Whitespace" means strings.TrimSpace's definition, not just the space
// character. The first run of this target found that out: "0\r," round-tripped
// to "0," because the carriage return was trimmed away. That is correct — it
// is what makes a CRLF .env file produce clean list elements — but it made the
// original guard, which only skipped spaces, assert the wrong thing.
func FuzzStringSliceRoundTripsThroughItsDelimiter(f *testing.F) {
	for _, seed := range []string{"", "a", "a,b", "a,,b", "a,", ",a", "x,y,z"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		for _, elem := range strings.Split(raw, ",") {
			if strings.TrimSpace(elem) != elem {
				t.Skip("trimming is deliberately lossy for padded elements")
			}
		}

		cfg, _, err := cfgkit.Load[stringList](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"V": raw}),
		))
		if err != nil {
			t.Fatalf("a string list rejected %q: %v", raw, err)
		}

		// An empty value is the one input that yields no elements at all,
		// rather than one empty element — so it cannot round-trip by joining.
		if raw == "" {
			if len(cfg.V) != 0 {
				t.Fatalf("empty value produced %d elements, want 0", len(cfg.V))
			}
			return
		}
		if got := strings.Join(cfg.V, ","); got != raw {
			t.Fatalf("join(split(%q)) = %q", raw, got)
		}
	})
}

// stringList is separate from listTypes so the round-trip target binds only
// the field it reasons about.
type stringList struct {
	V []string `env:"V" delim:","`
}
