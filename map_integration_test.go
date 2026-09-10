// Map fields crossed with every other feature in the library.
//
// map_test.go pins the decoder itself. This file pins the INTERACTIONS, which
// is where a new type usually breaks things: provenance, masking, prefixes,
// structured sources, precedence, the capability hooks, and the generated
// contract file. A type that decodes correctly but renders a secret, or that
// silently merges instead of replacing, is still broken.
package cfgkit_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ubgo/cfgkit"
)

// ---------------------------------------------------------------------------
// Sources: every built-in source must be able to supply a map.
// ---------------------------------------------------------------------------

// TestMapFromDotenvFile is the primary real-world path.
//
// It also pins a GOTCHA that surprised the author of this test: quoting a value
// in the .env file does NOT protect a comma from the map splitter. dotenv
// resolves quoting first and hands cfgkit one flat string, by which point the
// quotes are gone and every comma separates an entry. Use `delim:` when values
// legitimately contain commas.
func TestMapFromDotenvFile(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	if err := os.WriteFile(env, []byte(
		"FLAGS=new-checkout:on,dark-mode:off\n"+
			"LIMITS=acme:1000,globex:500\n"+
			"DSNS=primary:postgres://u@h:5432/db\n"+
			`NOTES="greeting:hello, world"`+"\n"+
			"PIPED=greeting:hello, world|farewell:bye\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	type cfg struct {
		Flags  map[string]string `env:"FLAGS"`
		Limits map[string]int    `env:"LIMITS"`
		DSNs   map[string]string `env:"DSNS"`
		Piped  map[string]string `env:"PIPED" delim:"|"`
	}
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(cfgkit.FromFiles(env)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Flags["new-checkout"] != "on" || got.Flags["dark-mode"] != "off" {
		t.Errorf("Flags = %#v", got.Flags)
	}
	if got.Limits["acme"] != 1000 {
		t.Errorf("Limits = %#v", got.Limits)
	}
	if got.DSNs["primary"] != "postgres://u@h:5432/db" {
		t.Errorf("DSNs = %#v, want the colons inside the value preserved", got.DSNs)
	}
	// The remedy for a comma inside a value: a different entry separator.
	if got.Piped["greeting"] != "hello, world" {
		t.Errorf("Piped = %#v, want the comma kept as part of the value", got.Piped)
	}
}

// TestMapQuotingDoesNotProtectTheSeparator states the gotcha above as its own
// failing case, so the reason for `delim:` is impossible to miss.
func TestMapQuotingDoesNotProtectTheSeparator(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	if err := os.WriteFile(env, []byte(`NOTES="greeting:hello, world"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	type cfg struct {
		Notes map[string]string `env:"NOTES"`
	}
	err := cfgkit.Check[cfg](cfgkit.WithSources(cfgkit.FromFiles(env)))
	if err == nil {
		t.Fatal("want an error: the quoted comma still splits, leaving \" world\" with no separator")
	}
	if !strings.Contains(err.Error(), "world") {
		t.Errorf("error %q should name the fragment that has no separator", err)
	}
}

func TestMapFromEnviron(t *testing.T) {
	t.Setenv("MI_FLAGS", "a:1,b:2")

	type cfg struct {
		Flags map[string]string `env:"MI_FLAGS"`
	}
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(cfgkit.FromEnviron()))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Flags) != 2 {
		t.Errorf("Flags = %#v", got.Flags)
	}
}

func TestMapFromPrefixedEnviron(t *testing.T) {
	t.Setenv("SVCM_FLAGS", "a:1")

	type cfg struct {
		Flags map[string]string `env:"FLAGS"`
	}
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(cfgkit.FromPrefixedEnviron("SVCM_")))
	if err != nil {
		t.Fatal(err)
	}
	if got.Flags["a"] != "1" {
		t.Errorf("Flags = %#v", got.Flags)
	}
}

// TestMapFromJSONIsNative is the important asymmetry: a structured source fills
// a map through encoding/json, so it uses REAL nesting and never sees the
// "k:v,k:v" text form at all. The two source kinds reach the same field by two
// different mechanisms, which is the whole §4 design.
func TestMapFromJSONIsNative(t *testing.T) {
	type cfg struct {
		Flags map[string]string `env:"FLAGS" json:"flags"`
	}
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromJSON([]byte(`{"flags":{"new-checkout":"on","dark-mode":"off"}}`)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Flags["new-checkout"] != "on" || got.Flags["dark-mode"] != "off" {
		t.Errorf("Flags = %#v, want the JSON object bound natively", got.Flags)
	}
}

// TestMapFromFlagSet covers the flag path, where a map is typed on a command
// line as the same packed string a .env file holds.
func TestMapFromFlagSet(t *testing.T) {
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	fs.String("flags", "", "feature flags")
	if err := fs.Parse([]string{"-flags", "beta:on"}); err != nil {
		t.Fatal(err)
	}

	type cfg struct {
		Flags map[string]string `env:"FLAGS" flag:"flags"`
	}
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"FLAGS": "beta:off"}),
		cfgkit.FromFlagSet(fs),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Flags["beta"] != "on" {
		t.Errorf("Flags = %#v, want the flag to win", got.Flags)
	}
}

// TestMapFromFileOption covers `env:",file"` — the Docker/Kubernetes mounted
// secret path — with a map of credentials.
func TestMapFromFileOption(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "keys")
	// The trailing newline is what every tool writes and must be trimmed
	// BEFORE the map is split, or the last value would carry it.
	if err := os.WriteFile(secret, []byte("stripe:sk_test_1,aws:AKIA2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	type cfg struct {
		Keys map[string]string `env:"API_KEYS_FILE,file" secret:"true"`
	}
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"API_KEYS_FILE": secret}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Keys["aws"] != "AKIA2" {
		t.Errorf("Keys[aws] = %q, want the newline trimmed before splitting", got.Keys["aws"])
	}
}

// ---------------------------------------------------------------------------
// Precedence: a map REPLACES, it never merges.
// ---------------------------------------------------------------------------

// TestMapReplacesRatherThanMerges is the behaviour most likely to surprise, so
// it is pinned explicitly. The whole map arrives in one value, so a later
// source supplying FLAGS replaces every entry — it cannot add one.
//
// The consequence for an operator: to change one flag in production you must
// restate the whole set. That is a real cost, and it is the price of the map
// shape; a named bool field per flag does not have it.
func TestMapReplacesRatherThanMerges(t *testing.T) {
	type cfg struct {
		Flags map[string]string `env:"FLAGS"`
	}
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"FLAGS": "a:1,b:2,c:3"}),
		cfgkit.FromMap(map[string]string{"FLAGS": "a:9"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Flags) != 1 || got.Flags["a"] != "9" {
		t.Errorf("Flags = %#v, want ONLY the later source's entries", got.Flags)
	}
}

// TestMapStructuredThenFlatPrecedence pins that the two source kinds compose in
// the documented order: structured sources merge onto the struct first, then
// flat sources bind over the top.
func TestMapStructuredThenFlatPrecedence(t *testing.T) {
	type cfg struct {
		Flags map[string]string `env:"FLAGS" json:"flags"`
	}
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromJSON([]byte(`{"flags":{"from":"json"}}`)),
		cfgkit.FromMap(map[string]string{"FLAGS": "from:flat"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Flags["from"] != "flat" {
		t.Errorf("Flags = %#v, want the flat source to win", got.Flags)
	}
}

// TestMapWasTagBindsFromAFormerKey covers the rename path for a map field.
func TestMapWasTagBindsFromAFormerKey(t *testing.T) {
	type cfg struct {
		Flags map[string]string `env:"FEATURE_FLAGS" was:"FLAGS"`
	}
	got, res, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"FLAGS": "legacy:on"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Flags["legacy"] != "on" {
		t.Errorf("Flags = %#v, want the former key honoured", got.Flags)
	}
	if u := res.Unknown(); len(u) != 0 {
		t.Errorf("Unknown() = %v, want empty — a `was:` name is wanted, not a typo", u)
	}
}

// ---------------------------------------------------------------------------
// Structure: prefixes, sections, embedding.
// ---------------------------------------------------------------------------

type mapSection struct {
	Flags map[string]string `env:"FLAGS"`
	Port  int               `env:"PORT" default:"8080"`
}

// TestMapInsidePrefixedSection pins that a map is a leaf that still collects
// its parent's prefix, exactly like a scalar.
func TestMapInsidePrefixedSection(t *testing.T) {
	type cfg struct {
		Svc mapSection `env:",prefix=SVC_"`
	}
	got, res, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"SVC_FLAGS": "a:1"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Svc.Flags["a"] != "1" {
		t.Errorf("Flags = %#v", got.Svc.Flags)
	}
	var key string
	for _, f := range res.Fields() {
		if f.Path == "Svc.Flags" {
			key = f.Key
		}
	}
	if key != "SVC_FLAGS" {
		t.Errorf("key = %q, want the prefix applied", key)
	}
}

// TestMapAloneAllocatesAnOptionalSection crosses maps with §6.4. A map is the
// only field in the section, so if the pruner did not count it as "a source set
// a descendant", the section would be wrongly discarded and the configured
// feature would silently not start.
func TestMapAloneAllocatesAnOptionalSection(t *testing.T) {
	type feature struct {
		Flags map[string]string `env:"FLAGS"`
	}
	type cfg struct {
		Feature *feature `env:",prefix=FEAT_"`
	}

	t.Run("set", func(t *testing.T) {
		got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"FEAT_FLAGS": "a:1"}),
		))
		if err != nil {
			t.Fatal(err)
		}
		if got.Feature == nil {
			t.Fatal("Feature = nil, but a source set the map beneath it")
		}
		if got.Feature.Flags["a"] != "1" {
			t.Errorf("Flags = %#v", got.Feature.Flags)
		}
	})

	t.Run("unset", func(t *testing.T) {
		got, _, err := cfgkit.Load[cfg]()
		if err != nil {
			t.Fatal(err)
		}
		if got.Feature != nil {
			t.Errorf("Feature = %#v, want nil when nothing set it", got.Feature)
		}
	})
}

// TestMapIsNotConfusedWithAStructSection guards the walker: both a map and a
// struct are "a container of named things", and a walker that descended into a
// map would give the field no key and bind it from nothing.
func TestMapIsNotConfusedWithAStructSection(t *testing.T) {
	type cfg struct {
		Flags map[string]string `env:"FLAGS"`
		Svc   mapSection        `env:",prefix=SVC_"`
	}
	_, res, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"FLAGS": "a:1", "SVC_FLAGS": "b:2"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"Flags": "FLAGS", "Svc.Flags": "SVC_FLAGS", "Svc.Port": "SVC_PORT"}
	got := map[string]string{}
	for _, f := range res.Fields() {
		got[f.Path] = f.Key
	}
	for path, key := range want {
		if got[path] != key {
			t.Errorf("field %s bound to key %q, want %q", path, got[path], key)
		}
	}
	if len(got) != len(want) {
		t.Errorf("bound %d fields (%v), want %d — a map must contribute exactly one", len(got), got, len(want))
	}
}

// ---------------------------------------------------------------------------
// Provenance and reporting.
// ---------------------------------------------------------------------------

func TestMapProvenanceNamesItsSource(t *testing.T) {
	type cfg struct {
		Flags map[string]string `env:"FLAGS" default:"a:0"`
	}
	_, res, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"FLAGS": "a:1"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Fields() {
		if f.Path == "Flags" && f.Source != "map" {
			t.Errorf("Flags source = %q, want map", f.Source)
		}
	}
}

// TestMapSecretNeverLeaks is the security case, checked on every surface that
// can carry a value out of the process: Explain, JSON, and the error text.
//
// Key NAMES are masked along with values, because a key name like
// "stripe-live" identifies an account even when its value is hidden.
func TestMapSecretNeverLeaks(t *testing.T) {
	type cfg struct {
		Keys map[string]string `env:"API_KEYS" secret:"true"`
	}
	const value = "stripe-live:sk_live_abc,aws-prod:AKIAsecret"

	_, res, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"API_KEYS": value}),
	))
	if err != nil {
		t.Fatal(err)
	}

	var explain bytes.Buffer
	if err := res.Explain(&explain); err != nil {
		t.Fatal(err)
	}
	raw, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}

	// A decode failure on a secret map must not echo the text either.
	decodeErr := cfgkit.Check[struct {
		Keys map[string]int `env:"API_KEYS" secret:"true"`
	}](cfgkit.WithSources(cfgkit.FromMap(map[string]string{"API_KEYS": value})))
	if decodeErr == nil {
		t.Fatal("want a decode error for an int-valued map given text")
	}

	surfaces := map[string]string{
		"Explain": explain.String(),
		"JSON":    string(raw),
		"error":   decodeErr.Error(),
	}
	for name, out := range surfaces {
		for _, leak := range []string{"sk_live_abc", "AKIAsecret", "stripe-live", "aws-prod"} {
			if strings.Contains(out, leak) {
				t.Errorf("%s leaked %q:\n%s", name, leak, out)
			}
		}
	}
}

// TestMapRevealShowsTheWholeMap pins the other side: with Reveal the operator
// asked for the values, and all of them must appear.
func TestMapRevealShowsTheWholeMap(t *testing.T) {
	type cfg struct {
		Keys map[string]string `env:"API_KEYS" secret:"true"`
	}
	_, res, err := cfgkit.Load[cfg](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"API_KEYS": "a:1,b:2"})),
		cfgkit.Reveal(),
	)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := res.Explain(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "a:1,b:2") {
		t.Errorf("Explain with Reveal = %q, want the map rendered in full", buf.String())
	}
}

// TestMapInJSONResultIsAString pins the wire shape of Result.JSON: every value
// is the rendered TEXT, not a nested object. A consumer diffing two provenance
// records compares strings for every field type, maps included.
func TestMapInJSONResultIsAString(t *testing.T) {
	type cfg struct {
		Flags map[string]string `env:"FLAGS"`
	}
	_, res, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"FLAGS": "b:2,a:1"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Fields []struct {
			Path  string `json:"path"`
			Value string `json:"value"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Result.JSON is not valid JSON with a string value: %v", err)
	}
	for _, f := range decoded.Fields {
		if f.Path == "Flags" && f.Value != "a:1,b:2" {
			t.Errorf("Flags value = %q, want the sorted text form", f.Value)
		}
	}
}

// TestMapDocumentOutputReloads is the contract-file property. Document
// generates .env.example, CI diffs it, and an operator copies it — so what it
// prints must be something Load can read back.
func TestMapDocumentOutputReloads(t *testing.T) {
	type cfg struct {
		Flags  map[string]string `env:"FLAGS" default:"b:2,a:1" doc:"feature toggles"`
		Limits map[string]int    `env:"LIMITS" default:"acme:1000"`
	}

	var out bytes.Buffer
	if err := cfgkit.Document[cfg](&out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "FLAGS=a:1,b:2") {
		t.Errorf("Document output %q must carry the sorted default", text)
	}

	// Parse the generated file back and load from it — the real round trip.
	dir := t.TempDir()
	env := filepath.Join(dir, ".env.example")
	if err := os.WriteFile(env, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(cfgkit.FromFiles(env)))
	if err != nil {
		t.Fatalf("the generated contract file does not load: %v", err)
	}
	if got.Flags["a"] != "1" || got.Flags["b"] != "2" || got.Limits["acme"] != 1000 {
		t.Errorf("round trip through Document lost data: %#v %#v", got.Flags, got.Limits)
	}
}

// TestMapDocumentIsStableAcrossRuns is the reason for sorting, stated as the CI
// check it protects: `git diff --exit-code .env.example`.
func TestMapDocumentIsStableAcrossRuns(t *testing.T) {
	type cfg struct {
		Flags map[string]string `env:"FLAGS" default:"z:1,a:2,m:3,q:4,c:5"`
	}
	var first string
	for i := range 20 {
		var out bytes.Buffer
		if err := cfgkit.Document[cfg](&out); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = out.String()
			continue
		}
		if out.String() != first {
			t.Fatalf("run %d differs from run 0 — a contract diff would fail at random:\n%q\n%q", i, out.String(), first)
		}
	}
}

// TestMapUnknownKeysStillWork pins that adding maps did not disturb §4.3.
func TestMapUnknownKeysStillWork(t *testing.T) {
	type cfg struct {
		Flags map[string]string `env:"FLAGS"`
	}
	_, res, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"FLAGS": "a:1", "FLGAS": "typo:1"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	u := res.Unknown()
	if len(u) != 1 || u[0].Key != "FLGAS" {
		t.Errorf("Unknown() = %v, want just the typo", u)
	}
}

// ---------------------------------------------------------------------------
// Requirements and validation.
// ---------------------------------------------------------------------------

func TestMapRequiredAndNotEmpty(t *testing.T) {
	type required struct {
		Flags map[string]string `env:"FLAGS,required"`
	}
	type notEmpty struct {
		Flags map[string]string `env:"FLAGS,notempty"`
	}

	t.Run("required fires when no source supplies the key", func(t *testing.T) {
		if err := cfgkit.Check[required](); err == nil {
			t.Error("want a required error")
		}
	})

	t.Run("required is satisfied by an EMPTY map", func(t *testing.T) {
		// The key resolved, so `required` passes even though the map has no
		// entries — the same rule as PORT=0 in §4.4. Use notempty, or a check
		// in Validate, when entries are what you actually need.
		if err := cfgkit.Check[required](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"FLAGS": ""}),
		)); err != nil {
			t.Errorf("required must pass on an empty-but-present value: %v", err)
		}
	})

	t.Run("notempty rejects the empty map", func(t *testing.T) {
		if err := cfgkit.Check[notEmpty](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"FLAGS": ""}),
		)); err == nil {
			t.Error("want a notempty error")
		}
	})
}

