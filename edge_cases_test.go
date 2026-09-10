// Edge cases across the decoding surface: numeric boundaries, delimiter
// handling, value fidelity, and tag interactions.
//
// These exist because 100% statement coverage proves every line RAN, not that
// every input class was tried. Every behaviour pinned here was verified by
// running it, and several were undocumented until this file existed — so the
// point is as much to write the contract down as to defend it.
package cfgkit_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/cfgkit"
)

// bind is the shorthand every table below uses.
func edgeBind[T any](t *testing.T, kv map[string]string) (*T, error) {
	t.Helper()
	cfg, _, err := cfgkit.Load[T](cfgkit.WithSources(cfgkit.FromMap(kv)))
	return cfg, err
}

type edgeNumeric struct {
	I8  int8    `env:"I8"`
	U8  uint8   `env:"U8"`
	U   uint    `env:"U"`
	I64 int64   `env:"I64"`
	F   float64 `env:"F"`
}

// TestNumericBoundaries pins that a value too large for its Go type is an
// ERROR rather than a silent wrap.
//
// This is the failure mode that matters: int8 wrapping 128 to -128 would give
// a plausible-looking negative worker count that nothing downstream questions.
func TestNumericBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, key, val string
		wantErr        bool
	}{
		{"int8 at max", "I8", "127", false},
		{"int8 at min", "I8", "-128", false},
		{"int8 above max", "I8", "128", true},
		{"int8 below min", "I8", "-129", true},
		{"uint8 at max", "U8", "255", false},
		{"uint8 above max", "U8", "256", true},
		{"uint rejects negative", "U", "-1", true},
		{"int64 at max", "I64", "9223372036854775807", false},
		{"int64 above max", "I64", "9223372036854775808", true},
		{"float above max", "F", "1e400", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := edgeBind[edgeNumeric](t, map[string]string{tc.key: tc.val})
			if (err != nil) != tc.wantErr {
				t.Errorf("%s=%q err=%v, wantErr=%t", tc.key, tc.val, err, tc.wantErr)
			}
		})
	}
}

// TestNumberFormatIsGoStrconvNotShell pins which spellings are accepted.
//
// The set is strconv's, deliberately: a config file is not a Go source file,
// so "1_000" and "0x10" are NOT numbers here. Accepting them would mean two
// config formats disagreeing about what 0x10 means, and shells do not accept
// them either.
func TestNumberFormatIsGoStrconvNotShell(t *testing.T) {
	for _, tc := range []struct {
		name, val string
		wantErr   bool
	}{
		{"plain", "5", false},
		{"leading plus", "+5", false},
		{"underscores rejected", "1_000", true},
		{"hex rejected", "0x10", true},
		{"octal rejected", "0o17", true},
		{"surrounding spaces rejected", " 5 ", true},
		{"empty rejected", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := edgeBind[edgeNumeric](t, map[string]string{"I8": tc.val})
			if (err != nil) != tc.wantErr {
				t.Errorf("I8=%q err=%v, wantErr=%t", tc.val, err, tc.wantErr)
			}
		})
	}
}

// TestFloatSpecialsAreAccepted documents a SHARP EDGE rather than endorsing it.
//
// strconv.ParseFloat accepts "NaN" and "Inf", so cfgkit does too. That is
// consistent, and it is also a hazard worth knowing: NaN compares false to
// everything, so a NaN rate limit never trips and a NaN threshold never fires
// — with no error anywhere.
//
// It is not rejected here because rejecting would be a policy decision baked
// into the decoder, and some programs legitimately want +Inf as "no ceiling".
// The answer is a validation rule; this test exists so the behaviour is a
// documented choice instead of a surprise.
func TestFloatSpecialsAreAccepted(t *testing.T) {
	for _, tc := range []struct {
		val   string
		check func(float64) bool
	}{
		{"NaN", math.IsNaN},
		{"Inf", func(f float64) bool { return math.IsInf(f, 1) }},
		{"+Inf", func(f float64) bool { return math.IsInf(f, 1) }},
		{"-Inf", func(f float64) bool { return math.IsInf(f, -1) }},
	} {
		t.Run(tc.val, func(t *testing.T) {
			cfg, err := edgeBind[edgeNumeric](t, map[string]string{"F": tc.val})
			if err != nil {
				t.Fatalf("F=%q was rejected: %v", tc.val, err)
			}
			if !tc.check(cfg.F) {
				t.Errorf("F=%q decoded to %v", tc.val, cfg.F)
			}
		})
	}
}

