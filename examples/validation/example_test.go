// Output is pinned so a change in rule messages, mode scoping or the problem
// count fails the gate rather than making the README wrong.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/ubgo/cfgkit"
)

// Example_check runs the three cases a config gate actually sees: a laptop, a
// production deploy missing its secrets, and values that are present but wrong.
func Example_check() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// dev, nothing set:
	//   ok
	//
	// prod, database unset:
	//   cfgkit: 2 problem(s):
	//   DATABASE_URL: is required in mode=prod but was not set (required_in)
	//   DATABASE_PASSWORD: is required in mode=prod but was not set (required_in)
	//
	// bad port and unknown log level:
	//   cfgkit: 2 problem(s):
	//   PORT: is 70000, want between 1 and 65535 (range)
	//   LOG_LEVEL: is verbose, want one of [debug info warn error] (one_of)
}

// TestEveryProblemIsReportedAtOnce pins the reason Validate joins instead of
// returning early. Reporting one problem per run turns a misconfigured deploy
// into a guessing game of one fix per restart, and every restart is another
// failed rollout.
func TestEveryProblemIsReportedAtOnce(t *testing.T) {
	err := cfgkit.Check[Config](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
		"PORT":      "70000",
		"LOG_LEVEL": "verbose",
	})))
	if err == nil {
		t.Fatal("two invalid values passed Check")
	}
	msg := err.Error()
	for _, want := range []string{"PORT", "LOG_LEVEL"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%s missing from the report:\n%s", want, msg)
		}
	}
}

// TestProblemCountMatchesTheListing pins the header against the lines beneath
// it. A header promising fewer problems than it lists sends someone away
// having fixed only the ones it admitted to.
func TestProblemCountMatchesTheListing(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()

	if strings.Contains(got, "1 problem(s)") {
		t.Errorf("a joined Validate was counted as one problem:\n%s", got)
	}
	if strings.Count(got, "2 problem(s)") != 2 {
		t.Errorf("expected both failing cases to report 2 problems:\n%s", got)
	}
}

// TestDevIsValidWithNothingSet pins the mode scoping, which is what keeps the
// zero-input promise from quietly becoming "zero input, except the seven you
// need". The same struct that fails a production gate runs on a laptop.
func TestDevIsValidWithNothingSet(t *testing.T) {
	if err := cfgkit.Check[Config](cfgkit.WithSources(cfgkit.FromMap(nil))); err != nil {
		t.Errorf("dev with nothing set did not validate: %v", err)
	}
}

// TestStagingCountsAsProduction pins the policy in mode(): staging is where a
// missing production secret should be caught, not the place it is tolerated.
func TestStagingCountsAsProduction(t *testing.T) {
	err := cfgkit.Check[Config](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
		"APP_ENV": "staging",
	})))
	if err == nil {
		t.Fatal("staging skipped the production requirements")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("staging did not require the database URL:\n%v", err)
	}
}

// TestSecretIsNotEchoedByAFailure pins that a validation failure naming a
// secret field prints the FIELD, never the value. An error message is one of
// the easiest ways for a credential to reach a log aggregator.
func TestSecretIsNotEchoedByAFailure(t *testing.T) {
	const canary = "correct-horse-battery-staple"

	err := cfgkit.Check[Config](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
		"APP_ENV":           "production",
		"DATABASE_PASSWORD": canary,
		// DATABASE_URL stays unset, so validation still fails and prints.
	})))
	if err == nil {
		t.Fatal("expected the missing database URL to fail")
	}
	if strings.Contains(err.Error(), canary) {
		t.Errorf("the secret reached the error message:\n%v", err)
	}
}
