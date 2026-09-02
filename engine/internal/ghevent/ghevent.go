// Package ghevent reads the pull request GitHub Actions is running us for.
//
// It exists for one reason: `github.fork_policy` in the manifest was validated,
// defaulted, and printed by `af explain`, and nothing anywhere refused
// anything. `af explain` said "forks never, no environment is created for a
// fork" and `af up` on a fork pull request answered "Bringing up
// forkrepro-main-0cd221" and went to the Docker daemon. A security control
// that is only ever printed is worse than no control, because the reader stops
// looking.
//
// Everything here comes out of GITHUB_EVENT_PATH, the file the runner writes
// the webhook payload to. That file is written by the runner and not by the
// checkout, so a fork cannot edit it. What a fork CAN edit is the manifest, and
// that is handled a layer up, in the caller, by reading the policy from the
// base branch rather than from the tree under test.
package ghevent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// ApprovalLabel is the label a maintainer adds to let a fork's code run.
//
// The same string the control plane uses (FORK_APPROVAL_LABEL in
// web/apps/api/src/github/lifecycle.ts) and the same string the manifest
// schema, the reference table and the pull request guide all name. One
// spelling, because a label that is nearly right is a label that never
// matches and a maintainer who thinks they approved something.
const ApprovalLabel = "antifailure:allow"

// PullRequest is the part of the payload this package has an opinion about.
type PullRequest struct {
	// Number is the pull request, for the message.
	Number int
	// BaseRepository is owner/name of the repository being merged into. It is
	// read from the top level `repository`, which is present on every
	// delivery, rather than from `pull_request.base.repo`, which is not.
	BaseRepository string
	// HeadRepository is owner/name of the repository the code comes from, or
	// empty when GitHub sent none.
	HeadRepository string
	// HeadSHA is the commit under test.
	HeadSHA string
	// Labels are the label names on the pull request at the moment the event
	// fired.
	Labels []string
	// Action is what happened: opened, synchronize, labeled, and so on.
	Action string
}

// FromFork reports whether the code being run was written somewhere the base
// repository's write access does not reach.
//
// A missing head repository counts as a fork, not as an unknown. GitHub sends
// `head.repo: null` for a pull request whose fork has since been deleted, and
// treating that as "the same repository" would run a deleted fork's code with
// the base repository's credentials. This agrees with rememberPullRequest in
// the control plane on purpose: two halves of one product disagreeing about
// what a fork is would be a hole shaped exactly like the one this closes.
func (pr *PullRequest) FromFork() bool {
	if pr.HeadRepository == "" {
		return true
	}
	return !strings.EqualFold(pr.HeadRepository, pr.BaseRepository)
}

// Approved reports whether the approval label is on the pull request.
func (pr *PullRequest) Approved() bool {
	for _, l := range pr.Labels {
		if strings.EqualFold(strings.TrimSpace(l), ApprovalLabel) {
			return true
		}
	}
	return false
}

// Event names the kinds of delivery a fork's code can be running under.
//
// pull_request_target is here and it is the one that matters most. It runs
// with the BASE repository's secrets by design, which is exactly the
// configuration the fork policy exists to protect, and it is the shape a
// maintainer reaches for when a fork pull request needs a credential.
func pullRequestEvent(name string) bool {
	return name == "pull_request" || name == "pull_request_target"
}

// Read returns the pull request this process is running for.
//
// The three results are distinct states and every caller has to tell them
// apart:
//
//	pr != nil, err == nil   a pull request, and this is it
//	pr == nil, err == nil   not a pull request event, so the policy is silent
//	pr == nil, err != nil   a pull request event we could not read
//
// The third is not the second. A caller that folds "could not read the event"
// into "not a pull request" turns every unreadable payload into an allow, and
// the payload is the only thing that says whether the code is a stranger's.
func Read(getenv func(string) string) (*PullRequest, error) {
	name := strings.TrimSpace(getenv("GITHUB_EVENT_NAME"))
	if !pullRequestEvent(name) {
		return nil, nil
	}
	path := strings.TrimSpace(getenv("GITHUB_EVENT_PATH"))
	if path == "" {
		return nil, fmt.Errorf("GITHUB_EVENT_NAME is %s and GITHUB_EVENT_PATH is not set", name)
	}
	body, err := os.ReadFile(path) //nolint:gosec // the path is the runner's, and reading it is the point
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Parse(body)
}

