package cfgkit

import (
	"encoding"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// The supported type set is deliberately closed and small. Anything outside it
// must implement encoding.TextUnmarshaler — the single documented escape hatch.
//
// Why one hatch rather than a growing switch: url.URL, net.IP, slog.Level and
// every user-defined enum either already satisfy TextUnmarshaler or can in four
// lines. A library that instead special-cases each type grows a type zoo and
// still cannot cover the next one.

// timeLayout is the only accepted layout for time.Time. RFC3339 is chosen
// because it is unambiguous across locales and is what every config format and
// every log aggregator already emits; accepting several layouts would make
// "2026-01-02" mean different instants depending on which one matched first.
const timeLayout = time.RFC3339

// defaultDelim separates elements of a slice value, and one key/value pair of a
// map value from the next. Comma is the convention in every dotenv-adjacent
// tool; a field may override it with the delim tag.
const defaultDelim = ","

// defaultKVDelim separates a map key from its value. Colon matches caarlos0/env
// and go-envconfig, so a struct moved from either library keeps working, and it
// reads naturally in a .env file: TAGS=env:prod,team:core.
const defaultKVDelim = ":"

// delims carries the separators a compound value is split on. It travels as one
// value rather than as two string arguments so that adding a third separator
// later — should a nested shape ever need one — does not touch every call site.
//
// A zero delims means "use the defaults", which is what an untagged field has.
type delims struct {
	pair string // between slice elements, and between map entries
	kv   string // between a map key and its value
}

// newDelims normalises tag input, substituting the defaults for anything empty.
// Normalising once here is what lets decode and its recursive calls treat the
// separators as always-present.
func newDelims(pair, kv string) delims {
	if pair == "" {
		pair = defaultDelim
	}
	if kv == "" {
		kv = defaultKVDelim
	}
	return delims{pair: pair, kv: kv}
}

// The escape hatches, in priority order.
//
// encoding.TextUnmarshaler is the documented one, and it is what a user should
// implement. encoding.BinaryUnmarshaler is accepted as a fallback for one
// reason: *url.URL implements only the binary form (its UnmarshalBinary takes
// the URL text verbatim), and a URL is far too common in configuration to
// exclude over a stdlib inconsistency. Text is tried first so a type
// implementing both keeps its textual meaning.
var (
	textUnmarshalerType   = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
	binaryUnmarshalerType = reflect.TypeOf((*encoding.BinaryUnmarshaler)(nil)).Elem()
)

// durationType is special-cased before the integer branch: time.Duration has an
// integer kind, so without this check "30s" would fail to parse and "30" would
// silently mean 30 nanoseconds — a bug that looks like a working timeout.
var durationType = reflect.TypeOf(time.Duration(0))

// timeType is special-cased for the same reason: time.Time is a struct, and the
// struct branch would otherwise try to walk into its unexported fields.
var timeType = reflect.TypeOf(time.Time{})

// decode parses raw into dst, which must be a settable value.
//
// The returned error names the offending text and the target type but never the
// field — the caller owns the field path, and duplicating it here would produce
// messages that repeat themselves.
func decode(dst reflect.Value, raw string, d delims) error {
	// The two exact stdlib types are handled BEFORE the escape hatch, and only
	// because they are exact: a defined type does not inherit methods, so
	// `type MyTime time.Time` still reaches the hatch and a caller's own type
	// still wins over the built-in handling of its kind.
	//
	// time.Time implements encoding.TextUnmarshaler, so without this ordering
	// the timeType branch was DEAD CODE — a coverage report is what revealed
	// it. The accepted format was identical either way, but the diagnostic was
	// not: the hatch produced `"2026-09-08" is not a valid time.Time: parsing
	// time "2026-09-08" as "2006-01-02T15:04:05Z07:00": cannot parse "" as
	// "T"`, which tells an operator to go and read the Go time package.
	//
	// A registered Decoder still wins over both — decodeWith consults those
	// first — so the override seam is unaffected.
	switch dst.Type() {
	case durationType:
		// Named dur, not d: d is the separator set for this call, and shadowing
		// it here would make a later edit inside this branch silently wrong.
		dur, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("%q is not a valid duration", raw)
		}
		dst.SetInt(int64(dur))
		return nil
	case timeType:
		t, err := time.Parse(timeLayout, raw)
		if err != nil {
			return fmt.Errorf("%q is not a valid RFC3339 time", raw)
		}
		dst.Set(reflect.ValueOf(t))
		return nil
	}

	// Then the escape hatch, so any other user type wins over the built-in
	// handling of its underlying kind.
	if u, ok := unmarshalerFor(dst); ok {
		if err := u(raw); err != nil {
			return fmt.Errorf("%q is not a valid %s: %w", raw, dst.Type(), err)
		}
		return nil
	}

	switch dst.Kind() {
	case reflect.String:
		dst.SetString(raw)
		return nil

	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("%q is not a valid bool", raw)
		}
		dst.SetBool(b)
		return nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, dst.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not a valid %s", raw, dst.Kind())
		}
		dst.SetInt(n)
		return nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, dst.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not a valid %s", raw, dst.Kind())
		}
		dst.SetUint(n)
		return nil

	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(raw, dst.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not a valid %s", raw, dst.Kind())
		}
		dst.SetFloat(f)
		return nil

	case reflect.Slice:
		return decodeSlice(dst, raw, d)

	case reflect.Map:
		return decodeMap(dst, raw, d)

	case reflect.Pointer:
		// A pointer to a supported scalar: allocate, then decode into it. This
		// is how an "unset means nil, empty means set-to-empty" field is
		// expressed, which a bare string cannot distinguish.
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		return decode(dst.Elem(), raw, d)
	}

	return fmt.Errorf("unsupported type %s (implement encoding.TextUnmarshaler for it)", dst.Type())
}

