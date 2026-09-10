// Output is pinned so a change in FromStruct or Explain fails the gate.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// Example_computedBaseline shows a baseline no `default:` tag could express:
// the values follow from a tier chosen at startup.
func Example_computedBaseline() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// api in us-east-1: concurrency=64 endpoint=https://large.internal
	//
	// FIELD        KEY          VALUE                   SOURCE
	// Concurrency  CONCURRENCY  64                      baseline
	// Endpoint     ENDPOINT     https://large.internal  baseline
	// Region       REGION       us-east-1               baseline
	// Service      SERVICE      api                     default
	//
	// structured sources applied: baseline
}

// TestEnvironmentStillOverridesTheComputedBaseline pins that a baseline built
// in Go gets no special authority. It is a source like any other, and it is
// listed before FromEnviron, so an operator can still win.
func TestEnvironmentStillOverridesTheComputedBaseline(t *testing.T) {
	t.Setenv("CONCURRENCY", "8")

	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()

	if !strings.Contains(got, "concurrency=8") {
		t.Errorf("the environment did not override the baseline:\n%s", got)
	}
	if !strings.Contains(got, "Concurrency  CONCURRENCY  8") {
		t.Errorf("Explain does not attribute the override:\n%s", got)
	}
}

// TestUnsetBaselineFieldFallsBackToTheTag pins the merge rule: a structured
// source supplies what it has, and the struct's own default covers the rest.
// Service is in no tier, so it stays "api".
func TestUnsetBaselineFieldFallsBackToTheTag(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "Service      SERVICE      api                     default") {
		t.Errorf("Service did not fall back to its tag:\n%s", out.String())
	}
}
