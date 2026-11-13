package jsonc_test

import (
	"testing"

	jsoncsrc "github.com/ubgo/cfgkit/contrib/format-jsonc"
)

// Label is a non-struct type, embedded below to check that LocateField skips
// embedded fields it cannot look inside.
type Label string

type Meta struct {
	Owner string `json:"owner"`
}

type Net struct {
	Port int `json:"port"`
}

// edgeCfg covers the json-tag rules LocateField mirrors from encoding/json:
// a skipped field, an embedded non-struct, an embedded struct with its own
// JSON name (a nested object) and one without (promoted members).
type edgeCfg struct {
	Label
	Secret string `json:"-"`
	Meta   `json:"meta"`
	*Net
	Name string
}

const edgeDoc = `{
  "Name": "svc",
  "meta": {"owner": "ops"},
  "port": 9000,
  "counts": {"0": "zero"}
}
`

func TestLocateFieldFollowsJSONTagRules(t *testing.T) {
	p := write(t, "edge.jsonc", edgeDoc)
	src := jsoncsrc.File(p)
	var got edgeCfg
	if err := src.Apply(&got); err != nil {
		t.Fatal(err)
	}
	if got.Owner != "ops" || got.Net == nil || got.Port != 9000 || got.Name != "svc" {
		t.Fatalf("decoded %+v", got)
	}

	cases := []struct {
		goPath    string
		jsonPath  string
		line, col int
	}{
		{"Name", "Name", 2, 11},        // untagged: the Go name is the member name
		{"Owner", "meta.owner", 3, 21}, // named embedded struct is a nested object
		{"Port", "port", 4, 11},        // unnamed embedded pointer struct is promoted
	}
	for _, c := range cases {
		l, ok := src.LocateField(got, c.goPath)
		if !ok || l.Path != c.jsonPath || l.Pos.Line != c.line || l.Pos.Col != c.col || l.File != p {
			t.Errorf("LocateField(%s) = %+v, %v; want %s at %d:%d", c.goPath, l, ok, c.jsonPath, c.line, c.col)
		}
	}
	for _, goPath := range []string{
		"Secret",    // json:"-" is never in the document
		"Label",     // an embedded non-struct is not looked inside
		"Name.Deep", // a path through a string field
		"",          // no field at all
	} {
		if l, ok := src.LocateField(got, goPath); ok {
			t.Errorf("LocateField(%q) = %+v, want not found", goPath, l)
		}
	}
	if _, ok := src.LocateField(42, "Name"); ok {
		t.Error("a non-struct configuration type has no fields to locate")
	}
}

func TestLocateEdgeCases(t *testing.T) {
	src := jsoncsrc.Document("edge.jsonc", []byte(edgeDoc))
	var got edgeCfg
	if err := src.Apply(&got); err != nil {
		t.Fatal(err)
	}
	// A JSON Pointer token of digits under an object is a member name, not an
	// index, so the canonical path keeps it as a key.
	l, ok := src.Locate("/counts/0")
	if !ok || l.Pos.Line != 5 || l.Pos.Col != 19 || l.File != "edge.jsonc" {
		t.Fatalf("Locate(/counts/0) = %+v, %v", l, ok)
	}
	if again, ok := src.Locate(l.Path); !ok || again.Pos != l.Pos {
		t.Errorf("canonical path %q does not locate the same value: %+v, %v", l.Path, again, ok)
	}
	for _, p := range []string{"nope", "meta.nope", "/port/1", "meta[", "/counts/1"} {
		if l, ok := src.Locate(p); ok {
			t.Errorf("Locate(%q) = %+v, want not found", p, l)
		}
	}
}

// TestBrokenDocumentLocatesNothing: after an Apply that failed on syntax, the
// source holds no document to point into.
func TestBrokenDocumentLocatesNothing(t *testing.T) {
	src := jsoncsrc.Document("broken.jsonc", []byte(`{"name": `))
	var got edgeCfg
	if err := src.Apply(&got); err == nil {
		t.Fatal("want a syntax error")
	}
	if _, ok := src.Locate("name"); ok {
		t.Error("a broken document has no locations")
	}
	if n := len(src.Locations()); n != 0 {
		t.Errorf("Locations = %d entries, want none", n)
	}
	if o := src.Origins(nil, got); len(o) != 0 {
		t.Errorf("Origins(nil) = %v, want empty", o)
	}
}
