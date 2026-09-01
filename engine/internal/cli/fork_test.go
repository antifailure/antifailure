package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// What `github.fork_policy` does, now that it does anything.
//
// Every test here was written against the broken behaviour first and watched
// fail. Before the gate existed, `af up` on a fork pull request with
// `fork_policy: never` printed "Bringing up forkrepro-main-0cd221" and went to
// the Docker daemon, so the refusal assertions all failed on the message and
// the attack test below failed by letting the attack through.
//
// The allowed cases assert AF-RUN-040 rather than a successful environment.
// That is lifecycle_failure_test.go's trick: a file where `.antifailure` would
// go makes `af up` fail as soon as it opens its state directory, which is
// after the gate and before anything is built. It is the cheapest observable
// that says "the gate let this through", and it costs no daemon.

// forkManifest is the manifest committed to the base branch, with a policy.
func forkManifest(policy string) string {
	return `version: 1
name: standing
services:
  - name: standing
    kind: web
    build:
      strategy: dockerfile
      dockerfile: Dockerfile
    command: node server.js
    port: 3000
database:
  provider: docker
  version: 17
  url_env: DATABASE_URL
egress:
  default: block
github:
  mode: actions
  fork_policy: ` + policy + `
`
}

// forkRepo builds a checkout with basePolicy committed to the base branch and
// headPolicy in the working tree, which is the difference the attack test
// turns on.
func forkRepo(t *testing.T, basePolicy, headPolicy string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	write("Dockerfile", "FROM alpine\n")
	write("antifailure.yaml", forkManifest(basePolicy))

	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	}
	git("init", "-q", "-b", "main")
	git("add", "-A")
	git("commit", "-qm", "base")

	if headPolicy != basePolicy {
		write("antifailure.yaml", forkManifest(headPolicy))
	}
	// Where .antifailure would be created, so af up fails the moment it opens
	// its state directory. Written after the commit so it is not in the tree
	// the base manifest is read from.
	write(".antifailure", "not a directory")
	return dir
}

