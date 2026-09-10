// Embedded structs — how a project extends a framework's configuration.
//
//	type MyConfig struct {
//		volt.Config                                   // everything volt defines
//		Stripe StripeConfig `env:",prefix=STRIPE_"`   // everything I define
//	}
//
// One Load, one struct, one Explain, and the framework's config file is never
// edited — which is what makes upgrading it a version bump rather than a merge.
//
// This file exists because the unexported case was SILENTLY BROKEN: an embedded
// struct of an unexported type was skipped entirely, so PORT=9000 was accepted,
// ignored, and reported nowhere — while FromJSON filled the very same field,
// because encoding/json binds it. Three separate traversals each carried their
// own copy of the skip rule, which is why fixing one was not enough.
package cfgkit_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/cfgkit"
)

// frameworkConfig stands in for a framework's own config type, UNEXPORTED and
// carrying no hooks — the shape that was silently skipped.
type frameworkConfig struct {
	Port int    `env:"PORT" default:"8080"`
	DB   string `env:"DATABASE_URL" default:"postgres://localhost/dev"`

	// Private state a config struct may legitimately hold. It must still be
	// skipped: reflect genuinely cannot set it.
	initialised bool
}

// FrameworkConfig is the real framework shape: EXPORTED, so a consumer in
// another package can name it — which they must, or they could not embed it at
// all. Hooks work here, which is why this is the shape to recommend.
type FrameworkConfig struct {
	Port int    `env:"PORT" default:"8080"`
	DB   string `env:"DATABASE_URL" default:"postgres://localhost/dev"`

	Initialised bool `env:"-"`
}

func (f *FrameworkConfig) Defaults()       { f.Initialised = true }
func (f *FrameworkConfig) Validate() error { return cfgkit.Required("DB", f.DB) }

type projectConfig struct {
	frameworkConfig // the framework's fields, at the top level

	Stripe struct {
		Key string `env:"KEY"`
	} `env:",prefix=STRIPE_"`
}

// TestEmbeddedUnexportedStructBinds is the regression test. Before the fix this
// returned Port=0 with no error and no provenance entry at all.
func TestEmbeddedUnexportedStructBinds(t *testing.T) {
	cfg, res, err := cfgkit.Load[projectConfig](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"PORT": "9000", "STRIPE_KEY": "sk_x"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9000 {
		t.Errorf("Port = %d, want 9000 — an embedded unexported struct must bind", cfg.Port)
	}
	if cfg.DB != "postgres://localhost/dev" {
		t.Errorf("DB = %q, want the framework's default", cfg.DB)
	}
	if cfg.Stripe.Key != "sk_x" {
		t.Errorf("Stripe.Key = %q", cfg.Stripe.Key)
	}

	// Field paths are FLAT: the framework's fields appear as if declared on the
	// outer struct, matching how Go itself promotes them.
	paths := map[string]string{}
	for _, f := range res.Fields() {
		paths[f.Path] = f.Key
	}
	for path, key := range map[string]string{
		"Port": "PORT", "DB": "DATABASE_URL", "Stripe.Key": "STRIPE_KEY",
	} {
		if paths[path] != key {
			t.Errorf("field %s bound to %q, want %q (all paths: %v)", path, paths[path], key, paths)
		}
	}
	if len(paths) != 3 {
		t.Errorf("bound %d fields (%v), want exactly 3 — private state must stay skipped", len(paths), paths)
	}
	if cfg.initialised {
		t.Error("initialised was set, but this type has no Defaults hook")
	}
}

// TestEmbeddedExportedStructBinds pins the exported spelling, which worked
// before, so the fix cannot have regressed it.
func TestEmbeddedExportedStructBinds(t *testing.T) {
	type outer struct {
		FrameworkConfig
		Extra string `env:"EXTRA" default:"x"`
	}
	cfg, _, err := cfgkit.Load[outer](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"PORT": "9000"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9000 || cfg.Extra != "x" {
		t.Errorf("cfg = %+v", cfg)
	}
}

// TestEmbeddedExportedHooksRun pins the framework case that matters: a project
// embeds the framework's EXPORTED config type, and the framework's own Defaults
// and Validate run as part of the project's single Load. That is what lets a
// framework enforce its invariants without the project doing anything.
func TestEmbeddedExportedHooksRun(t *testing.T) {
	type outer struct {
		FrameworkConfig
		Stripe string `env:"STRIPE_KEY" default:"sk_test"`
	}

	t.Run("Defaults runs", func(t *testing.T) {
		cfg, _, err := cfgkit.Load[outer]()
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.Initialised {
			t.Error("the embedded framework's Defaults() never ran")
		}
	})

	t.Run("Validate runs", func(t *testing.T) {
		err := cfgkit.Check[outer](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"DATABASE_URL": ""}),
		))
		if err == nil {
			t.Fatal("the embedded framework's Validate() never ran")
		}
		if !strings.Contains(err.Error(), "DB") {
			t.Errorf("error %q should name the framework's field", err)
		}
	})
}

