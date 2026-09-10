// Output is pinned so a change in ordering, attribution or unknown-key
// reporting fails the gate rather than making the README wrong.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// Example_fourLayers stacks defaults, a baseline file, an overlay file and an
// operator override, and shows which one won each field.
func Example_fourLayers() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// checkout :9090 level=warn new-checkout=true
	//
	// FIELD               KEY                   VALUE     SOURCE
	// AppName             APP_NAME              checkout  file:base.env
	// FeatureNewCheckout  FEATURE_NEW_CHECKOUT  true      file:prod.env
	// LogLevel            LOG_LEVEL             warn      file:prod.env
	// Port                PORT                  9090      map
	//
	// keys no field claimed:
	//   DEPLOY_REGION (from file:prod.env)
}

// TestEverySourceIsRepresented pins the point of the example: with four
// layers, each one actually decided something, and Explain names which. A
// SOURCE column that collapsed to one name would make layering unauditable.
func TestEverySourceIsRepresented(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	for _, source := range []string{"file:base.env", "file:prod.env", "map"} {
		if !strings.Contains(got, source) {
			t.Errorf("no field attributed to %q:\n%s", source, got)
		}
	}
}

// TestOverlayOnlySetsWhatDiffers pins the layering idiom: prod.env does not
// mention APP_NAME, so the baseline still owns it. An overlay that had to
// restate every key would drift from the baseline the first time one changed.
func TestOverlayOnlySetsWhatDiffers(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "AppName             APP_NAME              checkout  file:base.env") {
		t.Errorf("APP_NAME was not left to the baseline:\n%s", out.String())
	}
}

// TestUnknownKeyIsReportedNotFatal pins the deliberate non-failure. One file
// commonly serves several audiences — app config beside deploy-pipeline
// variables — so a key no field binds is surfaced for typo-hunting rather
// than treated as an error that stops the program booting.
func TestUnknownKeyIsReportedNotFatal(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run returned an error for an unbound key: %v", err)
	}
	if !strings.Contains(out.String(), "DEPLOY_REGION (from file:prod.env)") {
		t.Errorf("the unbound key was not reported:\n%s", out.String())
	}
}
