// Command routecheck refuses a marketing site that calls a control plane route
// the control plane it is about to talk to does not serve.
//
// THE FAILURE IT WAS WRITTEN FOR. Somebody filled in the careers form on
// antifailure.dev and was told "Could not reach the server." The form was
// correct. `www/components/pages/company/ApplicationForm.tsx` posts to
// `/v1/applications`, the route is registered in the control plane's server.ts,
// and every test of both halves passed. What was wrong was neither half:
//
//	POST https://app.antifailure.dev/v1/applications  ->  404
//	POST https://app.antifailure.dev/v1/leads         ->  400
//
//	git show v1.1.1:web/apps/api/src/server.ts    | grep -c v1/applications  ->  0
//	git show origin/main:web/apps/api/src/server.ts | grep -c v1/applications -> 1
//
// The site publishes on every merge to main. The control plane only moves when
// a `v*` tag is promoted to production. The careers page went live the instant
// its pull request merged and the API it posts to was still serving v1.1.1,
// twenty two commits behind. The front end had shipped ahead of its own back
// end and nothing anywhere said a word.
//
// WHY THE OBVIOUS CHECK IS THE WRONG ONE, and this is the whole point. Extract
// the routes www calls, compare them against the routes server.ts declares, and
// the check PASSES: main's API declares /v1/applications. The mismatch is not
// between two files in one tree. It is between the front end and the control
// plane that is CURRENTLY RUNNING. A gate that compares the tree against itself
// answers a nearby question, and this repository has thrown away enough
// instruments that answered a nearby question to know what that is worth. So
// the second half of this asks the deployment.
//
// deploy.yml had already written the split down. Its comment on the published
// document says the site "can be ahead of what app.antifailure.dev serves. That
// is tolerable while the API VERSION agrees, because the paths are additive and
// a caller reading an operation that is not there yet gets a 404." That is
// right about openapi.json, which is a document somebody READS. It is wrong
// about this site, which is a CALLER, and for a caller a 404 is a form that
// does not work.
//
// WHAT IT WILL NOT DO. It will not send a request that reaches a handler
// unless it is told to in as many words. Finding out whether the careers
// endpoint exists must not file a job application, so every route in the
// inventory carries the request that proves it is there and the reason that
// request cannot reach what is behind it, checked against the API's source.
// The one route with no such request, `/auth/github`, says so, and is refused
// rather than skipped unless -allow-write-probes is passed.
//
// WHAT IT REFUSES TO GUESS. A route is ABSENT only when the control plane
// itself answered 404. If something in front of the application answered, if
// the origin could not be reached, or if it answered 5xx to a probe, the answer
// is not "absent" and it is not "present": it is that this run did not find
// out, and the run fails saying so. A gate that reported a pass it did not earn
// because a network was briefly down is worse than no gate.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	root := flag.String("root", ".", "repository root")
	origin := flag.String("origin", "", "control plane origin to probe, for example https://app.antifailure.dev. Omitted runs the offline half only.")
	allowWrites := flag.Bool("allow-write-probes", false, "send the probes the inventory declares as writing something. Without it a route that has no inert probe is reported as not checked and fails.")
	timeout := flag.Duration("timeout", 20*time.Second, "per request timeout")
	attempts := flag.Int("attempts", 3, "attempts per route before a transport failure is final")
	flag.Parse()

	if err := run(*root, *origin, *allowWrites, *timeout, *attempts, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "\nroutecheck: %v\n", err)
		os.Exit(1)
	}
}

func run(root, origin string, allowWrites bool, timeout time.Duration, attempts int, out io.Writer) error {
	routes, err := ParseInventory(filepath.Join(root, inventoryPath))
	if err != nil {
		return err
	}
	if len(routes) == 0 {
		return fmt.Errorf("%s declares no routes. An empty inventory would pass every check in this command while the site went on calling whatever it liked, so it is refused rather than treated as nothing to do", inventoryPath)
	}

	strays, err := FindStrayCallSites(filepath.Join(root, "www"))
	if err != nil {
		return err
	}
	if len(strays) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "%d place(s) in www build a control plane URL outside the inventory:\n", len(strays))
		for _, s := range strays {
			fmt.Fprintf(&b, "  %s:%d: %s\n", s.File, s.Line, strings.TrimSpace(s.Text))
		}
		b.WriteString("\nEvery control plane call has to be declared in " + inventoryPath + " so that this\n")
		b.WriteString("command can prove the deployed control plane serves it. Import controlPlaneUrl\n")
		b.WriteString("from that module instead of building the URL here.")
		return errors.New(b.String())
	}
	fmt.Fprintf(out, "the site builds control plane URLs in one place, and declares %d route(s) there\n", len(routes))

	served, err := ParseBoundaryRegister(filepath.Join(root, boundaryPath))
	if err != nil {
		return err
	}
	var undeclared []string
	for _, r := range routes {
		if !served[r.Method+" "+r.Path] {
			undeclared = append(undeclared, r.Name+" calls "+r.Method+" "+r.Path)
		}
	}
	if len(undeclared) > 0 {
		sort.Strings(undeclared)
		return fmt.Errorf("the site calls %d route(s) this repository's control plane does not serve at all:\n  %s\n\n%s classifies every route the router mounts, and route-boundary.test.ts\nfails on one that is missing from it, so a route absent from that register is a\nroute absent from the server", len(undeclared), strings.Join(undeclared, "\n  "), boundaryPath)
	}
	fmt.Fprintf(out, "every route it declares is one this repository's control plane mounts\n")

	if origin == "" {
		fmt.Fprintf(out, "\nno -origin given, so what is DEPLOYED was not checked. The offline half cannot\nsee the failure this command exists for: it passed on the day the careers form\nbroke, because main's API did declare the route.\n")
		return nil
	}
	return probeAll(origin, routes, allowWrites, timeout, attempts, out)
}
