package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
)

// newReadSecurityFindingsTool builds read_security_findings.
//
// It is a filtered, family-grouped projection over the findings ALREADY in the
// run store, the same store get_rehearsal_run reads. It creates no new data
// path and it runs nothing: it reads a finished run, keeps the findings whose
// rule is in the security namespace, and returns them grouped by family. Where
// a general purpose reviewer would return the offending body, this returns a
// location and a bounded description and never the value, because these
// findings come from a copy of production and the value must not leave the run.
func newReadSecurityFindingsTool(p *Project, store *Store) *Tool {
	return &Tool{
		Name:     "read_security_findings",
		Title:    "Read security findings",
		ReadOnly: true,
		Description: "Read the security findings a rehearsal produced, grouped by family " +
			"and filterable by family, level and location. Each finding carries a rule, a " +
			"level, a title, a bounded description, a fix and a location, and NEVER the " +
			"offending request body, response or row: those stay in the copy of production " +
			"the run drove. Give a run_id, or omit it for the latest finished run. The loop " +
			"is: read a finding's rule, fix and where, change the code, re-run the rehearsal, " +
			"and read again, rather than scraping the pull request comment.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id": projectIDSchema(),
				"run_id": {
					Type: "string", MaxLength: 64, MinLength: 8, Pattern: `run_[0-9a-f]{32}`,
					Description: "Optional. The run to read. Omit it for the latest finished " +
						"run this client submitted for this project. A run belonging to another " +
						"project or client reads as not found.",
				},
				"family": {
					Type: "string", MaxLength: 40,
					Enum: []string{
						"authz", "injection", "ssrf", "side_effect", "canary_leak",
						"db_security", "headers", "supply_chain", "secret_exposure",
					},
					Description: "Optional. Return only findings from this security family, " +
						"which is the segment after security. in a rule: security.authz.idor " +
						"is the authz family.",
				},
				"level": {
					Type: "string", MaxLength: 8, Enum: []string{"fail", "warn"},
					Description: "Optional. Return only findings at this level. Findings at " +
						"ignore are never returned, so the two values are fail and warn.",
				},
				"target_ref": {
					Type: "string", MaxLength: 400,
					Description: "Optional. Return only findings at this location, matched " +
						"against the finding's where: a route, a table or a header name.",
				},
				"cursor": {
					Type: "string", MaxLength: 256,
					Description: "Optional. The cursor from a previous response, to read the " +
						"next page of findings.",
				},
			},
		},
		Handler: func(ctx context.Context, call *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			id, _ := args["run_id"].(string)
			family, _ := args["family"].(string)
			level, _ := args["level"].(string)
			targetRef, _ := args["target_ref"].(string)
			cursor, _ := args["cursor"].(string)

			run, fault := resolveRun(ctx, store, call.Caller, p.ID, id)
			if fault != nil {
				return nil, fault
			}
			return securityProjection(run, family, level, targetRef, cursor)
		},
	}
}

// resolveRun reads the named run, or the latest finished one when no id is
// given. A missing default is reported as plainly as a missing named run, so a
// caller that has run nothing yet is told so rather than handed an empty page
// it might read as a clean bill of health.
func resolveRun(ctx context.Context, store *Store, caller, project, id string) (Run, *Fault) {
	if id != "" {
		return store.Get(ctx, caller, project, id)
	}
	run, found, fault := store.LatestFinished(ctx, caller, project)
	if fault != nil {
		return Run{}, fault
	}
	if !found {
		return Run{}, fieldFault(FaultRunNotFound, "run_id",
			"No finished run to read. Submit a rehearsal and poll it to a verdict, then "+
				"read its security findings, or name a run_id.")
	}
	return run, nil
}

// securityFinding is one finding in the projection.
//
// Family is added beside the rule so a caller can group without parsing the
// rule, and target_ref mirrors where so a caller filtering by one field reads
// the value it filtered on. There is no field here for a body, a row or a
// response, and that absence is the contract, not an omission.
type securityFinding struct {
	Rule      string `json:"rule"`
	Family    string `json:"family"`
	Level     string `json:"level"`
	Title     string `json:"title"`
	Detail    string `json:"detail,omitempty"`
	Fix       string `json:"fix,omitempty"`
	Where     string `json:"where,omitempty"`
	TargetRef string `json:"target_ref,omitempty"`
	Count     int    `json:"count,omitempty"`
}

