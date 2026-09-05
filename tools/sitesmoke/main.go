// Command sitesmoke drives a DEPLOYED antifailure.dev with the product's own
// agent and fails when a person's path through it does not work.
//
// THE FAILURE IT WAS WRITTEN FOR. Somebody filled in the careers form on the
// real https://antifailure.dev and was told "Could not reach the server." The
// question that followed was the right one: this product's whole promise is
// exploratory agents that drive an application the way a person does, and it
// dogfoods itself on every pull request, so why did they not find it.
//
// The first answer was that the agents were pointed at the wrong thing. A
// dogfood run builds a disposable stack out of ONE commit, where the front end
// and the API necessarily agree, so the form submits and the run correctly
// reports nothing wrong. The bug lived in the space between two independently
// versioned deployments, and an environment built from a single commit has no
// such space in it. That answer is true.
//
// It is not the whole answer, and the rest of it is the reason this tool comes
// with changes to the runner rather than on its own. Pointed straight at the
// deployed site, on the day it was broken, the agent would have exited zero.
// Four things, each independent, each now fixed and each covered by a test in
// runner/test/browser.test.ts:
//
//  1. A checkbox reports value="on" whether or not it is ticked, so the
//     snapshot called the required compensation acknowledgment "filled" and
//     the planner skipped it. So did the role radios.
//  2. "What have you built or grown, and why this role" is required and
//     matches no field shape the planner knows, so it stayed empty and the
//     browser refused to submit the form at all.
//  3. The button says "Send application", which is on no list of words that
//     move a workflow forward, so nothing pressed it. Adding the document's
//     own submit controls then exposed a worse one: the site header carries a
//     "Sign in" link, the word list is consulted first, and the agent filled
//     in the whole form and then navigated away from it.
//  4. "Could not reach the server" was on no list of failure signals, so the
//     page telling the agent its request never arrived was judged UNREADABLE.
//     The verdict was unverified, and unverified exits zero.
//
// WHAT IT WILL NOT DO. It will not file a job application. The careers form
// writes into a queue a person reads, and a check that runs on a schedule must
// not put a row in it every time. The scheduled workflow answers the optional
// work link with a URL the control plane's own validation refuses, so the
// request reaches the handler, is refused before anything is written, and the
// page renders the control plane's own sentence. The whole path including the
// write is a second workflow, and it runs only when somebody passes
// -allow-writes. See tools/sitesmoke/workflows.go, which carries the reason
// each is inert or is not, checked against the API's source.
//
// WHAT IT IS NOT. It is not tools/routecheck, which asks a control plane
// whether a route exists and merged before this. An endpoint probe reports
// `POST /v1/applications -> 404`; a person reads "Could not reach the server."
// Only the second is the failure anybody had, and reaching it needs the page,
// its JavaScript, its build time configuration and a cross origin request from
// the exact hostname somebody typed. Both halves are wanted. Neither is the
// other.
//
// WHAT IT REFUSES TO GUESS. Three answers, not two. 0 is a proven pass, 1 is a
// page that showed the wrong thing, and 2 is a run that did not find out: the
// origin unreachable, the browser unable to drive it, or two attempts
// disagreeing. Continuous integration treats 1 and 2 alike. Nothing here
// returns 0 except a "pass" verdict read out of a document the runner wrote.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type origins []string

func (o *origins) String() string     { return strings.Join(*o, ",") }
func (o *origins) Set(v string) error { *o = append(*o, v); return nil }

func main() {
	var targets origins
	root := flag.String("root", ".", "repository root")
	flag.Var(&targets, "origin", "a deployed site origin to drive, for example https://antifailure.dev. Repeatable. With none, only the offline half runs.")
	entry := flag.String("runner", "", "path to the runner's entry point. Defaults to runner/src/main.ts under -root.")
	node := flag.String("node", "node", "the node binary to run the runner with")
	artifacts := flag.String("artifacts", "", "where to keep screenshots, video and traces. Defaults to a temporary directory.")
	attempts := flag.Int("attempts", 2, "how many independent browser sessions must agree before a failure is called one")
	timeout := flag.Duration("timeout", 5*time.Minute, "per workflow timeout")
	allowWrites := flag.Bool("allow-writes", false, "also run the workflow that files a real job application. Off by default: the careers form writes into a queue a person reads.")
	flag.Parse()

	code, err := run(*root, targets, *entry, *node, *artifacts, *attempts, *timeout, *allowWrites, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nsitesmoke: %v\n", err)
	}
	os.Exit(code)
}

