// The paths a coverage report says nothing reaches.
//
// Each one is here because it is a behaviour somebody can actually cause — a
// mistyped source, a badly formatted timestamp, a closed pipe — not because a
// percentage wanted raising. A test that exists only to colour a line green
// asserts nothing and is worse than the gap it hides.
//
// Two of these gaps turned out to be BUGS rather than untested branches, which
// is the argument for reading a coverage report instead of only its total:
//
//   - a structured source setting a field inside an optional section did not
//     count as intent, so the section was pruned back to nil and a Pkl or JSON
//     document's own values were silently discarded;
//   - the special case for time.Time was dead code, because time.Time
//     implements TextUnmarshaler and the escape hatch ran first, so an operator
//     got Go's internal parser error instead of "is not a valid RFC3339 time".
//
// SEVEN BLOCKS ARE DELIBERATELY LEFT UNCOVERED, and pretending otherwise would
// be the dishonest kind of 100%:
//
//	cfgkit.go:313   forEachStruct's non-walkable guard
//	field.go:199    checkShape's non-struct guard
//	field.go:267    typeHasHook's non-struct guard
//	field.go:297    typeWantsBinding's non-struct guard
//	result.go:103   markSourced's nil-map guard
//	result.go:151   Explain's header write error
//	result.go:155   Explain's row write error
//
// The first five are defensive: every caller already guarantees the condition,
// and reaching them would need a test that constructs an impossible state. The
// last two cannot be reached at all — tabwriter BUFFERS, so its Write returns
// no error and the failure surfaces at Flush, which IS covered. Deleting the
// checks would raise the number and remove a correct error path, which is the
// wrong trade.
package cfgkit_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/cfgkit"
)

type edgeCfg struct {
	Host string `env:"E_HOST" default:"localhost"`
	Port int    `env:"E_PORT" default:"8080"`
}

// TestSourceOfTheWrongTypeIsRejected pins the diagnostic for the mistake the
// `...any` source list makes possible: WithSources takes any, because it must
// accept two unrelated interfaces, so passing something that implements neither
// compiles and can only be caught at run time. The message therefore has to
// name the offending type.
func TestSourceOfTheWrongTypeIsRejected(t *testing.T) {
	_, _, err := cfgkit.Load[edgeCfg](cfgkit.WithSources("PORT=9000"))
	if err == nil {
		t.Fatal("want an error: a string is neither a Source nor a StructuredSource")
	}
	for _, want := range []string{"string", "Source", "StructuredSource"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// TestTimeValueMustBeRFC3339 pins the one accepted layout and its error.
//
// Accepting several layouts would make "2026-01-02" mean different instants
// depending on which one matched first, so the rejection is deliberate and the
// message has to say what was expected.
func TestTimeValueMustBeRFC3339(t *testing.T) {
	type cfg struct {
		At time.Time `env:"E_AT"`
	}

	t.Run("valid", func(t *testing.T) {
		got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"E_AT": "2026-09-08T10:30:00Z"}),
		))
		if err != nil {
			t.Fatal(err)
		}
		if got.At.Year() != 2026 || got.At.Month() != time.September {
			t.Errorf("At = %v", got.At)
		}
	})

	t.Run("a date alone is rejected, not guessed at", func(t *testing.T) {
		err := cfgkit.Check[cfg](cfgkit.WithSources(
			cfgkit.FromMap(map[string]string{"E_AT": "2026-09-08"}),
		))
		if err == nil {
			t.Fatal("want an error")
		}
		if !strings.Contains(err.Error(), "RFC3339") {
			t.Errorf("error %q should name the expected format", err)
		}
	})
}

