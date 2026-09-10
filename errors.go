package cfgkit

import (
	"errors"
	"fmt"
)

// Every failure names the field path, the key, and where the value came from.
//
// Errors are COLLECTED across the whole pipeline and joined, never returned at
// the first failure. A misconfigured deploy must not be a guessing game of one
// fix per restart: an operator needs every problem in one run.

// DecodeError reports a value that could not be parsed into its field's type.
type DecodeError struct {
	Path   string // "Server.Port"
	Key    string // "PORT"
	Source string // "file:.env.local"
	// Value is the raw text, for programmatic handling only. It is EMPTY when
	// the field is secret, and it is deliberately not part of Error(): the
	// message would then carry the value into whatever logs the error.
	Value string
	// Err is the underlying failure. For a secret field it is rewritten to name
	// the expected TYPE and nothing else, because a decoder's own message
	// quotes the offending text.
	Err error
}

// Error renders "Path (KEY from source): cause".
//
// The offending VALUE is deliberately absent. For a secret field the wrapped
// cause is rewritten to name the expected type, because a decoder's own
// message quotes the text it choked on — which is how a credential reaches a
// log aggregator.
func (e *DecodeError) Error() string {
	return fmt.Sprintf("%s (%s from %s): %v", e.Path, e.Key, e.Source, e.Err)
}

// Unwrap exposes the decoder's own failure to errors.Is and errors.As. For a
// secret field this is the rewritten cause, never the one quoting the value.
func (e *DecodeError) Unwrap() error { return e.Err }

// errNilBuild is returned by NewWatcher when handed no builder. It is a plain
// sentinel rather than a typed error because there is exactly one way to cause
// it and nothing to report about it beyond the remedy.
var errNilBuild = errors.New("cfgkit: NewWatcher needs a build function that returns a fresh option list")

// UnreachableFieldError reports a struct shape whose fields no source could
// ever fill, so that it fails loudly instead of binding nothing.
//
// The only shape that produces it is an embedded POINTER to an unexported type
// carrying config tags. reflect refuses to set such a pointer, so cfgkit cannot
// allocate it and every field beneath it is unreachable. Embedding the type by
// VALUE works and is the fix; exporting the type also works.
type UnreachableFieldError struct {
	Path string // "Config" — the embedded field
	Type string // "*internalConfig"
}

// Error names the field, its type, and both fixes — embed by value, or export
// the type. A shape error that does not say how to reshape it just relocates
// the puzzle.
func (e *UnreachableFieldError) Error() string {
	return fmt.Sprintf("%s (%s): an embedded pointer to an unexported type cannot be allocated, "+
		"so no source can fill the fields beneath it — embed it by value, or export the type",
		e.Path, e.Type)
}

// UnreachableHookError reports a Defaults, Derive or Validate method that can
// never run, so that it fails loudly instead of silently doing nothing.
//
// Go's reflect refuses to produce an interface value for anything reached
// through an unexported field, and an embedded unexported struct is exactly
// that. Its FIELDS still bind — reflect can set them, and encoding/json binds
// them too — but its methods are unreachable. A framework's Validate is what
// enforces its invariants, so a silently dead one is worse than a load failure.
//
// The fix is to export the embedded type. Across packages it must be exported
// anyway, so this only ever fires within one package.
type UnreachableHookError struct {
	Path string // "Config" — the embedded field
	Type string // "frameworkConfig"
	Hook string // "Validate"
}

// Error names the field, its type, the dead hook, and the fix. It says which
// hook so a reader is not left checking all three.
func (e *UnreachableHookError) Error() string {
	return fmt.Sprintf("%s (%s): %s() can never run, because Go does not allow calling a method "+
		"on a value reached through an unexported embedded field — export the type",
		e.Path, e.Type, e.Hook)
}

// RequiredError reports a field that had to resolve and did not.
//
// Missing and Empty are separate because they have different causes and
// different fixes: a missing key needs a new line in the deployment, while an
// empty one needs a value filled into a line that already exists. Reporting
// both as "required" sends an operator looking for a line that is already there.
type RequiredError struct {
	Path  string
	Key   string
	Mode  Mode // the mode that made it required, empty when unconditional
	Empty bool // true when the key resolved but the value was ""
}

// Error distinguishes the two causes in words, not just in the Empty field:
// "no source supplied it" versus "resolved to an empty value". They need
// different fixes, and mode is named when the requirement was conditional so
// nobody hunts for a rule that does not apply to their environment.
func (e *RequiredError) Error() string {
	what := "is required but no source supplied it"
	if e.Empty {
		what = "is required but resolved to an empty value"
	}
	if e.Mode != "" {
		return fmt.Sprintf("%s (%s) %s in mode=%s", e.Path, e.Key, what, e.Mode)
	}
	return fmt.Sprintf("%s (%s) %s", e.Path, e.Key, what)
}

// ValidationError reports a rule that failed. Rule names the check so a reader
// can find it in the code without matching on message text.
type ValidationError struct {
	Path string
	Rule string
	Msg  string
}

// Error renders "Path: message (rule)". The rule name is included so a reader
// can grep for the check itself rather than matching on message text, which
// is the thing most likely to be reworded.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s (%s)", e.Path, e.Msg, e.Rule)
}

// SourceError reports that a source itself failed.
//
// This is never a miss. An unreachable secret store must not be
// indistinguishable from an unset variable, because that difference is a deploy
// proceeding with an empty password.
type SourceError struct {
	Source string
	Key    string
	Err    error
}

// Error renders "source NAME failed for KEY: cause", naming both the source
// and the key. A load drawing on six sources otherwise leaves the reader
// guessing which one was unreachable.
func (e *SourceError) Error() string {
	return fmt.Sprintf("source %s failed for %s: %v", e.Source, e.Key, e.Err)
}

// Unwrap exposes the transport failure, so a caller can match on its own
// backend's error types through errors.As without parsing this message.
func (e *SourceError) Unwrap() error { return e.Err }