type validatedMapCfg struct {
	Limits map[string]int `env:"LIMITS"`
}

// Validate shows the documented pattern for a per-entry requirement: a map
// cannot express it in a tag, because the entry names are unknown by design.
func (c *validatedMapCfg) Validate() error {
	if _, ok := c.Limits["default"]; !ok {
		return cfgkit.Required("Limits", "") // reuse the standard error shape
	}
	return nil
}

func TestMapPerEntryValidation(t *testing.T) {
	if err := cfgkit.Check[validatedMapCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"LIMITS": "acme:10"}),
	)); err == nil {
		t.Error("want a validation error when the required entry is missing")
	}
	if err := cfgkit.Check[validatedMapCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"LIMITS": "default:1,acme:10"}),
	)); err != nil {
		t.Errorf("want success when the entry is present: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Capability hooks.
// ---------------------------------------------------------------------------

// TestMapTransformerRewritesTheRawText pins that a Transformer sees the packed
// string BEFORE it is split, which is what makes templating a map possible.
func TestMapTransformerRewritesTheRawText(t *testing.T) {
	type cfg struct {
		Flags map[string]string `env:"FLAGS"`
	}
	got, res, err := cfgkit.Load[cfg](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"FLAGS": "a:PLACEHOLDER"})),
		cfgkit.WithTransform(cfgkit.TransformerFunc(func(key, value, source string) (string, error) {
			return strings.ReplaceAll(value, "PLACEHOLDER", "resolved"), nil
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Flags["a"] != "resolved" {
		t.Errorf("Flags = %#v, want the transformed text", got.Flags)
	}
	// The firewall: a transformer changes the value, never the origin.
	for _, f := range res.Fields() {
		if f.Path == "Flags" && f.Source != "map" {
			t.Errorf("source = %q, a transformer must not change provenance", f.Source)
		}
	}
}

// TestMapObserverSeesTheRenderedMap pins that an audit sink gets a usable
// value for a map field rather than an empty string.
func TestMapObserverSeesTheRenderedMap(t *testing.T) {
	type cfg struct {
		Flags map[string]string `env:"FLAGS"`
	}
	var seen string
	_, _, err := cfgkit.Load[cfg](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"FLAGS": "b:2,a:1"})),
		cfgkit.WithObserver(cfgkit.ObserverFunc(func(f cfgkit.Field) {
			if f.Path == "Flags" {
				seen = f.Value
			}
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	if seen != "a:1,b:2" {
		t.Errorf("observer saw %q, want the sorted rendered map", seen)
	}
}

// jsonMapDecoder claims map[string]string outright, proving a caller can
// replace the built-in format wholesale — here, to accept a JSON object.
func jsonMapDecoder() cfgkit.Decoder {
	want := reflect.TypeOf(map[string]string{})
	return cfgkit.DecoderFunc(func(typ reflect.Type, raw string) (any, bool, error) {
		if typ != want {
			return nil, false, nil // decline; the core handles it
		}
		m := map[string]string{}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, true, err // claimed, and the value is bad
		}
		return m, true, nil
	})
}

// TestMapCustomDecoderWins pins that the escape hatch outranks the built-in map
// format, so a caller who wants JSON-in-an-env-var is not blocked by it.
func TestMapCustomDecoderWins(t *testing.T) {
	type cfg struct {
		Flags map[string]string `env:"FLAGS"`
	}
	got, _, err := cfgkit.Load[cfg](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"FLAGS": `{"a":"1","b":"2"}`})),
		cfgkit.WithDecoder(jsonMapDecoder()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Flags["a"] != "1" || got.Flags["b"] != "2" {
		t.Errorf("Flags = %#v, want the registered decoder to have parsed JSON", got.Flags)
	}
}

// ---------------------------------------------------------------------------
// Robustness.
// ---------------------------------------------------------------------------

// TestMapEdgeCaseInputs walks the inputs a hand-edited .env file actually
// produces. None may panic, and each must land on the documented side.
func TestMapEdgeCaseInputs(t *testing.T) {
	type cfg struct {
		Flags map[string]string `env:"FLAGS"`
	}
	cases := []struct {
		name  string
		raw   string
		want  map[string]string
		isErr bool
	}{
		{name: "empty", raw: "", want: map[string]string{}},
		{name: "single", raw: "a:1", want: map[string]string{"a": "1"}},
		{name: "spaces around entries", raw: " a : 1 , b : 2 ", want: map[string]string{"a": "1", "b": "2"}},
		{name: "empty value", raw: "a:", want: map[string]string{"a": ""}},
		{name: "empty key", raw: ":1", want: map[string]string{"": "1"}},
		{name: "value is only a colon", raw: "a::", want: map[string]string{"a": ":"}},
		{name: "unicode", raw: "ключ:значение", want: map[string]string{"ключ": "значение"}},
		{name: "equals inside value", raw: "q:a=b", want: map[string]string{"q": "a=b"}},
		{name: "trailing separator", raw: "a:1,", isErr: true},
		{name: "no separator at all", raw: "abc", isErr: true},
		{name: "only a comma", raw: ",", isErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
				cfgkit.FromMap(map[string]string{"FLAGS": tc.raw}),
			))
			if tc.isErr {
				if err == nil {
					t.Fatalf("want an error for %q, got %#v", tc.raw, got.Flags)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.raw, err)
			}
			if len(got.Flags) != len(tc.want) {
				t.Fatalf("Flags = %#v, want %#v", got.Flags, tc.want)
			}
			for k, v := range tc.want {
				if got.Flags[k] != v {
					t.Errorf("Flags[%q] = %q, want %q", k, got.Flags[k], v)
				}
			}
		})
	}
}

