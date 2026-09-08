package ecs

// Not MIT. This directory is covered by the Antifailure Enterprise License; see
// ee/LICENSE.md.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Evidence is the half of this package that is not a document.
//
// Everything in egress.go is a predicate over a plan, and the caveat that
// travels with it says exactly what that is worth: a closed verdict there is a
// statement about a JSON document. Two of the thirteen paths are link local
// addresses, and no field of any AWS request decides them. AWS documents that
// Fargate removes the EC2 instance profile, which is a claim about what
// credentials exist; it does not say that 169.254.169.254 stops answering,
// which is a claim about what the address does. Reading the first as the second
// is how a containment claim turns into a plausible sentence.
//
// So this file settles them the only way they can be settled, which is by
// trying. The attempt runs inside a task in the customer's own account, and
// what it saw is recorded beside the environment and read back by the
// predicate. That converts unproven from a permanent hole into a value that is
// unproven until an environment has actually run and decided afterwards.
//
// The rule that keeps it honest is the absence rule: NO OBSERVATION MEANS
// UNPROVEN, forever. A missing probe result is not a quiet pass, a probe that
// could not run is not a refusal, and nothing in this file can move a path to
// closed except a recorded attempt that got nowhere.

// Outcome is what one attempt saw.
//
// Three values rather than four, and the missing one is the point. An earlier
// draft of this file had Refused and TimedOut as separate outcomes, which reads
// as more precise and is not: the probe is a shell command inside a container
// image, it distinguishes those two cases nowhere, and every recorded Refused
// would have been a guess dressed as a measurement. A vocabulary wider than the
// instrument is the same defect as a check that cannot say no, pointed the
// other way.
type Outcome string

const (
	// Reachable means something answered. It is the one outcome that is
	// positive evidence rather than the absence of it, and it opens the path.
	Reachable Outcome = "reachable"
	// NoAnswer means the attempt was made and got nowhere within its deadline,
	// whether it was refused outright or hung until it was cut off.
	NoAnswer Outcome = "no_answer"
	// Errored means the probe could not make the attempt at all. It is NOT a
	// refusal and must never be read as one: a probe that did not run tells you
	// nothing about the path, which is the same distinction the three valued
	// verdict exists for.
	Errored Outcome = "errored"
)

// Observation is one recorded attempt from one running task.
//
// It carries the address and the deadline as well as the outcome, because a
// timeout is only evidence if you know what was dialled and for how long. An
// observation that says "timed out" and does not say against what is the same
// kind of unfalsifiable claim as a check that cannot say no.
type Observation struct {
	// PathID is the egress path this attempt was about, and it must match an
	// id in Paths.
	PathID string `json:"path_id"`
	// EnvironmentID names the environment whose task made the attempt.
	EnvironmentID string `json:"environment_id"`
	// Address is exactly what was dialled.
	Address string `json:"address"`
	// Network is the dial network, tcp or udp.
	Network string `json:"network"`
	// Timeout is how long the attempt was given before it was called a
	// timeout.
	Timeout time.Duration `json:"timeout"`
	// Outcome is what happened.
	Outcome Outcome `json:"outcome"`
	// Detail is the error string or the answer, kept verbatim.
	Detail string `json:"detail"`
	// ObservedAt is when the attempt was made.
	ObservedAt time.Time `json:"observed_at"`
	// ProbeVersion identifies the probe that made it, so a result recorded by
	// an older probe is not silently read as one this code would produce.
	ProbeVersion string `json:"probe_version"`
}

// ProbeVersion is stamped into every observation this build records.
const ProbeVersion = "af-ecs-probe/1"

// Observations is a set of recorded attempts.
type Observations []Observation

// For returns the observation for a path, and whether there is one.
//
// When several were recorded for one path it returns the most recent, because
// the question the predicate asks is what the environment saw last, not what
// it saw first. An observation with a zero timestamp loses to any dated one.
func (o Observations) For(pathID string) (Observation, bool) {
	var best Observation
	found := false
	for _, obs := range o {
		if obs.PathID != pathID {
			continue
		}
		if !found || obs.ObservedAt.After(best.ObservedAt) {
			best, found = obs, true
		}
	}
	return best, found
}

