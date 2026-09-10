// Output is pinned so a change in the range scan, base64 handling or prefix
// trimming fails the gate.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func Example_etcd() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// etcd.internal:2379
	//
	// FIELD     KEY       VALUE          SOURCE
	// Host      HOST      etcd.internal  etcd:app/config
	// Password  PASSWORD  ••••••         etcd:app/config
	// Port      PORT      2379           etcd:app/config
}

// TestKeysArriveRelativeToThePrefix pins the same trimming rule Consul has, so
// the two adapters are interchangeable from the struct's point of view.
func TestKeysArriveRelativeToThePrefix(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out.String(), prefix+"HOST") {
		t.Errorf("the full key leaked into binding:\n%s", out.String())
	}
}

// TestSecretIsMasked pins that etcd values are masked like any other source's.
func TestSecretIsMasked(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out.String(), "s3cret-from-etcd") {
		t.Errorf("the secret reached the output:\n%s", out.String())
	}
}
