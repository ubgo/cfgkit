package cfgkit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/ubgo/dotenv"
)

// Config data comes in exactly two shapes, and cfgkit models both rather than
// pretending everything is flat.
//
// Flattening nested data into keys does not work: {"hyperdx":{"logsSourceId":…}}
// flattens to "hyperdx.logsSourceId", which matches no environment key, and
// deriving HYPERDX_LOGS_SOURCE_ID from it is impossible because "_" means both
// nesting and word break. So each shape fills the destination struct through
// its own natural mechanism — flat sources by key lookup, structured sources by
// merging onto the struct.

// Source supplies raw string values for keys. Implementations must be safe for
// concurrent use and must not mutate process state.
type Source interface {
	// Name identifies the source in provenance output ("file:.env.local",
	// "environ", "vault"). It appears in error messages and in Explain, so it
	// should name the concrete origin rather than the type.
	Name() string

	// Lookup returns the value for key. found=false means "this source has no
	// opinion", and resolution continues to the next source.
	//
	// An error is NOT a miss: it aborts the whole Load. An unreachable secret
	// store must never be indistinguishable from an unset variable, because
	// that difference is a deploy proceeding with an empty password.
	//
	// COST CONTRACT: Lookup is called once per bound field, so a remote store
	// MUST pre-load or cache. A source that dials the network per key turns a
	// 200-field config into 200 round-trips at boot.
	Lookup(key string) (value string, found bool, err error)
}

// KeyLister is implemented by a source that knows its COMPLETE key set, so
// cfgkit can report a key that matched no field — a typo.
//
// It is optional on purpose, and the omission is the whole design. FromEnviron
// deliberately does NOT implement it: the process environment holds PATH, HOME,
// SHELL and sixty more variables that belong to the machine rather than to this
// application, and a report listing all of them buries the one line that
// matters. Rather than asking FromEnviron to return false from some
// CanEnumerate method — a lie a future refactor could get wrong — the type
// simply lacks the method, and the type system carries that fact permanently.
//
// A source whose keys ARE the application's should implement it: a .env file, a
// prefixed environment, a map, a secret store that can list its own paths. Four
// lines buys typo detection for that source.
type KeyLister interface {
	// Keys returns every key this source could answer for. Order does not
	// matter; duplicates are harmless.
	Keys() []string
}

// StructuredSource merges nested data onto the destination struct. It exists
// because nested data has a shape that flat keys cannot express.
type StructuredSource interface {
	Name() string

	// Apply merges into dst, which already holds defaults and every earlier
	// source's values. Implementations MUST leave absent fields untouched —
	// encoding/json does this natively, which is why FromJSON is four lines.
	// Apply runs once per Load, so it carries no per-key cost concern.
	Apply(dst any) error
}

// listSource is a Source that also knows its key set, for the built-in sources
// whose keys belong to the application.
type listSource struct {
	Source
	keys []string
}

// Keys implements KeyLister.
func (l listSource) Keys() []string { return l.keys }

// withKeys upgrades a source to a KeyLister.
func withKeys(s Source, keys []string) Source { return listSource{Source: s, keys: keys} }

// sourceFunc adapts a plain function into a Source so that injecting a one-off
// lookup never requires declaring a type.
type sourceFunc struct {
	name string
	fn   func(string) (string, bool, error)
}

// Name identifies the source in provenance output.
func (s sourceFunc) Name() string { return s.name }

// Lookup delegates to the wrapped function.
func (s sourceFunc) Lookup(key string) (string, bool, error) { return s.fn(key) }

// SourceFunc wraps fn as a Source named name.
//
// This is the extension point that makes the catalogue open-ended: any flat
// backend — Vault, SSM, Consul, a database table — is a closure, and its
// dependency stays in the caller rather than in cfgkit.
func SourceFunc(name string, fn func(key string) (value string, found bool, err error)) Source {
	return sourceFunc{name: name, fn: fn}
}

// structuredFunc adapts a plain function into a StructuredSource.
type structuredFunc struct {
	name string
	fn   func(any) error
}

// Name identifies the source in provenance output.
func (s structuredFunc) Name() string { return s.name }

// Apply merges the document onto dst. dst is `any` because it is handed
// straight to a decoder such as encoding/json, whose own signature takes any.
func (s structuredFunc) Apply(dst any) error { return s.fn(dst) }

