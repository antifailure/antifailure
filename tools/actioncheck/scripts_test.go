package main

// THE STEPS IN action.yml THAT DECIDE WHETHER A CUSTOMER'S RUN CAN SAY WHAT IT
// DID, RUN.
//
// main_test.go and the command beside it read these files for SHAPE: which
// input is declared, which key is passed on, whether the copies are identical.
// Shape is worth checking and it is not what failed. What failed is two scripts
// that could not tell one answer from another:
//
//   the claim step read `.token` and kept only whether one came back, so a
//   repository nobody connected, a stopped account, a fork and a re-run the
//   control plane turned away were one empty string and one sentence, and
//
//   the publish step wrote `handled=true` whatever came back, so a report the
//   control plane refused left the check with nothing, the pull request with no
//   comment, because the fallback is skipped when this says it handled it, and
//   the job green.
//
// So these tests EXECUTE both scripts, exactly as action.yml holds them, under
// bash, against stubs standing in for GitHub's token endpoint and for the
// control plane. The text comes out of the file, so a change to the file changes
// what is tested.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type actionStep struct {
	Name string            `yaml:"name"`
	ID   string            `yaml:"id"`
	If   string            `yaml:"if"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
}

func actionSteps(t *testing.T) []actionStep {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", actionFile))
	if err != nil {
		t.Fatalf("could not read the action: %v", err)
	}
	var parsed struct {
		Runs struct {
			Steps []actionStep `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("could not parse the action: %v", err)
	}
	if len(parsed.Runs.Steps) == 0 {
		t.Fatal("action.yml declares no steps, so every test in this file would check nothing")
	}
	return parsed.Runs.Steps
}

// Found by what it talks to rather than by its name, so renaming a step does not
// quietly turn a test off.
func stepCalling(t *testing.T, steps []actionStep, endpoint string) actionStep {
	t.Helper()
	for _, s := range steps {
		if strings.Contains(s.Run, endpoint) {
			return s
		}
	}
	t.Fatalf("no step in action.yml calls %s, so a customer's run either never introduces itself "+
		"or never reports, and there is nothing here to run", endpoint)
	return actionStep{}
}

var actionOutputLine = regexp.MustCompile(`^[a-z][a-z0-9_-]*=`)

// bash, with an environment and nothing else from this machine. GITHUB_OUTPUT is
// a real file, because that is how a step output is written and reading it back
// is how the next step's condition is decided.
func runActionScript(t *testing.T, script string, env map[string]string) (int, string, string, string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "github-output")
	if err := os.WriteFile(out, nil, 0o600); err != nil {
		t.Fatalf("could not make a GITHUB_OUTPUT: %v", err)
	}
	dir := t.TempDir()
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GITHUB_OUTPUT="+out, "RUNNER_TEMP="+t.TempDir())
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	log, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("could not run the step's script: %v\n%s", err, log)
		}
		code = exit.ExitCode()
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("could not read back GITHUB_OUTPUT: %v", err)
	}
	// Every line of a step output is a key=value line. A value carrying a
	// newline needs a delimiter, and without one the line after it is read as
	// another key or thrown away. The value at risk here is a sentence from a
	// server, and no single case can know what one will say.
	for _, line := range strings.Split(strings.TrimRight(string(written), "\n"), "\n") {
		if line == "" {
			continue
		}
		if !actionOutputLine.MatchString(line) {
			t.Errorf("the step wrote %q into GITHUB_OUTPUT, which is not a key=value line", line)
		}
	}
	return code, string(log), string(written), dir
}

func actionOutput(written, key string) (string, bool) {
	for _, line := range strings.Split(written, "\n") {
		if strings.HasPrefix(line, key+"=") {
			return strings.TrimPrefix(line, key+"="), true
		}
	}
	return "", false
}

