// Output is pinned so a change in the bucket read fails the gate. The suite
// starts a real nats-server in-process: NATS speaks its own wire protocol, so
// there is no HTTP transport to fake in front of.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func Example_natsKV() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// nats.internal:4222
	//
	// FIELD     KEY       VALUE          SOURCE
	// Host      HOST      nats.internal  nats:app-config
	// Password  PASSWORD  ••••••         nats:app-config
	// Port      PORT      4222           nats:app-config
}

// TestReadsFromARealServer is the point of the example: this is not a fake, so
// "reads a NATS KV bucket" is verified rather than claimed.
func TestReadsFromARealServer(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("the real server read failed: %v", err)
	}
	if !strings.Contains(out.String(), "nats.internal:4222") {
		t.Errorf("the bucket did not bind:\n%s", out.String())
	}
}

// TestSecretFromTheBucketIsMasked pins that a KV value is masked like any
// other source's.
func TestSecretFromTheBucketIsMasked(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out.String(), "s3cret-from-nats") {
		t.Errorf("the secret reached the output:\n%s", out.String())
	}
}