// StructuredFunc wraps fn as a StructuredSource named name.
//
// Because fn receives the destination struct directly, the parser for a format
// lives in the caller: yaml.Unmarshal(b, dst) makes YAML work without cfgkit
// ever importing a YAML package. The library never has to add a format and
// never has to refuse one.
func StructuredFunc(name string, fn func(dst any) error) StructuredSource {
	return structuredFunc{name: name, fn: fn}
}

// FromMap returns a Source backed by an in-memory map. It is the seam tests use
// to run without touching the process environment or the filesystem.
func FromMap(m map[string]string) Source {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return withKeys(SourceFunc("map", func(key string) (string, bool, error) {
		v, ok := m[key]
		return v, ok, nil
	}), keys)
}

// FromEnviron returns a Source backed by the process environment.
func FromEnviron() Source {
	return SourceFunc("environ", func(key string) (string, bool, error) {
		v, ok := os.LookupEnv(key)
		return v, ok, nil
	})
}

// FromPrefixedEnviron is FromEnviron restricted to keys carrying prefix, with
// the prefix stripped before matching.
//
// Why it exists: one process may host several components whose configurations
// would otherwise collide on short names like PORT. The prefix namespaces them
// without every field having to repeat it in a tag.
func FromPrefixedEnviron(prefix string) Source {
	// The prefix defines a closed set that belongs to this application, so
	// unlike FromEnviron this source CAN honestly enumerate. Keys are reported
	// with the prefix stripped, matching what the binder looks up.
	var keys []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if rest, ok := strings.CutPrefix(k, prefix); ok {
			keys = append(keys, rest)
		}
	}
	return withKeys(SourceFunc("environ:"+prefix, func(key string) (string, bool, error) {
		v, ok := os.LookupEnv(prefix + key)
		return v, ok, nil
	}), keys)
}

// FromJSON returns a StructuredSource backed by a JSON object.
//
// This is the seam Pkl uses: `pkl eval -f json` at BUILD time, go:embed the
// result, and hand the bytes here. The pkl binary never has to exist at run
// time, which keeps a JVM out of the production image and turns a config error
// into a build failure rather than a container-start panic.
//
// encoding/json does the whole job: it fills only the fields present in the
// document and leaves everything else untouched, which is exactly the overlay
// semantics a layered loader needs. Note that nested structs MERGE field by
// field while slices and maps REPLACE wholesale — standard json behaviour, and
// almost always what a reader expects, but worth knowing.
func FromJSON(b []byte) StructuredSource {
	return StructuredFunc("json", func(dst any) error {
		return json.Unmarshal(b, dst)
	})
}

// FromFiles returns a Source backed by .env files, parsed by ubgo/dotenv.
//
// Files are consulted in REVERSE order, so a later path in the argument list
// wins — matching the .env / .env.local / .env.<mode> convention users already
// know from Vite, Next and Rails.
//
// A missing file is NOT an error. That is what makes zero-config possible: the
// absence of configuration is a normal, silent, correct outcome, and it mirrors
// dotenv.Open's own promise.
//
// Files are read once, at construction, so Lookup honours the cost contract.
func FromFiles(paths ...string) Source {
	return mergeEnvFiles("file:"+strings.Join(paths, ","), paths, func(path string) (string, bool, error) {
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				// Absent is not an error — see the doc comment.
				return "", false, nil
			}
			return "", false, err
		}
		return string(b), true, nil
	})
}

// FromFS returns a Source backed by .env files inside an fs.FS.
//
// The case this exists for is go:embed: a program can carry its own defaults in
// the binary and still be overridden by a file or the environment, with no file
// needing to exist on the target machine at all.
//
//	//go:embed defaults.env
//	var defaults embed.FS
//
//	cfgkit.WithSources(
//		cfgkit.FromFS(defaults, "defaults.env"),  // compiled in, lowest
//		cfgkit.FromFiles(".env"),                 // optional local override
//		cfgkit.FromEnviron(),                     // deployment wins
//	)
//
// Every rule FromFiles follows holds here: later paths win, a missing entry is
// silent, ${VAR} references resolve and may name a key an earlier file defined,
// and the whole set is read once at construction.
//
// PATHS ARE fs.FS PATHS, not OS paths — always forward-slash separated and
// never rooted, even on Windows, because that is what io/fs specifies. A
// leading "/" or a volume letter will simply not be found.
func FromFS(fsys fs.FS, paths ...string) Source {
	return mergeEnvFiles("fs:"+strings.Join(paths, ","), paths, func(path string) (string, bool, error) {
		b, err := fs.ReadFile(fsys, path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return "", false, nil
			}
			return "", false, err
		}
		return string(b), true, nil
	})
}

