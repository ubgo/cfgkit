package cfgkit

import (
	"os"
	"path/filepath"
	"strings"
)

// The conventional file chain, named once so the order is stated in exactly one
// place. It is the layout Vite, Next and Rails already established, so an
// operator who has met any of them knows what each file is for without reading
// this package's documentation.
const (
	// fileBase is committed to the repository and holds the values a fresh
	// clone needs. It is the only file in the chain that is expected to exist.
	fileBase = ".env"
	// suffixLocal marks a file that is gitignored and personal to one machine.
	suffixLocal = ".local"
)

// chainFiles returns the file chain for a mode, lowest precedence first.
//
// `.env.local` is EXCLUDED in test mode, following create-react-app rather than
// Vite. The reason is reproducibility: a developer's personal, gitignored file
// silently changing the result of `go test` on one machine and not another is
// the exact class of bug this package exists to make impossible. Every other
// mode loads it, because that is what it is for.
func chainFiles(dir string, mode Mode) []string {
	files := []string{filepath.Join(dir, fileBase)}
	if mode != ModeTest {
		files = append(files, filepath.Join(dir, fileBase+suffixLocal))
	}
	files = append(files,
		filepath.Join(dir, fileBase+"."+string(mode)),
		filepath.Join(dir, fileBase+"."+string(mode)+suffixLocal),
	)
	return files
}

// DefaultSources configures the conventional chain: the `.env` files for the
// resolved mode, then the process environment, then any extra sources given.
//
//	cfg, res, err := cfgkit.Load[Config](cfgkit.DefaultSources())
//
// is the same as writing this by hand, which most services otherwise do
// identically:
//
//	cfgkit.WithSources(
//		cfgkit.FromFiles(".env", ".env.local", ".env.dev", ".env.dev.local"),
//		cfgkit.FromEnviron(),
//	)
//	cfgkit.WithMode(cfgkit.ModeDev)
//
// WHY THIS IS NOT THE MAGIC THE NON-GOALS REJECT: the chain is a list this
// function builds and hands to the same WithSources every caller uses. Nothing
// is hidden at resolution time — Explain still names the exact file or
// `environ` that supplied each field, and Result.Files reports the chain that
// was consulted. What a reader loses is seeing the filenames in main.go; what
// they gain is that the chicken-and-egg below is solved once, correctly, rather
// than re-derived wrongly in every service.
//
// Extra sources are placed at the HIGHEST precedence, after the environment,
// because that is where a flag set or a per-run override belongs. A source that
// must sit lower — a secret store that the environment should be able to
// override — needs the explicit list; this helper does not try to express every
// arrangement, only the common one.
//
// A missing file is not an error, which is what keeps zero-config true: the
// whole chain may be absent and the load still succeeds on compiled-in
// defaults.
func DefaultSources(extra ...any) Option {
	return DefaultSourcesIn(".", extra...)
}

// DefaultSourcesIn is DefaultSources rooted at dir, for a program whose
// configuration does not live in the working directory — a test fixture
// directory, or a service in a monorepo run from the repository root.
func DefaultSourcesIn(dir string, extra ...any) Option {
	return func(o *options) {
		o.chain = true
		o.chainDir = dir
		o.chainExtra = extra
	}
}

// applyChain resolves the file chain and installs it as the source list.
//
// It runs after every Option has been applied, not inside the Option itself,
// because the chain depends on the mode and the mode may be set by a WithMode
// that appears AFTER DefaultSources in the argument list. Resolving eagerly
// would make the two options order-dependent, which is the kind of rule nobody
// remembers.
func applyChain(o *options) {
	if !o.chain {
		return
	}
	o.mode = resolveChainMode(o.chainDir, o.modeKey, o.mode)

	files := chainFiles(o.chainDir, o.mode)
	sources := make([]any, 0, len(o.chainExtra)+2)
	sources = append(sources, FromFiles(files...), FromEnviron())
	sources = append(sources, o.chainExtra...)

	// The caller's own WithSources, if any, sits BELOW the chain: an explicitly
	// listed source is a base the conventional chain then overrides, which is
	// the only ordering under which combining the two is not a silent trap.
	o.sources = append(o.sources, sources...)
	o.chainFiles = files
}

// resolveChainMode answers "which mode am I in" before any source has run.
//
// This is the chicken-and-egg the chain creates: `.env.<mode>` cannot be chosen
// until the mode is known, but the mode is itself a configuration value that an
// operator reasonably writes into a .env file. Resolving it needs a first pass.
//
// The order is the same precedence rule the rest of the package uses, so there
// is nothing new to learn:
//
//  1. an explicit WithMode — the caller stated it, nothing may override that
//  2. the real process environment — how a deployment sets it
//  3. `.env` and `.env.local` — how a developer's machine sets it
//  4. dev
//
// Only the base files are consulted in the first pass, never `.env.<mode>`:
// reading a file whose name depends on the answer would be circular.
//
// Without this pass, `APP_ENV=production` written into a .env file was silently
// ignored, the mode stayed dev, and every RequiredIn(ModeProd, …) rule quietly
// did not fire — a production deployment running under development strictness
// with nothing in the logs to say so.
func resolveChainMode(dir, modeKey string, explicit Mode) Mode {
	if explicit != "" {
		return explicit
	}
	if v, ok := os.LookupEnv(modeKey); ok {
		return parseMode(v)
	}

	// FromFiles merges with the later file winning, so .env.local beats .env,
	// matching the chain's own order. A read error is ignored here: the real
	// load consults the same files and reports it properly, and failing this
	// early would report it twice.
	probe := FromFiles(filepath.Join(dir, fileBase), filepath.Join(dir, fileBase+suffixLocal))
	if v, ok, err := probe.Lookup(modeKey); err == nil && ok {
		return parseMode(v)
	}
	return ModeDev
}

// parseMode maps the text an operator writes to a Mode.
//
// Both the short and long spellings are accepted because both are in wide use —
// "prod" in Go tooling, "production" in Node and Rails — and an operator should
// never have to guess which one a library wants. Anything unrecognised is dev,
// for the reason resolveMode documents: an unset or misspelled mode means a
// developer's machine far more often than it means production, and every
// prod-only rule fails closed anyway.
func parseMode(v string) Mode {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "prod", "production":
		return ModeProd
	case "test", "testing":
		return ModeTest
	default:
		return ModeDev
	}
}
