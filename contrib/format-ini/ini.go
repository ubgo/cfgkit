// Package ini adds INI documents as a cfgkit source.
//
// It is a separate module because it carries an INI parser, and the core
// promises exactly one dependency.
//
// INI IS A FLAT SOURCE, NOT A STRUCTURED ONE, which is the design decision
// worth explaining. YAML, TOML and JSON have a type system and a document
// shape, so they merge onto a struct through their own tags. INI has neither:
// every value is a string, and its only structure is one level of sections. So
// it maps onto cfgkit's flat Source interface — the same one .env files use —
// and a key is addressed the way an operator writes it.
package ini

import (
	"fmt"
	"os"

	"github.com/ubgo/cfgkit"
	"gopkg.in/ini.v1"
)

// SectionSeparator joins a section name to a key: "database.host".
//
// A dot is used rather than an underscore because an INI key may itself contain
// underscores, and a separator that can appear inside a name makes the mapping
// ambiguous — the same reason cfgkit refuses to derive keys by splitting on "_".
const SectionSeparator = "."

// Source returns a flat source backed by an INI document.
//
// Keys in the DEFAULT (unnamed) section are addressed by their own name.
// Everything else is addressed as "section.key":
//
//	port = 8080
//
//	[database]
//	host = db.internal
//
//	Port int    `env:"port"`
//	Host string `env:"database.host"`
//
// The names are case-sensitive and used exactly as written, because an INI file
// is written by an operator and guessing at a canonical case would make a
// working file stop working.
func Source(name string, doc []byte) cfgkit.Source {
	f, err := ini.Load(doc)
	return newSource(name, f, err)
}

// File returns a flat source backed by an INI file.
//
// A MISSING file is not an error: it is a normal, silent outcome, matching
// FromFiles in the core. A file that exists but cannot be read or parsed IS an
// error, because a broken configuration must never look like an absent one.
func File(path string) cfgkit.Source {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newSource("ini:"+path, nil, nil)
		}
		return newSource("ini:"+path, nil, err)
	}
	f, err := ini.Load(b)
	return newSource("ini:"+path, f, err)
}

// newSource flattens the document once, at construction, so Lookup is a map
// read — cfgkit calls it once per bound field.
func newSource(name string, f *ini.File, err error) cfgkit.Source {
	s := &source{name: name, err: err, data: map[string]string{}}
	if err != nil || f == nil {
		return s
	}
	for _, sec := range f.Sections() {
		prefix := ""
		// ini.v1 calls the unnamed section "DEFAULT"; its keys are addressed
		// by their own name, as an operator would write them.
		if sec.Name() != ini.DefaultSection {
			prefix = sec.Name() + SectionSeparator
		}
		for _, k := range sec.Keys() {
			s.data[prefix+k.Name()] = k.Value()
		}
	}
	s.keys = make([]string, 0, len(s.data))
	for k := range s.data {
		s.keys = append(s.keys, k)
	}
	return s
}

type source struct {
	name string
	data map[string]string
	keys []string
	err  error
}

// Name identifies the source in provenance output.
func (s *source) Name() string { return s.name }

// Lookup returns the value for key. A parse failure is an ERROR, never a miss:
// a broken file must not be indistinguishable from an absent one.
func (s *source) Lookup(key string) (string, bool, error) {
	if s.err != nil {
		return "", false, fmt.Errorf("%s: %w", s.name, s.err)
	}
	v, ok := s.data[key]
	return v, ok, nil
}

// Keys implements cfgkit.KeyLister, so a key in the file that matches no field
// is reported as a probable typo. An INI file's keys were written for this
// application, which is why it can honestly enumerate.
func (s *source) Keys() []string { return s.keys }
