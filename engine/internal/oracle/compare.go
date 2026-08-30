package oracle

import "strconv"

// Input is everything one comparison needs, already collected.
//
// A struct of gathered evidence rather than a function that goes and gets it,
// so the comparison itself has no environments, no Docker and no network in
// it. That is what makes the part of this package that decides what counts as a
// difference testable without bringing two applications up, and the part that
// brings two applications up a thin layer over it.
type Input struct {
	Config   Config
	Database DatabaseOptions
	// Probes is what each request returned on each side, in the order sent.
	Probes []ProbeResult
	// BaselineBefore and CandidateBefore are the two databases as they stood
	// after the migrations and before any request. Both nil means the database
	// comparison had nothing to attribute against, and every database finding
	// is reported as the traffic's.
	BaselineBefore  *Snapshot
	CandidateBefore *Snapshot
	// BaselineAfter and CandidateAfter are the two databases once the probes
	// have run. Both nil turns the database comparison off.
	BaselineAfter  *Snapshot
	CandidateAfter *Snapshot
}

// Compare produces the whole result from gathered evidence.
func Compare(in Input) *Result {
	collect := newCollector()
	result := &Result{}

	truncatedProbes := 0
	for i := range in.Probes {
		p := &in.Probes[i]
		findings, truncated := CompareResponses(
			in.Config, collect, i, p.Name, p.Baseline, p.Candidate)
		p.Findings = len(findings)
		result.Findings = append(result.Findings, findings...)
		if truncated {
			truncatedProbes++
		}
	}
	result.Probes = in.Probes
	if truncatedProbes > 0 {
		result.Notes = append(result.Notes, plural(truncatedProbes, "probe", "probes")+
			" produced more differences than are listed; each response is capped at "+
			strconv.Itoa(maxFindingsPerBody)+".")
	}

	if in.BaselineAfter != nil && in.CandidateAfter != nil {
		after, summary := CompareSnapshots(
			in.Config, in.Database, collect, in.BaselineAfter, in.CandidateAfter)
		var before []Finding
		if in.BaselineBefore != nil && in.CandidateBefore != nil {
			before, _ = CompareSnapshots(
				in.Config, in.Database, nil, in.BaselineBefore, in.CandidateBefore)
		}
		result.Findings = append(result.Findings, AttributePhases(before, after)...)
		result.Database = summary
	}

	result.Ignored = collect.Ignored(in.Config)
	result.Sort()
	return result
}
