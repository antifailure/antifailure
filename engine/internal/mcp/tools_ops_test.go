package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// theToken is a credential with a shape the engine's pattern rules recognise.
// A real engine token has no such prefix, which is why the adapter that holds
// one registers it exactly; scrubberFor is what that path relies on and it is
// proved separately below.
const theToken = "sk-ant-a-real-looking-credential-value"

// theShapelessSecret has no prefix any pattern rule names, so nothing but
// exact registration can find it.
const theShapelessSecret = "af_engine_token_a_real_looking_value"

func passingDiagnosis() Diagnosis {
	return Diagnosis{
		OK: true, Platform: "darwin/arm64",
		Checks: []DiagnosticCheck{
			{Name: "docker", Status: "ok", Detail: "27.0.3", Remediation: "Nothing to do."},
			{Name: "disk", Status: "ok", Detail: "80 GiB free", Remediation: "Nothing to do."},
		},
	}
}

func readyRunner() RunnerReadiness {
	return RunnerReadiness{
		Verdict: "ready", Path: "/home/x/.antifailure/runner", Node: ">=20",
		Checks: []DiagnosticCheck{
			{Name: "runner", Status: "ok", Detail: "/home/x/.antifailure/runner"},
			{Name: "node", Status: "ok", Detail: "v22.4.0"},
		},
	}
}

// ---------------------------------------------------------------------------
// check_prerequisites
// ---------------------------------------------------------------------------

func TestCheckPrerequisites_ReadyWhenEveryDecidingQuestionWasAnswered(t *testing.T) {
	t.Parallel()
	tool := newCheckPrerequisitesTool(testProject(t),
		func(context.Context) (Diagnosis, error) { return passingDiagnosis(), nil },
		func(context.Context) (RunnerReadiness, error) { return readyRunner(), nil })

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(prerequisitesResult)

	require.Equal(t, verdictReady, out.Verdict)
	require.NotNil(t, out.Machine)
	require.NotNil(t, out.Runner)
	require.Empty(t, out.NotChecked)
	require.Equal(t, "darwin/arm64", out.Platform)
}

func TestCheckPrerequisites_AnUnwiredCheckIsNotCheckedAndNeverAPass(t *testing.T) {
	t.Parallel()
	// A section that vanishes reads as a section that passed. This build
	// genuinely did not look, and saying so is the whole difference between a
	// diagnostic and a reassurance.
	tool := newCheckPrerequisitesTool(testProject(t), nil, nil)

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(prerequisitesResult)

	require.Equal(t, verdictUndetermined, out.Verdict)
	require.Len(t, out.NotChecked, 2)
	require.Nil(t, out.Machine)
	require.Nil(t, out.Runner)
	require.Contains(t, out.Summary, "UNDETERMINED")
}

func TestCheckPrerequisites_AnUnwiredMachineCheckAloneStillDegradesTheVerdict(t *testing.T) {
	t.Parallel()
	// With the runner ready and only the machine half unwired, the verdict has
	// to fall to undetermined on the strength of that one gap. A test with both
	// halves unwired cannot tell which one degraded it.
	tool := newCheckPrerequisitesTool(testProject(t), nil,
		func(context.Context) (RunnerReadiness, error) { return readyRunner(), nil })

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(prerequisitesResult)

	require.Equal(t, verdictReady, out.Runner.Verdict, "the half that was checked is ready")
	require.Equal(t, verdictUndetermined, out.Verdict,
		"one unchecked deciding question is enough to stop this claiming the machine is ready")
	require.Len(t, out.NotChecked, 1)
}

func TestCheckPrerequisites_ACheckThatCouldNotRunIsNotCheckedAndNeverAPass(t *testing.T) {
	t.Parallel()
	tool := newCheckPrerequisitesTool(testProject(t),
		func(context.Context) (Diagnosis, error) {
			return Diagnosis{}, errors.New("the prober blew up")
		},
		func(context.Context) (RunnerReadiness, error) { return readyRunner(), nil })

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(prerequisitesResult)

	require.Equal(t, verdictUndetermined, out.Verdict)
	require.Nil(t, out.Machine)
	require.NotEmpty(t, out.NotChecked)
}

