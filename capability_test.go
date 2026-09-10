package cfgkit_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/cfgkit"
)

type capCfg struct {
	Secret  string        `env:"CAP_SECRET" secret:"true"`
	Plain   string        `env:"CAP_PLAIN"`
	Timeout time.Duration `env:"CAP_TIMEOUT" default:"1s"`
}

// TestTransformerRewritesValues pins the decryption use case: a transformer
// changes what a value reads as, and the field receives the transformed text.
func TestTransformerRewritesValues(t *testing.T) {
	cfg, _, err := cfgkit.Load[capCfg](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{
			"CAP_SECRET": "encrypted:aGVsbG8",
			"CAP_PLAIN":  "untouched",
		})),
		cfgkit.WithTransform(func(key, v, src string) (string, error) {
			s, ok := strings.CutPrefix(v, "encrypted:")
			if !ok {
				return v, nil // not ours — pass through unchanged
			}
			return "decrypted(" + s + ")", nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Secret != "decrypted(aGVsbG8)" {
		t.Errorf("Secret = %q, want the transformed value", cfg.Secret)
	}
	if cfg.Plain != "untouched" {
		t.Errorf("Plain = %q — a transformer that declines must pass the value through", cfg.Plain)
	}
}

// TestTransformersChain pins that several transformers compose, each seeing the
// previous one's output, in registration order.
func TestTransformersChain(t *testing.T) {
	cfg, _, err := cfgkit.Load[capCfg](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"CAP_PLAIN": "a"})),
		cfgkit.WithTransform(func(_, v, _ string) (string, error) { return v + "-first", nil }),
		cfgkit.WithTransform(func(_, v, _ string) (string, error) { return v + "-second", nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plain != "a-first-second" {
		t.Errorf("Plain = %q, want the chain applied in registration order", cfg.Plain)
	}
}

// TestTransformerSeesKeyAndSource pins that a transformer can act on one origin
// only — decrypt from Vault, leave the same key alone from a local file.
func TestTransformerSeesKeyAndSource(t *testing.T) {
	var seen []string
	_, _, err := cfgkit.Load[capCfg](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"CAP_PLAIN": "v"})),
		cfgkit.WithTransform(func(key, v, src string) (string, error) {
			seen = append(seen, key+"@"+src)
			return v, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != "CAP_PLAIN@map" {
		t.Errorf("transformer saw %v, want the key and the winning source", seen)
	}
}

// TestTransformerErrorAborts pins that a failing transformer stops the load
// rather than leaving a half-transformed value in place.
func TestTransformerErrorAborts(t *testing.T) {
	_, _, err := cfgkit.Load[capCfg](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"CAP_PLAIN": "v"})),
		cfgkit.WithTransform(func(string, string, string) (string, error) {
			return "", errors.New("decryption key unavailable")
		}),
	)
	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Errorf("a failing transformer must abort, got: %v", err)
	}
}

