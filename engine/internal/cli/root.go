package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/redact"
)

// Version information, set by the linker at release time.
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
	Edition   = "community"
)

// Env carries everything a command needs that is not a flag.
//
// It exists so that no command reaches for a package level variable, a global
// logger, or os.Stdout directly. Every command is therefore runnable in a test
// with substituted streams, a fake clock, and a scratch directory, which is
// what makes the snapshot tests possible at all.
type Env struct {
	Out      *Output
	Clock    clock.Clock
	Redactor *redact.Redactor
	Bus      *events.Bus
	// WorkDir is where the command runs. It is resolved once so that no
	// command calls os.Getwd and gets a different answer than its sibling.
	WorkDir string
	// Getenv reads the process environment. Injecting it keeps environment
	// handling testable and makes every read auditable.
	Getenv func(string) string
	// Stdin is the input stream, for interactive prompts.
	Stdin io.Reader
}

// Interactive reports whether there is a terminal to ask a question on.
//
// A command that needs an answer and has no terminal must refuse rather than
// read, because a read from a closed or piped stdin either returns nothing
// forever or returns end of file immediately, and both look like a hang or a
// wrong answer to whoever is watching a CI log.
func (e *Env) Interactive() bool {
	f, ok := e.Stdin.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// Options configure the root command.
type Options struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	Getenv func(string) string
	Clock  clock.Clock
	// WorkDir overrides the working directory. Tests set it; the binary does
	// not, and reads the process directory instead.
	WorkDir string
}

// Execute runs the command line and returns the process exit code.
//
// It never calls os.Exit itself. Returning the code means main is the only
// place the process ends, which keeps every command testable and keeps
// deferred cleanup running.
func Execute(ctx context.Context, args []string, opts Options) int {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.Clock == nil {
		opts.Clock = clock.New()
	}

	out := NewOutput(opts.Stdout, opts.Stderr)
	out.Color = DetectColor(opts.Stdout, opts.Getenv)

	env := &Env{
		Out:      out,
		Clock:    opts.Clock,
		Redactor: redact.New(),
		Getenv:   opts.Getenv,
		Stdin:    opts.Stdin,
		WorkDir:  opts.WorkDir,
	}
	if env.WorkDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			out.Error(fmt.Errorf("cli: resolve the working directory: %w", err))
			return int(aferrors.ExitFailure)
		}
		env.WorkDir = wd
	}

	root := newRootCommand(env)
	root.SetArgs(args)
	root.SetOut(opts.Stdout)
	root.SetErr(opts.Stderr)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return int(aferrors.ExitSuccess)
	}

	// A command that already rendered its own report, such as doctor, exits
	// non zero without a second message. Printing one would either duplicate
	// the report or, in JSON mode, emit a second document into a stream a
	// script is parsing.
	var quiet *silentError
	if aferrors.As(err, &quiet) {
		return int(quiet.ExitCode())
	}

	// A usage error is cobra's, and it has already printed the usage. Anything
	// else is ours and gets the code, cause, and next step rendering.
	var usage *usageError
	if aferrors.As(err, &usage) || isUsageMessage(err) {
		fmt.Fprintln(opts.Stderr, err.Error())
		return int(aferrors.ExitUsage)
	}
	if out.Format == FormatJSON {
		_ = out.JSON(ErrorDocument(err))
	} else {
		out.Error(err)
	}
	return int(aferrors.ExitCodeOf(err))
}

// isUsageMessage recognises the errors cobra produces for a command line that
// does not parse.
//
// Matching on the message is unpleasant, and the alternative is worse: cobra
// returns plain errors for these, so without the check they exit 1, which is
// the code for "the command ran and failed" rather than "you typed it wrong".
// Scripts branch on that difference.
func isUsageMessage(err error) bool {
	msg := err.Error()
	for _, prefix := range []string{
		"unknown command",
		"unknown flag",
		"unknown shorthand flag",
		"flag needs an argument",
		"invalid argument",
		"accepts ",
		"requires at least",
		"unknown subcommand",
	} {
		if strings.HasPrefix(msg, prefix) {
			return true
		}
	}
	return false
}