func TestCheckPrerequisites_BlockedBeatsUndetermined(t *testing.T) {
	t.Parallel()
	// Both mean do not proceed, and between them the actionable one is the
	// proof. A reader with a real failure in front of them should be told
	// about the failure, not about the question that went unasked beside it.
	tool := newCheckPrerequisitesTool(testProject(t),
		func(context.Context) (Diagnosis, error) {
			return Diagnosis{
				OK: false, Platform: "linux/amd64",
				Checks: []DiagnosticCheck{{
					Name: "docker", Status: "fail",
					Detail: "the daemon is not running", Remediation: "Start Docker Desktop.",
				}},
			}, nil
		},
		nil)

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(prerequisitesResult)

	require.Equal(t, verdictBlocked, out.Verdict)
	require.Contains(t, out.Summary, "BLOCKED")
	require.Contains(t, out.Summary, "docker")
}

func TestCheckPrerequisites_CarriesTheRemediationForEveryFailure(t *testing.T) {
	t.Parallel()
	// A diagnostic that says something is wrong and stops costs the same
	// attention as one that says what to do, and yields nothing.
	tool := newCheckPrerequisitesTool(testProject(t),
		func(context.Context) (Diagnosis, error) {
			return Diagnosis{
				Checks: []DiagnosticCheck{{
					Name: "docker", Status: "fail",
					Detail: "the daemon is not running", Remediation: "Start Docker Desktop.",
				}},
			}, nil
		},
		nil)

	out := mustInvoke(t, tool, `{"project_id":"test-project","scope":"machine"}`).(prerequisitesResult)

	require.Len(t, out.Machine.Blocked, 1)
	require.Equal(t, "Start Docker Desktop.", out.Machine.Blocked[0].Remediation)
}

func TestCheckPrerequisites_ScopeMachineDoesNotTouchTheRunner(t *testing.T) {
	t.Parallel()
	tool := newCheckPrerequisitesTool(testProject(t),
		func(context.Context) (Diagnosis, error) { return passingDiagnosis(), nil },
		func(context.Context) (RunnerReadiness, error) {
			t.Fatal("the runner must not be inspected for the machine scope")
			return RunnerReadiness{}, nil
		})

	out := mustInvoke(t, tool, `{"project_id":"test-project","scope":"machine"}`).(prerequisitesResult)

	require.NotNil(t, out.Machine)
	require.Nil(t, out.Runner)
	require.Empty(t, out.NotChecked, "a scope nobody asked for is not an unchecked thing")
}

func TestMachineVerdict_ASkippedCheckDecidesNothing(t *testing.T) {
	t.Parallel()
	// A skip is a check that does not apply here, and it is reported as
	// exactly that. It neither passes nor blocks: on a Mac packet filtering
	// is skipped on every run, and a verdict that could never be ready there
	// is a verdict nobody reads.
	require.Equal(t, verdictReady, machineVerdict(Diagnosis{
		OK: true, Checks: []DiagnosticCheck{
			{Name: "docker", Status: "ok"},
			{Name: "kernel isolation", Status: "skip"},
		},
	}))
	// OK left true on purpose: the failure alone decides, without help
	// from the doctor's own summary flag.
	require.Equal(t, verdictBlocked, machineVerdict(Diagnosis{
		OK: true, Checks: []DiagnosticCheck{
			{Name: "kernel isolation", Status: "skip"},
			{Name: "docker", Status: "fail"},
		},
	}))
}

