package compliance

// af compliance, as a command an embedding binary contributes.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Written here rather than in the enterprise binary's main so that the flags,
// the output and the exit codes are testable without a process, and so that
// main stays short enough to read in one go.
//
// The exit code is the part worth getting right, because this is a command that
// runs in a pipeline. Zero when a report was produced. A distinct non-zero when
// a control has evidence of NOT holding, so that a nightly job can fail on a
// broken audit chain without anybody having to parse the document. Controls
// that are merely not evidenced are not a failure: most controls are not
// evidenced on the first day and a command that failed on that would be turned
// off in a week.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
	"github.com/antifailure/antifailure/engine/pkg/afcli"
)

// Exit codes, matching the engine's own vocabulary: 1 is a failure, 3 is a
// configuration problem the user has to fix, 6 is a refusal that is about the
// data rather than about the run.
const (
	exitOK        = 0
	exitFailure   = 1
	exitConfig    = 3
	exitControlNo = 6
)

// Name is what this command is called on the command line.
//
// A constant because two things have to agree about it: the enterprise binary,
// which contributes it, and the community docs test, which allows it on an
// enterprise page precisely because the community tree does not have it. A test
// in this module asserts they still agree.
const Name = "compliance"

// Contributed returns this command in the form an embedding binary registers.
//
// Built here rather than in main so that the help text lives beside the flags
// it describes, and so that a test can assert the binary contributes what the
// community docs test was told to expect.
func Contributed(gather func(ctx context.Context, org string, from, to time.Time) (Evidence, error)) afcli.Command {
	return afcli.Command{
		Use:   Name + " <pack>",
		Short: "Produce evidence for SOC 2 or HIPAA from what this installation recorded",
		Long: strings.TrimSpace(`
Produces evidence for one framework from the audit log, the masking
attestations and the policy decisions this installation recorded.

It is not an audit report and it is not an opinion. Every control says what the
evidence shows, names the artifact so somebody can go and look, and says which
part of the requirement this product covers, which is never all of it. Controls
this product records nothing about are listed as such rather than omitted, so
the gaps are visible rather than implied.

Packs: soc2, hipaa

It exits 6 when a control has evidence of NOT holding, so a nightly job can fail
on a broken audit chain without parsing the document.`),
		Run: func(ctx context.Context, args []string) int {
			return Command(ctx, args, Options{Getenv: os.Getenv, Gather: gather})
		},
	}
}

// Options are what the command needs from the binary embedding it.
type Options struct {
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	// Gather reads the evidence. Injected so the command can be tested without
	// a database, and so the binary decides how it connects.
	Gather func(ctx context.Context, org string, from, to time.Time) (Evidence, error)
}

// Command runs af compliance.
func Command(ctx context.Context, args []string, opts Options) int {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}

	flags := flag.NewFlagSet("compliance", flag.ContinueOnError)
	flags.SetOutput(opts.Stderr)
	var (
		org    = flags.String("org", "", "the organization to report on")
		months = flags.Int("months", 12, "how many months back the report covers")
		format = flags.String("output", "markdown", "markdown or json")
		out    = flags.String("out", "", "write to this file instead of standard output")
	)
	flags.Usage = func() {
		fmt.Fprint(opts.Stderr, usage)
		flags.PrintDefaults()
	}
	// The pack name is taken before the flags are parsed when it comes first,
	// because Go's flag package stops at the first argument that is not a flag.
	// Without this, "af compliance soc2 --org acme" parses no flags at all and
	// answers with the usage text, which reads as if the command were typed
	// wrongly when it was typed the obvious way.
	name := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	if err := flags.Parse(args); err != nil {
		return exitConfig
	}
	// And the other order, so that "af compliance --org acme soc2" works too.
	if rest := flags.Args(); name == "" && len(rest) == 1 {
		name = rest[0]
	} else if len(rest) > 0 {
		fmt.Fprintf(opts.Stderr, "af: unexpected argument %q\n", rest[0])
		return exitConfig
	}
	if name == "" {
		flags.Usage()
		return exitConfig
	}

	pack, known := Packs()[strings.ToLower(name)]
	if !known {
		names := make([]string, 0, len(Packs()))
		for name := range Packs() {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintf(opts.Stderr, "af: there is no %q pack in this build; it has %s\n",
			name, strings.Join(names, " and "))
		return exitConfig
	}

	// The licence gate is here rather than at registration, for the same reason
	// every other one in this module is: a licence can lapse while the process
	// is running, and a feature that asked once keeps working for an
	// organization that stopped paying.
	if !feature.Enabled(ctx, license.FeatureCompliance) {
		fmt.Fprintf(opts.Stderr,
			"af: the compliance_packs feature is not licensed on this installation, so no "+
				"report was produced. Everything it would read is still recorded and nothing "+
				"has been lost; a licence turns this back on unchanged.\n")
		return exitConfig
	}

	if strings.TrimSpace(*org) == "" {
		fmt.Fprintln(opts.Stderr,
			"af: --org is required; a report is about one organization and its period")
		return exitConfig
	}
	if opts.Gather == nil {
		fmt.Fprintln(opts.Stderr,
			"af: this build has no way to read evidence, so no report can be produced")
		return exitFailure
	}

	to := time.Now().UTC()
	from := to.AddDate(0, -*months, 0)

	evidence, err := opts.Gather(ctx, *org, from, to)
	if err != nil {
		fmt.Fprintf(opts.Stderr, "af: the evidence could not be read: %v\n", err)
		return exitFailure
	}
	report := pack.Evaluate(evidence)

	var rendered []byte
	switch strings.ToLower(*format) {
	case "markdown", "md":
		rendered = []byte(report.Markdown())
	case "json":
		rendered, err = report.JSON()
		if err != nil {
			fmt.Fprintf(opts.Stderr, "af: the report could not be rendered: %v\n", err)
			return exitFailure
		}
	default:
		fmt.Fprintf(opts.Stderr, "af: --output must be markdown or json, not %q\n", *format)
		return exitConfig
	}

	if *out == "" {
		if _, err := opts.Stdout.Write(rendered); err != nil {
			return exitFailure
		}
	} else {
		// 0600, because this document names organizations, actors and the
		// artifacts they touched. It holds no secrets and it is not something
		// to leave world readable on a shared runner either.
		if err := os.WriteFile(*out, rendered, 0o600); err != nil {
			fmt.Fprintf(opts.Stderr, "af: the report could not be written: %v\n", err)
			return exitFailure
		}
		fmt.Fprintf(opts.Stderr, "af: wrote %s\n", *out)
	}

	if report.Failed() {
		// The exit code a nightly job watches. A broken audit chain should stop
		// a pipeline without anybody having to parse the document.
		return exitControlNo
	}
	return exitOK
}

const usage = `Usage: af compliance <pack> --org <organization>

Produces evidence for one framework from what this installation recorded.

It is not an audit report and it is not an opinion. Every control says what the
evidence shows, names the artifact so somebody can go and look, and says which
part of the requirement this product covers, which is never all of it. Controls
this product records nothing about are listed as such rather than omitted.

Packs: soc2, hipaa

Exit codes: 0 a report was produced; 6 a control has evidence of NOT holding,
which is what a nightly job should watch for; 3 a configuration problem.

Options:
`
