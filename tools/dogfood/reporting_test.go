package main

// THE STEPS IN dogfood.yml THAT DECIDE WHETHER ANYTHING WAS VERIFIED, RUN.
//
// Every other test in this package that reads the workflow reads it for shape:
// which permission is declared, which endpoint appears, which step comes
// first. Shape is worth checking and it is not what failed here. On pull
// requests 224, 225 and 227 the workflow contained every word those tests look
// for, and re-running it still produced:
//
//	dogfood, against the control plane: success
//	  Tell the control plane this run is the one checking this commit: success
//	  The whole check, the way a customer runs it: success
//	  Tell the control plane what this commit did: SKIPPED
//
// with the check on the commit reading "Nothing was verified". The claim step
// kept only whether a token came back, so a control plane that turned the run
// away was indistinguishable from a fork, and the report step's guard skipped
// on both. A test that the file mentions `/v1/pr/callback-token` cannot see
// that, and neither can one that the file mentions `outcome`.
//
// So these tests EXECUTE the two scripts, exactly as the file holds them, under
// bash, against a stub standing in for GitHub's token endpoint and for the
// control plane. Nothing here is a copy of the script: the text comes out of
// .github/workflows/dogfood.yml, so a change to the file changes what is
// tested.

import (
	"errors"
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

type workflowStep struct {
	Name string            `yaml:"name"`
	ID   string            `yaml:"id"`
	If   string            `yaml:"if"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
}

type workflowJob struct {
	Env   map[string]string `yaml:"env"`
	Steps []workflowStep    `yaml:"steps"`
}

// The job in dogfood.yml that runs the check on a pull request, found the way
// the other tests here find it: by what it does rather than by its key.
func pullRequestJob(t *testing.T) workflowJob {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "dogfood.yml"))
	if err != nil {
		t.Fatalf("could not read the workflow: %v", err)
	}
	var workflow struct {
		Jobs map[string]workflowJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatalf("could not parse the workflow: %v", err)
	}
	for _, job := range workflow.Jobs {
		for _, s := range job.Steps {
			if strings.Contains(s.Run, "tools/dogfood") && strings.Contains(s.Run, "--mode pr") {
				return job
			}
		}
	}
	t.Fatal("no job in dogfood.yml runs the harness in pull request mode, so every test in " +
		"this file checked nothing. Either the job was renamed out from under them or the " +
		"check is gone")
	return workflowJob{}
}

// The step that trades a workflow identity for a callback credential.
func claimStep(t *testing.T, job workflowJob) workflowStep {
	t.Helper()
	for _, s := range job.Steps {
		if strings.Contains(s.Run, "/v1/pr/callback-token") {
			return s
		}
	}
	t.Fatal("no step in the pull request job asks for a callback credential, so the control " +
		"plane is never told which run is checking the commit and there is nothing here to run")
	return workflowStep{}
}

// The step that decides whether this run is allowed to be green.
//
// Found by what it reads rather than by its name: the step whose environment
// takes the claim step's recorded outcome. A step that stopped reading it would
// not be found, and not being found is a hard failure below rather than a test
// that quietly checks nothing.
func verdictStep(t *testing.T, job workflowJob) workflowStep {
	t.Helper()
	var found []workflowStep
	for _, s := range job.Steps {
		if strings.Contains(s.Env["OUTCOME"], ".outputs.outcome") {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one step in the pull request job to read the claim step's "+
			"recorded outcome and decide the job on it, found %d. Without it a run that could "+
			"not report is green and silent, which is the defect this file exists for", len(found))
	}
	return found[0]
}

// bash, with an environment and nothing else from this machine.
//
// GITHUB_OUTPUT is a real file, because that is how a step output is written
// and reading it back is how the next step's condition is decided.
func runScript(t *testing.T, script string, env map[string]string) (int, string, string, string) {
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
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("could not run the step's script: %v\n%s", err, log)
		}
		code = exit.ExitCode()
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("could not read back GITHUB_OUTPUT: %v", err)
	}

	// EVERY LINE OF A STEP OUTPUT IS A `key=value` LINE.
	//
	// GitHub reads this file line by line, so a value carrying a newline needs
	// a heredoc delimiter and one written bare leaves an orphan line that is
	// either dropped or read as another key. Checked here rather than in one
	// case, because the value at risk is an error message from a server and no
	// single case can know what one will say.
	for _, line := range strings.Split(strings.TrimRight(string(written), "\n"), "\n") {
		if line == "" {
			continue
		}
		if !outputLine.MatchString(line) {
			t.Errorf("the step wrote %q into GITHUB_OUTPUT, which is not a `key=value` line. A "+
				"value carrying a newline needs a delimiter, and without one the line after it "+
				"is read as a key or thrown away", line)
		}
	}
	return code, string(log), string(written), dir
}

var outputLine = regexp.MustCompile(`^[a-z][a-z0-9_-]*=`)

// Everything the step left in the directory it ran in, which on a runner is
// the workspace the artifact step uploads.
func leftBehind(t *testing.T, dir string) string {
	t.Helper()
	var all strings.Builder
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("could not read the working directory back: %v", err)
	}
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		all.WriteString(e.Name())
		all.WriteString("\n")
		all.Write(body)
		all.WriteString("\n")
	}
	return all.String()
}

// One `key=value` line out of a step's recorded output.
func output(written, key string) (string, bool) {
	for _, line := range strings.Split(written, "\n") {
		if strings.HasPrefix(line, key+"=") {
			return strings.TrimPrefix(line, key+"="), true
		}
	}
	return "", false
}

// GitHub's token endpoint, which is the thing the claim step asks to prove
// what this job is.
func actionsTokenEndpoint(identity string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("audience") == "" {
			// The audience is what makes the identity worth anything. Without
			// one GitHub mints a token every workflow in the organization can
			// obtain, so a stub that answered anyway would hide a real defect.
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

// The control plane, answering the exchange however this case needs.
func controlPlane(code int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/pr/callback-token" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
}

// ---------------------------------------------------------------------------
// The claim step
// ---------------------------------------------------------------------------

func TestTheClaimStepSaysWhichSilenceThisIs(t *testing.T) {
	step := claimStep(t, pullRequestJob(t))
	const head = "c78bc279185f9ab7ddb69ffb3a6bce5f43639041"

	for _, c := range []struct {
		name string
		// Whether GitHub granted this job an identity at all.
		runnerIdentity bool
		// What GitHub's token endpoint returns as the identity, empty for none.
		identity string
		// What the control plane answers the exchange with. code 0 means the
		// control plane is not listening at all.
		code int
		body string

		wantOutcome string
		wantToken   string
		wantReason  string
	}{
		{
			// A FORK, WHICH MUST NOT GO RED. GitHub sets neither runner
			// variable on a pull request from a fork, deliberately, and an
			// outside contributor can fix nothing about it.
			name:        "a fork, where GitHub grants no identity",
			wantOutcome: "no-identity",
		},
		{
			name:           "GitHub grants the variables and then mints nothing",
			runnerIdentity: true,
			identity:       "",
			wantOutcome:    "no-identity",
		},
		{
			name:           "the ordinary case",
			runnerIdentity: true,
			identity:       "signed-by-github",
			code:           200,
			body:           `{"token":"callback-credential","expires_in":3600}`,
			wantOutcome:    "claimed",
			wantToken:      "callback-credential",
		},
		{
			// THE CASE THAT PRODUCED THIS FILE. Run 33927593837 was re-run,
			// and the control plane answered this because the generation for
			// c78bc27 had already concluded. The claim step read it as the fork
			// case above and the job went green having verified nothing.
			name:           "a re-run the control plane turned away",
			runnerIdentity: true,
			identity:       "signed-by-github",
			code:           409,
			body:           `{"error":"the check on c78bc27 is already unverified"}`,
			wantOutcome:    "refused",
			wantReason:     "already unverified",
		},
		{
			name:           "a commit with no check waiting on it",
			runnerIdentity: true,
			identity:       "signed-by-github",
			code:           409,
			body:           `{"error":"no check is waiting on c78bc27"}`,
			wantOutcome:    "refused",
			wantReason:     "no check is waiting",
		},
		{
			name:           "a control plane with no GitHub App configured",
			runnerIdentity: true,
			identity:       "signed-by-github",
			code:           503,
			body:           `{"error":"This control plane has no GitHub App configured."}`,
			wantOutcome:    "refused",
			wantReason:     "no GitHub App",
		},
		{
			name:           "a control plane that answers with no reason at all",
			runnerIdentity: true,
			identity:       "signed-by-github",
			code:           500,
			body:           `nope`,
			wantOutcome:    "refused",
			wantReason:     "HTTP 500",
		},
		{
			name:           "a control plane that is not listening",
			runnerIdentity: true,
			identity:       "signed-by-github",
			code:           0,
			wantOutcome:    "refused",
			// The words either side matter. curl writes `000` as the code
			// when it could not connect AND exits non zero, so a fallback
			// that wrote one too would produce `000 000` here, a reason with
			// a newline in it, and a step output GitHub cannot read. This
			// says the code is exactly three zeroes and nothing else.
			wantReason: "answered HTTP 000 and",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Two separate stubs, because the case where the control plane
			// is not listening still needs GitHub to grant an identity: the
			// exchange is only reached once one exists.
			actions := actionsTokenEndpoint(c.identity)
			defer actions.Close()

			plane := controlPlane(c.code, c.body)
			address := plane.URL
			if c.code == 0 {
				// Closed rather than never started, so the address is one
				// nothing answers on rather than one nothing has bound.
				plane.Close()
			} else {
				defer plane.Close()
			}

			env := map[string]string{
				"AF_CONTROL_PLANE": address,
				"HEAD_SHA":         head,
			}
			if c.runnerIdentity {
				env["ACTIONS_ID_TOKEN_REQUEST_TOKEN"] = "runner-request-token"
				env["ACTIONS_ID_TOKEN_REQUEST_URL"] = actions.URL + "/oidc?x=1"
			}

			exit, log, written, dir := runScript(t, step.Run, env)

			// NEVER RED, WHATEVER HAPPENED. The observability of a run is not
			// the run, and this step runs before the work: ending the job here
			// would turn a control plane outage into an unrunnable check.
			if exit != 0 {
				t.Errorf("the claim step exited %d, so a control plane that answered %d ends "+
					"the job before the check has run:\n%s", exit, c.code, log)
			}

			got, ok := output(written, "outcome")
			if !ok {
				t.Fatalf("the claim step recorded no outcome, so the step that decides whether "+
					"this run may be green has nothing to read and cannot tell a fork from a "+
					"refusal:\n%s\n%s", log, written)
			}
			if got != c.wantOutcome {
				t.Errorf("the claim step recorded outcome %q, want %q:\n%s", got, c.wantOutcome, log)
			}

			token, hasToken := output(written, "token")
			if c.wantToken == "" && hasToken {
				t.Errorf("the claim step recorded a token %q when the control plane issued none, "+
					"so the report step will present a credential nobody granted", token)
			}
			if c.wantToken != "" && token != c.wantToken {
				t.Errorf("the claim step recorded token %q, want %q", token, c.wantToken)
			}

			reason, _ := output(written, "reason")
			if c.wantReason != "" && !strings.Contains(reason, c.wantReason) {
				t.Errorf("the claim step recorded the reason %q, which does not name %q, so the "+
					"job's own error message cannot say why nothing was reported:\n%s",
					reason, c.wantReason, log)
			}
			// A reason has to survive being a step output. A newline in one
			// needs a delimiter, and a value that can carry the delimiter can
			// write its own lines into GITHUB_OUTPUT.
			if strings.Contains(reason, "\n") {
				t.Errorf("the recorded reason carries a newline, which a bare key=value step "+
					"output cannot express: %q", reason)
			}
			if c.wantOutcome == "refused" && !strings.Contains(log, "no callback credential") {
				t.Errorf("the log of a refused claim does not say a credential was refused, so a "+
					"reader of the job has to infer it from a skipped step:\n%s", log)
			}

			// THE CREDENTIAL DOES NOT LAND IN THE WORKSPACE.
			//
			// The step's own comment says the token is written to the step
			// output rather than to a file, because the workspace is what the
			// artifact step at the end of this job uploads and a published
			// artifact is a published credential. Reading the exchange's HTTP
			// status means keeping its body somewhere, and somewhere has to be
			// outside the directory that gets uploaded.
			if c.wantToken != "" {
				if left := leftBehind(t, dir); strings.Contains(left, c.wantToken) {
					t.Errorf("the claim step left the callback credential in the directory it "+
						"ran in, which on a runner is the workspace the artifact step "+
						"uploads:\n%s", left)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The step that decides whether the job may be green
// ---------------------------------------------------------------------------

func TestARunThatReportedNothingIsNotAPass(t *testing.T) {
	job := pullRequestJob(t)
	step := verdictStep(t, job)

	// Every variable this test sets has to be one the step actually declares,
	// or the test is configuring a script that reads something else and every
	// case below passes for the wrong reason.
	for _, key := range []string{"OUTCOME", "REASON", "REPORT", "ON_A_PULL_REQUEST"} {
		if _, ok := step.Env[key]; !ok {
			t.Fatalf("the step that decides the job declares no %s, so this test would drive a "+
				"script that reads something else and prove nothing about it", key)
		}
		if !strings.Contains(step.Run, key) {
			t.Fatalf("the step that decides the job declares %s and never reads it", key)
		}
	}
	// And it has to be reached on a run that failed, or the whole point is lost:
	// the run this exists for is one where an earlier step went wrong.
	if !strings.Contains(step.If, "always()") {
		t.Errorf("the step that decides the job is not `always()`, so a job that failed earlier "+
			"skips the one step that says nothing was verified: %q", step.If)
	}

	for _, c := range []struct {
		name          string
		outcome       string
		report        string
		pullRequest   bool
		wantExit      int
		wantMentioned string
	}{
		{
			name:        "claimed and reported",
			outcome:     "claimed",
			report:      "success",
			pullRequest: true,
			wantExit:    0,
		},
		{
			// THE EXACT SHAPE OF THE FAILING RUN. A credential in hand and the
			// report step skipped, which is what a guard that cannot tell a
			// fork from a refusal produces one layer down.
			name:          "claimed and the report step skipped anyway",
			outcome:       "claimed",
			report:        "skipped",
			pullRequest:   true,
			wantExit:      1,
			wantMentioned: "skipped",
		},
		{
			name:          "claimed and the report was refused",
			outcome:       "claimed",
			report:        "failure",
			pullRequest:   true,
			wantExit:      1,
			wantMentioned: "failure",
		},
		{
			// THE ONE CASE THAT STAYS GREEN. Nobody outside this repository can
			// grant themselves a workflow identity, and GitHub withholds it
			// from a fork on purpose.
			name:        "a fork, which nobody can fix",
			outcome:     "no-identity",
			report:      "skipped",
			pullRequest: true,
			wantExit:    0,
		},
		{
			name:          "refused on a pull request, where a check was waiting",
			outcome:       "refused",
			report:        "skipped",
			pullRequest:   true,
			wantExit:      1,
			wantMentioned: "the check on c78bc27 is already unverified",
		},
		{
			// The nightly schedule and a manual dispatch run against a commit
			// with no pull request, so "no check is waiting on this commit" is
			// the right answer rather than a defect.
			name:        "refused off a pull request, where no check was waiting",
			outcome:     "refused",
			report:      "skipped",
			pullRequest: false,
			wantExit:    0,
		},
		{
			// A claim step that did not run at all, which is what a renamed id
			// or a step that died in its first line looks like from here.
			// Unknown is not a pass.
			name:          "no outcome recorded at all",
			outcome:       "",
			report:        "skipped",
			pullRequest:   true,
			wantExit:      1,
			wantMentioned: "no outcome",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			exit, log, _, _ := runScript(t, step.Run, map[string]string{
				"OUTCOME":           c.outcome,
				"REASON":            "the check on c78bc27 is already unverified",
				"REPORT":            c.report,
				"HEAD_SHA":          "c78bc279185f9ab7ddb69ffb3a6bce5f43639041",
				"ON_A_PULL_REQUEST": fmt.Sprint(c.pullRequest),
			})
			if exit != c.wantExit {
				t.Errorf("outcome %q with the report step %q exited %d, want %d:\n%s",
					c.outcome, c.report, exit, c.wantExit, log)
			}
			if c.wantExit != 0 && !strings.Contains(log, "::error::") {
				t.Errorf("outcome %q failed the job without an ::error:: annotation, so the "+
					"failure is a line in a log nobody opens:\n%s", c.outcome, log)
			}
			if c.wantMentioned != "" && !strings.Contains(log, c.wantMentioned) {
				t.Errorf("outcome %q with the report step %q never says %q, so a reader cannot "+
					"tell why the job is red:\n%s", c.outcome, c.report, c.wantMentioned, log)
			}
		})
	}
}

// The two halves are actually wired to each other.
//
// Both scripts above are proved on their own. Nothing in either proves the
// report step is guarded on the credential the claim step recorded, or that the
// deciding step reads the report step's own outcome, and a rename on either
// side would leave both tests green and the job silent again.
func TestTheStepsThatDecideTheJobAreWiredToEachOther(t *testing.T) {
	job := pullRequestJob(t)
	claim := claimStep(t, job)
	verdict := verdictStep(t, job)

	if claim.ID == "" {
		t.Fatal("the claim step has no id, so nothing can read what it recorded and the report " +
			"step cannot be guarded on the credential")
	}

	var report workflowStep
	for _, s := range job.Steps {
		if strings.Contains(s.Run, "/v1/pr/report") {
			report = s
		}
	}
	if report.Run == "" {
		t.Fatal("no step posts to /v1/pr/report, so the whole product runs and the check on the " +
			"commit hears nothing")
	}
	if report.ID == "" {
		t.Error("the report step has no id, so whether it ran cannot be read and a skipped " +
			"report is indistinguishable from a delivered one")
	}
	if !strings.Contains(report.If, "steps."+claim.ID+".outputs.token") {
		t.Errorf("the report step is not guarded on the credential the claim step recorded "+
			"(`%s`), so it either presents a credential nobody granted or is guarded on "+
			"something else entirely", report.If)
	}
	if !strings.Contains(verdict.Env["OUTCOME"], "steps."+claim.ID+".outputs.outcome") {
		t.Errorf("the deciding step does not read the claim step's outcome, so it cannot tell a "+
			"fork from a refusal: %q", verdict.Env["OUTCOME"])
	}
	if !strings.Contains(verdict.Env["REPORT"], "steps."+report.ID+".outcome") {
		t.Errorf("the deciding step does not read the report step's outcome (`%s`), so a report "+
			"step that skipped is indistinguishable from one that landed",
			verdict.Env["REPORT"])
	}
	// The deciding step LAST. Everything it reads is a fact only once the
	// report step has had its turn, and a step that ran before it would read
	// an empty outcome and fail every run.
	last := job.Steps[len(job.Steps)-1]
	if last.Name != verdict.Name {
		t.Errorf("the step that decides the job is not the last one in it (%q is), so it reads "+
			"the report step's outcome before the report step has had its turn", last.Name)
	}
}