func TestCheckPrerequisites_ASkippedCheckIsNeverBlocking(t *testing.T) {
	t.Parallel()
	// What the first tool an agent calls said on 2026-09-06: verdict
	// BLOCKED, and a blocking list of three, two of which were skips whose
	// own remediation read "No action needed". An agent that trusted it
	// stalled on a false block or spent a turn fixing a proxy that was
	// never wrong. Only the failure is in the way; the skip is listed among
	// the checks with its reason and nowhere else.
	tool := newCheckPrerequisitesTool(testProject(t),
		func(context.Context) (Diagnosis, error) {
			return Diagnosis{
				OK: false, Platform: "darwin/arm64",
				Checks: []DiagnosticCheck{
					{Name: "CLI version", Status: "fail",
						Detail:      "1.3.0 is older than the latest release, v1.3.2",
						Remediation: "Run 'af update'."},
					{Name: "Packet filtering", Status: "skip",
						Detail:      "handled inside the Docker virtual machine on this platform",
						Remediation: "No action needed."},
					{Name: "Corporate proxy", Status: "skip",
						Detail: "no proxy variables are set", Remediation: "No action needed."},
				},
			}, nil
		},
		nil)

	out := mustInvoke(t, tool, `{"project_id":"test-project","scope":"machine"}`).(prerequisitesResult)

	require.Equal(t, verdictBlocked, out.Verdict)
	require.Len(t, out.Machine.Blocked, 1)
	require.Equal(t, "CLI version", out.Machine.Blocked[0].Name)
	require.Len(t, out.Machine.Checks, 3, "the skips are still reported, with their reasons")
	require.Equal(t, "skip", out.Machine.Checks[1].Result)
	require.Contains(t, out.Summary, "1 of which is in the way: CLI version")
	require.NotContains(t, out.Summary, "Packet filtering")
}

func TestCheckPrerequisites_OnlySkipsIsReady(t *testing.T) {
	t.Parallel()
	// The same machine with the CLI up to date. Nothing failed, two checks
	// do not apply, and the verdict is ready rather than undetermined,
	// because a skip that carries its own reason is an answer, not a
	// question left open.
	tool := newCheckPrerequisitesTool(testProject(t),
		func(context.Context) (Diagnosis, error) {
			return Diagnosis{
				OK: true, Platform: "darwin/arm64",
				Checks: []DiagnosticCheck{
					{Name: "Docker daemon", Status: "pass"},
					{Name: "Packet filtering", Status: "skip",
						Detail: "handled inside the Docker virtual machine on this platform"},
				},
			}, nil
		},
		nil)

	out := mustInvoke(t, tool, `{"project_id":"test-project","scope":"machine"}`).(prerequisitesResult)

	require.Equal(t, verdictReady, out.Verdict)
	require.Empty(t, out.Machine.Blocked)
	require.NotContains(t, out.Summary, "in the way")
}

func TestMachineVerdict_AStatusThisPackageDoesNotKnowIsNotAPass(t *testing.T) {
	t.Parallel()
	// The four words are closed on purpose. A new word on the other side of
	// the boundary must not arrive here looking like a pass to something
	// branching on the string.
	require.Equal(t, verdictUndetermined, machineVerdict(Diagnosis{
		OK: true, Checks: []DiagnosticCheck{{Name: "something", Status: "degraded"}},
	}))
	require.Equal(t, "unknown", normaliseResult("degraded"))
}

func TestWorseVerdict_BlockedWinsAndReadyLoses(t *testing.T) {
	t.Parallel()
	require.Equal(t, verdictBlocked, worseVerdict(verdictUndetermined, verdictBlocked))
	require.Equal(t, verdictBlocked, worseVerdict(verdictBlocked, verdictUndetermined))
	require.Equal(t, verdictUndetermined, worseVerdict(verdictReady, verdictUndetermined))
	require.Equal(t, verdictReady, worseVerdict(verdictReady, verdictReady))
}

func TestCheckPrerequisitesTool_IsReadOnly(t *testing.T) {
	t.Parallel()
	tool := newCheckPrerequisitesTool(testProject(t), nil, nil)
	require.True(t, tool.ReadOnly, "it runs local probes and changes nothing")
	require.False(t, tool.Destructive)
}

func TestCheckPrerequisites_PointsAtASupportBundleWithoutOfferingOne(t *testing.T) {
	t.Parallel()
	// A bundle carries the application's own logs and every outbound request
	// it made. That is not content to put through a model's context, so the
	// tool names the command and does not collect one.
	tool := newCheckPrerequisitesTool(testProject(t), nil, nil)

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(prerequisitesResult)

	require.Contains(t, out.Note, "af support bundle")
	require.Contains(t, out.Note, "not offered as a tool")
}

