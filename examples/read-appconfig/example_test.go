// Output is pinned so a change in the two-call protocol or the document
// merging fails the gate.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func Example_appConfig() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// checkout on 0.0.0.0:9100
	// calls: [StartConfigurationSession GetLatestConfiguration]
	//
	// FIELD        KEY      VALUE     SOURCE
	// Server.Host  HOST     0.0.0.0   default
	// Server.Port  PORT     9100      appconfig:checkout/production/config
	// Service      SERVICE  checkout  appconfig:checkout/production/config
	//
	// structured sources applied: appconfig:checkout/production/config
}

// TestBothCallsHappenInOrder pins AppConfig's two-call protocol. Reading
// without a session fails, and the two calls fail for DIFFERENT reasons —
// permissions on the profile versus nothing deployed to it — which is why the
// adapter names the stage rather than reporting one opaque error.
func TestBothCallsHappenInOrder(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "calls: [StartConfigurationSession GetLatestConfiguration]") {
		t.Errorf("the protocol was not followed in order:\n%s", out.String())
	}
}

// TestProfileIsNamedInProvenance pins that the SOURCE column carries all three
// coordinates. "appconfig" alone would not distinguish staging from prod.
func TestProfileIsNamedInProvenance(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "appconfig:" + application + "/" + environment + "/" + profile
	if !strings.Contains(out.String(), want) {
		t.Errorf("provenance does not name the profile:\n%s", out.String())
	}
}
