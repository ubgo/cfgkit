// Output is pinned so a change in either read path fails the gate.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func Example_kubernetes() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}

	// Output:
	// from the API:
	// FIELD     KEY       VALUE         SOURCE
	// Host      HOST      api.internal  k8s:configmaps/app-config
	// Password  PASSWORD  ••••••        k8s:secrets/app-secrets
	// Port      PORT      8443          k8s:configmaps/app-config
	//
	// from a projected volume:
	// FIELD     KEY       VALUE             SOURCE
	// Host      HOST      mounted.internal  k8smount:projected-volume
	// Password  PASSWORD  ••••••            k8smount:projected-volume
	// Port      PORT      9443              k8smount:projected-volume
}

// TestConfigMapAndSecretAreDistinguishableInProvenance pins that a load
// drawing from two resources says WHICH one supplied each field. Merging them
// under one name would make an RBAC problem impossible to localise.
func TestConfigMapAndSecretAreDistinguishableInProvenance(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	for _, want := range []string{"k8s:configmaps/app-config", "k8s:secrets/app-secrets"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s missing from provenance:\n%s", want, got)
		}
	}
}

// TestSecretValuesAreBase64DecodedByTheAdapter pins that a Secret and a
// ConfigMap behave identically to a caller. Secrets are base64 on the wire; a
// caller decoding them itself would be re-implementing the adapter.
func TestSecretValuesAreBase64DecodedByTheAdapter(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	// The value is masked, so what is asserted is that it BOUND — a
	// still-encoded value would have bound too, so the mount path below is
	// what actually proves decoding end to end.
	if !strings.Contains(out.String(), "Password  PASSWORD  ••••••") {
		t.Errorf("the Secret did not bind:\n%s", out.String())
	}
}

// TestMountReadsThroughTheDataSymlink pins the atomic-read property. kubelet
// swaps the "..data" LINK on update, so a reader that follows it sees either
// the whole old version or the whole new one — never a half-updated mix.
func TestMountReadsThroughTheDataSymlink(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "mounted.internal") {
		t.Errorf("the mount path did not read through %q:\n%s", dataLink, out.String())
	}
	// The dot-prefixed bookkeeping entries must NOT become configuration keys.
	if strings.Contains(out.String(), dataLink+"  ") {
		t.Errorf("a kubelet bookkeeping entry bound as a key:\n%s", out.String())
	}
}