// TestRequiredErrorWithoutAMode pins the unconditional wording. A required
// field that names no mode must not print "in mode=" with an empty value, which
// is what the two branches of RequiredError.Error exist to avoid.
func TestRequiredErrorWithoutAMode(t *testing.T) {
	type cfg struct {
		Key string `env:"E_KEY,required"`
	}
	err := cfgkit.Check[cfg]()
	if err == nil {
		t.Fatal("want a required error")
	}
	if strings.Contains(err.Error(), "mode=") {
		t.Errorf("error %q must not mention a mode: `required` is unconditional", err)
	}

	var re *cfgkit.RequiredError
	if !errors.As(err, &re) {
		t.Fatalf("want a *RequiredError, got %T", err)
	}
	if re.Mode != "" {
		t.Errorf("Mode = %q, want empty for an unconditional requirement", re.Mode)
	}
	if re.Empty {
		t.Error("Empty must be false when no source supplied the key at all")
	}
}

// TestDocumentHonoursOptions pins that Document runs the same option pipeline
// as Load. It has to: the contract file must show the values a reader would
// actually get, and a registered Decoder can change what a default parses to.
func TestDocumentHonoursOptions(t *testing.T) {
	type cfg struct {
		Port int `env:"E_PORT" default:"8080" doc:"the listen port"`
	}

	var out strings.Builder
	if err := cfgkit.Document[cfg](&out, cfgkit.WithMode(cfgkit.ModeProd)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "E_PORT=8080") {
		t.Errorf("Document output %q lost the default", out.String())
	}
	if !strings.Contains(out.String(), "the listen port") {
		t.Errorf("Document output %q lost the doc text", out.String())
	}
}

// TestDocumentReportsAWriteFailure pins that a broken pipe surfaces rather than
// producing a silently truncated contract file. A half-written .env.example
// that CI then diffs would report drift that does not exist.
func TestDocumentReportsAWriteFailure(t *testing.T) {
	type cfg struct {
		A string `env:"E_A" default:"1"`
		B string `env:"E_B" default:"2"`
		C string `env:"E_C" default:"3"`
	}

	// A writer that succeeds once and then fails reaches the separator branch
	// between entries, which a writer failing immediately never does.
	for _, n := range []int{0, 1, 3, 5} {
		if err := cfgkit.Document[cfg](&flakyWriter{ok: n}); err == nil {
			t.Errorf("writer failing after %d writes: want the error surfaced", n)
		}
	}
}

// flakyWriter succeeds for ok writes and fails after that.
type flakyWriter struct{ ok int }

func (w *flakyWriter) Write(p []byte) (int, error) {
	if w.ok <= 0 {
		return 0, errors.New("pipe closed")
	}
	w.ok--
	return len(p), nil
}

// TestExplainReportsAWriteFailureAtEveryStage pins the same for Explain,
// including the trailing structured-sources line, which only appears when a
// structured source ran and so needs its own case.
func TestExplainReportsAWriteFailureAtEveryStage(t *testing.T) {
	type cfg struct {
		Host string `env:"E_HOST" json:"host" default:"localhost"`
	}
	_, res, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromJSON([]byte(`{"host":"from-json"}`)),
	))
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range []int{0, 1, 2} {
		if err := res.Explain(&flakyWriter{ok: n}); err == nil {
			t.Errorf("writer failing after %d writes: want the error surfaced", n)
		}
	}
}

