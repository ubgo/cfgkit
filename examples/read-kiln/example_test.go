// Output is pinned so a change in decryption, masking or the denial message
// fails the gate. Every identity here is generated into a temp directory and
// discarded; nothing reads the developer's ~/.kiln.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func Example_kiln() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// kiln.internal:8600
	//
	// FIELD     KEY       VALUE          SOURCE
	// Host      HOST      kiln.internal  kiln:production
	// Password  PASSWORD  ••••••         kiln:production
	// Port      PORT      8600           kiln:production
	//
	// with an identity the file does not grant:
	//   cannot decrypt 'production' (ensure your key has access to this file)
}

// TestDenialIsAnErrorNotAMiss is the safety claim, proved rather than
// asserted: an identity the file does not grant gets an ERROR. A source that
// reported "not found" when it meant "not allowed" would let a deploy proceed
// with an empty password, and nothing downstream could tell the difference.
func TestDenialIsAnErrorNotAMiss(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "cannot decrypt") {
		t.Errorf("an unauthorised read did not fail:\n%s", out.String())
	}
}

// TestPlaintextNeverReachesTheOutput pins that a decrypted secret is masked
// like any other. Decryption succeeding is not permission to print.
func TestPlaintextNeverReachesTheOutput(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out.String(), "s3cret-from-kiln") {
		t.Errorf("the decrypted secret reached the output:\n%s", out.String())
	}
}
