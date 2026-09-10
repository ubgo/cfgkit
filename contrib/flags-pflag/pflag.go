// Package pflag adds cobra/pflag flag sets as a cfgkit source.
//
// It is a separate module because it carries pflag, and the core promises one
// dependency. A program without cobra never compiles this package.
//
// The package does NOT declare flags. Cobra owns its commands and their flags;
// this adapter only reads a flag set that has already been parsed. Generating
// flags from the config struct — as qor5/confx does — was rejected for three
// reasons: it fights cobra for the same names, it cannot control when parsing
// happens, and it turns 150 config fields into 150 entries in --help.
package pflag

import (
	"github.com/spf13/pflag"
	"github.com/ubgo/cfgkit"
)

// Source returns a cfgkit source backed by a parsed pflag set.
//
// ONLY flags the user actually typed are included. pflag spells that
// Flag.Changed, and honouring it is the difference between working and the
// long-standing viper defect (GH-671, GH-375) where a flag's own default
// silently overrides a config file.
//
// Concretely: declare --port with default 9999, put PORT=3000 in .env, and type
// no flag. Reading every flag yields 9999 and the file is dead. Reading only
// typed flags yields nothing, and 3000 stands.
//
// ORDER MATTERS: a flag set holds nothing until it is parsed, and cobra parses
// when the command runs. Build this inside RunE, never in init() or a
// package-level variable. A source built too early sees an empty set, silently
// contributes nothing, and looks like "flags do not work".
//
// Fields opt in with the `flag:"name"` tag; a field without one is invisible
// here.
func Source(fs *pflag.FlagSet) cfgkit.Source {
	typed := make(map[string]string)
	fs.Visit(func(f *pflag.Flag) {
		// Visit already walks only changed flags, but the explicit check keeps
		// the rule visible at the point it matters and survives a future switch
		// to VisitAll by someone who has not read this comment.
		if f.Changed {
			typed[f.Name] = f.Value.String()
		}
	})
	return cfgkit.FlagSource("pflag", typed)
}
