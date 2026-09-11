// Package cobracfg exposes cfgkit's check, explain and document verbs as a
// cobra command you can mount on an existing CLI.
//
// Every cfgkit user needs these three verbs, and every one of them has to write
// the same fifty-line main to get them — because Check, Explain and Document are
// generic over the caller's own config struct, so no prebuilt binary can ever
// supply them. This package is that fifty lines, written once.
//
// It asks for nothing from the host application. The only import a caller needs
// is cobra, and the command never opens a database, a cache or a network
// connection: the verbs it exposes exist precisely to run when those are absent
// or misconfigured.
package cobracfg

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/ubgo/cfgkit"
)

// DefaultUse is the command's name when none is given.
//
// Named rather than inlined because it is the string a host CLI's help text,
// its documentation and its users all have to agree on.
const DefaultUse = "config"

type options struct {
	use string
	out io.Writer
}

// Option configures the command.
type Option func(*options)

// WithUse renames the command.
//
// A host whose CLI already has a "config" verb, or which prefers "cfg", renames
// it here rather than rebuilding the command tree by hand.
func WithUse(use string) Option { return func(o *options) { o.use = use } }

// WithOut redirects output.
//
// It exists for tests and for a host that composes output itself; the default
// is the command's own stdout, which cobra points at os.Stdout. Errors are
// never written here — they are returned, so the host's error handling and
// exit code stay the host's decision.
func WithOut(w io.Writer) Option { return func(o *options) { o.out = w } }

// Command returns a "config" command with check, explain and document
// subcommands, bound to the configuration type T.
//
//	root.AddCommand(cobracfg.Command[Config](appconfig.Options()))
//
// load is a REQUIRED parameter rather than an option, and that is deliberate.
// cfgkit has no default source chain — by design, because a convenience that
// hides precedence is the magic it exists to avoid — so a command built with no
// sources would read nothing and cheerfully report "configuration is valid".
// A gate that passes without looking is worse than no gate, so the caller is
// made to name the sources. Pass nil to mean "compiled-in defaults only", which
// is then an explicit choice rather than an omission.
//
// Pass the SAME options the application loads with. A check against a different
// source list is a check of something the application never runs.
//
// The command builds nothing and connects to nothing. Mount it on a CLI whose
// other commands need a database and it still runs when that database is
// unreachable, which is the situation its verbs are for.
func Command[T any](load []cfgkit.Option, opts ...Option) *cobra.Command {
	o := &options{use: DefaultUse}
	for _, fn := range opts {
		fn(o)
	}

	cmd := &cobra.Command{
		Use:   o.use,
		Short: "Inspect and validate the configuration",
		Long: "Inspect and validate the configuration.\n\n" +
			"These verbs read configuration only. They open no database and no\n" +
			"network connection, so they work on a machine where the application\n" +
			"itself could not start — which is when they are most useful.",
	}
	cmd.AddCommand(checkCmd[T](o, load), explainCmd[T](o, load), documentCmd[T](o, load))
	return cmd
}

// writer resolves where a subcommand writes.
//
// cmd.OutOrStdout() rather than os.Stdout so a host that redirected the root
// command's output keeps that redirection, and so cobra's own test helpers work.
func (o *options) writer(cmd *cobra.Command) io.Writer {
	if o.out != nil {
		return o.out
	}
	return cmd.OutOrStdout()
}

func checkCmd[T any](o *options, load []cfgkit.Option) *cobra.Command {
	var strict bool

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Validate the configuration; exit non-zero on any problem",
		Long: "Validate the configuration without constructing the application.\n\n" +
			"Every problem is reported at once rather than one per run, because a\n" +
			"misconfigured deploy should be one fix, not one fix per restart.",
		Example: "  app config check\n  APP_ENV=prod app config check\n  app config check --strict",
		Args:    cobra.NoArgs,
		// SilenceUsage: a configuration problem is not a usage problem. Dumping
		// the flag list under a list of bad keys buries the thing the operator
		// needs to read.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load rather than Check: Check discards the Result, and --strict
			// needs the unknown-key findings that only the Result carries.
			// The pipeline it runs is identical.
			_, res, err := cfgkit.Load[T](load...)
			if err != nil {
				return err
			}
			if strict {
				if err := unknownErr(res); err != nil {
					return err
				}
			}
			_, err = fmt.Fprintln(o.writer(cmd), "configuration is valid")
			return err
		},
	}
	cmd.Flags().BoolVar(&strict, "strict", false,
		"also fail when a source supplied a key that matched no field")
	return cmd
}

