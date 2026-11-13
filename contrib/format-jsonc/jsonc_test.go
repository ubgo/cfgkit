package jsonc_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ubgo/cfgkit"
	"github.com/ubgo/cfgkit/cfgkittest"
	jsoncsrc "github.com/ubgo/cfgkit/contrib/format-jsonc"
	"github.com/ubgo/textdata/jsonc"
	"github.com/ubgo/textdata/pos"
)

// TestConformance runs the shared adapter suite: absent fields are left
// untouched, so a JSONC document overlays rather than replaces.
func TestConformance(t *testing.T) {
	cfgkittest.RunStructuredTests(t, "// a comment\n{\"value\": \"from-doc\",}\n", func(doc string) cfgkit.StructuredSource {
		return jsoncsrc.Document("conformance.jsonc", []byte(doc))
	})
}

type server struct {
	Host string `json:"host" env:"HOST" default:"localhost"`
	Port int    `json:"port" env:"PORT" default:"8080"`
}

type Common struct {
	Region string `json:"region"`
}

type cfg struct {
	Common
	Name   string   `json:"name" env:"NAME" default:"default-name"`
	Server server   `json:"server" env:",prefix=SERVER_"`
	Hosts  []string `json:"hosts"`
	Untag  string
}

// write puts content in a fresh file and returns its path.
func write(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFileDecodes(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want cfg
	}{
		{"plain json", `{"name":"a","server":{"port":1}}`, cfg{Name: "a", Server: server{Host: "localhost", Port: 1}}},
		{"comments and trailing commas", "{\n  // the name\n  \"name\": \"b\", /* inline */\n  \"hosts\": [\"x\", \"y\",],\n}\n", cfg{Name: "b", Server: server{Host: "localhost", Port: 8080}, Hosts: []string{"x", "y"}}},
		{"bom", "\xef\xbb\xbf{\"name\":\"c\"}", cfg{Name: "c", Server: server{Host: "localhost", Port: 8080}}},
		{"embedded promoted", `{"region":"eu","Untag":"u"}`, cfg{Common: Common{Region: "eu"}, Name: "default-name", Server: server{Host: "localhost", Port: 8080}, Untag: "u"}},
		{"empty object keeps defaults", `{}`, cfg{Name: "default-name", Server: server{Host: "localhost", Port: 8080}}},
		{"empty file keeps defaults", ``, cfg{Name: "default-name", Server: server{Host: "localhost", Port: 8080}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := write(t, "config.jsonc", c.doc)
			got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(jsoncsrc.File(p)))
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != c.want.Name || got.Server != c.want.Server || got.Region != c.want.Region || got.Untag != c.want.Untag || strings.Join(got.Hosts, ",") != strings.Join(c.want.Hosts, ",") {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

const provDoc = `{
  // service name
  "name": "svc",
  "region": "eu",
  "server": {
    "host": "db.internal",
    "PORT": 5432
  },
  "hosts": ["a", "b"]
}
`

func TestProvenanceFileLine(t *testing.T) {
	p := write(t, "config.jsonc", provDoc)
	src := jsoncsrc.File(p)
	got, res, err := cfgkit.Load[cfg](cfgkit.WithSources(src, cfgkit.FromMap(map[string]string{"NAME": "env-wins"})))
	if err != nil {
		t.Fatal(err)
	}
	if got.Server.Port != 5432 {
		t.Fatalf("case-insensitive key not decoded: %+v", got)
	}

	// cfgkit names the source; the contrib adds the line.
	var buf bytes.Buffer
	if err := res.Explain(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "jsonc:"+p) {
		t.Errorf("Explain does not name the source:\n%s", buf.String())
	}

	origins := src.Origins(res, got)
	want := map[string]string{
		"Server.Host": p + ":6",
		"Server.Port": p + ":7", // matched "PORT" case-insensitively, as encoding/json does
	}
	for k, v := range want {
		if origins[k].String() != v {
			t.Errorf("Origins[%s] = %q, want %q", k, origins[k].String(), v)
		}
	}
	if _, ok := origins["Name"]; ok {
		t.Error("Name was won by the map source and must not be attributed to the file")
	}

	fields := []struct {
		goPath    string
		jsonPath  string
		line, col int
	}{
		{"Name", "name", 3, 11},
		{"Region", "region", 4, 13},
		{"Server.Host", "server.host", 6, 13},
		{"Server.Port", "server.PORT", 7, 13},
		{"Hosts", "hosts", 9, 12},
	}
	for _, f := range fields {
		l, ok := src.LocateField(&got, f.goPath)
		if !ok {
			t.Errorf("LocateField(%s): not found", f.goPath)
			continue
		}
		if l.Path != f.jsonPath || l.Pos.Line != f.line || l.Pos.Col != f.col || l.File != p {
			t.Errorf("LocateField(%s) = %+v, want %s at %d:%d", f.goPath, l, f.jsonPath, f.line, f.col)
		}
	}
	if _, ok := src.LocateField(got, "Untag"); ok {
		t.Error("a field the document does not set has no location")
	}
	if _, ok := src.LocateField(got, "Nope.Deeper"); ok {
		t.Error("an unknown Go path has no location")
	}

	if l, ok := src.Locate("/hosts/1"); !ok || l.Path != "hosts[1]" || l.Pos.Line != 9 || l.Pos.Col != 18 {
		t.Errorf("Locate(/hosts/1) = %#v, %v", l, ok)
	}

	var paths []string
	for _, l := range src.Locations() {
		paths = append(paths, l.Path)
	}
	if got, want := strings.Join(paths, " "), "name region server.host server.PORT hosts[0] hosts[1]"; got != want {
		t.Errorf("Locations = %s, want %s", got, want)
	}
}

// TestDuplicateKeysLastWins: encoding/json keeps the last matching member, so
// the location must point at that one.
func TestDuplicateKeysLastWins(t *testing.T) {
	src := jsoncsrc.Document("dup.jsonc", []byte("{\n\"name\": \"a\",\n\"NAME\": \"b\"\n}"))
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(src))
	if err != nil {
		t.Fatal(err)
	}
	l, ok := src.LocateField(got, "Name")
	if got.Name != "b" || !ok || l.String() != "dup.jsonc:3" {
		t.Errorf("Name = %q at %v (%v), want b at dup.jsonc:3", got.Name, l, ok)
	}
}

func TestMissingFileIsNotAnError(t *testing.T) {
	src := jsoncsrc.File(filepath.Join(t.TempDir(), "absent.jsonc"))
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(src))
	if err != nil {
		t.Fatalf("a missing file must not fail the load: %v", err)
	}
	if got.Name != "default-name" {
		t.Error("defaults must survive a missing file")
	}
	if _, ok := src.Locate("name"); ok {
		t.Error("a missing file has no locations")
	}
	if len(src.Locations()) != 0 {
		t.Error("a missing file has no locations")
	}
}

func TestUnreadableFileIsAnError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config.jsonc")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	err := cfgkit.Check[cfg](cfgkit.WithSources(jsoncsrc.File(dir)))
	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("want a *SourceError, got %T: %v", err, err)
	}
}