// hookedButUnexported has a hook AND is unexported, which Go makes impossible
// to honour: reflect refuses to produce an interface for a value reached
// through an unexported embedded field, so the method can never be called.
type hookedButUnexported struct {
	Port int `env:"PORT"`
}

func (h *hookedButUnexported) Validate() error { return nil }

// TestEmbeddedUnexportedHookIsAnError pins that this fails loudly.
//
// It is the alternative to two worse outcomes, both of which the code did at
// some point during this work: panicking inside reflect, or quietly not running
// a framework's Validate — the method whose entire job is to refuse a bad
// configuration.
func TestEmbeddedUnexportedHookIsAnError(t *testing.T) {
	type outer struct {
		hookedButUnexported
	}
	err := cfgkit.Check[outer](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"PORT": "9000"}),
	))
	if err == nil {
		t.Fatal("want an error: Validate() can never run on this shape")
	}

	var uhe *cfgkit.UnreachableHookError
	if !errors.As(err, &uhe) {
		t.Fatalf("want an *UnreachableHookError, got %T: %v", err, err)
	}
	if uhe.Hook != "Validate" {
		t.Errorf("Hook = %q, want Validate", uhe.Hook)
	}
	if !strings.Contains(err.Error(), "export the type") {
		t.Errorf("error %q must state the remedy", err)
	}
}

// TestEmbeddedUnexportedNestedHookIsAlsoReported pins that the restriction is
// INHERITED: every struct beneath an embedded unexported one is equally
// unreachable, so a nested section's Validate is just as silently dead.
func TestEmbeddedUnexportedNestedHookIsAlsoReported(t *testing.T) {
	type outer struct {
		nestingUnexported
	}
	err := cfgkit.Check[outer]()
	if err == nil {
		t.Fatal("want an error: the nested Validate() can never run either")
	}
	var uhe *cfgkit.UnreachableHookError
	if !errors.As(err, &uhe) {
		t.Fatalf("want an *UnreachableHookError, got %T: %v", err, err)
	}
}

