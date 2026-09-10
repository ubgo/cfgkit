package cfgkit

import (
	"reflect"
	"sort"
	"strings"
)

// Struct tags, all of which are OPTIONAL except env on a bound leaf.
//
// The mapping from key to field is DECLARED, never inferred. Splitting a key on
// "_" cannot work: HYPERDX_LOGS_SOURCE_ID has at least six valid readings
// (HyperDX.Logs.Source.ID, HyperDX.LogsSourceID, …) because "_" separates both
// nesting levels and words inside one name. Inference also breaks renames — it
// would silently change which variable feeds a field when a Go identifier is
// renamed, which compiles, passes tests, and fails in production only.
const (
	tagEnv     = "env"     // the flat key, or ",prefix=P_" on a struct field
	tagJSON    = "json"    // the nested path for structured sources
	tagDefault = "default" // a default value, parsed by the field's decoder
	tagSecret  = "secret"  // mask in Explain and every diagnostic surface
	tagDelim   = "delim"   // slice element / map entry separator, default ","
	tagKVDelim = "kvdelim" // map key-to-value separator, default ":"
	tagFlag    = "flag"    // the command-line flag that may set this field
	tagWas     = "was"     // former key name(s), comma separated
	tagDoc     = "doc"     // one-line description, used by Document
)

// env tag options, appended after the key as ",option".
const (
	optPrefix   = "prefix="  // on a struct field: prepend to every key beneath
	optFile     = "file"     // the value is a path; read the file at it
	optUnset    = "unset"    // remove the key from the environment after reading
	optRequired = "required" // the key must resolve
	optNotEmpty = "notempty" // the key must resolve to a non-empty value
	optInit     = "init"     // on a struct pointer: always allocate it (§6.4)
)

// skipKey marks a field that is never bound from any source. It is set by
// Derive, or left at its default.
const skipKey = "-"

// section is an optional *Struct field, tracked so it can be set back to nil
// when no source turned out to set anything beneath it.
//
// A nil section is how Go already says "this feature is absent", and it is the
// check every Go programmer writes without being told:
//
//	if cfg.SMTP != nil { startEmailSender(cfg.SMTP) }
//
// Allocating unconditionally would make that check always true, so an app would
// start an email sender pointed at an empty host and fail on the first send,
// far from the cause.
type section struct {
	// Ptr is the pointer field itself, so it can be zeroed.
	Ptr reflect.Value
	// Fields are the leaves beneath it, used to ask whether a source set any.
	Fields []*field
	// Forced records the `init` option: allocate regardless.
	Forced bool
}

// field is one bound leaf of the destination struct, resolved once per Load.
//
// Path is the Go path ("Server.Port") and Key is the flat key ("PORT"). They are
// deliberately independent: Path is what a developer reads in an error, Key is
// what an operator sets in a deployment, and neither is derived from the other.
type field struct {
	Path string
	Key  string
	Was  []string
	Flag string
	Doc  string

	Delim   string
	KVDelim string
	Default string
	HasDef  bool

	Secret   bool
	File     bool
	Unset    bool
	Required bool
	NotEmpty bool

	// Value is the settable destination. It is valid only for the duration of
	// the Load that produced it.
	Value reflect.Value
}

// delims returns the separators this field's compound values are split on,
// with the package defaults substituted for anything the tags left empty.
func (f *field) delims() delims { return newDelims(f.Delim, f.KVDelim) }

