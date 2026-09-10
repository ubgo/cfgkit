package cfgkit

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
)

// Document writes a .env.example describing every key the configuration binds.
//
// The struct already holds every fact such a file needs: the key, whether it is
// required, the default, whether it is a secret, and a description. Generating
// it removes the only way a contract file can drift from the code — a human
// keeping two things in sync by hand. sync_go's committed sample still hardcodes
// a developer's absolute path for exactly that reason.
//
// This closes a loop no other toolchain has: the struct writes the contract,
// and `dotenvctl matrix --contract .env.example` then fails CI when any
// environment lacks a key the contract declares.
//
//	struct → .env.example → dotenvctl matrix --contract → red build
//
// Secrets are emitted with an EMPTY value and a warning comment. A contract
// file is committed, so it must never carry a real credential — and a generator
// that wrote one would be a credential leak with a schedule.
func Document[T any](w io.Writer, opts ...Option) error {
	o := &options{modeKey: defaultModeKey}
	for _, fn := range opts {
		fn(o)
	}

	var cfg T
	v := reflect.ValueOf(&cfg).Elem()
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("cfgkit: Document requires a struct type, got %s", v.Kind())
	}

	// Defaults run first so the file shows the value a reader would actually
	// get, not the type's zero value.
	applyDefaults(v, o.claims())
	fields, _ := walk(v, "", "", o.claims())
	// Tag defaults too, so the file shows what a reader actually gets rather
	// than the type's zero value. A default that cannot be parsed is a bug in
	// the struct, so it is reported here rather than silently rendered as zero.
	if errs := applyTagDefaults(fields, o); len(errs) > 0 {
		return errors.Join(errs...)
	}

	// File order follows the Go path so the output is stable across runs and
	// can be diffed, which is what makes the CI drift gate possible.
	sort.Slice(fields, func(i, j int) bool { return fields[i].Path < fields[j].Path })

	for i, f := range fields {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if err := writeEntry(w, f); err != nil {
			return err
		}
	}
	return nil
}

// writeEntry emits one documented key: its description, its status line, and
// the assignment itself.
func writeEntry(w io.Writer, f *field) error {
	if f.Doc != "" {
		if _, err := fmt.Fprintf(w, "# %s\n", f.Doc); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintf(w, "# %s\n", statusLine(f)); err != nil {
		return err
	}

	// A secret's value is always empty here regardless of its default: the
	// point of the file is to be committed.
	value := ""
	if !f.Secret {
		value = defaultText(f)
	}
	_, err := fmt.Fprintf(w, "%s=%s\n", f.Key, value)
	return err
}

// statusLine describes the field's obligations in one comment line.
func statusLine(f *field) string {
	var parts []string
	switch {
	case f.NotEmpty:
		parts = append(parts, "REQUIRED, must not be empty")
	case f.Required:
		parts = append(parts, "REQUIRED")
	default:
		parts = append(parts, "optional")
	}
	if f.Secret {
		parts = append(parts, "secret — do not commit a real value")
	}
	if len(f.Was) > 0 {
		parts = append(parts, "formerly "+strings.Join(f.Was, ", "))
	}
	if f.File {
		parts = append(parts, "the value is a PATH to a file holding the real value")
	}
	return strings.Join(parts, " · ")
}

// defaultText renders the value a reader gets when they set nothing.
//
// It reads the field after Defaults has run rather than the tag, so a computed
// default appears exactly as it will behave. An empty result is left empty
// rather than written as "" — the file is read by shells and by dotenvctl, and
// both treat a bare KEY= as the empty value already.
func defaultText(f *field) string {
	return formatValue(f.Value, f.delims())
}
