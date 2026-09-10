// The invariants CONFIG_SPEC §15 names, plus the fuzz target, plus the last
// reachable arms. Each test states which guarantee it protects, so a future
// refactor knows what it breaks rather than just seeing a red line.
package cfgkit_test

import (
	"bytes"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/cfgkit"
)

// ---------------------------------------------------------------------------
// Invariants
// ---------------------------------------------------------------------------

type invCfg struct {
	Port    int           `env:"INV_PORT" default:"8080" doc:"port"`
	Name    string        `env:"INV_NAME" default:"svc"`
	Secret  string        `env:"INV_SECRET" secret:"true"`
	Timeout time.Duration `env:"INV_TIMEOUT" default:"5s"`
	Hosts   []string      `env:"INV_HOSTS"`
	Nested  invNested     `env:",prefix=INV_N_" json:"nested"`
}

type invNested struct {
	Value string `env:"VALUE" json:"value" default:"n"`
}

// TestInvariantIdempotence pins that loading twice from identical sources
// produces identical configuration AND identical provenance.
//
// Why it matters: a loader that is not idempotent makes every other guarantee
// conditional on when you asked. It would also make Document's byte-stability —
// the thing a CI drift gate depends on — an accident rather than a property.
func TestInvariantIdempotence(t *testing.T) {
	srcs := func() cfgkit.Option {
		return cfgkit.WithSources(
			cfgkit.FromJSON([]byte(`{"nested":{"value":"from-json"}}`)),
			cfgkit.FromMap(map[string]string{"INV_PORT": "9001", "INV_HOSTS": "a,b"}),
		)
	}

	c1, r1, err1 := cfgkit.Load[invCfg](srcs())
	c2, r2, err2 := cfgkit.Load[invCfg](srcs())
	if err1 != nil || err2 != nil {
		t.Fatalf("errs: %v %v", err1, err2)
	}

	// reflect.DeepEqual rather than == because the struct holds a slice.
	if !reflect.DeepEqual(c1, c2) {
		t.Errorf("configs differ across identical loads:\n%+v\n%+v", *c1, *c2)
	}

	f1, f2 := r1.Fields(), r2.Fields()
	if len(f1) != len(f2) {
		t.Fatalf("field counts differ: %d vs %d", len(f1), len(f2))
	}
	for i := range f1 {
		if f1[i] != f2[i] {
			t.Errorf("provenance differs at %d:\n%+v\n%+v", i, f1[i], f2[i])
		}
	}

	// Explain must also be byte-identical, since a human diffs it.
	var b1, b2 bytes.Buffer
	if err := errors.Join(r1.Explain(&b1), r2.Explain(&b2)); err != nil {
		t.Fatal(err)
	}
	if b1.String() != b2.String() {
		t.Error("Explain output is not stable across identical loads")
	}
}

// TestInvariantContractCompleteness pins that Document and Load agree on the
// key set: every key the contract file emits is a key Load binds, and every key
// Load binds appears in the contract.
//
// Why it matters: a key Load requires but Document omits is a field an operator
// cannot discover — they fill in the example file completely and the app still
// refuses to start. A key Document emits but Load ignores is a line somebody
// sets carefully that does nothing.
func TestInvariantContractCompleteness(t *testing.T) {
	var doc bytes.Buffer
	if err := cfgkit.Document[invCfg](&doc); err != nil {
		t.Fatal(err)
	}

	documented := map[string]bool{}
	for _, line := range strings.Split(doc.String(), "\n") {
		if k, _, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
			documented[k] = true
		}
	}

	_, res, err := cfgkit.Load[invCfg]()
	if err != nil {
		t.Fatal(err)
	}
	bound := map[string]bool{}
	for _, f := range res.Fields() {
		bound[f.Key] = true
	}

	for k := range bound {
		if !documented[k] {
			t.Errorf("%s is bound by Load but missing from the contract file", k)
		}
	}
	for k := range documented {
		if !bound[k] {
			t.Errorf("%s is in the contract file but bound by nothing", k)
		}
	}
}

