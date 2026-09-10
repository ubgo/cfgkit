// Output is pinned so a change in paging, name trimming or decryption fails
// the gate rather than making the README wrong.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func Example_parameterStore() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// ssm.internal:8443 origins=[https://acme.test https://www.acme.test]
	//
	// parameters that matched no field: 10
	//
	// FIELD     KEY       VALUE                                    SOURCE
	// Host      HOST      ssm.internal                             ssm:/acme/checkout
	// Origins   ORIGINS   https://acme.test,https://www.acme.test  ssm:/acme/checkout
	// Password  PASSWORD  ••••••                                   ssm:/acme/checkout
	// Port      PORT      8443                                     ssm:/acme/checkout
}

// TestPagingIsNotOptional is the failure this example exists to prevent.
//
// AWS returns at most 10 parameters per call. A configuration of eleven
// silently loses one if the second page is never fetched — no error, just a
// field quietly on its default. The fixture holds more than one page, and the
// filler parameters can only appear as unknown keys if page two was read.
func TestPagingIsNotOptional(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "parameters that matched no field: 10") {
		t.Errorf("the second page was not fetched:\n%s", out.String())
	}
}

// TestNamesArriveRelativeToThePath pins that the path prefix is trimmed, which
// is what lets the same struct bind from SSM and from a .env file.
func TestNamesArriveRelativeToThePath(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out.String(), path+"HOST") {
		t.Errorf("the full parameter name leaked into binding:\n%s", out.String())
	}
}

// TestStringListNeedsNoSpecialHandling pins that AWS's own list type composes
// with the delim tag: it arrives comma-joined, which is what delim expects.
func TestStringListNeedsNoSpecialHandling(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "origins=[https://acme.test https://www.acme.test]") {
		t.Errorf("the StringList did not split into a slice:\n%s", out.String())
	}
}
