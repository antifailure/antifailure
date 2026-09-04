// Command planguard refuses a Terraform plan that destroys a resource nobody
// said they wanted destroyed.
//
// THE DEFECT THIS EXISTS FOR, because it is a defect with no symptom. Commit
// ff893073, whose subject and whole message are about a Postgres test port,
// carried a stale checkout of twelve unrelated files. Among them it deleted
// `azurerm_role_assignment.cd_deploys_the_group`, which is the Contributor
// grant on af-cp-prod-centralus that lets cd.yml deploy production at all.
//
// Nothing failed. Nothing could have. The grant stayed live in Azure and stayed
// recorded in control-plane-production.tfstate; only the configuration that
// declared it went away. Deploys kept working because they run through
// `az containerapp update` rather than through Terraform. The whole of the
// damage was that every production plan from then on said "1 to destroy", and
// the first `terraform apply` would have revoked the grant that lets deployment
// reach production, after which the next deploy fails at its first
// `az containerapp` call and production cannot be shipped to.
//
// So the shape of the bug is: a resource that exists in Azure, exists in state,
// and exists in no configuration. Terraform is not confused by that and does
// not need to be told about it. Terraform reports it correctly, as a destroy,
// and the report goes into a plan log that a green check invites nobody to
// read.
//
// WHAT THIS REPOSITORY ALREADY HAD, and why it was not enough. infra.yml
// already counts destroys and already bolds the count into the job summary:
// "**This plan DESTROYS 1 resource(s).** Read it before approving." That is the
// right instinct and it failed twice over. It ran only against STAGING, and
// production was the environment that could not be planned at all until the
// step this branch adds. And it is a sentence rather than an exit code, so even
// pointed at the right plan it asks a person to notice. A check whose failure
// mode is "somebody did not read the summary" is documentation.
//
// WHY THE RULE IS "NO DESTROY", AND NOT SOMETHING NARROWER. The tempting
// narrowing is to gate only the resources that look important, role assignments
// say, or only production. Both require this tool to know which destroys matter,
// and it cannot: the grant that started this was three lines of HCL and the
// least imposing thing in the file. The honest rule is that no destroy is
// routine, because a destroy is Terraform reporting that the configuration and
// the world disagree, and the whole question is whether a person decided that.
//
// AND WHY IT IS NOT A BLANKET REFUSAL EITHER. A gate with no way through it
// gets deleted the first time somebody legitimately needs to remove a resource,
// and then the protection is gone permanently for the sake of one afternoon.
// That is close to how the grant was lost in the first place: something with no
// owner got swept along. So a destroy is allowed when it is WRITTEN DOWN, in
// tools/planguard/destroys-acknowledged.tsv, naming the exact address, an
// expiry and a reason. The price of an intended destroy is one line and the
// review that line gets. The price of an unintended one is a red check.
//
// THE THREE RULES ARE tools/tfsecignores' THREE RULES, deliberately, because
// this repository already made that decision about suppressions and a second
// mechanism with different manners would be one more thing to learn:
//
//  1. An entry with no expiry or no reason is not a decision.
//  2. An entry past its expiry has lapsed, and it says so rather than quietly
//     continuing to hold the gate open.
//  3. An entry that acknowledged NOTHING is stale and is an error. This is the
//     rule that keeps the file honest. Somebody acknowledges a destroy, the
//     destroy happens, and the line stays behind reading as permission that is
//     no longer scoped to anything. From then on it would wave through a future
//     destroy of the same address that nobody agreed to.
//
// IT REFUSES TO PASS A PLAN THAT COULD NOT HAVE FAILED. This is the part worth
// being careful about, and infra.yml already learned it the hard way in the
// empty-state warning it prints: a plan made with no remote state shows every
// resource as a create, so it CANNOT contain a destroy, and running this tool
// against one would produce a green check that examined nothing. That is worse
// than no check, because it looks like evidence. So a plan whose prior state
// holds no managed resources at all is an ERROR here and never a pass. The same
// reasoning is why a plan file this tool cannot parse is an error rather than
// an empty set of changes: tfsec failing to run and tfsec finding nothing look
// identical in an exit code, and eight findings once sat behind a green check in
// this repository for exactly that reason.
//
// WHAT IT DOES NOT CATCH, said here so the check is not read as more than it
// is. It sees destroys and it sees nothing else. The same commit that deleted
// the grant also reverted `github_app_id` to empty, which strips the GitHub App
// id and its two secret references out of the running production container app.
// That is an in-place UPDATE, not a destroy, and this tool passes it. A gate on
// updates would have to know which updates are wanted, and almost all of them
// are; that is a different check, if it is a check at all.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// dateLayout matches tools/tfsecignores. One spelling of a date across the
// repository is worth more than any argument for a different one.
const dateLayout = "2006-01-02"