// decodeSlice splits raw on delim and decodes each element.
//
// An empty string yields an EMPTY slice rather than a one-element slice holding
// "". "HOSTS=" means "no hosts", never "one host with no name" — the opposite
// reading silently produces an empty entry that fails much later.
func decodeSlice(dst reflect.Value, raw string, d delims) error {
	if raw == "" {
		dst.Set(reflect.MakeSlice(dst.Type(), 0, 0))
		return nil
	}

	parts := strings.Split(raw, d.pair)
	out := reflect.MakeSlice(dst.Type(), len(parts), len(parts))
	for i, p := range parts {
		// Elements are trimmed because "a, b" is what a human writes and
		// " b" is never the intended value.
		if err := decode(out.Index(i), strings.TrimSpace(p), d); err != nil {
			return fmt.Errorf("element %d: %w", i, err)
		}
	}
	dst.Set(out)
	return nil
}

// decodeMap splits raw into entries on d.pair, then each entry into a key and a
// value on d.kv, decoding both through the same decoder every other field uses.
// So map[string]time.Duration and map[string]int work with no extra code.
//
// Why maps exist at all: every other field type requires the NAME to be known
// when the struct is written. A map is for the case where it genuinely is not —
// feature flags, per-tenant limits, arbitrary extra headers — where a new entry
// must be addable by editing a .env file rather than the Go source.
//
// GOTCHA — the split is on the FIRST occurrence of d.kv only. A value is
// routinely a URL or a host:port, so "primary:postgres://db1" has three colons
// and only the first one is the separator. Splitting on all of them would break
// every connection string, which is the most common thing a map holds.
//
// An empty string yields an EMPTY map, matching decodeSlice: "TAGS=" means "no
// tags". A duplicate key takes its LAST value, matching how every source in the
// chain resolves a repeat.
func decodeMap(dst reflect.Value, raw string, d delims) error {
	t := dst.Type()
	out := reflect.MakeMap(t)
	if raw == "" {
		dst.Set(out)
		return nil
	}

	for i, entry := range strings.Split(raw, d.pair) {
		entry = strings.TrimSpace(entry)
		rawKey, rawVal, ok := strings.Cut(entry, d.kv)
		if !ok {
			// Naming the separator and the offending entry matters: the usual
			// cause is a value that itself contains the pair separator, and a
			// bare "invalid map" would send the reader to the wrong line.
			return fmt.Errorf("entry %d (%q) has no %q between key and value", i, entry, d.kv)
		}

		key := reflect.New(t.Key()).Elem()
		if err := decode(key, strings.TrimSpace(rawKey), d); err != nil {
			return fmt.Errorf("entry %d: key: %w", i, err)
		}
		val := reflect.New(t.Elem()).Elem()
		if err := decode(val, strings.TrimSpace(rawVal), d); err != nil {
			return fmt.Errorf("entry %d (key %q): %w", i, rawKey, err)
		}
		out.SetMapIndex(key, val)
	}

	dst.Set(out)
	return nil
}

// unmarshalerFor returns a decode function when v (or its address) implements
// one of the escape-hatch interfaces, allocating a nil pointer if needed.
//
// It returns a closure rather than an interface so the two hatches collapse to
// one call site; the caller does not care which one matched.
func unmarshalerFor(v reflect.Value) (func(string) error, bool) {
	// A pointer field: the pointer itself may carry the method set, in which
	// case it must be allocated before the method can be called.
	if v.Kind() == reflect.Pointer && isUnmarshaler(v.Type()) {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		return unmarshalFn(v), true
	}
	// A value field: the method set almost always lives on the pointer.
	if v.CanAddr() {
		if p := v.Addr(); isUnmarshaler(p.Type()) {
			return unmarshalFn(p), true
		}
	}
	return nil, false
}

// isUnmarshaler reports whether t implements either escape hatch.
func isUnmarshaler(t reflect.Type) bool {
	return t.Implements(textUnmarshalerType) || t.Implements(binaryUnmarshalerType)
}

// unmarshalFn binds v's unmarshal method, preferring the textual form.
func unmarshalFn(v reflect.Value) func(string) error {
	if u, ok := v.Interface().(encoding.TextUnmarshaler); ok {
		return func(raw string) error { return u.UnmarshalText([]byte(raw)) }
	}
	u := v.Interface().(encoding.BinaryUnmarshaler)
	return func(raw string) error { return u.UnmarshalBinary([]byte(raw)) }
}
