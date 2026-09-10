// Internal tests for defensive guards on unexported helpers.
//
// Every guard here is unreachable through the public API today, because each
// helper's current callers already filter to the shape it expects. That makes
// them dead code by measurement and load-bearing by intent: the moment a new
// caller forgets to filter, the guard is the difference between returning a
// zero value and panicking inside reflect.
//
// Testing them directly is the honest way to keep both properties — the guard
// stays, and it is exercised, so nobody deletes it later reading a coverage
// report. These tests live in package cfgkit because the helpers are
// unexported; every behavioural test is in cfgkit_test.
package cfgkit

import (
	"reflect"
	"testing"
)

// nonStructKinds are the shapes a future caller is most likely to pass by
// mistake — a field type reached without checking Kind first.
func nonStructKinds() map[string]reflect.Type {
	return map[string]reflect.Type{
		"string": reflect.TypeOf(""),
		"int":    reflect.TypeOf(0),
		"slice":  reflect.TypeOf([]string{}),
		"map":    reflect.TypeOf(map[string]string{}),
		"ptr":    reflect.TypeOf((*int)(nil)),
		"iface":  reflect.TypeOf((*error)(nil)).Elem(),
	}
}

// TestCheckShapeIgnoresNonStructs pins that the guard returns no errors rather
// than calling NumField, which panics on every kind below.
func TestCheckShapeIgnoresNonStructs(t *testing.T) {
	for name, typ := range nonStructKinds() {
		t.Run(name, func(t *testing.T) {
			if errs := checkShape(typ, "Path"); errs != nil {
				t.Errorf("checkShape(%s) = %v; want nil", name, errs)
			}
		})
	}
}

// TestTypeHasHookIgnoresNonStructs pins the same guard on the hook probe. A
// non-struct cannot declare Defaults, Derive or Validate in a way this package
// would call, so the answer is "no hook" rather than a panic.
func TestTypeHasHookIgnoresNonStructs(t *testing.T) {
	for name, typ := range nonStructKinds() {
		t.Run(name, func(t *testing.T) {
			if hook := typeHasHook(typ); hook != "" {
				t.Errorf("typeHasHook(%s) = %q; want \"\"", name, hook)
			}
		})
	}
}

// TestTypeWantsBindingIgnoresNonStructs pins the guard on the binding probe.
func TestTypeWantsBindingIgnoresNonStructs(t *testing.T) {
	for name, typ := range nonStructKinds() {
		t.Run(name, func(t *testing.T) {
			if typeWantsBinding(typ) {
				t.Errorf("typeWantsBinding(%s) = true; want false", name)
			}
		})
	}
}

// TestForEachStructSkipsUnwalkableValues pins that the walker returns without
// invoking the callback, rather than descending into something that is not a
// struct at all.
//
// The callback recording a visit is the assertion: a guard that let the value
// through would run it, and the count would not be zero.
func TestForEachStructSkipsUnwalkableValues(t *testing.T) {
	// claimFn answers "is this type claimed by a decoder", so a type that
	// claims everything is the strictest case: even then the guard must hold.
	always := func(reflect.Type) bool { return true }

	for name, typ := range nonStructKinds() {
		t.Run(name, func(t *testing.T) {
			visits := 0
			forEachStruct(reflect.New(typ).Elem(), always, func(reflect.Value) { visits++ })
			if visits != 0 {
				t.Errorf("forEachStruct visited %d values of kind %s; want 0", visits, name)
			}
		})
	}
}

// TestForEachStructVisitsAWalkableStruct is the control. Without it the test
// above would pass just as happily against a walker that never visits
// anything, which would prove nothing about the guard.
func TestForEachStructVisitsAWalkableStruct(t *testing.T) {
	type inner struct {
		A string `env:"A"`
	}
	type outer struct {
		B     string `env:"B"`
		Inner inner
	}

	visits := 0
	forEachStruct(reflect.ValueOf(&outer{}).Elem(), func(reflect.Type) bool { return false },
		func(reflect.Value) { visits++ })

	// Children before parents: the inner struct and the outer one.
	if visits != 2 {
		t.Errorf("forEachStruct visited %d structs; want 2 (inner, then outer)", visits)
	}
}

// TestMarkSourcedInitialisesItsMap pins the nil-map guard on a zero-value
// Result. Load always builds a Result with the map already made, so this arm
// only fires for a Result somebody constructed directly — and a nil map write
// panics, which is the failure the guard prevents.
func TestMarkSourcedInitialisesItsMap(t *testing.T) {
	var r Result // deliberately not built by Load: sourcedPaths is nil

	r.markSourced("Server.Port")

	if !r.sourcedPaths["Server.Port"] {
		t.Error("markSourced did not record the path on a zero-value Result")
	}
}

// TestMarkSourcedIsIdempotent pins that recording the same path twice is safe,
// which matters because a field can be claimed by several sources in turn and
// only the last one wins.
func TestMarkSourcedIsIdempotent(t *testing.T) {
	var r Result

	r.markSourced("Port")
	r.markSourced("Port")

	if len(r.sourcedPaths) != 1 {
		t.Errorf("sourcedPaths = %v; want exactly one entry", r.sourcedPaths)
	}
}
