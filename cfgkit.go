// Package cfgkit turns layered sources into a validated, typed Go struct.
//
// The property everything else hangs off: a complete valid configuration exists
// with ZERO input. Defaults live in Go and are compiled into the binary, so a
// program runs on a machine with no config file, no environment variable, and
// no tool installed. Every source is an override of a value that already exists.
//
// Sources come in two shapes, because config data does. Flat sources answer
// lookups by key (.env files, the process environment, a secret store);
// structured sources merge nested data onto the struct (JSON, and therefore Pkl
// or any other nested format). They cannot be collapsed into one: flattening
// {"hyperdx":{"logsSourceId":…}} yields a key that matches no environment
// variable, and deriving HYPERDX_LOGS_SOURCE_ID from it is impossible because
// "_" means both nesting and word break.
//
// cfgkit deliberately has no global state and no package-level Get. Load
// returns a value the caller owns. There is nowhere to put hidden state, which
// is a stronger guarantee than the discipline not to use it.
package cfgkit

import (
	"errors"
	"fmt"
	"os"
	"reflect"
)

// Mode selects one coherent, tested bundle of behaviour rather than exposing
// many independent booleans.
//
// Why one knob: each independent on/off flag creates a second code path that
// nobody exercises. Two modes mean two configurations that are actually run —
// and in a zero-config tool the lenient path is what every new user hits first,
// so it must be the well-tested one.
type Mode string

const (
	ModeDev  Mode = "dev"
	ModeTest Mode = "test"
	ModeProd Mode = "prod"
)

// Defaulter is implemented by any struct in the tree that wants to populate
// itself before binding.
//
// This is the primary defaults mechanism, preferred over `default:` tags,
// because it is typed and refactor-safe: a renamed field is a compile error
// rather than a silently dropped default. It may also compute, which a tag
// cannot.
type Defaulter interface {
	Defaults()
}

// Deriver fills fields computed from other fields. It runs after all sources
// are bound and before validation, so it may read any bound value and its
// output is validated like everything else.
type Deriver interface {
	Derive() error
}

// Validator reports every problem with a struct, joined.
//
// Returning early on the first failure would make a misconfigured deploy a
// guessing game of one fix per restart. Implementations should use errors.Join.
type Validator interface {
	Validate() error
}

// options holds one Load's configuration. It is unexported so the only way to
// build it is through the With* functions, which keeps the zero value valid.
type options struct {
	sources []any // Source or StructuredSource, in precedence order
	mode    Mode
	modeKey string
	reveal  bool

	// Capabilities (§11). Each may change what a value reads as; none may
	// change which source won — see capability.go for the firewall.
	decoders     []Decoder
	transformers []Transformer
	observers    []Observer

	// The conventional chain (chain.go). It is recorded rather than resolved
	// at option-apply time because it depends on the mode, which a later
	// option may still set.
	chain      bool
	chainDir   string
	chainExtra []any
	chainFiles []string
}

// Option configures a Load.
type Option func(*options)

// defaultModeKey is consulted when no mode is passed explicitly. APP_ENV is
// chosen over CONFIG_ENV because it describes the application's environment
// rather than the location of a config file.
const defaultModeKey = "APP_ENV"

// WithSources sets the ordered source list. A LATER source overrides an earlier
// one, regardless of whether it is flat or structured.
//
// Precedence is positional and explicit. There is no implicit ordering and no
// built-in default chain, because a convenience that hides precedence is the
// magic this package exists to avoid.
func WithSources(sources ...any) Option {
	return func(o *options) { o.sources = append(o.sources, sources...) }
}

// WithMode sets the mode explicitly, bypassing the environment lookup.
func WithMode(m Mode) Option {
	return func(o *options) { o.mode = m }
}

// WithModeKey changes which environment variable supplies the mode.
func WithModeKey(key string) Option {
	return func(o *options) { o.modeKey = key }
}

// Reveal allows secret values to appear in Explain output. It is a separate,
// explicit call so that a report is safe by default: without it, a secret's
// value is never placed into the output at all.
func Reveal() Option {
	return func(o *options) { o.reveal = true }
}