// TestEmbeddedStructuredSourceAgrees is the consistency the fix restores.
//
// encoding/json fills the promoted fields of an embedded unexported struct, so
// before the fix FromJSON could set a field that PORT= could not. The two
// source kinds must agree about which fields exist.
func TestEmbeddedStructuredSourceAgrees(t *testing.T) {
	type inner struct {
		Port int `env:"PORT" json:"port" default:"8080"`
	}
	type outer struct {
		inner
	}

	fromJSON, _, err := cfgkit.Load[outer](cfgkit.WithSources(
		cfgkit.FromJSON([]byte(`{"port":9000}`)),
	))
	if err != nil {
		t.Fatal(err)
	}
	fromFlat, _, err := cfgkit.Load[outer](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"PORT": "9000"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if fromJSON.Port != fromFlat.Port {
		t.Errorf("structured gave %d, flat gave %d — the two source kinds must agree",
			fromJSON.Port, fromFlat.Port)
	}
}

// TestEmbeddedStructuredProvenance pins the third traversal, collectLeaves,
// which attributes a structured source's changes. It had its own copy of the
// skip rule too, so a JSON-set embedded field would have been bound but
// attributed to "default" — an Explain that lies.
func TestEmbeddedStructuredProvenance(t *testing.T) {
	type inner struct {
		Port int `env:"PORT" json:"port" default:"8080"`
	}
	type outer struct {
		inner
	}

	_, res, err := cfgkit.Load[outer](cfgkit.WithSources(
		cfgkit.FromJSON([]byte(`{"port":9000}`)),
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Fields() {
		if f.Path == "Port" && f.Source != "json" {
			t.Errorf("Port source = %q, want json — the structured source did set it", f.Source)
		}
	}
}

// TestEmbeddedPrefixApplies pins that an embedded struct can still carry a
// prefix, so a framework's keys can be namespaced by the project embedding it.
func TestEmbeddedPrefixApplies(t *testing.T) {
	type inner struct {
		Port int `env:"PORT"`
	}
	type outer struct {
		inner `env:",prefix=APP_"`
	}
	cfg, _, err := cfgkit.Load[outer](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"APP_PORT": "9000", "PORT": "1"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9000 {
		t.Errorf("Port = %d, want the prefixed key to win", cfg.Port)
	}
}

// nestingUnexported holds a section whose type has a hook, one level down.
type nestingUnexported struct {
	Sub hookedSection
}

type hookedSection struct {
	Port int `env:"SUB_PORT"`
}

func (h *hookedSection) Validate() error { return nil }

// TestEmbeddedPointerToExportedTypeWorks pins the allocatable pointer case,
// which is the one an embedded pointer normally is.
func TestEmbeddedPointerToExportedTypeWorks(t *testing.T) {
	type outer struct {
		*FrameworkConfig
	}
	cfg, _, err := cfgkit.Load[outer](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"PORT": "9000"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FrameworkConfig == nil || cfg.Port != 9000 {
		t.Errorf("cfg = %+v, want the embedded pointer allocated and bound", cfg)
	}
}

// unreachable is embedded BY POINTER below. reflect refuses to set a pointer
// field of an unexported type, so cfgkit cannot allocate it and nothing beneath
// it can ever be filled.
type unreachable struct {
	Port int `env:"PORT"`
}

// TestEmbeddedPointerToUnexportedTypeIsAnError pins the one shape that cannot
// work. It must FAIL rather than bind nothing: silently accepting PORT= and
// doing nothing with it is the exact failure this whole file exists to remove.
func TestEmbeddedPointerToUnexportedTypeIsAnError(t *testing.T) {
	type outer struct {
		*unreachable
	}
	err := cfgkit.Check[outer](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"PORT": "9000"}),
	))
	if err == nil {
		t.Fatal("want an error: no source could ever fill these fields")
	}

	var ufe *cfgkit.UnreachableFieldError
	if !errors.As(err, &ufe) {
		t.Fatalf("want an *UnreachableFieldError, got %T: %v", err, err)
	}
	// The message must name the shape AND the remedy, or a reader has a
	// diagnosis with no cure.
	for _, want := range []string{"unreachable", "embed it by value", "export the type"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// TestEmbeddedPointerToUnexportedNonConfigIsFine keeps the error honest.
// Embedding an unexported pointer to something that is not configuration at all
// is nobody's mistake, and reporting it would be noise.
func TestEmbeddedPointerToUnexportedNonConfigIsFine(t *testing.T) {
	type notConfig struct {
		Cache map[string]string // no env or json tag: not configuration
	}
	type outer struct {
		*notConfig
		Port int `env:"PORT" default:"8080"`
	}
	cfg, _, err := cfgkit.Load[outer]()
	if err != nil {
		t.Fatalf("embedding a non-config pointer must not be an error: %v", err)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want the default", cfg.Port)
	}
}

// TestPlainUnexportedFieldsAreStillSkipped is the other half of the rule. Only
// EMBEDDED structs are the exception; a plain unexported field genuinely cannot
// be set by reflect and must stay invisible.
func TestPlainUnexportedFieldsAreStillSkipped(t *testing.T) {
	type outer struct {
		Port    int `env:"PORT" default:"8080"`
		private string
		nested  struct {
			Inner string `env:"INNER"`
		}
	}
	cfg, res, err := cfgkit.Load[outer](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"PORT": "9000", "INNER": "x"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Fields() {
		if f.Path != "Port" {
			t.Errorf("field %q was bound; only exported fields and embedded structs may be", f.Path)
		}
	}
	// Absent from provenance is not the same as untouched. INNER=x was
	// supplied on purpose: if the binder reached the unexported nested struct
	// it would land here, and only reading the value can tell.
	if cfg.private != "" {
		t.Errorf("unexported field was written: %q", cfg.private)
	}
	if cfg.nested.Inner != "" {
		t.Errorf("field inside an unexported struct was written: %q", cfg.nested.Inner)
	}
}

// NilPtrHook is embedded BY POINTER below. Go promotes its methods to the outer
// struct, so the outer struct satisfies Defaulter even while the pointer is nil.
type NilPtrHook struct {
	Port int  `env:"PORT" default:"8080"`
	Seen bool `env:"-"`
}

func (n *NilPtrHook) Defaults() { n.Seen = true }

// TestEmbeddedNilPointerHookDoesNotPanic is a regression test for a crash that
// predates this work.
//
// Go PROMOTES the methods of an embedded pointer, so `outer` satisfies
// Defaulter whenever nilPtrHook does — and the promoted call dereferences the
// pointer. Defaults ran before anything allocated it, so Load segfaulted on a
// struct shape that compiles and looks entirely ordinary.
func TestEmbeddedNilPointerHookDoesNotPanic(t *testing.T) {
	type outer struct {
		*NilPtrHook
		Extra string `env:"EXTRA" default:"x"`
	}

	cfg, _, err := cfgkit.Load[outer]()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NilPtrHook == nil {
		t.Fatal("the embedded pointer was left nil; its promoted methods would panic")
	}
	if !cfg.Seen {
		t.Error("the promoted Defaults() never ran")
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want the default bound through the embedded pointer", cfg.Port)
	}
}

// TestNamedPointerSectionStillPrunes pins that allocating EMBEDDED pointers did
// not turn every optional section on. A named *Struct is the "feature absent"
// idiom and must still come back nil.
func TestNamedPointerSectionStillPrunes(t *testing.T) {
	type smtp struct {
		Host string `env:"HOST"`
	}
	type outer struct {
		SMTP *smtp `env:",prefix=SMTP_"`
		Port int   `env:"PORT" default:"8080"`
	}

	cfg, _, err := cfgkit.Load[outer]()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SMTP != nil {
		t.Errorf("SMTP = %+v, want nil — a named section is not an embedded one", cfg.SMTP)
	}
}

// TestUnreachableDetectionEdges covers the branches that decide whether a shape
// is worth reporting. Each is a case where reporting or not reporting would be
// wrong, so none of them is incidental coverage.
func TestUnreachableDetectionEdges(t *testing.T) {
	t.Run("json-tagged fields count as wanting binding", func(t *testing.T) {
		// A structured source fills by json tag alone, so a type with only
		// json tags is still configuration whose fields would be unreachable.
		type jsonOnly struct {
			Port int `json:"port"`
		}
		type outer struct{ *jsonOnly }
		if err := cfgkit.Check[outer](); err == nil {
			t.Error("want an error: json-tagged fields are configuration too")
		}
	})

	t.Run("a nested tagged struct counts", func(t *testing.T) {
		// The tags are one level down, so the shallow check alone would miss it.
		type deep struct {
			Port int `env:"DEEP_PORT"`
		}
		type middle struct{ Deep deep }
		type outer struct{ *middle }
		if err := cfgkit.Check[outer](); err == nil {
			t.Error("want an error: a nested tagged struct is still unreachable")
		}
	})

	t.Run("time.Time is not descended into", func(t *testing.T) {
		// time.Time is a struct full of unexported fields. Treating it as a
		// container to search would be wrong and slow.
		type withTime struct {
			At time.Time `env:"AT"`
		}
		type outer struct {
			withTime
			Port int `env:"PORT" default:"8080"`
		}
		if err := cfgkit.Check[outer](); err != nil {
			t.Errorf("time.Time must not trip the shape check: %v", err)
		}
	})

	t.Run("a hook on the value type is found too", func(t *testing.T) {
		// typeHasHook checks both T and *T: a Validate declared on the value
		// receiver is just as unreachable.
		type outer struct{ valueHook }
		if err := cfgkit.Check[outer](); err == nil {
			t.Error("want an error for a value-receiver hook")
		}
	})

	t.Run("exported embedded types are never reported", func(t *testing.T) {
		type outer struct {
			FrameworkConfig
			Port int `env:"PORT" default:"8080"`
		}
		if err := cfgkit.Check[outer](); err != nil {
			t.Errorf("an exported embedded type must be fine: %v", err)
		}
	})
}

// valueHook declares its hook on the VALUE receiver, so the type itself
// implements Validator rather than only its pointer.
type valueHook struct {
	Port int `env:"VH_PORT"`
}

func (valueHook) Validate() error { return nil }