// usageError marks an error that cobra has already reported as usage.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func newRootCommand(env *Env) *cobra.Command {
	var (
		formatFlag  string
		quiet       bool
		verbose     bool
		workDirFlag string
		noColor     bool
	)

	root := &cobra.Command{
		Use:   "af",
		Short: "A disposable copy of your production stack for every pull request",
		Long: strings.TrimSpace(`
Antifailure builds an environment from the shape of production: the real schema
and real data volume with every identifier masked and the masking proved, your
services running in a sandbox that cannot reach the internet except where you
say it can, and inbound webhooks simulated so flows actually finish.

  af init     read the repository and write antifailure.yaml
  af up       create an environment
  af test     run the agent workflows against it
  af down     remove everything it created

Nothing it creates outlives the environment. Every resource is journaled before
it is made and compensated on teardown, so a crash at any instant is
recoverable by replay.`),
		SilenceUsage:  true,
		SilenceErrors: true,
		// A command with no subcommand prints help rather than an error,
		// because "af" alone is how people find out what it does.
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return &usageError{err: fmt.Errorf("unknown command %q", args[0])}
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			switch Format(formatFlag) {
			case FormatText, FormatJSON:
				env.Out.Format = Format(formatFlag)
			default:
				return &usageError{err: fmt.Errorf(
					"the output format %q is not recognised; use text or json", formatFlag)}
			}
			env.Out.Quiet = quiet
			env.Out.Verbose = verbose
			if noColor || env.Out.Format == FormatJSON {
				env.Out.Color = false
			}
			if workDirFlag != "" {
				abs, err := filepath.Abs(workDirFlag)
				if err != nil {
					return fmt.Errorf("cli: resolve %s: %w", workDirFlag, err)
				}
				if st, err := os.Stat(abs); err != nil || !st.IsDir() {
					return &usageError{err: fmt.Errorf("%s is not a directory", workDirFlag)}
				}
				env.WorkDir = abs
			}
			return nil
		},
	}

	root.PersistentFlags().StringVarP(&formatFlag, "output", "o", "text",
		"Output format: text or json")
	root.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false,
		"Print only what was asked for")
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false,
		"Print the underlying cause of an error")
	root.PersistentFlags().StringVarP(&workDirFlag, "directory", "C", "",
		"Run as if started in this directory")
	root.PersistentFlags().BoolVar(&noColor, "no-color", false,
		"Do not emit colour, regardless of the terminal")

	root.AddCommand(
		newInitCommand(env),
		newUpCommand(env),
		newDownCommand(env),
		newStatusCommand(env),
		newLogsCommand(env),
		newTestCommand(env),
		newLoadCommand(env),
		newGoldenCommand(env),
		newMaskCommand(env),
		newNetCommand(env),
		newWebhookCommand(env),
		newInboxCommand(env),
		newInsightsCommand(env),
		newEnvCommand(env),
		newExplainCommand(env),
		newDoctorCommand(env),
		newSupportCommand(env),
		newVersionCommand(env),
	)
	root.SetHelpTemplate(helpTemplate)
	root.SetUsageTemplate(usageTemplate)
	// A flag that does not parse is a usage error, not a failure of the
	// command, and the exit code has to say so.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &usageError{err: err}
	})
	return root
}

// notYetAvailable is what a command returns when its engine has not landed.
//
// It exits with a usage code and says so plainly. The alternative, a command
// that appears to work and does nothing, is the exact failure this product
// exists to prevent, and shipping one in our own binary would be indefensible.
func notYetAvailable(name string) error {
	return aferrors.Coded(aferrors.AFRUN001, "command", name)
}

// WithSignals returns a context cancelled by an interrupt, and a function that
// reports whether a second signal arrived.
//
// The contract at the command boundary: the first signal cancels the root
// context so that in flight work rolls back and teardown runs. The second
// forces exit with code 10 and the journal intact, because a user pressing
// control C twice means "stop now", and the journal is what makes stopping now
// safe.
func WithSignals(ctx context.Context) (context.Context, func() bool, func()) {
	ctx, cancel := context.WithCancel(ctx)
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)

	forced := make(chan struct{})
	go func() {
		<-ch
		cancel()
		<-ch
		close(forced)
	}()

	second := func() bool {
		select {
		case <-forced:
			return true
		default:
			return false
		}
	}
	stop := func() {
		signal.Stop(ch)
		cancel()
	}
	return ctx, second, stop
}

const helpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}`

const usageTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

Commands:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Run "{{.CommandPath}} [command] --help" for more about a command.{{end}}
`
