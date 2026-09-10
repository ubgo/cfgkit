// Output is pinned so a change in Fields, JSON or masking fails the gate
// rather than making the README wrong.
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/ubgo/cfgkit"
)

// Example_audit shows the two machine-readable views of a load.
func Example_audit() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// still on a default:
	//   AppName (APP_NAME)
	//   DatabasePassword (DATABASE_PASSWORD)
	//   DatabaseURL (DATABASE_URL)
	//
	// as JSON, for a /debug/config endpoint:
	// {"mode":"prod","fields":[{"path":"AppName","key":"APP_NAME","value":"demo","source":"default","secret":false},{"path":"DatabasePassword","key":"DATABASE_PASSWORD","value":"••••••","source":"default","secret":true},{"path":"DatabaseURL","key":"DATABASE_URL","value":"postgres://localhost/dev","source":"default","secret":false},{"path":"Port","key":"PORT","value":"9090","source":"map","secret":false}]}
}

// TestSecretIsMaskedInJSONToo pins that masking follows the FIELD rather than
// the rendering. A /debug/config endpoint is exactly the place a password
// leaks, and it is reached by a different code path from Explain.
func TestSecretIsMaskedInJSONToo(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out.String(), "dev-password") {
		t.Errorf("the secret's value reached the JSON:\n%s", out.String())
	}
}

// TestJSONIsValidAndCarriesProvenance pins the shape a consumer depends on:
// every field, with its source, in a document that parses. A "JSON" output
// that only happens to look like JSON is worse than none.
func TestJSONIsValidAndCarriesProvenance(t *testing.T) {
	_, res, err := cfgkit.Load[Config](
		cfgkit.WithMode(cfgkit.ModeProd),
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"PORT": "9090"})),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	doc, err := res.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}

	var parsed struct {
		Mode   string `json:"mode"`
		Fields []struct {
			Path   string `json:"path"`
			Source string `json:"source"`
			Secret bool   `json:"secret"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("the JSON does not parse: %v", err)
	}
	if parsed.Mode != string(cfgkit.ModeProd) {
		t.Errorf("mode = %q, want prod", parsed.Mode)
	}
	if len(parsed.Fields) != 4 {
		t.Fatalf("got %d fields, want 4 — every field must be auditable", len(parsed.Fields))
	}
	for _, f := range parsed.Fields {
		if f.Source == "" {
			t.Errorf("field %s carries no source", f.Path)
		}
	}
}

// TestRevealOptsOutDeliberately pins that unmasking is possible but explicit.
// A library that could never print a secret would be worked around with
// fmt.Println, which is worse; the point is that it takes saying so.
func TestRevealOptsOutDeliberately(t *testing.T) {
	_, res, err := cfgkit.Load[Config](
		cfgkit.Reveal(),
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{})),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var out bytes.Buffer
	if err := res.Explain(&out); err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if !strings.Contains(out.String(), "dev-password") {
		t.Errorf("Reveal() did not unmask:\n%s", out.String())
	}
}

// TestDefaultAuditFindsUnsetProductionFields pins the audit itself: in
// production, a field still on its compiled-in default is usually a mistake,
// and it is invisible without provenance because the VALUE looks fine.
func TestDefaultAuditFindsUnsetProductionFields(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	for _, want := range []string{"DatabaseURL", "DatabasePassword"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s not flagged as still-on-default:\n%s", want, got)
		}
	}
	// Port WAS set, so it must not be flagged.
	if strings.Contains(strings.SplitN(got, "as JSON", 2)[0], "Port (PORT)") {
		t.Errorf("a field that was set got flagged as a default:\n%s", got)
	}
}
