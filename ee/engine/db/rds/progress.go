// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds

// The two waits this provider spends minutes in, reported while they happen.
//
// A snapshot and a restore are the slow half of every refresh and every branch,
// and until the engine gave providers somewhere to report, a first af up against
// RDS printed nothing for as long as AWS took. The engine calls ReportProgressTo
// once, after opening the provider, and cloudgate forwards it through the
// licence gate.

import (
	"time"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// waitHeartbeat is how often a wait that is still going says so, the cadence
// the other managed providers and the sidecar's image obtain use.
const waitHeartbeat = 15 * time.Second

var _ provider.ProgressReporting = (*Provider)(nil)

// ReportProgressTo implements provider.ProgressReporting.
func (p *Provider) ReportProgressTo(report func(string)) {
	p.progress.Store(&report)
}

// report sends one progress line when the engine gave the provider somewhere to
// send it, and does nothing otherwise.
func (p *Provider) report(line string) {
	if f := p.progress.Load(); f != nil && *f != nil {
		(*f)(line)
	}
}