// walk collects every bindable leaf of v, depth-first, accumulating key
// prefixes down the tree.
//
// It returns leaves only: a nested struct contributes its prefix and its
// children, never itself. A struct that implements encoding.TextUnmarshaler is
// treated as a LEAF, because its own parser owns the whole string — that is why
// time.Time and url.URL bind from one key rather than being walked into.
func walk(v reflect.Value, path, prefix string, claimed claimFn) ([]*field, []*section) {
	var out []*field
	var sections []*section

	t := v.Type()
	for i := range t.NumField() {
		sf := t.Field(i)
		fv := v.Field(i)

		// Unexported fields are skipped rather than reported: a config struct
		// may legitimately hold private state, and reflect cannot set it.
		//
		// An EMBEDDED unexported struct is the exception, and it is not an
		// edge case — it is how a package exposes a framework's configuration
		// without exporting the type itself. Go's reflect can set the exported
		// fields inside one (only the container is off limits), and
		// encoding/json binds them, so skipping them here would make the two
		// source kinds disagree about which fields exist: FromJSON would fill
		// a field that PORT= could not, with nothing reported either way.
		if !visitable(sf, fv) {
			continue
		}

		envTag, hasEnv := sf.Tag.Lookup(tagEnv)
		key, opts := splitTag(envTag)
		if key == skipKey {
			continue
		}

		childPath := sf.Name
		if path != "" {
			childPath = path + "." + sf.Name
		}

		// A struct field carries a prefix down to its children instead of
		// binding itself. Embedded structs are walked with the SAME path, so a
		// consumer that embeds a framework's Config sees flat field paths.
		if isWalkable(fv, claimed) {
			childPrefix := prefix + optionValue(opts, optPrefix)
			target := fv
			if target.Kind() == reflect.Pointer {
				if target.IsNil() {
					// checkShape has already rejected the pointer that cannot
					// be allocated, so reaching here with an unsettable one is
					// impossible; skipping keeps walk total regardless.
					if !target.CanSet() {
						continue
					}
					target.Set(reflect.New(target.Type().Elem()))
				}
				target = target.Elem()
			}
			nested := childPath
			if sf.Anonymous {
				nested = path
			}
			kids, subSections := walk(target, nested, childPrefix, claimed)
			out = append(out, kids...)
			sections = append(sections, subSections...)

			// A pointer section is a candidate for pruning. It was allocated
			// above so its children are addressable; whether it survives is
			// decided after binding, once we know what a source actually set.
			if fv.Kind() == reflect.Pointer && !sf.Anonymous {
				sections = append(sections, &section{
					Ptr:    fv,
					Fields: kids,
					Forced: hasOption(opts, optInit),
				})
			}
			continue
		}

		// A leaf with no env tag is not bound from a flat source. It may still
		// be filled by a structured source or by Derive, so it is not an error.
		if !hasEnv || key == "" {
			continue
		}

		out = append(out, newField(sf, fv, childPath, prefix+key, opts))
	}

	return out, sections
}

// checkShape rejects struct shapes whose fields or hooks no source could ever
// reach, BEFORE any phase runs.
//
// It works on types alone, for two reasons. It must run before defaults, since
// one of the shapes it rejects makes the defaults phase dereference a nil
// pointer. And a nil pointer has no value to inspect — only a type.
//
// Both shapes come from the same language rule: reflect refuses to write
// through, or take an interface from, anything reached via an unexported field.
// An embedded struct is a partial exception — its fields are writable, its
// methods are not — which is why the two errors are different.
func checkShape(t reflect.Type, path string) []error {
	if t.Kind() != reflect.Struct {
		return nil
	}
	var errs []error
	for i := range t.NumField() {
		sf := t.Field(i)
		childPath := sf.Name
		if path != "" {
			childPath = path + "." + sf.Name
		}

		inner := sf.Type
		isPtr := inner.Kind() == reflect.Pointer
		if isPtr {
			inner = inner.Elem()
		}
		if inner.Kind() != reflect.Struct || inner == timeType {
			continue
		}

		if sf.Anonymous && !sf.IsExported() {
			switch {
			case isPtr:
				// The pointer itself would have to be set to allocate it, and
				// reflect refuses. Nothing beneath it can ever be filled.
				if typeWantsBinding(inner) || typeHasHook(inner) != "" {
					errs = append(errs, &UnreachableFieldError{
						Path: childPath, Type: sf.Type.String(),
					})
					continue
				}
			default:
				// Fields bind — reflect can set them, and encoding/json does
				// too — but methods cannot be called.
				if hook := typeHasHook(inner); hook != "" {
					errs = append(errs, &UnreachableHookError{
						Path: childPath, Type: sf.Type.String(), Hook: hook,
					})
				}
			}
		}

		if !sf.IsExported() && !sf.Anonymous {
			continue // a plain unexported field is invisible; nothing to check
		}
		errs = append(errs, checkShape(inner, childPath)...)
	}
	return errs
}

// The hook interfaces, as types, so a struct can be asked whether it implements
// one WITHOUT having to produce an interface value for it — which is precisely
// what is impossible for a value reached through an unexported field.
var hookTypes = []struct {
	name string
	typ  reflect.Type
}{
	{"Defaults", reflect.TypeOf((*Defaulter)(nil)).Elem()},
	{"Derive", reflect.TypeOf((*Deriver)(nil)).Elem()},
	{"Validate", reflect.TypeOf((*Validator)(nil)).Elem()},
}