// reasonFloor is how much prose an acknowledgement needs before it counts as a
// stated reason, and it is tfsecignores' number for the same reason: the bar is
// "somebody wrote something" rather than a word count nobody can defend.
const reasonFloor = 40

// planChange is the subset of `terraform show -json` this tool decides on.
//
// It reads resource_changes and prior_state and nothing else. Notably it does
// NOT read the `variables` block, which carries the values of sensitive input
// variables: this tool is run in a pipeline where a plan holding production
// credentials passes through it, and the less of that it touches the smaller
// the chance a future edit prints one.
type planFile struct {
	FormatVersion   string           `json:"format_version"`
	ResourceChanges []resourceChange `json:"resource_changes"`
	PriorState      *struct {
		Values *struct {
			RootModule module `json:"root_module"`
		} `json:"values"`
	} `json:"prior_state"`
}

type module struct {
	Resources []struct {
		Mode string `json:"mode"`
	} `json:"resources"`
	ChildModules []module `json:"child_modules"`
}

// managed is every MANAGED resource the prior state knows about, at any depth.
//
// BOTH HALVES OF THAT SENTENCE WERE LEARNED FROM A REAL PLAN RATHER THAN
// REASONED, and the tool was wrong about each of them first.
//
// At any depth, because the control plane stack keeps almost everything inside
// module.control_plane; counting only the root module would call a perfectly
// good plan empty and refuse it.
//
// MANAGED, because a plan built with no state at all is not empty. Terraform
// reads data sources during the plan and records them in prior_state, so the
// genuinely stateless production plan carries exactly one entry,
// module.control_plane.data.azurerm_client_config.current, and an earlier
// version of this function counted it and passed the plan. That is the precise
// failure this guard exists to prevent, reproduced by the guard itself. Only a
// managed resource can be destroyed, so only a managed resource is evidence
// that a destroy was possible.
func (m module) managed() int {
	n := 0
	for _, r := range m.Resources {
		if r.Mode == "managed" {
			n++
		}
	}
	for _, c := range m.ChildModules {
		n += c.managed()
	}
	return n
}

type resourceChange struct {
	Address string `json:"address"`
	Type    string `json:"type"`
	Mode    string `json:"mode"`
	Change  struct {
		Actions []string `json:"actions"`
	} `json:"change"`
}

// destroys reports whether this change removes the real resource.
//
// "delete" appearing anywhere in actions is the test, which means a REPLACE
// counts. That is deliberate and is not over-reach: replacing a role assignment
// revokes it and grants it again, and a deploy that runs in that window fails
// exactly as it would against a plain destroy. infra.yml's existing destroy
// counter asks the same question the same way.
func (c resourceChange) destroys() bool {
	for _, a := range c.Change.Actions {
		if a == "delete" {
			return true
		}
	}
	return false
}

// acknowledgement is one line of destroys-acknowledged.tsv.
type acknowledgement struct {
	Address string
	Expires string
	Reason  string
	Line    int

	expires time.Time
	matched bool
}

func main() {
	plan := flag.String("plan", "", "Path to the output of `terraform show -json <planfile>`.")
	ackPath := flag.String("acknowledged", "tools/planguard/destroys-acknowledged.tsv", "Path to the acknowledgement file.")
	label := flag.String("environment", "", "Which environment this plan is for, used only in messages.")
	now := flag.String("now", "", "Override today's date, as YYYY-MM-DD. For tests.")
	flag.Parse()

	if err := run(*plan, *ackPath, *label, *now); err != nil {
		fmt.Fprintf(os.Stderr, "planguard: %v\n", err)
		os.Exit(1)
	}
}

