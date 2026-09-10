// Package properties adds Java-style .properties files as a cfgkit source.
//
// It is a separate module because it carries a properties parser, and the core
// promises exactly one dependency.
//
// LIKE INI AND UNLIKE YAML, THIS IS A FLAT SOURCE. A .properties file has no
// type system and no document shape — every value is a string and every key is
// a flat name — so it maps onto the same Source interface .env files use, and a
// key is addressed exactly as an operator writes it.
//
// The format earns an adapter rather than a note because it is what a polyglot
// shop already has: a Spring Boot service and a Go service reading the SAME
// application.properties is the case this exists for.
package properties

import (
	"fmt"
	"os"

	"github.com/magiconair/properties"
	"github.com/ubgo/cfgkit"
)

// Source returns a flat source backed by a .properties document.
//
// Java's escaping rules are honoured by the parser, including continuation
// lines and \u escapes, which is the main reason to use one rather than split
// on "=".
//
// `${...}` EXPANSION IS DISABLED. The format supports it, but cfgkit resolves
// references at the .env layer where a whole chain of files is visible, and two
// expansion passes over the same value would apply different rules depending on
// which source it came from. A value here is what the file says.
func Source(name string, doc []byte) cfgkit.Source {
	l := &properties.Loader{Encoding: properties.UTF8, DisableExpansion: true}
	p, err := l.LoadBytes(doc)
	return newSource(name, p, err)
}

// File returns a flat source backed by a .properties file.
//
// A MISSING file is not an error: it is a normal, silent outcome, matching
// FromFiles in the core. A file that exists but cannot be read or parsed IS an
// error, because a broken configuration must never look like an absent one.
func File(path string) cfgkit.Source {
	name := "properties:" + path
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newSource(name, nil, nil)
		}
		return newSource(name, nil, err)
	}
	l := &properties.Loader{Encoding: properties.UTF8, DisableExpansion: true}
	p, err := l.LoadBytes(b)
	return newSource(name, p, err)
}

// newSource flattens once, at construction, so Lookup is a map read — cfgkit
// calls it once per bound field.
func newSource(name string, p *properties.Properties, err error) cfgkit.Source {
	s := &source{name: name, err: err, data: map[string]string{}}
	if err != nil || p == nil {
		return s
	}
	for _, k := range p.Keys() {
		// Expansion is disabled on the loader above, so this is the literal
		// value from the file. The empty-string fallback is unreachable: the
		// key came from Keys() a line earlier.
		s.data[k] = p.GetString(k, "")
	}
	s.keys = p.Keys()
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
// is reported as a probable typo.
func (s *source) Keys() []string { return s.keys }