// TestMapLoadIsConcurrencySafe crosses maps with the no-global-state guarantee.
// Run under -race, this is what proves several tenants can load at once.
func TestMapLoadIsConcurrencySafe(t *testing.T) {
	type cfg struct {
		Flags map[string]string `env:"FLAGS"`
	}
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
				cfgkit.FromMap(map[string]string{"FLAGS": "a:1,b:2"}),
			))
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
				return
			}
			if len(got.Flags) != 2 {
				t.Errorf("goroutine %d: Flags = %#v", i, got.Flags)
			}
		}()
	}
	wg.Wait()
}

// TestMapsAreIndependentBetweenLoads guards against a shared backing map: two
// Loads must not be able to see each other's entries.
func TestMapsAreIndependentBetweenLoads(t *testing.T) {
	type cfg struct {
		Flags map[string]string `env:"FLAGS" default:"a:1"`
	}
	first, _, err := cfgkit.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	first.Flags["injected"] = "yes"

	second, _, err := cfgkit.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := second.Flags["injected"]; leaked {
		t.Error("a second Load saw the first Load's mutation — the default map is shared")
	}
}

// FuzzDecodeMap pins the invariant that matters most for a parser fed by
// hand-edited files: it may reject anything, but it may never panic.
func FuzzDecodeMap(f *testing.F) {
	for _, seed := range []string{
		"", "a:1", "a:1,b:2", ":", ",", "a", "a::", "a:1,", ",,,",
		"postgres://u:p@h:5432/d", "k:v,k:v2", strings.Repeat("a:1,", 100),
	} {
		f.Add(seed)
	}

	type cfg struct {
		Strings   map[string]string        `env:"S"`
		Ints      map[string]int           `env:"I"`
		Durations map[string]time.Duration `env:"D"`
	}
	f.Fuzz(func(t *testing.T, raw string) {
		// Errors are fine and expected; a panic is not.
		_, _, _ = cfgkit.Load[cfg](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"S": raw, "I": raw, "D": raw}),
		))
	})
}
