// The example's output is pinned here, so a change in Explain, Document,
// masking or prefix handling fails the gate instead of silently making the
// documentation wrong. A doc that shows output nobody re-runs is a claim.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/ubgo/cfgkit"
	"github.com/ubgo/cfgkit/examples/plugin/acme"
)

// Example_zeroInput is the property the whole library hangs off: nothing is
// set, and the host and the plugin both run on compiled-in defaults.
func Example_zeroInput() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// host: demo listening on :8080
	//
	// the HOST's Explain — only the host's fields:
	// FIELD  KEY       VALUE  SOURCE
	// Name   APP_NAME  demo   default
	// Port   PORT      8080   default
	//
	// the HOST using the plugin: endpoint=https://api.acme.test retries=3
	//
	// the PLUGIN's Explain — only the plugin's fields, secret masked:
	// FIELD     KEY       VALUE                  SOURCE
	// APIKey    API_KEY   ••••••                 default
	// Endpoint  ENDPOINT  https://api.acme.test  default
	// Retries   RETRIES   3                      default
	//
	// the PLUGIN's own .env contract, which the host does not maintain:
	// # credential for the endpoint
	// # optional · secret — do not commit a real value
	// API_KEY=
	//
	// # where to send requests
	// # optional
	// ENDPOINT=https://api.acme.test
	//
	// # attempts before giving up
	// # optional
	// RETRIES=3
}

// TestPrefixedEnvironReachesOnlyThePlugin is the mechanism, asserted rather
// than described: ACME_ variables reach the plugin with the prefix stripped,
// PORT reaches the host, and neither sees the other's keys.
func TestPrefixedEnvironReachesOnlyThePlugin(t *testing.T) {
	t.Setenv("PORT", "9000")
	t.Setenv("ACME_ENDPOINT", "https://acme.prod")
	t.Setenv("ACME_RETRIES", "5")
	t.Setenv("ACME_API_KEY", "sk_live_CANARY")

	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatal(err)
	}
	got := out.String()

	for _, want := range []string{
		"listening on :9000", // the host read PORT
		"https://acme.prod",  // the plugin read ACME_ENDPOINT as ENDPOINT
		"environ:ACME_",      // provenance names the prefixed view
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}

	// The plugin's secret must not appear anywhere, including in the host's
	// output — the host never handles the value at all.
	if strings.Contains(got, "sk_live_CANARY") {
		t.Errorf("the plugin's secret leaked into the output:\n%s", got)
	}
}

// TestHostAndPluginKeysDoNotCollide pins the reason for the prefix. Both
// structs would otherwise want a bare PORT, and one would silently win.
func TestHostAndPluginKeysDoNotCollide(t *testing.T) {
	t.Setenv("PORT", "9000")
	t.Setenv("ACME_RETRIES", "7")

	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "listening on :9000") {
		t.Error("the host did not read PORT")
	}
	if strings.Contains(got, "RETRIES   9000") {
		t.Error("the host's PORT leaked into the plugin's namespace")
	}
	if !strings.Contains(got, "7") {
		t.Errorf("the plugin did not read ACME_RETRIES:\n%s", got)
	}
}

// TestPluginValidationIsThePluginsOwn pins the separation that matters most: a
// bad ACME_ value fails the load with the PLUGIN's rule, named as the plugin's
// field — and the host wrote none of that.
func TestPluginValidationIsThePluginsOwn(t *testing.T) {
	t.Setenv("ACME_RETRIES", "99") // the plugin's Range allows 0..10

	err := run(&bytes.Buffer{})
	if err == nil {
		t.Fatal("want the plugin's own validation to fail the run")
	}
	for _, want := range []string{"ACME_", "Retries"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should name %q", err, want)
		}
	}
}

// TestAccessorsExposeConfiguredValuesNotDefaults pins the payoff of the whole
// pattern: the host reads real configuration through a NARROW surface.
//
// Endpoint and Retries are the only two fields the plugin chose to expose. The
// API key sits beside them in the same unexported struct and has no accessor,
// so the host cannot read it, cannot log it, and cannot accidentally pass it
// somewhere else — a guarantee no amount of care in the host could provide on
// its own.
func TestAccessorsExposeConfiguredValuesNotDefaults(t *testing.T) {
	t.Setenv("ACME_ENDPOINT", "https://acme.prod")
	t.Setenv("ACME_RETRIES", "7")
	t.Setenv("ACME_API_KEY", "sk_live_should_never_be_reachable")

	p := &acme.Plugin{}
	if err := p.Configure(cfgkit.FromPrefixedEnviron("ACME_")); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	if got := p.Endpoint(); got != "https://acme.prod" {
		t.Errorf("Endpoint() = %q, want the configured value", got)
	}
	if got := p.Retries(); got != 7 {
		t.Errorf("Retries() = %d, want the configured value", got)
	}

	// The secret is reachable through NO exported method. That is enforced by
	// the compiler, so the test asserts the observable half: it does not leak
	// through the plugin's own Explain either.
	var out bytes.Buffer
	if err := p.Explain(&out); err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if strings.Contains(out.String(), "sk_live_should_never_be_reachable") {
		t.Errorf("the API key reached the plugin's Explain:\n%s", out.String())
	}
}
