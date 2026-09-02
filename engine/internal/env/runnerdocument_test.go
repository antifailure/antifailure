package env_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The document between the engine and the runner is a wire format, and until
// af explore turned out to be impossible on every machine nothing asserted its
// shape.
//
// The exploration path never set the workflows field. A nil slice marshals as
// null. main.ts read doc.workflows.length before it looked at the goals, so
// every af explore run exited AF-AGT-003 with a TypeError. Both halves worked
// in isolation and neither suite sent the document the product sends.
//
// Two tests, because two claims. The first is about the bytes and needs
// nothing installed. The second drives the real runner and refuses rather than
// skips when node is absent, because a silent skip on a missing tool reads as
// a pass and that is the shape that hid this in the first place.

// Returns the root as well as the orchestrator, because findRunner resolves an
// empty override against it and a test that wants a runner found has to be able
// to put one there.
func orchestratorForDocument(t *testing.T) (*env.Orchestrator, string) {
	t.Helper()
	root := t.TempDir()
	o, err := env.New(env.Options{
		Root:     root,
		Manifest: &schema.Manifest{Name: "app"},
		Branch:   "main",
		Clock:    clock.New(),
		Redactor: redact.New(),
	})
	require.NoError(t, err)
	return o, root
}

func TestTheRunnerDocumentNeverCarriesANullList(t *testing.T) {
	o, root := orchestratorForDocument(t)
	artifacts := filepath.Join(t.TempDir(), "artifacts")

	// A runner where findRunner looks first, because an empty override
	// resolves against the orchestrator's root and everything after that is
	// somebody's machine: the home directory and the directory the test binary
	// was built into. This test passed only where a release happened to be
	// installed under ~/.antifailure and failed in CI, which is the machine
	// dependence this file's own comment complains about one test lower down.
	//
	// Its content is deliberately never read. The node below ignores the script
	// it is handed, so what is asserted stays the bytes on stdin.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "runner", "src"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "runner", "src", "main.ts"), []byte("// not read\n"), 0o644))

	// A node that reads the document and prints it back, so the assertion is
	// over the bytes the subprocess actually received rather than over a
	// struct that has not been marshalled yet.
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "node"),
		[]byte("#!/bin/sh\ncat\n"), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	sent, out, err := o.InvokeRunnerCapturingDocument(
		context.Background(), "", artifacts,
		[]schema.Goal{{Name: "upgrade", Goal: "upgrade the plan", Seed: "s"}})
	require.NoError(t, err)
	require.JSONEq(t, string(sent), string(out),
		"the bytes the runner received are the bytes the engine marshalled")

	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(sent, &doc))
	for _, field := range []string{"workflows", "personas", "goals"} {
		raw, ok := doc[field]
		require.Truef(t, ok, "%s must be present", field)
		require.NotEqualf(t, "null", string(raw),
			"%s marshalled as null, which is what the runner cannot read; "+
				"an empty list is a legal document and null is a crash", field)
	}
	// And the goals it was given survived, so the fix is not "send empty
	// lists for everything".
	require.Contains(t, string(doc["goals"]), "upgrade")
}

func TestTheRealRunnerAcceptsADocumentWithNoWorkflows(t *testing.T) {
	// Refuses rather than skips. A check that goes quiet when a tool is
	// missing reads as a pass in every summary anybody looks at, and the
	// defect this test exists for survived precisely because nothing drove
	// this path.
	if _, err := exec.LookPath("node"); err != nil {
		if os.Getenv("AF_REQUIRE_RUNNER") != "" {
			t.Fatal("AF_REQUIRE_RUNNER is set and node is not on PATH")
		}
		t.Skip("node is not installed; set AF_REQUIRE_RUNNER to make this a failure")
	}
	// Absolute, because invokeRunner sets the subprocess working directory to
	// the repository root the orchestrator was built with, which here is a
	// temporary directory. A relative path resolves against that and not
	// against the test's own directory.
	runner, err := filepath.Abs(filepath.Join("..", "..", "..", "runner", "src", "main.ts"))
	require.NoError(t, err)
	if _, statErr := os.Stat(runner); statErr != nil {
		t.Skip("the runner source is not in this tree")
	}
	modules, err := filepath.Abs(filepath.Join("..", "..", "..", "runner", "node_modules"))
	require.NoError(t, err)
	if _, statErr := os.Stat(modules); statErr != nil {
		if os.Getenv("AF_REQUIRE_RUNNER") != "" {
			t.Fatal("AF_REQUIRE_RUNNER is set and runner/node_modules is absent; run npm ci in runner")
		}
		t.Skip("runner/node_modules is absent; run npm ci in runner")
	}

	o, _ := orchestratorForDocument(t)
	_, out, runErr := o.InvokeRunnerCapturingDocument(
		context.Background(), runner, filepath.Join(t.TempDir(), "artifacts"), nil)
	require.NoError(t, runErr,
		"the runner produced no output at all, which is what a document it cannot read looks like")

	var doc struct {
		Results      []json.RawMessage `json:"results"`
		Explorations []json.RawMessage `json:"explorations"`
	}
	require.NoError(t, json.Unmarshal(out, &doc))
	require.Empty(t, doc.Results)
	require.Empty(t, doc.Explorations)
}
