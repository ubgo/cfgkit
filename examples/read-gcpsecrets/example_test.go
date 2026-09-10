// Output is pinned so a change in the token exchange or payload decoding
// fails the gate.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func Example_googleSecretManager() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// host=localhost password-loaded=true
	//
	// FIELD     KEY       VALUE      SOURCE
	// Host      HOST      localhost  default
	// Password  PASSWORD  ••••••     gcpsecrets:checkout-db-password
}

// TestTokenComesFromTheMetadataServer pins the credential path, which is what
// this adapter replaces an SDK with. The fake metadata server refuses any
// request without Metadata-Flavor: Google, so a load that succeeds proves the
// header was sent — the exchange is not merely assumed.
func TestTokenComesFromTheMetadataServer(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("the metadata exchange failed: %v", err)
	}
	if !strings.Contains(out.String(), "password-loaded=true") {
		t.Errorf("the secret did not load:\n%s", out.String())
	}
}

// TestSecretIsMaskedAndNamed pins that the value stays masked and provenance
// names which secret it came from.
func TestSecretIsMaskedAndNamed(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "s3cret-from-gcp") {
		t.Errorf("the secret reached the output:\n%s", got)
	}
	if !strings.Contains(got, "gcpsecrets:"+secretName) {
		t.Errorf("provenance does not name the secret:\n%s", got)
	}
}
