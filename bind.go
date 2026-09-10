package cfgkit

import (
	"fmt"
	"os"
	"strings"
)

// sourceDefault is the origin recorded for a value no source supplied. It is a
// real origin, not a placeholder: "came from the compiled-in default" is the
// answer to "where did this value come from" whenever zero-config is working.
const sourceDefault = "default"

// bind resolves every field against the flat sources and records provenance.
//
// Sources are consulted LAST FIRST, so the last source in the caller's list
// wins. The first source that claims a key ends the search — a later (lower
// precedence) source is never asked, which is what makes precedence positional
// rather than emergent.
func bind(fields []*field, sources []Source, res *Result, mode Mode, o *options) []error {
	var errs []error

	// Tag defaults are applied before any source, so they participate in the
	// same required/notempty checks as a sourced value.
	errs = append(errs, applyTagDefaults(fields, o)...)

	for _, f := range fields {
		raw, origin, found, err := lookup(f, sources)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if !found {
			// Nothing supplied the key. Whether that is a problem depends on
			// the field, not on the source list.
			if f.Required && f.Value.IsZero() {
				// No Mode: the `required` option is UNCONDITIONAL, and
				// RequiredError.Mode is documented as "the mode that made it
				// required, empty when unconditional". Naming the current mode
				// implied the requirement was scoped to it — exactly the
				// confusion RequiredIn exists to keep out of the tag.
				errs = append(errs, &RequiredError{Path: f.Path, Key: f.Key})
			}
			// No flat source claimed the key, but a structured source may have
			// set the field, in which case IT is the origin — not the default.
			origin := sourceDefault
			if s, ok := res.origins[f.Path]; ok {
				origin = s
			}
			observe(o, f, origin, res)
			res.add(Field{Path: f.Path, Key: f.Key, Source: origin, Secret: f.Secret,
				Value: renderValue(f, res.reveal)})
			continue
		}

		// Transformers rewrite the value; they cannot change the origin, which
		// is the firewall that keeps Explain honest.
		if len(o.transformers) > 0 {
			out, terr := applyTransforms(o.transformers, f.Key, raw, origin)
			if terr != nil {
				errs = append(errs, &SourceError{Source: origin, Key: f.Key, Err: terr})
				continue
			}
			raw = out
		}

		if f.NotEmpty && raw == "" {
			errs = append(errs, &RequiredError{Path: f.Path, Key: f.Key, Empty: true})
			continue
		}

		if err := decodeWith(o.decoders, f.Value, raw, f.delims()); err != nil {
			// A secret's raw text never enters an error message: an error is
			// logged, and a logged credential is a leak.
			//
			// The decoder's own message is DISCARDED for a secret rather than
			// merely kept out of DecodeError.Value, because every decoder quotes
			// the offending text — `"hunter2" is not a valid int` — and a slice
			// or map error quotes the offending element or key on top of that.
			// Replacing the message with one built only from the field's TYPE is
			// the only way to be sure nothing derived from the value survives.
			// Withholding Value alone did not work: it is not printed anyway.
			de := &DecodeError{Path: f.Path, Key: f.Key, Source: origin, Err: err}
			if f.Secret {
				de.Err = fmt.Errorf("the supplied value is not a valid %s", f.Value.Type())
			} else {
				de.Value = raw
			}
			errs = append(errs, de)
			continue
		}

		// Removing the variable stops a child process this program starts later
		// from inheriting the secret. This is the single documented exception
		// to "Load never writes to os.Environ", and it touches only this key.
		if f.Unset {
			_ = os.Unsetenv(f.Key)
		}

		res.markSourced(f.Path)
		observe(o, f, origin, res)
		res.add(Field{Path: f.Path, Key: f.Key, Source: origin, Secret: f.Secret,
			Value: renderValue(f, res.reveal)})
	}

	return errs
}

// observe notifies every registered observer that a field resolved.
//
// The value handed over is UNMASKED regardless of res.reveal: an audit sink
// needs the real value, and an observer that logs must consult Field.Secret
// itself. cfgkit cannot know whether a given sink is a safe place for a
// credential, so it does not pretend to.
func observe(o *options, f *field, origin string, res *Result) {
	if len(o.observers) == 0 {
		return
	}
	fl := Field{
		Path:   f.Path,
		Key:    f.Key,
		Source: origin,
		Secret: f.Secret,
		Value:  formatValue(f.Value, f.delims()),
	}
	for _, ob := range o.observers {
		ob.ObserveResolve(fl)
	}
}

// applyTagDefaults fills fields carrying a `default:` tag that are still at
// their zero value.
//
// Defaulter has already run, so a tag only fills what the method left unset —
// the method wins because it is typed and can compute. Document calls this too,
// so the generated contract file shows the value a reader would actually get
// rather than the type's zero value.
func applyTagDefaults(fields []*field, o *options) []error {
	var errs []error
	for _, f := range fields {
		if !f.HasDef || !f.Value.IsZero() {
			continue
		}
		if err := decodeWith(o.decoders, f.Value, f.Default, f.delims()); err != nil {
			errs = append(errs, &DecodeError{Path: f.Path, Key: f.Key, Source: sourceDefault, Err: err})
		}
	}
	return errs
}

// lookup finds the first source claiming the field's key, searching from the
// highest-precedence source backwards.
//
// It tries the current key first and each former name (the was tag) only after
// every source has declined the current one. A deployment that still exports an
// old name keeps working, while a deployment that has migrated is never
// second-guessed.
func lookup(f *field, sources []Source) (raw, origin string, found bool, err error) {
	keys := append([]string{f.Key}, f.Was...)

	for ki, key := range keys {
		for i := len(sources) - 1; i >= 0; i-- {
			src := sources[i]

			// A flag source is keyed by flag name rather than env key, and it
			// applies only to fields that opted in with a flag tag.
			lookupKey, applies := keyForSource(src, f, key)
			if !applies {
				continue
			}

			v, ok, lerr := src.Lookup(lookupKey)
			if lerr != nil {
				// A source failure is never a miss. An unreachable secret store
				// must not look like an unset variable, because that difference
				// is a deploy proceeding with an empty password.
				return "", "", false, &SourceError{Source: src.Name(), Key: lookupKey, Err: lerr}
			}
			if !ok {
				continue
			}

			name := src.Name()
			if ki > 0 {
				// Surfacing the deprecated origin is the whole point of `was`:
				// an operator can see what to change before the tag is removed.
				name += " (deprecated key " + key + ")"
			}

			// The value names a path rather than being the value itself. This
			// is how Docker and Kubernetes mount a secret: the file never
			// appears in docker inspect, in /proc/<pid>/environ, or in a child
			// process.
			if f.File {
				b, rerr := os.ReadFile(v)
				if rerr != nil {
					return "", "", false, &SourceError{Source: name, Key: key, Err: rerr}
				}
				// Every tool that writes these files adds a trailing newline,
				// and no secret ever intends one.
				return strings.TrimRight(string(b), "\r\n"), name, true, nil
			}
			return v, name, true, nil
		}
	}

	return "", "", false, nil
}

// renderValue formats a field's current value for provenance output, masking
// secrets structurally.
//
// A masked value is ABSENT, not hidden by styling and not truncated: the string
// never enters the output, so a report cannot leak one by being copied,
// screenshotted, or rendered by something that ignores formatting.
func renderValue(f *field, reveal bool) string {
	if f.Secret && !reveal {
		return maskedValue
	}
	return formatValue(f.Value, f.delims())
}