// TestTransformerCannotChangeTheOrigin is THE FIREWALL TEST.
//
// A capability may change what a value reads as. It may never change which
// source won. If a transformer could rewrite the origin, Explain would become
// a lie — and provenance that cannot be trusted is worse than none, because
// somebody debugging at 3am would believe it.
func TestTransformerCannotChangeTheOrigin(t *testing.T) {
	_, res, err := cfgkit.Load[capCfg](
		cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"CAP_PLAIN": "from-first"}),
			cfgkit.SourceFunc("winner", func(k string) (string, bool, error) {
				if k == "CAP_PLAIN" {
					return "from-winner", true, nil
				}
				return "", false, nil
			}),
		),
		// A transformer that tries its hardest to look like another source.
		cfgkit.WithTransform(func(_, _, _ string) (string, error) {
			return "rewritten-by-transformer", nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range res.Fields() {
		if f.Path != "Plain" {
			continue
		}
		// The VALUE is the transformer's — that is allowed.
		if f.Value != "rewritten-by-transformer" {
			t.Errorf("value = %q, a transformer may change what a value reads as", f.Value)
		}
		// The SOURCE is still the source that won — that is the firewall.
		if f.Source != "winner" {
			t.Errorf("source = %q, want %q — no capability may change which source won", f.Source, "winner")
		}
	}
}

// Bespoke is a third-party-shaped type: no unmarshaler, not ours to change.
type Bespoke struct {
	A, B string
}

type decCfg struct {
	Pair    Bespoke       `env:"DEC_PAIR"`
	Timeout time.Duration `env:"DEC_TIMEOUT" default:"1s"`
}

// TestDecoderHandlesAnUnknownType pins the primary use case: a type the core
// cannot parse and that you cannot add a method to.
func TestDecoderHandlesAnUnknownType(t *testing.T) {
	cfg, _, err := cfgkit.Load[decCfg](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"DEC_PAIR": "x|y"})),
		cfgkit.WithDecoder(cfgkit.DecoderFunc(func(typ reflect.Type, raw string) (any, bool, error) {
			if typ != reflect.TypeOf(Bespoke{}) {
				return nil, false, nil // decline; the core handles it
			}
			a, b, ok := strings.Cut(raw, "|")
			if !ok {
				return nil, true, fmt.Errorf("%q is not A|B", raw)
			}
			return Bespoke{A: a, B: b}, true, nil
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Pair.A != "x" || cfg.Pair.B != "y" {
		t.Errorf("Pair = %+v", cfg.Pair)
	}
	if cfg.Timeout != time.Second {
		t.Errorf("Timeout = %v — a declining decoder must leave core types alone", cfg.Timeout)
	}
}

// TestDecoderOverridesCore pins that a registered decoder wins over the
// built-in handling — the reason to register one for a type the core knows.
func TestDecoderOverridesCore(t *testing.T) {
	cfg, _, err := cfgkit.Load[decCfg](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"DEC_TIMEOUT": "2"})),
		// Treat a bare number as minutes rather than rejecting it.
		cfgkit.WithDecoder(cfgkit.DecoderFunc(func(typ reflect.Type, raw string) (any, bool, error) {
			if typ != reflect.TypeOf(time.Duration(0)) {
				return nil, false, nil
			}
			var n int
			if _, err := fmt.Sscanf(raw, "%d", &n); err != nil {
				return nil, true, err
			}
			return time.Duration(n) * time.Minute, true, nil
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Timeout != 2*time.Minute {
		t.Errorf("Timeout = %v, want the decoder's reading of a bare number", cfg.Timeout)
	}
}

// TestDecoderError pins that a decoder claiming a type and rejecting the value
// reports through the normal error path.
func TestDecoderError(t *testing.T) {
	_, _, err := cfgkit.Load[decCfg](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"DEC_PAIR": "no-separator"})),
		cfgkit.WithDecoder(cfgkit.DecoderFunc(func(typ reflect.Type, raw string) (any, bool, error) {
			if typ != reflect.TypeOf(Bespoke{}) {
				return nil, false, nil
			}
			return nil, true, fmt.Errorf("%q is not A|B", raw)
		})),
	)
	if err == nil || !strings.Contains(err.Error(), "is not A|B") {
		t.Errorf("want the decoder's own error, got: %v", err)
	}
}

// TestDecoderMismatchIsReported pins the guard against a decoder that claims a
// type and returns something else — reporting beats a reflect panic.
func TestDecoderMismatchIsReported(t *testing.T) {
	_, _, err := cfgkit.Load[decCfg](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"DEC_PAIR": "x|y"})),
		cfgkit.WithDecoder(cfgkit.DecoderFunc(func(typ reflect.Type, raw string) (any, bool, error) {
			if typ != reflect.TypeOf(Bespoke{}) {
				return nil, false, nil
			}
			return 42, true, nil // wrong type on purpose
		})),
	)
	if err == nil || !strings.Contains(err.Error(), "not assignable") {
		t.Errorf("want an assignability error, got: %v", err)
	}
}