func TestErrorsCarryFileLineCol(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		opts []jsonc.Option
		want string // file:line:col suffix after the path; "|" separates accepted alternatives
	}{
		{"missing comma", "{\n  \"name\": \"a\"\n  \"port\": 1\n}", nil, ":3:3"},
		{"unclosed", "{\n  \"name\": [\n", nil, ":2:11"},
		{"wrong type", "{\n  \"server\": {\n    \"port\": \"eighty\"\n  }\n}", nil, ":3:13"},
		// encoding/json names the element (hosts.1) only when the main module
		// declares go 1.27 or later; before that it names the array, which is
		// still a real position in the file.
		{"wrong type in array", "{\n  \"hosts\": [\"a\",\n    1]\n}", nil, ":3:5|:2:12"},
		{"comment in strict mode", "{\n  // no\n  \"name\": \"a\"\n}", []jsonc.Option{jsonc.Strict()}, ":2:3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := write(t, "bad.jsonc", c.doc)
			_, _, err := cfgkit.Load[cfg](cfgkit.WithSources(jsoncsrc.File(p, c.opts...)))
			if err == nil {
				t.Fatal("want an error")
			}
			matched := false
			for _, w := range strings.Split(c.want, "|") {
				matched = matched || strings.Contains(err.Error(), p+w)
			}
			if !matched {
				t.Errorf("error %q does not point at %s%s", err, p, c.want)
			}
			if pos.First(err) == nil {
				t.Errorf("error %T carries no *pos.Error", err)
			}
		})
	}
}

func TestDocumentErrorsUseItsName(t *testing.T) {
	_, _, err := cfgkit.Load[cfg](cfgkit.WithSources(jsoncsrc.Document("embedded.jsonc", []byte("{,}"))))
	if err == nil || !strings.Contains(err.Error(), "embedded.jsonc:1:2") {
		t.Errorf("error = %v, want embedded.jsonc:1:2", err)
	}
}

const expDoc = `{
  "name": "${APP:-fallback}",
  "server": {"host": "$${literal}"}
}`

