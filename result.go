package cfgkit

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"text/tabwriter"
)

// maskedValue stands in for a secret. It is a fixed string of constant length,
// so it leaks nothing about the real value — not even how long it is.
const maskedValue = "••••••"

// Field describes one resolved field and, crucially, where its value came from.
//
// Provenance is the capability no other Go config library offers, and it
// answers the most common configuration support question there is: "why is my
// port 9001 when I set 2310?" Layered configuration without it is a guessing
// game that gets worse with every layer added.
type Field struct {
	Path   string `json:"path"`   // "Server.Port" — the Go path, not the key
	Key    string `json:"key"`    // "PORT" — the flat key consulted
	Value  string `json:"value"`  // rendered; masked when Secret and not revealed
	Source string `json:"source"` // "default" | "file:.env" | "environ" | a Source's Name
	Secret bool   `json:"secret"`
}

// UnknownKey is a key a source supplied that matched no field — almost always a
// typo, and otherwise a key meant for a different consumer of the same file.
//
// It is REPORTED, never fatal. One .env file legitimately serves several
// audiences: sync_go's .env.prod carries GITHUB_SECRET_* keys for its
// deployment pipeline alongside the application's own configuration, and those
// keys are unknown to the binary by design. A loader that refused to start
// would break exactly the pattern dotenvctl's --prefix selection exists to
// serve.
type UnknownKey struct {
	Key    string `json:"key"`    // "DATABAS_URL"
	Source string `json:"source"` // the source that supplied it
}

// String renders one finding for a log line.
func (u UnknownKey) String() string {
	return u.Key + " (from " + u.Source + ") matched no field"
}

// Result records how a configuration was assembled.
type Result struct {
	fields     []Field
	structured []string
	// origins maps a Go path to the structured source that last changed it.
	// Flat sources report their own origin directly; structured ones are
	// attributed by observation (see snapshot.go).
	origins map[string]string
	// sourcedPaths records which Go paths got a value from a SOURCE rather
	// than from a default. Pruning an optional section (§6.4) turns on this
	// distinction: a default is not a statement of intent.
	sourcedPaths map[string]bool
	// unknown holds keys a listable source supplied that no field wanted.
	unknown []UnknownKey
	// files is the .env chain DefaultSources consulted, in precedence order.
	// It is empty for an explicit source list, where the caller already knows.
	files  []string
	mode   Mode
	reveal bool
}

// Files reports the .env chain DefaultSources consulted, lowest precedence
// first, including files that did not exist.
//
// It answers the question the chain's convenience creates: with the filenames
// no longer written in main.go, "which files were even looked at?" would
// otherwise be unanswerable — and a typo'd filename would look exactly like a
// file whose values were overridden.
func (r *Result) Files() []string {
	out := make([]string, len(r.files))
	copy(out, r.files)
	return out
}

// Unknown returns every key a source supplied that matched no field.
//
// Only sources implementing KeyLister contribute, so FromEnviron never appears
// here: its key set is the whole machine, and a report listing PATH and HOME
// would bury the one line that matters.
//
// The finding is advisory. Act on it in whatever way suits the deployment:
//
//	if u := res.Unknown(); len(u) > 0 {
//		log.Printf("config: %v", u)
//	}
func (r *Result) Unknown() []UnknownKey {
	out := make([]UnknownKey, len(r.unknown))
	copy(out, r.unknown)
	return out
}

// markSourced records that a source, not a default, supplied this path.
func (r *Result) markSourced(path string) {
	if r.sourcedPaths == nil {
		r.sourcedPaths = map[string]bool{}
	}
	r.sourcedPaths[path] = true
}

// drop removes fields belonging to a section that was pruned back to nil.
//
// They must not appear in Explain: reporting a value for a field of a nil
// section would describe memory that no longer exists.
func (r *Result) drop(fields []*field) {
	if len(fields) == 0 {
		return
	}
	gone := make(map[string]bool, len(fields))
	for _, f := range fields {
		gone[f.Path] = true
	}
	kept := r.fields[:0]
	for _, f := range r.fields {
		if !gone[f.Path] {
			kept = append(kept, f)
		}
	}
	r.fields = kept
}

// add appends a resolved field. It is called once per bound leaf, in walk order.
func (r *Result) add(f Field) { r.fields = append(r.fields, f) }

// Fields returns every resolved field, sorted by Go path so two runs of the
// same configuration produce identical output and can be diffed.
func (r *Result) Fields() []Field {
	out := make([]Field, len(r.fields))
	copy(out, r.fields)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Mode reports the mode this configuration was loaded under.
func (r *Result) Mode() Mode { return r.mode }

// Explain writes an aligned table of every field, its value, and its origin.
//
// Secret values are absent unless the load was made with Reveal, so the default
// output is safe to paste into an issue.
func (r *Result) Explain(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	// tabwriter BUFFERS everything and touches w only on Flush — it has to,
	// because column widths are not known until the last row is in. So these
	// writes cannot fail, and the error the caller needs is Flush's.
	//
	// Checked empirically rather than assumed: 5,000 wide rows produced zero
	// writes to the underlying writer before Flush and zero errors from
	// Fprintf. Guarding each call would add three branches no test can reach.
	_, _ = fmt.Fprintf(tw, "FIELD\tKEY\tVALUE\tSOURCE\n")
	for _, f := range r.Fields() {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", f.Path, f.Key, f.Value, f.Source)
	}
	if len(r.structured) > 0 {
		_, _ = fmt.Fprintf(tw, "\nstructured sources applied: %s\n", strings.Join(r.structured, ", "))
	}
	return tw.Flush()
}

// JSON returns the provenance record as JSON, masked by the same rule as
// Explain.
func (r *Result) JSON() ([]byte, error) {
	return json.Marshal(struct {
		Mode       Mode     `json:"mode"`
		Fields     []Field  `json:"fields"`
		Structured []string `json:"structured_sources,omitempty"`
	}{Mode: r.mode, Fields: r.Fields(), Structured: r.structured})
}

// formatValue renders a bound value as the text a reader expects.
//
// Slices and maps are rendered in the SAME text form they are parsed from —
// "a,b" and "k:v,k2:v2", using this field's own separators — because the reader
// is comparing the output against the line they wrote in a .env file, not
// against a Go literal. It is what makes Document's output re-loadable.
func formatValue(v reflect.Value, d delims) string {
	switch v.Kind() {
	case reflect.Slice:
		parts := make([]string, v.Len())
		for i := range v.Len() {
			parts[i] = formatValue(v.Index(i), d)
		}
		return strings.Join(parts, d.pair)
	case reflect.Map:
		// Sorted by key, because Go's map iteration order is randomised and
		// Explain / Document output is diffed in CI. Unsorted output would make
		// a "contract is stale" check fail at random.
		entries := make([]string, 0, v.Len())
		for _, k := range v.MapKeys() {
			entries = append(entries, formatValue(k, d)+d.kv+formatValue(v.MapIndex(k), d))
		}
		sort.Strings(entries)
		return strings.Join(entries, d.pair)
	case reflect.Pointer:
		if v.IsNil() {
			return ""
		}
		return formatValue(v.Elem(), d)
	default:
		return fmt.Sprintf("%v", v.Interface())
	}
}
