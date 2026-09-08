package ecs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/runtime/ecs"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// observedAt is a fixed clock, so that a test asserting on a rendered detail
// line is asserting on the code rather than on the minute it ran in.
func observedAt() time.Time { return time.Date(2026, 9, 8, 4, 5, 6, 0, time.UTC) }

// imds is the path the probe exists for.
const imds = "the-ec2-instance-metadata-service"

// observation builds one recorded attempt for the reference environment.
func observation(outcome ecs.Outcome) ecs.Observation {
	return ecs.Observation{
		PathID:        imds,
		EnvironmentID: "af-example",
		Address:       "169.254.169.254:80",
		Network:       "tcp",
		Timeout:       ecs.ProbeTimeout,
		Outcome:       outcome,
		Detail:        "nothing answered within 3s",
		ObservedAt:    observedAt(),
		ProbeVersion:  ecs.ProbeVersion,
	}
}

// TestNoEvidenceLeavesTheLinkLocalPathUnproven is the assertion the whole
// evidence mechanism exists to be safe under.
//
// It is written as its own test rather than folded into the verdict table
// because the failure it guards is not a wrong verdict, it is a mechanism that
// only ever moves in one direction. Every other path in this package is decided
// by a plan, so its check answers whatever the plan says. This one is decided by
// something that might not have happened, and the way that goes wrong is that
// "we have no result" quietly becomes "we found nothing", which reads the same
// in a report and means the opposite.
func TestNoEvidenceLeavesTheLinkLocalPathUnproven(t *testing.T) {
	for _, empty := range []ecs.Observations{nil, {}} {
		report := ecs.EvaluateWith(referencePlan(), empty)
		byID := verdictsByID(report)
		require.Equal(t, ecs.Unproven, byID[imds],
			"with no probe result the instance metadata path must stay unproven")

		for _, v := range report.Verdicts {
			if v.Path.ID != imds {
				continue
			}
			require.Equal(t, ecs.NotEstablished, v.Grade)
			require.Contains(t, v.Detail, "no probe result has been recorded")
		}
		require.Equal(t, 0, report.ClosedByObservation,
			"nothing was observed, so nothing may be counted as observed")
	}
}

// TestNoPathClosesOnAbsentEvidence is the same rule stated over the whole
// enumeration rather than over one path.
//
// The one above would keep passing if somebody added a second observable path
// and got its default wrong. This one cannot, because it asks the question of
// every path that declares an Observe, and the set it asks about is read from
// the enumeration rather than written down here.
func TestNoPathClosesOnAbsentEvidence(t *testing.T) {
	observable := 0
	for _, p := range ecs.Paths() {
		if p.Observe == nil {
			continue
		}
		observable++
		verdict, detail := p.Check(referencePlan())
		require.Equal(t, ecs.Unproven, verdict,
			"%s can be answered by a probe, and with no probe result its own check returned "+
				"%q. A path whose evidence is optional must be unproven without it, or the "+
				"absence of a probe is a pass: %s", p.ID, verdict, detail)
	}
	require.Equal(t, 1, observable,
		"exactly one path has a probe today, and a change to that number should be a decision")
}

// TestAProbeResultDecidesTheLinkLocalPath walks the three outcomes.
//
// One test per outcome would read better and would test less: the value of
// running them together is that the same plan and the same environment produce
// three different verdicts, so a mechanism that ignored the observation
// entirely could not pass any of them by accident.
func TestAProbeResultDecidesTheLinkLocalPath(t *testing.T) {
	for _, tc := range []struct {
		outcome ecs.Outcome
		want    ecs.Verdict
		grade   ecs.Grade
		says    string
	}{
		{ecs.Reachable, ecs.Open, ecs.FromObservation, "something answered"},
		{ecs.NoAnswer, ecs.Closed, ecs.FromObservation, "nothing answered within"},
		{ecs.Errored, ecs.Unproven, ecs.NotEstablished, "could not make the attempt at all"},
	} {
		t.Run(string(tc.outcome), func(t *testing.T) {
			report := ecs.EvaluateWith(referencePlan(),
				ecs.Observations{observation(tc.outcome)})
			for _, v := range report.Verdicts {
				if v.Path.ID != imds {
					continue
				}
				require.Equal(t, tc.want, v.Verdict)
				require.Equal(t, tc.grade, v.Grade)
				require.Contains(t, v.Detail, tc.says)
			}
		})
	}
}

