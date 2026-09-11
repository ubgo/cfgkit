package cobracfg_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/ubgo/cfgkit"
	cobracfg "github.com/ubgo/cfgkit/contrib/cli-cobra"
)

// testCfg is deliberately small: this package's job is wiring cobra to cfgkit,
// so the tests assert on what the wiring does, not on binding behaviour the
// core already covers.
type testCfg struct {
	Port   int    `env:"TEST_PORT" default:"8080" doc:"listen port"`
	Host   string `env:"TEST_HOST" default:"localhost"`
	APIKey string `env:"TEST_API_KEY" default:"placeholder" secret:"true" doc:"upstream credential"`
}

// Validate fails on a sentinel port so a test can force the error path without
// depending on a parse failure, which would exercise the core rather than this
// package.
func (c *testCfg) Validate() error {
	if c.Port == 1 {
		return errors.New("TEST_PORT: 1 is reserved")
	}
	return nil
}

// run executes the command with args and returns stdout plus the error cobra
// surfaced. Output is captured through WithOut rather than by swapping
// os.Stdout, so the tests stay parallel-safe.
func run(t *testing.T, args []string, opts ...cobracfg.Option) (string, error) {
	t.Helper()
	return runWith(t, envOnly, args, opts...)
}

// envOnly is the source list the tests load with. cfgkit has no default chain,
// so a test that omitted this would assert on defaults and prove nothing about
// whether the environment reaches the command.
var envOnly = []cfgkit.Option{cfgkit.WithSources(cfgkit.FromEnviron())}

func runWith(t *testing.T, load []cfgkit.Option, args []string, opts ...cobracfg.Option) (string, error) {
	t.Helper()

	var out bytes.Buffer
	cmd := cobracfg.Command[testCfg](load, append([]cobracfg.Option{cobracfg.WithOut(&out)}, opts...)...)
	// A parent root mirrors real mounting and keeps cobra from treating the
	// config command as the program itself.
	root := &cobra.Command{Use: "app"}
	root.AddCommand(cmd)
	root.SetArgs(args)
	root.SetOut(&out)
	root.SetErr(&out)

	err := root.Execute()
	return out.String(), err
}

// isolate unsets every key this package's tests bind, so a developer's own
// environment cannot leak in and produce a failure nobody can reproduce.
// Unset, not blank: cfgkit correctly treats an empty variable as a value.
func isolate(t *testing.T) {
	t.Helper()
	for _, k := range []string{"TEST_PORT", "TEST_HOST", "TEST_API_KEY", "TEST_MODE"} {
		// t.Setenv first so the original value is restored at cleanup; then
		// actually remove it, because an empty variable is a value here.
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unsetting %s: %v", k, err)
		}
	}
}

func TestCheckReportsAValidConfiguration(t *testing.T) {
	isolate(t)

	out, err := run(t, []string{"config", "check"})
	if err != nil {
		t.Fatalf("check on a valid configuration returned %v", err)
	}
	if !strings.Contains(out, "configuration is valid") {
		t.Errorf("check did not confirm validity; got %q", out)
	}
}

// TestCheckReturnsTheErrorRatherThanPrintingIt pins the contract that makes the
// command usable as a CI gate: the host decides the exit code, and it can only
// do that if the error comes back rather than being swallowed.
func TestCheckReturnsTheErrorRatherThanPrintingIt(t *testing.T) {
	isolate(t)
	t.Setenv("TEST_PORT", "1")

	out, err := run(t, []string{"config", "check"})
	if err == nil {
		t.Fatal("check accepted a configuration its own Validate rejects")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("the returned error lost the problem text; got %v", err)
	}
	if strings.Contains(out, "configuration is valid") {
		t.Error("check claimed validity while returning an error")
	}
}

// TestCheckDoesNotPrintUsageOnAConfigProblem: a bad key is not a bad
// invocation, and dumping the flag list under the problem buries it.
func TestCheckDoesNotPrintUsageOnAConfigProblem(t *testing.T) {
	isolate(t)
	t.Setenv("TEST_PORT", "1")

	out, _ := run(t, []string{"config", "check"})
	if strings.Contains(out, "Usage:") {
		t.Errorf("a configuration problem printed the usage block:\n%s", out)
	}
}