// TestStructuredSourceSkipsUnexportedFields pins that the provenance walker
// leaves private state alone. It shares the `visitable` rule with the binder,
// and the point of sharing it is that the two can never disagree about which
// fields exist.
func TestStructuredSourceSkipsUnexportedFields(t *testing.T) {
	cfg, res, err := cfgkit.Load[privateStateCfg](cfgkit.WithSources(
		cfgkit.FromJSON([]byte(`{"host":"from-json"}`)),
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Fields() {
		if strings.Contains(f.Path, "internal") {
			t.Errorf("private field %s appeared in provenance", f.Path)
		}
	}
	// Absent from provenance is not the same as untouched. Assert the VALUE
	// too: a binder that wrote the field and merely failed to report it would
	// pass the loop above while corrupting private state.
	if cfg.internal.Cache != "" {
		t.Errorf("private field was written: %q", cfg.internal.Cache)
	}
}

type privateStateCfg struct {
	Host     string `env:"E_HOST" json:"host" default:"localhost"`
	internal struct{ Cache string }
}

// TestStructuredSourceWithANilSection pins how the snapshot records a section
// that does not exist yet. It must record something rather than descending into
// nil, or attributing a structured source's changes would panic on any config
// with an optional section — which is most of them.
func TestStructuredSourceWithANilSection(t *testing.T) {
	type smtp struct {
		Host string `env:"HOST" json:"host"`
	}
	type cfg struct {
		Port int   `env:"E_PORT" json:"port" default:"8080"`
		SMTP *smtp `env:",prefix=SMTP_" json:"smtp"`
	}

	t.Run("section absent from the document", func(t *testing.T) {
		got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
			cfgkit.FromJSON([]byte(`{"port":9000}`)),
		))
		if err != nil {
			t.Fatal(err)
		}
		if got.SMTP != nil {
			t.Errorf("SMTP = %+v, want nil — the document did not mention it", got.SMTP)
		}
		if got.Port != 9000 {
			t.Errorf("Port = %d", got.Port)
		}
	})

	t.Run("section present in the document", func(t *testing.T) {
		got, res, err := cfgkit.Load[cfg](cfgkit.WithSources(
			cfgkit.FromJSON([]byte(`{"smtp":{"host":"mail.test"}}`)),
		))
		if err != nil {
			t.Fatal(err)
		}
		if got.SMTP == nil {
			t.Fatal("SMTP = nil, but the document set a field beneath it")
		}
		if got.SMTP.Host != "mail.test" {
			t.Errorf("SMTP.Host = %q", got.SMTP.Host)
		}
		for _, f := range res.Fields() {
			if f.Path == "SMTP.Host" && f.Source != "json" {
				t.Errorf("SMTP.Host source = %q, want json", f.Source)
			}
		}
	})
}

// TestEmbeddedNonStructIsIgnored pins the guards on the shape checks. An
// embedded named scalar is legal Go and carries no fields, so it must be passed
// over rather than treated as a container to search.
func TestEmbeddedNonStructIsIgnored(t *testing.T) {
	type cfg struct {
		Counter
		Port int `env:"E_PORT" default:"8080"`
	}
	got, res, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"E_PORT": "9000"}),
	))
	if err != nil {
		t.Fatalf("an embedded scalar must not trip the shape checks: %v", err)
	}
	if got.Port != 9000 {
		t.Errorf("Port = %d", got.Port)
	}
	if n := len(res.Fields()); n != 1 {
		t.Errorf("bound %d fields, want only Port", n)
	}
}

// Counter is a named scalar, embedded above.
type Counter int

// TestEmptyResultSurfaces pins that the reporting API is total: a config with
// nothing in it produces an empty report rather than a panic or a nil slice
// that a caller has to guard.
func TestEmptyResultSurfaces(t *testing.T) {
	type empty struct{}

	cfg, res, err := cfgkit.Load[empty]()
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("Load returned nil for an empty struct")
	}
	if got := res.Fields(); got == nil || len(got) != 0 {
		t.Errorf("Fields() = %v, want an empty non-nil slice", got)
	}
	if got := res.Unknown(); got == nil || len(got) != 0 {
		t.Errorf("Unknown() = %v, want an empty non-nil slice", got)
	}
	if got := res.Files(); got == nil || len(got) != 0 {
		t.Errorf("Files() = %v, want an empty non-nil slice", got)
	}
	if err := res.Explain(io.Discard); err != nil {
		t.Errorf("Explain on an empty result: %v", err)
	}
	if _, err := res.JSON(); err != nil {
		t.Errorf("JSON on an empty result: %v", err)
	}
	if err := cfgkit.Document[empty](io.Discard); err != nil {
		t.Errorf("Document on an empty struct: %v", err)
	}
}