// ---------------------------------------------------------------------------
// describe_control_plane_account
// ---------------------------------------------------------------------------

func TestDescribeAccount_NotSignedInIsANormalAnswer(t *testing.T) {
	t.Parallel()
	// Most of this product works with no control plane at all, so reporting a
	// failure here would send somebody to fix something that is not broken.
	tool := newDescribeAccountTool(testProject(t),
		func(context.Context, bool) (Account, error) {
			return Account{ControlPlane: "https://app.antifailure.dev"}, nil
		})

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(accountResult)

	require.False(t, out.SignedIn)
	require.Contains(t, out.Summary, "not signed in")
	require.Contains(t, out.Summary, "af login")
}

func TestDescribeAccount_ReportsTheCapabilitiesTheCredentialCarries(t *testing.T) {
	t.Parallel()
	// When something is refused, the reason is a missing capability or a
	// lapsed sign in, and guessing which is a wasted round trip.
	tool := newDescribeAccountTool(testProject(t),
		func(context.Context, bool) (Account, error) {
			return Account{
				SignedIn: true, ControlPlane: "https://app.antifailure.dev",
				Login: "ada", Organization: "acme", Role: "admin",
				Scopes: []string{"providers.write", "environments.read"},
			}, nil
		})

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(accountResult)

	require.True(t, out.SignedIn)
	require.Equal(t, []string{"environments.read", "providers.write"}, out.Scopes)
	require.Contains(t, out.Summary, "providers.write")
}

func TestDescribeAccount_AMissingCapabilityDoesNotFailTheWholeCall(t *testing.T) {
	t.Parallel()
	// A credential without the capability to read model keys is a perfectly
	// ordinary credential. Refusing to say who somebody is because of it would
	// be the wrong trade.
	tool := newDescribeAccountTool(testProject(t),
		func(_ context.Context, includeProviders bool) (Account, error) {
			require.True(t, includeProviders)
			return Account{
				SignedIn: true, ControlPlane: "https://app.antifailure.dev",
				Login: "ada", Organization: "acme",
				Providers: &ProviderSpend{
					Unavailable: "this credential does not carry the capability",
				},
			}, nil
		})

	out := mustInvoke(t, tool,
		`{"project_id":"test-project","include_model_providers":true}`).(accountResult)

	require.True(t, out.SignedIn)
	require.NotNil(t, out.Providers)
	require.NotEmpty(t, out.Providers.Unavailable)
	require.Contains(t, out.Summary, "could not be read")
}

func TestDescribeAccount_SaysWhenAProviderCanSpendNothingMore(t *testing.T) {
	t.Parallel()
	// A cap with nothing left is the state a run is refused in, before the key
	// is even decrypted.
	tool := newDescribeAccountTool(testProject(t),
		func(context.Context, bool) (Account, error) {
			return Account{
				SignedIn: true, ControlPlane: "https://app.antifailure.dev",
				Login: "ada", Organization: "acme",
				Providers: &ProviderSpend{
					Sealing: true,
					Keys:    []ProviderKeyState{{Provider: "anthropic", Last4: "1a2b"}},
					Budgets: []ProviderBudgetState{{
						Provider: "anthropic", Period: "2026-09",
						CapUSD: 50, SpentUSD: 50, RemainingUSD: 0,
					}},
				},
			}, nil
		})

	out := mustInvoke(t, tool,
		`{"project_id":"test-project","include_model_providers":true}`).(accountResult)

	require.True(t, out.Providers.Budgets[0].Exhausted)
	require.Contains(t, out.Summary, "spend nothing more")
	require.Contains(t, out.Summary, "nothing on this server can raise one")
}

