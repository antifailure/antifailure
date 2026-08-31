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

	"github.com/antifailure/antifailure/engine/internal/auth"
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
	// Credentials is where the personal token lives. Nil means this
	// platform's own store; read through CredentialStore, never directly.
	Credentials *auth.Store
}

// CredentialStore is where af login put the token.
//
// One accessor rather than auth.NewStore() at each call site, so that a test
// can substitute an in-memory keyring and a temporary directory in one place
// and no command can reach past it to the real one by accident.
func (e *Env) CredentialStore() *auth.Store {
	if e.Credentials != nil {
		return e.Credentials
	}
	return auth.NewStore()
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
	// Extra are commands contributed by a binary that embeds this one.
	//
	// It exists so that the enterprise binary's own commands appear in af
	// --help rather than being intercepted before the command tree is built. A
	// command a user cannot discover is a command that does not exist for most
	// of the people it was written for.
	//
	// Deliberately a plain struct rather than a cobra command: cobra is an
	// implementation detail of this package and putting it in a signature an
	// external module fills in would make it a public dependency for ever. A
	// contributed command gets a name, help text, and a function; anything more
	// belongs in this tree.
	//
	// A contributed name that collides with a built-in one is ignored, so that
	// an embedding binary cannot replace af down with something of its own.
	Extra []ExtraCommand

	// Credentials is where af login, af logout, af whoami and af provider read
	// and write the personal token. Nil means the platform's own store.
	//
	// Injected for the same reason Getenv and Clock are: without it, a test of
	// any command that reads a credential writes to the developer's keychain,
	// prompts them for it on macOS, and leaves a token behind on a shared CI
	// machine. So those commands had no end-to-end test at all, which is
	// exactly the gap that lets a broken command ship looking fine.
	Credentials *auth.Store
}

// ExtraCommand is a command contributed by an embedding binary.
type ExtraCommand struct {
	// Use is the command line, as "compliance <pack>".
	Use string
	// Short is the one line in af --help.
	Short string
	// Long is the help text for this command.
	Long string
	// Run receives the arguments after the command's own name and returns an
	// exit code, the same contract Execute has.
	Run func(ctx context.Context, args []string) int
}

