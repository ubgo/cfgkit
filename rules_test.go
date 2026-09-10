package cfgkit_test

import (
	"errors"
	"flag"
	"regexp"
	"strings"
	"testing"

	"github.com/ubgo/cfgkit"
)

// ServeKind is the discriminant of a tagged union. It is a real Go type with
// real constants, so a comparison against it cannot be a mistyped string — which
// is more than a `validate:"required_if=Kind cloudflare"` tag can promise.
type ServeKind string

const (
	ServeKindNone       ServeKind = ""
	ServeKindCloudflare ServeKind = "cloudflare"
	ServeKindDirect     ServeKind = "direct"
)

type CloudflarePurge struct {
	Token string `env:"TOKEN" secret:"true"`
	Zone  string `env:"ZONE"`
}

// PublicServe is the sync_go rule that motivated the whole validation design.
// In Pkl it reads:
//
//	cloudflare: CloudflarePurgeSetting?(((kind == "cloudflare") && (this != null)) || (kind != "cloudflare"))
type PublicServe struct {
	Kind       ServeKind        `env:"SERVE_KIND"`
	Cloudflare *CloudflarePurge `env:",prefix=CF_"`
}

func (p *PublicServe) Validate() error {
	// The clean pointer check, which the old eager-allocation behaviour made
	// impossible: before §6.4 the section was never nil, so this had to reach
	// inside and test a field instead.
	return cfgkit.RequiredWhen(
		"Cloudflare", p.Cloudflare != nil,
		"kind=cloudflare", p.Kind == ServeKindCloudflare,
	)
}

// TestRequiredWhen pins the discriminated-union invariant in BOTH directions
// from one rule call, which is the case only a config language could express
// before.
func TestRequiredWhen(t *testing.T) {
	t.Run("condition holds and the field is present", func(t *testing.T) {
		if _, _, err := cfgkit.Load[PublicServe](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
			"SERVE_KIND": "cloudflare",
			"CF_TOKEN":   "tok",
		}))); err != nil {
			t.Errorf("valid combination rejected: %v", err)
		}
	})

	t.Run("condition holds but the field is missing", func(t *testing.T) {
		_, _, err := cfgkit.Load[PublicServe](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
			"SERVE_KIND": "cloudflare",
		})))
		if err == nil || !strings.Contains(err.Error(), "is required when kind=cloudflare") {
			t.Errorf("want the required-when direction, got: %v", err)
		}
	})

	t.Run("condition fails but the field is set", func(t *testing.T) {
		_, _, err := cfgkit.Load[PublicServe](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
			"SERVE_KIND": "direct",
			"CF_TOKEN":   "tok",
		})))
		if err == nil || !strings.Contains(err.Error(), "must not be set unless kind=cloudflare") {
			t.Errorf("want the must-not-be-set direction, got: %v", err)
		}
	})

	t.Run("neither", func(t *testing.T) {
		if _, _, err := cfgkit.Load[PublicServe](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
			"SERVE_KIND": "direct",
		}))); err != nil {
			t.Errorf("neither-set combination rejected: %v", err)
		}
	})
}