// Load builds a T from the configured sources.
//
// The pipeline is: defaults, then sources in order, then Derive, then Validate.
// It returns the config, a Result describing where every value came from, and
// an error joining every problem found — never just the first.
func Load[T any](opts ...Option) (*T, *Result, error) {
	o := &options{modeKey: defaultModeKey}
	for _, fn := range opts {
		fn(o)
	}
	applyChain(o)
	if o.mode == "" {
		o.mode = resolveMode(o.modeKey)
	}

	cfg := new(T)
	v := reflect.ValueOf(cfg).Elem()
	if v.Kind() != reflect.Struct {
		return nil, nil, fmt.Errorf("cfgkit: Load requires a struct type, got %s", v.Kind())
	}

	// Reject unreachable shapes before ANY phase runs. One of them makes the
	// defaults phase dereference a nil pointer, so this cannot wait for walk.
	if shapeErrs := checkShape(v.Type(), ""); len(shapeErrs) > 0 {
		return nil, nil, fmt.Errorf("cfgkit: %d problem(s):\n%w",
			countProblems(shapeErrs), errors.Join(shapeErrs...))
	}

	res := &Result{mode: o.mode, reveal: o.reveal, files: o.chainFiles,
		origins: map[string]string{}, sourcedPaths: map[string]bool{}}

	// 0. Allocate embedded pointers. Go PROMOTES the methods of an embedded
	// pointer, so a struct embedding a nil *T satisfies Defaulter whenever T
	// does — and calling the promoted method dereferences the nil pointer.
	// Allocating first is also the correct semantics: an embedded pointer is
	// part of the outer type's identity, unlike a NAMED *Struct field, which is
	// the "this feature is absent" idiom and stays nil until a source speaks.
	allocEmbedded(v, o.claims())

	// 1. Defaults, depth-first, so a parent's Defaults may overwrite a child's.
	applyDefaults(v, o.claims())

	// 2. Structured sources merge onto the struct; flat sources are collected
	// for the binding pass. Both are consulted in list order, so a later source
	// wins either way.
	flat := make([]Source, 0, len(o.sources))
	for _, s := range o.sources {
		switch src := s.(type) {
		case StructuredSource:
			// Snapshot before and after so whatever the source changed can be
			// attributed to it — the only way to recover provenance from an
			// Apply that reports nothing.
			before := snapshotLeaves(v, o.claims())
			if err := src.Apply(cfg); err != nil {
				return nil, nil, &SourceError{Source: src.Name(), Err: err}
			}
			attributeChanges(before, snapshotLeaves(v, o.claims()), src.Name(), res)
			res.structured = append(res.structured, src.Name())
		case Source:
			flat = append(flat, src)
		default:
			return nil, nil, fmt.Errorf("cfgkit: %T is neither a Source nor a StructuredSource", s)
		}
	}

	// 3. Bind every leaf, recording provenance as we go.
	fields, sections := walk(v, "", "", o.claims())
	errs := bind(fields, flat, res, o.mode, o)

	// 3b. An optional section nothing sourced goes back to nil (§6.4), so
	// `cfg.SMTP != nil` means what a Go programmer expects. This runs BEFORE
	// derive and validate, so neither sees a section the operator did not ask
	// for — and a half-configured section still reports its own missing field.
	pruneEmptySections(sections, res)

	// 3c. Report keys a listable source supplied that no field wanted — the
	// mirror of Explain: not "where did this value come from" but "why did my
	// value go nowhere". Advisory only; see UnknownKey.
	res.unknown = findUnknownKeys(flat, fields)

	// 4. Derive, then 5. Validate — both depth-first, children before parents,
	// so a parent sees fully derived and validated children.
	if err := runDerive(v, o.claims()); err != nil {
		errs = append(errs, err)
	}
	if err := runValidate(v, o.claims()); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return nil, res, fmt.Errorf("cfgkit: %d problem(s):\n%w",
			countProblems(errs), errors.Join(errs...))
	}
	return cfg, res, nil
}

// Check runs the whole pipeline and reports problems without returning the
// configuration.
//
// It exists so config errors fail a pipeline instead of a production boot. In
// both projects this package was written for, a stale environment file
// surfaced as a panic at container start — after deploy, and before
// observability existed to record it.
func Check[T any](opts ...Option) error {
	_, _, err := Load[T](opts...)
	return err
}

// resolveMode reads the mode from the environment, defaulting to dev.
//
// Dev is the default because an unset mode means a developer's machine far more
// often than it means production, and every prod-only strictness rule fails
// closed anyway.
func resolveMode(key string) Mode { return parseMode(os.Getenv(key)) }

