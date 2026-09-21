package sqlload

import "fmt"

// What a run is judged against, and the two ways judging can go wrong.
//
// A breach is a threshold that was exceeded, which is the case everybody
// expects. The other two are the ones this repository keeps shipping by
// accident and they are named here rather than left to a caller to remember.
//
// A threshold that was in force and measured NOTHING is not a pass. Under a
// declared workload there is no baseline for a statement that has never run,
// so mean_increase would be listed on the report and compared against nothing,
// which reads exactly like a run that compared and found no regression.
//
// A run that committed NO transaction has measured neither throughput nor
// latency. Its TPS is zero, its percentiles are zero, and every threshold it
// carries passes trivially, so the report of a database that refused every
// single transaction is a clean one unless something says otherwise.

// Breach is one threshold that was exceeded.
type Breach struct {
	// What names the threshold and the thing it was measured on.
	What string `json:"what"`
	// Detail is the sentence a person reads.
	Detail    string  `json:"detail"`
	Threshold float64 `json:"threshold"`
	Observed  float64 `json:"observed"`
}

// Breaches reports every threshold this run exceeded.
//
// A threshold of zero is not in force. That is the same reading load.Breaches
// gives, and it matters because the normalizer leaves mean_increase at zero
// under a declared workload on purpose.
func (r *Result) Breaches(meanIncrease, errorRate float64) []Breach {
	var out []Breach
	if r == nil {
		return nil
	}

	if errorRate > 0 && r.attempts() > 0 && r.ErrorRate > errorRate {
		out = append(out, Breach{
			What: "error_rate",
			Detail: fmt.Sprintf("%.1f percent of transaction attempts failed, against a limit of %.1f",
				r.ErrorRate*100, errorRate*100),
			Threshold: errorRate, Observed: r.ErrorRate,
		})
	}

	if meanIncrease > 0 {
		for _, tx := range r.PerTransaction {
			if !tx.Baselines.Has || tx.Executed == 0 {
				continue
			}
			if tx.Baselines.MeanIncrease > meanIncrease {
				out = append(out, Breach{
					What: "mean_increase on " + tx.Name,
					Detail: fmt.Sprintf(
						"the mean is %+.0f percent against the %.3fms the statistics recorded, "+
							"over a limit of %+.0f percent",
						tx.Baselines.MeanIncrease*100, tx.Baselines.MeanMs, meanIncrease*100),
					Threshold: meanIncrease, Observed: tx.Baselines.MeanIncrease,
				})
			}
		}
	}
	return out
}

// InertMeanIncrease reports a mean_increase threshold that was in force and had
// nothing to measure.
//
// Nothing to measure means no transaction both carried a baseline and ran. A
// declared workload carries no baselines at all, and a derived one can still
// reach this if every transaction it took was refused or never picked.
func (r *Result) InertMeanIncrease(threshold float64) bool {
	if r == nil || threshold <= 0 {
		return false
	}
	for _, tx := range r.PerTransaction {
		if tx.Baselines.Has && tx.Executed > 0 {
			return false
		}
	}
	return true
}

// Unverified reports a run that committed no transaction.
//
// It is the single most important line in this file. Every threshold passes
// trivially over an empty measurement, so a database that refused every
// transaction reports zero breaches, and a caller reading breaches alone would
// print a clean run. af test already exits zero on unverified and an entire
// nightly corpus in this repository was green having never once reached an
// agent, which is this shape exactly.
func (r *Result) Unverified() bool {
	return r == nil || r.Transactions == 0
}

// UnverifiedDetail says why in the run's own numbers.
func (r *Result) UnverifiedDetail() string {
	if r == nil {
		return "the run produced no result at all"
	}
	switch {
	case r.attempts() == 0:
		return "no transaction was attempted, so there is nothing to measure"
	case r.ClientsStopped >= r.Clients && r.Clients > 0:
		return fmt.Sprintf("every one of the %d clients stopped before committing anything", r.Clients)
	default:
		return fmt.Sprintf("all %d transaction attempts failed, so there is neither a "+
			"throughput nor a latency to report", r.attempts())
	}
}

// attempts is commits plus failures, which is the denominator of the error
// rate. Recomputed from the result rather than carried, so a document read back
// from a control plane answers the same way a fresh one does.
func (r *Result) attempts() int {
	return r.Transactions + r.TransactionsFailed
}