// TestAnObservedClosureIsCountedApartFromADocumentedOne is the arithmetic the
// grade was introduced for.
func TestAnObservedClosureIsCountedApartFromADocumentedOne(t *testing.T) {
	report := ecs.EvaluateWith(referencePlan(), ecs.Observations{observation(ecs.NoAnswer)})
	require.Equal(t, 11, report.Closed, "the probe settles one more path")
	require.Equal(t, 10, report.ClosedByDocument)
	require.Equal(t, 1, report.ClosedByObservation)
	require.Equal(t, 1, report.Unproven, "the time sync path has no probe and stays unproven")
	require.False(t, report.Contained(),
		"one open path remains and no evidence closes it, so this is still not contained")

	out := report.String()
	require.Contains(t, out, "11 of 13 egress paths")
	require.Contains(t, out, "10 are closed by the generated configuration and 1 by an attempt")
	require.Contains(t, out, "[observation]")
	require.Contains(t, out, "[document]")
}

// TestEvidenceFromAnotherEnvironmentDoesNotAnswerForThisOne holds the boundary
// that makes the evidence about this task rather than about some task.
//
// Every environment in this design shares a VPC, so evidence from a neighbour
// is evidence about a different ENI in a different subnet under a different
// security group. Reading it here would be the same defect as a security group
// shared between environments: every field still reads as correct and the
// conclusion is about somewhere else.
func TestEvidenceFromAnotherEnvironmentDoesNotAnswerForThisOne(t *testing.T) {
	neighbour := observation(ecs.NoAnswer)
	neighbour.EnvironmentID = "af-somebody-else"

	all := ecs.Observations{neighbour}
	require.Empty(t, all.ForEnvironment("af-example"),
		"a neighbour's result must not be visible to this environment")

	report := ecs.EvaluateWith(referencePlan(), all.ForEnvironment("af-example"))
	require.Equal(t, ecs.Unproven, verdictsByID(report)[imds])
}

// TestAResultFromAProbeThisBuildDoesNotKnowIsNotEvidence guards the version
// stamp, which is the only thing standing between this reader and a file
// written by something whose behaviour it is guessing at.
func TestAResultFromAProbeThisBuildDoesNotKnowIsNotEvidence(t *testing.T) {
	stale := observation(ecs.NoAnswer)
	stale.ProbeVersion = "af-ecs-probe/0"

	report := ecs.EvaluateWith(referencePlan(), ecs.Observations{stale})
	byID := verdictsByID(report)
	require.Equal(t, ecs.Unproven, byID[imds],
		"a result recorded by a probe with different behaviour is not evidence about this one")
	require.Equal(t, 0, report.ClosedByObservation)
}

// TestTheMostRecentResultWins is the ordering rule, and it is asserted in both
// directions because a comparison written the wrong way round passes one of
// them.
func TestTheMostRecentResultWins(t *testing.T) {
	older := observation(ecs.Reachable)
	newer := observation(ecs.NoAnswer)
	newer.ObservedAt = older.ObservedAt.Add(time.Hour)

	forwards := ecs.EvaluateWith(referencePlan(), ecs.Observations{older, newer})
	require.Equal(t, ecs.Closed, verdictsByID(forwards)[imds])

	backwards := ecs.EvaluateWith(referencePlan(), ecs.Observations{newer, older})
	require.Equal(t, ecs.Closed, verdictsByID(backwards)[imds],
		"the answer must depend on the timestamps and not on the order of the slice")
}

// TestTheProbeScriptAttemptsWhatItSaysItDoes reads the generated script.
//
// A script is code that nothing in this repository executes, so the only thing
// that can check it is an assertion about its text. That is weaker than running
// it and it is said plainly rather than dressed up: what this proves is that the
// script names the address the enumeration names and prints a marked line on
// every branch, not that a container running it behaves as described.
func TestTheProbeScriptAttemptsWhatItSaysItDoes(t *testing.T) {
	script := ecs.ProbeScript("af-example")
	require.Contains(t, script, "169.254.169.254")
	require.Contains(t, script, "af-example")

	require.Equal(t, 3, strings.Count(script, ecs.Marker+" "),
		"reachable, no_answer and errored must each print a marked line, because a probe that "+
			"stays silent when it cannot run is indistinguishable from one that found nothing")
	require.Contains(t, script, "reachable")
	require.Contains(t, script, "no_answer")
	require.Contains(t, script, "errored no wget")
}

// TestTheProbeScriptOutputParsesBackIntoObservations closes the loop between
// the two halves that have to agree.
//
// The script writes lines and the parser reads them, and nothing else checks
// that they agree about the format. Feeding the parser a line shaped exactly
// like the one the script prints is the closest this can get to running it.
func TestTheProbeScriptOutputParsesBackIntoObservations(t *testing.T) {
	out := "starting up\n" +
		ecs.Marker + " " + ecs.ProbeVersion +
		" af-example 169.254.169.254:80 no_answer nothing answered within 3s\n" +
		"done\n"

	obs, err := ecs.ParseProbeOutput(out, observedAt)
	require.NoError(t, err)
	require.Len(t, obs, 1, "the unmarked lines are not results and must not become any")
	require.Equal(t, imds, obs[0].PathID)
	require.Equal(t, "af-example", obs[0].EnvironmentID)
	require.Equal(t, ecs.NoAnswer, obs[0].Outcome)
	require.Equal(t, "nothing answered within 3s", obs[0].Detail)
	require.Equal(t, observedAt(), obs[0].ObservedAt)

	report := ecs.EvaluateWith(referencePlan(), obs)
	require.Equal(t, ecs.Closed, verdictsByID(report)[imds],
		"a result the probe could have printed must reach the predicate")
}

