// Package kiln reads secrets from a kiln-encrypted environment file as a cfgkit
// source.
//
// kiln (github.com/thunderbottom/kiln) keeps environment variables encrypted at
// rest with age or SSH keys, and decrypts them for the identities a kiln.toml
// grants access to. That makes it the one source in this catalogue whose file
// is SAFE TO COMMIT — which is the whole reason it is worth an adapter: it
// removes the gap between "the config is in the repo" and "the secrets are
// somewhere else nobody can find".
//
// It carries a real dependency, like the AWS adapters and unlike the six
// no-SDK sources, and for the same kind of reason: the work here is age
// decryption and role-based access, not an HTTP call. Reimplementing either
// would be a worse version of something that already exists, and getting
// cryptography subtly wrong is not a failure that announces itself.
package kiln

import (
	"fmt"
	"strings"

	upstream "github.com/thunderbottom/kiln/pkg/kiln"
	"github.com/ubgo/cfgkit"
)

// Decrypter turns a kiln environment file into plain key/value pairs.
//
// It exists so this module can be TESTED WITHOUT KEYS. The default
// implementation is the real one; a test supplies a fake and never generates an
// age identity, writes a kiln.toml, or depends on what is in the developer's
// ~/.kiln directory. Taking a cryptography dependency does not require taking
// its key management into the test suite.
type Decrypter interface {
	// Decrypt returns every variable in the named environment file.
	//
	// keyPath may be empty, meaning "discover a usable private key from the
	// standard locations", which is what the CLI does.
	Decrypt(configPath, keyPath, file string) (map[string]string, error)
}

type options struct {
	keyPath   string
	prefix    string
	decrypter Decrypter
	optional  bool
}

// Option configures a source.
type Option func(*options)

// WithKeyPath names the private key to decrypt with — an age or SSH key.
//
// The default discovers one from the standard locations (~/.kiln/kiln.key,
// ~/.ssh/id_ed25519, …), which is what the kiln CLI does, so a developer who
// can already run `kiln` needs no extra configuration.
func WithKeyPath(path string) Option { return func(o *options) { o.keyPath = path } }

// WithPrefix keeps only variables whose names carry the prefix, and STRIPS it
// before binding.
//
// Stripping is the difference from koanf's provider, which filters but keeps
// the prefix. Here the point of a prefix is that one encrypted file can serve
// several components without every struct repeating the namespace in its tags —
// the same thing FromPrefixedEnviron does in the core, and it would be
// confusing for the two to mean different things.
func WithPrefix(prefix string) Option { return func(o *options) { o.prefix = prefix } }

// WithDecrypter replaces the decryption step.
//
// It is what makes this module testable, and it is a real seam too: a caller
// who already holds an unlocked identity can decrypt once and share it rather
// than paying for a second key discovery.
func WithDecrypter(d Decrypter) Option { return func(o *options) { o.decrypter = d } }

// Optional makes an EMPTY environment file an empty source rather than an
// error.
//
// The default matches every other remote source: you named this file, so
// finding nothing in it is a deployment mistake rather than a normal outcome.
func Optional() Option { return func(o *options) { o.optional = true } }

// Env reads one environment from a kiln-encrypted file.
//
// configPath is the kiln.toml; file is the environment named inside it
// ("production", "staging"). Both come from kiln's own model rather than being
// invented here.
//
//	cfgkit.WithSources(
//		kiln.Env("kiln.toml", "production"),
//		cfgkit.FromEnviron(),   // still wins
//	)
//
// Decryption happens ONCE, at construction. cfgkit calls Lookup per bound
// field, and decrypting per key would unlock the identity once per field and
// leave that many more copies of plaintext in memory.
func Env(configPath, file string, opts ...Option) cfgkit.Source {
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}
	if o.decrypter == nil {
		o.decrypter = realDecrypter{}
	}

	name := "kiln:" + file
	data, err := fetch(configPath, file, o)

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	return &source{name: name, data: data, err: err, keys: keys}
}

// source is the decrypted environment, immutable after construction.
type source struct {
	name string
	data map[string]string
	err  error
	keys []string
}

// Name identifies the source in provenance output — "kiln:production".
func (s *source) Name() string { return s.name }

// Lookup returns the value for key.
//
// A decryption failure is an ERROR, never a miss. A file this identity may not
// read must not be indistinguishable from an unset key, because that difference
// is a deploy proceeding with an empty password.
func (s *source) Lookup(key string) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	v, ok := s.data[key]
	return v, ok, nil
}

// Keys implements cfgkit.KeyLister, so a variable in the file that matches no
// field is reported as a probable typo.
func (s *source) Keys() []string { return s.keys }

func fetch(configPath, file string, o *options) (map[string]string, error) {
	vars, err := o.decrypter.Decrypt(configPath, o.keyPath, file)
	if err != nil {
		// The upstream message names which step failed — loading the config,
		// discovering a key, or being denied by the file's own access rules —
		// and that is more useful than anything this package could add.
		return nil, fmt.Errorf("decrypting %s from %s: %w", file, configPath, err)
	}

	out := make(map[string]string, len(vars))
	for k, v := range vars {
		if o.prefix != "" {
			rest, ok := strings.CutPrefix(k, o.prefix)
			if !ok {
				continue
			}
			k = rest
		}
		out[k] = v
	}

	if len(out) == 0 && !o.optional {
		return nil, fmt.Errorf("no variables in %s of %s "+
			"(pass kiln.Optional() if that is expected)", file, configPath)
	}
	return out, nil
}

// realDecrypter is the production path: kiln's own library, doing the age
// decryption and the access-control check.
type realDecrypter struct{}

// Decrypt is the production path: kiln's own library, performing the age
// decryption and the access-control check. An empty keyPath means discover
// one, matching the kiln CLI.
func (realDecrypter) Decrypt(configPath, keyPath, file string) (map[string]string, error) {
	cfg, err := upstream.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}

	if keyPath == "" {
		// Match the CLI: discover a usable key rather than demanding one, so a
		// developer who can run `kiln` needs no extra configuration here.
		discovered, err := upstream.DiscoverPrivateKey()
		if err != nil {
			return nil, err
		}
		keyPath = discovered
	}

	identity, err := upstream.NewIdentityFromKey(keyPath)
	if err != nil {
		return nil, err
	}
	// Cleanup zeroes the unlocked key material. Deferring it here rather than
	// leaving it to the caller is the point of doing decryption in one place.
	defer identity.Cleanup()

	vars, cleanup, err := upstream.GetAllEnvironmentVars(identity, cfg, file)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// The values are copied into strings BEFORE cleanup runs, because cleanup
	// wipes the buffers they point at. Returning the []byte directly would hand
	// back memory that is about to be zeroed.
	out := make(map[string]string, len(vars))
	for k, v := range vars {
		out[k] = string(v)
	}
	return out, nil
}