// forkEvent writes a payload and returns its path.
func forkEvent(t *testing.T, dir, headRepo string, labels ...string) string {
	t.Helper()
	quoted := make([]string, 0, len(labels))
	for _, l := range labels {
		quoted = append(quoted, `{"name":"`+l+`"}`)
	}
	head := `null`
	if headRepo != "" {
		head = `{"full_name":"` + headRepo + `"}`
	}
	body := `{"action":"opened","number":7,"repository":{"full_name":"acme/shop"},
	  "pull_request":{"number":7,"labels":[` + strings.Join(quoted, ",") + `],
	    "head":{"sha":"c0ffee","ref":"attack","repo":` + head + `},
	    "base":{"ref":"main","repo":{"full_name":"acme/shop"}}}}`
	path := filepath.Join(dir, "event.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

// runUp runs af up with a substituted environment and returns everything it
// said, plus the exit code.
func runUp(t *testing.T, dir string, env map[string]string) (int, string) {
	t.Helper()
	var out, errW bytes.Buffer
	code := Execute(context.Background(), []string{"up"}, Options{
		Stdout: &out, Stderr: &errW, Stdin: strings.NewReader(""),
		Getenv: func(k string) string { return env[k] },
		Clock:  clock.New(), WorkDir: dir,
	})
	return code, strings.Join(strings.Fields(out.String()+errW.String()), " ")
}

// prEnv is what GitHub Actions sets on a pull request.
func prEnv(eventPath string) map[string]string {
	return map[string]string{
		"GITHUB_EVENT_NAME": "pull_request",
		"GITHUB_EVENT_PATH": eventPath,
		"GITHUB_REPOSITORY": "acme/shop",
		"GITHUB_BASE_REF":   "main",
		"GITHUB_HEAD_REF":   "attack",
		"GITHUB_REF":        "refs/pull/7/merge",
	}
}

func TestForkGate_NeverRefusesAForksPullRequest(t *testing.T) {
	dir := forkRepo(t, "never", "never")
	code, said := runUp(t, dir, prEnv(forkEvent(t, dir, "stranger/shop")))

	require.Equal(t, 6, code, "a policy denial is exit 6")
	require.Contains(t, said, "AF-GH-003")
	require.Contains(t, said, "stranger/shop")
	require.NotContains(t, said, "Bringing up",
		"the refusal has to happen before an environment is named, "+
			"which is the whole difference between this and printing the policy")
}

func TestForkGate_LabelRefusesUntilAMaintainerAddsTheLabel(t *testing.T) {
	dir := forkRepo(t, "label", "label")

	code, said := runUp(t, dir, prEnv(forkEvent(t, dir, "stranger/shop", "needs-review")))
	require.Equal(t, 6, code)
	require.Contains(t, said, "AF-GH-003")
	require.Contains(t, said, "antifailure:allow")

	// And with it. AF-RUN-040 is the state directory failing, which is the
	// step after the gate: reaching it is the proof the gate let this through.
	code, said = runUp(t, dir, prEnv(forkEvent(t, dir, "stranger/shop", "needs-review", "antifailure:allow")))
	require.NotContains(t, said, "AF-GH-003", "the label is what the docs promise, so it has to work")
	require.Contains(t, said, "AF-RUN-040")
	require.NotEqual(t, 6, code)
}

// The attack the gate exists to survive, and the reason it does not read the
// manifest in front of it.
//
// The manifest lives in the repository, so on a fork pull request the checked
// out antifailure.yaml is the FORK'S. If the policy were read from there,
// anybody could add `fork_policy: always` to their own pull request and the
// security control would be a suggestion. This test fails, loudly, against any
// implementation that reads the tree under test.
func TestForkGate_ReadsThePolicyFromTheBaseBranchAndNotFromTheFork(t *testing.T) {
	dir := forkRepo(t, "never", "always")

	// Sanity: the working tree really does say always, so a pass here is not
	// the fixture failing to set up the attack.
	head, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(head), "fork_policy: always")

	code, said := runUp(t, dir, prEnv(forkEvent(t, dir, "stranger/shop")))
	require.Equal(t, 6, code,
		"the fork's own manifest must not be able to lift the base branch's policy")
	require.Contains(t, said, "AF-GH-003")
	require.Contains(t, said, "never")
}

// The cases where the gate has no opinion, tested at the gate rather than
// through the command.
//
// Deliberate, and the reason belongs next to it: an allowed run goes on to do
// real work, and on this codebase `af up` takes forty seconds to reach the
// state directory even when it is going to fail there. One allowed run through
// the whole command is enough to prove the wiring, and it is in the label test
// above. Multiplying it by every silent case buys nothing but minutes.
func TestForkGate_IsSilentWhenThereIsNoForkInTheQuestion(t *testing.T) {
	dir := forkRepo(t, "never", "never")
	gate := func(env map[string]string) forkDecision {
		return forkGate(&Env{WorkDir: dir, Getenv: func(k string) string { return env[k] }})
	}

	t.Run("a pull request from the same repository", func(t *testing.T) {
		d := gate(prEnv(forkEvent(t, dir, "acme/shop")))
		require.False(t, d.Refused,
			"fork_policy is about forks; a branch on the repository is people with write access")
	})

	t.Run("a push", func(t *testing.T) {
		env := prEnv(forkEvent(t, dir, "stranger/shop"))
		env["GITHUB_EVENT_NAME"] = "push"
		require.False(t, gate(env).Refused)
	})

	t.Run("a workstation, with no GitHub at all", func(t *testing.T) {
		require.False(t, gate(nil).Refused)
	})

	t.Run("a base branch that says always", func(t *testing.T) {
		always := forkRepo(t, "always", "always")
		d := forkGate(&Env{WorkDir: always, Getenv: func(k string) string {
			return prEnv(forkEvent(t, always, "stranger/shop"))[k]
		}})
		require.False(t, d.Refused, "always is a policy somebody chose, and it has to be honoured")
		require.Equal(t, schema.ForkAlways, d.Policy)
	})
}

// A pull request event whose payload cannot be read is not a pull request that
// is fine. This is the arm where an implementation folding "cannot tell" into
// "not a fork" turns every unreadable payload into an allow.
func TestForkGate_FailsClosedWhenTheEventCannotBeRead(t *testing.T) {
	dir := forkRepo(t, "never", "never")

	for name, env := range map[string]map[string]string{
		"no event path": {"GITHUB_EVENT_NAME": "pull_request"},
		"a path that is not there": {
			"GITHUB_EVENT_NAME": "pull_request",
			"GITHUB_EVENT_PATH": filepath.Join(dir, "nothing.json"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			code, said := runUp(t, dir, env)
			require.Equal(t, 6, code)
			require.Contains(t, said, "AF-GH-003")
		})
	}

	t.Run("a payload that is not a pull request", func(t *testing.T) {
		path := filepath.Join(dir, "junk.json")
		require.NoError(t, os.WriteFile(path, []byte(`{"zen":"hello"}`), 0o644))
		code, said := runUp(t, dir, map[string]string{
			"GITHUB_EVENT_NAME": "pull_request", "GITHUB_EVENT_PATH": path,
		})
		require.Equal(t, 6, code)
		require.Contains(t, said, "AF-GH-003")
	})
}

// pull_request_target is the configuration this whole control matters most
// for: it hands the BASE repository's secrets to a job checking out a
// stranger's code, on purpose.
func TestForkGate_CoversPullRequestTarget(t *testing.T) {
	dir := forkRepo(t, "label", "label")
	env := prEnv(forkEvent(t, dir, "stranger/shop"))
	env["GITHUB_EVENT_NAME"] = "pull_request_target"
	code, said := runUp(t, dir, env)
	require.Equal(t, 6, code)
	require.Contains(t, said, "AF-GH-003")
}

// A shallow clone cannot produce the base branch, so the policy cannot be read
// from it. Landing on label rather than on the fork's copy is the fail-closed
// property, and saying so is what stops it being a silent one.
func TestForkGate_FallsBackToLabelWhenTheBaseBranchIsNotInTheCheckout(t *testing.T) {
	dir := forkRepo(t, "always", "always")
	env := prEnv(forkEvent(t, dir, "stranger/shop"))
	delete(env, "GITHUB_BASE_REF")

	code, said := runUp(t, dir, env)
	require.Equal(t, 6, code,
		"a base branch nobody can read must not be answered with the fork's own manifest")
	require.Contains(t, said, "antifailure:allow")
	require.Contains(t, said, "fetch-depth: 0",
		"the fallback has to say what to do about it, or it is a mystery refusal")
}

// af ci does not exit non zero, and that is deliberate, so it is pinned. What
// it does instead is write a report that cannot be read as a pass.
func TestForkGate_CIWritesAReportSayingNothingRan(t *testing.T) {
	dir := forkRepo(t, "label", "label")
	out := filepath.Join(dir, "report.md")

	var stdout, stderr bytes.Buffer
	env := prEnv(forkEvent(t, dir, "stranger/shop"))
	code := Execute(context.Background(), []string{"ci", "--report", out}, Options{
		Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader(""),
		Getenv: func(k string) string { return env[k] },
		Clock:  clock.New(), WorkDir: dir,
	})
	require.Zero(t, code,
		"a fork awaiting a maintainer is not a finding about the change, and "+
			"fork_policy: never would otherwise paint every fork pull request permanently red")

	body, err := os.ReadFile(out)
	require.NoError(t, err, "the comment is how this speaks on a pull request")
	said := strings.Join(strings.Fields(string(body)), " ")
	require.Contains(t, said, "Nothing ran.")
	require.Contains(t, said, "**This check did not run.**")
	require.Contains(t, said, "antifailure:allow")
	require.NotContains(t, said, "passed",
		"an exit code of zero is only safe while the comment cannot be read as a pass")
}

// Every door, not just the one in the workflow template.
//
// af ci is the command the template runs, and it is not the only way an
// environment is created. A gate on af ci alone would be a gate on the front
// of the building: `af up` is what a dispatch runs and what anybody writing
// their own workflow reaches for, `af test` drives the agents at whatever is
// standing, and `af load run` points traffic at it.
func TestForkGate_CoversEveryCommandThatTouchesAnEnvironment(t *testing.T) {
	dir := forkRepo(t, "never", "never")
	env := prEnv(forkEvent(t, dir, "stranger/shop"))

	for _, args := range [][]string{{"up"}, {"test"}, {"load", "run"}, {"load", "smoke"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out, errW bytes.Buffer
			code := Execute(context.Background(), args, Options{
				Stdout: &out, Stderr: &errW, Stdin: strings.NewReader(""),
				Getenv: func(k string) string { return env[k] },
				Clock:  clock.New(), WorkDir: dir,
			})
			said := strings.Join(strings.Fields(out.String()+errW.String()), " ")
			require.Equal(t, 6, code, "said: %s", said)
			require.Contains(t, said, "AF-GH-003")
		})
	}
}

