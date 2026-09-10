// Output is pinned so a change in document handling or merging fails the gate.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func Example_s3() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// checkout on 0.0.0.0:9000
	//
	// FIELD        KEY      VALUE     SOURCE
	// Server.Host  HOST     0.0.0.0   default
	// Server.Port  PORT     9000      s3:acme-config/checkout/production.json
	// Service      SERVICE  checkout  s3:acme-config/checkout/production.json
	//
	// structured sources applied: s3:acme-config/checkout/production.json
}

// TestObjectIsADocumentNotAReplacement pins the merge rule: the object says
// nothing about server.host, and the compiled-in default survives. A source
// that replaced the struct would blank every field it omitted.
func TestObjectIsADocumentNotAReplacement(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "Server.Host  HOST     0.0.0.0   default") {
		t.Errorf("an omitted field was not left at its default:\n%s", out.String())
	}
}

// TestSourceNamesBucketAndKey pins that provenance identifies the object, not
// just "s3" — a program reading two objects needs to tell them apart.
func TestSourceNamesBucketAndKey(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "s3:"+bucket+"/"+key) {
		t.Errorf("the SOURCE column does not name the object:\n%s", out.String())
	}
}
