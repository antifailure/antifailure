package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// The encrypted local store, from the outside.
//
// The store has been the last link in the lookup chain from the beginning, and
// every "not found" error has named it as somewhere to put the value. Nothing
// could put anything in it: the store had Set, Delete and Names and no caller.
// A source a user is told to use and cannot write to is worse than one that
// does not exist, because the message sends them looking for a command.
//
// Values are read from a terminal without echo, or from stdin when there is no
// terminal, and never from an argument. An argument is in the shell history and
// in the process list of every other user on the machine.

func (e *Env) secretStore() *secrets.FileStore {
	return secrets.NewFileStore(
		filepath.Join(e.WorkDir, ".antifailure", "secrets.enc"),
		secrets.StorePassphrase(e.Getenv),
	)
}

func newSecretCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Store values in the encrypted local store",
		Long: strings.TrimSpace(`
The last place the engine looks for a declared variable, after this shell's
environment and after .env.

The file is encrypted with a key derived from a passphrase, and it lives under
.antifailure, which 'af init' adds to .gitignore. It is a convenience for a
workstation and it is not a secret manager for a team: a value here is as safe
as the passphrase and the disk it is on.

Set AF_SECRET_PASSPHRASE before using it. There is deliberately no default:
a store encrypted with a passphrase everybody knows is a store that only looks
encrypted.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newSecretSetCommand(env),
		newSecretListCommand(env),
		newSecretRemoveCommand(env),
	)
	return cmd
}

func newSecretSetCommand(e *Env) *cobra.Command {
	var fromStdin bool
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Store a value, read without echo",
		Long: strings.TrimSpace(`
Reads the value from the terminal without echoing it, or from stdin when there
is no terminal.

It is never taken as an argument. An argument is in the shell history, in the
process list, and in the CI log of whatever ran it.`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := checkPassphrase(e); err != nil {
				return err
			}

			value, err := readSecret(e, fromStdin)
			if err != nil {
				return err
			}
			if value == "" {
				return aferrors.Coded(aferrors.AFMAN002,
					"path", name,
					"detail", "the value is empty; use 'af secret rm' to remove one")
			}

			if err := e.secretStore().Set(name, value); err != nil {
				return err
			}
			// The name, never the value, and never a length: a length is a
			// hint about a password.
			e.Out.Printf("Stored %s.\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&fromStdin, "stdin", false,
		"Read the value from stdin rather than prompting")
	return cmd
}

func newSecretListCommand(e *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the names in the store",
		Long: strings.TrimSpace(`
Names only. There is no command that prints a stored value: a store that can
print its contents is one screenshot away from not being a store.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkPassphrase(e); err != nil {
				return err
			}
			names, err := e.secretStore().Names()
			if err != nil {
				return err
			}
			if len(names) == 0 {
				e.Out.Printf("The store is empty.\n")
				return nil
			}
			sort.Strings(names)
			for _, name := range names {
				e.Out.Printf("  %s\n", name)
			}
			return nil
		},
	}
}

func newSecretRemoveCommand(e *Env) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"remove"},
		Short:   "Remove a value from the store",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkPassphrase(e); err != nil {
				return err
			}
			// Removing something that is not there succeeds, for the same
			// reason every teardown does: the caller wanted it gone.
			if err := e.secretStore().Delete(args[0]); err != nil {
				return err
			}
			e.Out.Printf("Removed %s.\n", args[0])
			return nil
		},
	}
}

func checkPassphrase(e *Env) error {
	if secrets.StorePassphrase(e.Getenv) != "" {
		return nil
	}
	return aferrors.Coded(aferrors.AFSEC004)
}

// readSecret reads a value without putting it anywhere it can be read back.
//
// The piped path reads e.Stdin rather than os.Stdin. It read os.Stdin once, and
// that made every command using it untestable and unusable from anything that
// embeds the CLI with its own streams: --stdin would block on the process's
// real standard input while the caller's data sat unread. In the binary the two
// are the same value, so nothing about production behaviour changes.
//
// The interactive path still uses the process's own descriptor, because reading
// a password without echo means turning echo off on a terminal, and only a real
// file descriptor has one.
func readSecret(e *Env, fromStdin bool) (string, error) {
	fd := int(os.Stdin.Fd())
	if fromStdin || !term.IsTerminal(fd) {
		scanner := bufio.NewScanner(e.Stdin)
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return "", fmt.Errorf("reading the value from stdin: %w", err)
			}
			return "", nil
		}
		return strings.TrimRight(scanner.Text(), "\r\n"), nil
	}

	e.Out.Printf("Value (not echoed): ")
	raw, err := term.ReadPassword(fd)
	e.Out.Printf("\n")
	if err != nil {
		return "", fmt.Errorf("reading the value: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}
