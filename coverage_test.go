// Coverage for the arms the behavioural suites do not reach: error paths in the
// decoder, the writers, and the source layer. Each test names the arm it pins
// so a future refactor knows what it breaks.
package cfgkit_test

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/cfgkit"
)

// errWriter fails every write, exercising the error returns of Explain and
// Document — paths that only fire when stdout is a closed pipe.
type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("pipe closed") }

type wide struct {
	I8   int8      `env:"W_I8"`
	I16  int16     `env:"W_I16"`
	U    uint      `env:"W_U"`
	U8   uint8     `env:"W_U8"`
	F32  float32   `env:"W_F32"`
	F64  float64   `env:"W_F64"`
	At   time.Time `env:"W_AT"`
	IP   net.IP    `env:"W_IP"`
	Ints []int     `env:"W_INTS"`
	Semi []string  `env:"W_SEMI" delim:";"`
}

// TestDecodeEveryKind covers the supported type set in one pass, including the
// sized integer and float branches the common config shapes never reach.
func TestDecodeEveryKind(t *testing.T) {
	cfg, _, err := cfgkit.Load[wide](cfgkit.WithSources(cfgkit.FromMap(map[string]string{
		"W_I8": "-8", "W_I16": "-16", "W_U": "7", "W_U8": "255",
		"W_F32": "1.5", "W_F64": "2.5",
		"W_AT":   "2026-09-07T10:00:00Z",
		"W_IP":   "10.0.0.1",
		"W_INTS": "1,2,3",
		"W_SEMI": "a;b",
	})))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.I8 != -8 || cfg.I16 != -16 || cfg.U != 7 || cfg.U8 != 255 {
		t.Errorf("integer kinds: %+v", cfg)
	}
	if cfg.F32 != 1.5 || cfg.F64 != 2.5 {
		t.Errorf("float kinds: %v %v", cfg.F32, cfg.F64)
	}
	if !cfg.At.Equal(time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("time = %v", cfg.At)
	}
	if cfg.IP.String() != "10.0.0.1" {
		t.Errorf("net.IP = %v — a stdlib TextUnmarshaler must bind", cfg.IP)
	}
	if len(cfg.Ints) != 3 || cfg.Ints[2] != 3 {
		t.Errorf("[]int = %v", cfg.Ints)
	}
	if len(cfg.Semi) != 2 || cfg.Semi[1] != "b" {
		t.Errorf("custom delimiter ignored: %v", cfg.Semi)
	}
}

// TestDecodeErrorsPerKind pins that every decoder branch reports rather than
// silently zeroing. A silent zero is the failure mode this package exists to
// prevent: a service comes up on the wrong port with nothing in the logs.
func TestDecodeErrorsPerKind(t *testing.T) {
	for key, bad := range map[string]string{
		"W_I8":   "x",
		"W_U":    "-1", // negative into unsigned
		"W_F64":  "x",
		"W_AT":   "not-a-time",
		"W_INTS": "1,x,3",
		"W_IP":   "999.999.999.999",
	} {
		_, _, err := cfgkit.Load[wide](cfgkit.WithSources(cfgkit.FromMap(map[string]string{key: bad})))
		if err == nil {
			t.Errorf("%s=%q was accepted", key, bad)
		}
	}
}

type unsupported struct {
	Ch chan int `env:"U_CHAN"`
}

// TestUnsupportedTypeIsReported pins that a type outside the closed set fails
// loudly and names the escape hatch, rather than being skipped in silence.
func TestUnsupportedTypeIsReported(t *testing.T) {
	_, _, err := cfgkit.Load[unsupported](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"U_CHAN": "x"}),
	))
	if err == nil || !strings.Contains(err.Error(), "encoding.TextUnmarshaler") {
		t.Errorf("want an unsupported-type error naming the hatch, got: %v", err)
	}
}

type badDefault struct {
	Port int `env:"BD_PORT" default:"not-a-number"`
}