// typeHasHook names the first hook t or anything beneath it implements, or "".
//
// The search is recursive because the restriction is inherited: every struct
// under an embedded unexported one is equally unreachable, so a nested section's
// Validate would be just as silently dead as the embedded type's own.
func typeHasHook(t reflect.Type) string {
	if t.Kind() != reflect.Struct {
		return ""
	}
	ptr := reflect.PointerTo(t)
	for _, h := range hookTypes {
		if ptr.Implements(h.typ) || t.Implements(h.typ) {
			return h.name
		}
	}
	for i := range t.NumField() {
		inner := t.Field(i).Type
		if inner.Kind() == reflect.Pointer {
			inner = inner.Elem()
		}
		if inner.Kind() == reflect.Struct && inner != timeType {
			if hook := typeHasHook(inner); hook != "" {
				return hook
			}
		}
	}
	return ""
}

// typeWantsBinding reports whether a type has any field that a source could
// fill, looking at the TYPE alone so it can be asked about a nil pointer.
//
// It is what keeps the unreachable-field error honest: embedding an unexported
// pointer to something that is not configuration at all is nobody's mistake,
// and reporting it would be noise.
func typeWantsBinding(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	for i := range t.NumField() {
		f := t.Field(i)
		if _, ok := f.Tag.Lookup(tagEnv); ok {
			return true
		}
		if _, ok := f.Tag.Lookup(tagJSON); ok {
			return true
		}
		inner := f.Type
		if inner.Kind() == reflect.Pointer {
			inner = inner.Elem()
		}
		if inner.Kind() == reflect.Struct && inner != timeType && typeWantsBinding(inner) {
			return true
		}
	}
	return false
}

