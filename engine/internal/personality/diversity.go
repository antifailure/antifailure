// Resolving a diversity block into a concrete, replayable per-agent plan.
//
// This is the engine's half of the personality engine. The manifest declares
// how many personality varied agents drive each workflow, which personalities
// may be drawn, and a seed. This turns that declaration into a fixed list of
// assignments: for every (workflow, agent index) it names the personality that
// drives it and the behavioral profile layered on top, both derived only from
// the seed, the workflow name, and the agent index. The runner consumes the
// list; it never draws anything itself, so the run replays step for step the
// way an exploration from a seed does.
//
// Two layers of variance, ported from Crowdi and kept orthogonal. The
// PERSONALITY (from the catalogue) is the reasoning lens: patience, what it
// attends to, how it phrases a decision. The PROFILE (computed here) is a
// second spread over strategy, risk, pacing, attention, error response, and
// cognitive style, so two agents drawn as the same personality still diverge.
// generateDiversityProfiles and buildDiversityDiagnostics port directly.
package personality

import (
	"fmt"
	"math"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Spec is the resolved personality data the runner needs: an id to label the
// result with, a name for the report, and the reasoning preamble that is the
// personality's whole effect on the model.
type Spec struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ReasoningPrompt string `json:"reasoningPrompt"`
}

// Profile is the second, computed layer of behavioral variance. Every field is
// read: the runner composes the second line of its preamble from pacing, risk,
// attention, and error response, and the report shows the diagnostics built
// from these keys.
type Profile struct {
	PersonaID          string  `json:"personaId"`
	StrategyArchetype  string  `json:"strategyArchetype"`
	RiskStyle          string  `json:"riskStyle"`
	PacingStyle        string  `json:"pacingStyle"`
	AttentionBias      string  `json:"attentionBias"`
	ErrorResponseStyle string  `json:"errorResponseStyle"`
	CognitiveStyle     string  `json:"cognitiveStyle"`
	NoveltyBias        float64 `json:"noveltyBias"`
	Seed               uint32  `json:"seed"`
	ProfileKey         string  `json:"profileKey"`
}

// Assignment is one personality varied agent driving one workflow.
type Assignment struct {
	Workflow    string  `json:"workflow"`
	AgentIndex  int     `json:"agentIndex"`
	Personality Spec    `json:"personality"`
	Profile     Profile `json:"profile"`
}

// Diagnostics summarise how much behavioral spread a plan actually achieved.
// Ported from buildDiversityDiagnostics.
type Diagnostics struct {
	UniquenessScore float64 `json:"uniquenessScore"`
	StrategyCount   int     `json:"strategyCount"`
	PersonaCount    int     `json:"personaCount"`
}

// Resolved is the whole plan the engine sends to the runner and echoes into
// the report.
type Resolved struct {
	Seed        string                  `json:"seed"`
	Assignments map[string][]Assignment `json:"assignments"`
	Diagnostics Diagnostics             `json:"diagnostics"`
}

// WorkflowRef is the little the resolver needs about a workflow: its name and
// any pinned personality. Kept minimal so this package does not depend on the
// whole workflow type.
type WorkflowRef struct {
	Name        string
	Personality string
}

// pools of the second variance layer, in the order Crowdi lists them.
var (
	strategies   = []string{"explorer", "task_finisher", "skeptic", "speed_runner", "accessibility"}
	risks        = []string{"conservative", "balanced", "risky"}
	pacings      = []string{"slow", "medium", "fast"}
	attentions   = []string{"visual_heavy", "text_heavy", "cta_focused", "navigation_focused"}
	errorStyles  = []string{"retry_heavy", "reroute_fast", "diagnostic"}
	cognitiveSet = []string{"technical_engineer", "gen_z", "business_operator", "careful_researcher", "accessibility_advocate"}
)