func tokenEndpoint(identity string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("audience") == "" {
			// The audience is what makes the identity worth anything: without
			// one GitHub mints a token every workflow in the organization can
			// get, so a stub that answered anyway would hide a real defect.
			http.Error(w, `{"error":"no audience"}`, http.StatusBadRequest)
			return
		}
		if identity == "" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"value":%q}`, identity)
	}))
}

func plane(path string, code int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
}

func TestTheActionsClaimStepSaysWhichSilenceThisIs(t *testing.T) {
	step := stepCalling(t, actionSteps(t), "/v1/pr/callback-token")
	const head = "c78bc279185f9ab7ddb69ffb3a6bce5f43639041"

	for _, c := range []struct {
		name        string
		runner      bool
		identity    string
		code        int
		body        string
		wantOutcome string
		wantToken   string
		wantReason  string
	}{
		{name: "a fork, where GitHub grants no identity", wantOutcome: "no-identity"},
		{name: "the variables granted and nothing minted", runner: true, wantOutcome: "no-identity"},
		{
			name: "the ordinary case", runner: true, identity: "signed-by-github",
			code: 200, body: `{"token":"callback-credential","expires_in":3600}`,
			wantOutcome: "claimed", wantToken: "callback-credential",
		},
		{
			// The shape that produced this file, on a customer's repository:
			// a claim the control plane answered no to, with a reason.
			name: "a claim the control plane turned away", runner: true, identity: "signed-by-github",
			code: 409, body: `{"error":"no check is waiting on c78bc27"}`,
			wantOutcome: "refused", wantReason: "no check is waiting",
		},
		{
			name: "an account whose billing is stopped", runner: true, identity: "signed-by-github",
			code: 409, body: `{"error":"This organization is suspended, so no credential is issued"}`,
			wantOutcome: "refused", wantReason: "suspended",
		},
		{
			// NOT the same fact, and the last step acts on the difference: a
			// customer's build is not the place to learn that somebody else's
			// service is down.
			name: "a control plane that is broken", runner: true, identity: "signed-by-github",
			code: 503, body: `{"error":"no GitHub App configured"}`,
			wantOutcome: "unreachable", wantReason: "no GitHub App",
		},
		{
			name: "a control plane that is not listening at all", runner: true, identity: "signed-by-github",
			code: 0, wantOutcome: "unreachable",
			// Exactly three zeroes and nothing after them: curl writes `000`
			// AND exits non zero when it could not connect, so a fallback that
			// wrote a code of its own too would produce a reason with a newline
			// in it and a step output GitHub cannot read.
			wantReason: "answered HTTP 000 and",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			actions := tokenEndpoint(c.identity)
			defer actions.Close()
			cp := plane("/v1/pr/callback-token", c.code, c.body)
			address := cp.URL
			if c.code == 0 {
				cp.Close() // an address nothing answers on, not one nothing bound
			} else {
				defer cp.Close()
			}

			env := map[string]string{"AF_CONTROL_PLANE": address, "HEAD_SHA": head}
			if c.runner {
				env["ACTIONS_ID_TOKEN_REQUEST_TOKEN"] = "runner-request-token"
				env["ACTIONS_ID_TOKEN_REQUEST_URL"] = actions.URL + "/oidc?x=1"
			}
			exit, log, written, dir := runActionScript(t, step.Run, env)

			// NEVER RED, WHATEVER HAPPENED. This step runs before the work, in
			// somebody else's repository: ending the job here would turn an
			// outage of ours into an unrunnable check of theirs.
			if exit != 0 {
				t.Errorf("the claim step exited %d, so a control plane that answered %d ends a "+
					"customer's job before their check has run:\n%s", exit, c.code, log)
			}
			got, ok := actionOutput(written, "outcome")
			if !ok {
				t.Fatalf("the claim step recorded no outcome, so nothing downstream can tell a "+
					"fork from a refusal:\n%s", log)
			}
			if got != c.wantOutcome {
				t.Errorf("the claim step recorded outcome %q, want %q:\n%s", got, c.wantOutcome, log)
			}
			token, has := actionOutput(written, "token")
			if c.wantToken == "" && has {
				t.Errorf("a token %q was recorded when none was issued, so the publish step will "+
					"present a credential nobody granted", token)
			}
			if c.wantToken != "" && token != c.wantToken {
				t.Errorf("recorded token %q, want %q", token, c.wantToken)
			}
			reason, _ := actionOutput(written, "reason")
			if c.wantReason != "" && !strings.Contains(reason, c.wantReason) {
				t.Errorf("the recorded reason %q does not name %q, so the run cannot say why "+
					"nothing was reported:\n%s", reason, c.wantReason, log)
			}
			if strings.Contains(reason, "\n") {
				t.Errorf("the recorded reason carries a newline, which a bare step output cannot "+
					"express: %q", reason)
			}
			// THE CREDENTIAL DOES NOT LAND IN THE WORKSPACE. Reading the HTTP
			// status means keeping the body, the body holds the credential, and
			// the workspace is what an upload step publishes.
			if c.wantToken != "" {
				entries, _ := os.ReadDir(dir)
				for _, e := range entries {
					body, err := os.ReadFile(filepath.Join(dir, e.Name()))
					if err == nil && strings.Contains(string(body), c.wantToken) {
						t.Errorf("the claim step left the callback credential in %s, which on a "+
							"runner is the workspace", e.Name())
					}
				}
			}
		})
	}
}

func TestTheActionsPublishStepCannotSwallowARefusal(t *testing.T) {
	steps := actionSteps(t)
	step := stepCalling(t, steps, "/v1/pr/report")
	const head = "c78bc279185f9ab7ddb69ffb3a6bce5f43639041"

	for _, c := range []struct {
		name        string
		code        int
		body        string
		wantExit    int
		wantHandled bool
		wantSaid    string
	}{
		{
			name: "the report lands", code: 200, body: `{"recorded":true,"state":"passed"}`,
			wantExit: 0, wantHandled: true,
		},
		{
			// A CREDENTIAL IN HAND AND A VERDICT NOBODY HEARD. The check on the
			// commit is waiting for exactly this, and until the status was read
			// this was a green step: handled=true was written anyway, so the
			// fallback comment was skipped too and the run said nothing anywhere.
			name: "the control plane refuses the report", code: 409,
			body:     `{"error":"a1b2c3d already has a result: passed."}`,
			wantExit: 1, wantHandled: false, wantSaid: "already has a result",
		},
		{
			name: "the credential is not one this control plane issued", code: 409,
			body:     `{"error":"That credential does not belong to any commit this control plane is waiting on."}`,
			wantExit: 1, wantHandled: false, wantSaid: "does not belong",
		},
		{
			// Not the customer's problem and not their red build. The report is
			// on the pull request as a comment, because handled stays unset.
			name: "the control plane is broken", code: 503, body: `{"error":"down"}`,
			wantExit: 0, wantHandled: false, wantSaid: "::warning::",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			cp := plane("/v1/pr/report", c.code, c.body)
			defer cp.Close()
			dir := t.TempDir()
			report := filepath.Join(dir, "report.md")
			if err := os.WriteFile(report, []byte("### Antifailure: it ran\n"), 0o600); err != nil {
				t.Fatalf("could not write a report: %v", err)
			}
			// The step reads report.json out of the working directory, the way
			// `af ci --report-json report.json` leaves it.
			script := "printf '%s' '{\"Workflows\":[{\"Name\":\"read\",\"Verdict\":\"pass\"}]}' > report.json\n" + step.Run
			exit, log, written, _ := runActionScript(t, script, map[string]string{
				"AF_CONTROL_PLANE": cp.URL,
				"AF_REPORT":        report,
				"TOKEN":            "callback-credential",
				"HEAD_SHA":         head,
			})
			if exit != c.wantExit {
				t.Errorf("a report the control plane answered %d with exited %d, want %d:\n%s",
					c.code, exit, c.wantExit, log)
			}
			handled, _ := actionOutput(written, "handled")
			if c.wantHandled && handled != "true" {
				t.Errorf("a delivered report recorded handled=%q, so the fallback comment writes a "+
					"second copy of a report the control plane already published", handled)
			}
			if !c.wantHandled && handled == "true" {
				t.Errorf("a report the control plane answered %d with recorded handled=true, so "+
					"the fallback comment is skipped and this run said nothing anywhere", c.code)
			}
			if c.wantSaid != "" && !strings.Contains(log, c.wantSaid) {
				t.Errorf("the log never says %q, so a reader cannot tell what happened:\n%s",
					c.wantSaid, log)
			}
			if c.wantExit != 0 && !strings.Contains(log, "::error::") {
				t.Errorf("the step failed with no ::error:: annotation, so the failure is a line "+
					"in a log nobody opens:\n%s", log)
			}
			if strings.Contains(log, "callback-credential") {
				t.Errorf("the step printed the credential into its own log:\n%s", log)
			}
		})
	}
}

// The steps are wired to each other, which neither test above can see.
func TestTheActionsReportingStepsAreWiredToEachOther(t *testing.T) {
	steps := actionSteps(t)
	claim := stepCalling(t, steps, "/v1/pr/callback-token")
	publish := stepCalling(t, steps, "/v1/pr/report")
	if claim.ID == "" {
		t.Fatal("the claim step has no id, so nothing can read the credential it recorded")
	}
	if publish.ID == "" {
		t.Fatal("the publish step has no id, so whether it delivered cannot be read")
	}
	if !strings.Contains(publish.If, "steps."+claim.ID+".outputs.token") {
		t.Errorf("the publish step is not guarded on the credential the claim step recorded: %q",
			publish.If)
	}

	// The fallback comment, which is what a run with no credential has instead
	// of a check, must be skipped only when the control plane really took the
	// report. Guarded on `handled`, which is now written on a 200 alone.
	var comment actionStep
	for _, s := range steps {
		if strings.Contains(s.Run, "antifailure:report") {
			comment = s
		}
	}
	if comment.Run == "" {
		t.Fatal("no step leaves the report on the pull request, so a run whose report the control " +
			"plane would not take has nowhere to say what it found")
	}
	if !strings.Contains(comment.If, "steps."+publish.ID+".outputs.handled != 'true'") {
		t.Errorf("the fallback comment is not guarded on the publish step having actually "+
			"delivered (%q), so a refused report leaves the pull request with nothing at all",
			comment.If)
	}

	// And the outcome the claim step records is READ. A recorded value nothing
	// consumes is the shape of this whole defect one layer up.
	last := steps[len(steps)-1]
	if !strings.Contains(last.Env["OUTCOME"], "steps."+claim.ID+".outputs.outcome") {
		t.Errorf("the last step of the action does not read the claim step's outcome (%q), so "+
			"which silence a run hit is recorded and never said", last.Env["OUTCOME"])
	}
	if !strings.Contains(last.If, "always()") {
		t.Errorf("the step that says what the control plane knows is not always(), so the runs "+
			"worth hearing it about skip it: %q", last.If)
	}
}

// The last step only SAYS. It runs in somebody else's repository under
// `set -euo pipefail`, so an unset variable or a branch that exits non zero
// would fail a customer's build over what is meant to be a sentence. Every
// outcome the claim step can record is driven through it here, together with
// the one it records when it did not run at all.
func TestTheActionsLastStepSaysWhatItKnowsAndNeverFailsTheBuild(t *testing.T) {
	steps := actionSteps(t)
	last := steps[len(steps)-1]
	for _, key := range []string{"OUTCOME", "REASON", "DELIVERED", "HEAD_SHA", "ON_A_PULL_REQUEST"} {
		if _, ok := last.Env[key]; !ok {
			t.Fatalf("the last step declares no %s, so this test would drive a script that reads "+
				"something else and prove nothing about it", key)
		}
	}
	const reason = "no check is waiting on c78bc27"

	for _, c := range []struct {
		name        string
		outcome     string
		delivered   string
		pullRequest bool
		wantSaid    string
		wantWarning bool
	}{
		{name: "claimed and delivered", outcome: "claimed", delivered: "true", pullRequest: true,
			wantSaid: "has this run's verdict"},
		{name: "claimed and delivered nothing", outcome: "claimed", pullRequest: true,
			wantSaid: "delivered no report", wantWarning: true},
		{name: "a fork", outcome: "no-identity", pullRequest: true,
			wantSaid: "nothing here is wrong"},
		{name: "refused", outcome: "refused", pullRequest: true,
			wantSaid: reason, wantWarning: true},
		{name: "unreachable", outcome: "unreachable", pullRequest: true,
			wantSaid: reason, wantWarning: true},
		{name: "no outcome on a pull request", pullRequest: true,
			wantSaid: "recorded no outcome", wantWarning: true},
		{name: "no outcome off a pull request"},
	} {
		t.Run(c.name, func(t *testing.T) {
			exit, log, _, _ := runActionScript(t, last.Run, map[string]string{
				"OUTCOME":           c.outcome,
				"REASON":            reason,
				"DELIVERED":         c.delivered,
				"HEAD_SHA":          "c78bc279185f9ab7ddb69ffb3a6bce5f43639041",
				"ON_A_PULL_REQUEST": fmt.Sprint(c.pullRequest),
			})
			if exit != 0 {
				t.Errorf("outcome %q exited %d, so a sentence about the control plane failed a "+
					"customer's build:\n%s", c.outcome, exit, log)
			}
			if strings.Contains(log, "::error::") {
				t.Errorf("outcome %q wrote an ::error:: annotation from a step that only says:\n%s",
					c.outcome, log)
			}
			if c.wantSaid != "" && !strings.Contains(log, c.wantSaid) {
				t.Errorf("outcome %q never says %q:\n%s", c.outcome, c.wantSaid, log)
			}
			if got := strings.Contains(log, "::warning::"); got != c.wantWarning {
				t.Errorf("outcome %q wrote a warning: %v, want %v:\n%s", c.outcome, got, c.wantWarning, log)
			}
		})
	}
}
