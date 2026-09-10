// Tests for the conformance harness itself.
//
// Every adapter in the catalogue claims conformance by calling RunSourceTests
// and passing. That claim is worth exactly as much as the suite's ability to
// FAIL a source that does not conform — a harness that accepts everything
// certifies nothing, and would do it silently across twenty modules.
//
// So each test here builds a source that violates exactly one guarantee and
// asserts the suite catches it. The suite is re-run in a SUBPROCESS, because a
// deliberately failing subtest would otherwise fail this test too; the exit
// code is the observation.
package cfgkittest_test

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/ubgo/cfgkit"
	"github.com/ubgo/cfgkit/cfgkittest"
)

// brokenEnv names the violation the child process should exhibit. Empty means
// "this is the parent, go spawn a child".
const brokenEnv = "CFGKITTEST_BROKEN_KIND"

// --- sources that each break exactly one rule -------------------------------

// ignoresValues never returns anything, so a value the caller supplied is lost.
type ignoresValues struct{}

func (ignoresValues) Name() string { return "broken" }
func (ignoresValues) Lookup(string) (string, bool, error) {
	return "", false, nil
}

// overwritesOnMiss claims every key, so a field nobody configured is blanked
// and its default is destroyed.
type overwritesOnMiss struct{}

func (overwritesOnMiss) Name() string { return "broken" }
func (overwritesOnMiss) Lookup(string) (string, bool, error) {
	return "", true, nil
}

// conflatesEmptyWithMissing treats a deliberately cleared value as no opinion,
// which silently resurrects the default an operator just removed.
type conflatesEmptyWithMissing struct{ values map[string]string }

func (conflatesEmptyWithMissing) Name() string { return "broken" }
func (c conflatesEmptyWithMissing) Lookup(key string) (string, bool, error) {
	v, ok := c.values[key]
	if v == "" {
		return "", false, nil // the bug
	}
	return v, ok, nil
}

// swallowsErrors reports a backend failure as a miss — the single most
// dangerous thing a source can do, because it boots on an empty password.
type swallowsErrors struct{}

func (swallowsErrors) Name() string { return "broken" }
func (swallowsErrors) Lookup(string) (string, bool, error) {
	return "", false, nil
}

// replacesInsteadOfMerging zeroes every field the document did not mention.
func replacesInsteadOfMerging(string) cfgkit.StructuredSource {
	return cfgkit.StructuredFunc("broken", func(dst any) error {
		// Overwrite the whole struct, discarding anything set before.
		return cfgkit.FromJSON([]byte(`{"value":"v","kept":""}`)).Apply(dst)
	})
}

// conforming is the control: a correct source, so a test can prove the suite
// PASSES something valid rather than merely failing everything.
type conforming struct{ values map[string]string }

func (conforming) Name() string { return "conforming" }
func (c conforming) Lookup(key string) (string, bool, error) {
	v, ok := c.values[key]
	return v, ok, nil
}

// --- the child half ---------------------------------------------------------

// runBrokenCase is executed inside the subprocess. It runs the suite against
// whichever broken source was named, and the suite's verdict becomes the
// process's exit code.
func runBrokenCase(t *testing.T, kind string) {
	switch kind {
	case "ignores-values":
		cfgkittest.RunSourceTests(t, func(map[string]string) cfgkit.Source { return ignoresValues{} })
	case "overwrites-on-miss":
		cfgkittest.RunSourceTests(t, func(map[string]string) cfgkit.Source { return overwritesOnMiss{} })
	case "conflates-empty":
		cfgkittest.RunSourceTests(t, func(v map[string]string) cfgkit.Source {
			return conflatesEmptyWithMissing{values: v}
		})
	case "swallows-errors":
		cfgkittest.RunFailureTest(t, swallowsErrors{})
	case "replaces-instead-of-merging":
		cfgkittest.RunStructuredTests(t, `{"value":"v"}`, replacesInsteadOfMerging)
	case "conforming":
		cfgkittest.RunSourceTests(t, func(v map[string]string) cfgkit.Source {
			return conforming{values: v}
		})
	default:
		t.Fatalf("unknown broken kind %q", kind)
	}
}