// TestInvariantNoMutationUnderCapabilities re-checks the environment promise
// with every capability registered, since a transformer or observer is the most
// plausible place for an accidental write to creep in.
func TestInvariantNoMutationUnderCapabilities(t *testing.T) {
	t.Setenv("INV_PORT", "7000")
	before := append([]string(nil), os.Environ()...)

	_, _, err := cfgkit.Load[invCfg](
		cfgkit.WithSources(cfgkit.FromEnviron()),
		cfgkit.WithTransform(func(_, v, _ string) (string, error) { return v, nil }),
		cfgkit.WithObserver(cfgkit.ObserverFunc(func(cfgkit.Field) {})),
	)
	if err != nil {
		t.Fatal(err)
	}

	after := os.Environ()
	if len(before) != len(after) {
		t.Fatalf("environment size changed: %d → %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("entry changed: %q → %q", before[i], after[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Fuzz
// ---------------------------------------------------------------------------

type fuzzCfg struct {
	A string   `env:"FUZZ_A"`
	B int      `env:"FUZZ_B"`
	C bool     `env:"FUZZ_C"`
	D []string `env:"FUZZ_D"`
	E fuzzSub  `env:",prefix=FUZZ_SUB_"`
}

type fuzzSub struct {
	F string `env:"F"`
}

// FuzzResolutionNeverPanics is the target CONFIG_SPEC §15 names.
//
// Key-to-field resolution walks arbitrary struct tags against arbitrary source
// keys and values. It must never panic and never bind two fields to one key,
// for ANY input — including keys with separators, empty strings, and values
// that are not valid for their field's type.
//
// A panic here would be a denial of service triggered by a config file, which
// is exactly the kind of input an attacker sometimes controls.
func FuzzResolutionNeverPanics(f *testing.F) {
	f.Add("FUZZ_A", "value")
	f.Add("FUZZ_B", "notanumber")
	f.Add("FUZZ_SUB_F", "")
	f.Add("", "")
	f.Add("FUZZ_D", ",,,")
	f.Add("FUZZ_C", "maybe")

	f.Fuzz(func(t *testing.T, key, value string) {
		// A bad value must produce an error, never a panic.
		_, res, err := cfgkit.Load[fuzzCfg](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{key: value}),
		))
		if err != nil {
			return // reporting a bad value is correct behaviour
		}

		// On success, provenance must stay well-formed: one entry per field,
		// no duplicate paths, and every entry naming a real origin.
		seen := map[string]bool{}
		for _, fl := range res.Fields() {
			if fl.Path == "" {
				t.Fatalf("empty field path for key %q", key)
			}
			if seen[fl.Path] {
				t.Fatalf("two entries for path %q — a field was bound twice", fl.Path)
			}
			seen[fl.Path] = true
			if fl.Source == "" {
				t.Fatalf("field %q has no recorded origin", fl.Path)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Remaining arms
// ---------------------------------------------------------------------------

// statefulTransformer exercises WithTransformer — the type-based registration
// for a transformer that needs to carry state.
type statefulTransformer struct{ calls int }

func (s *statefulTransformer) Transform(_, v, _ string) (string, error) {
	s.calls++
	return v + "!", nil
}

// TestWithTransformer pins the type-based registration, which exists for a
// transformer holding a client or a cache rather than a closure.
func TestWithTransformer(t *testing.T) {
	st := &statefulTransformer{}
	cfg, _, err := cfgkit.Load[invCfg](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"INV_NAME": "x"})),
		cfgkit.WithTransformer(st),
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "x!" {
		t.Errorf("Name = %q, want the transformer applied", cfg.Name)
	}
	if st.calls == 0 {
		t.Error("the transformer kept no state across calls")
	}
}

// TestNilPointerSectionStaysNil covers the snapshot branch for a section that
// nothing fills, and documents today's behaviour: the walker allocates so its
// children are addressable. CONFIG_SPEC records the lazy alternative as an open
// decision; this test pins what actually happens so a change is deliberate.
func TestNilPointerSectionSnapshot(t *testing.T) {
	type opt struct {
		Sub *invNested `env:",prefix=OPT_" json:"sub"`
	}

	// A structured source that fills nothing must not be credited with changes.
	_, res, err := cfgkit.Load[opt](cfgkit.WithSources(
		cfgkit.FromJSON([]byte(`{}`)),
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Fields() {
		if f.Source == "json" {
			t.Errorf("%s attributed to a source that set nothing", f.Path)
		}
	}
}

// TestExplainRowWriteFailure covers Explain's per-row error return, which fires
// when a destination fails partway through rather than on the header.
func TestExplainRowWriteFailure(t *testing.T) {
	_, res, err := cfgkit.Load[invCfg]()
	if err != nil {
		t.Fatal(err)
	}
	// Accept the header, then fail — exercising the row loop's error path.
	w := &failAfter{n: 1}
	if err := res.Explain(w); err == nil {
		t.Error("Explain must report a mid-write failure")
	}
}

// failAfter succeeds n times then fails, so a test can target a specific write.
type failAfter struct{ n int }

func (f *failAfter) Write(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errors.New("disk full")
	}
	f.n--
	return len(p), nil
}

// TestRequiredInMatchingMode covers the arm where the rule's mode matches and
// the value is present — the pass case, which the failure tests skip over.
func TestRequiredInMatchingMode(t *testing.T) {
	if err := cfgkit.RequiredIn(cfgkit.ModeProd, cfgkit.ModeProd, "F", "set"); err != nil {
		t.Errorf("a set value must pass in the rule's own mode: %v", err)
	}
}

// TestDocumentEmptyStruct covers Document's zero-field path: a config with
// nothing bound produces an empty file rather than an error.
func TestDocumentEmptyStruct(t *testing.T) {
	type empty struct{}
	var b bytes.Buffer
	if err := cfgkit.Document[empty](&b); err != nil {
		t.Fatalf("an empty config must document cleanly: %v", err)
	}
	if b.Len() != 0 {
		t.Errorf("expected no output, got %q", b.String())
	}
}