// TestStructuredSourceCountsAsIntentForASection is the regression test for a
// bug this pass found.
//
// Section pruning (§6.4) asks "did a SOURCE set anything beneath this
// section". Before the fix it only ever heard from the flat binder, because
// markSourced was called from bind alone — so a section configured entirely by
// a JSON or Pkl document was pruned back to nil, the feature silently did not
// start, and the document's own values were discarded with nothing reported.
//
// It is the Pkl path (§12) exactly: evaluate to JSON at build time, embed it,
// and every optional section in it used to vanish.
func TestStructuredSourceCountsAsIntentForASection(t *testing.T) {
	type smtp struct {
		Host string `env:"HOST" json:"host"`
		Port int    `env:"PORT" json:"port" default:"587"`
	}
	type cfg struct {
		Port int   `env:"E_PORT" json:"port" default:"8080"`
		SMTP *smtp `env:",prefix=SMTP_" json:"smtp"`
	}

	got, res, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromJSON([]byte(`{"smtp":{"host":"mail.test"}}`)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.SMTP == nil {
		t.Fatal("SMTP = nil, but a structured source set a field beneath it — a document's own values must not be discarded")
	}
	if got.SMTP.Host != "mail.test" {
		t.Errorf("SMTP.Host = %q, want mail.test", got.SMTP.Host)
	}
	if got.SMTP.Port != 587 {
		t.Errorf("SMTP.Port = %d, want the section's default once it is allocated", got.SMTP.Port)
	}

	// And the section's fields must still be reported, with the right origin.
	var seen bool
	for _, f := range res.Fields() {
		if f.Path == "SMTP.Host" {
			seen = true
			if f.Source != "json" {
				t.Errorf("SMTP.Host source = %q, want json", f.Source)
			}
		}
	}
	if !seen {
		t.Error("SMTP.Host missing from provenance")
	}
}

// TestDefaultAloneStillDoesNotAllocateASection is the other half, and the
// reason the fix had to be "a source set it" rather than "anything set it": a
// default is not a statement of intent, so a section whose fields only have
// defaults must still come back nil.
func TestDefaultAloneStillDoesNotAllocateASection(t *testing.T) {
	type smtp struct {
		Host string `env:"HOST" json:"host" default:"localhost"`
	}
	type cfg struct {
		Port int   `env:"E_PORT" json:"port" default:"8080"`
		SMTP *smtp `env:",prefix=SMTP_" json:"smtp"`
	}

	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromJSON([]byte(`{"port":9000}`)), // says nothing about smtp
	))
	if err != nil {
		t.Fatal(err)
	}
	if got.SMTP != nil {
		t.Errorf("SMTP = %+v, want nil — a default is not intent", got.SMTP)
	}
}

// TestTimeErrorNamesTheFormat pins the diagnostic that a coverage report
// uncovered. time.Time implements encoding.TextUnmarshaler, so the special case
// for it was DEAD CODE and the hatch produced Go's internal parser error
// instead — accurate, and useless to an operator editing a .env file.
func TestTimeErrorNamesTheFormat(t *testing.T) {
	type cfg struct {
		At time.Time `env:"E_AT"`
	}
	err := cfgkit.Check[cfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"E_AT": "2026-09-08"}),
	))
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "RFC3339") {
		t.Errorf("error %q must name the expected format, not Go's layout string", err)
	}
	if strings.Contains(err.Error(), "2006-01-02T15:04:05") {
		t.Errorf("error %q leaks Go's reference layout, which means nothing to an operator", err)
	}
}

// TestOwnTimeTypeStillReachesTheHatch pins that handling time.Time before the
// escape hatch did not close the hatch for anyone else. A defined type does not
// inherit methods, so a caller's own type still wins.
func TestOwnTimeTypeStillReachesTheHatch(t *testing.T) {
	type cfg struct {
		Day loudDate `env:"E_DAY"`
	}
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"E_DAY": "2026-09-08"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if time.Time(got.Day).Day() != 8 {
		t.Errorf("Day = %v, want the caller's own parser to have run", time.Time(got.Day))
	}
}

// loudDate is a caller's own date type accepting a plain date, which time.Time
// deliberately does not.
type loudDate time.Time

func (d *loudDate) UnmarshalText(b []byte) error {
	t, err := time.Parse("2006-01-02", string(b))
	if err != nil {
		return err
	}
	*d = loudDate(t)
	return nil
}

