package cfgkit

import "flag"

// FromFlagSet returns a Source backed by a parsed stdlib flag set, matching
// flags to fields through the `flag:"name"` tag.
//
// THE RULE, and the one thing every other library gets wrong: a flag counts
// only when the user actually typed it. A flag's own default must never enter
// the configuration.
//
// Why it matters. Declare --port with default 8080, and put PORT=3000 in a
// .env file. The user types no flag. A reader that asks the flag set for "port"
// receives 8080 and treats it as a value — so the flag default silently beats
// the file, and the file is dead for every field that happens to have a flag.
//
// The stdlib offers two walks and only one is correct:
//
//	fs.VisitAll — every declared flag, defaults included. WRONG.
//	fs.Visit    — only the flags the user typed. CORRECT.
//
// This is a long-lived defect in viper: "Default value of Cobra flag overrides
// the viper env variable" (#671), "BindPFlags functionality does not seem to
// match documentation" (#375). Koanf avoids it by asking the config object
// whether another provider already set the key, which needs a back-reference.
// cfgkit needs neither mechanism, because its defaults already live in Go: a
// flag never has to supply one, so the rule collapses to "typed flags win,
// everything else is invisible".
//
// ORDER: a flag set holds nothing until it is parsed. With cobra, parsing
// happens when the command runs, so Load belongs inside RunE — never in init()
// or a package-level variable. A Load that runs too early sees an empty flag
// set, silently ignores every flag, and looks like "flags do not work".
//
// Flags are also opt-in per field: a config with 150 fields must not produce
// 150 flags, so only a field carrying a `flag:` tag is ever read from here.
func FromFlagSet(fs *flag.FlagSet) Source {
	// Snapshot once, at construction. Visit walks the whole set per call, and
	// Lookup runs once per bound field.
	typed := make(map[string]string)
	fs.Visit(func(f *flag.Flag) {
		typed[f.Name] = f.Value.String()
	})
	return flagSource{name: "flags", typed: typed}
}

// FlagSource wraps a map of flag names the user actually typed, for flag
// packages other than the stdlib's.
//
// A contrib module for cobra/pflag builds this map by walking the flag set and
// keeping only flags whose Changed field is true — pflag's spelling of "the
// user typed it". Exposing the map rather than an interface keeps the contrib
// module to a dozen lines and keeps pflag out of this module's dependencies.
func FlagSource(name string, typed map[string]string) Source {
	return flagSource{name: name, typed: typed}
}

// flagSource is keyed by FLAG NAME ("port"), while every other source is keyed
// by env key ("PORT"). It is a distinct type so bind can recognise it and
// consult the field's flag tag instead of its env key — which keeps Source at
// one method rather than giving it two key concepts.
type flagSource struct {
	name  string
	typed map[string]string
}

// Name identifies the source in provenance — "flags", or the name a contrib
// module chose for its own flag package.
func (s flagSource) Name() string { return s.name }

// Lookup answers by FLAG NAME rather than env key, which is why flagSource is
// a distinct type: bind recognises it and consults the field's flag tag. A
// flag the user did not type is absent here, never a value.
func (s flagSource) Lookup(name string) (string, bool, error) {
	v, ok := s.typed[name]
	return v, ok, nil
}

// keyForSource returns the key to look up in src for this field, and whether
// the source applies to it at all.
//
// A field with no flag tag is invisible to a flag source: flags are opt-in per
// field, because a config with 150 fields must not produce 150 flags.
func keyForSource(src Source, f *field, envKey string) (string, bool) {
	if _, isFlag := src.(flagSource); isFlag {
		if f.Flag == "" {
			return "", false
		}
		return f.Flag, true
	}
	return envKey, true
}
