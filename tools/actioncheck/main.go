// Command actioncheck verifies that the three published GitHub surfaces agree
// with each other and with every copy of the customer's workflow file.
//
// It exists because the three files are read by three different parties and
// nothing else connects them. A customer's repository holds
// examples/github-workflow.yml, which calls .github/workflows/check.yml, which
// calls action.yml. GitHub reads each one at a different moment: the customer's
// file when a pull request opens, the reusable workflow when the job is
// scheduled, the action when the step runs. A `with:` key that one of them
// passes and the next does not declare is refused by GitHub at that moment,
// on the customer's pull request, with a message that names our file rather
// than theirs. No compiler and no test in engine/ or web/ can see it, because
// none of them parse YAML that GitHub interprets.
//
// The customer's file also exists as two copies, one embedded in the engine so
// `af init` can write it and one embedded in the control plane so the App can
// open a pull request adding it. A copy that drifts is the file a customer
// gets, so the copies have to be byte identical to the example the
// documentation shows, and that is checked here rather than in two tests in
// two languages that could each be deleted alone.
//
// The workflow_dispatch input names are checked against a fixed list because
// the hosted control plane sends exactly those names, GitHub answers 422
// "Unexpected inputs provided" to any other, and web/apps/api/src/routers
// names them in the sentence it shows a customer when that happens.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	actionFile   = "action.yml"
	reusableFile = ".github/workflows/check.yml"
	exampleFile  = "examples/github-workflow.yml"
)

// copies are the embedded copies of the example, each of which must equal it
// byte for byte. The example is the source of truth because it is the one
// the documentation renders.
var copies = []string{
	"engine/internal/cli/templates/antifailure.yml",
	"web/apps/api/src/github/setup/antifailure.yml",
}

// dispatchInputs is what the hosted control plane sends. See
// web/apps/api/src/routers/dispatch.ts and workloads/bodies.ts.
var dispatchInputs = []string{"command", "workflows", "duration", "scale", "seed", "concurrency", "run_id"}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	problems, err := check(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "actioncheck:", err)
		os.Exit(2)
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, p)
		}
		fmt.Fprintf(os.Stderr, "\n%d problem(s). The three GitHub surfaces no longer agree.\n", len(problems))
		os.Exit(1)
	}
	fmt.Println("actioncheck: action.yml, check.yml and the example agree, and every copy of the example is identical")
}

type action struct {
	Inputs  map[string]any `yaml:"inputs"`
	Outputs map[string]any `yaml:"outputs"`
	Runs    struct {
		Using string `yaml:"using"`
		Steps []step `yaml:"steps"`
	} `yaml:"runs"`
}

type step struct {
	ID   string            `yaml:"id"`
	Uses string            `yaml:"uses"`
	With map[string]string `yaml:"with"`
	Run  string            `yaml:"run"`
	If   string            `yaml:"if"`
}

type workflow struct {
	On struct {
		WorkflowCall *struct {
			Inputs map[string]any `yaml:"inputs"`
		} `yaml:"workflow_call"`
		WorkflowDispatch *struct {
			Inputs map[string]any `yaml:"inputs"`
		} `yaml:"workflow_dispatch"`
		PullRequest any `yaml:"pull_request"`
	} `yaml:"on"`
	Jobs map[string]job `yaml:"jobs"`
}

type job struct {
	Uses    string            `yaml:"uses"`
	With    map[string]string `yaml:"with"`
	Secrets any               `yaml:"secrets"`
	Steps   []step            `yaml:"steps"`
}

var (
	inputRef = regexp.MustCompile(`inputs(?:\.([A-Za-z0-9_-]+)|\['([A-Za-z0-9_-]+)'\])`)
	stepRef  = regexp.MustCompile(`steps\.([A-Za-z0-9_-]+)\.outputs`)
	usesRef  = regexp.MustCompile(`^antifailure/antifailure(/[^@]*)?@(.+)$`)
)