// TestRuleHelpers covers the combinators directly. They return plain errors, so
// each is testable without a Load.
func TestRuleHelpers(t *testing.T) {
	t.Run("Required", func(t *testing.T) {
		if cfgkit.Required("F", "x") != nil {
			t.Error("a set value must pass")
		}
		if cfgkit.Required("F", "") == nil {
			t.Error("a zero value must fail")
		}
	})

	t.Run("NotEmpty", func(t *testing.T) {
		if cfgkit.NotEmpty("F", "x") != nil || cfgkit.NotEmpty("F", "") == nil {
			t.Error("NotEmpty rejects only the empty string")
		}
	})

	t.Run("RequiredIn", func(t *testing.T) {
		// The same empty value is fine in dev and fatal in prod. That asymmetry
		// is what lets a fresh clone run while production stays strict.
		if cfgkit.RequiredIn(cfgkit.ModeProd, cfgkit.ModeDev, "F", "") != nil {
			t.Error("must not fire outside its mode")
		}
		if cfgkit.RequiredIn(cfgkit.ModeProd, cfgkit.ModeProd, "F", "") == nil {
			t.Error("must fire in its mode")
		}
	})

	t.Run("OneOf", func(t *testing.T) {
		if cfgkit.OneOf("F", "b", "a", "b") != nil {
			t.Error("a member must pass")
		}
		err := cfgkit.OneOf("F", "z", "a", "b")
		if err == nil || !strings.Contains(err.Error(), "want one of [a b]") {
			t.Errorf("the error must list the allowed set: %v", err)
		}
	})

	t.Run("Range", func(t *testing.T) {
		if cfgkit.Range("F", 80, 1, 65535) != nil {
			t.Error("an in-range value must pass")
		}
		if cfgkit.Range("F", 70000, 1, 65535) == nil {
			t.Error("an out-of-range value must fail")
		}
	})

	t.Run("Matches", func(t *testing.T) {
		re := regexp.MustCompile(`^v\d+$`)
		if cfgkit.Matches("F", "v2", re) != nil || cfgkit.Matches("F", "x", re) == nil {
			t.Error("Matches must follow the pattern")
		}
	})

	t.Run("MutuallyExclusive", func(t *testing.T) {
		if cfgkit.MutuallyExclusive(cfgkit.Set{Name: "A", Value: "x"}, cfgkit.Set{Name: "B", Value: ""}) != nil {
			t.Error("one set value must pass")
		}
		err := cfgkit.MutuallyExclusive(cfgkit.Set{Name: "A", Value: "x"}, cfgkit.Set{Name: "B", Value: "y"})
		if err == nil || !strings.Contains(err.Error(), "A, B") {
			t.Errorf("the error must name the offending fields: %v", err)
		}
	})

	t.Run("AtLeastOneOf", func(t *testing.T) {
		if cfgkit.AtLeastOneOf(cfgkit.Set{Name: "A", Value: ""}, cfgkit.Set{Name: "B", Value: "y"}) != nil {
			t.Error("one set value must pass")
		}
		if cfgkit.AtLeastOneOf(cfgkit.Set{Name: "A", Value: ""}, cfgkit.Set{Name: "B", Value: ""}) == nil {
			t.Error("none set must fail")
		}
	})
}

// TestNotWeakSecret pins the strictness inversion: the value that makes a fresh
// clone run must be the value that refuses to boot in production.
func TestNotWeakSecret(t *testing.T) {
	const placeholder = "__CHANGE_ME__"

	if err := cfgkit.NotWeakSecret(cfgkit.ModeDev, "Key", placeholder); err != nil {
		t.Errorf("dev must tolerate a placeholder, else zero-config is impossible: %v", err)
	}

	for _, v := range []string{"", placeholder, "change-me-internal-key"} {
		err := cfgkit.NotWeakSecret(cfgkit.ModeProd, "Key", v, "change-me-internal-key")
		if err == nil {
			t.Errorf("prod must refuse %q", v)
		}
		// The offending value is a credential; it must never be echoed back.
		if v != "" && err != nil && strings.Contains(err.Error(), v) {
			t.Errorf("the error echoed the secret value: %v", err)
		}
	}

	if err := cfgkit.NotWeakSecret(cfgkit.ModeProd, "Key", "a-real-secret"); err != nil {
		t.Errorf("a real value must pass in prod: %v", err)
	}
}

type Flagged struct {
	Port int    `env:"PORT" flag:"port" default:"8080"`
	Host string `env:"HOST" flag:"host" default:"localhost"`
	Only string `env:"ONLY_ENV" default:"env-only"`
}

