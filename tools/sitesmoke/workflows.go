package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// What this tool asks the deployed site to do, and what it must say back.
//
// A WORKFLOW HERE IS THE PRODUCT'S OWN WORKFLOW, not a second test framework.
// Each one below is a `Workflow` in the shape `af-runner` reads on standard
// input: a name, a sentence, where to start, what has to be on the page at the
// end. The runner opens Chromium, reads the accessibility tree, decides what
// to fill and what to press, and returns a verdict. Nothing here drives a
// browser and nothing here knows a selector.
//
// EVERY EXPECTATION IS QUOTED, and that is not a style. An unquoted expectation
// is judged by how many of its meaningful words appear on the page, which is
// right for a sentence about a product and wrong for a sentence a page either
// renders or does not. The control plane's own refusal, "Use a public http or
// https link without credentials", scores six of its seven words against the
// careers page BEFORE the form is touched, because `public`, `link`, `use`,
// `credentials` and an install command containing `https` are all already on
// it. Quoted, it is present or it is absent, and absent is an answer.
type smokeWorkflow struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	StartPath   string            `json:"startPath"`
	Expect      []string          `json:"expect"`
	Answers     map[string]string `json:"answers,omitempty"`

	// writes says whether running this against a deployment puts a row in a
	// table a person reads. Declared rather than discovered, the same way
	// tools/routecheck declares what each of its probes costs, so a run that
	// files a real job application is a run somebody asked for in as many
	// words.
	writes bool
	// why records what makes this inert, checked against the API's source
	// rather than assumed. Empty for a workflow that writes.
	why string
}

// The work link that makes the scheduled check inert.
//
// A URL with credentials in it. `applicationInput` in the control plane refines
// projectUrl to reject exactly that, and the refinement runs inside
// `safeParse`, which the route calls BEFORE `recordApplication`, so the request
// reaches the handler, is refused, and writes nothing. The browser's own
// validation accepts it, because it is a valid absolute URL, which is the
// property that lets the form be submitted at all.
//
// Verified against the deployed control plane rather than reasoned about:
//
//	POST https://app.antifailure.dev/v1/applications
//	  Origin: https://antifailure.dev
//	  -> 400 {"error":"Use a public http or https link without credentials."}
//
// example.test is a reserved domain under RFC 2606, so the host in it belongs
// to nobody and can never be reached.
const inertWorkLink = "https://smoke:smoke@example.test/"

// refusalSentence is the control plane's own answer to that link.
//
// This tool and the control plane ship on different clocks, so this string is
// a coupling across a deployment boundary, which is the exact hazard this
// whole lane is about. It is not left to trust: contractHolds below reads the
// API's source and fails when the sentence there is no longer this one.
const refusalSentence = "Use a public http or https link without credentials."

// tryAgain is the site's own label once a submission has been refused. It is
// what proves the form was actually SENT rather than merely filled in: the
// button says "Send application" until something comes back.
const tryAgain = "Try it again"

// recordedSentence is what the careers page says when a row exists.
//
// The site earns it: ApplicationForm.tsx checks that the answer carries
// recorded:true, the submission key it sent, and a uuid, before it renders
// this. So a workflow that ends here has proved the whole path, browser to
// database, and not merely that something answered.
const recordedSentence = "It is written down."

// theCareersFormReachesTheControlPlane is the scheduled check.
//
// A PERSON'S EXPERIENCE, WHICH IS THE WHOLE POINT OF IT. An endpoint probe
// reports `POST /v1/applications -> 404`. A person reads "Could not reach the
// server." Those are different failures and only the second is the one anybody
// actually had. tools/routecheck owns the first and runs before the site
// publishes; this owns the second and runs against what browsers are pointed
// at. Neither replaces the other: routecheck sends no Origin header and never
// opens a page, so it cannot see the site's build time configuration, its
// JavaScript, or a control plane that refuses this particular hostname.
//
// It writes nothing. See inertWorkLink.
func theCareersFormReachesTheControlPlane() smokeWorkflow {
	return smokeWorkflow{
		Name: "the-careers-form-reaches-the-control-plane",
		Description: "Fill in the careers form as a person would, press the button, and read what " +
			"the page says came back.",
		StartPath: "/careers",
		Expect: []string{
			quoted(refusalSentence),
			quoted(tryAgain),
		},
		Answers: map[string]string{"Link to your work": inertWorkLink},
		writes:  false,
		why: "The control plane's own validation refuses a work link with credentials in it, " +
			"in applicationInput's projectUrl refinement, and safeParse runs before " +
			"recordApplication. Nothing is written and nothing is sent.",
	}
}