func check(root string) ([]string, error) {
	var problems []string
	say := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	actionBytes, err := os.ReadFile(filepath.Join(root, actionFile))
	if err != nil {
		return nil, err
	}
	var act action
	if err := yaml.Unmarshal(actionBytes, &act); err != nil {
		return nil, fmt.Errorf("%s: %w", actionFile, err)
	}
	if act.Runs.Using != "composite" {
		say("%s: runs.using is %q, and only a composite action can be called by check.yml", actionFile, act.Runs.Using)
	}
	if len(act.Inputs) == 0 {
		say("%s: declares no inputs", actionFile)
	}

	// Every expression inside the action names an input it declares and a
	// step that exists. A typo here is a GitHub error on a customer's run.
	ids := map[string]bool{}
	for _, s := range act.Runs.Steps {
		if s.ID != "" {
			ids[s.ID] = true
		}
	}
	for _, m := range inputRef.FindAllStringSubmatch(string(actionBytes), -1) {
		name := m[1] + m[2]
		if _, ok := act.Inputs[name]; !ok {
			say("%s: references inputs.%s, which it does not declare", actionFile, name)
		}
	}
	for _, m := range stepRef.FindAllStringSubmatch(string(actionBytes), -1) {
		if !ids[m[1]] {
			say("%s: references steps.%s.outputs, and no step has that id", actionFile, m[1])
		}
	}

	reusableBytes, err := os.ReadFile(filepath.Join(root, reusableFile))
	if err != nil {
		return nil, err
	}
	var reusable workflow
	if err := yaml.Unmarshal(reusableBytes, &reusable); err != nil {
		return nil, fmt.Errorf("%s: %w", reusableFile, err)
	}
	if reusable.On.WorkflowCall == nil {
		say("%s: has no workflow_call trigger, so the example cannot call it", reusableFile)
	}
	var actionRef string
	var actionStep *step
	for _, j := range reusable.Jobs {
		for i := range j.Steps {
			s := j.Steps[i]
			m := usesRef.FindStringSubmatch(s.Uses)
			if m == nil || m[1] != "" {
				continue
			}
			actionStep = &j.Steps[i]
			actionRef = m[2]
		}
	}
	if actionStep == nil {
		say("%s: no step uses antifailure/antifailure@<ref>, so nothing runs the action", reusableFile)
	} else {
		for k := range actionStep.With {
			if _, ok := act.Inputs[k]; !ok {
				say("%s: passes with.%s to the action, which %s does not declare", reusableFile, k, actionFile)
			}
		}
		// Every input the reusable workflow accepts has to reach the action,
		// or a customer setting it would be setting nothing.
		withText := strings.Join(values(actionStep.With), "\n")
		if reusable.On.WorkflowCall != nil {
			for name := range reusable.On.WorkflowCall.Inputs {
				if !inputMentioned(withText, name) {
					say("%s: accepts input %q and never passes it to the action", reusableFile, name)
				}
			}
		}
	}
	for _, m := range inputRef.FindAllStringSubmatch(string(reusableBytes), -1) {
		name := m[1] + m[2]
		if reusable.On.WorkflowCall == nil {
			break
		}
		if _, ok := reusable.On.WorkflowCall.Inputs[name]; !ok {
			say("%s: references inputs.%s, which it does not declare", reusableFile, name)
		}
	}

	exampleBytes, err := os.ReadFile(filepath.Join(root, exampleFile))
	if err != nil {
		return nil, err
	}
	var example workflow
	if err := yaml.Unmarshal(exampleBytes, &example); err != nil {
		return nil, fmt.Errorf("%s: %w", exampleFile, err)
	}
	if example.On.PullRequest == nil {
		say("%s: has no pull_request trigger", exampleFile)
	}
	if example.On.WorkflowDispatch == nil {
		say("%s: has no workflow_dispatch trigger, and the hosted control plane dispatches it", exampleFile)
	} else {
		got := keys(example.On.WorkflowDispatch.Inputs)
		want := append([]string(nil), dispatchInputs...)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			say("%s: workflow_dispatch inputs are [%s], and the control plane sends [%s]",
				exampleFile, strings.Join(got, " "), strings.Join(want, " "))
		}
	}
	var calls int
	for name, j := range example.Jobs {
		m := usesRef.FindStringSubmatch(j.Uses)
		if m == nil {
			continue
		}
		calls++
		if m[1] != "/"+reusableFile {
			say("%s: job %s uses %q rather than the reusable workflow at %s", exampleFile, name, j.Uses, reusableFile)
		}
		if actionRef != "" && m[2] != actionRef {
			say("%s: calls the reusable workflow at @%s while %s calls the action at @%s; the two move together on release",
				exampleFile, m[2], reusableFile, actionRef)
		}
		if s, ok := j.Secrets.(string); !ok || s != "inherit" {
			say("%s: job %s does not set `secrets: inherit`, so the manifest's variables would never reach the run", exampleFile, name)
		}
		if reusable.On.WorkflowCall != nil {
			for k := range j.With {
				if _, ok := reusable.On.WorkflowCall.Inputs[k]; !ok {
					say("%s: passes with.%s, which %s does not declare", exampleFile, k, reusableFile)
				}
			}
		}
	}
	if calls == 0 {
		say("%s: no job calls the reusable workflow", exampleFile)
	}

	for _, c := range copies {
		b, err := os.ReadFile(filepath.Join(root, c))
		if err != nil {
			say("%s: %v (every embedded copy of %s must exist)", c, err, exampleFile)
			continue
		}
		if !bytes.Equal(b, exampleBytes) {
			say("%s differs from %s. The example is the source of truth: copy it over this file.", c, exampleFile)
		}
	}

	sort.Strings(problems)
	return problems, nil
}

func inputMentioned(text, name string) bool {
	return strings.Contains(text, "inputs."+name) || strings.Contains(text, "inputs['"+name+"']")
}

func values(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
