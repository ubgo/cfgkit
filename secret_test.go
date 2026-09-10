// The one guarantee a config library must never break: a value marked secret
// does not appear in anything the program emits.
//
// This file exists because the guarantee was DOCUMENTED before it was true.
// types.md said "except for a secret field, whose raw text is withheld", and
// DecodeError did carry an empty Value for secrets — but the raw text reached
// the message anyway through the wrapped decoder error, which quotes it:
//
//	Port (P from map): "hunter2" is not a valid int
//
// It was found while testing secret MAPS and turned out to affect scalars and
// slices equally, so the fix and these tests cover every shape. A guarantee
// stated in prose is a claim nobody has checked; only a test verifies it.
package cfgkit_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/cfgkit"
)

// theSecret is distinctive enough that a substring search cannot produce a
// false negative, and it is used as the value for every shape below.
const theSecret = "hunter2-CANARY-do-not-leak"

// TestSecretNeverReachesAnErrorMessage sweeps every field shape that can fail
// to decode. Each case supplies text that is invalid for the target type, so
// the decoder is guaranteed to build an error mentioning the offending value.
func TestSecretNeverReachesAnErrorMessage(t *testing.T) {
	cases := []struct {
		name  string
		check func() error
	}{
		{"int", func() error {
			return cfgkit.Check[struct {
				V int `env:"S" secret:"true"`
			}](cfgkit.WithSources(cfgkit.FromMap(map[string]string{"S": theSecret})))
		}},
		{"uint", func() error {
			return cfgkit.Check[struct {
				V uint `env:"S" secret:"true"`
			}](cfgkit.WithSources(cfgkit.FromMap(map[string]string{"S": theSecret})))
		}},
		{"float", func() error {
			return cfgkit.Check[struct {
				V float64 `env:"S" secret:"true"`
			}](cfgkit.WithSources(cfgkit.FromMap(map[string]string{"S": theSecret})))
		}},
		{"bool", func() error {
			return cfgkit.Check[struct {
				V bool `env:"S" secret:"true"`
			}](cfgkit.WithSources(cfgkit.FromMap(map[string]string{"S": theSecret})))
		}},
		{"duration", func() error {
			return cfgkit.Check[struct {
				V time.Duration `env:"S" secret:"true"`
			}](cfgkit.WithSources(cfgkit.FromMap(map[string]string{"S": theSecret})))
		}},
		{"time", func() error {
			return cfgkit.Check[struct {
				V time.Time `env:"S" secret:"true"`
			}](cfgkit.WithSources(cfgkit.FromMap(map[string]string{"S": theSecret})))
		}},
		{"slice element", func() error {
			return cfgkit.Check[struct {
				V []int `env:"S" secret:"true"`
			}](cfgkit.WithSources(cfgkit.FromMap(map[string]string{"S": "1," + theSecret})))
		}},
		{"map value", func() error {
			return cfgkit.Check[struct {
				V map[string]int `env:"S" secret:"true"`
			}](cfgkit.WithSources(cfgkit.FromMap(map[string]string{"S": "k:" + theSecret})))
		}},
		{"map key", func() error {
			return cfgkit.Check[struct {
				V map[int]string `env:"S" secret:"true"`
			}](cfgkit.WithSources(cfgkit.FromMap(map[string]string{"S": theSecret + ":v"})))
		}},
		{"map with no separator", func() error {
			return cfgkit.Check[struct {
				V map[string]string `env:"S" secret:"true"`
			}](cfgkit.WithSources(cfgkit.FromMap(map[string]string{"S": theSecret})))
		}},
		{"pointer", func() error {
			return cfgkit.Check[struct {
				V *int `env:"S" secret:"true"`
			}](cfgkit.WithSources(cfgkit.FromMap(map[string]string{"S": theSecret})))
		}},
		{"default tag", func() error {
			return cfgkit.Check[struct {
				V int `env:"S" secret:"true" default:"not-a-number"`
			}]()
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.check()
			if err == nil {
				t.Fatal("want a decode error, so there is a message to inspect")
			}
			if strings.Contains(err.Error(), theSecret) {
				t.Errorf("the secret reached the error message:\n%v", err)
			}
			// The message must still be USEFUL — naming the field and the key is
			// what lets an operator fix it without seeing the value.
			if !strings.Contains(err.Error(), "V") || !strings.Contains(err.Error(), "S") {
				t.Errorf("message %q must still name the field and the key", err)
			}
		})
	}
}