// Name is the first word of Use, which is what the command is called.
func (e ExtraCommand) Name() string {
	if i := strings.IndexByte(e.Use, ' '); i > 0 {
		return e.Use[:i]
	}
	return e.Use
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
	// Decided here, once, for the same reason colour is: every command asks
	// the Output how wide a line may be, and a command that measured the
	// terminal for itself would be a command that disagrees with its sibling
	// about where the right margin is.
	out.Width = DetectWidth(opts.Stdout, opts.Getenv)

	env := &Env{
		Out:         out,
		Clock:       opts.Clock,
		Redactor:    redact.New(),
		Getenv:      opts.Getenv,
		Stdin:       opts.Stdin,
		WorkDir:     opts.WorkDir,
		Credentials: opts.Credentials,
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
	addExtraCommands(root, opts.Extra)
	// Never nil. Cobra treats a nil slice as "take the process's arguments",
	// which for a function that was handed an argument list is a surprising
	// thing to do and, inside a test binary, means the test runner's own flags
	// reach the command tree. That produced a failure that appeared only when
	// somebody added a flag to the test binary, which is a long way from the
	// cause.
	if args == nil {
		args = []string{}
	}
	root.SetArgs(args)
	root.SetOut(opts.Stdout)
	root.SetErr(opts.Stderr)

	// ExecuteContextC rather than ExecuteContext, for the command it hands
	// back. A usage error is the one failure where the useful thing to print
	// is not the error at all, it is what the command actually takes, and
	// without the command there is nothing to print it from.
	ran, err := root.ExecuteContextC(ctx)
	if err == nil {
		// A command that could not write what it was asked to write did not
		// succeed. Reporting zero here tells a script that the output it just
		// failed to receive is complete, which is the one thing it must not
		// be told. The error goes to stderr because stdout is the stream that
		// is broken.
		if writeErr := out.WriteErr(); writeErr != nil {
			_, _ = fmt.Fprintf(opts.Stderr, "af: could not write output: %v\n", writeErr)
			return int(aferrors.ExitFailure)
		}
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
		renderUsageError(out, ran, err)
		return int(aferrors.ExitUsage)
	}
	if out.Format == FormatJSON {
		_ = out.JSON(ErrorDocument(err))
	} else {
		out.Error(err)
	}
	return int(aferrors.ExitCodeOf(err))
}

// renderUsageError says what the command takes, not just that it was typed
// wrong.
//
// Cobra's own message for the most common mistake in this tree is "accepts 2
// arg(s), received 1", printed alone, with no usage line and no example. It
// names the count and nothing else: not which two arguments, not what they
// look like, not where to find out. Every command that takes an argument
// failed that way, which is twenty four of them.
//
// So the shape is the same one the error renderer uses, because it is the same
// question being answered: what happened, what to do, where to read more.
//
// Writes are not checked, for the same reason as Output.Error: this is the
// path that reports a failure, and a broken error stream leaves nowhere to
// report that to. The exit code is what the caller reads.
func renderUsageError(o *Output, cmd *cobra.Command, err error) {
	name := "af"
	if cmd != nil {
		name = cmd.CommandPath()
	}
	_, _ = fmt.Fprintf(o.Err, "%s %s\n",
		o.S(StyleBad, "Usage:"), o.Wrap(plainUsage(name, err.Error()), len("Usage: ")))
	if cmd == nil {
		return
	}
	// A command that was never named has no usage worth printing: "Takes: af
	// [flags]" answers a question nobody asked. The list of commands is what
	// somebody who typed a name that does not exist actually wants.
	if cmd.Parent() == nil {
		_, _ = fmt.Fprintf(o.Err, "  %s  %s\n",
			o.S(StyleDim, "More:"), o.S(StyleDim, "af --help lists every command"))
		return
	}
	if line := strings.TrimSpace(cmd.UseLine()); line != "" {
		_, _ = fmt.Fprintf(o.Err, "  %s %s\n", o.S(StyleBold, "Takes:"), line)
	}
	// The first example only. A usage error is read in a hurry and the point
	// is one line somebody can copy, not the catalogue that af <command>
	// --help already holds.
	if ex := firstExample(cmd.Example); ex != "" {
		_, _ = fmt.Fprintf(o.Err, "  %s   %s\n", o.S(StyleBold, "Try:"), ex)
	}
	_, _ = fmt.Fprintf(o.Err, "  %s  %s\n",
		o.S(StyleDim, "More:"), o.S(StyleDim, cmd.CommandPath()+" --help"))
}

// plainUsage rewrites cobra's argument count messages as English.
//
// "accepts 2 arg(s), received 1" names a count and nothing else: not the
// command, not which two arguments, not what they look like. It is also not a
// sentence. Every other message a user sees from this tool is one, and the
// arithmetic notation in the middle of it is the tell that this particular
// message was never written by anybody, it just leaked out of a library.
//
// Anything not recognised passes through unchanged rather than being mangled
// into something that reads well and says the wrong thing.
func plainUsage(name, msg string) string {
	var want, got int
	switch {
	case matchCounts(msg, "accepts %d arg(s), received %d", &want, &got):
		return fmt.Sprintf("%s takes %s and got %d.", name, plural(want, "argument", "arguments"), got)
	case matchCounts(msg, "accepts at most %d arg(s), received %d", &want, &got):
		return fmt.Sprintf("%s takes at most %s and got %d.", name, plural(want, "argument", "arguments"), got)
	case matchCounts(msg, "requires at least %d arg(s), only received %d", &want, &got):
		return fmt.Sprintf("%s needs at least %s and got %d.", name, plural(want, "argument", "arguments"), got)
	case strings.HasPrefix(msg, "unknown command"):
		return msg + ". Run 'af --help' for the ones that exist."
	}
	return msg
}

// matchCounts reads two numbers out of a message with a known shape.
func matchCounts(msg, format string, a, b *int) bool {
	n, err := fmt.Sscanf(msg, format, a, b)
	return err == nil && n == 2
}

// firstExample is the first runnable line of a command's examples.
func firstExample(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			return line
		}
	}
	return ""
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
		newExploreCommand(env),
		newLoadCommand(env),
		newGoldenCommand(env),
		newMaskCommand(env),
		newNetCommand(env),
		newWebhookCommand(env),
		newInboxCommand(env),
		newInsightsCommand(env),
		newOracleCommand(env),
		newInvariantsCommand(env),
		newEnvCommand(env),
		newExplainCommand(env),
		newDoctorCommand(env),
		newSupportCommand(env),
		newCICommand(env),
		newRunnerCommand(env),
		newSecretCommand(env),
		newProviderCommand(env),
		newLoginCommand(env),
		newLogoutCommand(env),
		newWhoamiCommand(env),
		newLicenseCommand(env),
		newVersionCommand(env),
	)
	attachExamples(root)
	root.SetHelpTemplate(helpTemplate)
	root.SetUsageTemplate(usageTemplate)
	// A flag that does not parse is a usage error, not a failure of the
	// command, and the exit code has to say so.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &usageError{err: err}
	})
	return root
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

// RootForDocs builds the command tree for the reference generator.
//
// It exists because the generator has to read the same tree the binary serves,
// and the tree is built inside Execute alongside a great deal of runtime setup
// it has no business doing: no working directory, no clock, no output.
//
// Nothing here runs a command. The generator reads names, descriptions, and
// flags, so the environment it is given only has to be non-nil, and giving it
// a real one would mean generating documentation could touch a Docker daemon.
func RootForDocs() *cobra.Command {
	return newRootCommand(&Env{
		Out:      NewOutput(io.Discard, io.Discard),
		Clock:    clock.New(),
		Redactor: redact.New(),
		Getenv:   func(string) string { return "" },
		Stdin:    strings.NewReader(""),
		WorkDir:  ".",
	})
}

// addExtraCommands attaches an embedding binary's commands.
//
// A name that already exists is refused rather than replaced. An embedding
// binary that could shadow af down with its own implementation would be a
// binary where the documented behaviour of a command depends on which build
// somebody is holding, and there is no version of that which ends well.
func addExtraCommands(root *cobra.Command, extra []ExtraCommand) {
	existing := map[string]bool{}
	for _, cmd := range root.Commands() {
		existing[cmd.Name()] = true
	}
	for _, e := range extra {
		if e.Run == nil || e.Use == "" || existing[e.Name()] {
			continue
		}
		contributed := e
		root.AddCommand(&cobra.Command{
			Use:                contributed.Use,
			Short:              contributed.Short,
			Long:               contributed.Long,
			DisableFlagParsing: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				if code := contributed.Run(cmd.Context(), args); code != 0 {
					// Carried as a silent error rather than returned as a code,
					// because Execute turns errors into codes and a second path
					// out of a command is a second place for the exit code to
					// be wrong. Silent because the command has already written
					// whatever it had to say, and a second message would either
					// duplicate it or, in JSON mode, put a second document into
					// a stream something is parsing.
					return &silentError{code: aferrors.ExitCode(code)}
				}
				return nil
			},
		})
	}
}