// securityProjection filters a run's findings to the security namespace, groups
// them, and returns one page.
func securityProjection(run Run, family, level, targetRef, cursor string) (any, *Fault) {
	summary, all := securityFindingsOf(run)

	kept := make([]securityFinding, 0, len(all))
	totals := map[string]int{"fail": 0, "warn": 0, "ignore": 0}
	byFamily := map[string]map[string]int{}
	for _, f := range all {
		fam := security.FamilyOf(f.Rule)
		// A finding at ignore is dropped, never returned: the manifest turned
		// it off, and returning it would put back exactly what was silenced.
		if f.Level == string(report.LevelIgnore) || f.Level == "" {
			continue
		}
		if family != "" && fam != family {
			continue
		}
		if level != "" && f.Level != level {
			continue
		}
		if targetRef != "" && f.Where != targetRef {
			continue
		}
		totals[f.Level]++
		if byFamily[fam] == nil {
			byFamily[fam] = map[string]int{"fail": 0, "warn": 0}
		}
		byFamily[fam][f.Level]++
		kept = append(kept, securityFinding{
			Rule: f.Rule, Family: fam, Level: f.Level, Title: f.Title,
			Detail: f.Detail, Fix: f.Fix, Where: f.Where, TargetRef: f.Where,
			Count: f.Count,
		})
	}
	// Worst first, then stable, so paging is deterministic and the first page
	// holds the findings that decide the verdict.
	sort.SliceStable(kept, func(i, j int) bool {
		return levelRank(kept[i].Level) < levelRank(kept[j].Level)
	})

	offset := 0
	if cursor != "" {
		var fault *Fault
		offset, fault = decodeCursor(run.ID, cursor)
		if fault != nil {
			return nil, fault
		}
	}
	if offset > len(kept) {
		offset = len(kept)
	}
	end := offset + maxFindings
	if end > len(kept) {
		end = len(kept)
	}
	page := append([]securityFinding{}, kept[offset:end]...)

	verdict := run.Verdict
	if verdict == "" {
		verdict = VerdictInconclusive
	}
	out := map[string]any{
		"kind":           "security_findings",
		"run_id":         run.ID,
		"verdict":        verdict,
		"native_verdict": run.NativeVerdict,
		"summary":        securitySummary(summary, len(kept), totals),
		"totals":         totals,
		"by_family":      byFamily,
		"findings":       page,
		"truncated":      end < len(kept),
		"cursor":         nil,
		"boundary_note": "Findings are reduced to a rule, a level, a location and a bounded " +
			"description. The offending request bodies, responses and rows are never " +
			"returned; they live in a copy of production.",
	}
	if end < len(kept) {
		out["cursor"] = encodeCursor(run.ID, end)
	}
	return out, nil
}

// securityFindingsOf reads a run's stored result and returns its summary and
// the findings whose rule is in the security namespace.
//
// A run with no stored result, one still in flight or one that failed, has no
// findings to project, which is not an error: the projection is empty and the
// summary says why the run reached no verdict.
func securityFindingsOf(run Run) (summary string, out []Finding) {
	if len(run.Result) == 0 {
		return summaryForIncomplete(run), nil
	}
	var stored storedResult
	if err := json.Unmarshal(run.Result, &stored); err != nil {
		// A stored result that will not decode is our problem, not the
		// caller's, but there is nothing to project, so it reads as empty with
		// the run's own summary rather than as a fault that hides the verdict.
		return "", nil
	}
	for _, f := range stored.Findings.Items {
		if strings.HasPrefix(f.Rule, security.Prefix()) {
			out = append(out, f)
		}
	}
	return stored.Summary, out
}

// securitySummary is one to three sentences a person could read aloud.
func securitySummary(runSummary string, shown int, totals map[string]int) string {
	if shown == 0 {
		if runSummary != "" {
			return "No security findings. " + runSummary
		}
		return "No security findings in this run."
	}
	return fmt.Sprintf("%s: %s, %s.",
		plural(shown, "security finding", "security findings"),
		plural(totals["fail"], "fail", "fail"),
		plural(totals["warn"], "warn", "warn"))
}