// TestRequiredErrorWithAModeReadsCorrectly covers the other branch of
// RequiredError.Error. The type is exported, so a caller — or a future
// mode-scoped rule — can construct one, and its message has to read properly
// in both shapes.
func TestRequiredErrorWithAModeReadsCorrectly(t *testing.T) {
	cases := []struct {
		err  *cfgkit.RequiredError
		want string
	}{
		{&cfgkit.RequiredError{Path: "DB.URL", Key: "DATABASE_URL"},
			"DB.URL (DATABASE_URL) is required but no source supplied it"},
		{&cfgkit.RequiredError{Path: "DB.URL", Key: "DATABASE_URL", Empty: true},
			"DB.URL (DATABASE_URL) is required but resolved to an empty value"},
		{&cfgkit.RequiredError{Path: "DB.URL", Key: "DATABASE_URL", Mode: cfgkit.ModeProd},
			"DB.URL (DATABASE_URL) is required but no source supplied it in mode=prod"},
		{&cfgkit.RequiredError{Path: "DB.URL", Key: "DATABASE_URL", Mode: cfgkit.ModeProd, Empty: true},
			"DB.URL (DATABASE_URL) is required but resolved to an empty value in mode=prod"},
	}
	for _, tc := range cases {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("Error() = %q, want %q", got, tc.want)
		}
	}
}

// TestUnknownKeysAreSortedBySourceThenKey pins the ordering when more than one
// listable source contributes. Grouping by source is what makes the report
// readable — "these three came from your .env, that one from the map" — and it
// has to be deterministic so a caller can diff it.
func TestUnknownKeysAreSortedBySourceThenKey(t *testing.T) {
	type cfg struct {
		Port int `env:"E_PORT" default:"8080"`
	}
	// Both sources must contribute an unknown key, or the source-comparison
	// branch of the sort is never reached.
	t.Setenv("EDGE_MYSTERY", "1")

	_, res, err := cfgkit.Load[cfg](cfgkit.WithSources(
		cfgkit.FromMap(map[string]string{"ZED": "1", "ALPHA": "2"}),
		cfgkit.FromPrefixedEnviron("EDGE_"),
	))
	if err != nil {
		t.Fatal(err)
	}

	u := res.Unknown()
	if len(u) < 3 {
		t.Fatalf("Unknown() = %v, want both map keys and the prefixed one", u)
	}
	sources := map[string]bool{}
	for _, k := range u {
		sources[k.Source] = true
	}
	if len(sources) < 2 {
		t.Fatalf("Unknown() = %v, want findings from BOTH sources", u)
	}
	for i := 1; i < len(u); i++ {
		prev, cur := u[i-1], u[i]
		if prev.Source > cur.Source || (prev.Source == cur.Source && prev.Key > cur.Key) {
			t.Errorf("out of order at %d: %v then %v", i, prev, cur)
		}
	}
}

// TestDocumentReportsAFailureWritingTheDocComment covers the doc-comment branch
// specifically: a contract file truncated after the key but before its
// explanation is still a broken file.
func TestDocumentReportsAFailureWritingTheDocComment(t *testing.T) {
	type cfg struct {
		Port int `env:"E_PORT" default:"8080" doc:"the listen port"`
	}
	// A one-field contract is three writes: the doc comment, the optional
	// marker, and the key line. Each of the first three must surface.
	for n := range 3 {
		if err := cfgkit.Document[cfg](&flakyWriter{ok: n}); err == nil {
			t.Errorf("writer failing after %d writes: want the error surfaced", n)
		}
	}
}

