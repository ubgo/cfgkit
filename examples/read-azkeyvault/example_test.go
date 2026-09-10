// Output is pinned so a change in the IMDS exchange or vault read fails the
// gate.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func Example_azureKeyVault() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// host=localhost password-loaded=true
	//
	// FIELD     KEY       VALUE      SOURCE
	// Host      HOST      localhost  default
	// Password  PASSWORD  ••••••     azurekeyvault:checkout-db-password
}

// TestTokenComesFromIMDS pins the managed-identity exchange. The fake IMDS
// rejects any request without "Metadata: true" — the header that stops a
// browser being tricked into fetching a token — so a successful load proves it
// was sent rather than assuming it.
func TestTokenComesFromIMDS(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("the IMDS exchange failed: %v", err)
	}
	if !strings.Contains(out.String(), "password-loaded=true") {
		t.Errorf("the secret did not load:\n%s", out.String())
	}
}

// TestSecretIsMaskedAndNamed pins masking and provenance together.
func TestSecretIsMaskedAndNamed(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "s3cret-from-azure") {
		t.Errorf("the secret reached the output:\n%s", got)
	}
	if !strings.Contains(got, "azurekeyvault:"+secretName) {
		t.Errorf("provenance does not name the secret:\n%s", got)
	}
}
