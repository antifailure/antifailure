package env_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Whether a stored model key actually reaches the two processes that spend it.
//
// This is the test that decides whether 'af model set' is a feature or a
// shippable gap that looks like one. The key resolves correctly in the CLI, in
// af doctor and in af explain whether or not anything is wired, and this
// repository has shipped that exact shape before: a capability defined, visible
// from every angle, and called by nothing.
//
// The two consumers are the runner subprocess, which drives the browser and
// plans with the model, and the egress sidecar, which spends a key when a rule
// is in synth mode. Both read the provider's own variable names, so what is
// asserted here is that a key from a source other than the process environment
// arrives looking exactly like one somebody exported.

func modelOrchestrator(
	t *testing.T, manifest *schema.Manifest, chain *secrets.Chain,
) *env.Orchestrator {
	t.Helper()
	o, err := env.New(env.Options{
		Root:     t.TempDir(),
		Manifest: manifest,
		Branch:   "main",
		Clock:    clock.New(),
		Redactor: redact.New(),
		Secrets:  chain,
	})
	require.NoError(t, err)
	return o
}

// keyringChain is a chain whose only source is an in-memory credential store,
// so a key can only be found somewhere other than the process environment.
func keyringChain(values map[string]string) *secrets.Chain {
	return secrets.NewChain(secrets.NewKeyringSource(fakeRing(values), "antifailure"))
}

type fakeRing map[string]string

func (r fakeRing) Get(_, name string) (string, error) {
	v, ok := r[name]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}
func (r fakeRing) Set(_, name, value string) error { r[name] = value; return nil }
func (r fakeRing) Delete(_, name string) error     { delete(r, name); return nil }

// The runner inherited this process's environment and nothing else, so a key in
// the keyring or the encrypted store was found by every part of the engine
// except the one that needed it.
func TestRunnerEnvironment_CarriesAKeyFromOutsideTheProcess(t *testing.T) {
	o := modelOrchestrator(t, &schema.Manifest{Name: "app"},
		keyringChain(map[string]string{"ANTHROPIC_API_KEY": "sk-from-the-keyring"}))

	got := o.RunnerEnvironmentForTest(context.Background())

	require.Contains(t, got, "ANTHROPIC_API_KEY=sk-from-the-keyring")
	require.Contains(t, got, "AF_MODEL=claude-sonnet-5")
	// Still a pass-through. The runner is af's own subprocess on this machine,
	// not a container in the preview environment, and it needs node's own
	// configuration, a home directory and a browser cache. The rule that a
	// service gets only what the manifest declares is about isolating the
	// application under test.
	require.Greater(t, len(got), len(os.Environ()),
		"the process environment was replaced rather than added to")
	for _, want := range os.Environ() {
		if strings.HasPrefix(want, "PATH=") {
			require.Contains(t, got, want)
		}
	}
}

// A key that cannot be resolved is not a reason to fail a run. With none the
// deterministic planner runs, which is a supported mode, and stopping here
// would turn a locked keyring into a failed test suite.
func TestRunnerEnvironment_NoKeyChangesNothing(t *testing.T) {
	o := modelOrchestrator(t, &schema.Manifest{Name: "app"}, keyringChain(nil))

	got := o.RunnerEnvironmentForTest(context.Background())
	require.Equal(t, os.Environ(), got)
}

// The key is registered with the redactor before it is handed over, so that
// nothing the runner prints on its way to AF-AGT-003 can carry it into an
// error message a person reads.
func TestRunnerEnvironment_RegistersTheKeyForRedaction(t *testing.T) {
	redactor := redact.New()
	o, err := env.New(env.Options{
		Root:     t.TempDir(),
		Manifest: &schema.Manifest{Name: "app"},
		Branch:   "main",
		Clock:    clock.New(),
		Redactor: redactor,
		Secrets:  keyringChain(map[string]string{"ANTHROPIC_API_KEY": "sk-must-be-hidden"}),
	})
	require.NoError(t, err)

	require.Contains(t, redactor.String("before: sk-must-be-hidden"), "sk-must-be-hidden",
		"nothing has registered it yet, so this is what the redactor does before")

	o.RunnerEnvironmentForTest(context.Background())

	require.NotContains(t,
		redactor.String("the runner said sk-must-be-hidden"), "sk-must-be-hidden")
}

// ---------------------------------------------------------------------------
// The egress sidecar
// ---------------------------------------------------------------------------

