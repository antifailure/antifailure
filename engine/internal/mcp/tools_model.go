package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/auth"
	"github.com/antifailure/antifailure/engine/internal/controlplane"
	"github.com/antifailure/antifailure/engine/internal/model"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// The model key decides whether the agents that drive a browser can reason at
// all, and getting it wrong is discovered twenty minutes into a run.
//
// NOTHING HERE READS, RETURNS OR STORES A KEY. There is no tool below that
// takes a key, none that removes one, and no field on any result that could
// carry one: the state a caller gets is a provider name, an endpoint, where
// the key was found and a fingerprint of it. Storing and removing a key are
// deliberately absent from this server, because a key that passes through a
// model's context is a key in a transcript, and af model set already refuses
// to let one reach even a shell's argument vector.

// modelKeyState is what a caller may know about the configured key.
//
// It is a distinct type from model.Config rather than a rendering of one, and
// that is the control. model.Config holds the key. This struct has no field
// that could, so no future edit to a summary or a document can accidentally
// put one in a result.
type modelKeyState struct {
	// Configured is false when no key was found anywhere, which is a
	// supported mode and not a failure: runs use the deterministic planner.
	Configured bool
	Provider   string
	Model      string
	BaseURL    string
	// Custom reports an endpoint that is not the provider's own.
	Custom bool
	// Capped reports that calls go through a control plane, where a monthly
	// cap is checked before the sealed key is decrypted.
	Capped bool
	// UncappedControlPlane names a control plane this machine is signed in to
	// whose cap is NOT in force, because this key goes straight to the
	// provider. It is the quiet expensive case.
	UncappedControlPlane string
	// Source names where the key was found, never what it is.
	Source string
	// Fingerprint is short and not reversible. It answers "is this the key I
	// think it is" without either person reading a secret out loud.
	Fingerprint string
	// VerifiedAt is when a probe last proved this exact key works. Zero when
	// it never has, or when the key has changed since.
	VerifiedAt time.Time
	// Shadowing names a lower priority source that also holds a key, which is
	// what answers "why is the key I just set not the one being used".
	Shadowing string
	// Searched names every place that was asked, in the order they are asked,
	// so a "not configured" answer says where a key could go.
	Searched []string
}

// readModelKey reports the configured key without revealing it.
type readModelKey func(ctx context.Context) (modelKeyState, error)

// modelProbeState is one real call to the provider.
type modelProbeState struct {
	// Configured is false when there was no key to test, which is not a
	// failure and is reported as such.
	Configured bool
	OK         bool
	// Outcome tells the failures apart: key-rejected, no-credit,
	// unknown-model, rate-limited, provider-down, unreachable, timed-out and
	// unreadable all fail, and they all have different fixes.
	Outcome    string
	Provider   string
	Model      string
	BaseURL    string
	Status     int
	Latency    time.Duration
	Detail     string
	NextStep   string
	VerifiedAt time.Time
}

// probeModelKey sends one cheap completion and reports what came back.
type probeModelKey func(ctx context.Context, timeout time.Duration) (modelProbeState, error)

// ---------------------------------------------------------------------------
// describe_model_key
// ---------------------------------------------------------------------------