type edgeBool struct {
	B bool `env:"B"`
}

// TestBoolVocabularyIsStrconvNotYAML pins which words are booleans.
//
// "yes"/"no"/"on"/"off" are REJECTED. YAML accepts them and that is famously
// how a country code NO became false; taking strconv's narrower set means a
// value either parses the way Go parses it or fails loudly.
func TestBoolVocabularyIsStrconvNotYAML(t *testing.T) {
	accepted := map[string]bool{
		"1": true, "t": true, "T": true, "true": true, "TRUE": true, "True": true,
		"0": false, "f": false, "F": false, "false": false, "FALSE": false, "False": false,
	}
	for val, want := range accepted {
		t.Run("accepts_"+val, func(t *testing.T) {
			cfg, err := edgeBind[edgeBool](t, map[string]string{"B": val})
			if err != nil {
				t.Fatalf("B=%q rejected: %v", val, err)
			}
			if cfg.B != want {
				t.Errorf("B=%q = %t, want %t", val, cfg.B, want)
			}
		})
	}
	for _, val := range []string{"yes", "no", "on", "off", "y", "n", "", " true"} {
		t.Run("rejects_"+val, func(t *testing.T) {
			if _, err := edgeBind[edgeBool](t, map[string]string{"B": val}); err == nil {
				t.Errorf("B=%q was accepted; the vocabulary must stay strconv's", val)
			}
		})
	}
}

type edgeDuration struct {
	D time.Duration `env:"D"`
}

// TestDurationRequiresAUnit pins that a bare number is NOT a duration.
//
// "500" is the dangerous input: a reader assumes milliseconds, Go's zero-value
// semantics would make it 500 nanoseconds, and either way somebody is wrong.
// Requiring a unit makes the file say what it means. "0" is the one exception
// Go itself allows, because zero is unitless.
func TestDurationRequiresAUnit(t *testing.T) {
	for _, tc := range []struct {
		val     string
		wantErr bool
	}{
		{"30s", false},
		{"1h30m", false},
		{"-5s", false},
		{"0", false},
		{"500", true},
		{"1d", true}, // Go has no day unit; a month has no fixed length either
		{"", true},
		{"  10s  ", true},
	} {
		t.Run(tc.val, func(t *testing.T) {
			_, err := edgeBind[edgeDuration](t, map[string]string{"D": tc.val})
			if (err != nil) != tc.wantErr {
				t.Errorf("D=%q err=%v, wantErr=%t", tc.val, err, tc.wantErr)
			}
		})
	}
}

type edgeSlice struct {
	S []string `env:"S" delim:","`
	I []int    `env:"I" delim:","`
}

// TestSliceSplittingIsFaithful pins exactly what the delimiter produces,
// including the cases that surprise people.
//
// An empty value yields an EMPTY slice, not a one-element slice containing "".
// A trailing delimiter DOES yield a trailing empty element, because "a," is
// two fields in every other tool that splits on a comma — and silently
// dropping it would mean a list whose length depends on trailing punctuation.
func TestSliceSplittingIsFaithful(t *testing.T) {
	for _, tc := range []struct {
		name, val string
		want      []string
	}{
		{"empty is no elements", "", []string{}},
		{"single", "a", []string{"a"}},
		{"two", "a,b", []string{"a", "b"}},
		{"elements are trimmed", "a , b", []string{"a", "b"}},
		{"trailing delimiter keeps an empty", "a,", []string{"a", ""}},
		{"leading delimiter keeps an empty", ",a", []string{"", "a"}},
		{"double delimiter keeps an empty", "a,,b", []string{"a", "", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := edgeBind[edgeSlice](t, map[string]string{"S": tc.val})
			if err != nil {
				t.Fatalf("S=%q: %v", tc.val, err)
			}
			if len(cfg.S) != len(tc.want) {
				t.Fatalf("S=%q -> %q (%d elements), want %q (%d)",
					tc.val, cfg.S, len(cfg.S), tc.want, len(tc.want))
			}
			for i := range tc.want {
				if cfg.S[i] != tc.want[i] {
					t.Errorf("S=%q element %d = %q, want %q", tc.val, i, cfg.S[i], tc.want[i])
				}
			}
		})
	}
}