// TestMain routes the subprocess to the case it was asked to run.
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

// TestHarnessSubprocess is the entry point the parent re-executes. It is a
// no-op unless the environment names a case, so a normal `go test` run does
// nothing here.
func TestHarnessSubprocess(t *testing.T) {
	kind := os.Getenv(brokenEnv)
	if kind == "" {
		t.Skip("parent process: nothing to do")
	}
	runBrokenCase(t, kind)
}

// runChild re-executes this binary for one case and reports whether the
// conformance suite passed, along with everything it printed.
//
// The output matters as much as the verdict: asserting only "the child failed"
// would let a broken source be caught by an UNRELATED check and still look
// like the guarantee under test was enforced. That is not hypothetical — the
// first version of this file did exactly that, and only noticed when a
// deliberately-conforming source still failed for a different reason.
func runChild(t *testing.T, kind string) (passed bool, output string) {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run", "^TestHarnessSubprocess$", "-test.v")
	cmd.Env = append(os.Environ(), brokenEnv+"="+kind)
	out, err := cmd.CombinedOutput()

	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("could not run the child process: %v\n%s", err, out)
	}
	return err == nil, string(out)
}

// --- the assertions ---------------------------------------------------------

// TestSuiteRejectsNonConformingSources is the point of this file: each broken
// source must be CAUGHT. A suite that passed any of them would be certifying
// behaviour it never checked, across every module that calls it.
func TestSuiteRejectsNonConformingSources(t *testing.T) {
	for _, tc := range []struct {
		kind, why string
		// wantFailure is a fragment of the harness's own FAILURE MESSAGE for
		// the check that must catch this violation.
		//
		// It is deliberately the message and not the subtest name: `go test
		// -v` prints "=== RUN .../supplies_a_value" whether that subtest
		// passes or fails, so matching the name proves nothing. This file has
		// made both mistakes — asserting only that the child failed, then
		// asserting on a name that is always printed — and each time the test
		// looked green while checking nothing.
		wantFailure string
	}{
		{
			kind:        "ignores-values",
			why:         "a source that never supplies a value",
			wantFailure: `want "found"`,
		},
		{
			kind:        "overwrites-on-miss",
			why:         "a source that claims keys it has no value for, destroying defaults",
			wantFailure: "a miss must not overwrite a default",
		},
		{
			kind:        "conflates-empty",
			why:         "a source that treats a cleared value as no opinion",
			wantFailure: "want the empty value the source supplied",
		},
		{
			kind:        "swallows-errors",
			why:         "a source that reports a backend failure as a miss",
			wantFailure: "must abort the load with a *cfgkit.SourceError",
		},
		{
			kind:        "replaces-instead-of-merging",
			why:         "a structured source that zeroes fields it does not mention",
			wantFailure: "must not clear fields it does not mention",
		},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			passed, output := runChild(t, tc.kind)
			if passed {
				t.Fatalf("the conformance suite ACCEPTED %s\n%s", tc.why, output)
			}
			if !strings.Contains(output, tc.wantFailure) {
				t.Errorf("the suite failed, but not on the check that should catch %s\n"+
					"wanted %q in the output; the violation may be caught only by accident\n%s",
					tc.why, tc.wantFailure, output)
			}
		})
	}
}

// TestSuiteAcceptsAConformingSource is the control.
//
// Without it every assertion above would pass just as happily against a suite
// that fails everything unconditionally — which would be a different bug with
// the same test results.
func TestSuiteAcceptsAConformingSource(t *testing.T) {
	passed, output := runChild(t, "conforming")
	if !passed {
		t.Errorf("the conformance suite REJECTED a correct source\n%s", output)
	}
}