// TestSecretErrorStillNamesTheType pins that redaction did not reduce the
// message to uselessness: the expected type is the actionable half, and it
// cannot leak anything because it comes from the struct, not from the value.
func TestSecretErrorStillNamesTheType(t *testing.T) {
	err := cfgkit.Check[struct {
		V int `env:"S" secret:"true"`
	}](cfgkit.WithSources(cfgkit.FromMap(map[string]string{"S": theSecret})))
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "int") {
		t.Errorf("message %q should name the expected type", err)
	}
}

// TestNonSecretErrorsStillShowTheValue is the other half. Redacting everything
// would be safe and useless: for an ordinary field the offending text is the
// single most useful thing in the message.
func TestNonSecretErrorsStillShowTheValue(t *testing.T) {
	err := cfgkit.Check[struct {
		V int `env:"S"`
	}](cfgkit.WithSources(cfgkit.FromMap(map[string]string{"S": "eighty"})))
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "eighty") {
		t.Errorf("message %q must quote the offending value for a non-secret field", err)
	}
}

// TestSecretNeverReachesAnyOutputSurface sweeps the surfaces rather than the
// types: everything the library can write must be free of the value.
func TestSecretNeverReachesAnyOutputSurface(t *testing.T) {
	type cfg struct {
		Password string            `env:"PASSWORD" secret:"true"`
		Keys     map[string]string `env:"KEYS" secret:"true"`
		Tokens   []string          `env:"TOKENS" secret:"true"`
		Port     int               `env:"PORT" default:"8080"`
	}

	_, res, err := cfgkit.Load[cfg](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
		"PASSWORD": theSecret,
		"KEYS":     "stripe:" + theSecret,
		"TOKENS":   theSecret + ",second",
	})))
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
	var doc bytes.Buffer
	if err := cfgkit.Document[cfg](&doc); err != nil {
		t.Fatal(err)
	}

	for name, out := range map[string]string{
		"Explain":  explain.String(),
		"JSON":     string(raw),
		"Document": doc.String(),
	} {
		if strings.Contains(out, theSecret) {
			t.Errorf("%s leaked the secret:\n%s", name, out)
		}
	}

	// Explain must still be useful: the non-secret field is reported normally.
	if !strings.Contains(explain.String(), "8080") {
		t.Errorf("Explain dropped the non-secret field:\n%s", explain.String())
	}
}

// TestRevealIsTheOnlyWayOut pins that masking is opt-out, never accidental —
// and that opting out actually works, since a guarantee with no escape hatch
// gets worked around in ways nobody can audit.
func TestRevealIsTheOnlyWayOut(t *testing.T) {
	type cfg struct {
		Password string `env:"PASSWORD" secret:"true"`
	}
	sources := cfgkit.WithSources(cfgkit.FromMap(map[string]string{"PASSWORD": theSecret}))

	_, masked, err := cfgkit.Load[cfg](sources)
	if err != nil {
		t.Fatal(err)
	}
	_, revealed, err := cfgkit.Load[cfg](sources, cfgkit.Reveal())
	if err != nil {
		t.Fatal(err)
	}

	var a, b bytes.Buffer
	if err := masked.Explain(&a); err != nil {
		t.Fatal(err)
	}
	if err := revealed.Explain(&b); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a.String(), theSecret) {
		t.Error("the default Explain leaked the secret")
	}
	if !strings.Contains(b.String(), theSecret) {
		t.Error("Reveal did not reveal — the escape hatch must work")
	}
}

// TestObserverStillSeesTheRealValue pins the documented exception. An audit
// sink needs the real value; cfgkit cannot know whether a given sink is a safe
// place for one, so it hands the value over and marks the field Secret.
func TestObserverStillSeesTheRealValue(t *testing.T) {
	type cfg struct {
		Password string `env:"PASSWORD" secret:"true"`
	}
	var seen cfgkit.Field
	_, _, err := cfgkit.Load[cfg](
		cfgkit.WithSources(cfgkit.FromMap(map[string]string{"PASSWORD": theSecret})),
		cfgkit.WithObserver(cfgkit.ObserverFunc(func(f cfgkit.Field) { seen = f })),
	)
	if err != nil {
		t.Fatal(err)
	}
	if seen.Value != theSecret {
		t.Errorf("observer saw %q, want the real value", seen.Value)
	}
	if !seen.Secret {
		t.Error("observer must be told the field is secret so it can decide")
	}
}