// TestOneBadElementFailsTheWholeSlice pins that a typed slice is all-or-nothing.
//
// Binding [1, 3] from "1,x,3" and dropping the bad one would hand the program
// a shorter list than the operator wrote, which is worse than not starting.
func TestOneBadElementFailsTheWholeSlice(t *testing.T) {
	for _, val := range []string{"1,x,3", "1,,3"} {
		t.Run(val, func(t *testing.T) {
			if _, err := edgeBind[edgeSlice](t, map[string]string{"I": val}); err == nil {
				t.Errorf("I=%q was accepted; a partial list must not bind", val)
			}
		})
	}
}

type edgeMap struct {
	M map[string]string `env:"M" delim:"," kvdelim:"="`
	N map[string]int    `env:"N" delim:"," kvdelim:"="`
}

// TestMapSplittingIsFaithful pins the pair grammar, including the two cases
// that decide whether a map is usable for real data.
//
// A value containing the kv delimiter splits on the FIRST one only, so
// "url=postgres://h/db?x=1" keeps its query string. A repeated key takes the
// LAST value, matching how every environment and .env file already behaves.
func TestMapSplittingIsFaithful(t *testing.T) {
	for _, tc := range []struct {
		name, val string
		want      map[string]string
	}{
		{"empty is no entries", "", map[string]string{}},
		{"one pair", "a=1", map[string]string{"a": "1"}},
		{"pairs are trimmed", " a = 1 ", map[string]string{"a": "1"}},
		{"empty key is kept", "=1", map[string]string{"": "1"}},
		{"empty value is kept", "a=", map[string]string{"a": ""}},
		{"last duplicate wins", "a=1,a=2", map[string]string{"a": "2"}},
		{"splits on the first delimiter only", "a=1=2", map[string]string{"a": "1=2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := edgeBind[edgeMap](t, map[string]string{"M": tc.val})
			if err != nil {
				t.Fatalf("M=%q: %v", tc.val, err)
			}
			if len(cfg.M) != len(tc.want) {
				t.Fatalf("M=%q -> %v, want %v", tc.val, cfg.M, tc.want)
			}
			for k, v := range tc.want {
				if cfg.M[k] != v {
					t.Errorf("M=%q[%q] = %q, want %q", tc.val, k, cfg.M[k], v)
				}
			}
		})
	}
}

// TestMapEntryWithoutADelimiterIsAnError pins that a malformed pair fails
// rather than binding a key with an empty value — the reading that would make
// a typo'd "a1" indistinguishable from a deliberately empty "a=".
func TestMapEntryWithoutADelimiterIsAnError(t *testing.T) {
	if _, err := edgeBind[edgeMap](t, map[string]string{"M": "abc"}); err == nil {
		t.Error("a pair with no kv delimiter was accepted")
	}
}

type edgeValue struct {
	V string `env:"V"`
}

// TestStringValuesSurviveVerbatim pins that a string field is a byte pipe.
//
// Configuration carries certificates, JSON blobs, base64 and binary-ish
// secrets. Any normalisation — validating UTF-8, trimming, collapsing
// newlines — would corrupt one of those silently, which is far worse than
// passing bytes through unexamined.
func TestStringValuesSurviveVerbatim(t *testing.T) {
	for _, tc := range []struct{ name, val string }{
		{"invalid utf8", "\xff\xfe"},
		{"embedded NUL", "a\x00b"},
		{"embedded newline", "line1\nline2"},
		{"carriage return", "a\r\nb"},
		{"emoji", "🔥 fire"},
		{"only spaces", "   "},
		{"leading and trailing spaces", "  padded  "},
		{"one MiB", strings.Repeat("x", 1<<20)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := edgeBind[edgeValue](t, map[string]string{"V": tc.val})
			if err != nil {
				t.Fatalf("V=%q: %v", tc.name, err)
			}
			if cfg.V != tc.val {
				t.Errorf("%s: value was altered (got %d bytes, want %d)",
					tc.name, len(cfg.V), len(tc.val))
			}
		})
	}
}