func run(
	root string, targets []string, entry, node, artifacts string,
	attempts int, timeout time.Duration, allowWrites bool, out *os.File,
) (int, error) {
	var report strings.Builder

	// The offline half first, and it runs whether or not an origin was given.
	// It is cheap and its failure invalidates everything below it: an
	// expectation waiting for a sentence this repository no longer produces
	// would fail every deployment, and one satisfied by the wrong page would
	// pass every deployment.
	report.WriteString("The sentences this check waits for\n")
	if err := contractHolds(root, &report); err != nil {
		fmt.Fprint(out, report.String())
		return exitUndecided, err
	}

	if len(targets) == 0 {
		fmt.Fprint(out, report.String())
		fmt.Fprint(out, "\nNo origin was given, so NO DEPLOYMENT WAS CHECKED.\n"+
			"This half proves only that the sentences above are still the ones this\n"+
			"repository produces. On the day the careers form broke, this half was green:\n"+
			"the tree was correct and production was serving a version from before the\n"+
			"route existed. Pass -origin https://antifailure.dev to ask a deployment.\n")
		return exitAllowed, nil
	}

	if entry == "" {
		entry = filepath.Join(root, "runner", "src", "main.ts")
	}
	if _, err := os.Stat(entry); err != nil {
		return exitUndecided, fmt.Errorf(
			"the runner's entry point is not at %s (%v). This tool drives the product's own "+
				"agent and will not substitute anything else for it", entry, err)
	}
	if artifacts == "" {
		dir, err := os.MkdirTemp("", "sitesmoke-")
		if err != nil {
			return exitUndecided, fmt.Errorf("make a directory for the evidence: %w", err)
		}
		artifacts = dir
	}
	if attempts < 1 {
		return exitUndecided, fmt.Errorf(
			"-attempts is %d. A check that runs a workflow no times cannot fail, which is worse "+
				"than not having it", attempts)
	}

	// Interrupted rather than killed, so a half finished browser still writes
	// its evidence out.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	r := runner{
		Entry: entry, Node: node, Artifacts: artifacts,
		Attempts: attempts, Timeout: timeout,
	}
	workflows := workflowsFor(allowWrites)

	fmt.Fprint(out, report.String())
	fmt.Fprintf(out, "\nDriving %d %s against %d %s, %d attempts each.\n",
		len(workflows), plural(len(workflows), "workflow", "workflows"),
		len(targets), plural(len(targets), "origin", "origins"), attempts)
	for _, w := range workflows {
		if w.writes {
			fmt.Fprintf(out, "  %s WRITES. It files a real job application into the queue a "+
				"person reads, and it is running because -allow-writes was passed.\n", w.Name)
		} else {
			fmt.Fprintf(out, "  %s writes nothing. %s\n", w.Name, w.why)
		}
	}
	fmt.Fprintln(out)

	var findings []Finding
	for _, origin := range targets {
		for _, w := range workflows {
			fmt.Fprintf(out, "  %s  %s ...\n", origin, w.Name)
			findings = append(findings, r.drive(ctx, origin, w))
		}
	}

	answer := verdict(findings)
	fmt.Fprintf(out, "\n%s\n", summary(findings))
	fmt.Fprintf(out, "Evidence is in %s\n", artifacts)

	switch answer {
	case Allowed:
		fmt.Fprintf(out, "\nEvery origin did what a person needs it to do.\n")
		return exitAllowed, nil
	case Refused:
		return exitRefused, fmt.Errorf(
			"a person cannot complete the careers form on at least one of these hostnames right " +
				"now. The sentence the page showed is above, and it is what somebody filling the " +
				"form in would read")
	default:
		return exitUndecided, fmt.Errorf(
			"this run did not find out. That is not a pass: it is reported as its own answer " +
				"rather than rounded to one, because a smoke that cannot say \"I did not find " +
				"out\" says \"fine\" instead")
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