// payload is the shape of the part of the delivery this reads.
//
// Pointers on the two nested repositories, because null and absent are the
// case that decides the answer and a value type would render both as the empty
// string alongside a genuinely empty name.
type payload struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest *struct {
		Number int `json:"number"`
		Head   *struct {
			SHA  string `json:"sha"`
			Repo *struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	} `json:"pull_request"`
	Repository *struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// Parse reads one delivery body.
//
// Split out from Read so the decision can be tested against a real payload
// without a file, and so a caller holding the bytes already does not write
// them out to hand them back.
func Parse(body []byte) (*PullRequest, error) {
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("the event payload is not JSON: %w", err)
	}
	if p.PullRequest == nil {
		return nil, fmt.Errorf("the event payload has no pull_request")
	}
	if p.Repository == nil || strings.TrimSpace(p.Repository.FullName) == "" {
		// Without the base repository there is nothing to compare the head
		// against, so fork-ness cannot be decided. Said rather than guessed.
		return nil, fmt.Errorf("the event payload names no repository")
	}
	pr := &PullRequest{
		Number:         p.PullRequest.Number,
		BaseRepository: strings.TrimSpace(p.Repository.FullName),
		Action:         p.Action,
	}
	if pr.Number == 0 {
		pr.Number = p.Number
	}
	if p.PullRequest.Head != nil {
		pr.HeadSHA = p.PullRequest.Head.SHA
		if p.PullRequest.Head.Repo != nil {
			pr.HeadRepository = strings.TrimSpace(p.PullRequest.Head.Repo.FullName)
		}
	}
	for _, l := range p.PullRequest.Labels {
		if n := strings.TrimSpace(l.Name); n != "" {
			pr.Labels = append(pr.Labels, n)
		}
	}
	return pr, nil
}

// Decision is what the fork policy says about this run.
type Decision struct {
	// Refused is whether nothing may run.
	Refused bool
	// Reason is the sentence a person reads on the pull request. Empty when
	// nothing was refused.
	Reason string
}

// Decide applies the fork policy to one pull request.
//
// pr nil means this is not a pull request, which is every local run, every
// push, and every workflow_dispatch. The policy is silent there rather than
// permissive: there is no fork in the question.
func Decide(policy schema.ForkPolicy, pr *PullRequest) Decision {
	if pr == nil || !pr.FromFork() {
		return Decision{}
	}
	switch policy {
	case schema.ForkAlways:
		return Decision{}
	case schema.ForkNever:
		return Decision{Refused: true, Reason: fmt.Sprintf(
			"This pull request is from %s, and the manifest sets `github.fork_policy: never`, "+
				"so no environment is created for a fork. "+
				"Change the policy to `label` if a maintainer should be able to approve one.",
			forkName(pr))}
	default:
		// label, and anything unrecognised. The default in normalize.go is
		// label and an unknown value is refused by validation, so this arm is
		// reached only by a caller constructing a policy by hand; landing on
		// the strictest workable answer is the right way to be wrong.
		if pr.Approved() {
			return Decision{}
		}
		return Decision{Refused: true, Reason: fmt.Sprintf(
			"This pull request is from %s. A fork's code would run against an environment holding "+
				"a masked copy of your data, with whatever this runner can reach, so nothing runs "+
				"until a maintainer adds the `%s` label to pull request #%d and the check runs again.",
			forkName(pr), ApprovalLabel, pr.Number)}
	}
}

// forkName is how the head repository is described in the refusal.
func forkName(pr *PullRequest) string {
	if pr.HeadRepository == "" {
		// The fork was deleted between the push and this run. Saying "a fork
		// that no longer exists" beats naming the empty string, and it is the
		// truthful description of what GitHub sent.
		return "a fork that no longer exists"
	}
	return "the fork " + pr.HeadRepository
}