// TestTheParserRefusesWhatItCannotRead is the parser's own ability to say no.
//
// Each row is a line that is marked, so it claims to be a result, and that this
// build cannot turn into one. Skipping such a line would leave the path
// unproven, which is the answer for a probe that did not report and the wrong
// answer for a probe that reported something unreadable.
func TestTheParserRefusesWhatItCannotRead(t *testing.T) {
	for _, tc := range []struct{ name, line, says string }{
		{
			name: "too few fields",
			line: ecs.Marker + " " + ecs.ProbeVersion + " af-example",
			says: "needs at least 5",
		},
		{
			name: "an address this build does not probe",
			line: ecs.Marker + " " + ecs.ProbeVersion +
				" af-example 169.254.170.2:80 no_answer x",
			says: "which path it settles is unknown",
		},
		{
			name: "an outcome this build does not know",
			line: ecs.Marker + " " + ecs.ProbeVersion +
				" af-example 169.254.169.254:80 probably_fine x",
			says: "not one this build knows",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obs, err := ecs.ParseProbeOutput(tc.line+"\n", observedAt)
			require.Error(t, err, "a marked line this build cannot read is a malfunction")
			require.Contains(t, err.Error(), tc.says)
			require.Nil(t, obs)
		})
	}
}

// TestUnmarkedOutputProducesNothing is the other half of the marker rule.
func TestUnmarkedOutputProducesNothing(t *testing.T) {
	obs, err := ecs.ParseProbeOutput("standard_init_linux.go: exec format error\n", observedAt)
	require.NoError(t, err)
	require.Empty(t, obs, "a container that failed for some other reason is not a result")
}

// TestRecordAndLoadKeepEveryEnvironmentsAnswer is the merge rule.
func TestRecordAndLoadKeepEveryEnvironmentsAnswer(t *testing.T) {
	dir := t.TempDir()

	mine := observation(ecs.NoAnswer)
	theirs := observation(ecs.Reachable)
	theirs.EnvironmentID = "af-other"

	require.NoError(t, ecs.Record(dir, "af-other", ecs.Observations{theirs}))
	require.NoError(t, ecs.Record(dir, "af-example", ecs.Observations{mine}))

	back, err := ecs.Load(dir)
	require.NoError(t, err)
	require.Len(t, back, 2, "recording one environment's answer must not erase another's")

	got, ok := back.For(imds)
	require.True(t, ok)
	require.NotEmpty(t, got.EnvironmentID)

	// And a second run of one environment replaces its own answer rather than
	// accumulating beside it, because two answers for one path from one
	// environment is a question about which is current that nobody wants.
	again := observation(ecs.Reachable)
	again.ObservedAt = observedAt().Add(time.Hour)
	require.NoError(t, ecs.Record(dir, "af-example", ecs.Observations{again}))

	back, err = ecs.Load(dir)
	require.NoError(t, err)
	require.Len(t, back, 2)
	require.Equal(t, ecs.Reachable, mustFind(t, back, "af-example").Outcome)
	require.Equal(t, ecs.Reachable, mustFind(t, back, "af-other").Outcome)
}

// TestLoadTellsAbsenceApartFromMalfunction is the distinction that decides
// whether an unproven verdict means what it says.
func TestLoadTellsAbsenceApartFromMalfunction(t *testing.T) {
	dir := t.TempDir()

	obs, err := ecs.Load(dir)
	require.NoError(t, err, "an installation that has never run a probe is not an error")
	require.Empty(t, obs)

	require.NoError(t, os.WriteFile(filepath.Join(dir, ecs.EvidenceFile),
		[]byte("{not json"), 0o644))
	obs, err = ecs.Load(dir)
	require.Error(t, err,
		"a file that exists and cannot be read is a malfunction, and reporting it as no "+
			"evidence would answer unproven for a reason that is not the reason unproven means")
	require.Contains(t, err.Error(), "which is not the same as there being none")
	require.Nil(t, obs)
}