// github.comment: false, which nothing consulted.
//
// The setting defaults to true, `af explain` printed "comment on", and turning
// it off left the comment exactly where it was: writeReport wrote the file
// whatever the manifest said, and the workflow's own step posts whatever it
// finds at that path.
//
// It is answered with a STEP OUTPUT rather than by suppressing the file, and
// the first version of this did suppress the file. `github-lifecycle` caught
// why that breaks: the hosted publish step builds its payload with
// `jq --rawfile markdown report.md` while being gated on
// `hashFiles('report.json')`, so a missing report.md does not skip that step,
// it makes it FAIL. `comment: false` would have turned a quiet run into a red
// one. The report is also the job summary and the payload a control plane is
// sent; the setting means do not comment, not do not produce a report.
func commentEnv(t *testing.T, dir string, actions bool) (map[string]string, string) {
	t.Helper()
	out := filepath.Join(dir, "outputs.txt")
	env := prEnv(forkEvent(t, dir, "acme/shop"))
	env["GITHUB_OUTPUT"] = out
	if actions {
		env["GITHUB_ACTIONS"] = "true"
	}
	return env, out
}

func outputs(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err, "the engine has to write the answer somewhere the workflow can read it")
	return string(body)
}

func TestComment_FalseTellsTheWorkflowNotToComment(t *testing.T) {
	dir := forkRepo(t, "always", "always")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"),
		[]byte(strings.Replace(forkManifest("always"), "  mode: actions",
			"  mode: actions\n  comment: false", 1)), 0o644))

	report := filepath.Join(dir, "report.md")
	env, out := commentEnv(t, dir, true)
	var stdout, stderr bytes.Buffer
	Execute(context.Background(), []string{"ci", "--report", report}, Options{
		Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader(""),
		Getenv: func(k string) string { return env[k] },
		Clock:  clock.New(), WorkDir: dir,
	})

	require.Contains(t, outputs(t, out), "comment=false")
	require.Contains(t, strings.Join(strings.Fields(stdout.String()+stderr.String()), " "),
		"github.comment: false",
		"a decision nobody prints is a decision nobody can debug")
}