// TestSectionWithNoFieldsAtAll is a shape nobody writes on purpose and somebody
// eventually writes by accident — a section whose fields were all removed. It
// must prune like any other, not panic.
func TestSectionWithNoFieldsAtAll(t *testing.T) {
	type hollow struct{}
	type cfg struct {
		Port  int     `env:"E_PORT" default:"8080"`
		Empty *hollow `env:",prefix=EMPTY_"`
	}
	got, res, err := cfgkit.Load[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	if got.Empty != nil {
		t.Errorf("Empty = %+v, want nil — nothing beneath it was sourced", got.Empty)
	}
	if n := len(res.Fields()); n != 1 {
		t.Errorf("reported %d fields, want only Port", n)
	}
}

// TestUnreachablePointerChildIsDetected reaches the pointer branch of the shape
// scan: the tagged fields are behind a POINTER one level below an embedded
// unexported pointer, so a scan that did not follow pointers would miss them
// and report nothing.
func TestUnreachablePointerChildIsDetected(t *testing.T) {
	type outer struct {
		*ptrParent
		Port int `env:"E_PORT" default:"8080"`
	}
	err := cfgkit.Check[outer]()
	if err == nil {
		t.Fatal("want an error: the fields under this pointer can never be filled")
	}
	var ufe *cfgkit.UnreachableFieldError
	if !errors.As(err, &ufe) {
		t.Fatalf("want an *UnreachableFieldError, got %T: %v", err, err)
	}
}

type ptrParent struct {
	Child *ptrChild // the tags are one pointer deeper
}

type ptrChild struct {
	Host string `env:"DEEP_HOST"`
}

// TestUnreachablePointerHookIsDetected is the same for a hook behind a pointer,
// so both scans follow pointers rather than only one of them.
func TestUnreachablePointerHookIsDetected(t *testing.T) {
	type outer struct {
		hookBehindPointer
	}
	err := cfgkit.Check[outer]()
	if err == nil {
		t.Fatal("want an error: this hook can never run")
	}
	var uhe *cfgkit.UnreachableHookError
	if !errors.As(err, &uhe) {
		t.Fatalf("want an *UnreachableHookError, got %T: %v", err, err)
	}
}

type hookBehindPointer struct {
	Sub *hookedChild
}

type hookedChild struct {
	Port int `env:"HC_PORT"`
}

func (h *hookedChild) Validate() error { return nil }

// TestFileReferencesAreExpanded is the regression test for a bug found while
// trying to write one accurate sentence in dotenv's README.
//
// FromFiles used Map(), which does NOT resolve ${VAR}. A composed DSN arrived
// with the braces intact — a broken connection string, delivered silently,
// which is exactly the failure this package exists to prevent. Docker Compose,
// Node's dotenv and this parser's own GetExpanded all resolve it.
func TestFileReferencesAreExpanded(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	write(t, env, "DB_HOST=db.internal\nDB_PORT=5432\n"+
		"DATABASE_URL=postgres://${DB_HOST}:${DB_PORT}/app\n")

	type cfg struct {
		URL  string `env:"DATABASE_URL"`
		Host string `env:"DB_HOST"`
	}
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(cfgkit.FromFiles(env)))
	if err != nil {
		t.Fatal(err)
	}
	if want := "postgres://db.internal:5432/app"; got.URL != want {
		t.Errorf("URL = %q, want %q — ${VAR} must be resolved", got.URL, want)
	}
	if got.Host != "db.internal" {
		t.Errorf("Host = %q", got.Host)
	}
}

// TestFileReferenceDefaultsAndRequired pins the two Compose forms that make
// expansion worth having: a fallback, and a reference that FAILS the load.
func TestFileReferenceDefaultsAndRequired(t *testing.T) {
	dir := t.TempDir()

	t.Run("a default fills an undefined reference", func(t *testing.T) {
		env := filepath.Join(dir, "a.env")
		write(t, env, "URL=postgres://${MISSING:-localhost}/app\n")

		type cfg struct {
			URL string `env:"URL"`
		}
		got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(cfgkit.FromFiles(env)))
		if err != nil {
			t.Fatal(err)
		}
		if want := "postgres://localhost/app"; got.URL != want {
			t.Errorf("URL = %q, want %q", got.URL, want)
		}
	})

	t.Run("a required reference fails the load", func(t *testing.T) {
		// ${VAR:?message} is how a .env file states its own requirement, and it
		// must be an error rather than an empty value — an empty password that
		// looks configured is the case this rejects.
		env := filepath.Join(dir, "b.env")
		write(t, env, "URL=postgres://${MUST_BE_SET:?set the database host}/app\n")

		type cfg struct {
			URL string `env:"URL" default:"unset"`
		}
		err := cfgkit.Check[cfg](cfgkit.WithSources(cfgkit.FromFiles(env)))
		if err == nil {
			t.Fatal("want an error: the file itself declared this reference required")
		}
		if !strings.Contains(err.Error(), "MUST_BE_SET") {
			t.Errorf("error %q should name the unresolved reference", err)
		}
	})
}