// TestBadTagDefaultIsReported pins that an unparseable default is a reported
// bug in the struct, not a silent zero.
func TestBadTagDefaultIsReported(t *testing.T) {
	_, _, err := cfgkit.Load[badDefault]()
	var de *cfgkit.DecodeError
	if !errors.As(err, &de) || de.Source != "default" {
		t.Errorf("want a DecodeError attributed to the default, got: %v", err)
	}

	// Document runs the same step, so it must report it too rather than
	// emitting a contract file with a wrong value in it.
	if err := cfgkit.Document[badDefault](io.Discard); err == nil {
		t.Error("Document must report an unparseable default")
	}
}

// TestExplainAndDocumentWriteErrors pin the arms that fire when the destination
// fails mid-write — a closed pipe, a full disk.
func TestExplainAndDocumentWriteErrors(t *testing.T) {
	_, res, err := cfgkit.Load[App]()
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Explain(errWriter{}); err == nil {
		t.Error("Explain must report a write failure")
	}
	if err := cfgkit.Document[App](errWriter{}); err == nil {
		t.Error("Document must report a write failure")
	}
}

// TestUnwrapChains pins that the wrapping errors expose their cause, so
// errors.Is works through them.
func TestUnwrapChains(t *testing.T) {
	inner := errors.New("boom")

	de := &cfgkit.DecodeError{Path: "P", Key: "K", Source: "s", Err: inner}
	if !errors.Is(de, inner) {
		t.Error("DecodeError does not unwrap to its cause")
	}
	se := &cfgkit.SourceError{Source: "s", Key: "K", Err: inner}
	if !errors.Is(se, inner) {
		t.Error("SourceError does not unwrap to its cause")
	}
	// A secret's raw text is withheld, so the message must still be useful.
	if !strings.Contains(de.Error(), "P") || !strings.Contains(de.Error(), "K") {
		t.Errorf("DecodeError message lost its field or key: %s", de)
	}
}

type derr struct {
	V string `env:"-"`
}

func (d *derr) Derive() error { return errors.New("derive failed") }

// TestDeriveErrorIsReported pins that a failing Derive fails the load. A
// computed value that silently stayed empty would surface far from its cause.
func TestDeriveErrorIsReported(t *testing.T) {
	_, _, err := cfgkit.Load[derr]()
	if err == nil || !strings.Contains(err.Error(), "derive failed") {
		t.Errorf("want the Derive error, got: %v", err)
	}
}

// TestFromFilesUnreadable pins that a file which EXISTS but cannot be read is
// an error, unlike a missing one. A broken config must never look like an
// absent config.
func TestFromFilesUnreadable(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	if err := os.WriteFile(p, []byte("A=1\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(p); err == nil {
		t.Skip("mode 0000 is readable here (privileged run) — the arm cannot fire")
	}

	_, _, err := cfgkit.Load[App](cfgkit.WithSources(cfgkit.FromFiles(p)))
	var se *cfgkit.SourceError
	if !errors.As(err, &se) {
		t.Errorf("an unreadable file must abort the load, got: %v", err)
	}
}

type optionalSection struct {
	Name string    `env:"OS_NAME" default:"n"`
	Sub  *subBlock `env:",prefix=SUB_"`
}

type subBlock struct {
	Value string `env:"VALUE"`
}

// TestNilPointerSection covers the pointer-struct branch of the walker: a
// section a source DID touch is allocated and bound. The nil case and the
// `,init` override are covered in section_test.go (CONFIG_SPEC §6.4).
func TestNilPointerSection(t *testing.T) {
	cfg, _, err := cfgkit.Load[optionalSection](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"SUB_VALUE": "v"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sub == nil || cfg.Sub.Value != "v" {
		t.Errorf("Sub = %+v, want the prefixed value bound", cfg.Sub)
	}
}

// TestRequiredInOffMode covers the early return when the current mode does not
// match the rule's mode.
func TestRequiredInOffMode(t *testing.T) {
	if err := cfgkit.RequiredIn(cfgkit.ModeProd, cfgkit.ModeTest, "F", ""); err != nil {
		t.Errorf("a rule scoped to prod must not fire in test: %v", err)
	}
	if err := cfgkit.Required("F", nil); err == nil {
		t.Error("a nil interface must count as unset")
	}
}
