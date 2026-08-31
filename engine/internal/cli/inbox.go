package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
)

// The inbox is what makes an agent able to finish a sign up.
//
// A welcome email, a magic link, a one time code: the workflow is waiting on
// one, and in a preview environment nobody should receive it. Capture records
// the message and answers the provider's success shape, and these commands are
// how a person or an agent reads what arrived.

// MessageJSON is one captured message.
type MessageJSON struct {
	At       string   `json:"at"`
	Seq      uint64   `json:"seq"`
	Provider string   `json:"provider"`
	Kind     string   `json:"kind"`
	From     string   `json:"from,omitempty"`
	To       []string `json:"to,omitempty"`
	Subject  string   `json:"subject,omitempty"`
	Text     string   `json:"text,omitempty"`
	HTML     string   `json:"html,omitempty"`
	Links    []string `json:"links,omitempty"`
	Link     string   `json:"link,omitempty"`
	Code     string   `json:"code,omitempty"`
}

func messageJSON(m local.Message) MessageJSON {
	return MessageJSON{
		At: m.AtRaw, Seq: m.Seq, Provider: m.Provider, Kind: m.Kind,
		From: m.From, To: m.To, Subject: m.Subject, Text: m.Text, HTML: m.HTML,
		Links: m.Links, Link: m.Link(), Code: m.Code,
	}
}

func newInboxCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Read the mail and messages the environment sent",
		Long: strings.TrimSpace(`
Every message a captured provider was asked to send is recorded here instead of
being delivered. Nobody receives anything, and the workflow that was waiting on
it can carry on.

The link and code are extracted for you, because an agent following a magic
link should not have to parse HTML to find it.`),
	}
	cmd.AddCommand(newInboxListCommand(env))
	cmd.AddCommand(newInboxGetCommand(env))
	cmd.AddCommand(newInboxWaitCommand(env))
	return cmd
}

func newInboxListCommand(env *Env) *cobra.Command {
	var limit int
	var branch, to string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List what the environment sent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			msgs, err := readMessages(cmd, env, branch, limit)
			if err != nil {
				return err
			}
			msgs = filterTo(msgs, to)

			if env.Out.Format == FormatJSON {
				docs := make([]MessageJSON, 0, len(msgs))
				for _, m := range msgs {
					// The bodies are dropped from a listing. A list of twenty
					// emails with their HTML is not a list anybody can read,
					// and 'af inbox get' returns the whole thing.
					doc := messageJSON(m)
					doc.Text, doc.HTML = "", ""
					docs = append(docs, doc)
				}
				return env.Out.JSON(docs)
			}
			if len(msgs) == 0 {
				env.Out.Empty(
					"Nothing has been sent yet. Mail and messages appear here as the "+
						"environment's captured providers are asked to send them.",
					"Drive a flow that sends one with", "af test")
				return nil
			}
			rows := make([][]string, 0, len(msgs))
			for _, m := range msgs {
				rows = append(rows, []string{
					fmt.Sprint(m.Seq), shortTime(m.AtRaw), m.Provider,
					m.Recipient(), truncate(m.Subject, 40), summarize(m),
				})
			}
			env.Out.Table([]Column{
				Num("#"), Col("TIME"), Col("VIA"), Col("TO"), Flex("SUBJECT"), Flex("CONTAINS"),
			}, rows)
			env.Out.Println("")
			env.Out.Hint("Read one in full with",
				fmt.Sprintf("af inbox get %d", msgs[len(msgs)-1].Seq))
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "How many messages to show")
	cmd.Flags().StringVar(&to, "to", "", "Only messages addressed to this recipient")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to read, defaulting to the checked out one")
	return cmd
}