// The report file SURVIVES comment: false. This is the assertion that pins the
// correction: suppressing it broke the hosted publish step, which reads
// report.md while being gated on report.json.
func TestComment_FalseStillLeavesTheReportOnDisk(t *testing.T) {
	dir := forkRepo(t, "never", "never")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"),
		[]byte(strings.Replace(forkManifest("never"), "  mode: actions",
			"  mode: actions\n  comment: false", 1)), 0o644))

	report := filepath.Join(dir, "report.md")
	env, _ := commentEnv(t, dir, true)
	env["GITHUB_EVENT_PATH"] = forkEvent(t, dir, "stranger/shop")
	var stdout, stderr bytes.Buffer
	Execute(context.Background(), []string{"ci", "--report", report}, Options{
		Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader(""),
		Getenv: func(k string) string { return env[k] },
		Clock:  clock.New(), WorkDir: dir,
	})

	body, err := os.ReadFile(report)
	require.NoError(t, err,
		"the report is the job summary and the control plane's payload too; "+
			"a step that reads a file somebody deleted fails rather than skipping")
	require.Contains(t, string(body), "antifailure:report")
}

// comment: true is written too, not just omitted. A key that appears only when
// it is false reads as an empty string in a workflow expression, and an empty
// string is not "true" to somebody debugging at eleven at night. This is the
// same argument writeChangeOutputs already makes for the plan.
func TestComment_TrueIsWrittenRatherThanLeftOut(t *testing.T) {
	dir := forkRepo(t, "never", "never")
	env, out := commentEnv(t, dir, true)
	env["GITHUB_EVENT_PATH"] = forkEvent(t, dir, "stranger/shop")
	var stdout, stderr bytes.Buffer
	Execute(context.Background(), []string{"ci"}, Options{
		Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader(""),
		Getenv: func(k string) string { return env[k] },
		Clock:  clock.New(), WorkDir: dir,
	})
	require.Contains(t, outputs(t, out), "comment=true")
}

// af change writes the file that gets posted when nothing else runs, and it is
// the step the workflow's condition reads, so it answers the same question.
func TestComment_AfChangeAnswersTheSameQuestion(t *testing.T) {
	dir := forkRepo(t, "always", "always")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"),
		[]byte(strings.Replace(forkManifest("always"), "  mode: actions",
			"  mode: actions\n  comment: false", 1)), 0o644))
	report := filepath.Join(dir, "report.md")
	env, out := commentEnv(t, dir, true)
	var stdout, stderr bytes.Buffer
	Execute(context.Background(), []string{"change", "--write", report}, Options{
		Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader(""),
		Getenv: func(k string) string { return env[k] },
		Clock:  clock.New(), WorkDir: dir,
	})
	require.Contains(t, outputs(t, out), "comment=false")
	_, err := os.Stat(report)
	require.NoError(t, err, "and its file survives too")
}

// Outside Actions the setting is silent, because it is about a comment on a
// pull request and there is no GITHUB_OUTPUT to answer into.
func TestComment_IsSilentOnAWorkstation(t *testing.T) {
	dir := forkRepo(t, "always", "always")
	report := filepath.Join(dir, "report.md")
	var stdout, stderr bytes.Buffer
	Execute(context.Background(), []string{"ci", "--report", report}, Options{
		Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader(""),
		Getenv: func(string) string { return "" },
		Clock:  clock.New(), WorkDir: dir,
	})
	_, err := os.Stat(report)
	require.NoError(t, err)
}
