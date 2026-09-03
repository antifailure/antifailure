package cli

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// Every command's worked example, in one table.
//
// The reason they are here rather than beside each command: before this, three
// commands out of sixty five had an example and sixty two had a list of flags.
// A flag list tells a reader what the switches are called. It does not tell
// them what the command is for, and it never tells them the shape of a real
// invocation, which is the thing somebody actually wants at the moment they
// run af something --help. The three that had one had it because whoever wrote
// those three thought of it, which is exactly the drift a shared table
// prevents: sixty five examples in one place can be read for consistency in a
// minute, and sixty five examples spread over twenty six files cannot.
//
// TestEveryCommandHasAWorkedExample holds both directions, the way the error
// catalogue does: a command with no entry fails, and an entry naming a command
// that does not exist fails too, so the table can neither rot nor drift.
//
// A line beginning with # is a comment and is not run. The first line that is
// not a comment is what a usage error offers as the thing to try, so put the
// plainest invocation first.
var commandExamples = map[string]string{
	"af change": "" +
		"# Against the base branch this job names.\n" +
		"af change\n" +
		"# Against a ref you choose, or a diff you already have.\n" +
		"af change --base origin/main\n" +
		"af change --diff pr.patch",
	"af ci": "" +
		"# What CI runs: up, migrate, test, load, gate, report, down.\n" +
		"af ci\n" +
		"# --report is Markdown for a person, --report-json is the same run\n" +
		"# for a program.\n" +
		"af ci --report report.md --report-json report.json --keep",
	"af mcp": "" +
		"# Started by an MCP client, not typed. It speaks the protocol on\n" +
		"# standard input and output, so running it in a terminal looks idle.\n" +
		"af mcp\n" +
		"# It serves exactly the checkout it starts in. A client with nowhere\n" +
		"# to set a working directory passes an absolute path instead.\n" +
		"af -C /absolute/path/to/your/project mcp",
	"af doctor": "af doctor\naf doctor -o json",
	"af start": "" +
		"# Where you are on the first run, and the one command that moves you on.\n" +
		"af start\n" +
		"af start -o json",
	"af down": "af down\naf down --branch feature/checkout",
	"af env":  "af env list",
	"af env list": "" +
		"af env list\n" +
		"af env list -o json",
	"af env prune": "" +
		"# Nothing is removed until you drop --dry-run.\n" +
		"af env prune --dry-run\n" +
		"af env prune --older-than 24h",
	"af env reap": "" +
		"# Only environments past the lifetime they were created with, and\n" +
		"# nothing is removed until you drop --dry-run.\n" +
		"af env reap --dry-run\n" +
		"af env reap",
	"af env extend": "" +
		"# The ceiling is measured from when the environment was created, so\n" +
		"# extending twice does not buy twice the time.\n" +
		"af env extend af-orders-feature-checkout-05ca6c --for 2h\n" +
		"af env extend af-orders-feature-checkout-05ca6c --for 2h --reason 'debugging the failing checkout'",
	"af env pull": "af env pull af-orders-feature-checkout-05ca6c",
	"af explain":  "af explain\naf explain -o json",
	"af explore": "" +
		"# Agents go at a goal with no workflow written for it.\n" +
		"af explore\n" +
		"af explore --emit-workflow checkout.yaml",
	"af fidelity": "" +
		"# An inventory of the copy against the thing it is a copy of.\n" +
		"af fidelity\n" +
		"af fidelity -o json",
	"af golden": "af golden list",
	"af golden gc": "" +
		"af golden gc\n" +
		"af golden gc --keep 3",
	"af golden list": "af golden list",
	"af golden pull": "" +
		"af golden pull\n" +
		"af golden pull gv_20260830044013_74234e98",
	"af golden refresh": "af golden refresh",
	"af golden verify":  "af golden verify gv_20260830044013_74234e98",
	"af inbox":          "af inbox list",
	"af inbox get":      "af inbox get 1",
	"af inbox list": "" +
		"af inbox list\n" +
		"af inbox list --to ada@example.com --limit 5",
	"af inbox wait": "" +
		"# Blocks until the message arrives, or the timeout runs out.\n" +
		"af inbox wait --to ada@example.com\n" +
		"af inbox wait --subject 'Verify your email' --timeout 60s",
	"af init": "" +
		"af init\n" +
		"af init --non-interactive --answer database.present=yes",
	"af insights": "" +
		"# Rehearses the migration against a branch of the golden.\n" +
		"af insights\n" +
		"# Save a report on the base branch, compare against it on this one.\n" +
		"af insights --save baseline.json\n" +
		"af insights --baseline baseline.json",
	"af invariants":      "af invariants",
	"af license":         "af license status",
	"af license install": "af license install AF-LICENSE-KEY",
	"af license remove":  "af license remove",
	"af license status":  "af license status",
	"af load":            "af load smoke",
	"af load run": "" +
		"# A weighted mix, not one endpoint at a fixed rate.\n" +
		"af load run\n" +
		"af load run --duration 60s --scale 2",
	"af load scenario": "" +
		"af load scenario\n" +
		"af load scenario --only checkout --concurrency 20",
	"af load smoke": "af load smoke",
	"af login": "" +
		"af login\n" +
		"af login --control-plane https://app.antifailure.dev --no-browser",
	"af logout": "af logout",
	"af logs": "" +
		"af logs\n" +
		"af logs web --tail 100",
	"af mask": "af mask plan",
	"af mask apply": "" +
		"# Rewrites this environment's data in place.\n" +
		"af mask apply",
	"af mask plan": "af mask plan",
	"af mask preview": "" +
		"af mask preview\n" +
		"af mask preview --table users --rows 5",
	"af mask verify": "af mask verify",
	"af model":       "af model show",
	"af model show":  "af model show\naf model show -o json",
	"af model test": "" +
		"# One cheap call, so a broken key is found here and not mid run.\n" +
		"af model test\n" +
		"af model test --timeout 10s",
	"af model set": "" +
		"# The key is read from the environment or from stdin, so it never\n" +
		"# reaches the command line or the shell history.\n" +
		"af model set anthropic --from-env ANTHROPIC_API_KEY\n" +
		"af model set anthropic --stdin < key.txt",
	"af model rm": "af model rm anthropic",
	"af net":      "af net policy",
	"af net explain": "" +
		"af net explain GET https://api.stripe.com/v1/charges\n" +
		"af net explain POST https://api.resend.com/emails",
	"af net log": "" +
		"af net log\n" +
		"af net log --blocked --limit 20",
	"af net policy": "af net policy",
	"af oracle": "" +
		"# Runs this change beside the version it replaces and diffs both.\n" +
		"af oracle\n" +
		"af oracle --baseline origin/main --fail-on any",
	"af provider":        "af provider list",
	"af provider budget": "af provider budget anthropic 50",
	"af provider list":   "af provider list",
	"af provider rm":     "af provider rm anthropic",
	"af provider set": "" +
		"# The key is read from the environment or stdin, never from a flag,\n" +
		"# so it does not land in shell history.\n" +
		"af provider set anthropic\n" +
		"af provider set anthropic --stdin\n" +
		"af provider set anthropic --from-env ANTHROPIC_API_KEY",
	"af runner":       "af runner check",
	"af runner check": "af runner check",
	"af runner install": "" +
		"af runner install\n" +
		"af runner install --skip-browser",
	"af secret":      "af secret list",
	"af secret list": "af secret list",
	"af secret rm":   "af secret rm STRIPE_SECRET_KEY",
	"af secret set": "" +
		"# Prompts for the value, or reads it from stdin. Never a flag.\n" +
		"af secret set STRIPE_SECRET_KEY\n" +
		"af secret set STRIPE_SECRET_KEY --stdin",
	"af status":  "af status\naf status -o json",
	"af support": "af support bundle",
	"af support bundle": "" +
		"# Redacted on the way in, with a list of what it included.\n" +
		"af support bundle\n" +
		"af support bundle --archive af-support.zip",
	"af test": "" +
		"af test\n" +
		"af test --only checkout --headed",
	"af token":      "af token list",
	"af token list": "af token list",
	"af token create": "" +
		"# Shown once, at creation. There is no command that prints it again.\n" +
		"af token create ci\n" +
		"af token create ci --control-plane https://app.antifailure.dev",
	"af token rm": "af token rm afe_1a2b3c4d",
	"af up": "" +
		"af up\n" +
		"af up --rebuild --hud",
	"af version": "af version\naf version --short",
	"af webhook": "af webhook list",
	"af webhook list": "" +
		"af webhook list\n" +
		"af webhook list stripe",
	"af webhook trigger": "" +
		"af webhook trigger stripe checkout.session.completed\n" +
		"af webhook trigger stripe invoice.paid --set id=in_123 --set amount_paid=4900",
	"af whoami": "af whoami\naf whoami --offline",
}

// attachExamples puts each command's example on the command.
//
// Applied by walking the tree rather than by naming commands, so a command
// added tomorrow is covered by the test that every command has one, not
// silently skipped by a list somebody forgot to extend.
func attachExamples(root *cobra.Command) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if ex, ok := commandExamples[c.CommandPath()]; ok {
			c.Example = indentExample(ex)
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

// indentExample puts the example under the Examples heading at the same two
// columns everything else in this tree is indented by.
func indentExample(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}

// ExampleCommandPaths is the set of commands the table covers.
//
// Exported for the test that holds the table and the command tree to each
// other. Nothing else has any business reading it.
func ExampleCommandPaths() []string {
	out := make([]string, 0, len(commandExamples))
	for k := range commandExamples {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
