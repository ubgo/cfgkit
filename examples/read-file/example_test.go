// The output below is pinned, so a change in loading, expansion, masking or
// Explain fails the gate rather than quietly making the README wrong. A
// documented output that nobody re-runs is a claim, not documentation.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// Example_fromFile is the default path: the file supplies everything, and the
// environment overrides nothing.
func Example_fromFile() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// checkout listening on http://localhost:8080
	//
	// where each value came from:
	// FIELD             KEY                VALUE                              SOURCE
	// AppName           APP_NAME           checkout                           file:app.env
	// BaseURL           BASE_URL           http://localhost:8080              file:app.env
	// DatabasePassword  DATABASE_PASSWORD  ••••••                             file:app.env
	// DatabaseURL       DATABASE_URL       postgres://localhost/checkout_dev  file:app.env
	// Greeting          GREETING           hello, world                       file:app.env
	// Port              PORT               8080                               file:app.env
}

// TestEnvironmentOverridesTheFile pins the precedence rule rather than
// describing it: FromEnviron is listed after FromFiles, so it wins — and the
// SOURCE column says so, which is what makes the override debuggable.
func TestEnvironmentOverridesTheFile(t *testing.T) {
	t.Setenv("PORT", "9090")

	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()

	if !strings.Contains(got, "Port              PORT               9090                               environ") {
		t.Errorf("PORT did not come from the environment:\n%s", got)
	}
	// BASE_URL still reads 8080: it was expanded when the FILE was parsed,
	// against the file's own PORT. An override of PORT does not retroactively
	// rewrite a value another source already resolved.
	if !strings.Contains(got, "http://localhost:8080") {
		t.Errorf("BASE_URL was rewritten by the override:\n%s", got)
	}
}