// Target is one link local address the probe attempts.
//
// The list is short on purpose. A probe is only allowed to exist here for a
// path the configuration cannot decide, because a probe for a path a route
// table already answers would be a second instrument disagreeing with the
// first about the same fact.
type Target struct {
	// PathID is the path in Paths this attempt settles.
	PathID string
	// Network and Address are what to dial.
	Network string
	Address string
	// Why records where the address came from, because an address asserted
	// from memory is exactly the kind of thing that makes a probe report
	// confidently about the wrong endpoint.
	Why string
}

// Targets are the link local addresses the probe attempts.
//
// One, not two, and the omission is deliberate rather than unfinished. The
// instance metadata address is the one the ECS documentation names when it
// describes containers on container instances reaching instance metadata and
// IAM role credentials, and an HTTP GET against it is something any image with
// wget can make.
//
// The local Amazon Time Sync Service is NOT here even though its address is now
// known, because the attempt is an NTP exchange over UDP and nothing in a
// minimal container image makes one. A target whose only possible recorded
// outcome is Errored is not a way to answer a question, it is a way to fill a
// report with the word "probe" while learning nothing, so that path keeps its
// unproven verdict and this file says why instead.
func Targets() []Target {
	return []Target{
		{
			PathID:  "the-ec2-instance-metadata-service",
			Network: "tcp",
			Address: "169.254.169.254:80",
			Why: "the ECS documentation names this address when it describes containers on " +
				"container instances reaching instance metadata and IAM role credentials",
		},
	}
}

// Marker is the prefix of every line the probe emits.
//
// It exists for the reason the Kubernetes probe's marker exists: a container
// that failed for some other reason must never be read as a result. A line
// without it is not a probe result, and a run that produced no marked lines at
// all produced no evidence, which is different from producing evidence of
// containment.
const Marker = "AF-ECS-PROBE"

// ProbeScript is what the probe container runs.
//
// It is a shell script rather than a Go program because it runs inside the
// customer's task on the customer's image, where there is no Antifailure
// binary. The Kubernetes runtime's containment probe has exactly this shape and
// for exactly this reason.
//
// Every branch prints a marked line, including the branch where there is no
// tool to make the attempt with. A probe that stayed silent when it could not
// run would be indistinguishable from one that found nothing, and the reader
// would take the silence for an unrecorded path rather than for a broken
// instrument.
func ProbeScript(envID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#!/bin/sh\nenv_id=%q\n", envID)
	for _, t := range Targets() {
		fmt.Fprintf(&b, `
if command -v wget >/dev/null 2>&1; then
  if wget -T %d -q -O /dev/null http://%s/ >/dev/null 2>&1; then
    echo "%s %s $env_id %s reachable something answered within %ds"
  else
    echo "%s %s $env_id %s no_answer nothing answered within %ds"
  fi
else
  echo "%s %s $env_id %s errored no wget in this image, so no attempt was made"
fi
`,
			int(ProbeTimeout.Seconds()), t.Address,
			Marker, ProbeVersion, t.Address, int(ProbeTimeout.Seconds()),
			Marker, ProbeVersion, t.Address, int(ProbeTimeout.Seconds()),
			Marker, ProbeVersion, t.Address)
	}
	return b.String()
}