// applyDefaults calls Defaults on every struct in the tree, children first.
func applyDefaults(v reflect.Value, claimed claimFn) {
	forEachStruct(v, claimed, func(sv reflect.Value) {
		if d, ok := addrOf(sv).(Defaulter); ok {
			d.Defaults()
		}
	})
}

// runDerive calls Derive on every struct in the tree, children first, and joins
// the failures so one run reports all of them.
func runDerive(v reflect.Value, claimed claimFn) error {
	var errs []error
	forEachStruct(v, claimed, func(sv reflect.Value) {
		if d, ok := addrOf(sv).(Deriver); ok {
			if err := d.Derive(); err != nil {
				errs = append(errs, err)
			}
		}
	})
	return errors.Join(errs...)
}

// runValidate calls Validate on every struct in the tree, children first.
func runValidate(v reflect.Value, claimed claimFn) error {
	var errs []error
	forEachStruct(v, claimed, func(sv reflect.Value) {
		if val, ok := addrOf(sv).(Validator); ok {
			if err := val.Validate(); err != nil {
				errs = append(errs, err)
			}
		}
	})
	return errors.Join(errs...)
}

// allocEmbedded allocates every nil embedded pointer in the tree, so that the
// methods Go promotes from them can be called without dereferencing nil.
//
// Only ANONYMOUS pointers are touched. A named *Struct field is an optional
// section: walk allocates it so its children are addressable, and prunes it
// again when no source set anything beneath (§6.4). Allocating those here would
// not change the outcome but would blur two rules that should stay distinct.
func allocEmbedded(v reflect.Value, claimed claimFn) {
	if !isWalkable(v, claimed) {
		return
	}
	t := v.Type()
	for i := range t.NumField() {
		sf := t.Field(i)
		fv := v.Field(i)
		if !visitable(sf, fv) {
			continue
		}
		if fv.Kind() == reflect.Pointer {
			if fv.IsNil() {
				// An unexported embedded pointer cannot be set at all; walk
				// reports that shape rather than leaving it silently empty.
				if !sf.Anonymous || !fv.CanSet() {
					continue
				}
				fv.Set(reflect.New(fv.Type().Elem()))
			}
			fv = fv.Elem()
		}
		allocEmbedded(fv, claimed)
	}
}

// forEachStruct visits every struct in the tree depth-first, children before
// parents, so a parent's hook observes its children's results.
func forEachStruct(v reflect.Value, claimed claimFn, fn func(reflect.Value)) {
	if !isWalkable(v, claimed) {
		return
	}
	t := v.Type()
	for i := range t.NumField() {
		fv := v.Field(i)
		if !visitable(t.Field(i), fv) {
			continue
		}
		if fv.Kind() == reflect.Pointer {
			if fv.IsNil() {
				continue
			}
			fv = fv.Elem()
		}
		if isWalkable(fv, claimed) {
			forEachStruct(fv, claimed, fn)
		}
	}
	fn(v)
}

// addrOf returns an addressable pointer to v for interface assertions, or nil
// when v cannot be turned into an interface at all.
//
// CanInterface is checked as well as CanAddr because reflect refuses to produce
// an interface for a value reached through an unexported field — which is every
// struct beneath an embedded unexported one. Calling Interface there panics, so
// the hooks CANNOT run for such a struct. walk reports that shape as an error
// rather than letting a Validate silently not run; this check is the mechanical
// half of the same rule.
func addrOf(v reflect.Value) any {
	if !v.CanAddr() || !v.CanInterface() {
		return nil
	}
	return v.Addr().Interface()
}

// countProblems counts LEAF errors, flattening errors.Join trees.
//
// Validate is documented to report every problem joined, so a struct with two
// bad fields arrives here as ONE error containing two. Counting the slice
// would print "1 problem(s)" above two lines — and a reader who trusts the
// header, fixes the single problem it promised and redeploys gets a second
// failed rollout. The header has to agree with the list beneath it.
func countProblems(errs []error) int {
	n := 0
	var walk func(error)
	walk = func(err error) {
		if err == nil {
			return
		}
		// errors.Join's own type exposes exactly this shape, and so does any
		// caller that joins its own problems.
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, sub := range joined.Unwrap() {
				walk(sub)
			}
			return
		}
		n++
	}
	for _, err := range errs {
		walk(err)
	}
	return n
}
