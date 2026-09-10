// Output is pinned so a change in prefix trimming, folder-key handling or
// base64 decoding fails the gate.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func Example_consul() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// consul.internal:7000
	//
	// FIELD     KEY       VALUE            SOURCE
	// Host      HOST      consul.internal  consul:app/checkout
	// Password  PASSWORD  ••••••           consul:app/checkout
	// Port      PORT      7000             consul:app/checkout
}

// TestKeysArriveRelativeToThePrefix pins the property that lets one struct
// bind from Consul and from a .env file: the prefix is trimmed, so the field
// tag says HOST rather than app/checkout/HOST.
func TestKeysArriveRelativeToThePrefix(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Host      HOST") {
		t.Errorf("keys did not arrive relative to the prefix:\n%s", got)
	}
	if strings.Contains(got, prefix+"HOST") {
		t.Errorf("the full key leaked into binding:\n%s", got)
	}
}

// TestFolderKeyIsSkipped pins Consul's own quirk: it creates a valueless entry
// for the prefix itself. Binding it would produce a field named "" — so it is
// skipped, and the load succeeds rather than reporting a phantom key.
func TestFolderKeyIsSkipped(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("the folder key was not skipped: %v", err)
	}
	if strings.Contains(out.String(), "consul:app/checkout/\n") {
		t.Errorf("the folder key produced a field:\n%s", out.String())
	}
}