func TestExplainListsEveryFieldWithItsSource(t *testing.T) {
	isolate(t)
	t.Setenv("TEST_HOST", "db.internal")

	out, err := run(t, []string{"config", "explain"})
	if err != nil {
		t.Fatalf("explain returned %v", err)
	}
	for _, want := range []string{"FIELD", "KEY", "VALUE", "SOURCE", "TEST_PORT", "TEST_HOST"} {
		if !strings.Contains(out, want) {
			t.Errorf("explain output is missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "db.internal") {
		t.Errorf("explain did not report the value the environment set:\n%s", out)
	}
}

// TestExplainMasksSecrets is the reason the output is safe to paste into an
// issue. A regression here leaks a credential into a bug report.
func TestExplainMasksSecrets(t *testing.T) {
	isolate(t)
	t.Setenv("TEST_API_KEY", "super-secret-value")

	out, err := run(t, []string{"config", "explain"})
	if err != nil {
		t.Fatalf("explain returned %v", err)
	}
	if strings.Contains(out, "super-secret-value") {
		t.Errorf("explain leaked a secret value:\n%s", out)
	}
	if !strings.Contains(out, "TEST_API_KEY") {
		t.Errorf("explain hid the secret's KEY as well as its value; the field must still be listed:\n%s", out)
	}
}

func TestExplainJSONIsValidAndAlsoMasks(t *testing.T) {
	isolate(t)
	t.Setenv("TEST_API_KEY", "super-secret-value")

	out, err := run(t, []string{"config", "explain", "--json"})
	if err != nil {
		t.Fatalf("explain --json returned %v", err)
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("explain --json did not emit valid JSON:\n%s", out)
	}
	if strings.Contains(out, "super-secret-value") {
		t.Errorf("explain --json leaked a secret value:\n%s", out)
	}
}

func TestDocumentEmitsEveryKeyWithSecretsBlank(t *testing.T) {
	isolate(t)
	t.Setenv("TEST_API_KEY", "super-secret-value")

	out, err := run(t, []string{"config", "document"})
	if err != nil {
		t.Fatalf("document returned %v", err)
	}
	for _, want := range []string{"TEST_PORT", "TEST_HOST", "TEST_API_KEY", "listen port"} {
		if !strings.Contains(out, want) {
			t.Errorf("document is missing %q:\n%s", want, out)
		}
	}
	// The generated file is meant to be committed, so a secret must never carry
	// a value — not the live one, and not the compiled-in placeholder either.
	if strings.Contains(out, "super-secret-value") || strings.Contains(out, "TEST_API_KEY=placeholder") {
		t.Errorf("document emitted a secret value; the contract is committed:\n%s", out)
	}
}

func TestWithUseRenamesTheCommand(t *testing.T) {
	isolate(t)

	out, err := run(t, []string{"cfg", "check"}, cobracfg.WithUse("cfg"))
	if err != nil {
		t.Fatalf("the renamed command did not run: %v", err)
	}
	if !strings.Contains(out, "configuration is valid") {
		t.Errorf("the renamed command produced no output:\n%s", out)
	}
}

// TestLoadOptionsReachTheLoad proves the pass-through is real rather than
// accepted and dropped. Reveal is the probe because its effect is unambiguous:
// a secret's value can only appear if cfgkit actually received the option.
func TestLoadOptionsReachTheLoad(t *testing.T) {
	isolate(t)
	t.Setenv("TEST_API_KEY", "super-secret-value")

	masked, err := runWith(t, envOnly, []string{"config", "explain"})
	if err != nil {
		t.Fatalf("explain returned %v", err)
	}
	if strings.Contains(masked, "super-secret-value") {
		t.Fatalf("the baseline run already revealed the secret:\n%s", masked)
	}

	revealed, err := runWith(t,
		[]cfgkit.Option{cfgkit.WithSources(cfgkit.FromEnviron()), cfgkit.Reveal()},
		[]string{"config", "explain"})
	if err != nil {
		t.Fatalf("explain with Reveal returned %v", err)
	}
	if !strings.Contains(revealed, "super-secret-value") {
		t.Errorf("Reveal never reached the load; the value stayed masked:\n%s", revealed)
	}
}

// TestNilLoadReadsDefaultsOnly pins the reason load is a required parameter:
// passing nil is legal, but it must visibly mean "defaults only" rather than
// quietly appearing to have consulted the environment.
func TestNilLoadReadsDefaultsOnly(t *testing.T) {
	isolate(t)
	t.Setenv("TEST_HOST", "db.internal")

	out, err := runWith(t, nil, []string{"config", "explain"})
	if err != nil {
		t.Fatalf("explain returned %v", err)
	}
	if strings.Contains(out, "db.internal") {
		t.Errorf("a nil load read the environment; it must read defaults only:\n%s", out)
	}
	if !strings.Contains(out, "localhost") {
		t.Errorf("a nil load did not fall back to the compiled-in default:\n%s", out)
	}
}

// TestSubcommandsRejectArguments: a stray argument is a typo, and silently
// ignoring it would let `config check prod` look like it checked prod.
func TestSubcommandsRejectArguments(t *testing.T) {
	isolate(t)

	for _, verb := range []string{"check", "explain", "document"} {
		if _, err := run(t, []string{"config", verb, "unexpected"}); err == nil {
			t.Errorf("%s accepted a stray positional argument", verb)
		}
	}
}

// TestDefaultUseIsTheDocumentedName guards the constant every host's help text
// and documentation agree on.
func TestDefaultUseIsTheDocumentedName(t *testing.T) {
	if cobracfg.DefaultUse != "config" {
		t.Errorf("DefaultUse = %q, want \"config\"", cobracfg.DefaultUse)
	}
	cmd := cobracfg.Command[testCfg](envOnly)
	if cmd.Use != cobracfg.DefaultUse {
		t.Errorf("Command().Use = %q, want %q", cmd.Use, cobracfg.DefaultUse)
	}
}

// TestWritesGoToTheCommandsOwnStdoutByDefault pins the fallback path in writer:
// without WithOut, output follows whatever the host redirected the command to.
func TestWritesGoToTheCommandsOwnStdoutByDefault(t *testing.T) {
	isolate(t)

	var out bytes.Buffer
	root := &cobra.Command{Use: "app"}
	root.AddCommand(cobracfg.Command[testCfg](envOnly))
	root.SetArgs([]string{"config", "check"})
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("check returned %v", err)
	}
	if !strings.Contains(out.String(), "configuration is valid") {
		t.Errorf("output did not follow the host's redirection; got %q", out.String())
	}
}

// envFile writes a .env in a temp dir and returns a source list reading it.
//
// A file source is required for these tests because FromEnviron deliberately
// contributes no unknown-key findings: its key set is the whole machine, so a
// report would list PATH and HOME and bury the one line that matters.
func envFile(t *testing.T, body string) []cfgkit.Option {
	t.Helper()

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return []cfgkit.Option{cfgkit.WithSources(cfgkit.FromFiles(path))}
}

// TestExplainReportsKeysThatMatchedNoField is the gap this footer closes. A
// typo'd key binds nothing, so the field keeps its default and the table looks
// entirely correct — the mistake is invisible unless it is named.
func TestExplainReportsKeysThatMatchedNoField(t *testing.T) {
	isolate(t)
	load := envFile(t, "TEST_HOST=db.internal\nTEST_HSOT=typo.internal\n")

	out, err := runWith(t, load, []string{"config", "explain"})
	if err != nil {
		t.Fatalf("explain returned %v", err)
	}
	if !strings.Contains(out, "TEST_HSOT") {
		t.Errorf("explain did not report the unmatched key:\n%s", out)
	}
	if !strings.Contains(out, "matched no field") {
		t.Errorf("explain reported the key without saying what is wrong with it:\n%s", out)
	}
	// The good key must still bind: the report is advisory, not a rejection.
	if !strings.Contains(out, "db.internal") {
		t.Errorf("explain dropped the key that did match:\n%s", out)
	}
}

func TestExplainOmitsTheUnknownSectionWhenThereIsNothingToReport(t *testing.T) {
	isolate(t)
	load := envFile(t, "TEST_HOST=db.internal\n")

	out, err := runWith(t, load, []string{"config", "explain"})
	if err != nil {
		t.Fatalf("explain returned %v", err)
	}
	if strings.Contains(out, "matched no field") {
		t.Errorf("explain printed an empty unknown-key section:\n%s", out)
	}
}

// TestExplainReportsTheResolvedMode: "which mode's rules applied" is the first
// question asked when validation behaves unexpectedly, and the field table
// cannot answer it.
func TestExplainReportsTheResolvedMode(t *testing.T) {
	isolate(t)

	out, err := runWith(t, envOnly, []string{"config", "explain"})
	if err != nil {
		t.Fatalf("explain returned %v", err)
	}
	if !strings.Contains(out, "mode: dev") {
		t.Errorf("explain did not report the resolved mode:\n%s", out)
	}

	t.Setenv("TEST_MODE", "prod")
	out, err = runWith(t,
		[]cfgkit.Option{cfgkit.WithSources(cfgkit.FromEnviron()), cfgkit.WithModeKey("TEST_MODE")},
		[]string{"config", "explain"})
	if err != nil {
		t.Fatalf("explain returned %v", err)
	}
	if !strings.Contains(out, "mode: prod") {
		t.Errorf("explain did not reflect the resolved mode:\n%s", out)
	}
}

func TestExplainJSONCarriesTheUnknownKeys(t *testing.T) {
	isolate(t)
	load := envFile(t, "TEST_HOST=db.internal\nTEST_HSOT=typo.internal\n")

	out, err := runWith(t, load, []string{"config", "explain", "--json"})
	if err != nil {
		t.Fatalf("explain --json returned %v", err)
	}

	// The envelope is decoded with concrete types, never map[string]any: a test
	// that decoded loosely would pass even if the shape changed underneath it.
	var doc struct {
		Provenance struct {
			Mode   string `json:"mode"`
			Fields []struct {
				Key    string `json:"key"`
				Source string `json:"source"`
			} `json:"fields"`
		} `json:"provenance"`
		Unknown []struct {
			Key    string `json:"key"`
			Source string `json:"source"`
		} `json:"unknown"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("explain --json emitted unparseable JSON: %v\n%s", err, out)
	}
	// cfgkit's own record must survive embedding, intact and parseable.
	if doc.Provenance.Mode == "" || len(doc.Provenance.Fields) == 0 {
		t.Errorf("the embedded provenance record lost cfgkit's own fields:\n%s", out)
	}
	if len(doc.Unknown) != 1 || doc.Unknown[0].Key != "TEST_HSOT" {
		t.Errorf("explain --json did not carry the unmatched key:\n%s", out)
	}
}

// TestCheckIgnoresUnknownKeysByDefault pins the deliberate default. One .env
// legitimately serves several audiences — deploy keys beside application
// settings — so failing on an unrecognised key would break that pattern.
func TestCheckIgnoresUnknownKeysByDefault(t *testing.T) {
	isolate(t)
	load := envFile(t, "TEST_HOST=db.internal\nGITHUB_SECRET_PAT=for-the-pipeline\n")

	out, err := runWith(t, load, []string{"config", "check"})
	if err != nil {
		t.Fatalf("check failed on an advisory finding: %v", err)
	}
	if !strings.Contains(out, "configuration is valid") {
		t.Errorf("check did not confirm validity:\n%s", out)
	}
}

func TestCheckStrictFailsOnUnknownKeys(t *testing.T) {
	isolate(t)
	load := envFile(t, "TEST_HOST=db.internal\nTEST_HSOT=typo.internal\n")

	_, err := runWith(t, load, []string{"config", "check", "--strict"})
	if err == nil {
		t.Fatal("--strict accepted a key that matched no field")
	}
	if !strings.Contains(err.Error(), "TEST_HSOT") {
		t.Errorf("the --strict failure did not name the offending key: %v", err)
	}
}

func TestCheckStrictPassesWhenEveryKeyMatches(t *testing.T) {
	isolate(t)
	load := envFile(t, "TEST_HOST=db.internal\nTEST_PORT=9999\n")

	out, err := runWith(t, load, []string{"config", "check", "--strict"})
	if err != nil {
		t.Fatalf("--strict rejected a file whose every key matches: %v", err)
	}
	if !strings.Contains(out, "configuration is valid") {
		t.Errorf("--strict did not confirm validity:\n%s", out)
	}
}

// failWriter fails every write, so the error paths behind each Fprintf are
// reachable. Without it those branches are untestable and would have to be
// excused rather than covered.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestWriteFailuresAreReturned(t *testing.T) {
	isolate(t)
	load := envFile(t, "TEST_HOST=db.internal\nTEST_HSOT=typo.internal\n")

	for _, args := range [][]string{
		{"config", "check"},
		{"config", "explain"},
		{"config", "explain", "--json"},
		{"config", "document"},
	} {
		cmd := cobracfg.Command[testCfg](load, cobracfg.WithOut(failWriter{}))
		root := &cobra.Command{Use: "app"}
		root.AddCommand(cmd)
		root.SetArgs(args)
		root.SetOut(failWriter{})
		root.SetErr(&bytes.Buffer{})

		if err := root.Execute(); err == nil {
			t.Errorf("%v swallowed a write failure", args)
		}
	}
}

// TestExplainNamesTheFilesTheChainConsulted covers the default .env chain,
// where the filenames are no longer written in the caller's own code — so
// "which files were even looked at?" is otherwise unanswerable, and a typo'd
// filename looks exactly like a file whose values were overridden.
func TestExplainNamesTheFilesTheChainConsulted(t *testing.T) {
	isolate(t)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TEST_HOST=db.internal\n"), 0o600); err != nil {
		t.Fatalf("writing .env: %v", err)
	}

	out, err := runWith(t,
		[]cfgkit.Option{cfgkit.DefaultSourcesIn(dir)},
		[]string{"config", "explain"})
	if err != nil {
		t.Fatalf("explain returned %v", err)
	}
	if !strings.Contains(out, "files consulted:") {
		t.Errorf("explain did not name the files the chain consulted:\n%s", out)
	}
	if !strings.Contains(out, ".env") {
		t.Errorf("the file list does not mention the file that was read:\n%s", out)
	}
}

// failAfter succeeds for n writes and then fails, so each error branch in the
// footer can be reached in turn. A writer that fails on the first write only
// ever exercises the first branch, leaving the rest to be excused rather than
// covered.
type failAfter struct {
	n int
}

func (w *failAfter) Write(p []byte) (int, error) {
	if w.n <= 0 {
		return 0, errors.New("write failed")
	}
	w.n--
	return len(p), nil
}

// countWriter records how many Write calls a full run makes, so the failure
// walk below knows where to stop rather than guessing.
type countWriter struct{ n int }

func (w *countWriter) Write(p []byte) (int, error) { w.n++; return len(p), nil }

// TestEveryWriteFailureIsReturned walks the failure point across EVERY write a
// full explain makes, so no error return in the command or its footer is merely
// assumed to work. The write count is measured rather than guessed: Explain's
// tabwriter emits many writes of its own, and a hardcoded bound would stop
// inside the table and never reach the footer at all.
func TestEveryWriteFailureIsReturned(t *testing.T) {
	isolate(t)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"),
		[]byte("TEST_HOST=db.internal\nTEST_HSOT=typo.internal\n"), 0o600); err != nil {
		t.Fatalf("writing .env: %v", err)
	}
	load := []cfgkit.Option{cfgkit.DefaultSourcesIn(dir)}

	explain := func(t *testing.T, w io.Writer) error {
		t.Helper()
		cmd := cobracfg.Command[testCfg](load, cobracfg.WithOut(w))
		root := &cobra.Command{Use: "app"}
		root.AddCommand(cmd)
		root.SetArgs([]string{"config", "explain"})
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		return root.Execute()
	}

	total := &countWriter{}
	if err := explain(t, total); err != nil {
		t.Fatalf("the baseline run failed: %v", err)
	}
	if total.n < 2 {
		t.Fatalf("explain made %d writes; the walk would prove nothing", total.n)
	}

	for n := range total.n {
		if err := explain(t, &failAfter{n: n}); err == nil {
			t.Errorf("a write failure after %d of %d writes was swallowed", n, total.n)
		}
	}
}

// TestExplainFailsWhenTheConfigurationDoesNotLoad pins the arm at the top of
// explain's RunE — the one that returns Load's error instead of rendering a
// table.
//
// Why it matters: explain is what an operator reaches for when something is
// wrong, so the case where the configuration is TOO wrong to load must produce
// the reason rather than an empty table that looks like a working run.
func TestExplainFailsWhenTheConfigurationDoesNotLoad(t *testing.T) {
	isolate(t)
	t.Setenv("TEST_PORT", "1") // the sentinel testCfg.Validate rejects

	out, err := runWith(t, envOnly, []string{"config", "explain"})
	if err == nil {
		t.Fatal("explain rendered a table for a configuration that does not load")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("the returned error lost the reason: %v", err)
	}
	if strings.Contains(out, "FIELD") {
		t.Errorf("explain printed a field table despite the load failing:\n%s", out)
	}
}

// Same arm, on the --json path: the failure must come back as an error, not as
// an empty or partial JSON document a caller would parse as success.
func TestExplainJSONFailsWhenTheConfigurationDoesNotLoad(t *testing.T) {
	isolate(t)
	t.Setenv("TEST_PORT", "1")

	out, err := runWith(t, envOnly, []string{"config", "explain", "--json"})
	if err == nil {
		t.Fatal("explain --json emitted a document for a configuration that does not load")
	}
	// Not "no output" — the captured buffer also carries cobra's own error
	// print. What must not appear is a JSON document, which a caller piping
	// this into jq would parse as a successful run.
	if strings.Contains(out, "{") {
		t.Errorf("explain --json emitted a document despite the load failing:\n%s", out)
	}
}
