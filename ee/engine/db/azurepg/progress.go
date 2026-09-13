// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package azurepg

import "time"

// restoreWaitHeartbeat is how often a restore that is still waiting for a first
// backup says so, the same cadence the sidecar's image obtain uses.
const restoreWaitHeartbeat = 15 * time.Second

// ReportProgressTo implements provider.ProgressReporting. The engine calls it
// once, after opening the provider.
func (p *Provider) ReportProgressTo(report func(string)) {
	p.progress.Store(&report)
}

// report sends one progress line when the engine gave the provider somewhere
// to send it, and does nothing otherwise.
func (p *Provider) report(line string) {
	if f := p.progress.Load(); f != nil && *f != nil {
		(*f)(line)
	}
}
