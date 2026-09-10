// Output is pinned so a change in defaulting, slice splitting or Explain
// fails the gate rather than making the README wrong.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// Example_zeroInput is the property the whole library hangs off: no sources,
// no variables, and still a valid configuration.
func Example_zeroInput() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// demo :8080 debug=false timeout=30s
	// origins: [http://localhost:3000 http://localhost:5173]
	//
	// FIELD        KEY           VALUE                                        SOURCE
	// Debug        DEBUG         false                                        default
	// ExtraHeader  EXTRA_HEADER                                               default
	// Name         APP_NAME      demo                                         default
	// Origins      ORIGINS       http://localhost:3000,http://localhost:5173  default
	// Port         PORT          8080                                         default
	// Timeout      TIMEOUT       30s                                          default
}

// TestEveryFieldIsAccountedFor pins that Explain lists EVERY field, including
// the one with no default and no value. A field missing from the table would
// be a field nobody can audit, which defeats the purpose of provenance.
func TestEveryFieldIsAccountedFor(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	for _, field := range []string{"Debug", "ExtraHeader", "Name", "Origins", "Port", "Timeout"} {
		if !strings.Contains(got, field) {
			t.Errorf("field %q missing from Explain:\n%s", field, got)
		}
	}
}

// TestDurationAndSliceDefaultsAreParsedNotStored pins that a default is fed
// through the SAME decoding path as a real value. "30s" becoming a
// time.Duration and a comma list becoming a slice is what makes defaults
// trustworthy — a default that skipped decoding could hold a value the type
// cannot represent, and would only fail once someone set it.
func TestDurationAndSliceDefaultsAreParsedNotStored(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "timeout=30s") {
		t.Errorf("duration default did not decode:\n%s", got)
	}
	// Two elements, printed as a Go slice — not one string with a comma in it.
	if !strings.Contains(got, "origins: [http://localhost:3000 http://localhost:5173]") {
		t.Errorf("slice default did not split on the delimiter:\n%s", got)
	}
}