// TestWhitespaceIsTrimmedInsideListsButNotInScalars pins an ASYMMETRY that is
// easy to trip over and was previously undocumented.
//
// A scalar is a byte pipe: " 8080" stays " 8080" and fails to parse as an int,
// because trimming a string field would corrupt a value whose spaces matter.
// Inside a list the delimiter already implies structure, so elements are
// trimmed and "a, b" means what it looks like.
func TestWhitespaceIsTrimmedInsideListsButNotInScalars(t *testing.T) {
	// Scalar: not trimmed, so a padded number does not parse.
	if _, err := edgeBind[edgeNumeric](t, map[string]string{"I8": " 5"}); err == nil {
		t.Error("a padded scalar parsed; scalars must not be trimmed")
	}
	// Scalar string: padding is preserved exactly.
	cfg, err := edgeBind[edgeValue](t, map[string]string{"V": " x "})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.V != " x " {
		t.Errorf("scalar string was trimmed: %q", cfg.V)
	}
	// List: elements ARE trimmed.
	sl, err := edgeBind[edgeSlice](t, map[string]string{"S": " a , b "})
	if err != nil {
		t.Fatal(err)
	}
	if len(sl.S) != 2 || sl.S[0] != "a" || sl.S[1] != "b" {
		t.Errorf("list elements were not trimmed: %q", sl.S)
	}
}

type edgeAlias struct {
	Port int `env:"PORT" was:"OLD_PORT,ANCIENT_PORT"`
}

// TestFormerKeyNamesResolveInOrder pins the migration path.
//
// The current name always wins. Among former names the FIRST listed wins, so
// the tag reads newest-to-oldest and a two-step rename resolves predictably.
// A former key is never reported as an unknown key — that is the whole point,
// since a deployment still on the old name is not making a typo.
func TestFormerKeyNamesResolveInOrder(t *testing.T) {
	for _, tc := range []struct {
		name string
		kv   map[string]string
		want int
	}{
		{"first former name", map[string]string{"OLD_PORT": "1"}, 1},
		{"second former name", map[string]string{"ANCIENT_PORT": "2"}, 2},
		{"current name beats former", map[string]string{"PORT": "3", "OLD_PORT": "4"}, 3},
		{"earlier former beats later", map[string]string{"OLD_PORT": "5", "ANCIENT_PORT": "6"}, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, res, err := cfgkit.Load[edgeAlias](cfgkit.WithSources(cfgkit.FromMap(tc.kv)))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Port != tc.want {
				t.Errorf("Port = %d, want %d", cfg.Port, tc.want)
			}
			if n := len(res.Unknown()); n != 0 {
				t.Errorf("a former key was reported as unknown: %v", res.Unknown())
			}
		})
	}
}

// TestDeprecatedKeyIsVisibleInProvenance pins that using an old name is
// reported rather than silently honoured. Silent acceptance means a rename
// nobody ever finishes.
func TestDeprecatedKeyIsVisibleInProvenance(t *testing.T) {
	_, res, err := cfgkit.Load[edgeAlias](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"OLD_PORT": "1"}),
	))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	fields := res.Fields()
	if len(fields) != 1 {
		t.Fatalf("got %d fields, want 1", len(fields))
	}
	if !strings.Contains(fields[0].Source, "OLD_PORT") {
		t.Errorf("provenance does not name the deprecated key: %q", fields[0].Source)
	}
}

type edgeSharedKey struct {
	A string `env:"SHARED"`
	B string `env:"SHARED"`
}

