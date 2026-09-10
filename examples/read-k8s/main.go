// Command read-k8s reads configuration from Kubernetes, both ways.
//
// The module supports two, and they are not interchangeable:
//
//   - ConfigMap/Secret — an API call. Needs RBAC, and sees the value the
//     moment it is written.
//   - Mount — a projected VOLUME on disk. Needs no RBAC at all, because
//     kubelet already put the file there.
//
// It RUNS outside a cluster: the API path talks to an httptest server and the
// mount path reads a directory laid out exactly as kubelet lays one out.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/ubgo/cfgkit"
	k8s "github.com/ubgo/cfgkit/contrib/source-k8s"
)

// Config is the same struct either way — which is the point.
type Config struct {
	Host     string `env:"HOST" default:"localhost"`
	Port     int    `env:"PORT" default:"8080"`
	Password string `env:"PASSWORD" secret:"true"`
}

// dataLink is kubelet's atomic-swap symlink. A projected volume is a symlink
// to a timestamped directory, and kubelet replaces the LINK on update — so a
// reader that follows it sees either the whole old version or the whole new
// one, never a half-updated mix.
const dataLink = "..data"

// fakeAPI serves one ConfigMap.
func fakeAPI() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]string{"HOST": "api.internal", "PORT": "8443"},
		})
	}))
}

// fakeSecretAPI serves a Secret, whose values are base64 on the wire. The
// adapter decodes them, so a Secret and a ConfigMap behave identically here.
func fakeSecretAPI() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enc := base64.StdEncoding.EncodeToString
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]string{"PASSWORD": enc([]byte("s3cret-from-k8s"))},
		})
	}))
}

// projected builds the real kubelet layout: a timestamped directory holding
// the values, a "..data" symlink pointing at it, and one symlink per key.
func projected(dir string, kv map[string]string) error {
	versioned := filepath.Join(dir, "..2026_09_09_00_00_00.123456789")
	if err := os.MkdirAll(versioned, 0o755); err != nil {
		return err
	}
	for k, v := range kv {
		if err := os.WriteFile(filepath.Join(versioned, k), []byte(v), 0o644); err != nil {
			return err
		}
	}
	if err := os.Symlink(filepath.Base(versioned), filepath.Join(dir, dataLink)); err != nil {
		return err
	}
	for k := range kv {
		link := filepath.Join(dir, k)
		if err := os.Symlink(filepath.Join(dataLink, k), link); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	cm := fakeAPI()
	defer cm.Close()
	sec := fakeSecretAPI()
	defer sec.Close()

	// 1. The API path. In a pod this is the whole integration —
	//    k8s.ConfigMap("app-config") — because the namespace, the API address
	//    and the token all come from the service account kubelet mounted.
	_, _ = fmt.Fprintln(w, "from the API:")
	_, res, err := cfgkit.Load[Config](cfgkit.WithSources(
		k8s.ConfigMap("app-config",
			k8s.WithServer(cm.URL), k8s.WithToken("t"), k8s.WithNamespace("default")),
		k8s.Secret("app-secrets",
			k8s.WithServer(sec.URL), k8s.WithToken("t"), k8s.WithNamespace("default")),
	))
	if err != nil {
		return err
	}
	if err := res.Explain(w); err != nil {
		return err
	}

	// 2. The mount path. No API, no RBAC, no token — the file is already
	//    there. This is the one to reach for when the pod should not have
	//    permission to read the API at all.
	// A fixed RELATIVE directory, not os.MkdirTemp: the mount path appears in
	// the SOURCE column, and a random temp path would make this example's
	// output different on every run and impossible to pin.
	const dir = "projected-volume"
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	// Cleanup failure is not actionable and must not mask the real result.
	defer func() { _ = os.RemoveAll(dir) }()
	if err := projected(dir, map[string]string{
		"HOST": "mounted.internal", "PORT": "9443", "PASSWORD": "s3cret-from-disk",
	}); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "from a projected volume:")
	_, mres, err := cfgkit.Load[Config](cfgkit.WithSources(k8s.Mount(dir)))
	if err != nil {
		return err
	}
	return mres.Explain(w)
}
