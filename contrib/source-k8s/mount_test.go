package k8s_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/cfgkit"
	k8s "github.com/ubgo/cfgkit/contrib/source-k8s"
)

// projected builds the directory layout kubelet actually creates: a timestamped
// directory holding the values, a `..data` symlink pointing at it, and one
// symlink per key pointing through `..data`.
//
// Faking the real layout rather than a flat directory is the point — the
// symlink indirection IS the mechanism, and a test against a flat directory
// would pass while the provider failed in every real pod.
func projected(t *testing.T, kv map[string]string) string {
	t.Helper()
	root := t.TempDir()
	stamp := "..2026_01_02_15_04_05.123456789"
	dataDir := filepath.Join(root, stamp)
	if err := os.Mkdir(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for k, v := range kv {
		if err := os.WriteFile(filepath.Join(dataDir, k), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(stamp, filepath.Join(root, "..data")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	for k := range kv {
		if err := os.Symlink(filepath.Join("..data", k), filepath.Join(root, k)); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

type mountCfg struct {
	Host     string `env:"HOST" default:"localhost"`
	Port     int    `env:"PORT" default:"8080"`
	Password string `env:"PASSWORD" secret:"true"`
}

func TestMountBinds(t *testing.T) {
	dir := projected(t, map[string]string{"HOST": "mounted.internal", "PORT": "9000"})

	got, res, err := cfgkit.Load[mountCfg](cfgkit.WithSources(k8s.Mount(dir)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "mounted.internal" || got.Port != 9000 {
		t.Errorf("cfg = %+v", got)
	}
	for _, f := range res.Fields() {
		if f.Path == "Host" && !strings.HasPrefix(f.Source, "k8smount:") {
			t.Errorf("Host source = %q, want the mount named", f.Source)
		}
	}
}

// TestKubeletBookkeepingIsSkipped pins that the `..data` symlink and the
// timestamped directory beside it never become keys. Both start with a dot, and
// a ConfigMap key cannot — the API rejects it — so skipping dot-names can never
// hide a real key.
func TestKubeletBookkeepingIsSkipped(t *testing.T) {
	dir := projected(t, map[string]string{"HOST": "svc"})

	_, res, err := cfgkit.Load[mountCfg](cfgkit.WithSources(k8s.Mount(dir)))
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range res.Unknown() {
		if strings.HasPrefix(u.Key, ".") {
			t.Errorf("kubelet bookkeeping became a key: %q", u.Key)
		}
	}
}

// TestReadIsAtomicThroughDataSymlink is the reason to follow `..data` rather
// than list the mount. kubelet updates a ConfigMap by writing a NEW timestamped
// directory and swinging one symlink, so a reader that follows it sees the whole
// old version or the whole new one — never a mixture.
func TestReadIsAtomicThroughDataSymlink(t *testing.T) {
	dir := projected(t, map[string]string{"HOST": "v1", "PORT": "1"})

	// Simulate kubelet's swap: a second timestamped directory, then relink.
	next := filepath.Join(dir, "..2026_01_02_16_00_00.000000000")
	if err := os.Mkdir(next, 0o755); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"HOST": "v2", "PORT": "2"} {
		if err := os.WriteFile(filepath.Join(next, k), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tmp := filepath.Join(dir, "..data_tmp")
	if err := os.Symlink(filepath.Base(next), tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}

	got, _, err := cfgkit.Load[mountCfg](cfgkit.WithSources(k8s.Mount(dir)))
	if err != nil {
		t.Fatal(err)
	}
	// Both values come from the new generation — never one of each.
	if got.Host != "v2" || got.Port != 2 {
		t.Errorf("cfg = %+v, want both values from the new generation", got)
	}
}

// TestDanglingSymlinkIsSkippedNotFatal pins how a removed key appears in the
// layouts that have no `..data` indirection — a subPath mount, or a hand-made
// directory of symlinks. The link survives its target, and failing on it would
// let one key's deletion take the process down on its next restart.
//
// The FIRST version of this test claimed to cover this and did not: it removed
// a file from the timestamped directory while the provider read through
// `..data`, where the entries are real files, so the key was simply absent and
// the skip never ran. Coverage caught it. Reading through `..data` cannot
// produce a dangling entry at all, which is itself a reason to prefer it.
func TestDanglingSymlinkIsSkippedNotFatal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "HOST"), []byte("svc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "no-such-target"), filepath.Join(dir, "GONE")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	got, res, err := cfgkit.Load[mountCfg](cfgkit.WithSources(k8s.Mount(dir)))
	if err != nil {
		t.Fatalf("a dangling symlink means a deleted key, not a failure: %v", err)
	}
	if got.Host != "svc" {
		t.Errorf("Host = %q, want the surviving key still read", got.Host)
	}
	for _, u := range res.Unknown() {
		if u.Key == "GONE" {
			t.Error("a dangling symlink was bound as a key")
		}
	}
}

// TestDotEntriesAreSkippedInAFlatDirectory covers the same guard on the layout
// where it is reachable. In a projected volume the dot-names live beside
// `..data` and the provider reads through it, so only a flat directory can put
// a dot-entry in front of the reader.
func TestDotEntriesAreSkippedInAFlatDirectory(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"HOST":         "svc",
		"..2026_01_02": "stale",
		".hidden":      "x",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, res, err := cfgkit.Load[mountCfg](cfgkit.WithSources(k8s.Mount(dir)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "svc" {
		t.Errorf("Host = %q", got.Host)
	}
	for _, u := range res.Unknown() {
		if strings.HasPrefix(u.Key, ".") {
			t.Errorf("a dot-entry became a key: %q", u.Key)
		}
	}
}

// TestMountPathThatIsAFileIsReported pins that pointing Mount at a file rather
// than a directory fails with the path named, instead of looking like an empty
// mount.
func TestMountPathThatIsAFileIsReported(t *testing.T) {
	f := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := cfgkit.Check[mountCfg](cfgkit.WithSources(k8s.Mount(f)))
	if err == nil {
		t.Fatal("want an error: the mount path is a file")
	}
	if !strings.Contains(err.Error(), "not-a-dir") {
		t.Errorf("error %q should name the path", err)
	}
}

// TestFlatDirectoryWorksToo covers the layouts that have no symlink layer: a
// `subPath` mount, and the hand-made directory used to fake a mount locally.
func TestFlatDirectoryWorksToo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "HOST"), []byte("flat.internal"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, _, err := cfgkit.Load[mountCfg](cfgkit.WithSources(k8s.Mount(dir)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "flat.internal" {
		t.Errorf("Host = %q", got.Host)
	}
}

// TestOneTrailingNewlineIsTrimmed pins the rule and its reason: kubelet writes
// the value's exact bytes with no newline, so this only affects a hand-made
// directory — where an editor's trailing newline is never part of the value.
func TestOneTrailingNewlineIsTrimmed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "HOST"), []byte("edited.internal\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, _, err := cfgkit.Load[mountCfg](cfgkit.WithSources(k8s.Mount(dir)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "edited.internal" {
		t.Errorf("Host = %q, want one trailing newline trimmed", got.Host)
	}
}

// TestSubdirectoriesAreNotDescended pins a deliberate limit. A ConfigMap has no
// nesting to project, so a subdirectory in a mount is something else — a second
// volume, a subPath — and guessing at its meaning would invent a key namespace
// the platform never defined.
func TestSubdirectoriesAreNotDescended(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "HOST"), []byte("top"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "nested")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "PORT"), []byte("9999"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, _, err := cfgkit.Load[mountCfg](cfgkit.WithSources(k8s.Mount(dir)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 8080 {
		t.Errorf("Port = %d, want the default — a subdirectory is not descended into", got.Port)
	}
	if got.Host != "top" {
		t.Errorf("Host = %q", got.Host)
	}
}

func TestSecretFromAMountIsMasked(t *testing.T) {
	dir := projected(t, map[string]string{"PASSWORD": "hunter2"})

	got, res, err := cfgkit.Load[mountCfg](cfgkit.WithSources(k8s.Mount(dir)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "hunter2" {
		t.Errorf("Password = %q", got.Password)
	}
	for _, f := range res.Fields() {
		if strings.Contains(f.Value, "hunter2") {
			t.Errorf("the secret reached Explain: %q", f.Value)
		}
	}
}

// TestMissingMountNamesTheLikelyCause pins the message for the failure a
// misconfigured pod produces: the volume was never declared.
func TestMissingMountNamesTheLikelyCause(t *testing.T) {
	err := cfgkit.Check[mountCfg](cfgkit.WithSources(
		k8s.Mount(filepath.Join(t.TempDir(), "never-mounted")),
	))
	if err == nil {
		t.Fatal("want an error: the mount does not exist")
	}
	if !strings.Contains(err.Error(), "pod spec") {
		t.Errorf("error %q should name the likely cause", err)
	}
	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("want a *SourceError, got %T", err)
	}
}

func TestOptionalMissingMountIsSilent(t *testing.T) {
	got, _, err := cfgkit.Load[mountCfg](cfgkit.WithSources(
		k8s.Mount(filepath.Join(t.TempDir(), "never-mounted"), k8s.MountOptional()),
	))
	if err != nil {
		t.Fatalf("an optional missing mount must not fail: %v", err)
	}
	if got.Host != "localhost" {
		t.Errorf("Host = %q, want the compiled-in default", got.Host)
	}
}

func TestEmptyMountIsAnErrorByDefault(t *testing.T) {
	err := cfgkit.Check[mountCfg](cfgkit.WithSources(k8s.Mount(t.TempDir())))
	if err == nil {
		t.Fatal("want an error: the mount has no keys")
	}
	if !strings.Contains(err.Error(), "MountOptional()") {
		t.Errorf("error %q should name the way to opt out", err)
	}
}

func TestMountPrecedenceAgainstOtherSources(t *testing.T) {
	dir := projected(t, map[string]string{"HOST": "from-mount"})

	got, _, err := cfgkit.Load[mountCfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"HOST": "from-map", "PORT": "1234"}),
		k8s.Mount(dir),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "from-mount" {
		t.Errorf("Host = %q, want the later source to win", got.Host)
	}
	if got.Port != 1234 {
		t.Errorf("Port = %d, want the earlier source where the later is silent", got.Port)
	}
}

// TestUnreadableFileIsAnErrorNotASkip pins the distinction the mount reader
// has to get right twice.
//
// A DANGLING symlink is skipped: kubelet leaves one behind mid-rotation, and
// it means "this key is gone". A file that exists and cannot be READ is the
// opposite — a permission or I/O fault — and skipping it would hand the
// program a configuration silently missing a key it asked for.
//
// Skipped when running as root, because root can read a 0000 file and the
// arm cannot be reached at all.
func TestUnreadableFileIsAnErrorNotASkip(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions, so the read cannot be made to fail")
	}

	dir := projected(t, map[string]string{"HOST": "h", "PORT": "8080"})

	// Chmod the file inside the versioned directory: the key is a symlink
	// through ..data, and permissions live on the target.
	target, err := filepath.EvalSymlinks(filepath.Join(dir, "PORT"))
	if err != nil {
		t.Fatalf("resolving the key symlink: %v", err)
	}
	if err := os.Chmod(target, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(target, 0o644) })

	_, _, err = k8s.Mount(dir).Lookup("HOST")
	if err == nil {
		t.Fatal("an unreadable key was skipped instead of reported")
	}
	if !strings.Contains(err.Error(), "PORT") {
		t.Errorf("the error does not name the unreadable key: %v", err)
	}
}
