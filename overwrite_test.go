// Tests for CONFIG_SPEC §4.4 — a source always wins over a default, including
// when the source's value is the type's zero value.
//
// This behaviour is correct today by construction rather than by intent, which
// is why it needs pinning. The plausible refactor that breaks it is three lines
// in bind.go that look harmless:
//
//	if raw == "" {
//		continue // "nothing to set"
//	}
//
// With that edit an operator who writes HOST= to clear a default is silently
// ignored. The existing suite does fail on it, but with a panic from an
// unrelated test — nothing names the rule. These tests name it.
package cfgkit_test

import (
	"testing"

	"github.com/ubgo/cfgkit"
)

type overwriteCfg struct {
	Host    string   `env:"OW_HOST" default:"localhost"`
	Port    int      `env:"OW_PORT" default:"8080"`
	Debug   bool     `env:"OW_DEBUG" default:"true"`
	Origins []string `env:"OW_ORIGINS" default:"a,b"`
	Ratio   float64  `env:"OW_RATIO" default:"1.5"`
}

// TestSourceOverridesDefaultWithZeroValue is the important one. Every case here
// is a value a naive implementation would skip as "empty" or "unset", and every
// one of them is a deliberate choice by whoever wrote the configuration.
func TestSourceOverridesDefaultWithZeroValue(t *testing.T) {
	t.Run("empty string clears a default", func(t *testing.T) {
		// HOST= in a .env file means "no host", not "use the default".
		cfg, _, err := cfgkit.Load[overwriteCfg](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"OW_HOST": ""}),
		))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Host != "" {
			t.Errorf("Host = %q, want %q — a source must win even when its value is empty", cfg.Host, "")
		}
	})

	t.Run("false turns off a true default", func(t *testing.T) {
		// This is the case that matters most: a boolean defaulting to true
		// would be impossible to disable if zero values were skipped.
		cfg, _, err := cfgkit.Load[overwriteCfg](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"OW_DEBUG": "false"}),
		))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Debug {
			t.Error("Debug = true, want false — a default of true must be disableable")
		}
	})

	t.Run("zero overrides a numeric default", func(t *testing.T) {
		cfg, _, err := cfgkit.Load[overwriteCfg](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"OW_PORT": "0", "OW_RATIO": "0"}),
		))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Port != 0 {
			t.Errorf("Port = %d, want 0 — zero is a value, not an absence", cfg.Port)
		}
		if cfg.Ratio != 0 {
			t.Errorf("Ratio = %v, want 0", cfg.Ratio)
		}
	})

	t.Run("empty list clears a defaulted list", func(t *testing.T) {
		// ORIGINS= means "no origins allowed", which is a meaningful and
		// security-relevant setting. It must not silently become the default.
		cfg, _, err := cfgkit.Load[overwriteCfg](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"OW_ORIGINS": ""}),
		))
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Origins) != 0 {
			t.Errorf("Origins = %v, want empty — an empty list is a choice", cfg.Origins)
		}
	})
}

// TestZeroValueIsAttributedToItsSource pins that provenance agrees: a field a
// source set to its zero value reports that source, not "default". Otherwise
// Explain would tell an operator their line had no effect when it did.
func TestZeroValueIsAttributedToItsSource(t *testing.T) {
	_, res, err := cfgkit.Load[overwriteCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"OW_HOST": "", "OW_DEBUG": "false"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Fields() {
		switch f.Path {
		case "Host", "Debug":
			if f.Source != "map" {
				t.Errorf("%s source = %q, want map — the source did set it", f.Path, f.Source)
			}
		case "Port":
			if f.Source != "default" {
				t.Errorf("Port source = %q, want default — no source touched it", f.Source)
			}
		}
	}
}

// TestLaterSourceOverridesWithZeroValue extends the rule across the chain: a
// later source clearing an earlier source's value must also win, or an
// operator could not override a file from the environment with an empty value.
func TestLaterSourceOverridesWithZeroValue(t *testing.T) {
	cfg, _, err := cfgkit.Load[overwriteCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"OW_HOST": "from-file", "OW_DEBUG": "true"}),
		cfgkit.FromMap(map[string]string{"OW_HOST": "", "OW_DEBUG": "false"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "" {
		t.Errorf("Host = %q, want empty — the later source cleared it", cfg.Host)
	}
	if cfg.Debug {
		t.Error("Debug = true, want false — the later source turned it off")
	}
}

// TestAbsentKeyLeavesTheDefault is the other half of the rule, and the reason
// the first half is not simply "always overwrite": a key NO source supplied
// must leave the default alone. Absent and empty are different.
func TestAbsentKeyLeavesTheDefault(t *testing.T) {
	cfg, _, err := cfgkit.Load[overwriteCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"OW_PORT": "9000"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "localhost" {
		t.Errorf("Host = %q, want the default — no source mentioned it", cfg.Host)
	}
	if !cfg.Debug {
		t.Error("Debug = false, want the default true — no source mentioned it")
	}
	if len(cfg.Origins) != 2 {
		t.Errorf("Origins = %v, want the default", cfg.Origins)
	}
}