func TestDescribeAccount_ProvidersAreNotFetchedUnlessAsked(t *testing.T) {
	t.Parallel()
	tool := newDescribeAccountTool(testProject(t),
		func(_ context.Context, includeProviders bool) (Account, error) {
			require.False(t, includeProviders,
				"the second request must not be made unless the caller asked for it")
			return Account{SignedIn: true, ControlPlane: "https://app.antifailure.dev"}, nil
		})

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(accountResult)
	require.Nil(t, out.Providers)
}

func TestDescribeAccount_TheWholeResultCarriesNoCredential(t *testing.T) {
	t.Parallel()
	tool := newDescribeAccountTool(testProject(t),
		func(context.Context, bool) (Account, error) {
			return Account{
				SignedIn: true, ControlPlane: "https://app.antifailure.dev",
				Login: "ada", Organization: "acme",
				// A prefix identifies which credential this is without being
				// one. A whole token in that field is not a prefix.
				TokenPrefix: "af_eng_",
				StoredIn:    "the operating system keyring holding " + theToken,
				Providers: &ProviderSpend{
					Sealing: true,
					Keys: []ProviderKeyState{{
						Provider: "anthropic", Last4: "1a2b", Fingerprint: "9f2a1c",
					}},
				},
			}, nil
		})

	out := mustInvoke(t, tool,
		`{"project_id":"test-project","include_model_providers":true}`).(accountResult)
	raw, err := json.Marshal(out)
	require.NoError(t, err)

	require.NotContains(t, string(raw), theToken,
		"nothing this server returns may carry a credential, however it got into a field")
	require.Contains(t, string(raw), "af_eng_",
		"the prefix identifies which credential this is and must survive the scrub")
}

func TestScrubberFor_RemovesASecretNoPatternRuleCouldRecognise(t *testing.T) {
	t.Parallel()
	// A real engine token and a model key for a self hosted endpoint have no
	// prefix a rule names, so the pattern rules alone cannot find them. The
	// adapter holding one registers it, which is the only thing that can.
	require.Contains(t, safeText(
		"the keyring holding "+theShapelessSecret, 400), theShapelessSecret,
		"the shared redactor genuinely cannot recognise this shape, which is why the "+
			"adapter has to register it")

	scrubbed := scrubberFor(theShapelessSecret).String(
		"the keyring holding " + theShapelessSecret)
	require.NotContains(t, scrubbed, theShapelessSecret)
	require.Contains(t, scrubbed, "the keyring holding")
}

func TestSafeText_RemovesARecognisableCredentialFromSomebodyElsesProse(t *testing.T) {
	t.Parallel()
	// The engine already redacts a model key out of a provider's message. This
	// is what still holds if it ever stops, and it needs nothing configured.
	out := safeText("the provider refused "+theToken, 400)
	require.NotContains(t, out, theToken)
	require.Contains(t, out, "the provider refused")
}

func TestDescribeAccountTool_IsReadOnlyAndExposesNoWayToChangeACap(t *testing.T) {
	t.Parallel()
	// A cap is a threshold. A tool that let a model raise its own ceiling
	// would be the one kind of argument this server refuses to have.
	tool := newDescribeAccountTool(testProject(t),
		func(context.Context, bool) (Account, error) { return Account{}, nil })
	require.True(t, tool.ReadOnly)

	for _, field := range []string{
		"cap_usd", "budget", "set_budget", "provider", "token", "scope", "control_plane",
	} {
		_, fault := invoke(t, tool, `{"project_id":"test-project","`+field+`":"50"}`)
		require.NotNil(t, fault, "the field %q must not be accepted", field)
		require.Equal(t, FaultUnknownField, fault.Code, "field %q", field)
	}
}

func TestOpsToolsRequireTheProjectAssertion(t *testing.T) {
	t.Parallel()
	p := testProject(t)
	for _, tool := range []*Tool{
		newCheckPrerequisitesTool(p,
			func(context.Context) (Diagnosis, error) { return passingDiagnosis(), nil },
			func(context.Context) (RunnerReadiness, error) { return readyRunner(), nil }),
		newDescribeAccountTool(p,
			func(context.Context, bool) (Account, error) { return Account{}, nil }),
	} {
		require.Contains(t, tool.Input.Required, "project_id", "tool %s", tool.Name)

		_, fault := invoke(t, tool, `{"project_id":"some-other-project"}`)
		require.NotNil(t, fault, "tool %s answered a call naming another project", tool.Name)
		require.Equal(t, FaultProjectMismatch, fault.Code, "tool %s", tool.Name)
	}
}