// TestResolvableHostsReadsTheManifestsOwnAnswer is the composition with the
// egress catalogue.
//
// The modes are not split by taste. A host the sidecar forwards to for real has
// to resolve or the environment does not work; a host the sidecar answers out of
// a fixture, an inbox or a model must NOT resolve, because a name the sidecar
// answers resolving publicly is a route around the decision the manifest made.
func TestResolvableHostsReadsTheManifestsOwnAnswer(t *testing.T) {
	allowed, unexpressible := ecs.ResolvableHosts(schema.Egress{Rules: []schema.EgressRule{
		{Host: "api.stripe.com", Mode: schema.ModeAllow},
		{Host: "api.twilio.com", Mode: schema.ModeSandbox},
		{Host: "hooks.slack.com", Mode: schema.ModeCapture},
		{Host: "api.openai.com", Mode: schema.ModeMock},
		{Host: "evil.example.com", Mode: schema.ModeBlock},
		{Host: "guess.example.com", Mode: schema.ModeSynth},
		{Host: "*.googleapis.com", Mode: schema.ModeAllow},
		{Host: "email.*.amazonaws.com", Mode: schema.ModeAllow},
	}})

	require.Equal(t, []string{"*.googleapis.com", "api.stripe.com", "api.twilio.com"}, allowed,
		"allow and sandbox reach the real host and must resolve; block, mock, capture and "+
			"synth are answered by the sidecar and must not")
	require.Equal(t, []string{"email.*.amazonaws.com"}, unexpressible,
		"a DNS Firewall domain specification may only start with a star, and a host that "+
			"cannot be written as one must be named rather than dropped or widened")
}

// TestTheDeclaredHostsReachTheFirewall proves the composition is wired rather
// than merely available.
//
// ResolvableHosts having the right answer proves nothing on its own: a reducer
// nobody calls is a dead function that looks like a feature, which is the exact
// shape this repository keeps finding. So this asserts the host is in the rule
// group the generator emits, and that the terminal rule is still last.
func TestTheDeclaredHostsReachTheFirewall(t *testing.T) {
	in := reference()
	in.ResolvableHosts = []string{"api.stripe.com"}
	plan := ecs.Generate(in, "af-example")

	rules := plan.Network.DNSFirewall.Rules
	require.Len(t, rules, 2)
	require.Equal(t, "ALLOW", rules[0].Action)
	require.Contains(t, rules[0].Domains, "api.stripe.com",
		"a host the manifest says the environment may reach has to resolve, or the firewall "+
			"breaks the environment it is protecting and somebody removes it")
	require.Contains(t, rules[0].Domains, "api.ecr.us-east-1.amazonaws.com",
		"and the image pull still has to resolve, or nothing starts")

	require.Equal(t, "BLOCK", rules[1].Action)
	require.Equal(t, []string{"*"}, rules[1].Domains)
	require.Greater(t, rules[1].Priority, rules[0].Priority,
		"AWS processes a rule group by order of priority starting from the lowest, so the "+
			"terminal rule has to carry the highest number")
	require.Equal(t, ecs.FailClosed, plan.Network.DNSFirewall.FailOpen)

	require.Equal(t, ecs.Closed, verdictsByID(ecs.Evaluate(plan))["the-amazon-provided-resolver"])
}

// TestTheProbeContainerIsEmittedOnlyWithAnImage guards the direction this could
// have been faked in.
//
// A container naming a command that does not exist would be a plan that cannot
// be applied wearing the shape of a control, which is what this package's own
// DNS firewall check was taught to refuse. So the absence of an image produces
// the absence of a container and a report that says so, not a container that
// would fail.
func TestTheProbeContainerIsEmittedOnlyWithAnImage(t *testing.T) {
	bare := ecs.Generate(reference(), "af-example")
	require.Empty(t, bare.TaskDefinition.Containers,
		"with no probe image named, no probe container")
	require.Contains(t, detailByID(ecs.Evaluate(bare))[imds], "no probe container either")

	in := reference()
	in.ProbeImage = "public.ecr.aws/docker/library/busybox:1.36"
	withProbe := ecs.Generate(in, "af-example")
	require.Len(t, withProbe.TaskDefinition.Containers, 1)

	probe := withProbe.TaskDefinition.Containers[0]
	require.Equal(t, ecs.ProbeContainerName, probe.Name)
	require.Equal(t, in.ProbeImage, probe.Image)
	require.True(t, probe.Essential,
		"a probe whose failure does not stop the task is a bystander")
	require.Equal(t, []string{"/bin/sh", "-c", ecs.ProbeScript("af-example")}, probe.Command)
	require.Contains(t, detailByID(ecs.Evaluate(withProbe))[imds], ecs.ProbeContainerName)
}

// mustFind returns one environment's observation or fails the test.
func mustFind(t *testing.T, all ecs.Observations, envID string) ecs.Observation {
	t.Helper()
	for _, o := range all {
		if o.EnvironmentID == envID {
			return o
		}
	}
	t.Fatalf("no observation recorded for %s", envID)
	return ecs.Observation{}
}
