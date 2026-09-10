// Output is pinned so a change in either read mode, or in the error that
// steers between them, fails the gate.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func Example_secretsManager() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// sm.internal:8500
	//
	// FIELD     KEY       VALUE        SOURCE
	// APIKey    API_KEY   ••••••       secretsmanager:acme/checkout/api-key
	// Host      HOST      sm.internal  secretsmanager:acme/checkout/config
	// Password  PASSWORD  ••••••       secretsmanager:acme/checkout/config
	// Port      PORT      8500         secretsmanager:acme/checkout/config
	//
	// JSON() against a bare string:
	//   source secretsmanager:acme/checkout/api-key failed for HOST: secret acme/checkout/api-key is not a JSON object (use secretsmanager.Whole for a plain string secret)
}

// TestTwoSecretsAreDistinguishableInProvenance pins that a load drawing from
// two secrets says which one supplied each field — needed the moment a
// rotation goes wrong and someone has to find the stale one.
func TestTwoSecretsAreDistinguishableInProvenance(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	for _, want := range []string{jsonSecret, wholeSecret} {
		if !strings.Contains(got, "secretsmanager:"+want) {
			t.Errorf("%s missing from provenance:\n%s", want, got)
		}
	}
}

// TestWrongModeNamesTheFix pins the reason JSON and Whole are separate calls
// rather than a content sniff. Guessing would be right nine times and silently
// wrong the tenth; instead the failure says which call to use.
func TestWrongModeNamesTheFix(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "use secretsmanager.Whole") {
		t.Errorf("the error does not name the fix:\n%s", out.String())
	}
}

// TestSecretValuesNeverReachTheOutput pins that neither read mode prints what
// it fetched.
func TestSecretValuesNeverReachTheOutput(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, canary := range []string{"s3cret-from-sm", "ak_live_51H8xEXAMPLE"} {
		if strings.Contains(out.String(), canary) {
			t.Errorf("%q reached the output:\n%s", canary, out.String())
		}
	}
}