// TestObserverSeesEveryField pins the audit use case: an observer is told about
// every resolved field, with its origin.
func TestObserverSeesEveryField(t *testing.T) {
	seen := map[string]string{}
	_, _, err := cfgkit.Load[capCfg](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"CAP_PLAIN": "v"})),
		cfgkit.WithObserver(cfgkit.ObserverFunc(func(f cfgkit.Field) {
			seen[f.Path] = f.Source
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	if seen["Plain"] != "map" {
		t.Errorf("Plain origin = %q, want map", seen["Plain"])
	}
	// Fields that fell back to a default are observed too — "still on the
	// compiled-in default" is exactly what an audit wants to know.
	if seen["Timeout"] != "default" {
		t.Errorf("Timeout origin = %q, want default", seen["Timeout"])
	}
}

// TestObserverReceivesUnmaskedSecrets pins the documented contract: an audit
// sink needs the real value, so masking is the observer's responsibility and
// Field.Secret is how it knows.
func TestObserverReceivesUnmaskedSecrets(t *testing.T) {
	const secret = "real-secret-value"
	var got Field
	_, res, err := cfgkit.Load[capCfg](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"CAP_SECRET": secret})),
		cfgkit.WithObserver(cfgkit.ObserverFunc(func(f cfgkit.Field) {
			if f.Path == "Secret" {
				got = Field(f)
			}
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != secret {
		t.Errorf("observer got %q, want the unmasked value", got.Value)
	}
	if !got.Secret {
		t.Error("Field.Secret must be set so an observer can decide for itself")
	}

	// Explain is still masked — the observer's access does not leak into it.
	var sb strings.Builder
	_ = res.Explain(&sb)
	if strings.Contains(sb.String(), secret) {
		t.Error("Explain leaked the secret")
	}
}

// Field is a local alias so the test above can copy the struct by value.
type Field = cfgkit.Field

// TestObserversAllRun pins that every registered observer fires, in order.
func TestObserversAllRun(t *testing.T) {
	var order []string
	_, _, err := cfgkit.Load[capCfg](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"CAP_PLAIN": "v"})),
		cfgkit.WithObserver(cfgkit.ObserverFunc(func(f cfgkit.Field) {
			if f.Path == "Plain" {
				order = append(order, "first")
			}
		})),
		cfgkit.WithObserver(cfgkit.ObserverFunc(func(f cfgkit.Field) {
			if f.Path == "Plain" {
				order = append(order, "second")
			}
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Errorf("observers ran %v, want both in registration order", order)
	}
}

// TestCapabilitiesComposeWithoutAffectingPrecedence is the second half of the
// firewall: with all three capabilities registered, the resolved origin of
// every field is exactly what it would be with none.
func TestCapabilitiesComposeWithoutAffectingPrecedence(t *testing.T) {
	srcs := cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"CAP_PLAIN": "low", "CAP_SECRET": "s"}),
		cfgkit.FromMap(map[string]string{"CAP_PLAIN": "high"}),
	)

	origins := func(opts ...cfgkit.Option) map[string]string {
		t.Helper()
		_, res, err := cfgkit.Load[capCfg](append([]cfgkit.Option{srcs}, opts...)...)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, f := range res.Fields() {
			out[f.Path] = f.Source
		}
		return out
	}

	bare := origins()
	loaded := origins(
		cfgkit.WithTransform(func(_, v, _ string) (string, error) { return v + "!", nil }),
		cfgkit.WithDecoder(cfgkit.DecoderFunc(func(reflect.Type, string) (any, bool, error) {
			return nil, false, nil
		})),
		cfgkit.WithObserver(cfgkit.ObserverFunc(func(cfgkit.Field) {})),
	)

	for path, want := range bare {
		if loaded[path] != want {
			t.Errorf("%s: origin changed from %q to %q once capabilities were registered", path, want, loaded[path])
		}
	}
}
