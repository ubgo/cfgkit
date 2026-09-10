package k8s

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ubgo/cfgkit"
)

// dataLink is the symlink kubelet maintains inside a projected volume, always
// pointing at the currently-active timestamped directory. Reading through it
// rather than listing the mount directly is what makes a read ATOMIC: kubelet
// updates a ConfigMap by writing a new timestamped directory and then swinging
// this one symlink, so a reader that follows it sees either the whole old
// version or the whole new one, never a mixture.
const dataLink = "..data"

// mountOptions collects what a caller may override for a mounted read.
type mountOptions struct {
	optional bool
}

// MountOption configures Mount.
//
// It is a separate type from Option because the two constructors share almost
// nothing: a mounted read makes no API call, so it has no server, token,
// namespace or client to configure. One option type covering both would offer
// WithToken on a function that cannot use it.
type MountOption func(*mountOptions)

// MountOptional makes a MISSING mount directory an empty source rather than an
// error.
//
// The default matches ConfigMap and Secret above: you named this mount, so its
// absence means the volume was not attached — a manifest mistake rather than a
// normal outcome.
func MountOptional() MountOption { return func(o *mountOptions) { o.optional = true } }

// Mount reads a ConfigMap or Secret that Kubernetes has MOUNTED AS FILES,
// rather than through the API.
//
//	volumeMounts:
//	  - name: config
//	    mountPath: /etc/app-config
//
//	k8s.Mount("/etc/app-config")
//
// WHY THIS EXISTS ALONGSIDE ConfigMap(). They read the same object by two
// different mechanisms, and the trade is real:
//
//	Mount()      no RBAC rule, no API call, no token, and kubelet REFRESHES the
//	             files in place — but the volume must be declared in the pod
//	             spec, and a mounted key cannot be read from another namespace.
//	ConfigMap()  works for any object the service account may read, including
//	             one not mounted — but needs an RBAC rule and a round trip.
//
// Mount is usually the better default in a pod: it is what the platform already
// does for you, and it is the only one of the two that keeps working when the
// API server is unreachable.
//
// Each FILE IS ONE KEY: the filename is the key and the file's contents are the
// value, which is exactly how kubelet projects `data`. Nested directories are
// NOT descended into — a ConfigMap has no nesting to project, so a subdirectory
// in a mount is something else (a second volume, a `subPath`) and guessing at
// its meaning would invent a key namespace the platform never defined.
//
// One trailing newline is trimmed, matching the `,file` tag option in the core.
// kubelet writes the value's exact bytes with no newline added, so this only
// affects a hand-made directory — which is how the mount is usually faked in a
// local development run, and there an editor's trailing newline is never part
// of the value.
func Mount(dir string, opts ...MountOption) cfgkit.Source {
	o := &mountOptions{}
	for _, fn := range opts {
		fn(o)
	}

	data, err := readMount(dir, o)
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	return &source{name: "k8smount:" + dir, data: data, err: err, keys: keys}
}

// readMount lists the mount and reads every projected key.
func readMount(dir string, o *mountOptions) (map[string]string, error) {
	// Follow ..data when it is present. Its absence is normal too: a mount made
	// with `subPath`, and any hand-made directory, has no symlink layer.
	root := dir
	if _, err := os.Stat(filepath.Join(dir, dataLink)); err == nil {
		root = filepath.Join(dir, dataLink)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) && o.optional {
			return map[string]string{}, nil
		}
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("mount %s does not exist — is the volume declared in the pod spec? "+
				"(pass k8s.MountOptional() if it is genuinely optional)", dir)
		}
		return nil, fmt.Errorf("reading mount %s: %w", dir, err)
	}

	out := make(map[string]string, len(entries))
	for _, e := range entries {
		name := e.Name()

		// Skip kubelet's own bookkeeping: `..data` itself and the
		// `..2026_01_02_15_04_05.123456789` directories it swings between.
		// Every one of them starts with a dot, and a ConfigMap key cannot —
		// the API rejects it — so this can never hide a real key.
		if strings.HasPrefix(name, ".") {
			continue
		}

		path := filepath.Join(root, name)
		info, err := os.Stat(path) // Stat, not Lstat: every projected key is a symlink.
		if err != nil {
			// A DANGLING SYMLINK is how a deleted key appears: kubelet removes
			// the target and the link survives until the next resync. It means
			// "this key is gone", so it is skipped rather than reported —
			// failing here would make an unrelated key's deletion take the
			// process down on its next restart.
			continue
		}
		if info.IsDir() {
			continue
		}

		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s from mount %s: %w", name, dir, err)
		}
		out[name] = strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
	}

	if len(out) == 0 && !o.optional {
		return nil, fmt.Errorf("mount %s has no keys "+
			"(pass k8s.MountOptional() if that is expected)", dir)
	}
	return out, nil
}