func TestOpsToolsBoundEveryArgument(t *testing.T) {
	t.Parallel()
	p := testProject(t)
	for _, tool := range []*Tool{
		newCheckPrerequisitesTool(p, nil, nil),
		newDescribeAccountTool(p, nil),
	} {
		requireBounded(t, tool.Name, tool.Input)
	}
}

// ---------------------------------------------------------------------------
// what the published tool list actually says
// ---------------------------------------------------------------------------

func TestPublishedAnnotations_ADestructiveToolSaysSoAndAReadOnlyOneDoesNot(t *testing.T) {
	t.Parallel()
	// The annotation is what a client uses to decide whether to stop and ask a
	// person, so declaring a field is not enough: it has to reach the wire. A
	// tool that deletes environments and publishes destructiveHint false is
	// this server telling a client it is safe to run unattended.
	p := testProject(t)
	s := NewServer(p.ID, nil, io.Discard)
	s.Register(newRemoveExpiredEnvironmentsTool(p, (&sweepRecorder{}).sweep))
	s.Register(newRemoveOldGoldensTool(p, nil, nil, nil))
	s.Register(newCheckPrerequisitesTool(p, nil, nil))
	s.Register(newDescribeModelKeyTool(p, nil))
	s.Register(newVerifyModelKeyTool(p, nil))
	s.Register(newSendWebhookEventTool(p, nil))
	// The other two destructive tools live in sibling files and are registered
	// here so the assertion below holds for every tool that publishes the hint.
	s.Register(newTeardownTool(p, nil, nil))
	s.Register(newApplyMaskingTool(p, nil, nil))

	got := converse(t, s, initFrame,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	require.Len(t, got, 2)

	published := map[string]map[string]any{}
	list, ok := got[1]["result"].(map[string]any)["tools"].([]any)
	require.True(t, ok, "no tool list in %v", got[1])
	for _, entry := range list {
		tool := entry.(map[string]any)
		published[tool["name"].(string)] = tool["annotations"].(map[string]any)
	}

	for _, name := range []string{"remove_expired_environments", "remove_old_goldens", "teardown_environment", "apply_data_masking"} {
		require.True(t, published[name]["destructiveHint"].(bool),
			"%s deletes things a caller owns and must publish destructiveHint true", name)
		require.False(t, published[name]["readOnlyHint"].(bool), name)
	}
	for _, name := range []string{"check_prerequisites", "describe_model_key"} {
		require.False(t, published[name]["destructiveHint"].(bool), name)
		require.True(t, published[name]["readOnlyHint"].(bool), name)
	}
	for _, name := range []string{"verify_model_key", "send_webhook_event"} {
		require.False(t, published[name]["readOnlyHint"].(bool),
			"%s changes something and must not be published as read only", name)
		require.False(t, published[name]["destructiveHint"].(bool),
			"%s removes nothing a caller owns", name)
	}
}

func TestPublishedSchemas_RefuseUnknownMembers(t *testing.T) {
	t.Parallel()
	// The published document says additionalProperties is false, so the
	// validator has to agree with it. A schema that promises a closed object
	// and a validator that accepts anything has told a caller a promise the
	// server does not keep.
	p := testProject(t)
	s := NewServer(p.ID, nil, io.Discard)
	s.Register(newRemoveExpiredEnvironmentsTool(p, (&sweepRecorder{}).sweep))

	got := converse(t, s, initFrame,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	list := got[1]["result"].(map[string]any)["tools"].([]any)
	schema := list[0].(map[string]any)["inputSchema"].(map[string]any)

	require.Equal(t, false, schema["additionalProperties"])
	require.Contains(t, schema["required"], "project_id")
}