// mergeEnvFiles is the shared body of FromFiles and FromFS.
//
// The two differ ONLY in how a path becomes bytes, so the merge order, the
// cross-file reference rule, the expansion policy and the error handling live
// here once. Duplicating them would let the disk and embedded paths drift
// apart, and a difference between "the same config from a file" and "from an
// embed" is exactly the kind of bug nobody thinks to look for.
//
// read returns (content, found, err): found=false means absent, which is
// silent; err is a real failure, which is reported at Lookup time so that
// construction stays total.
func mergeEnvFiles(name string, paths []string, read func(string) (string, bool, error)) Source {
	// Merge eagerly: later files overwrite earlier ones, so a single map
	// lookup answers correctly and no file is re-read per key.
	merged := make(map[string]string)
	var readErr error

	for _, p := range paths {
		content, found, err := read(p)
		if err != nil {
			// Record and keep going: reporting the first unreadable file at
			// Lookup time keeps construction total, and Load aborts on it.
			if readErr == nil {
				readErr = err
			}
			continue
		}
		if !found {
			continue
		}

		// Each file may reference values the EARLIER files defined. dotenv
		// consults this only for a name the file itself does not define, so a
		// file's own value always wins over an inherited one.
		//
		// It reads from `merged`, which grows as the loop proceeds, so the
		// direction matches precedence: .env.local may reference something
		// .env set, and not the other way round. A backward reference would
		// need a second pass and would let a lower-precedence file depend on a
		// higher-precedence one, which is the wrong way for values to flow.
		//
		// os.Environ is deliberately NOT consulted — that is dotenv's own
		// policy for KindInherited and this package's rule about never reading
		// the environment behind the caller's back. Put FromEnviron in the
		// source list to give the environment a say.
		f := dotenv.Parse(content, dotenv.WithLookup(func(n string) (string, bool) {
			v, ok := merged[n]
			return v, ok
		}))

		// ExpandedMap, not Map: `${VAR}` references are resolved, which is the
		// interpolation Docker Compose performs when IT reads a .env. Without
		// it DATABASE_URL=postgres://${DB_HOST}/app arrives with the braces
		// intact — a broken DSN, delivered silently, which is the failure this
		// package exists to prevent.
		vals, err := f.ExpandedMap()
		if err != nil {
			// An unresolvable reference is an error, not an empty value —
			// `${VAR:?message}` exists precisely to fail the load.
			if readErr == nil {
				readErr = err
			}
			continue
		}
		for k, v := range vals {
			merged[k] = v
		}
	}

	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}

	return withKeys(SourceFunc(name, func(key string) (string, bool, error) {
		if readErr != nil {
			return "", false, readErr
		}
		v, ok := merged[key]
		return v, ok, nil
	}), keys)
}

// FromStruct returns a StructuredSource backed by an already-populated Go
// struct, merged onto the destination through the `json:` tags both share.
//
// WHEN THIS IS THE RIGHT TOOL, and when it is not. `Defaults()` is how a struct
// supplies its own baseline: it is typed, refactor-safe, and a renamed field is
// a compile error. This is for the different case where the values come from
// somewhere the source list cannot reach — a config service with its own
// client, a test fixture, a struct assembled by a caller's own logic — and need
// to enter the chain at a chosen precedence rather than as a baseline.
//
//	fetched := myConfigService.Fetch(ctx)          // your client, your types
//	cfgkit.WithSources(
//		cfgkit.FromStruct("service", fetched),      // slots in wherever you put it
//		cfgkit.FromEnviron(),                       // still wins
//	)
//
// It round-trips through encoding/json, which is what makes the overlay
// semantics identical to FromJSON: a field the value does not mention is left
// untouched, so an earlier source's value is not erased by silence. The cost of
// that choice is that ONLY json-tagged fields travel, and a zero value is
// indistinguishable from an unset one unless the field is a pointer or carries
// `omitempty` — the same rule every JSON-shaped source here follows.
func FromStruct(name string, v any) StructuredSource {
	return StructuredFunc(name, func(dst any) error {
		if v == nil {
			// A nil value is not an error: "this layer has nothing to say" is
			// a normal outcome, and the alternative would make every optional
			// layer need a nil check at the call site.
			return nil
		}
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("encoding %T: %w", v, err)
		}
		return json.Unmarshal(b, dst)
	})
}