// Resolve turns a diversity block and the workflows into a fixed per-agent
// plan. A nil or disabled block yields an empty plan, which the caller reads as
// today's single neutral agent per workflow.
//
// defaultSeed is used when the block names no seed, and is the run id, so a run
// with no explicit seed still replays within itself and records the seed it
// used.
func Resolve(d *schema.Diversity, workflows []WorkflowRef, defaultSeed string) Resolved {
	if d == nil || !d.Enabled {
		return Resolved{}
	}
	seed := d.Seed
	if seed == "" {
		seed = defaultSeed
	}
	agents := d.AgentsPerWorkflow
	if agents < 1 {
		agents = 1
	}
	pool := buildPool(d.Personalities)
	mix := d.Mix
	if mix == "" {
		mix = schema.MixBalanced
	}
	variance := d.Variance
	if variance == "" {
		variance = schema.VarianceMedium
	}

	out := Resolved{Seed: seed, Assignments: map[string][]Assignment{}}
	var all []Assignment
	for _, w := range workflows {
		list := make([]Assignment, 0, agents)
		for i := 0; i < agents; i++ {
			def := pickPersonality(pool, w.Personality, seed, w.Name, i)
			profile := profileFor(def.ID, i, seed, w.Name, mix, variance)
			list = append(list, Assignment{
				Workflow:   w.Name,
				AgentIndex: i,
				Personality: Spec{
					ID:              def.ID,
					Name:            def.Name,
					ReasoningPrompt: def.ReasoningPrompt,
				},
				Profile: profile,
			})
		}
		out.Assignments[w.Name] = list
		all = append(all, list...)
	}
	out.Diagnostics = diagnostics(all)
	return out
}

// weighted pairs a built in with the weight it is drawn at.
type weighted struct {
	def    Definition
	weight float64
}

// buildPool resolves the manifest's personality selection into a weighted pool.
// An empty selection means every built in at its own population weight; an
// entry with zero weight falls back to the built in's own; an unknown id is
// skipped, because the manifest validator already refuses one and skipping is
// the safe answer if it somehow reached here. If nothing survives, the whole
// catalogue is the pool, so a plan is always producible.
func buildPool(sel []schema.Personality) []weighted {
	if len(sel) == 0 {
		pool := make([]weighted, 0, len(catalogue))
		for _, d := range catalogue {
			pool = append(pool, weighted{def: d, weight: d.Weight})
		}
		return pool
	}
	pool := make([]weighted, 0, len(sel))
	for _, s := range sel {
		def, ok := BuiltIn(s.ID)
		if !ok {
			continue
		}
		w := s.Weight
		if w <= 0 {
			w = def.Weight
		}
		pool = append(pool, weighted{def: def, weight: w})
	}
	if len(pool) == 0 {
		return buildPool(nil)
	}
	return pool
}

// pickPersonality chooses the personality for one agent. A pinned id wins
// outright; otherwise the pool is drawn from by weight, seeded only from the
// diversity seed, the workflow, and the agent index, so the choice replays.
func pickPersonality(pool []weighted, pinned, seed, workflow string, agentIndex int) Definition {
	if pinned != "" {
		if def, ok := BuiltIn(pinned); ok {
			return def
		}
	}
	rng := newSeeded(fmt.Sprintf("%s|pick|%s|%d", seed, workflow, agentIndex))
	var total float64
	for _, w := range pool {
		total += w.weight
	}
	if total <= 0 {
		return pool[0].def
	}
	roll := rng.next() * total
	var cum float64
	for _, w := range pool {
		cum += w.weight
		if roll < cum {
			return w.def
		}
	}
	return pool[len(pool)-1].def
}

