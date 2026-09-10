// Tests for arms the behavioural suites do not reach: output-write failures
// and defensive guards against third-party error shapes. Each names the arm it
// pins, so a future refactor knows what it is allowed to remove.
package cfgkit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/cfgkit"
)

// brokenWriter fails every write, standing in for a closed pipe — what happens
// when `myapp --explain | head -1` closes stdout early.
type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("pipe closed") }

// explainCfg is deliberately several fields wide so the table is large enough
// to force tabwriter to flush through to the underlying writer.
type explainCfg struct {
	Alpha string `env:"ALPHA" default:"alpha-value-long-enough-to-matter"`
	Beta  string `env:"BETA" default:"beta-value-long-enough-to-matter"`
	Gamma string `env:"GAMMA" default:"gamma-value-long-enough-to-matter"`
}

// TestExplainReportsAWriteFailure pins that Explain propagates a write error
// rather than reporting success for output nobody received.
//
// It matters because Explain is what a program prints at boot to a log
// pipeline. Swallowing the failure would mean a service that believes it
// logged its configuration and did not — and provenance you cannot trust to
// have been written is provenance you cannot use in an incident.
func TestExplainReportsAWriteFailure(t *testing.T) {
	_, res, err := cfgkit.Load[explainCfg]()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := res.Explain(brokenWriter{}); err == nil {
		t.Error("Explain reported success writing to a closed pipe")
	}
}

// TestExplainSucceedsOnAWorkingWriter is the other half: the failure above
// must come from the writer, not from Explain being broken for everyone.
func TestExplainSucceedsOnAWorkingWriter(t *testing.T) {
	_, res, err := cfgkit.Load[explainCfg]()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var sb strings.Builder
	if err := res.Explain(&sb); err != nil {
		t.Fatalf("Explain to a working writer: %v", err)
	}
	if !strings.Contains(sb.String(), "Alpha") {
		t.Errorf("Explain wrote no rows:\n%s", sb.String())
	}
}

// nilJoiner is a joined error carrying a nil child.
//
// errors.Join never produces one — it filters nils — but Unwrap() []error is a
// public shape any caller's error type may implement, and a third-party type
// is under no obligation to filter. This is what the guard in countProblems
// defends against.
type nilJoiner struct{ real error }

func (n nilJoiner) Error() string   { return n.real.Error() }
func (n nilJoiner) Unwrap() []error { return []error{nil, n.real, nil} }

// nilJoinCfg fails validation with an error tree containing nils.
type nilJoinCfg struct {
	Port int `env:"PORT" default:"70000"`
}

func (c *nilJoinCfg) Validate() error {
	return nilJoiner{real: cfgkit.Range("PORT", c.Port, 1, 65535)}
}

// TestProblemCountIgnoresNilsInAJoinedError pins that a nil child is skipped
// rather than counted or dereferenced.
//
// Counting it would print a header promising more problems than are listed,
// which is the same defect as the one that made the count wrong in the first
// place — just in the other direction.
func TestProblemCountIgnoresNilsInAJoinedError(t *testing.T) {
	_, _, err := cfgkit.Load[nilJoinCfg]()
	if err == nil {
		t.Fatal("an out-of-range port loaded without error")
	}

	msg := err.Error()
	if !strings.Contains(msg, "1 problem(s)") {
		t.Errorf("nil children were counted as problems:\n%s", msg)
	}
	if !strings.Contains(msg, "PORT") {
		t.Errorf("the real problem was lost among the nils:\n%s", msg)
	}
}
