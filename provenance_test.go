package cfgkit_test

import (
	"strings"
	"testing"

	"github.com/ubgo/cfgkit"
	"github.com/ubgo/cfgkit/cfgkittest"
)

type Layered struct {
	FromDefault    string `env:"L_DEFAULT" json:"fromDefault" default:"d"`
	FromStructured string `env:"L_STRUCT" json:"fromStructured"`
	FromFlat       string `env:"L_FLAT" json:"fromFlat"`
	Overridden     string `env:"L_OVER" json:"overridden"`
}

// TestStructuredProvenance pins the capability no other Go config library has:
// after a load, every field can name the source that set it — including fields
// filled by a structured source, which reports nothing about what it touched.
func TestStructuredProvenance(t *testing.T) {
	doc := []byte(`{"fromStructured":"s","overridden":"from-json"}`)

	_, res, err := cfgkit.Load[Layered](cfgkit.WithSources(
		cfgkit.FromJSON(doc),
		cfgkit.FromMap(map[string]string{"L_FLAT": "f", "L_OVER": "from-map"}),
	))
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"FromDefault":    "default",
		"FromStructured": "json",
		"FromFlat":       "map",
		"Overridden":     "map", // the flat source is later, so it wins
	}
	for _, f := range res.Fields() {
		if w, ok := want[f.Path]; ok && f.Source != w {
			t.Errorf("%s came from %q, want %q", f.Path, f.Source, w)
		}
	}
}

// TestExplainNamesEverySource pins that the human table is complete and that
// the structured sources are listed.
func TestExplainNamesEverySource(t *testing.T) {
	_, res, err := cfgkit.Load[Layered](cfgkit.WithSources(
		cfgkit.FromJSON([]byte(`{"fromStructured":"s"}`)),
		cfgkit.FromMap(map[string]string{"L_FLAT": "f"}),
	))
	if err != nil {
		t.Fatal(err)
	}

	var sb strings.Builder
	if err := res.Explain(&sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()

	for _, want := range []string{"FIELD", "KEY", "VALUE", "SOURCE", "L_FLAT", "structured sources applied: json"} {
		if !strings.Contains(out, want) {
			t.Errorf("Explain output is missing %q:\n%s", want, out)
		}
	}
}

// TestBuiltinSourcesPassConformance runs the shared adapter suite against the
// built-in sources. Every contrib module runs the same suite, so the built-ins
// are the reference implementation rather than a special case.
func TestBuiltinSourcesPassConformance(t *testing.T) {
	t.Run("FromMap", func(t *testing.T) {
		cfgkittest.RunSourceTests(t, func(values map[string]string) cfgkit.Source {
			return cfgkit.FromMap(values)
		})
	})

	t.Run("SourceFunc", func(t *testing.T) {
		cfgkittest.RunSourceTests(t, func(values map[string]string) cfgkit.Source {
			return cfgkit.SourceFunc("custom", func(k string) (string, bool, error) {
				v, ok := values[k]
				return v, ok, nil
			})
		})
	})

	t.Run("a failing source aborts", func(t *testing.T) {
		cfgkittest.RunFailureTest(t, cfgkit.SourceFunc("broken", func(string) (string, bool, error) {
			return "", false, errBackend
		}))
	})

	t.Run("FromJSON", func(t *testing.T) {
		cfgkittest.RunStructuredTests(t, `{"value":"from-doc"}`, func(doc string) cfgkit.StructuredSource {
			return cfgkit.FromJSON([]byte(doc))
		})
	})
}

// errBackend stands in for an unreachable store.
var errBackend = errBackendType{}

type errBackendType struct{}

func (errBackendType) Error() string { return "backend unreachable" }