// TestTwoFieldsMaySharaAKey pins that binding one key to two fields is allowed
// and gives both the same value.
//
// It is not rejected because the legitimate use is real — one value needed by
// two subsystems that each own their own struct — and a load-time error would
// forbid it for no safety gain. Both fields being equal is the contract.
func TestTwoFieldsMaySharaAKey(t *testing.T) {
	cfg, res, err := cfgkit.Load[edgeSharedKey](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"SHARED": "v"}),
	))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.A != "v" || cfg.B != "v" {
		t.Errorf("cfg = %+v; both fields should carry the value", cfg)
	}
	if len(res.Fields()) != 2 {
		t.Errorf("got %d fields, want both reported separately", len(res.Fields()))
	}
	if n := len(res.Unknown()); n != 0 {
		t.Errorf("a shared key was reported unknown: %v", res.Unknown())
	}
}

type edgeDeep5 struct {
	V string `env:"DEEP_V" default:"deep"`
}
type edgeDeep4 struct{ D5 edgeDeep5 }
type edgeDeep3 struct{ D4 edgeDeep4 }
type edgeDeep2 struct{ D3 edgeDeep3 }
type edgeDeep1 struct{ D2 edgeDeep2 }

// TestDeepNestingKeepsTheGoPathAndFlatKey pins that nesting depth does not
// change the key. The Go path grows for provenance; the key stays flat,
// because a .env file has no nesting to mirror.
func TestDeepNestingKeepsTheGoPathAndFlatKey(t *testing.T) {
	cfg, res, err := cfgkit.Load[edgeDeep1](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"DEEP_V": "set"}),
	))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.D2.D3.D4.D5.V != "set" {
		t.Errorf("five levels down = %q, want set", cfg.D2.D3.D4.D5.V)
	}
	fields := res.Fields()
	if len(fields) != 1 {
		t.Fatalf("got %d fields, want 1", len(fields))
	}
	if fields[0].Path != "D2.D3.D4.D5.V" {
		t.Errorf("Path = %q, want the full Go path", fields[0].Path)
	}
	if fields[0].Key != "DEEP_V" {
		t.Errorf("Key = %q, want the flat key regardless of depth", fields[0].Key)
	}
}

// TestListElementsAreTrimmedOfAllWhitespaceNotJustSpaces pins the behaviour a
// fuzz target surfaced: "a\r" in a list becomes "a".
//
// It matters for a CRLF file. `ORIGINS=a,b` written on Windows arrives with a
// trailing carriage return, and trimming only the space character would give
// a final element of "b\r" — an origin that matches nothing, with no error to
// explain why. TrimSpace's fuller definition is what makes the same file work
// on both platforms.
func TestListElementsAreTrimmedOfAllWhitespaceNotJustSpaces(t *testing.T) {
	for _, tc := range []struct {
		name, val string
		want      []string
	}{
		{"carriage return", "a,b\r", []string{"a", "b"}},
		{"tab", "a,\tb", []string{"a", "b"}},
		{"newline", "a,b\n", []string{"a", "b"}},
		{"crlf", "a,b\r\n", []string{"a", "b"}},
		{"mixed", " a ,\tb\r", []string{"a", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := edgeBind[edgeSlice](t, map[string]string{"S": tc.val})
			if err != nil {
				t.Fatalf("S=%q: %v", tc.val, err)
			}
			if len(cfg.S) != len(tc.want) {
				t.Fatalf("S=%q -> %q, want %q", tc.val, cfg.S, tc.want)
			}
			for i := range tc.want {
				if cfg.S[i] != tc.want[i] {
					t.Errorf("S=%q element %d = %q, want %q", tc.val, i, cfg.S[i], tc.want[i])
				}
			}
		})
	}

	// The contrast that makes it a rule rather than an accident: a SCALAR
	// string keeps its carriage return, because there is no delimiter implying
	// structure and the bytes might matter.
	cfg, err := edgeBind[edgeValue](t, map[string]string{"V": "a\r"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.V != "a\r" {
		t.Errorf("scalar string was trimmed: %q", cfg.V)
	}
}
