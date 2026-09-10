// Output is pinned so a change in the KV reply handling, naming or masking
// fails the gate rather than making the README wrong.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func Example_vault() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// vault.internal:9000
	//
	// FIELD     KEY       VALUE           SOURCE
	// Host      HOST      vault.internal  vault:secret/app/config
	// Password  PASSWORD  ••••••          vault:secret/app/config
	// Port      PORT      9000            vault:secret/app/config
}

// TestSourceNamesTheSecretPath pins that provenance identifies WHICH secret a
// value came from. "vault" alone would be useless in a program reading three.
func TestSourceNamesTheSecretPath(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "vault:secret/"+secretPath) {
		t.Errorf("the SOURCE column does not name the path:\n%s", out.String())
	}
}

// TestSecretFromVaultIsMasked pins that a value's origin does not change
// whether it can be printed. Masking follows the field.
func TestSecretFromVaultIsMasked(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out.String(), "s3cret-from-vault") {
		t.Errorf("the secret reached the output:\n%s", out.String())
	}
}