// TestFakeRecordsEveryCall pins the Fake's own contract. Adapters assert
// against Calls to prove they pre-load rather than dialling per field, so a
// Fake that dropped a call would let a per-field adapter claim otherwise.
func TestFakeRecordsEveryCall(t *testing.T) {
	f := &cfgkittest.Fake{Values: map[string]string{"A": "1"}}

	for _, k := range []string{"A", "B", "A"} {
		_, _, _ = f.Get(k)
	}

	if got := strings.Join(f.Calls, ","); got != "A,B,A" {
		t.Errorf("Calls = %q, want every call in order including repeats", got)
	}
}

// TestFakeDistinguishesMissFromValue pins that a Fake reports absence as a
// miss and a present key as found — including a present-but-empty value, which
// is the case the conformance suite itself relies on.
func TestFakeDistinguishesMissFromValue(t *testing.T) {
	f := &cfgkittest.Fake{Values: map[string]string{"SET": "v", "EMPTY": ""}}

	for _, tc := range []struct {
		key       string
		wantVal   string
		wantFound bool
	}{
		{"SET", "v", true},
		{"EMPTY", "", true},
		{"ABSENT", "", false},
	} {
		v, ok, err := f.Get(tc.key)
		if err != nil {
			t.Fatalf("Get(%q): %v", tc.key, err)
		}
		if v != tc.wantVal || ok != tc.wantFound {
			t.Errorf("Get(%q) = %q, %t; want %q, %t", tc.key, v, ok, tc.wantVal, tc.wantFound)
		}
	}
}

// TestFakeErrorBeatsEveryValue pins that Err wins over Values.
//
// It is what lets an adapter's test prove the miss-versus-error rule: with Err
// set, every Get fails, so a source that returned a cached value anyway would
// be caught.
func TestFakeErrorBeatsEveryValue(t *testing.T) {
	boom := errors.New("backend down")
	f := &cfgkittest.Fake{Values: map[string]string{"SET": "v"}, Err: boom}

	v, ok, err := f.Get("SET")
	if !errors.Is(err, boom) {
		t.Errorf("Get returned %v, want the configured error", err)
	}
	if v != "" || ok {
		t.Errorf("Get = %q, %t; a failing client must report neither value nor found", v, ok)
	}
	// The call is still recorded: a test asserting call counts must see
	// attempts that failed.
	if len(f.Calls) != 1 {
		t.Errorf("Calls = %v, want the failed attempt recorded", f.Calls)
	}
}

// conformingStructured is a correct StructuredSource: it merges the document
// onto the destination and leaves untouched whatever the document omits.
func conformingStructured(doc string) cfgkit.StructuredSource {
	return cfgkit.FromJSON([]byte(doc))
}

// failingSource reports a backend failure, which is what RunFailureTest exists
// to check an adapter surfaces rather than swallows.
type failingSource struct{}

func (failingSource) Name() string { return "failing" }
func (failingSource) Lookup(string) (string, bool, error) {
	return "", false, errors.New("backend unreachable")
}

// TestSuiteRunsInProcessAgainstCorrectSources executes all three suite
// entry points here rather than in a child.
//
// The subprocess tests above prove the suite REJECTS what it should, but their
// execution happens in a child whose coverage never reaches this profile —
// so without this the three exported functions read as 0% covered while being
// the most exercised code in the package. Running the conforming cases
// in-process makes the measurement honest, and costs nothing: a correct source
// passes, so this test stays green.
func TestSuiteRunsInProcessAgainstCorrectSources(t *testing.T) {
	t.Run("RunSourceTests", func(t *testing.T) {
		cfgkittest.RunSourceTests(t, func(v map[string]string) cfgkit.Source {
			return conforming{values: v}
		})
	})

	t.Run("RunFailureTest", func(t *testing.T) {
		cfgkittest.RunFailureTest(t, failingSource{})
	})

	t.Run("RunStructuredTests", func(t *testing.T) {
		cfgkittest.RunStructuredTests(t, `{"value":"v"}`, conformingStructured)
	})
}