// profileFor computes the behavioral profile for one agent, ported from
// generateDiversityProfiles. The rng sequence follows Crowdi's exactly so the
// spread it produces is the same shape; the seed string folds in the workflow
// name so two workflows do not share a profile for the same agent index.
func profileFor(personaID string, i int, seed, workflow string, mix schema.PersonaMixMode, variance schema.BehaviorVariance) Profile {
	rng := newSeeded(fmt.Sprintf("%s|profile|%s|%s|%d", seed, workflow, personaID, i))
	isAggressive := mix == schema.MixAggressive
	isRealistic := mix == schema.MixRealistic

	var strategyPool []string
	switch {
	case isAggressive:
		strategyPool = strategies
	case isRealistic:
		strategyPool = []string{"task_finisher", "explorer", "skeptic"}
	default:
		strategyPool = []string{"task_finisher", "explorer", "skeptic", "speed_runner"}
	}
	strategy := strategyPool[i%len(strategyPool)]

	risk := "balanced"
	switch {
	case isAggressive:
		risk = risks[i%len(risks)]
	case variance == schema.VarianceHigh && rng.probability(0.22):
		if rng.probability(0.55) {
			risk = "conservative"
		} else {
			risk = "risky"
		}
	case variance == schema.VarianceMedium && rng.probability(0.14):
		if rng.probability(0.65) {
			risk = "conservative"
		} else {
			risk = "risky"
		}
	case variance == schema.VarianceLow && rng.probability(0.06):
		risk = "conservative"
	}

	pacing := "medium"
	switch {
	case isAggressive:
		pacing = pacings[(i+1)%len(pacings)]
	case variance == schema.VarianceHigh && rng.probability(0.2):
		if rng.probability(0.5) {
			pacing = "slow"
		} else {
			pacing = "fast"
		}
	case variance == schema.VarianceMedium && rng.probability(0.12):
		if rng.probability(0.65) {
			pacing = "slow"
		} else {
			pacing = "fast"
		}
	}

	var attentionPool []string
	if isAggressive {
		attentionPool = attentions
	} else {
		attentionPool = []string{"cta_focused", "navigation_focused", "text_heavy", "visual_heavy"}
	}
	var attention string
	if isAggressive {
		attention = attentionPool[(i+2)%len(attentionPool)]
	} else {
		attention = attentionPool[rng.intn(len(attentionPool))]
	}

	var errorPool []string
	if isAggressive {
		errorPool = errorStyles
	} else {
		errorPool = []string{"diagnostic", "reroute_fast", "retry_heavy"}
	}
	var errStyle string
	if isAggressive {
		errStyle = errorPool[(i+1)%len(errorPool)]
	} else {
		errStyle = errorPool[rng.intn(len(errorPool))]
	}

	var cognitivePool []string
	if isAggressive {
		cognitivePool = cognitiveSet
	} else {
		cognitivePool = []string{"business_operator", "careful_researcher", "technical_engineer", "gen_z"}
	}
	var cognitive string
	if isAggressive {
		cognitive = cognitivePool[i%len(cognitivePool)]
	} else {
		cognitive = cognitivePool[rng.intn(len(cognitivePool))]
	}

	minN, maxN := noveltyRange(mix, variance)
	novelty := round3(rng.rangeFloat(minN, maxN))

	return Profile{
		PersonaID:          personaID,
		StrategyArchetype:  strategy,
		RiskStyle:          risk,
		PacingStyle:        pacing,
		AttentionBias:      attention,
		ErrorResponseStyle: errStyle,
		CognitiveStyle:     cognitive,
		NoveltyBias:        novelty,
		Seed:               rng.originalSeed(),
		ProfileKey:         fmt.Sprintf("%s:%s:%s:%s:%s:%s:%s", personaID, strategy, risk, pacing, attention, errStyle, cognitive),
	}
}

// noveltyRange ports the noveltyRanges table.
func noveltyRange(mix schema.PersonaMixMode, variance schema.BehaviorVariance) (float64, float64) {
	table := map[schema.BehaviorVariance][2]float64{
		schema.VarianceLow:    {0.24, 0.40},
		schema.VarianceMedium: {0.28, 0.50},
		schema.VarianceHigh:   {0.34, 0.62},
	}
	switch mix {
	case schema.MixAggressive:
		table = map[schema.BehaviorVariance][2]float64{
			schema.VarianceLow:    {0.28, 0.52},
			schema.VarianceMedium: {0.34, 0.70},
			schema.VarianceHigh:   {0.42, 0.86},
		}
	case schema.MixRealistic:
		table = map[schema.BehaviorVariance][2]float64{
			schema.VarianceLow:    {0.22, 0.38},
			schema.VarianceMedium: {0.26, 0.46},
			schema.VarianceHigh:   {0.30, 0.55},
		}
	}
	r, ok := table[variance]
	if !ok {
		r = table[schema.VarianceMedium]
	}
	return r[0], r[1]
}

func round3(f float64) float64 {
	return math.Round(f*1000) / 1000
}

// diagnostics ports buildDiversityDiagnostics.
func diagnostics(profiles []Assignment) Diagnostics {
	if len(profiles) == 0 {
		return Diagnostics{}
	}
	keys := map[string]struct{}{}
	strategySet := map[string]struct{}{}
	personaSet := map[string]struct{}{}
	for _, a := range profiles {
		keys[a.Profile.ProfileKey] = struct{}{}
		strategySet[a.Profile.StrategyArchetype] = struct{}{}
		personaSet[a.Personality.ID] = struct{}{}
	}
	return Diagnostics{
		UniquenessScore: round3(float64(len(keys)) / float64(len(profiles))),
		StrategyCount:   len(strategySet),
		PersonaCount:    len(personaSet),
	}
}
