// Output is pinned so a change in the typed-flag rule fails the gate. That
// rule is the whole reason a flag source is safe to put last.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// Example_noFlagsTyped is the case every other library gets wrong: the flags
// are DECLARED with defaults, none is typed, and the file still wins.
func Example_noFlagsTyped() {
	if err := run(os.Stdout, nil); err != nil {
		panic(err)
	}

	// Output:
	// stdlib flag: from-file:8080 level=info
	// FIELD     KEY        VALUE      SOURCE
	// Host      HOST       from-file  map
	// LogLevel  LOG_LEVEL  info       default
	// Port      PORT       8080       map
	//
	// pflag: from-file:8080 level=info
	// FIELD     KEY        VALUE      SOURCE
	// Host      HOST       from-file  map
	// LogLevel  LOG_LEVEL  info       default
	// Port      PORT       8080       map
}

// Example_flagTyped shows the other half: type it, and it wins — with the
// SOURCE column naming which flag package supplied it.
func Example_flagTyped() {
	if err := run(os.Stdout, []string{"--port=7000"}); err != nil {
		panic(err)
	}

	// Output:
	// stdlib flag: from-file:7000 level=info
	// FIELD     KEY        VALUE      SOURCE
	// Host      HOST       from-file  map
	// LogLevel  LOG_LEVEL  info       default
	// Port      PORT       7000       flags
	//
	// pflag: from-file:7000 level=info
	// FIELD     KEY        VALUE      SOURCE
	// Host      HOST       from-file  map
	// LogLevel  LOG_LEVEL  info       default
	// Port      PORT       7000       pflag
}

// TestFlagDefaultNeverWins is the defect this rule prevents, stated as a test.
//
// The flag default is 9999. If a source reported it as a VALUE, the flag layer
// would silently outrank the file for every field that happens to have a flag
// — and since the flag layer sits last, nothing below it could ever win again.
// viper has carried this bug for years (#671, #375).
func TestFlagDefaultNeverWins(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out.String(), "9999") {
		t.Errorf("a flag's own default reached the configuration:\n%s", out.String())
	}
	if strings.Contains(out.String(), "flag-default") {
		t.Errorf("a flag's own default reached the configuration:\n%s", out.String())
	}
}

// TestFieldWithoutAFlagTagIsNotConfigurableByFlag pins that flags are OPT-IN.
// A configuration with 150 fields must not produce a 150-flag command line, so
// exposing a field on the CLI is a decision written down in a tag.
func TestFieldWithoutAFlagTagIsNotConfigurableByFlag(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out, []string{"--port=7000"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	// LogLevel carries no flag tag, so it stays on its default regardless.
	if !strings.Contains(out.String(), "LogLevel  LOG_LEVEL  info       default") {
		t.Errorf("a field without a flag tag was affected by flags:\n%s", out.String())
	}
}
