// Tests for CONFIG_SPEC §7.x — map fields.
//
// A map exists for the one case every other field type cannot serve: the KEY
// NAMES are not known when the struct is written. Feature flags, per-tenant
// limits and arbitrary extra headers are all "add an entry by editing .env, not
// by editing Go". Everything else should stay a named field, which is typed and
// where a typo is a compile error.
package cfgkit_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ubgo/cfgkit"
)

type mapCfg struct {
	Flags   map[string]string        `env:"FLAGS"`
	Limits  map[string]int           `env:"LIMITS"`
	Timeout map[string]time.Duration `env:"TIMEOUTS"`
}

func TestMapDecodesEntries(t *testing.T) {
	cfg, _, err := cfgkit.Load[mapCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"FLAGS": "new-checkout:on,dark-mode:off"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Flags) != 2 || cfg.Flags["new-checkout"] != "on" || cfg.Flags["dark-mode"] != "off" {
		t.Errorf("Flags = %#v, want both entries", cfg.Flags)
	}
}

// TestMapDecodesValueTypes pins that a map value goes through the SAME decoder
// every other field uses. That is the whole reason maps needed no type zoo:
// int, Duration and anything with an UnmarshalText work for free.
func TestMapDecodesValueTypes(t *testing.T) {
	cfg, _, err := cfgkit.Load[mapCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{
			"LIMITS":   "acme:1000,globex:500",
			"TIMEOUTS": "read:30s,write:1m",
		}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Limits["acme"] != 1000 || cfg.Limits["globex"] != 500 {
		t.Errorf("Limits = %#v", cfg.Limits)
	}
	if cfg.Timeout["read"] != 30*time.Second || cfg.Timeout["write"] != time.Minute {
		t.Errorf("Timeout = %#v", cfg.Timeout)
	}
}

// TestMapSplitsOnTheFirstSeparatorOnly is THE test for this feature.
//
// A map value is very often a URL or a host:port, so most entries contain more
// colons than the one that separates key from value. Splitting on every colon
// would corrupt every connection string — the most common thing a map holds.
func TestMapSplitsOnTheFirstSeparatorOnly(t *testing.T) {
	type dsnCfg struct {
		DSNs map[string]string `env:"DSNS"`
	}
	cfg, _, err := cfgkit.Load[dsnCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{
			"DSNS": "primary:postgres://user@db1:5432/app,cache:redis://cache:6379",
		}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.DSNs["primary"], "postgres://user@db1:5432/app"; got != want {
		t.Errorf("primary = %q, want %q — only the FIRST colon separates", got, want)
	}
	if got, want := cfg.DSNs["cache"], "redis://cache:6379"; got != want {
		t.Errorf("cache = %q, want %q", got, want)
	}
}

// TestMapEmptyValueIsAnEmptyMap mirrors the slice rule: "FLAGS=" means no
// flags, never one flag with an empty name. Combined with §4.4, it is how a
// deployment clears a defaulted map.
func TestMapEmptyValueIsAnEmptyMap(t *testing.T) {
	type defaulted struct {
		Flags map[string]string `env:"FLAGS" default:"a:1,b:2"`
	}
	cfg, _, err := cfgkit.Load[defaulted](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"FLAGS": ""}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Flags == nil {
		t.Fatal("Flags = nil, want an allocated empty map — an empty map is a value")
	}
	if len(cfg.Flags) != 0 {
		t.Errorf("Flags = %#v, want empty — a source wins over a default", cfg.Flags)
	}
}

// TestMapAbsentStaysNil pins the other half: with nothing set, the field keeps
// its zero value, which for a map is nil. Callers can therefore distinguish
// "configured to hold nothing" (empty, non-nil) from "never configured" (nil).
func TestMapAbsentStaysNil(t *testing.T) {
	cfg, _, err := cfgkit.Load[mapCfg]()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Flags != nil {
		t.Errorf("Flags = %#v, want nil when no source and no default set it", cfg.Flags)
	}
}

func TestMapDefaultTag(t *testing.T) {
	type defaulted struct {
		Flags map[string]string `env:"FLAGS" default:"beta:on"`
	}
	cfg, _, err := cfgkit.Load[defaulted]()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Flags["beta"] != "on" {
		t.Errorf("Flags = %#v, want the default parsed", cfg.Flags)
	}
}

// TestMapCustomDelimiters covers the escape hatch for values that contain the
// default separators — the reason both are tags rather than constants.
func TestMapCustomDelimiters(t *testing.T) {
	type custom struct {
		Rules map[string]string `env:"RULES" delim:";" kvdelim:"="`
	}
	cfg, _, err := cfgkit.Load[custom](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"RULES": "a=1,2,3;b=4"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rules["a"] != "1,2,3" {
		t.Errorf("Rules[a] = %q, want the commas kept as part of the value", cfg.Rules["a"])
	}
	if cfg.Rules["b"] != "4" {
		t.Errorf("Rules[b] = %q", cfg.Rules["b"])
	}
}

// TestMapMalformedEntryNamesTheProblem pins that the error is actionable. The
// usual cause is a missing separator, and a bare "invalid map" would send the
// reader hunting through a long line.
func TestMapMalformedEntryNamesTheProblem(t *testing.T) {
	err := cfgkit.Check[mapCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"FLAGS": "ok:yes,broken"}),
	))
	if err == nil {
		t.Fatal("want an error for an entry with no separator")
	}
	msg := err.Error()
	for _, want := range []string{"Flags", "FLAGS", "broken", `":"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %s", msg, want)
		}
	}
}

func TestMapBadValueTypeNamesTheKey(t *testing.T) {
	err := cfgkit.Check[mapCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"LIMITS": "acme:lots"}),
	))
	if err == nil {
		t.Fatal("want an error for a non-numeric value")
	}
	if !strings.Contains(err.Error(), "acme") {
		t.Errorf("error %q must name the offending map key", err)
	}
}

// TestMapDuplicateKeyTakesTheLast matches how the source chain itself resolves
// a repeat: the last statement wins.
func TestMapDuplicateKeyTakesTheLast(t *testing.T) {
	cfg, _, err := cfgkit.Load[mapCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"FLAGS": "a:first,a:second"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Flags["a"] != "second" {
		t.Errorf("Flags[a] = %q, want the last entry", cfg.Flags["a"])
	}
}

// TestMapRendersDeterministically is why Explain and Document sort map entries:
// Go randomises map iteration, so unsorted output would make the "contract is
// stale" CI check in recipes.md fail at random.
func TestMapRendersDeterministically(t *testing.T) {
	const runs = 20
	var first string
	for i := range runs {
		_, res, err := cfgkit.Load[mapCfg](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"FLAGS": "z:1,a:2,m:3,b:4,y:5"}),
		))
		if err != nil {
			t.Fatal(err)
		}
		var got string
		for _, f := range res.Fields() {
			if f.Path == "Flags" {
				got = f.Value
			}
		}
		if i == 0 {
			first = got
			if want := "a:2,b:4,m:3,y:5,z:1"; got != want {
				t.Fatalf("rendered %q, want %q — sorted by key", got, want)
			}
			continue
		}
		if got != first {
			t.Fatalf("run %d rendered %q, run 0 rendered %q — output must not depend on map order", i, got, first)
		}
	}
}

// TestMapRoundTripsThroughItsOwnFormat is the property that keeps Document's
// output usable: what cfgkit prints must be what cfgkit can read back.
func TestMapRoundTripsThroughItsOwnFormat(t *testing.T) {
	const raw = "a:1,b:2,c:3"
	_, res, err := cfgkit.Load[mapCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"FLAGS": raw}),
	))
	if err != nil {
		t.Fatal(err)
	}
	var rendered string
	for _, f := range res.Fields() {
		if f.Path == "Flags" {
			rendered = f.Value
		}
	}

	cfg2, _, err := cfgkit.Load[mapCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"FLAGS": rendered}),
	))
	if err != nil {
		t.Fatalf("re-loading rendered output %q failed: %v", rendered, err)
	}
	if len(cfg2.Flags) != 3 || cfg2.Flags["b"] != "2" {
		t.Errorf("round trip lost data: %#v", cfg2.Flags)
	}
}

// TestMapIsNotWalkedInto pins that the walker treats a map as a LEAF. If it
// descended, a map field would contribute no key at all and would bind from
// nothing — silently empty in production.
func TestMapIsNotWalkedInto(t *testing.T) {
	_, res, err := cfgkit.Load[mapCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"FLAGS": "a:1"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range res.Fields() {
		if f.Path == "Flags" {
			found = true
			if f.Key != "FLAGS" {
				t.Errorf("Flags key = %q, want FLAGS", f.Key)
			}
		}
	}
	if !found {
		t.Error("Flags did not appear in the provenance record")
	}
}

// TestMapSecretIsMasked pins that a map of credentials is masked whole. A map
// leaks in exactly the way a scalar does, and masking per entry would still
// reveal the key names, which are often enough to identify an account.
func TestMapSecretIsMasked(t *testing.T) {
	type creds struct {
		Keys map[string]string `env:"API_KEYS" secret:"true"`
	}
	_, res, err := cfgkit.Load[creds](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"API_KEYS": "stripe:sk_live_abc,aws:AKIAsecret"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Fields() {
		if strings.Contains(f.Value, "sk_live_abc") || strings.Contains(f.Value, "stripe") {
			t.Errorf("secret map rendered as %q — neither values nor key names may leak", f.Value)
		}
	}
}

// TestMapBadKeyTypeIsReported covers the non-string key. Keys go through the
// same decoder as values, so map[int]string works — and fails honestly.
func TestMapBadKeyTypeIsReported(t *testing.T) {
	type shards struct {
		Hosts map[int]string `env:"SHARDS"`
	}

	cfg, _, err := cfgkit.Load[shards](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"SHARDS": "0:db-a,1:db-b"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hosts[0] != "db-a" || cfg.Hosts[1] != "db-b" {
		t.Errorf("Hosts = %#v, want integer keys decoded", cfg.Hosts)
	}

	err = cfgkit.Check[shards](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"SHARDS": "primary:db-a"}),
	))
	if err == nil {
		t.Fatal("want an error for a non-integer key")
	}
	if !strings.Contains(err.Error(), "key") {
		t.Errorf("error %q must say the KEY is the problem, not the value", err)
	}
}
