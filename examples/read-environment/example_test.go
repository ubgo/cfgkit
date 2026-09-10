// Output is pinned so a change in prefix handling or Explain fails the gate
// rather than quietly making the README wrong.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// Example_defaults shows the zero-input case: nothing is set, so every field
// falls back to its tag.
func Example_defaults() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// server: port=8080 level=info
	// FIELD  KEY        VALUE  SOURCE
	// Level  LOG_LEVEL  info   default
	// Port   PORT       8080   default
	//
	// worker: concurrency=4 queue=default
	// FIELD        KEY          VALUE    SOURCE
	// Concurrency  CONCURRENCY  4        default
	// Queue        QUEUE        default  default
}

// TestPrefixIsStrippedBeforeBinding pins the reason FromPrefixedEnviron
// exists: the environment is namespaced, the struct is not. Binding the
// namespaced spelling would force every component's struct to repeat the
// prefix, and the same struct could then no longer read a plain .env file.
func TestPrefixIsStrippedBeforeBinding(t *testing.T) {
	t.Setenv("WORKER_CONCURRENCY", "16")
	t.Setenv("WORKER_QUEUE", "billing")

	// The unprefixed spelling must NOT leak into the worker's view.
	t.Setenv("CONCURRENCY", "999")

	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()

	if !strings.Contains(got, "worker: concurrency=16 queue=billing") {
		t.Errorf("prefixed variables did not bind:\n%s", got)
	}
	if strings.Contains(got, "concurrency=999") {
		t.Errorf("an unprefixed variable leaked into the prefixed load:\n%s", got)
	}
}

// TestServerLoadIgnoresPrefixedVariables is the mirror: the process-wide load
// must not pick up a component's namespaced variable either.
func TestServerLoadIgnoresPrefixedVariables(t *testing.T) {
	t.Setenv("WORKER_PORT", "7777")

	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out.String(), "port=7777") {
		t.Errorf("WORKER_PORT bound to the server's PORT:\n%s", out.String())
	}
}