func newInboxGetCommand(env *Env) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "get <number>",
		Short: "Show one message in full",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			msgs, err := readMessages(cmd, env, branch, 500)
			if err != nil {
				return err
			}
			var found *local.Message
			for i := range msgs {
				if fmt.Sprint(msgs[i].Seq) == args[0] {
					found = &msgs[i]
					break
				}
			}
			if found == nil {
				return aferrors.Coded(aferrors.AFNET011,
					"match", "message "+args[0], "timeout", "0s")
			}
			if env.Out.Format == FormatJSON {
				return env.Out.JSON(messageJSON(*found))
			}

			env.Out.Section(orPlaceholder(found.Subject, "(no subject)"))
			env.Out.Printf("  From     %s\n", found.From)
			env.Out.Printf("  To       %s\n", strings.Join(found.To, ", "))
			env.Out.Printf("  Sent     %s via %s\n", shortTime(found.AtRaw), found.Provider)
			if found.Code != "" {
				env.Out.Printf("  Code     %s\n", env.Out.S(StyleBold, found.Code))
			}
			for i, link := range found.Links {
				label := "Links"
				if i > 0 {
					label = ""
				}
				env.Out.Printf("  %-8s %s\n", label, link)
			}
			env.Out.Println("")
			body := found.Text
			if body == "" {
				body = htmlToText(found.HTML)
			}
			for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
				env.Out.Printf("  %s\n", line)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to read, defaulting to the checked out one")
	return cmd
}

func newInboxWaitCommand(env *Env) *cobra.Command {
	var branch, to, subject string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Block until a matching message arrives",
		Long: strings.TrimSpace(`
Waits for a message, checking what already arrived first.

That order matters. The message has usually been sent before anybody starts
waiting for it, and a wait that only looks forward is how a test passes on a
slow machine and fails on a fast one.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(env, branch, false)
			if err != nil {
				return err
			}
			msg, err := o.WaitForMessage(cmd.Context(), to, subject, timeout)
			if err != nil {
				return err
			}
			if env.Out.Format == FormatJSON {
				return env.Out.JSON(messageJSON(msg))
			}
			env.Out.Status(env.Out.S(StyleGood, SymbolOK),
				orPlaceholder(msg.Subject, "(no subject)"), "to "+msg.Recipient())
			if msg.Link() != "" {
				env.Out.Printf("  Link  %s\n", msg.Link())
			}
			if msg.Code != "" {
				env.Out.Printf("  Code  %s\n", msg.Code)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "Wait for a message addressed to this recipient")
	cmd.Flags().StringVar(&subject, "subject", "", "Wait for a subject containing this text")
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "How long to wait")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to read, defaulting to the checked out one")
	return cmd
}

func readMessages(cmd *cobra.Command, env *Env, branch string, limit int) ([]local.Message, error) {
	o, err := orchestrator(env, branch, false)
	if err != nil {
		return nil, err
	}
	return o.Messages(cmd.Context(), limit)
}

func filterTo(msgs []local.Message, to string) []local.Message {
	if to == "" {
		return msgs
	}
	kept := msgs[:0]
	for _, m := range msgs {
		for _, r := range m.To {
			if strings.EqualFold(r, to) {
				kept = append(kept, m)
				break
			}
		}
	}
	return kept
}

// summarize says what is in a message that somebody might want.
func summarize(m local.Message) string {
	var parts []string
	if m.Code != "" {
		parts = append(parts, "code "+m.Code)
	}
	if n := len(m.Links); n == 1 {
		parts = append(parts, "1 link")
	} else if n > 1 {
		parts = append(parts, fmt.Sprintf("%d links", n))
	}
	return strings.Join(parts, ", ")
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func orPlaceholder(s, placeholder string) string {
	if strings.TrimSpace(s) == "" {
		return placeholder
	}
	return s
}

// htmlToText makes an HTML body readable in a terminal.
//
// Not a parser, and not trying to be. It strips tags and collapses whitespace,
// which is enough to read a transactional email and find the sentence with the
// code in it.
func htmlToText(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>':
			if depth > 0 {
				depth--
				b.WriteByte('\n')
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	var lines []string
	for _, line := range strings.Split(b.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, unescapeEntities(line))
		}
	}
	return strings.Join(lines, "\n")
}

func unescapeEntities(s string) string {
	return strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`,
		"&#39;", "'", "&nbsp;", " ",
	).Replace(s)
}