// A key is sent to the sidecar only when a rule actually asks for one. Handing
// a credential to a container with no use for it is a credential in one more
// place for no reason.
func TestModelEnv_OnlyForASynthRule(t *testing.T) {
	chain := keyringChain(map[string]string{"ANTHROPIC_API_KEY": "sk-for-synth"})

	noEgress := modelOrchestrator(t, &schema.Manifest{Name: "app"}, chain)
	require.Empty(t, noEgress.ModelEnvForTest(context.Background()))

	blocked := modelOrchestrator(t, &schema.Manifest{
		Name:   "app",
		Egress: &schema.Egress{Default: schema.ModeBlock},
	}, chain)
	require.Empty(t, blocked.ModelEnvForTest(context.Background()),
		"a manifest with no synth rule has no use for a model key")

	synth := modelOrchestrator(t, &schema.Manifest{
		Name: "app",
		Egress: &schema.Egress{
			Default: schema.ModeBlock,
			Rules:   []schema.EgressRule{{Host: "api.stripe.com", Mode: schema.ModeSynth}},
		},
	}, chain)
	require.Contains(t, synth.ModelEnvForTest(context.Background()),
		"ANTHROPIC_API_KEY=sk-for-synth")
}

// The sidecar read the process environment directly, which meant a key stored
// with 'af model set' was found by every other part of the engine and then not
// by the one place a synth rule spends it. The symptom was a synth rule
// refusing with "set ANTHROPIC_API_KEY" to somebody who had.
func TestModelEnv_CarriesAKeyFromOutsideTheProcess(t *testing.T) {
	o := modelOrchestrator(t, &schema.Manifest{
		Name: "app",
		Egress: &schema.Egress{
			Default: schema.ModeSynth,
		},
	}, keyringChain(map[string]string{
		"ANTHROPIC_API_KEY": "sk-from-the-keyring",
		"AF_MODEL":          "claude-opus-5",
	}))

	got := o.ModelEnvForTest(context.Background())
	require.Contains(t, got, "ANTHROPIC_API_KEY=sk-from-the-keyring")
	require.Contains(t, got, "AF_MODEL=claude-opus-5")
	// One provider's key, not every key that happens to be set. The sidecar
	// picks the first provider it has a key for, in this same order, so a
	// second one could never have been used.
	for _, entry := range got {
		require.False(t, strings.HasPrefix(entry, "OPENAI_API_KEY="))
	}
}

// The claim the rest of this file cannot make on its own: the subprocess
// actually receives the key.
//
// Testing runnerEnvironment alone proved nothing. Deleting the one line in
// invokeRunner that hands its result to the command left every test of the
// assembling function green, which is precisely the shape of defect this
// repository has shipped before: a capability defined, correct, and connected
// to nothing. So this runs a real subprocess and asks it what it was given.
func TestInvokeRunner_TheSubprocessReceivesTheKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in runner is a shell script")
	}

	root := t.TempDir()
	// findRunner looks for this beside the repository root. Its contents do
	// not matter: the stand-in below ignores the script it is handed.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "runner", "src"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "runner", "src", "main.ts"), nil, 0o644))

	// A stand-in for node, first on PATH, that reports one thing: the model
	// variables it was started with. It writes a document to stdout because
	// invokeRunner treats silence as the runner's own failure.
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "node"), []byte(
		"#!/bin/sh\n"+
			"cat > /dev/null\n"+
			"printf '{\"key\":\"%s\",\"model\":\"%s\"}' "+
			"\"$ANTHROPIC_API_KEY\" \"$AF_MODEL\"\n"), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	o, err := env.New(env.Options{
		Root:     root,
		Manifest: &schema.Manifest{Name: "app"},
		Branch:   "main",
		Clock:    clock.New(),
		Redactor: redact.New(),
		Secrets: keyringChain(map[string]string{
			"ANTHROPIC_API_KEY": "sk-reached-the-runner",
			"AF_MODEL":          "claude-opus-5",
		}),
	})
	require.NoError(t, err)

	out, err := o.InvokeRunnerForTest(
		context.Background(), "", filepath.Join(root, "artifacts"))
	require.NoError(t, err)

	var got struct {
		Key   string `json:"key"`
		Model string `json:"model"`
	}
	require.NoError(t, json.Unmarshal(out, &got))
	require.Equal(t, "sk-reached-the-runner", got.Key,
		"a key that resolves everywhere the engine looks never reached the "+
			"one subprocess that spends it")
	require.Equal(t, "claude-opus-5", got.Model)
}