func TestExpansion(t *testing.T) {
	lookup := jsonc.WithLookup(func(n string) (string, bool) {
		if n == "APP" {
			return "from-lookup", true
		}
		return "", false
	})
	cases := []struct {
		name     string
		opts     []jsonc.Option
		wantName string
		wantHost string
	}{
		{"off by default", []jsonc.Option{lookup}, "${APP:-fallback}", "$${literal}"},
		{"on with lookup", []jsonc.Option{lookup, jsonc.WithExpand()}, "from-lookup", "${literal}"},
		{"on without lookup uses default", []jsonc.Option{jsonc.WithExpand()}, "fallback", "${literal}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := jsoncsrc.File(write(t, "exp.jsonc", expDoc), c.opts...)
			got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(src))
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != c.wantName || got.Server.Host != c.wantHost {
				t.Errorf("got name=%q host=%q, want %q %q", got.Name, got.Server.Host, c.wantName, c.wantHost)
			}
			// Positions always describe the file as written.
			if l, ok := src.LocateField(got, "Server.Host"); !ok || l.Pos.Line != 3 || l.Pos.Col != 22 {
				t.Errorf("Server.Host at %+v (%v), want 3:22", l, ok)
			}
		})
	}
}

func TestExpansionErrorFailsTheLoad(t *testing.T) {
	src := jsoncsrc.Document("req.jsonc", []byte("{\n  \"name\": \"${MUST:?is required}\"\n}"), jsonc.WithExpand())
	_, _, err := cfgkit.Load[cfg](cfgkit.WithSources(src))
	if err == nil || !strings.Contains(err.Error(), "is required") || !strings.Contains(err.Error(), "req.jsonc:2:11") {
		t.Errorf("error = %v, want the required-variable message at req.jsonc:2:11", err)
	}
	if pe := pos.First(err); pe == nil || pe.Pos.Line != 2 || pe.Pos.Col != 11 {
		t.Errorf("pos.First = %+v, want line 2 col 11", pe)
	}
}

// TestBigIntegersStayExact: the document is decoded into the struct, never
// through a map of float64, so integers above 2^53 survive, with and without
// expansion.
func TestBigIntegersStayExact(t *testing.T) {
	type big struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	const doc = `{"id": 9007199254740993, "name": "${N:-x}"}`
	for _, opts := range [][]jsonc.Option{nil, {jsonc.WithExpand()}} {
		got, _, err := cfgkit.Load[big](cfgkit.WithSources(jsoncsrc.Document("big.jsonc", []byte(doc), opts...)))
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != 9007199254740993 {
			t.Errorf("expand=%v: ID = %d, want 9007199254740993", len(opts) > 0, got.ID)
		}
	}
}

// TestReloadSeesNewContents: Apply re-reads the file, and Locate answers for
// what it read last.
func TestReloadSeesNewContents(t *testing.T) {
	p := write(t, "config.jsonc", `{"name":"one"}`)
	src := jsoncsrc.File(p)
	if _, _, err := cfgkit.Load[cfg](cfgkit.WithSources(src)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{\n\n\"name\":\"two\"}"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(src))
	if err != nil {
		t.Fatal(err)
	}
	if l, ok := src.Locate("name"); got.Name != "two" || !ok || l.Pos.Line != 3 {
		t.Errorf("after reload: name=%q at %+v", got.Name, l)
	}
}

func TestLayering(t *testing.T) {
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		jsoncsrc.Document("base", []byte(`{"name":"base","server":{"port":1}}`)),
		jsoncsrc.Document("over", []byte(`{"server":{"port":2}}`)),
		cfgkit.FromMap(map[string]string{"SERVER_HOST": "flat"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "base" || got.Server.Port != 2 || got.Server.Host != "flat" {
		t.Errorf("got %+v", got)
	}
}

func TestConcurrentApplyAndLocate(t *testing.T) {
	src := jsoncsrc.Document("c.jsonc", []byte(provDoc))
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			var c cfg
			if err := src.Apply(&c); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			src.Locate("server.host")
			src.Locations()
		}()
	}
	wg.Wait()
}

func TestBeforeApplyNothingIsLocated(t *testing.T) {
	src := jsoncsrc.Document("x", []byte(`{"name":"a"}`))
	if _, ok := src.Locate("name"); ok {
		t.Error("Locate before Apply must report false")
	}
	if src.Origins(nil, cfg{}) == nil {
		t.Error("Origins(nil) must return an empty map")
	}
	if src.Name() != "jsonc:x" {
		t.Errorf("Name = %q", src.Name())
	}
}