// applyForAFoundingRole is the whole path, and it files a real application.
//
// It exists because the check above deliberately stops one step short: it
// proves that a person's browser reached the deployed control plane and that
// the control plane's own answer came back and rendered, and it does NOT prove
// that a valid application is written to a database. Nothing else proves that
// through a browser, so the workflow that does is kept, named, and refused
// unless somebody asks for it by name.
//
// There is no staging copy of this site to run it against. antifailure.dev and
// www.antifailure.dev are two custom domains on one Static Web App, publishing
// on every merge to main, and there is no third. So the choice really is
// between writing a row into the hiring queue a person reads and not proving
// the write at all, and the answer is that a scheduled check must not file job
// applications and a human running one deliberately may.
func applyForAFoundingRole() smokeWorkflow {
	return smokeWorkflow{
		Name: "apply-for-a-founding-role",
		Description: "Apply for a founding role and confirm the page says the application is " +
			"recorded.",
		StartPath: "/careers",
		Expect:    []string{quoted(recordedSentence)},
		writes:    true,
	}
}

func quoted(s string) string { return `"` + s + `"` }

// workflowsFor picks what this run will do.
func workflowsFor(allowWrites bool) []smokeWorkflow {
	if allowWrites {
		return []smokeWorkflow{theCareersFormReachesTheControlPlane(), applyForAFoundingRole()}
	}
	return []smokeWorkflow{theCareersFormReachesTheControlPlane()}
}

// contract is one sentence this tool expects a deployed page to show, and the
// file in this repository that is supposed to produce it.
type contract struct {
	sentence string
	file     string
	// why says what breaks when the file no longer carries the sentence.
	why string
}

func contracts() []contract {
	return []contract{
		{
			sentence: refusalSentence,
			file:     filepath.Join("web", "apps", "api", "src", "recruitment", "applications.ts"),
			why: "It is the control plane's refusal of a work link with credentials, and it is " +
				"what the scheduled check waits for. If the API says something else now, the " +
				"check will go red against a site that is working perfectly, on the day the " +
				"new message is promoted to production.",
		},
		{
			sentence: recordedSentence,
			file:     filepath.Join("www", "components", "pages", "company", "ApplicationForm.tsx"),
			why: "It is the confirmation the careers page renders once a row exists, and it is " +
				"the only thing that tells a completed submission from a submitted one.",
		},
		{
			sentence: tryAgain,
			file:     filepath.Join("www", "components", "pages", "company", "ApplicationForm.tsx"),
			why: "It is the button's label after a refusal, and it is what proves the form was " +
				"sent rather than merely filled in.",
		},
		{
			sentence: "compensationAcknowledged",
			file:     filepath.Join("web", "apps", "api", "src", "recruitment", "applications.ts"),
			why: "The form the agent drives has a required acknowledgment on it, and a control " +
				"plane that stopped requiring one would mean the agent's tick proves nothing.",
		},
	}
}

// contractHolds is the OFFLINE half, and it says out loud what it did not check.
//
// It cannot see a deployment. On the day the careers form broke, the tree was
// perfect: main's control plane declared the route, the form posted to it, and
// what was wrong was that production served a version from before the route
// existed. So this half is not evidence that the site works. It is evidence
// that the sentences the ONLINE half waits for are still the sentences this
// repository produces, which is the one way the online half could go quietly
// wrong: an expectation nothing can ever satisfy passes no deployment, and an
// expectation satisfied by the wrong page passes every one.
func contractHolds(root string, out *strings.Builder) error {
	var broken []string
	for _, c := range contracts() {
		path := filepath.Join(root, c.file)
		source, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s, which is supposed to carry %q: %w", c.file, c.sentence, err)
		}
		if !strings.Contains(string(source), c.sentence) {
			broken = append(broken, fmt.Sprintf("  %s\n    no longer contains %q\n    %s",
				c.file, c.sentence, c.why))
			continue
		}
		fmt.Fprintf(out, "  %s\n    still says %q\n", c.file, c.sentence)
	}
	if len(broken) > 0 {
		return fmt.Errorf("%d sentence(s) this check waits for are not in the tree any more:\n%s",
			len(broken), strings.Join(broken, "\n"))
	}
	return nil
}
