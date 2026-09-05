package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Driving the product's own agent, through the boundary it already has.
//
// af-runner reads one JSON document on standard input and writes one on
// standard output. That is the runner's documented interface and the engine is
// simply another caller of it, so pointing it at a deployed origin needs no new
// harness, no second browser driver, and no copy of the decision logic. The
// engine's own caller sets base_url to the address of an environment it just
// built; this one sets it to an address the internet already answers on.

// jobDocument is what af-runner reads.
type jobDocument struct {
	BaseURL   string          `json:"base_url"`
	Artifacts string          `json:"artifacts"`
	Workflows []smokeWorkflow `json:"workflows"`
	// Sent as an empty list rather than omitted. The runner is tolerant of a
	// null here and the engine was not always sending one, which is how every
	// exploration ever run died on doc.workflows.length; sending the honest
	// empty list is the strict half of that fix and this is a caller, so it
	// keeps it.
	Personas []struct{} `json:"personas"`
	Attempts int        `json:"attempts"`
	Headless bool       `json:"headless"`
}

type outcome struct {
	Verdict      string   `json:"verdict"`
	Cause        string   `json:"cause"`
	Detail       string   `json:"detail"`
	Reproduction []string `json:"reproduction"`
}

type workflowResult struct {
	Workflow string   `json:"workflow"`
	Outcome  outcome  `json:"outcome"`
	Steps    []string `json:"steps"`
	Evidence struct {
		Video      string `json:"video"`
		Trace      string `json:"trace"`
		Screenshot string `json:"screenshot"`
	} `json:"evidence"`
}

type resultDocument struct {
	Results []workflowResult `json:"results"`
}

// runner is how one origin is driven.
type runner struct {
	// Entry is the runner's entry point, which is TypeScript run by node. The
	// engine finds this the same way, and there is deliberately no second
	// build of it here.
	Entry     string
	Node      string
	Artifacts string
	Attempts  int
	Timeout   time.Duration
}

// drive runs one workflow against one origin and returns what it concluded.
//
// EVERY WAY THIS CAN GO WRONG RETURNS Undecided, never Allowed. A runner that
// could not start, a document that would not parse, a result list with nothing
// in it: none of those is evidence that a person can use the site, and the
// only thing that ever produces Allowed is a "pass" verdict read out of a
// document the runner actually wrote.
func (r runner) drive(ctx context.Context, origin string, workflow smokeWorkflow) Finding {
	undecided := func(format string, args ...any) Finding {
		return Finding{
			Origin: origin, Workflow: workflow.Name, Answer: Undecided,
			Said: fmt.Sprintf(format, args...),
		}
	}

	artifacts := filepath.Join(r.Artifacts, sanitize(origin), sanitize(workflow.Name))
	if err := os.MkdirAll(artifacts, 0o755); err != nil {
		return undecided("could not make a place to keep the evidence: %v", err)
	}
	document, err := json.Marshal(jobDocument{
		BaseURL:   strings.TrimSuffix(origin, "/"),
		Artifacts: artifacts,
		Workflows: []smokeWorkflow{workflow},
		Personas:  []struct{}{},
		Attempts:  r.Attempts,
		Headless:  true,
	})
	if err != nil {
		return undecided("could not write the job document: %v", err)
	}

	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.Node, r.Entry)
	cmd.Stdin = bytes.NewReader(document)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	// The exit code is read for the record and never for the answer. The
	// runner exits 8 on a failing workflow and 0 on a blocked one, which is
	// right for a pull request and is not the question here: this tool has to
	// tell "could not reach it" from "reached it and it was broken", and only
	// the document says which.
	var result resultDocument
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		if ctx.Err() != nil {
			return undecided("the run against %s did not finish inside %s.", origin, r.Timeout)
		}
		return undecided(
			"the runner did not return a result document (%v). It said: %s",
			runErr, firstLine(stderr.String()))
	}
	if len(result.Results) == 0 {
		return undecided(
			"the runner returned a document with no results in it, so nothing was checked.")
	}
	return decide(origin, result.Results[0])
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "nothing at all"
	}
	lines := strings.Split(s, "\n")
	// The last line, not the first: node prints its experimental type
	// stripping warning before anything the runner says.
	return strings.TrimSpace(lines[len(lines)-1])
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}