// ParseProbeOutput reads a probe container's log and returns what it recorded.
//
// It ignores every line that is not a marked one, so an image that prints its
// own startup noise cannot be mistaken for a probe, and it refuses a marked
// line it cannot read rather than skipping it, because a result this build
// cannot parse is a malfunction and not an absence.
//
// It also refuses to invent the paths it did not see. A target with no line in
// the output produces no observation, which leaves the path unproven, which is
// the correct answer for a probe that did not report about it.
func ParseProbeOutput(output string, now func() time.Time) (Observations, error) {
	if now == nil {
		now = time.Now
	}
	byAddress := map[string]Target{}
	for _, t := range Targets() {
		byAddress[t.Address] = t
	}
	var out Observations
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, Marker+" ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			return nil, fmt.Errorf("a %s line has %d fields and needs at least 5: %q",
				Marker, len(fields), line)
		}
		version, envID, address, outcome := fields[1], fields[2], fields[3], Outcome(fields[4])
		target, known := byAddress[address]
		if !known {
			return nil, fmt.Errorf("a %s line reports about %q, which is not one of this "+
				"build's probe targets, so which path it settles is unknown: %q",
				Marker, address, line)
		}
		switch outcome {
		case Reachable, NoAnswer, Errored:
		default:
			return nil, fmt.Errorf("a %s line reports the outcome %q, which is not one this "+
				"build knows: %q", Marker, outcome, line)
		}
		out = append(out, Observation{
			PathID:        target.PathID,
			EnvironmentID: envID,
			Address:       address,
			Network:       target.Network,
			Timeout:       ProbeTimeout,
			Outcome:       outcome,
			Detail:        strings.Join(fields[5:], " "),
			ObservedAt:    now(),
			ProbeVersion:  version,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ProbeTimeout is how long one attempt is given.
//
// Short, because the expected outcome of every attempt in Targets is that it
// hangs until it is cut off, and a probe that takes a minute per path is a
// probe somebody removes from the startup path.
const ProbeTimeout = 3 * time.Second

// EvidenceFile is the name the observations are recorded under, inside the
// runtime's state directory.
const EvidenceFile = "ecs-containment-evidence.json"

// Record writes observations for one environment into the state directory.
//
// It merges rather than replaces, keyed by environment and path, so a second
// run of one environment updates its own answer and leaves every other
// environment's alone. A file that only ever held the last run would answer
// the question for one environment and silently un answer it for the rest.
func Record(stateDir, envID string, obs Observations) error {
	if strings.TrimSpace(stateDir) == "" {
		return errors.New("recording containment evidence needs a state directory, and an " +
			"empty one would write beside whatever process was running")
	}
	existing, err := Load(stateDir)
	if err != nil {
		return err
	}
	keep := make(Observations, 0, len(existing)+len(obs))
	replaced := map[string]bool{}
	for _, o := range obs {
		replaced[o.EnvironmentID+"\x00"+o.PathID] = true
	}
	for _, o := range existing {
		if o.EnvironmentID == envID && replaced[o.EnvironmentID+"\x00"+o.PathID] {
			continue
		}
		keep = append(keep, o)
	}
	keep = append(keep, obs...)
	sort.SliceStable(keep, func(i, j int) bool {
		if keep[i].EnvironmentID != keep[j].EnvironmentID {
			return keep[i].EnvironmentID < keep[j].EnvironmentID
		}
		return keep[i].PathID < keep[j].PathID
	})

	body, err := json.MarshalIndent(keep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stateDir, EvidenceFile), append(body, '\n'), 0o644)
}

// Load reads recorded observations.
//
// A missing file is not an error and yields nothing, which is the absence rule
// again: an installation that has never run a probe is in exactly the state
// this package describes as unproven, and turning that into a failure would
// push somebody towards writing a file by hand to make the message go away.
//
// A file that exists and cannot be parsed IS an error, because that is a
// different thing: something wrote evidence and this build cannot read it, and
// answering unproven there would be reporting an absence that is really a
// malfunction.
func Load(stateDir string) (Observations, error) {
	// An empty state directory is not the current directory. Joining nothing
	// with the file name yields a bare relative path, which would read
	// whatever happens to be beside the process, and a containment verdict
	// taken from a file in somebody's working directory is worse than no
	// verdict at all.
	if strings.TrimSpace(stateDir) == "" {
		return nil, nil
	}
	body, err := os.ReadFile(filepath.Join(stateDir, EvidenceFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out Observations
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%s exists and could not be read as containment evidence, "+
			"which is not the same as there being none: %w",
			filepath.Join(stateDir, EvidenceFile), err)
	}
	return out, nil
}

// ForEnvironment narrows a set to one environment.
//
// It exists because evidence from another environment is evidence about
// another task in another subnet, and reading it here would be the same defect
// as a security group shared between environments: every rule still reads as
// correct and the conclusion is about somewhere else.
func (o Observations) ForEnvironment(envID string) Observations {
	var out Observations
	for _, obs := range o {
		if obs.EnvironmentID == envID {
			out = append(out, obs)
		}
	}
	return out
}
