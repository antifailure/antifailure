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
