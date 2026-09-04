package cli_test

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/cli"
)

// The command reference is generated from the command tree.
//
// A page somebody maintains by hand describes what the software did on the day
// they last remembered to update it. This one is built from the same tree the
// binary serves, so a flag added, renamed, or removed changes the page in the
// same commit, and this test fails if the checked-in page and the tree
// disagree.
//
// It lives as a test rather than a command under tools/ because the tree is in
// an internal package, which a separate module cannot import, and because the
// drift check and the generator want to be the same code: two of them would
// eventually generate different pages.

var updateReference = flag.Bool("update-reference", false,
	"rewrite the generated command reference")

func referencePath() string {
	return filepath.Join("..", "..", "..", "docs", "src", "content", "docs", "reference", "cli.md")
}

func TestCommandReferenceMatchesTheCommandTree(t *testing.T) {
	page := renderReference(cli.RootForDocs())

	path := referencePath()
	if *updateReference {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, page, 0o644))
		t.Logf("wrote %s", path)
		return
	}

	existing, err := os.ReadFile(path)
	require.NoErrorf(t, err,
		"%v\n\nRegenerate with: go test ./internal/cli -update-reference", err)
	require.True(t, bytes.Equal(bytes.TrimSpace(existing), bytes.TrimSpace(page)),
		"the command reference is out of date with the command tree.\n"+
			"Regenerate with: go test ./internal/cli -update-reference")
}

// Every command has to say what it does. A reference entry with no description
// is worse than no entry: somebody finds it, learns nothing, and now believes
// the documentation is unreliable.
func TestEveryCommandDescribesItself(t *testing.T) {
	var commands []*cobra.Command
	collectCommands(cli.RootForDocs(), &commands)
	require.NotEmpty(t, commands)

	for _, c := range commands {
		require.NotEmptyf(t, strings.TrimSpace(c.Short),
			"%s has no one-line description", c.CommandPath())
		require.Lessf(t, len(c.Short), 80,
			"%s has a one-line description that is not one line", c.CommandPath())
		require.Equalf(t, strings.ToUpper(c.Short[:1]), c.Short[:1],
			"%s starts its description in lower case", c.CommandPath())
	}
}

// A flag with no help is a flag nobody can use without reading the source.
func TestEveryFlagExplainsItself(t *testing.T) {
	root := cli.RootForDocs()
	var commands []*cobra.Command
	collectCommands(root, &commands)
	commands = append(commands, root)

	for _, c := range commands {
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if f.Hidden {
				return
			}
			require.NotEmptyf(t, f.Usage, "%s has --%s with no description", c.CommandPath(), f.Name)
		})
	}
}

func renderReference(root *cobra.Command) []byte {
	var b bytes.Buffer
	b.WriteString(`---
title: Command reference
description: Every command and every flag, generated from the command tree itself.
sidebar:
  order: 1
---

Generated from the command tree, so it cannot fall behind the binary: a flag
added, renamed, or removed changes this page in the same commit, and the build
fails if it does not.

`)

	b.WriteString("## Global flags\n\nThese work on every command.\n\n")
	writeFlagTable(&b, root.PersistentFlags())

	// The root's own flags, which do not persist to subcommands and so are
	// absent from the table above. This page's description promises every
	// flag, and af --version is the one somebody types the moment an installer
	// finishes, so it is the last flag that should be missing from it.
	if local := root.LocalNonPersistentFlags(); local.HasAvailableFlags() {
		b.WriteString("## Flags on `af` itself\n\n" +
			"These work on `af` on its own rather than on a command under it.\n\n")
		writeFlagTable(&b, local)
	}

	// Written here rather than in a hand maintained page, because these three
	// decide the shape of every command's output and a reader looking for them
	// looks at the command reference. They are read once, at the command
	// boundary, so they behave the same on every command in the table below.
	b.WriteString(`## How output adapts

Text output is stable for the same input. There are no timestamps and no
durations in it, so a snapshot test, a diff and two CI logs of the same run
compare cleanly. Timestamps live in ` + "`--output json`" + `, where a machine
wants them.

What does vary is layout, and only where there is a terminal to lay anything
out on. Colour, width and the live status line under a long run are decided
once, from the output stream, when the command starts.

| Variable | What it does |
| --- | --- |
| ` + "`NO_COLOR`" + ` | Any non-empty value turns colour off. It wins over everything, including ` + "`AF_FORCE_COLOR`" + `. |
| ` + "`AF_FORCE_COLOR`" + ` | Any non-empty value turns colour on for a stream that is not a terminal, for a CI system that renders escape codes. |
| ` + "`AF_WIDTH`" + ` | Lay output out at this many columns rather than measuring the terminal. Clamped to between 40 and 200. |

A stream that is not a terminal, a pipe, a file, or a CI log, is laid out at 80
columns and carries no escape sequences. That is what keeps the output of a
piped run identical from one machine to the next. ` + "`TERM=dumb`" + ` is
treated the same way.

`)

	var commands []*cobra.Command
	collectCommands(root, &commands)
	sort.Slice(commands, func(i, j int) bool {
		return commands[i].CommandPath() < commands[j].CommandPath()
	})

	b.WriteString("## Commands\n\n")
	for _, c := range commands {
		fmt.Fprintf(&b, "### `%s`\n\n", c.CommandPath())
		fmt.Fprintf(&b, "%s\n\n", sentence(c.Short))
		if long := strings.TrimSpace(c.Long); long != "" && long != strings.TrimSpace(c.Short) {
			fmt.Fprintf(&b, "%s\n\n", long)
		}
		fmt.Fprintf(&b, "```\n%s\n```\n\n", strings.TrimSpace(c.UseLine()))

		// The example goes on the page for the same reason it goes in the
		// help text: a flag table says what the switches are called and never
		// says what a real invocation looks like, and a reader trusts an
		// example more than prose because it looks like something they can
		// paste.
		if ex := strings.TrimSpace(c.Example); ex != "" {
			var lines []string
			for _, l := range strings.Split(ex, "\n") {
				lines = append(lines, strings.TrimPrefix(l, "  "))
			}
			fmt.Fprintf(&b, "```\n%s\n```\n\n", strings.Join(lines, "\n"))
		}

		if subs := visibleSubcommands(c); len(subs) > 0 {
			b.WriteString("Subcommands:\n\n")
			for _, sub := range subs {
				fmt.Fprintf(&b, "- [`%s`](#%s) %s\n",
					sub.CommandPath(), anchorOf(sub.CommandPath()), sentence(sub.Short))
			}
			b.WriteString("\n")
		}

		writeFlagTable(&b, c.LocalFlags())
	}
	return b.Bytes()
}