type modelKeyResult struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	// Planner is what a run will actually use: model, or deterministic. It is
	// the question this is really asked to answer.
	Planner    string `json:"planner"`
	Configured bool   `json:"configured"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
	Endpoint   string `json:"endpoint,omitempty"`
	// CustomEndpoint reports an endpoint that is not the provider's own.
	CustomEndpoint bool `json:"custom_endpoint,omitempty"`
	// Capped reports whether a monthly spending cap applies to this key.
	Capped bool `json:"capped_by_control_plane"`
	// UncappedControlPlane names a control plane whose cap this key bypasses.
	UncappedControlPlane string `json:"uncapped_despite_control_plane,omitempty"`
	// Source is where the key was found. Never what it is.
	Source string `json:"source,omitempty"`
	// Fingerprint identifies the key without revealing it.
	Fingerprint string `json:"fingerprint,omitempty"`
	// LastVerified is when a probe last proved this exact key works.
	LastVerified string `json:"last_verified,omitempty"`
	// AlsoSetIn names a second, lower priority copy that is not the one being
	// used.
	AlsoSetIn string   `json:"also_set_in,omitempty"`
	Searched  []string `json:"searched"`
	// KeyNote states the invariant rather than leaving it to be trusted.
	KeyNote string `json:"key_note"`
}

const modelKeyNote = "No tool on this server reads, returns or stores a model key. This " +
	"result carries a fingerprint and the name of the place the key was found, and there " +
	"is no argument anywhere that would produce the key itself."

func newDescribeModelKeyTool(p *Project, read readModelKey) *Tool {
	return &Tool{
		Name:     "describe_model_key",
		Title:    "See the model key configuration",
		ReadOnly: true,
		Description: "Report whether a model key is configured for the agents that drive a " +
			"browser, which provider and model a run would use, which endpoint it would " +
			"call, where the key was found, and whether a monthly spending cap actually " +
			"applies to it. Call this before a long run rather than discovering the answer " +
			"partway through one. No key is configured is a supported answer and not a " +
			"failure: runs fall back to a deterministic planner, which still drives a real " +
			"browser and still produces a verdict. This never reveals the key and there is " +
			"no argument that would; what it gives instead is a fingerprint, which answers " +
			"whether the key here is the one you think it is.",
		Input: &Schema{
			Type:       "object",
			Required:   []string{"project_id"},
			Properties: map[string]*Schema{"project_id": projectIDSchema()},
		},
		Handler: func(ctx context.Context, _ *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			state, err := read(ctx)
			if err != nil {
				return nil, &Fault{
					Code: FaultSafetyUnavailable,
					Detail: "The places a model key can be stored could not be read, so this " +
						"says nothing about what a run would use.",
					Retryable: true, wrapped: err,
				}
			}
			return describeModelKey(state), nil
		},
	}
}

func describeModelKey(state modelKeyState) modelKeyResult {
	out := modelKeyResult{
		Kind: "model_key", Configured: state.Configured, Planner: "deterministic",
		KeyNote: modelKeyNote, Searched: []string{},
	}
	for i, s := range state.Searched {
		if i >= 20 {
			break
		}
		out.Searched = append(out.Searched, safeText(s, 120))
	}
	if !state.Configured {
		out.Summary = "No model key is configured, so runs use the deterministic planner. " +
			"That is a supported mode: workflows still run, still drive a real browser and " +
			"still produce a verdict. What a key adds is turning a workflow written as a " +
			"sentence into one the runner follows without being told every field. The " +
			"searched list is where one can go, in the order they are asked."
		return out
	}

	out.Planner = "model"
	out.Provider, _ = safeIdentifier(state.Provider)
	out.Model = safeText(state.Model, 120)
	out.Endpoint = safeHostURL(state.BaseURL)
	out.CustomEndpoint = state.Custom
	out.Capped = state.Capped
	out.UncappedControlPlane = safeHostURL(state.UncappedControlPlane)
	out.Source = safeText(state.Source, 120)
	out.Fingerprint = safeText(state.Fingerprint, 64)
	out.AlsoSetIn = safeText(state.Shadowing, 120)
	if !state.VerifiedAt.IsZero() {
		out.LastVerified = state.VerifiedAt.UTC().Format(time.RFC3339)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "A run would use %s with %s, from %s. ",
		out.Provider, orNotRecorded(out.Model), orNotRecorded(out.Source))
	switch {
	case state.Capped:
		b.WriteString("Calls go through a control plane, so the monthly cap on that " +
			"provider is checked before the sealed key is decrypted. ")
	case out.UncappedControlPlane != "":
		fmt.Fprintf(&b, "Calls go STRAIGHT TO THE PROVIDER, so any monthly cap set on %s "+
			"is not in force for this key and there is no ceiling on what a run can spend. ",
			out.UncappedControlPlane)
	default:
		b.WriteString("Calls go straight to the provider, with no monthly cap. ")
	}
	if out.AlsoSetIn != "" {
		fmt.Fprintf(&b, "A second key is also set in %s and is NOT the one being used, "+
			"which is what to look at if a key that was just changed made no difference. ",
			out.AlsoSetIn)
	}
	if out.LastVerified == "" {
		b.WriteString("This exact key has never been proven to work here; " +
			"verify_model_key proves it with one cheap call.")
	} else {
		fmt.Fprintf(&b, "This exact key was last proven to work at %s.", out.LastVerified)
	}
	out.Summary = strings.TrimSpace(b.String())
	return out
}

// ---------------------------------------------------------------------------
// verify_model_key
// ---------------------------------------------------------------------------

type modelProbeResult struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	OK      bool   `json:"ok"`
	// Outcome names which failure it was, because a revoked key, an empty
	// balance, a model name that does not exist, a throttle, a provider
	// outage and an endpoint nothing answers on have different fixes and
	// being told only that the call failed sends you to the wrong one first.
	Outcome    string  `json:"outcome"`
	Provider   string  `json:"provider,omitempty"`
	Model      string  `json:"model,omitempty"`
	Endpoint   string  `json:"endpoint,omitempty"`
	Status     int     `json:"status,omitempty"`
	LatencyMS  float64 `json:"latency_ms"`
	Detail     string  `json:"detail,omitempty"`
	NextStep   string  `json:"next_step,omitempty"`
	VerifiedAt string  `json:"verified_at,omitempty"`
	KeyNote    string  `json:"key_note"`
}

// maxProbeSeconds bounds the wait. A local model needs more than a hosted one,
// and nothing needs two minutes.
const maxProbeSeconds = 120

func newVerifyModelKeyTool(p *Project, probe probeModelKey) *Tool {
	return &Tool{
		Name:  "verify_model_key",
		Title: "Prove the model key works",
		// Not read only. It makes a real call to the provider, which costs a
		// fraction of a cent and counts against the account's limits, and it
		// records the result on this machine. Not destructive: it removes
		// nothing.
		ReadOnly: false,
		Description: "Prove the configured model key actually works, by sending one real " +
			"completion of a single token to the provider. IT SPENDS MONEY: a fraction of a " +
			"cent, billed to whoever owns the configured key, and the call counts against " +
			"that account's rate limits. That is why it is not marked read only, and it is " +
			"why timeout_seconds has a ceiling rather than being open ended. Use it before " +
			"a long run, or when a run failed in a way that might " +
			"be the key. A real call rather than a check of the key's shape, because a well " +
			"formed key that was revoked this morning passes every shape check there is. It " +
			"tells the failures apart: a rejected key, an empty balance, a model name the " +
			"endpoint does not serve, a throttle, a provider outage and an endpoint nothing " +
			"answers on are reported as different outcomes because they have different " +
			"fixes. No key is configured is reported plainly and is not a failure. The key " +
			"itself is never revealed.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id": projectIDSchema(),
				"timeout_seconds": {
					Type: "integer", HasMin: true, Minimum: 1, HasMax: true, Maximum: maxProbeSeconds,
					Description: "Optional. How long to wait for the endpoint, in seconds. " +
						"Thirty is the default and is ample for a hosted provider; a model " +
						"running on this machine may need more.",
				},
			},
		},
		Handler: func(ctx context.Context, _ *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			timeout := 30 * time.Second
			if raw, ok := args["timeout_seconds"]; ok {
				n, err := toInt(raw)
				if err != nil {
					return nil, fieldFault(FaultInvalidArgument, "timeout_seconds",
						"This field must be a whole number of seconds.")
				}
				timeout = time.Duration(n) * time.Second
			}

			state, err := probe(ctx, timeout)
			if err != nil {
				return nil, &Fault{
					Code: FaultSafetyUnavailable,
					Detail: "The key could not be looked up, so no call was made and this " +
						"says nothing about whether it works.",
					Retryable: true, wrapped: err,
				}
			}
			return describeModelProbe(state), nil
		},
	}
}

func describeModelProbe(state modelProbeState) modelProbeResult {
	out := modelProbeResult{Kind: "model_probe", KeyNote: modelKeyNote}
	if !state.Configured {
		out.Outcome = "not-configured"
		out.Summary = "No model key is configured, so there was nothing to test and no " +
			"call was made. Runs use the deterministic planner, which needs no key. " +
			"describe_model_key lists the places a key can go."
		return out
	}

	out.OK = state.OK
	out.Outcome, _ = safeIdentifier(state.Outcome)
	out.Provider, _ = safeIdentifier(state.Provider)
	out.Model = safeText(state.Model, 120)
	out.Endpoint = safeHostURL(state.BaseURL)
	out.Status = state.Status
	out.LatencyMS = float64(state.Latency.Milliseconds())
	// The provider's own words, which are the useful part of a failure and
	// are neither this engine's prose nor the candidate repository's. The
	// engine already redacts the key out of them; they are bounded and
	// neutralised here because they are still somebody else's text.
	out.Detail = safeText(state.Detail, 400)
	out.NextStep = safeText(state.NextStep, 300)
	if !state.VerifiedAt.IsZero() {
		out.VerifiedAt = state.VerifiedAt.UTC().Format(time.RFC3339)
	}

	if state.OK {
		out.Summary = fmt.Sprintf(
			"The key works. %s answered as %s in %.0fms, and this exact key is now recorded "+
				"as proven so describe_model_key can say so.",
			orNotRecorded(out.Provider), orNotRecorded(out.Model), out.LatencyMS)
		return out
	}
	out.Summary = fmt.Sprintf(
		"The key does not work: %s. A run needing the model would fail this way partway "+
			"through. %s",
		orNotRecorded(out.Outcome), orNotRecorded(out.NextStep))
	return out
}

// ---------------------------------------------------------------------------
// the adapters
// ---------------------------------------------------------------------------
//
// These are methods on the factory that serve.go builds, so that the tools
// above can be driven by a fake in a test while the server drives them against
// the real chain. Nothing here is reachable from a tool argument.

// modelChain is the chain a key is looked up in.
//
// The same one the CLI resolves a model key against, and the same one af up
// resolves DATABASE_URL against. There is one precedence rule in this product:
// an export beats .env, .env beats the encrypted store, the store beats the
// keyring, and anything an enterprise build registered comes last. A second
// ordering invented here would be a second thing to get wrong.
func (f *orchestratorFactory) modelChain() *secrets.Chain {
	return secrets.LocalChain(
		f.project.Root, f.cfg.Getenv, extension.Default, secrets.NewSystemKeyring())
}

// modelKey resolves the key and reports everything about it except the key.
func (f *orchestratorFactory) modelKey(ctx context.Context) (modelKeyState, error) {
	chain := f.modelChain()
	cfg, err := model.Resolve(ctx, chain)
	if err != nil {
		return modelKeyState{}, err
	}
	state := modelKeyState{Searched: chain.Considered(ctx)}
	if cfg == nil {
		return state, nil
	}
	// The key this call is holding, registered so that it is replaced wherever
	// it appears in what follows. A key for a self hosted endpoint has no
	// recognisable prefix, so the pattern rules alone cannot find one.
	scrub := scrubberFor(cfg.Key.Reveal()).String

	state.Configured = true
	state.Provider = cfg.Provider.Name
	state.Model = scrub(cfg.Model)
	state.BaseURL = scrub(cfg.BaseURL)
	state.Custom = cfg.Custom()
	state.Capped = cfg.ThroughControlPlane()
	state.Source = scrub(cfg.Source)
	state.Fingerprint = cfg.Fingerprint
	state.Shadowing = scrub(f.shadowedBy(ctx, chain, cfg))
	state.UncappedControlPlane = f.uncappedControlPlane(cfg)
	if record := model.ReadRecord(f.project.Root, cfg.Fingerprint); record != nil {
		state.VerifiedAt = record.VerifiedAt
	}
	return state, nil
}

// shadowedBy names a lower priority source that also holds a key.
//
// Asked directly rather than guessed, so this reports a real second copy
// rather than the possibility of one.
func (f *orchestratorFactory) shadowedBy(
	ctx context.Context, chain *secrets.Chain, cfg *model.Config,
) string {
	found := false
	for _, source := range chain.Sources(ctx) {
		if source == cfg.Source {
			found = true
			continue
		}
		if !found {
			continue
		}
		if value, _, ok, err := chain.LookupIn(ctx, source, cfg.Provider.KeyVar); err == nil && ok {
			if strings.TrimSpace(value.Reveal()) != "" {
				return source
			}
		}
	}
	return ""
}

// uncappedControlPlane names a control plane whose cap this key is bypassing.
//
// The failure it exists for is quiet and expensive. Somebody stores a key on a
// control plane and sets a monthly cap, and believes they have a ceiling.
// Nothing routes a run through the control plane on its own, so a local key
// sends every run straight to the provider with no ceiling at all, and the
// only evidence is a bill at the end of the month.
func (f *orchestratorFactory) uncappedControlPlane(cfg *model.Config) string {
	if cfg.ThroughControlPlane() {
		return ""
	}
	origin := f.controlPlaneOrigin()
	cred, err := auth.NewStore().Load(origin)
	if err != nil || cred.Expired(f.cfg.Clock.Now()) {
		// Not signed in, or signed in with something that has lapsed. Neither
		// is a state where a cap could have been in force, so there is
		// nothing to report and saying so anyway would be noise on every
		// machine that has never seen a control plane.
		return ""
	}
	return origin
}

// controlPlaneOrigin resolves which control plane this machine talks to.
//
// The same order the CLI resolves it in: an explicit environment variable
// first, because somebody exporting one is deliberately overriding what is on
// the machine, then the hosted instance. There is no flag here, because a tool
// argument that could point this server at another control plane would be an
// argument that widens what a call reaches.
func (f *orchestratorFactory) controlPlaneOrigin() string {
	if v := strings.TrimSpace(f.cfg.Getenv("AF_CONTROL_PLANE_URL")); v != "" {
		return auth.Normalise(v)
	}
	return auth.Normalise(controlplane.DefaultBaseURL)
}

// probeModel sends one cheap completion and records a success.
func (f *orchestratorFactory) probeModel(
	ctx context.Context, timeout time.Duration,
) (modelProbeState, error) {
	cfg, err := model.Resolve(ctx, f.modelChain())
	if err != nil {
		return modelProbeState{}, err
	}
	if cfg == nil {
		return modelProbeState{}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result := model.Probe(ctx, airgap.Client(airgap.SiteModelProbe, timeout), *cfg, f.cfg.Clock.Now)
	// Detail is the provider's own words about a key it just rejected, which
	// is the one place in this file most likely to quote the key back. The
	// model package already removes it; this is what still holds if it stops.
	scrub := scrubberFor(cfg.Key.Reveal()).String
	state := modelProbeState{
		Configured: true, OK: result.OK(), Outcome: string(result.Outcome),
		Provider: cfg.Provider.Name, Model: scrub(cfg.Model), BaseURL: scrub(cfg.BaseURL),
		Status: result.Status, Latency: result.Latency,
		Detail: scrub(result.Detail), NextStep: scrub(result.NextStep),
	}
	if result.OK() {
		now := f.cfg.Clock.Now()
		if err := model.WriteRecord(f.project.Root, *cfg, now); err == nil {
			state.VerifiedAt = now
		}
		// A record that could not be written is not a failed probe. The call
		// succeeded, which is what was asked, and failing here would report a
		// working key as broken.
	}
	return state, nil
}
