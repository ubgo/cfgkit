package cfgkit

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// Validation is Go, not a tag language.
//
// A stringly-typed rule DSL cannot be type-checked, refactored, or stepped
// through in a debugger: rename a field and a `validate:"required_if=Kind …"`
// tag silently stops matching. These helpers are COMBINATORS, not a language —
// each returns a plain error so it composes with errors.Join and with any
// hand-written check beside it.
//
// A caller who wants a catalogue of ready rules (email, URL, UUID, CIDR) plugs
// go-playground/validator into the same Validator seam, with that dependency in
// the caller rather than here.

// placeholderPattern matches the __STAND_IN__ convention shared with dotenvctl,
// which reports such values as "!" in its drift matrix. A placeholder is a value
// somebody meant to replace and did not.
var placeholderPattern = regexp.MustCompile(`^__[A-Z0-9_]+__$`)

// Required reports an error when v is the zero value for its type.
func Required(field string, v any) error {
	if isZero(v) {
		return &ValidationError{Path: field, Rule: "required", Msg: "is required but was not set"}
	}
	return nil
}

// NotEmpty reports an error when v is an empty string.
//
// It is separate from Required because the two failures have different causes
// and different fixes: a missing key needs a new line in the deployment, an
// empty one needs a value in a line that already exists.
func NotEmpty(field string, v string) error {
	if v == "" {
		return &ValidationError{Path: field, Rule: "notempty", Msg: "must not be empty"}
	}
	return nil
}

// RequiredIn enforces Required only when running in mode.
//
// This is the mechanism behind the rule that makes zero-config safe: a
// convenience that keeps development frictionless must hard-fail in production
// rather than being silently accepted. A generated dev secret is delightful on
// a laptop and catastrophic if it survives to prod.
func RequiredIn(mode, current Mode, field string, v any) error {
	if current != mode {
		return nil
	}
	if isZero(v) {
		return &ValidationError{
			Path: field,
			Rule: "required_in",
			Msg:  fmt.Sprintf("is required in mode=%s but was not set", mode),
		}
	}
	return nil
}

// RequiredWhen asserts that a field is present exactly when a condition holds,
// and absent otherwise. It produces BOTH directions of the invariant from one
// call, with a distinct message for each.
//
// This is the discriminated-union case that only a config language could
// express before: "cloudflare is required when kind is cloudflare, and must not
// be set otherwise". In Go the discriminant is a real typed constant, so the
// comparison cannot be a mistyped string literal — which is more than the tag
// form can promise.
func RequiredWhen(field string, present bool, condition string, holds bool) error {
	switch {
	case holds && !present:
		return &ValidationError{
			Path: field,
			Rule: "required_when",
			Msg:  fmt.Sprintf("is required when %s, but it was not set", condition),
		}
	case !holds && present:
		return &ValidationError{
			Path: field,
			Rule: "required_when",
			Msg:  fmt.Sprintf("must not be set unless %s", condition),
		}
	default:
		return nil
	}
}

// OneOf reports an error when v is not in allowed.
//
// Prefer a typed constant with its own UnmarshalText where the set is fixed and
// known at compile time — that turns a bad value into a decode error naming the
// field, one layer earlier. OneOf is for sets that are only known at runtime.
func OneOf[T comparable](field string, v T, allowed ...T) error {
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	strs := make([]string, len(allowed))
	for i, a := range allowed {
		strs[i] = fmt.Sprintf("%v", a)
	}
	return &ValidationError{
		Path: field,
		Rule: "one_of",
		Msg:  fmt.Sprintf("is %v, want one of [%s]", v, strings.Join(strs, " ")),
	}
}

// Range reports an error when v falls outside [lo, hi] inclusive.
func Range[T int | int8 | int16 | int32 | int64 | float32 | float64](field string, v, lo, hi T) error {
	if v < lo || v > hi {
		return &ValidationError{
			Path: field,
			Rule: "range",
			Msg:  fmt.Sprintf("is %v, want between %v and %v", v, lo, hi),
		}
	}
	return nil
}

// Matches reports an error when v does not match re.
func Matches(field, v string, re *regexp.Regexp) error {
	if !re.MatchString(v) {
		return &ValidationError{
			Path: field,
			Rule: "matches",
			Msg:  fmt.Sprintf("does not match %s", re),
		}
	}
	return nil
}

// MutuallyExclusive reports an error when more than one of the named fields is
// set. Pass field names and their values in pairs via Set.
func MutuallyExclusive(fields ...Set) error {
	var present []string
	for _, f := range fields {
		if !isZero(f.Value) {
			present = append(present, f.Name)
		}
	}
	if len(present) > 1 {
		return &ValidationError{
			Path: strings.Join(present, ", "),
			Rule: "mutually_exclusive",
			Msg:  "only one of these may be set",
		}
	}
	return nil
}

// AtLeastOneOf reports an error when none of the named fields is set.
func AtLeastOneOf(fields ...Set) error {
	names := make([]string, len(fields))
	for i, f := range fields {
		if !isZero(f.Value) {
			return nil
		}
		names[i] = f.Name
	}
	return &ValidationError{
		Path: strings.Join(names, ", "),
		Rule: "at_least_one_of",
		Msg:  "at least one of these must be set",
	}
}

// Set pairs a field name with its value for the group rules. It exists because
// Go cannot recover a field's name from its value, and an error naming
// "arg 2" instead of "Database.URL" is not worth printing.
type Set struct {
	Name  string
	Value any
}

// NotWeakSecret rejects a value that is empty, a __PLACEHOLDER__, or one of the
// caller's known template strings — but only outside development.
//
// In dev the same value is fine and must stay fine, because that is what lets a
// fresh clone run with no setup. The whole point is that the convenience cannot
// survive to production silently.
func NotWeakSecret(current Mode, field, v string, known ...string) error {
	if current == ModeDev {
		return nil
	}
	bad := ""
	switch {
	case v == "":
		bad = "is empty"
	case placeholderPattern.MatchString(v):
		bad = "is still a placeholder"
	default:
		for _, k := range known {
			if v == k {
				bad = "is a well-known default value"
				break
			}
		}
	}
	if bad == "" {
		return nil
	}
	// The offending value is never echoed: it is a credential field, and an
	// error message is a thing that gets logged.
	return &ValidationError{
		Path: field,
		Rule: "not_weak_secret",
		Msg:  fmt.Sprintf("%s in mode=%s; set a real value", bad, current),
	}
}

// isZero reports whether v is its type's zero value, treating a nil interface
// as zero.
func isZero(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.IsZero()
}