func writeFlagTable(b *bytes.Buffer, set *pflag.FlagSet) {
	var flags []*pflag.Flag
	set.VisitAll(func(f *pflag.Flag) {
		if !f.Hidden {
			flags = append(flags, f)
		}
	})
	if len(flags) == 0 {
		return
	}
	sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })

	b.WriteString("| Flag | Default | What it does |\n| --- | --- | --- |\n")
	for _, f := range flags {
		name := "`--" + f.Name + "`"
		if f.Shorthand != "" {
			name = "`-" + f.Shorthand + "`, " + name
		}
		def := "-"
		if f.DefValue != "" && f.DefValue != "[]" {
			def = "`" + f.DefValue + "`"
		}
		fmt.Fprintf(b, "| %s | %s | %s |\n", name, def, sentence(f.Usage))
	}
	b.WriteString("\n")
}

func visibleSubcommands(c *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, sub := range c.Commands() {
		if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
			continue
		}
		out = append(out, sub)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// collectCommands walks the tree, leaving out the generated help and completion
// commands. A reference listing commands nobody asked for buries the ones
// somebody is looking for.
func collectCommands(c *cobra.Command, out *[]*cobra.Command) {
	for _, sub := range visibleSubcommands(c) {
		*out = append(*out, sub)
		collectCommands(sub, out)
	}
}

func anchorOf(path string) string {
	return strings.ReplaceAll(strings.ToLower(path), " ", "-")
}

// sentence makes a fragment read as prose, since cobra's convention is a
// lowercase phrase and a reference page is read as sentences.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "?") {
		s += "."
	}
	return s
}

// Every command shows a worked example, and every example is a command that
// exists with flags that exist.
//
// Held in both directions, the way the error catalogue is: a command with no
// entry in the table fails here, and an entry naming a command that is not in
// the tree fails here too, so the table can neither rot behind the tree nor
// drift ahead of it.
//
// The reason it is worth a test at all: a flag list tells a reader what the
// switches are called and never tells them what a real invocation looks like,
// which is what somebody actually wants at the moment they type --help. Three
// commands out of sixty five had an example before this, because three people
// thought of it on three different days.
func TestEveryCommandHasAWorkedExample(t *testing.T) {
	root := cli.RootForDocs()
	var commands []*cobra.Command
	collectCommands(root, &commands)
	require.NotEmpty(t, commands)

	inTree := map[string]bool{}
	for _, c := range commands {
		inTree[c.CommandPath()] = true
		require.NotEmptyf(t, strings.TrimSpace(c.Example),
			"%s shows no example. Add one to commandExamples in examples.go", c.CommandPath())

		for _, line := range strings.Split(c.Example, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			require.Truef(t, strings.HasPrefix(line+" ", c.CommandPath()+" ") || line == c.CommandPath(),
				"%s has an example that is not an invocation of it: %q", c.CommandPath(), line)
			// A flag in an example that the command does not have sends
			// somebody out of the help text straight into a usage error, which
			// is worse than showing them no example at all.
			for _, flag := range longFlag.FindAllString(line, -1) {
				name := strings.TrimPrefix(flag, "--")
				require.NotNilf(t, c.Flags().Lookup(name),
					"%s shows --%s and has no such flag", c.CommandPath(), name)
			}
		}
	}

	for _, path := range cli.ExampleCommandPaths() {
		require.Truef(t, inTree[path],
			"commandExamples has an entry for %q, which is not a command", path)
	}
}