// TestReferencesReachEarlierFilesInTheChain pins that a later file may name a
// value an earlier one defined — the .env / .env.local / .env.<mode> layout
// working the way anyone would assume.
//
// It did not, at first: each file was expanded on its own before the merge, so
// a ${DB_HOST} in .env.local resolved to nothing. dotenv's WithLookup is the
// seam that fixes it, and it is consulted only for a name the file itself does
// not define.
func TestReferencesReachEarlierFilesInTheChain(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, ".env")
	local := filepath.Join(dir, ".env.local")
	write(t, base, "DB_HOST=db.internal\nDB_PORT=5432\n")
	write(t, local, "URL=postgres://${DB_HOST}:${DB_PORT}/app\n")

	type cfg struct {
		URL string `env:"URL"`
	}
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(cfgkit.FromFiles(base, local)))
	if err != nil {
		t.Fatal(err)
	}
	if want := "postgres://db.internal:5432/app"; got.URL != want {
		t.Errorf("URL = %q, want %q — a later file must see an earlier one", got.URL, want)
	}
}

// TestAFilesOwnValueWinsOverAnInheritedOne pins the precedence inside
// expansion. WithLookup must be a FALLBACK: if the file defines the name
// itself, that definition wins, or a shared base file could silently override
// the environment-specific one that referenced it.
func TestAFilesOwnValueWinsOverAnInheritedOne(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, ".env")
	local := filepath.Join(dir, ".env.local")
	write(t, base, "DB_HOST=shared\n")
	write(t, local, "DB_HOST=mine\nURL=postgres://${DB_HOST}/app\n")

	type cfg struct {
		URL string `env:"URL"`
	}
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(cfgkit.FromFiles(base, local)))
	if err != nil {
		t.Fatal(err)
	}
	if want := "postgres://mine/app"; got.URL != want {
		t.Errorf("URL = %q, want %q — the file's own definition wins", got.URL, want)
	}
}

// TestReferencesDoNotReachLaterFiles pins the direction, which is a consequence
// of precedence rather than a gap: values flow from lower precedence to higher,
// so an earlier file depending on a later one would invert that.
func TestReferencesDoNotReachLaterFiles(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, ".env")
	local := filepath.Join(dir, ".env.local")
	write(t, base, "URL=postgres://${DB_HOST:-unresolved}/app\n")
	write(t, local, "DB_HOST=db.internal\n")

	type cfg struct {
		URL string `env:"URL"`
	}
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(cfgkit.FromFiles(base, local)))
	if err != nil {
		t.Fatal(err)
	}
	if want := "postgres://unresolved/app"; got.URL != want {
		t.Errorf("URL = %q, want %q — a reference may not reach forward", got.URL, want)
	}
}

// TestExpansionDoesNotReadTheEnvironment pins the rule that keeps FromFiles
// honest. Compose consults the shell environment when IT expands; this package
// does not, because reading os.Environ behind the caller's back is exactly what
// its source list exists to make explicit. Add FromEnviron to give the
// environment a say.
func TestExpansionDoesNotReadTheEnvironment(t *testing.T) {
	t.Setenv("SECRET_FROM_MACHINE", "leaked")

	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	write(t, env, "URL=x-${SECRET_FROM_MACHINE:-absent}\n")

	type cfg struct {
		URL string `env:"URL"`
	}
	got, _, err := cfgkit.Load[cfg](cfgkit.WithSources(cfgkit.FromFiles(env)))
	if err != nil {
		t.Fatal(err)
	}
	if want := "x-absent"; got.URL != want {
		t.Errorf("URL = %q, want %q — expansion must not read the process environment", got.URL, want)
	}
}

// write replaces a file's contents.
func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
