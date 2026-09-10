// Tests for CONFIG_SPEC §6.4 — when an optional *Struct section is nil.
//
// The behaviour being replaced allocated every section unconditionally, so
// `cfg.SMTP != nil` was always true and an app would start an email sender
// pointed at an empty host, failing on the first send far from the cause.
package cfgkit_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ubgo/cfgkit"
)

type smtpSection struct {
	Host string `env:"HOST"`
	User string `env:"USER"`
	Port int    `env:"PORT" default:"587"`
}

func (s *smtpSection) Validate() error {
	// Only runs when the section exists — that is the point.
	return cfgkit.Required("Host", s.Host)
}

type appWithSMTP struct {
	Port int          `env:"APP_PORT" default:"8080"`
	SMTP *smtpSection `env:",prefix=SMTP_"`
}

// TestSectionTable walks the exact table in CONFIG_SPEC §6.4.
func TestSectionTable(t *testing.T) {
	t.Run("nothing set — section is nil", func(t *testing.T) {
		cfg, _, err := cfgkit.Load[appWithSMTP]()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.SMTP != nil {
			t.Errorf("SMTP = %+v, want nil — no source set anything beneath it", cfg.SMTP)
		}
		if cfg.Port != 8080 {
			t.Errorf("the surrounding config must still load: Port = %d", cfg.Port)
		}
	})

	t.Run("one field set — section is allocated", func(t *testing.T) {
		// Setting SMTP_HOST is an unambiguous statement of intent. Requiring
		// EVERY field would make optional fields inside optional sections
		// impossible.
		cfg, _, err := cfgkit.Load[appWithSMTP](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"SMTP_HOST": "mail.example.com"}),
		))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.SMTP == nil {
			t.Fatal("SMTP = nil, want allocated — a source set a field beneath it")
		}
		if cfg.SMTP.Host != "mail.example.com" {
			t.Errorf("Host = %q", cfg.SMTP.Host)
		}
		if cfg.SMTP.User != "" {
			t.Errorf("User = %q, want empty — nobody set it", cfg.SMTP.User)
		}
		if cfg.SMTP.Port != 587 {
			t.Errorf("Port = %d, want the section's own default once it exists", cfg.SMTP.Port)
		}
	})

	t.Run("a default alone does NOT allocate", func(t *testing.T) {
		// smtpSection.Port has default:"587". If a default counted as intent,
		// every section holding one would always be non-nil — which is exactly
		// the behaviour being replaced.
		cfg, _, err := cfgkit.Load[appWithSMTP](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"APP_PORT": "9000"}),
		))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.SMTP != nil {
			t.Errorf("SMTP = %+v, want nil — only a source counts as intent", cfg.SMTP)
		}
	})
}

// TestSectionValidateOnlyWhenPresent pins the division of labour: the pointer
// answers "did you intend this feature", Validate answers "is it complete".
func TestSectionValidateOnlyWhenPresent(t *testing.T) {
	t.Run("absent section is not validated", func(t *testing.T) {
		// smtpSection.Validate requires Host. With no section there is nothing
		// to validate, so a config that does not use email must load cleanly.
		if _, _, err := cfgkit.Load[appWithSMTP](); err != nil {
			t.Errorf("an unused optional section must not fail validation: %v", err)
		}
	})

	t.Run("half-configured section reports its own missing field", func(t *testing.T) {
		_, _, err := cfgkit.Load[appWithSMTP](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"SMTP_USER": "bob"}),
		))
		if err == nil || !strings.Contains(err.Error(), "Host") {
			t.Errorf("want a Host-is-required error, got: %v", err)
		}
	})
}

type cacheSection struct {
	TTL  time.Duration `env:"TTL" default:"5m"`
	Size int           `env:"SIZE" default:"1000"`
}

type appWithCache struct {
	Cache *cacheSection `env:",prefix=CACHE_,init"`
}

// TestSectionInitOverride pins the escape hatch: a section whose defaults are a
// complete working setup is always allocated, so a caller reads it without a
// guard.
func TestSectionInitOverride(t *testing.T) {
	cfg, _, err := cfgkit.Load[appWithCache]()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Cache == nil {
		t.Fatal("Cache = nil despite the init option")
	}
	if cfg.Cache.TTL != 5*time.Minute || cfg.Cache.Size != 1000 {
		t.Errorf("Cache = %+v, want its defaults applied", cfg.Cache)
	}
}

// TestSectionPrunedFieldsLeaveTheRecord pins that Explain does not report
// fields of a section that is now nil — describing memory that no longer
// exists would make provenance wrong.
func TestSectionPrunedFieldsLeaveTheRecord(t *testing.T) {
	_, res, err := cfgkit.Load[appWithSMTP]()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Fields() {
		if strings.HasPrefix(f.Path, "SMTP.") {
			t.Errorf("%s appears in the record, but its section was pruned to nil", f.Path)
		}
	}

	var sb strings.Builder
	if err := res.Explain(&sb); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sb.String(), "SMTP_") {
		t.Errorf("Explain lists keys of a nil section:\n%s", sb.String())
	}
}

// TestSectionSurvivesWhenSourced is the same check in the positive direction:
// once a section exists, its fields ARE in the record.
func TestSectionSurvivesWhenSourced(t *testing.T) {
	_, res, err := cfgkit.Load[appWithSMTP](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"SMTP_HOST": "h"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	var seen int
	for _, f := range res.Fields() {
		if strings.HasPrefix(f.Path, "SMTP.") {
			seen++
		}
	}
	if seen != 3 {
		t.Errorf("found %d SMTP fields in the record, want 3", seen)
	}
}

type outer struct {
	Inner *middleSection `env:",prefix=OUT_"`
}

type middleSection struct {
	Deep *deepSection `env:",prefix=MID_"`
	Name string       `env:"NAME"`
}

type deepSection struct {
	Value string `env:"VALUE"`
}

// TestNestedSectionsPruneDeepestFirst pins that a parent sees its children's
// outcome: an outer section holding only an empty inner section is itself
// pruned.
func TestNestedSectionsPruneDeepestFirst(t *testing.T) {
	t.Run("nothing set — both nil", func(t *testing.T) {
		cfg, _, err := cfgkit.Load[outer]()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Inner != nil {
			t.Errorf("Inner = %+v, want nil", cfg.Inner)
		}
	})

	t.Run("deep field set — both allocated", func(t *testing.T) {
		cfg, _, err := cfgkit.Load[outer](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"OUT_MID_VALUE": "v"}),
		))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Inner == nil {
			t.Fatal("Inner = nil, but a source set a field two levels down")
		}
		if cfg.Inner.Deep == nil || cfg.Inner.Deep.Value != "v" {
			t.Errorf("Deep = %+v, want the value bound", cfg.Inner.Deep)
		}
	})
}