// findUnknownKeys reports keys a listable source supplied that no field wanted.
//
// Only sources implementing KeyLister are consulted, which is what keeps the
// machine's own environment out of the result. The "wanted" set includes every
// field's current key AND its `was:` former names, so a deployment still using
// an old key name is never reported as a typo — that would punish exactly the
// migration the was tag exists to make safe.
func findUnknownKeys(sources []Source, fields []*field) []UnknownKey {
	wanted := make(map[string]bool, len(fields)*2)
	for _, f := range fields {
		wanted[f.Key] = true
		for _, w := range f.Was {
			wanted[w] = true
		}
		if f.Flag != "" {
			// A flag source is keyed by flag name, so that spelling is wanted
			// too — otherwise a flag-keyed source would report every field.
			wanted[f.Flag] = true
		}
	}

	var out []UnknownKey
	seen := map[string]bool{}
	for _, src := range sources {
		lister, ok := src.(KeyLister)
		if !ok {
			continue // the source cannot honestly enumerate; skip it entirely
		}
		for _, k := range lister.Keys() {
			if wanted[k] || seen[src.Name()+"\x00"+k] {
				continue
			}
			seen[src.Name()+"\x00"+k] = true
			out = append(out, UnknownKey{Key: k, Source: src.Name()})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// pruneEmptySections sets back to nil every optional section that no source
// touched (§6.4).
//
// "Touched" means a SOURCE supplied a value for some descendant. A default —
// whether from a tag or a Defaulter — does not count: if it did, any section
// holding one default would always be non-nil, which is the behaviour this
// replaces. One descendant is enough, because setting SMTP_HOST is an
// unambiguous statement of intent; requiring all of them would make optional
// fields inside optional sections impossible.
//
// Deepest sections are pruned first, so a parent sees its children's outcome.
func pruneEmptySections(sections []*section, res *Result) {
	for i := len(sections) - 1; i >= 0; i-- {
		s := sections[i]
		if s.Forced || s.Ptr.IsNil() {
			continue
		}
		if sectionWasSourced(s, res) {
			continue
		}
		s.Ptr.Set(reflect.Zero(s.Ptr.Type()))
		// Its leaves no longer exist, so they must not appear in the record —
		// Explain would otherwise list fields of a nil section.
		res.drop(s.Fields)
	}
}

// sectionWasSourced reports whether any leaf beneath a section got its value
// from a source rather than from a default.
func sectionWasSourced(s *section, res *Result) bool {
	for _, f := range s.Fields {
		if res.sourcedPaths[f.Path] {
			return true
		}
	}
	return false
}

// newField builds a field from its struct tags.
func newField(sf reflect.StructField, fv reflect.Value, path, key string, opts []string) *field {
	f := &field{
		Path:     path,
		Key:      key,
		Flag:     sf.Tag.Get(tagFlag),
		Doc:      sf.Tag.Get(tagDoc),
		Delim:    sf.Tag.Get(tagDelim),
		KVDelim:  sf.Tag.Get(tagKVDelim),
		Secret:   sf.Tag.Get(tagSecret) == "true",
		File:     hasOption(opts, optFile),
		Unset:    hasOption(opts, optUnset),
		Required: hasOption(opts, optRequired),
		NotEmpty: hasOption(opts, optNotEmpty),
		Value:    fv,
	}
	if def, ok := sf.Tag.Lookup(tagDefault); ok {
		f.Default, f.HasDef = def, true
	}
	if was, ok := sf.Tag.Lookup(tagWas); ok {
		for _, w := range strings.Split(was, ",") {
			if w = strings.TrimSpace(w); w != "" {
				f.Was = append(f.Was, w)
			}
		}
	}
	// notempty implies required: a value that must be non-empty must first
	// exist, and reporting only "may not be empty" for a missing key would send
	// an operator looking for a line that is not there.
	if f.NotEmpty {
		f.Required = true
	}
	return f
}

// claimFn reports whether a registered decoder handles a type. A type a
// decoder claims is a LEAF, however struct-shaped it is — otherwise the walker
// would descend into it and the decoder would never be reached.
//
// It is a function rather than a slice so the walk does not depend on the
// options type, and so callers with no decoders can pass nil.
type claimFn func(reflect.Type) bool

// isWalkable reports whether a value is a struct cfgkit should descend into
// rather than bind as a leaf.
//
// time.Time, any TextUnmarshaler, and any type a decoder claims are excluded:
// they are structs, but something else owns the entire string, so walking into
// them would try to bind their internal fields from the environment.
func isWalkable(v reflect.Value, claimed claimFn) bool {
	t := v.Type()
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t == timeType {
		return false
	}
	if claimed != nil && claimed(v.Type()) {
		return false
	}
	// A type with its own unmarshaler owns the whole string, so it is a LEAF.
	// Without this, url.URL and time.Time would be walked into and their
	// internal fields bound from the environment.
	if isUnmarshaler(reflect.PointerTo(t)) {
		return false
	}
	return true
}

// splitTag separates a tag's value from its comma-separated options.
func splitTag(tag string) (value string, opts []string) {
	parts := strings.Split(tag, ",")
	return parts[0], parts[1:]
}

// hasOption reports whether opts contains name.
func hasOption(opts []string, name string) bool {
	for _, o := range opts {
		if strings.TrimSpace(o) == name {
			return true
		}
	}
	return false
}

// optionValue returns the value of a "name=value" option, or "".
func optionValue(opts []string, prefix string) string {
	for _, o := range opts {
		if o = strings.TrimSpace(o); strings.HasPrefix(o, prefix) {
			return strings.TrimPrefix(o, prefix)
		}
	}
	return ""
}

// visitable reports whether a struct field is one the tree traversals may
// touch. It is THE definition of that rule, and every traversal calls it —
// walk, forEachStruct and collectLeaves — because three copies of the same
// predicate means a fix to one silently leaves the others wrong. That is
// exactly how the embedded-struct defect below survived unnoticed.
func visitable(sf reflect.StructField, fv reflect.Value) bool {
	return sf.IsExported() || isEmbeddedStruct(sf, fv)
}

// isEmbeddedStruct reports whether sf is an embedded struct whose fields a
// traversal may descend into even though sf itself is unexported.
//
// The settability check is not decoration. reflect marks a value read-only when
// it was reached through an unexported field, EXCEPT when that field is an
// embedded struct — so the promoted exported fields of one are writable while a
// plain unexported field's are not. Asking the value rather than inferring it
// from the type keeps this correct if that rule ever narrows.
func isEmbeddedStruct(sf reflect.StructField, fv reflect.Value) bool {
	if !sf.Anonymous {
		return false
	}
	// The value's own CanSet is deliberately NOT consulted, because it is false
	// for exactly the case this exists to allow: reflect marks the embedded
	// struct itself read-only while leaving its exported fields writable. The
	// unreachable case is the nil embedded POINTER, which walk reports rather
	// than skipping, since there the pointer itself would have to be set.
	t := sf.Type
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind() == reflect.Struct
}
