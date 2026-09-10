// Package nats reads a NATS JetStream key/value bucket as a cfgkit source.
//
// A KV bucket is the natural configuration store for a fleet that already runs
// NATS: the connection, the credentials and the operational story exist
// already, and adding Consul or etcd next to it would be a second cluster to
// run for the same job.
//
// It carries a dependency, unlike the six no-SDK sources in this catalogue,
// and for the reason the catalogue states: there is no HTTP endpoint here to
// call. NATS speaks its own wire protocol, and a hand-rolled client would be a
// worse copy of one that already exists.
package nats

import (
	"errors"
	"fmt"
	"strings"

	upstream "github.com/nats-io/nats.go"
	"github.com/ubgo/cfgkit"
)

// KV is the slice of a JetStream key/value bucket this package uses.
//
// It exists so the module can be TESTED WITHOUT A SERVER — no test here starts
// nats-server, and no test depends on one being installed. It is a real seam
// besides: a program that already holds a bucket handle passes it in rather
// than opening a second connection for configuration alone.
//
// Both methods mirror nats.KeyValue, so the real client satisfies this
// interface without an adapter.
type KV interface {
	// Keys lists every key in the bucket.
	Keys(opts ...upstream.WatchOpt) ([]string, error)
	// Get returns one entry, or an error wrapping nats.ErrKeyNotFound.
	Get(key string) (upstream.KeyValueEntry, error)
}

type options struct {
	prefix   string
	kv       KV
	connect  []upstream.Option
	optional bool
}

// Option configures a source.
type Option func(*options)

// WithPrefix keeps only keys carrying the prefix, and STRIPS it before binding.
//
// Stripping is the difference from koanf's provider, which filters but keeps
// the prefix. Here the point of a prefix is that one bucket can serve several
// services without every struct repeating the namespace in its tags — the same
// thing FromPrefixedEnviron does in the core, and it would be confusing for the
// two to mean different things.
func WithPrefix(prefix string) Option { return func(o *options) { o.prefix = prefix } }

// WithKV supplies an already-open bucket instead of dialing.
//
// When it is set, url and bucket are ignored and NO connection is opened or
// closed by this package — the caller owns the handle's lifetime, because a
// package that closed a connection it did not open would break the program that
// was still using it.
func WithKV(kv KV) Option { return func(o *options) { o.kv = kv } }

// WithOptions passes NATS connect options through — credentials, TLS, timeouts.
//
// They are the vendor's own options rather than a re-declared subset, because
// any subset would be a list this package has to keep chasing.
func WithOptions(opts ...upstream.Option) Option {
	return func(o *options) { o.connect = append(o.connect, opts...) }
}

// Optional makes an EMPTY bucket an empty source rather than an error.
//
// The default matches every other remote source: you named this bucket, so
// finding nothing in it is a deployment mistake rather than a normal outcome.
// It never suppresses a connection or permission failure — those are "could not
// look", not "nothing there".
func Optional() Option { return func(o *options) { o.optional = true } }

// Bucket reads a JetStream key/value bucket.
//
//	cfgkit.WithSources(
//		nats.Bucket("nats://localhost:4222", "app-config"),
//		cfgkit.FromEnviron(),   // still wins
//	)
//
// url accepts the comma-separated form NATS itself accepts
// ("nats://one,nats://two").
//
// The bucket is read ONCE, at construction, and the connection this package
// opened is closed immediately afterwards. cfgkit calls Lookup per bound field,
// so a per-key read would turn a fifty-field configuration into fifty round
// trips at boot and hold a connection open for the life of the process to do
// it.
func Bucket(url, bucket string, opts ...Option) cfgkit.Source {
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}

	name := "nats:" + bucket
	data, err := fetch(url, bucket, o)

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	return &source{name: name, data: data, err: err, keys: keys}
}

// source is the bucket's contents, immutable after construction.
type source struct {
	name string
	data map[string]string
	err  error
	keys []string
}

// Name identifies the source in provenance output — "nats:app-config".
func (s *source) Name() string { return s.name }

// Lookup returns the value for key.
//
// A connection or permission failure is an ERROR, never a miss. A bucket this
// process may not read must not be indistinguishable from an unset key, because
// that difference is a deploy proceeding with an empty password.
func (s *source) Lookup(key string) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	v, ok := s.data[key]
	return v, ok, nil
}

// Keys implements cfgkit.KeyLister, so a key in the bucket that matches no
// field is reported as a probable typo. A configuration bucket's keys were
// written for this application, which is why it can honestly enumerate.
func (s *source) Keys() []string { return s.keys }

func fetch(url, bucket string, o *options) (map[string]string, error) {
	kv := o.kv
	if kv == nil {
		opened, closeConn, err := dial(url, bucket, o)
		if err != nil {
			return nil, err
		}
		// Closed as soon as the read finishes: nothing after construction needs
		// it, and holding a connection for the life of the process to serve a
		// map that never changes is a resource leak with extra steps.
		defer closeConn()
		kv = opened
	}

	names, err := kv.Keys()
	if err != nil {
		// An empty bucket reports ErrNoKeysFound rather than an empty list, so
		// it has to be translated here or every empty bucket would look like a
		// failure — which is a different thing and gets a different answer.
		if errors.Is(err, upstream.ErrNoKeysFound) {
			names = nil
		} else {
			return nil, fmt.Errorf("listing bucket %q: %w", bucket, err)
		}
	}

	out := make(map[string]string, len(names))
	for _, name := range names {
		key := name
		if o.prefix != "" {
			rest, ok := strings.CutPrefix(key, o.prefix)
			if !ok {
				continue
			}
			key = rest
		}

		entry, err := kv.Get(name)
		if err != nil {
			// A key deleted between the list and the read is a race, not a
			// failure: the bucket is live and other writers exist. Treating it
			// as fatal would make an unrelated deletion able to crash an
			// unrelated service's boot.
			if errors.Is(err, upstream.ErrKeyNotFound) {
				continue
			}
			return nil, fmt.Errorf("reading %q from bucket %q: %w", name, bucket, err)
		}
		out[key] = string(entry.Value())
	}

	if len(out) == 0 && !o.optional {
		return nil, fmt.Errorf("no keys in bucket %q "+
			"(pass nats.Optional() if that is expected)", bucket)
	}
	return out, nil
}

// dial opens a connection, resolves the bucket, and returns a close func.
//
// Returning the closer rather than closing here is what lets the caller hold
// the connection open across the whole read — resolving the bucket and then
// closing would leave a handle on a dead connection.
func dial(url, bucket string, o *options) (KV, func(), error) {
	nc, err := upstream.Connect(url, o.connect...)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to %s: %w", url, err)
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, nil, fmt.Errorf("opening JetStream on %s: %w", url, err)
	}

	kv, err := js.KeyValue(bucket)
	if err != nil {
		nc.Close()
		return nil, nil, fmt.Errorf("opening bucket %q: %w", bucket, err)
	}
	return kv, nc.Close, nil
}
