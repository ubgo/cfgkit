// Output is pinned so a change in structured merging, masking or Explain
// fails the gate rather than making the README wrong.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// Example_document binds a nested document onto a nested struct.
func Example_document() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// checkout on 0.0.0.0:9000
	//
	// FIELD              KEY                VALUE                   SOURCE
	// Database.Password  DATABASE_PASSWORD  ••••••                  json
	// Database.URL       DATABASE_URL       postgres://db/checkout  json
	// Server.Host        HOST               0.0.0.0                 default
	// Server.Port        PORT               9000                    json
	// Service            SERVICE            checkout                json
	//
	// structured sources applied: json
}

// TestStructuredSourceMergesRatherThanReplaces is the rule worth pinning: the
// document says nothing about server.host, and the compiled-in default
// survives. A source that REPLACED the struct would blank every field it
// omitted, which is how config systems lose settings nobody edited.
func TestStructuredSourceMergesRatherThanReplaces(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "Server.Host        HOST               0.0.0.0                 default") {
		t.Errorf("an omitted field was not left at its default:\n%s", out.String())
	}
}

// TestSecretFromAJSONDocumentIsStillMasked pins that masking follows the
// FIELD, not the source. A password does not become printable by arriving in
// a document rather than an environment variable.
func TestSecretFromAJSONDocumentIsStillMasked(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out.String(), "s3cret") {
		t.Errorf("the password reached the output:\n%s", out.String())
	}
}

// TestEnvironmentOverridesTheDocument pins that a structured source does not
// outrank a later flat one — precedence is positional for both kinds.
func TestEnvironmentOverridesTheDocument(t *testing.T) {
	t.Setenv("PORT", "7000")

	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "0.0.0.0:7000") {
		t.Errorf("the environment did not override the document:\n%s", out.String())
	}
}