// unknownErr turns unknown-key findings into a failure, one per line.
//
// It is behind --strict, never the default, because cfgkit reports these as
// ADVISORY on purpose: one .env legitimately serves several audiences, and a
// deployment file carrying GITHUB_SECRET_* keys for a pipeline alongside the
// application's own settings is a pattern, not a mistake. Failing by default
// would break it. Use --strict on a file this application alone owns, where an
// unrecognised key really is a typo.
func unknownErr(res *cfgkit.Result) error {
	u := res.Unknown()
	if len(u) == 0 {
		return nil
	}
	lines := make([]string, len(u))
	for i, k := range u {
		lines[i] = "  " + k.String()
	}
	return fmt.Errorf("%d key(s) matched no field:\n%s", len(u), strings.Join(lines, "\n"))
}

func explainCmd[T any](o *options, load []cfgkit.Option) *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "explain",
		Short: "Print every field with the source that set it",
		Long: "Print every field with the source that set it.\n\n" +
			"This answers \"why is this value what it is here\" — the question that\n" +
			"otherwise costs an afternoon. Secret values are masked, so the output\n" +
			"is safe to paste into an issue.",
		Example:      "  app config explain\n  app config explain --json",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, res, err := cfgkit.Load[T](load...)
			if err != nil {
				return err
			}
			w := o.writer(cmd)
			if asJSON {
				data, err := explainJSON(res)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(w, string(data))
				return err
			}
			if err := res.Explain(w); err != nil {
				return err
			}
			return explainFooter(w, res)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the provenance record as JSON")
	return cmd
}

// explainFooter writes what the field table cannot say.
//
// The table answers "where did this value come from". These three answer the
// questions asked when the table looks wrong: which mode's rules applied, which
// files were even opened, and which keys were read and then silently ignored.
// The last is the one that costs real time — a typo'd key binds nothing, so the
// field keeps its default and the table looks entirely correct.
func explainFooter(w io.Writer, res *cfgkit.Result) error {
	if _, err := fmt.Fprintf(w, "\nmode: %s\n", res.Mode()); err != nil {
		return err
	}
	// Files is empty unless the load used the default .env chain; printing an
	// empty list would imply no file was read, which is a different claim.
	if files := res.Files(); len(files) > 0 {
		if _, err := fmt.Fprintf(w, "files consulted: %s\n", strings.Join(files, ", ")); err != nil {
			return err
		}
	}
	u := res.Unknown()
	if len(u) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(w, "\n%d key(s) matched no field (probable typos):\n", len(u)); err != nil {
		return err
	}
	for _, k := range u {
		if _, err := fmt.Fprintf(w, "  %s\n", k.String()); err != nil {
			return err
		}
	}
	return nil
}

// explainDoc is the shape of `explain --json`.
//
// Provenance is cfgkit's OWN record, embedded verbatim as RawMessage rather
// than re-declared: a field added to it appears here without a change in this
// package, and this package can never misreport one. The two siblings are what
// the record does not carry — Unknown and Files live on Result, not in its JSON.
//
// Why not decode the record into a map and add keys to it: that requires
// map[string]any, re-parses JSON this program just produced, and introduces two
// error branches that cannot fire. A typed envelope has neither problem.
type explainDoc struct {
	Provenance json.RawMessage     `json:"provenance"`
	Unknown    []cfgkit.UnknownKey `json:"unknown,omitempty"`
	Files      []string            `json:"files,omitempty"`
}

// explainJSON renders the machine-readable form of explain.
func explainJSON(res *cfgkit.Result) ([]byte, error) {
	record, err := res.JSON()
	if err != nil {
		return nil, err
	}
	return json.Marshal(explainDoc{
		Provenance: record,
		Unknown:    res.Unknown(),
		Files:      res.Files(),
	})
}

func documentCmd[T any](o *options, load []cfgkit.Option) *cobra.Command {
	return &cobra.Command{
		Use:   "document",
		Short: "Print the .env.example contract generated from the struct",
		Long: "Print the .env.example contract generated from the struct.\n\n" +
			"Generating it is what stops the contract drifting from the code: there\n" +
			"is no hand-maintained copy to fall behind. Secrets are emitted empty,\n" +
			"so the output is safe to commit.",
		Example:      "  app config document > .env.example",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cfgkit.Document[T](o.writer(cmd), load...)
		},
	}
}
