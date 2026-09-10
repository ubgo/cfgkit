package cfgkit

import "reflect"

// Extension happens through three small interfaces, each with a plain-function
// adapter so injecting behaviour never requires declaring a type.
//
// THE FIREWALL, which every capability must respect:
//
//	A capability may change what a value READS AS.
//	It may never change WHICH SOURCE WON.
//
// Transformers rewrite values; they cannot redirect resolution. Decoders change
// how text becomes a Go value; they cannot claim a key. Observers see
// everything and change nothing. Precedence stays a property of the source list
// the caller wrote — visible in Explain and impossible to alter from a plugin.
//
// Without that rule, a transformer could make `Explain` a lie, which would cost
// more than the feature is worth: the whole point of provenance is that it can
// be trusted when someone is debugging at 3am.

// Decoder converts a raw string into a value of a type the core does not
// handle, or overrides how the core handles one.
//
// Decoders are consulted BEFORE the built-in type set and before the
// encoding.TextUnmarshaler hatch, so a registered decoder wins over both. That
// ordering is deliberate: overriding is the reason to register one.
//
// Use it when a type is not yours to change — a struct from a third-party
// package that implements no unmarshaler — or when a type must be parsed
// differently in configuration than everywhere else in the program. If the type
// IS yours, implement encoding.TextUnmarshaler instead: the rule then travels
// with the type rather than with the load call.
type Decoder interface {
	// Decode parses raw into a value assignable to typ.
	//
	// Return (value, true, nil) to claim the type, (nil, false, nil) to decline
	// so the next decoder or the core handles it, and (nil, true, err) to claim
	// it and report a bad value.
	Decode(typ reflect.Type, raw string) (any, bool, error)
}

// DecoderFunc adapts a function into a Decoder.
type DecoderFunc func(typ reflect.Type, raw string) (any, bool, error)

// Decode implements Decoder.
func (f DecoderFunc) Decode(typ reflect.Type, raw string) (any, bool, error) {
	return f(typ, raw)
}

// Transformer rewrites a raw value after a source supplied it and before it is
// decoded.
//
// Transformers are CHAINED in registration order, each seeing the previous
// one's output, so several may compose — decrypt, then expand a template, then
// trim. A transformer that does not care about a value returns it unchanged.
//
// It receives the key and the source name as well as the value, so a
// transformer can act on one origin only: decrypt values from Vault, leave the
// same key alone when it came from a local .env file.
//
// Use it when values arrive in a form the field's type cannot parse but a
// mechanical step can fix: ciphertext, base64, a value wrapped in quotes by a
// platform, a legacy format you are migrating away from.
type Transformer interface {
	// Transform returns the value to use in place of value.
	//
	// An error aborts the whole Load. Return the value unchanged rather than an
	// error when the transformer simply does not apply.
	Transform(key, value, source string) (string, error)
}

// TransformerFunc adapts a function into a Transformer.
type TransformerFunc func(key, value, source string) (string, error)

// Transform implements Transformer.
func (f TransformerFunc) Transform(key, value, source string) (string, error) {
	return f(key, value, source)
}

// Observer is told about every field once it resolves. It cannot change
// anything — that is what makes it safe to register several.
//
// Use it for auditing (record which secrets were read and from where),
// metrics (count fields still on their compiled-in defaults), or a startup
// warning about deprecated keys still in use.
type Observer interface {
	// ObserveResolve fires once per bound field, after its value is decoded.
	//
	// It receives the field with its value UNMASKED, because an audit sink
	// needs the real value. An observer that logs MUST consult Field.Secret
	// itself — cfgkit cannot know whether a given sink is safe.
	ObserveResolve(f Field)
}

// ObserverFunc adapts a function into an Observer.
type ObserverFunc func(f Field)

// ObserveResolve implements Observer.
func (f ObserverFunc) ObserveResolve(field Field) { f(field) }

// WithDecoder registers a decoder. Later registrations are consulted first, so
// the most recently added decoder wins — matching how a caller expects a
// late override to behave.
func WithDecoder(d Decoder) Option {
	return func(o *options) { o.decoders = append(o.decoders, d) }
}

// WithTransform registers a transformer from a plain function.
//
// Transformers chain in registration order: the first registered runs first,
// and each subsequent one sees the previous output.
func WithTransform(fn func(key, value, source string) (string, error)) Option {
	return func(o *options) { o.transformers = append(o.transformers, TransformerFunc(fn)) }
}

// WithTransformer registers a transformer implemented as a type, for a
// transformer that needs its own state — a decryption client, a cache.
func WithTransformer(t Transformer) Option {
	return func(o *options) { o.transformers = append(o.transformers, t) }
}

// WithObserver registers an observer. All observers run, in registration order.
func WithObserver(ob Observer) Option {
	return func(o *options) { o.observers = append(o.observers, ob) }
}

// claims returns a predicate reporting whether any registered decoder handles a
// type, so the walker can treat a claimed struct as a leaf.
//
// It probes with the empty string. A decoder must therefore decide whether it
// handles a TYPE without inspecting the value — which is the contract anyway:
// Decode's second return says "this type is mine", not "this value parsed".
func (o *options) claims() claimFn {
	if len(o.decoders) == 0 {
		return nil
	}
	return func(t reflect.Type) bool {
		for i := len(o.decoders) - 1; i >= 0; i-- {
			if _, ok, _ := o.decoders[i].Decode(t, ""); ok {
				return true
			}
		}
		return false
	}
}

// applyTransforms runs the chain over one raw value.
//
// The source name is passed through unchanged, and no transformer can alter
// it — that is the firewall in code rather than in prose.
func applyTransforms(ts []Transformer, key, value, source string) (string, error) {
	for _, t := range ts {
		out, err := t.Transform(key, value, source)
		if err != nil {
			return "", err
		}
		value = out
	}
	return value, nil
}

// decodeWith tries the registered decoders before falling back to the core.
//
// Decoders are tried in reverse registration order so a later registration
// overrides an earlier one.
func decodeWith(decoders []Decoder, dst reflect.Value, raw string, d delims) error {
	for i := len(decoders) - 1; i >= 0; i-- {
		v, claimed, err := decoders[i].Decode(dst.Type(), raw)
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		rv := reflect.ValueOf(v)
		if !rv.IsValid() || !rv.Type().AssignableTo(dst.Type()) {
			// A decoder that claims a type must return something assignable to
			// it. Reporting this loudly beats a confusing panic from reflect.
			return &DecodeError{
				Path: dst.Type().String(),
				Err:  errDecoderMismatch{got: rv.Type(), want: dst.Type()},
			}
		}
		dst.Set(rv)
		return nil
	}
	return decode(dst, raw, d)
}

// errDecoderMismatch reports a decoder that claimed a type and returned another.
type errDecoderMismatch struct{ got, want reflect.Type }

// Error reports a Decoder that claimed a value but returned the wrong Go type,
// naming both types — the mistake is in the caller's decoder, so the message
// has to point at it rather than at the field.
func (e errDecoderMismatch) Error() string {
	return "decoder returned " + e.got.String() + ", which is not assignable to " + e.want.String()
}