func run(planPath, ackPath, label, nowOverride string) error {
	if planPath == "" {
		return errors.New("-plan is required")
	}

	today := time.Now().UTC()
	if nowOverride != "" {
		t, err := time.Parse(dateLayout, nowOverride)
		if err != nil {
			return fmt.Errorf("-now: %w", err)
		}
		today = t
	}

	p, err := readPlan(planPath)
	if err != nil {
		return err
	}

	// The check that keeps this check honest. See the package comment: a plan
	// with no prior state cannot contain a destroy, so passing one is not
	// evidence of anything.
	if p.PriorState == nil || p.PriorState.Values == nil || p.PriorState.Values.RootModule.managed() == 0 {
		return fmt.Errorf(`this plan has NO PRIOR STATE, so it cannot contain a destroy and this check cannot say no.

A plan made without a state backend reads every resource as a create. Passing it
would be a green check that examined nothing, which is worse than no check.
Point this at a plan made against the real state, or do not run it at all.

  plan: %s`, planPath)
	}

	acks, err := readAcknowledgements(ackPath)
	if err != nil {
		return err
	}

	var problems []string

	// Rule 1 and rule 2, on the file itself, before any plan is consulted. A
	// malformed acknowledgement is a problem whether or not it is load bearing
	// today.
	for i := range acks {
		a := &acks[i]
		switch {
		case a.Expires == "":
			problems = append(problems, fmt.Sprintf(
				"%s:%d: %s has no expiry. An acknowledgement with no shelf life is a policy change wearing a temporary hat.",
				ackPath, a.Line, a.Address))
		case len(strings.TrimSpace(a.Reason)) < reasonFloor:
			problems = append(problems, fmt.Sprintf(
				"%s:%d: %s has no stated reason (needs at least %d characters saying why this destroy is intended).",
				ackPath, a.Line, a.Address, reasonFloor))
		case a.expires.Before(today):
			problems = append(problems, fmt.Sprintf(
				"%s:%d: %s expired on %s. The decision to allow this destroy has lapsed; renew it deliberately or remove the line.",
				ackPath, a.Line, a.Address, a.Expires))
		}
	}

	byAddress := map[string]*acknowledgement{}
	for i := range acks {
		byAddress[acks[i].Address] = &acks[i]
	}

	var unacknowledged []resourceChange
	for _, c := range p.ResourceChanges {
		if !c.destroys() {
			continue
		}
		if a, ok := byAddress[c.Address]; ok && a.Expires != "" && !a.expires.Before(today) {
			a.matched = true
			continue
		}
		unacknowledged = append(unacknowledged, c)
	}

	// Rule 3. An acknowledgement that matched nothing is stale, and a stale one
	// is permission sitting in the tree for a destroy nobody is proposing.
	for i := range acks {
		a := &acks[i]
		if !a.matched && a.Expires != "" && !a.expires.Before(today) {
			problems = append(problems, fmt.Sprintf(
				"%s:%d: %s acknowledges a destroy this plan does not propose. Either the destroy already happened and this line should go, or the address changed and this line no longer protects what somebody thought it did.",
				ackPath, a.Line, a.Address))
		}
	}

	sort.Slice(unacknowledged, func(i, j int) bool { return unacknowledged[i].Address < unacknowledged[j].Address })

	where := "this plan"
	if label != "" {
		where = label
	}

	if len(unacknowledged) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "%s proposes %d destroy(s) that nobody acknowledged.\n\n", where, len(unacknowledged))
		for _, c := range unacknowledged {
			fmt.Fprintf(&b, "  %s  (%s, actions: %s)\n", c.Address, c.Type, strings.Join(c.Change.Actions, ","))
		}
		b.WriteString(`
A destroy here means the resource exists in state and in the cloud, and no
configuration in this repository declares it any more. That is either a removal
somebody intended, or a removal nobody noticed, and the two are indistinguishable
from the outside. This gate exists because they were indistinguishable once and
the resource was the grant that lets continuous deployment reach production.

IF THIS DESTROY IS INTENDED, say so and this passes. Add a line to
` + ackPath + `, tab separated:

  <address>	<expiry YYYY-MM-DD>	<why it is intended>

IF IT IS NOT, the fix is in the configuration, not here. Find what stopped
declaring the resource. ` + "`git log -S <resource name>`" + ` on the stack usually names
the commit in one go.`)
		problems = append(problems, b.String())
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n\n"))
	}

	fmt.Printf("planguard: %s destroys nothing that is not acknowledged (%d change(s) examined, %d acknowledgement(s) live).\n",
		where, len(p.ResourceChanges), len(acks))
	return nil
}

// readPlan refuses anything it cannot understand rather than reporting an empty
// set of changes. See the package comment: "could not read it" and "found
// nothing" must not share an exit code.
func readPlan(path string) (*planFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading plan: %w", err)
	}
	var p planFile
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parsing %s: %w\n\nThis wants the output of `terraform show -json <planfile>`, not the plan file itself and not the human readable plan.", path, err)
	}
	if p.FormatVersion == "" {
		return nil, fmt.Errorf("%s has no format_version, so it is not a `terraform show -json` document. Refusing to report zero destroys from a file this tool did not understand.", path)
	}
	return &p, nil
}

// readAcknowledgements parses the TSV. A file that is absent is not an error:
// the expected steady state of this repository is that no destroy is
// acknowledged, and requiring an empty file to exist would be ceremony.
func readAcknowledgements(path string) ([]acknowledgement, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading acknowledgements: %w", err)
	}

	var out []acknowledgement
	for i, line := range strings.Split(string(raw), "\n") {
		n := i + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		a := acknowledgement{Line: n, Address: strings.TrimSpace(fields[0])}
		if len(fields) > 1 {
			a.Expires = strings.TrimSpace(fields[1])
		}
		if len(fields) > 2 {
			a.Reason = strings.TrimSpace(strings.Join(fields[2:], " "))
		}
		if a.Expires != "" {
			t, err := time.Parse(dateLayout, a.Expires)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: expiry %q is not %s", path, n, a.Expires, dateLayout)
			}
			a.expires = t
		}
		out = append(out, a)
	}
	return out, nil
}