// TestFlagDefaultNeverWins is the important flag test. It pins the rule viper
// gets wrong in #671 and #375: a flag the user did not type must contribute
// nothing, so a config file still wins over a flag's own default.
func TestFlagDefaultNeverWins(t *testing.T) {
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	fs.Int("port", 9999, "port") // a default that must NEVER apply
	fs.String("host", "flag", "host")
	if err := fs.Parse([]string{"--host", "typed.example"}); err != nil {
		t.Fatal(err)
	}

	cfg, res, err := cfgkit.Load[Flagged](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"PORT": "3000"}),
		cfgkit.FromFlagSet(fs), // highest precedence
	))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Port != 3000 {
		t.Errorf("Port = %d, want 3000 — an untyped flag's default must not beat the map", cfg.Port)
	}
	if cfg.Host != "typed.example" {
		t.Errorf("Host = %q, want the typed flag value", cfg.Host)
	}

	for _, f := range res.Fields() {
		switch f.Path {
		case "Port":
			if f.Source == "flags" {
				t.Error("Port must not be attributed to the flag source")
			}
		case "Host":
			if f.Source != "flags" {
				t.Errorf("Host source = %q, want flags", f.Source)
			}
		}
	}
}

// TestFlagsAreOptIn pins that a field without a flag tag is invisible to a flag
// source, so 150 config fields never become 150 flags.
func TestFlagsAreOptIn(t *testing.T) {
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	fs.String("only-env", "hijacked", "")
	if err := fs.Parse([]string{"--only-env", "hijacked"}); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := cfgkit.Load[Flagged](cfgkit.WithSources(cfgkit.FromFlagSet(fs)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Only != "env-only" {
		t.Errorf("Only = %q — a field with no flag tag must ignore the flag set", cfg.Only)
	}
}

// TestFlagSource pins the seam a cobra/pflag contrib module uses: it hands over
// a map of only the flags whose Changed field is true.
func TestFlagSource(t *testing.T) {
	cfg, _, err := cfgkit.Load[Flagged](cfgkit.WithSources(
		cfgkit.FlagSource("pflag", map[string]string{"port": "4444"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 4444 {
		t.Errorf("Port = %d, want the value from the external flag map", cfg.Port)
	}
}

type Documented struct {
	Port   int    `env:"PORT" default:"8080" doc:"HTTP listen port"`
	DBURL  string `env:"DATABASE_URL,required" doc:"Postgres connection string"`
	APIKey string `env:"HYPERDX_API_KEY,notempty" secret:"true" was:"HYPERDX_KEY" doc:"HyperDX ingest key"`
	PWFile string `env:"DB_PASSWORD_FILE,file" secret:"true" doc:"Path to the mounted password file"`
}

// TestDocument pins the contract generator: every bound key appears, statuses
// are correct, and a secret is emitted EMPTY because the file gets committed.
func TestDocument(t *testing.T) {
	var sb strings.Builder
	if err := cfgkit.Document[Documented](&sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()

	for _, want := range []string{
		"# HTTP listen port",
		"PORT=8080",
		"# REQUIRED",
		"DATABASE_URL=",
		"# REQUIRED, must not be empty · secret — do not commit a real value · formerly HYPERDX_KEY",
		"HYPERDX_API_KEY=",
		"the value is a PATH to a file",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	// A generator that wrote a real credential would be a leak with a schedule.
	if strings.Contains(out, "HYPERDX_API_KEY=s") {
		t.Error("a secret was emitted with a value")
	}
}

// TestDocumentIsStable pins that two runs produce identical bytes, which is what
// makes a CI drift gate ("regenerate and diff") possible at all.
func TestDocumentIsStable(t *testing.T) {
	var a, b strings.Builder
	if err := errors.Join(cfgkit.Document[Documented](&a), cfgkit.Document[Documented](&b)); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Error("Document output is not deterministic")
	}
}

// TestDocumentRejectsNonStruct mirrors Load's own shape check.
func TestDocumentRejectsNonStruct(t *testing.T) {
	var sb strings.Builder
	if err := cfgkit.Document[string](&sb); err == nil {
		t.Error("Document must reject a non-struct type")
	}
}
